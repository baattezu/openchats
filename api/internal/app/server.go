package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"log/slog"
	"net/http"
	"stepline/messaging-service/internal/provider"
	"stepline/messaging-service/internal/secure"
	"stepline/messaging-service/internal/store"
	"strings"
	"sync"
	"time"
)

type Principal struct {
	ID, Workspace, Role, Name, Email string
	API                              bool
}
type Server struct {
	Store   *store.Store
	Config  Config
	Manager *Manager
	mu      sync.Mutex
	logins  map[string][]time.Time
	ctx     context.Context
}
type handler func(http.ResponseWriter, *http.Request, Principal) error
type problem struct {
	code    int
	message string
}

func (p problem) Error() string { return p.message }
func bad(s string) error        { return problem{400, s} }
func missing() error            { return problem{404, "resource not found"} }
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return bad("invalid JSON body")
	}
	var extra any
	if e := d.Decode(&extra); e == io.EOF {
		return nil
	}
	return bad("only one JSON object is allowed")
}
func New(ctx context.Context, s *store.Store, c Config) *Server {
	a := &Server{Store: s, Config: c, ctx: ctx, logins: map[string][]time.Time{}}
	a.Manager = NewManager(ctx, s, c)
	a.Manager.mediaURL = a.mediaURL
	return a
}
func (a *Server) Router() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
		defer c()
		if e := a.Store.DB.Ping(ctx); e != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ok"})
	})
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if a.Store.DB.Ping(r.Context()) != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ready"})
	})
	m.HandleFunc("POST /api/v1/auth/login", a.wrap(a.login, false, false))
	m.HandleFunc("POST /api/v1/auth/accept-invitation", a.wrap(a.accept, false, false))
	m.HandleFunc("POST /api/v1/auth/logout", a.wrap(a.logout, true, false))
	m.HandleFunc("GET /api/v1/me", a.wrap(a.me, true, false))
	m.HandleFunc("GET /api/v1/members", a.wrap(a.members, true, true))
	m.HandleFunc("POST /api/v1/invitations", a.wrap(a.invite, true, true))
	m.HandleFunc("DELETE /api/v1/members/{id}", a.wrap(a.removeMember, true, true))
	m.HandleFunc("GET /api/v1/connections", a.wrap(a.connections, true, false))
	m.HandleFunc("POST /api/v1/connections", a.wrap(a.createConnection, true, true))
	m.HandleFunc("POST /api/v1/connections/{id}/challenge", a.wrap(a.challenge, true, true))
	m.HandleFunc("POST /api/v1/connections/{id}/disconnect", a.wrap(a.disconnect, true, true))
	m.HandleFunc("DELETE /api/v1/connections/{id}", a.wrap(a.deleteConnection, true, true))
	m.HandleFunc("POST /api/v1/connections/{id}/import", a.wrap(a.importHistory, true, true))
	m.HandleFunc("GET /api/v1/connections/{id}/templates", a.wrap(a.templates, true, false))
	m.HandleFunc("GET /api/v1/conversations", a.wrap(a.conversations, true, false))
	m.HandleFunc("GET /api/v1/conversations/{id}/messages", a.wrap(a.messages, true, false))
	m.HandleFunc("POST /api/v1/conversations/{id}/read", a.wrap(a.read, true, false))
	m.HandleFunc("POST /api/v1/conversations/{id}/messages", a.wrap(a.send, true, false))
	m.HandleFunc("PATCH /api/v1/messages/{id}", a.wrap(a.editMessage, true, false))
	m.HandleFunc("GET /api/v1/messages/{id}", a.wrap(a.message, true, false))
	m.HandleFunc("DELETE /api/v1/messages/{id}", a.wrap(a.deleteMessage, true, false))
	m.HandleFunc("POST /api/v1/media", a.wrap(a.upload, true, false))
	m.HandleFunc("GET /api/v1/media/{id}", a.wrap(a.media, true, false))
	m.HandleFunc("DELETE /api/v1/media/{id}", a.wrap(a.deleteMedia, true, false))
	m.HandleFunc("GET /api/v1/events", a.wrap(a.events, true, false))
	m.HandleFunc("GET /api/v1/api-keys", a.wrap(a.keys, true, true))
	m.HandleFunc("POST /api/v1/api-keys", a.wrap(a.createKey, true, true))
	m.HandleFunc("DELETE /api/v1/api-keys/{id}", a.wrap(a.deleteKey, true, true))
	m.HandleFunc("GET /api/v1/webhooks", a.wrap(a.webhooks, true, true))
	m.HandleFunc("POST /api/v1/webhooks", a.wrap(a.createWebhook, true, true))
	m.HandleFunc("DELETE /api/v1/webhooks/{id}", a.wrap(a.deleteWebhook, true, true))
	m.HandleFunc("GET /api/v1/webhook-deliveries", a.wrap(a.deliveries, true, true))
	m.HandleFunc("GET /api/v1/connections/{id}/oauth", a.wrap(a.oauthStart, true, true))
	m.HandleFunc("POST /api/v1/connections/{id}/embedded-signup", a.wrap(a.embeddedSignup, true, true))
	m.HandleFunc("GET /api/v1/oauth/instagram/callback", a.wrap(a.oauthCallback, true, false))
	m.HandleFunc("GET /api/v1/provider-webhooks/meta", a.wrap(a.verifyMeta, false, false))
	m.HandleFunc("POST /api/v1/provider-webhooks/meta", a.wrap(a.metaWebhook, false, false))
	m.HandleFunc("GET /api/v1/provider-media/{id}", a.wrap(a.providerMedia, false, false))
	m.HandleFunc("GET /api/v1/jobs", a.wrap(a.jobs, true, true))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if v := recover(); v != nil {
				slog.Error("request panic", "path", r.URL.Path)
				write(w, 500, map[string]string{"error": "internal error"})
			}
		}()
		m.ServeHTTP(w, r)
	})
}
func (a *Server) wrap(h handler, auth, admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := Principal{}
		var e error
		if auth {
			p, e = a.principal(r)
			if e == nil && admin && (p.API || p.Role != "admin") {
				e = problem{403, "administrator access required"}
			}
		}
		if e == nil && r.Method != "GET" && r.Method != "HEAD" && !p.API && strings.HasPrefix(r.URL.Path, "/api/v1/") && !strings.Contains(r.URL.Path, "provider-webhooks") {
			if r.Header.Get("Origin") != a.Config.Origin {
				e = problem{403, "origin check failed"}
			}
		}
		if e == nil {
			for _, value := range []string{r.PathValue("id"), r.URL.Query().Get("before"), r.URL.Query().Get("connection_id")} {
				if value != "" {
					if _, err := uuid.Parse(value); err != nil {
						e = bad("invalid resource UUID")
						break
					}
				}
			}
		}
		if e == nil {
			e = h(w, r, p)
		}
		if e != nil {
			code := 500
			message := "internal error"
			var pe problem
			if errors.As(e, &pe) {
				code = pe.code
				message = pe.message
			} else if errors.Is(e, pgx.ErrNoRows) {
				code = 404
				message = "resource not found"
			} else {
				slog.Error("request failed", "path", r.URL.Path, "error_type", fmt.Sprintf("%T", e))
			}
			write(w, code, map[string]string{"error": message})
		}
	}
}
func (a *Server) principal(r *http.Request) (Principal, error) {
	var p Principal
	ctx := r.Context()
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		p.API = true
		p.Role = "member"
		e := a.Store.DB.QueryRow(ctx, `SELECT id,workspace_id,name FROM api_keys WHERE token_hash=$1`, secure.Hash(strings.TrimPrefix(auth, "Bearer "))).Scan(&p.ID, &p.Workspace, &p.Name)
		if e != nil {
			return p, problem{401, "invalid API key"}
		}
		return p, nil
	}
	cookie, e := r.Cookie("ms_session")
	if e != nil {
		return p, problem{401, "login required"}
	}
	e = a.Store.DB.QueryRow(ctx, `SELECT u.id,u.workspace_id,u.role,u.name,u.email FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now()`, secure.Hash(cookie.Value)).Scan(&p.ID, &p.Workspace, &p.Role, &p.Name, &p.Email)
	if e != nil {
		return p, problem{401, "session expired"}
	}
	return p, nil
}
func (a *Server) callbacks(c provider.Connection) provider.Callbacks {
	return provider.Callbacks{Event: func(ctx context.Context, v provider.Event) error { return a.Store.Ingest(ctx, c, v) }, Status: func(status, detail, identity string) { a.Manager.status(c, status, detail, identity) }, SaveSession: func(b []byte) error { return a.Store.SaveBlob(a.ctx, c.ID, "session_data", b) }, LoadSession: func() ([]byte, error) { return a.Store.LoadBlob(a.ctx, c.ID, "session_data") }}
}
