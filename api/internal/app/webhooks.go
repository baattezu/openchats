package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"stepline/messaging-service/internal/secure"
	"time"
)

func (a *Server) keys(w http.ResponseWriter, r *http.Request, p Principal) error {
	b, e := a.Store.JSON(r.Context(), `SELECT COALESCE(jsonb_agg(x),'[]') FROM(SELECT id,name,prefix,created_at FROM api_keys WHERE workspace_id=$1 ORDER BY created_at)x`, p.Workspace)
	if e != nil {
		return e
	}
	write(w, 200, b)
	return nil
}
func (a *Server) createKey(w http.ResponseWriter, r *http.Request, p Principal) error {
	var in struct {
		Name string `json:"name"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.Name == "" || len(in.Name) > 100 {
		return bad("key name required")
	}
	token := "ms_" + secure.Token()
	id := secure.ID()
	_, e := a.Store.DB.Exec(r.Context(), `INSERT INTO api_keys(id,workspace_id,name,token_hash,prefix) VALUES($1,$2,$3,$4,$5)`, id, p.Workspace, in.Name, secure.Hash(token), token[:11])
	if e != nil {
		return e
	}
	a.Store.Audit(r.Context(), p.Workspace, p.ID, "api_key.created", id)
	write(w, 201, map[string]string{"id": id, "token": token})
	return nil
}
func (a *Server) deleteKey(w http.ResponseWriter, r *http.Request, p Principal) error {
	tag, e := a.Store.DB.Exec(r.Context(), `DELETE FROM api_keys WHERE id=$1 AND workspace_id=$2`, r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return missing()
	}
	a.Store.Audit(r.Context(), p.Workspace, p.ID, "api_key.revoked", r.PathValue("id"))
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) webhooks(w http.ResponseWriter, r *http.Request, p Principal) error {
	b, e := a.Store.JSON(r.Context(), `SELECT COALESCE(jsonb_agg(x),'[]') FROM(SELECT id,url,enabled,created_at FROM webhook_subscriptions WHERE workspace_id=$1 ORDER BY created_at)x`, p.Workspace)
	if e != nil {
		return e
	}
	write(w, 200, b)
	return nil
}
func (a *Server) createWebhook(w http.ResponseWriter, r *http.Request, p Principal) error {
	var in struct {
		URL string `json:"url"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if e := secure.ValidateURL(in.URL); e != nil {
		return bad(e.Error())
	}
	id := secure.ID()
	secret := secure.Token()
	_, e := a.Store.DB.Exec(r.Context(), `INSERT INTO webhook_subscriptions(id,workspace_id,url,secret) VALUES($1,$2,$3,$4)`, id, p.Workspace, in.URL, a.Store.Vault.Seal([]byte(secret), "webhook:"+id))
	if e != nil {
		return e
	}
	a.Store.Audit(r.Context(), p.Workspace, p.ID, "webhook.created", id)
	write(w, 201, map[string]string{"id": id, "secret": secret})
	return nil
}
func (a *Server) deleteWebhook(w http.ResponseWriter, r *http.Request, p Principal) error {
	_, e := a.Store.DB.Exec(r.Context(), `DELETE FROM webhook_subscriptions WHERE id=$1 AND workspace_id=$2`, r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) deliveries(w http.ResponseWriter, r *http.Request, p Principal) error {
	b, e := a.Store.JSON(r.Context(), `SELECT COALESCE(jsonb_agg(x),'[]') FROM(SELECT id,subscription_id,event_id,attempts,state,response_code,error FROM webhook_deliveries WHERE workspace_id=$1 ORDER BY id DESC LIMIT 100)x`, p.Workspace)
	if e != nil {
		return e
	}
	write(w, 200, b)
	return nil
}
func signature(secret string, b []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write(b)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}
func (a *Server) deliver(ctx context.Context, id string) error {
	var url, subscription, event, kind string
	var enc, data []byte
	var created time.Time
	e := a.Store.DB.QueryRow(ctx, `SELECT s.url,s.id,s.secret,e.id,e.type,e.data,e.created_at FROM webhook_deliveries d JOIN webhook_subscriptions s ON s.id=d.subscription_id JOIN events e ON e.id=d.event_id WHERE d.id=$1 AND s.enabled`, id).Scan(&url, &subscription, &enc, &event, &kind, &data, &created)
	if e != nil {
		return nil
	}
	secret, e := a.Store.Vault.Open(enc, "webhook:"+subscription)
	if e != nil {
		return e
	}
	body, _ := json.Marshal(map[string]any{"id": event, "type": kind, "created_at": created, "data": json.RawMessage(data)})
	ts := fmt.Sprint(time.Now().Unix())
	r, e := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Messaging-Event-ID", event)
	r.Header.Set("X-Messaging-Timestamp", ts)
	r.Header.Set("X-Messaging-Signature", signature(string(secret), append([]byte(ts+"."), body...)))
	resp, e := secure.PublicClient().Do(r)
	code := 0
	if resp != nil {
		code = resp.StatusCode
		resp.Body.Close()
	}
	success := e == nil && code >= 200 && code < 300
	state := "pending"
	detail := ""
	if success {
		state = "delivered"
	} else {
		detail = fmt.Sprintf("delivery failed (HTTP %d)", code)
	}
	_, dbErr := a.Store.DB.Exec(ctx, `UPDATE webhook_deliveries SET attempts=attempts+1,state=$2,response_code=NULLIF($3,0),error=$4 WHERE id=$1`, id, state, code, detail)
	if dbErr != nil {
		return dbErr
	}
	if !success {
		return errors.New(detail)
	}
	return nil
}
