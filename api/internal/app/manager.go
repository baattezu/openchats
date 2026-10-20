package app

import (
	"context"
	"encoding/json"
	"errors"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"log/slog"
	"stepline/messaging-service/internal/provider"
	"stepline/messaging-service/internal/store"
	"strconv"
	"sync"
	"time"
)

type live struct {
	adapter provider.Adapter
	cancel  context.CancelFunc
	done    chan struct{}
}
type Manager struct {
	ctx      context.Context
	s        *store.Store
	cfg      Config
	mu       sync.Mutex
	items    map[string]*live
	wa       *sqlstore.Container
	mediaURL func(string) string
}

func NewManager(ctx context.Context, s *store.Store, c Config) *Manager {
	return &Manager{ctx: ctx, s: s, cfg: c, items: map[string]*live{}}
}
func (m *Manager) status(c provider.Connection, status, detail, identity string) {
	tag, e := m.s.DB.Exec(m.ctx, `UPDATE connections SET status=$2,detail=$3,identity=CASE WHEN $4='' THEN identity ELSE $4 END WHERE id=$1 AND status NOT IN ('paused','disconnected')`, c.ID, status, detail, identity)
	if e != nil {
		slog.Error("connection status persistence failed", "connection_id", c.ID)
		return
	}
	if tag.RowsAffected() > 0 {
		_ = m.s.Emit(m.ctx, c.WorkspaceID, "connection.status", map[string]any{"id": c.ID, "status": status, "detail": detail, "identity": identity})
	}
}
func (m *Manager) Start() error {
	if m.cfg.EnableUnofficial {
		w, e := provider.NewWAStore(m.ctx, m.cfg.DSN, m.s.Vault)
		if e != nil {
			return e
		}
		m.wa = w
	}
	rows, e := m.s.DB.Query(m.ctx, `SELECT id,workspace_id FROM connections WHERE status NOT IN ('disconnected','paused')`)
	if e != nil {
		return e
	}
	var ids [][2]string
	for rows.Next() {
		var v [2]string
		if e = rows.Scan(&v[0], &v[1]); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range ids {
		if e = m.Connect(v[0], v[1]); e != nil {
			slog.Warn("connection could not start", "connection_id", v[0])
		}
	}
	return nil
}
func (m *Manager) Connect(id, w string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[id]; ok {
		return nil
	}
	c, e := m.s.Connection(m.ctx, id, w)
	if e != nil {
		return e
	}
	creds, e := m.s.Credentials(m.ctx, id)
	if e != nil {
		return e
	}
	cb := provider.Callbacks{Event: func(ctx context.Context, v provider.Event) error { return m.s.Ingest(ctx, c, v) }, Status: func(s, d, i string) { m.status(c, s, d, i) }, SaveSession: func(b []byte) error { return m.s.SaveBlob(m.ctx, id, "session_data", b) }, LoadSession: func() ([]byte, error) { return m.s.LoadBlob(m.ctx, id, "session_data") }, SaveCheckpoint: func(b []byte) error { return m.s.SaveBlob(m.ctx, id, "checkpoint", b) }, LoadCheckpoint: func() ([]byte, error) { return m.s.LoadBlob(m.ctx, id, "checkpoint") }}
	var adapter provider.Adapter
	ctx, cancel := context.WithCancel(m.ctx)
	switch c.Provider {
	case "whatsapp_web":
		if m.wa == nil {
			cancel()
			return bad("unofficial WhatsApp is disabled by the operator")
		}
		b, e := cb.LoadSession()
		if e != nil {
			cancel()
			return e
		}
		adapter, e = provider.NewWhatsApp(ctx, m.wa, string(b), cb)
		if e != nil {
			cancel()
			return e
		}
	case "telegram":
		apiID := m.cfg.TelegramID
		hash := m.cfg.TelegramHash
		if creds["api_id"] != "" {
			apiID = creds["api_id"]
		}
		if creds["api_hash"] != "" {
			hash = creds["api_hash"]
		}
		n, _ := strconv.Atoi(apiID)
		if n <= 0 || hash == "" {
			cancel()
			return bad("configure TELEGRAM_API_ID and TELEGRAM_API_HASH or provide your credentials")
		}
		adapter, e = provider.NewTelegram(n, hash, cb)
		if e != nil {
			cancel()
			return e
		}
	case "whatsapp_cloud", "instagram":
		adapter = &provider.Meta{Provider: c.Provider, ID: creds["account_id"], Token: creds["access_token"], Version: m.cfg.MetaVersion, CB: cb, PublicMedia: m.mediaURL}
	default:
		cancel()
		return bad("unknown provider")
	}
	l := &live{adapter: adapter, cancel: cancel, done: make(chan struct{})}
	m.items[id] = l
	m.status(c, "connecting", "Starting connection", "")
	go func() {
		defer close(l.done)
		e := adapter.Start(ctx)
		if e != nil && !errors.Is(e, context.Canceled) {
			m.status(c, "error", "Connection stopped; reconnect or check configuration", "")
			slog.Warn("provider stopped", "connection_id", id)
		}
		m.mu.Lock()
		if m.items[id] == l {
			delete(m.items, id)
		}
		m.mu.Unlock()
	}()
	return nil
}
func (m *Manager) Adapter(id string) (provider.Adapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.items[id]
	if l == nil {
		return nil, bad("connection is not running; reconnect first")
	}
	return l.adapter, nil
}
func (m *Manager) Stop(ctx context.Context, id string, revoke bool) error {
	m.mu.Lock()
	l := m.items[id]
	m.mu.Unlock()
	if l == nil {
		return nil
	}
	var e error
	if revoke {
		e = l.adapter.Disconnect(ctx)
		if e != nil {
			return e
		}
	}
	l.cancel()
	select {
	case <-l.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	for _, l := range m.items {
		l.cancel()
	}
	m.mu.Unlock()
	if m.wa != nil {
		_ = m.wa.Close()
	}
}
func (m *Manager) Meta(id string) (*provider.Meta, error) {
	c, e := m.s.Connection(m.ctx, id, store.Workspace)
	if e != nil {
		return nil, e
	}
	creds, e := m.s.Credentials(m.ctx, id)
	if e != nil {
		return nil, e
	}
	return &provider.Meta{Provider: c.Provider, ID: creds["account_id"], Token: creds["access_token"], Version: m.cfg.MetaVersion, PublicMedia: m.mediaURL}, nil
}
func (m *Manager) refreshTokens(ctx context.Context) {
	rows, e := m.s.DB.Query(ctx, `SELECT id FROM connections WHERE provider='instagram' AND credentials IS NOT NULL AND status='connected'`)
	if e != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		creds, e := m.s.Credentials(ctx, id)
		if e != nil {
			continue
		}
		exp, _ := time.Parse(time.RFC3339, creds["expires_at"])
		if exp.IsZero() || time.Until(exp) > 7*24*time.Hour {
			continue
		}
		a, e := m.Meta(id)
		if e != nil {
			continue
		}
		var out struct {
			Token   string `json:"access_token"`
			Expires int    `json:"expires_in"`
		}
		if e = a.RefreshInstagram(ctx, &out); e != nil {
			c, _ := m.s.Connection(ctx, id, store.Workspace)
			m.status(c, "needs_auth", "Instagram token refresh failed; reconnect", "")
			continue
		}
		creds["access_token"] = out.Token
		creds["expires_at"] = time.Now().Add(time.Duration(out.Expires) * time.Second).Format(time.RFC3339)
		b, _ := json.Marshal(creds)
		if m.s.SaveBlob(ctx, id, "credentials", b) == nil {
			_ = m.Stop(ctx, id, false)
			_ = m.Connect(id, store.Workspace)
		}
	}
}
