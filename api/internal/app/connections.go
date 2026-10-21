package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"stepline/messaging-service/internal/provider"
	"stepline/messaging-service/internal/secure"
	"strings"
	"time"
)

func (a *Server) connections(w http.ResponseWriter, r *http.Request, p Principal) error {
	rows, e := a.Store.DB.Query(r.Context(), `SELECT id FROM connections WHERE workspace_id=$1 ORDER BY created_at`, p.Workspace)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	out := []provider.Connection{}
	for _, id := range ids {
		c, e := a.Store.Connection(r.Context(), id, p.Workspace)
		if e != nil {
			return e
		}
		out = append(out, c)
	}
	write(w, 200, out)
	return nil
}
func (a *Server) createConnection(w http.ResponseWriter, r *http.Request, p Principal) error {
	var in struct {
		Provider     string            `json:"provider"`
		Label        string            `json:"label"`
		RiskAccepted bool              `json:"risk_accepted"`
		Credentials  map[string]string `json:"credentials"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.Provider != "telegram" && in.Provider != "instagram" && in.Provider != "whatsapp_cloud" && in.Provider != "whatsapp_web" {
		return bad("unknown provider")
	}
	if strings.TrimSpace(in.Label) == "" || len(in.Label) > 100 {
		return bad("label of 1–100 bytes required")
	}
	if in.Provider == "whatsapp_web" && (!in.RiskAccepted || !a.Config.EnableUnofficial) {
		return bad("unofficial WhatsApp requires operator enablement and risk acceptance")
	}
	if in.Provider == "telegram" && a.Config.TelegramID == "" && in.Credentials["api_id"] == "" {
		return bad("Telegram API credentials required")
	}
	if in.Provider == "instagram" && (a.Config.InstagramID == "" || a.Config.InstagramSecret == "") {
		return bad("Meta app is not configured")
	}
	if in.Provider == "whatsapp_cloud" && (a.Config.MetaID == "" || a.Config.MetaSecret == "" || a.Config.MetaConfig == "") {
		return bad("Meta Embedded Signup is not configured")
	}
	id := secure.ID()
	if _, e := a.Store.DB.Exec(r.Context(), `INSERT INTO connections(id,workspace_id,provider,label,status) VALUES($1,$2,$3,$4,'needs_auth')`, id, p.Workspace, in.Provider, in.Label); e != nil {
		return e
	}
	if in.Credentials != nil {
		b, _ := json.Marshal(in.Credentials)
		if e := a.Store.SaveBlob(r.Context(), id, "credentials", b); e != nil {
			return e
		}
	}
	a.Store.Audit(r.Context(), p.Workspace, p.ID, "connection.created", id)
	if in.Provider == "telegram" || in.Provider == "whatsapp_web" {
		if e := a.Manager.Connect(id, p.Workspace); e != nil {
			_, _ = a.Store.DB.Exec(r.Context(), `UPDATE connections SET status='error',detail='Connection setup failed' WHERE id=$1`, id)
			return e
		}
	}
	c, e := a.Store.Connection(r.Context(), id, p.Workspace)
	if e != nil {
		return e
	}
	write(w, 201, c)
	return nil
}
func (a *Server) challenge(w http.ResponseWriter, r *http.Request, p Principal) error {
	id := r.PathValue("id")
	c, e := a.Store.Connection(r.Context(), id, p.Workspace)
	if e != nil {
		return e
	}
	var in map[string]string
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if in["action"] == "pause" {
		if _, e = a.Store.DB.Exec(r.Context(), `UPDATE connections SET status='paused',detail='Paused by administrator' WHERE id=$1`, id); e != nil {
			return e
		}
		if e = a.Manager.Stop(r.Context(), id, false); e != nil {
			return e
		}
		write(w, 200, map[string]string{"step": "paused"})
		return nil
	}
	if in["action"] == "reconnect" {
		_ = a.Manager.Stop(r.Context(), id, false)
		_, _ = a.Store.DB.Exec(r.Context(), `UPDATE connections SET status='needs_auth' WHERE id=$1`, id)
		if e = a.Manager.Connect(id, p.Workspace); e != nil {
			return e
		}
	}
	adapter, e := a.Manager.Adapter(id)
	if e != nil {
		return e
	}
	ctx, cancel := timeContext(r, 30*time.Second)
	defer cancel()
	v, e := adapter.Challenge(ctx, in)
	if e != nil {
		a.Store.Audit(r.Context(), p.Workspace, p.ID, "connection.challenge_failed", c.ID)
		return bad(e.Error())
	}
	write(w, 200, v)
	return nil
}
func (a *Server) disconnect(w http.ResponseWriter, r *http.Request, p Principal) error {
	id := r.PathValue("id")
	c, e := a.Store.Connection(r.Context(), id, p.Workspace)
	if e != nil {
		return e
	}
	if c.Provider == "telegram" || c.Provider == "whatsapp_web" {
		if _, err := a.Manager.Adapter(id); err != nil {
			b, loadErr := a.Store.LoadBlob(r.Context(), id, "session_data")
			if loadErr != nil {
				return loadErr
			}
			if len(b) > 0 {
				return bad("reconnect this account before disconnecting so its remote session can be revoked")
			}
		}
	}
	if c.Provider == "whatsapp_cloud" || c.Provider == "instagram" {
		m, e := a.Manager.Meta(id)
		if e != nil {
			return e
		}
		path := m.ID + "/subscribed_apps"
		if c.Provider == "whatsapp_cloud" {
			creds, e := a.Store.Credentials(r.Context(), id)
			if e != nil {
				return e
			}
			path = creds["waba_id"] + "/subscribed_apps"
		}
		if m.Token != "" {
			if e = m.Request(r.Context(), "DELETE", path, nil, nil); e != nil {
				return bad("Meta subscription could not be revoked; retry or revoke access in Meta")
			}
		}
	}
	if e = a.Manager.Stop(r.Context(), id, true); e != nil {
		return bad("provider logout failed; retry or revoke the session in the official app")
	}
	_, e = a.Store.DB.Exec(r.Context(), `UPDATE connections SET credentials=NULL,session_data=NULL,checkpoint=NULL,external_id=NULL,identity='',status='disconnected',detail='Disconnected' WHERE id=$1`, id)
	if e != nil {
		return e
	}
	if c.Provider == guardedProvider && c.Identity != "" {
		// Отвязали подключённый номер — новая привязка WhatsApp не раньше чем через
		// WA_RELINK_COOLDOWN_HOURS: «отвязал и сразу привязал заново» выглядит для
		// WhatsApp как перехват аккаунта. Отмена привязки до скана QR (identity ещё
		// пустой) — не отвязка: устройство так и не появилось у WhatsApp.
		a.Manager.blockPairing(r.Context(), p.Workspace, provider.UseRelinkCooldown, "unlinked by administrator")
	}
	a.Store.Audit(r.Context(), p.Workspace, p.ID, "connection.disconnected", id)
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) deleteConnection(w http.ResponseWriter, r *http.Request, p Principal) error {
	id := r.PathValue("id")
	c, e := a.Store.Connection(r.Context(), id, p.Workspace)
	if e != nil {
		return e
	}
	if c.Status != "disconnected" {
		return bad("disconnect first to revoke the provider session")
	}
	rows, e := a.Store.DB.Query(r.Context(), `SELECT path FROM media WHERE connection_id=$1`, id)
	if e != nil {
		return e
	}
	var paths []string
	for rows.Next() {
		var path string
		if e = rows.Scan(&path); e != nil {
			rows.Close()
			return e
		}
		paths = append(paths, path)
	}
	rows.Close()
	tx, e := a.Store.DB.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	if _, e = tx.Exec(r.Context(), `DELETE FROM jobs WHERE workspace_id=$1 AND payload->>'connection_id'=$2`, p.Workspace, id); e != nil {
		return e
	}
	if _, e = tx.Exec(r.Context(), `DELETE FROM events WHERE workspace_id=$1 AND data->>'connection_id'=$2`, p.Workspace, id); e != nil {
		return e
	}
	if _, e = tx.Exec(r.Context(), `DELETE FROM connections WHERE id=$1`, id); e != nil {
		return e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	for _, path := range paths {
		_ = os.Remove(filepath.Join(a.Store.MediaDir, path))
	}
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) importHistory(w http.ResponseWriter, r *http.Request, p Principal) error {
	c, e := a.Store.Connection(r.Context(), r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	if !c.Capabilities.History {
		return bad("manual import is unsupported for this provider")
	}
	payload, _ := json.Marshal(map[string]string{"connection_id": c.ID})
	_, e = a.Store.DB.Exec(r.Context(), `INSERT INTO jobs(id,workspace_id,kind,payload) VALUES($1,$2,'import',$3)`, secure.ID(), p.Workspace, payload)
	if e != nil {
		return e
	}
	write(w, 202, map[string]string{"status": "queued"})
	return nil
}
func (a *Server) templates(w http.ResponseWriter, r *http.Request, p Principal) error {
	c, e := a.Store.Connection(r.Context(), r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	if c.Provider != "whatsapp_cloud" {
		return bad("templates are available only on official WhatsApp")
	}
	creds, e := a.Store.Credentials(r.Context(), c.ID)
	if e != nil {
		return e
	}
	m, e := a.Manager.Meta(c.ID)
	if e != nil {
		return e
	}
	var out json.RawMessage
	if e = m.Request(r.Context(), "GET", creds["waba_id"]+"/message_templates?limit=100", nil, &out); e != nil {
		return bad(e.Error())
	}
	write(w, 200, out)
	return nil
}
