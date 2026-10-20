package app

import (
	"context"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"stepline/messaging-service/internal/secure"
	"stepline/messaging-service/internal/store"
	"strings"
	"time"
)

func (a *Server) rateLogin(r *http.Request, subject string) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	host += ":" + secure.Hash(subject)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	xs := a.logins[host]
	out := xs[:0]
	for _, t := range xs {
		if now.Sub(t) < 15*time.Minute {
			out = append(out, t)
		}
	}
	if len(out) >= 10 {
		a.logins[host] = out
		return false
	}
	a.logins[host] = append(out, now)
	if len(a.logins) > 10000 {
		for k, v := range a.logins {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > 15*time.Minute {
				delete(a.logins, k)
			}
		}
	}
	return true
}
func (a *Server) resetLogin(r *http.Request, subject string) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	delete(a.logins, host+":"+secure.Hash(subject))
	a.mu.Unlock()
}
func (a *Server) session(w http.ResponseWriter, r *http.Request, id string) error {
	t := secure.Token()
	_, e := a.Store.DB.Exec(r.Context(), `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, secure.Hash(t), id, time.Now().Add(24*time.Hour))
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: "ms_session", Value: t, Path: "/", HttpOnly: true, Secure: a.Config.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	return nil
}
func (a *Server) login(w http.ResponseWriter, r *http.Request, p Principal) error {

	var in struct{ Email, Password string }
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if !a.rateLogin(r, strings.ToLower(strings.TrimSpace(in.Email))) {
		return problem{429, "too many login attempts; try later"}
	}
	var id, hash string
	e := a.Store.DB.QueryRow(r.Context(), `SELECT id,password_hash FROM users WHERE email=$1`, strings.ToLower(strings.TrimSpace(in.Email))).Scan(&id, &hash)
	if e != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		return problem{401, "invalid email or password"}
	}
	if e = a.session(w, r, id); e != nil {
		return e
	}
	a.resetLogin(r, strings.ToLower(strings.TrimSpace(in.Email)))
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) logout(w http.ResponseWriter, r *http.Request, p Principal) error {
	if p.API {
		return bad("API keys cannot log out")
	}
	if c, e := r.Cookie("ms_session"); e == nil {
		_, _ = a.Store.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, secure.Hash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "ms_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.Config.SecureCookie, SameSite: http.SameSiteLaxMode})
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) me(w http.ResponseWriter, r *http.Request, p Principal) error {
	write(w, 200, map[string]any{"id": p.ID, "name": p.Name, "email": p.Email, "role": p.Role, "workspace_id": p.Workspace, "api": p.API, "config": map[string]any{"whatsapp_web": a.Config.EnableUnofficial, "telegram": a.Config.TelegramID != "" && a.Config.TelegramHash != "", "instagram": a.Config.InstagramID != "" && a.Config.InstagramSecret != "", "whatsapp_cloud": a.Config.MetaID != "" && a.Config.MetaSecret != "" && a.Config.MetaConfig != "", "stop_all_sends": a.Config.StopSends}})
	return nil
}
func (a *Server) members(w http.ResponseWriter, r *http.Request, p Principal) error {
	b, e := a.Store.JSON(r.Context(), `SELECT COALESCE(jsonb_agg(x),'[]') FROM(SELECT id,email,name,role,created_at FROM users WHERE workspace_id=$1 ORDER BY created_at)x`, p.Workspace)
	if e != nil {
		return e
	}
	write(w, 200, b)
	return nil
}
func (a *Server) invite(w http.ResponseWriter, r *http.Request, p Principal) error {
	var in struct{ Email, Role string }
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(in.Email, "@") || (in.Role != "admin" && in.Role != "member") {
		return bad("valid email and admin/member role are required")
	}
	token := secure.Token()
	_, e := a.Store.DB.Exec(r.Context(), `INSERT INTO invitations VALUES($1,$2,$3,$4,$5)`, secure.Hash(token), p.Workspace, in.Email, in.Role, time.Now().Add(48*time.Hour))
	if e != nil {
		return e
	}
	a.Store.Audit(r.Context(), p.Workspace, p.ID, "invitation.created", "")
	write(w, 201, map[string]string{"url": a.Config.Origin + "/#invite=" + token})
	return nil
}
func (a *Server) accept(w http.ResponseWriter, r *http.Request, p Principal) error {
	if !a.rateLogin(r, "invitations") {
		return problem{429, "too many attempts"}
	}
	var in struct{ Token, Password, Name string }
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Password) < 12 || len(in.Password) > 72 || strings.TrimSpace(in.Name) == "" {
		return bad("name and password of 12–72 bytes required")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	tx, e := a.Store.DB.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	var workspace, email, role string
	e = tx.QueryRow(r.Context(), `DELETE FROM invitations WHERE token_hash=$1 AND expires_at>now() RETURNING workspace_id,email,role`, secure.Hash(in.Token)).Scan(&workspace, &email, &role)
	if e != nil {
		return bad("invitation expired or already used")
	}
	id := secure.ID()
	_, e = tx.Exec(r.Context(), `INSERT INTO users(id,workspace_id,email,password_hash,name,role) VALUES($1,$2,$3,$4,$5,$6)`, id, workspace, email, string(hash), in.Name, role)
	if e != nil {
		return bad("account cannot be created")
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	if e = a.session(w, r, id); e != nil {
		return e
	}
	a.resetLogin(r, "invitations")
	write(w, 201, map[string]bool{"ok": true})
	return nil
}
func (a *Server) removeMember(w http.ResponseWriter, r *http.Request, p Principal) error {
	if r.PathValue("id") == p.ID {
		return bad("you cannot delete your own account")
	}
	tag, e := a.Store.DB.Exec(r.Context(), `DELETE FROM users WHERE id=$1 AND workspace_id=$2`, r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return missing()
	}
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func Bootstrap(ctx context.Context, s *store.Store, email, name, password string) error {
	if len(password) < 12 || len(password) > 72 {
		return bad("password must contain 12–72 bytes")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(872952)`); e != nil {
		return e
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return bad("bootstrap is only available before the first account exists")
	}
	_, e = tx.Exec(ctx, `INSERT INTO users(id,workspace_id,email,name,password_hash,role) VALUES($1,$2,$3,$4,$5,'admin')`, secure.ID(), store.Workspace, strings.ToLower(strings.TrimSpace(email)), name, string(hash))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
