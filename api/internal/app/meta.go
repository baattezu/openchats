package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"stepline/messaging-service/internal/provider"
	"stepline/messaging-service/internal/secure"
	"stepline/messaging-service/internal/store"
	"strconv"
	"strings"
	"time"
)

func (a *Server) oauthStart(w http.ResponseWriter, r *http.Request, p Principal) error {
	if p.API {
		return bad("OAuth requires browser login")
	}
	c, e := a.Store.Connection(r.Context(), r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	if c.Provider != "instagram" && c.Provider != "whatsapp_cloud" {
		return bad("OAuth unsupported")
	}
	if (c.Provider == "instagram" && (a.Config.InstagramID == "" || a.Config.InstagramSecret == "")) || (c.Provider == "whatsapp_cloud" && (a.Config.MetaID == "" || a.Config.MetaSecret == "")) {
		return bad("Meta app is not configured")
	}
	state := secure.Token()
	_, e = a.Store.DB.Exec(r.Context(), `INSERT INTO oauth_states VALUES($1,$2,$3,$4)`, secure.Hash(state), c.ID, p.ID, time.Now().Add(10*time.Minute))
	if e != nil {
		return e
	}
	if c.Provider == "whatsapp_cloud" {
		write(w, 200, map[string]string{"app_id": a.Config.MetaID, "config_id": a.Config.MetaConfig, "version": a.Config.MetaVersion, "state": state})
		return nil
	}
	q := url.Values{"client_id": {a.Config.InstagramID}, "redirect_uri": {a.Config.Origin + "/api/v1/oauth/instagram/callback"}, "response_type": {"code"}, "scope": {"instagram_business_basic,instagram_business_manage_messages"}, "state": {state}, "enable_fb_login": {"0"}, "force_authentication": {"1"}}
	write(w, 200, map[string]string{"url": "https://www.instagram.com/oauth/authorize?" + q.Encode()})
	return nil
}
func (a *Server) consumeState(ctx context.Context, state, user string) (string, error) {
	var id string
	e := a.Store.DB.QueryRow(ctx, `DELETE FROM oauth_states WHERE token_hash=$1 AND user_id=$2 AND expires_at>now() RETURNING connection_id`, secure.Hash(state), user).Scan(&id)
	if e != nil {
		return "", bad("OAuth state expired or invalid")
	}
	return id, nil
}
func (a *Server) oauthCallback(w http.ResponseWriter, r *http.Request, p Principal) error {
	if p.API {
		return bad("browser login required")
	}
	id, e := a.consumeState(r.Context(), r.URL.Query().Get("state"), p.ID)
	if e != nil {
		return e
	}
	c, e := a.Store.Connection(r.Context(), id, p.Workspace)
	if e != nil {
		return e
	}
	if c.Provider != "instagram" {
		return bad("invalid OAuth provider")
	}
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, a.Config.Origin+"/?oauth=cancelled", 303)
		return nil
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		return bad("OAuth code missing")
	}
	v := url.Values{"client_id": {a.Config.InstagramID}, "client_secret": {a.Config.InstagramSecret}, "grant_type": {"authorization_code"}, "redirect_uri": {a.Config.Origin + "/api/v1/oauth/instagram/callback"}, "code": {code}}
	req, e := http.NewRequestWithContext(r.Context(), "POST", "https://api.instagram.com/oauth/access_token", strings.NewReader(v.Encode()))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := secure.PublicClient().Do(req)
	if e != nil {
		return bad("Instagram token exchange failed")
	}
	defer resp.Body.Close()
	var token struct {
		Token  string      `json:"access_token"`
		UserID json.Number `json:"user_id"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&token) != nil || token.Token == "" {
		return bad("Instagram token exchange rejected")
	}
	q := url.Values{"grant_type": {"ig_exchange_token"}, "client_secret": {a.Config.InstagramSecret}, "access_token": {token.Token}}
	req, _ = http.NewRequestWithContext(r.Context(), "GET", "https://graph.instagram.com/access_token?"+q.Encode(), nil)
	resp2, e := secure.PublicClient().Do(req)
	if e != nil {
		return bad("Instagram long-lived token exchange failed")
	}
	defer resp2.Body.Close()
	var long struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if resp2.StatusCode != 200 || json.NewDecoder(resp2.Body).Decode(&long) != nil || long.Token == "" {
		return bad("Instagram long-lived token exchange rejected")
	}
	m := &provider.Meta{Provider: "instagram", ID: token.UserID.String(), Token: long.Token, Version: a.Config.MetaVersion}
	var me struct {
		ID       string      `json:"id"`
		UserID   json.Number `json:"user_id"`
		Username string      `json:"username"`
	}
	if e = m.Request(r.Context(), "GET", "me?fields=user_id,username", nil, &me); e != nil {
		return bad("Instagram account lookup failed")
	}
	if me.UserID.String() != "" {
		m.ID = me.UserID.String()
	}
	if e = m.Request(r.Context(), "POST", m.ID+"/subscribed_apps", map[string]string{"subscribed_fields": "messages,messaging_postbacks,messaging_seen,message_reactions"}, nil); e != nil {
		return bad("Instagram webhook subscription failed; check permissions")
	}
	creds := map[string]string{"access_token": long.Token, "account_id": m.ID, "expires_at": time.Now().Add(time.Duration(long.Expires) * time.Second).Format(time.RFC3339)}
	if e = a.saveMeta(r.Context(), c, creds, "@"+me.Username); e != nil {
		return e
	}
	http.Redirect(w, r, a.Config.Origin+"/?oauth=connected", 303)
	return nil
}
func (a *Server) embeddedSignup(w http.ResponseWriter, r *http.Request, p Principal) error {
	var in struct {
		State string `json:"state"`
		Code  string `json:"code"`
		WABA  string `json:"waba_id"`
		Phone string `json:"phone_number_id"`
		PIN   string `json:"pin"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	id, e := a.consumeState(r.Context(), in.State, p.ID)
	if e != nil {
		return e
	}
	if id != r.PathValue("id") {
		return bad("OAuth connection mismatch")
	}
	c, e := a.Store.Connection(r.Context(), id, p.Workspace)
	if e != nil {
		return e
	}
	if c.Provider != "whatsapp_cloud" {
		return bad("invalid provider")
	}
	if in.Code == "" || !digits(in.WABA) || !digits(in.Phone) || len(in.PIN) != 6 || !digits(in.PIN) {
		return bad("code, business/phone IDs and six-digit WhatsApp two-step PIN required")
	}
	q := url.Values{"client_id": {a.Config.MetaID}, "client_secret": {a.Config.MetaSecret}, "code": {in.Code}}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", "https://graph.facebook.com/"+a.Config.MetaVersion+"/oauth/access_token?"+q.Encode(), nil)
	resp, e := secure.PublicClient().Do(req)
	if e != nil {
		return bad("Meta token exchange failed")
	}
	defer resp.Body.Close()
	var v struct {
		Token string `json:"access_token"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&v) != nil || v.Token == "" {
		return bad("Meta token exchange rejected")
	}
	m := &provider.Meta{Provider: "whatsapp_cloud", ID: in.Phone, Token: v.Token, Version: a.Config.MetaVersion}
	var phones struct {
		Data []struct {
			ID    string `json:"id"`
			Phone string `json:"display_phone_number"`
		} `json:"data"`
	}
	if e = m.Request(r.Context(), "GET", in.WABA+"/phone_numbers", nil, &phones); e != nil {
		return bad("business access validation failed")
	}
	identity := ""
	for _, p := range phones.Data {
		if p.ID == in.Phone {
			identity = p.Phone
		}
	}
	if identity == "" {
		return bad("selected number is not accessible on this business account")
	}
	if e = m.Request(r.Context(), "POST", in.Phone+"/register", map[string]string{"messaging_product": "whatsapp", "pin": in.PIN}, nil); e != nil {
		return bad("WhatsApp number registration failed; verify the two-step PIN and account eligibility, then repeat signup")
	}
	if e = m.Request(r.Context(), "POST", in.WABA+"/subscribed_apps", map[string]any{}, nil); e != nil {
		return bad("WhatsApp webhook subscription failed")
	}
	if e = a.saveMeta(r.Context(), c, map[string]string{"access_token": v.Token, "account_id": in.Phone, "waba_id": in.WABA}, identity); e != nil {
		return e
	}
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func digits(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func (a *Server) saveMeta(ctx context.Context, c provider.Connection, creds map[string]string, identity string) error {
	b, _ := json.Marshal(creds)
	tx, e := a.Store.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `UPDATE connections SET credentials=$2,external_id=$3,identity=$4,status='needs_auth' WHERE id=$1`, c.ID, a.Store.Vault.Seal(b, c.ID+":credentials"), creds["account_id"], identity)
	if e != nil {
		return bad("account is already connected or could not be saved")
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if e = a.Manager.Stop(ctx, c.ID, false); e != nil {
		return e
	}
	return a.Manager.Connect(c.ID, c.WorkspaceID)
}
func (a *Server) verifyMeta(w http.ResponseWriter, r *http.Request, p Principal) error {
	q := r.URL.Query()
	if a.Config.WebhookVerify == "" || q.Get("hub.mode") != "subscribe" || !hmac.Equal([]byte(q.Get("hub.verify_token")), []byte(a.Config.WebhookVerify)) {
		return problem{403, "verification failed"}
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(q.Get("hub.challenge")))
	return nil
}
func (a *Server) metaWebhook(w http.ResponseWriter, r *http.Request, p Principal) error {
	if a.Config.MetaSecret == "" && a.Config.InstagramSecret == "" {
		return problem{503, "Meta not configured"}
	}
	b, e := secure.ReadLimited(r.Body, 1<<20)
	if e != nil {
		return bad("webhook too large")
	}
	signed := r.Header.Get("X-Hub-Signature-256")
	metaValid := a.Config.MetaSecret != "" && hmac.Equal([]byte(signature(a.Config.MetaSecret, b)), []byte(signed))
	instagramValid := a.Config.InstagramSecret != "" && hmac.Equal([]byte(signature(a.Config.InstagramSecret, b)), []byte(signed))
	if !metaValid && !instagramValid {
		return problem{403, "invalid signature"}
	}
	var envelope provider.MetaEnvelope
	if json.Unmarshal(b, &envelope) != nil {
		return bad("invalid webhook")
	}
	tx, e := a.Store.DB.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	// Split multi-account envelopes so deleting one connection can erase its raw
	// failed jobs without discarding another account's pending messages.
	for _, entry := range envelope.Entry {
		count := len(entry.Changes)
		if envelope.Object == "instagram" {
			count = 1
		}
		for i := 0; i < count; i++ {
			part := envelope
			part.Entry = append(envelope.Entry[:0:0], entry)
			providerName := "whatsapp_cloud"
			external := ""
			if envelope.Object == "instagram" {
				providerName = "instagram"
				external = entry.ID
			} else {
				external = entry.Changes[i].Value.Metadata.PhoneID
				part.Entry[0].Changes = entry.Changes[i : i+1]
				part.Entry[0].Messaging = nil
			}
			var connectionID string
			err := tx.QueryRow(r.Context(), `SELECT id FROM connections WHERE workspace_id=$1 AND provider=$2 AND external_id=$3 AND status<>'disconnected'`, store.Workspace, providerName, external).Scan(&connectionID)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			canonical, err := json.Marshal(part)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(append(append([]byte(nil), b...), canonical...))
			id := fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
			payload, _ := json.Marshal(map[string]any{"body": a.Store.Vault.Seal(canonical, "meta-job:"+id), "connection_id": connectionID})
			if _, err = tx.Exec(r.Context(), `INSERT INTO jobs(id,workspace_id,kind,payload) VALUES($1,$2,'meta',$3) ON CONFLICT(id) DO NOTHING`, id, store.Workspace, payload); err != nil {
				return err
			}
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) processMeta(ctx context.Context, jobID string, payload []byte) error {
	var job struct {
		Body []byte `json:"body"`
	}
	if e := json.Unmarshal(payload, &job); e != nil {
		return e
	}
	b, e := a.Store.Vault.Open(job.Body, "meta-job:"+jobID)
	if e != nil {
		return e
	}
	var env provider.MetaEnvelope
	if e = json.Unmarshal(b, &env); e != nil {
		return e
	}
	for _, entry := range env.Entry {
		for _, change := range entry.Changes {
			v := change.Value
			var id string
			if e = a.Store.DB.QueryRow(ctx, `SELECT id FROM connections WHERE provider='whatsapp_cloud' AND external_id=$1 AND status<>'disconnected'`, v.Metadata.PhoneID).Scan(&id); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					continue
				}
				return e
			}
			c, e := a.Store.Connection(ctx, id, store.Workspace)
			if e != nil {
				return e
			}
			m, e := a.Manager.Meta(id)
			if e != nil {
				return e
			}
			for _, msg := range v.Messages {
				seconds, _ := strconv.ParseInt(msg.Timestamp, 10, 64)
				ev := provider.Event{ExternalID: msg.ID, ChatID: msg.From, Sender: msg.From, Kind: msg.Type, Text: msg.Text.Body, At: time.Unix(seconds, 0), ReplyTo: msg.Context.ID}
				for _, contact := range v.Contacts {
					if contact.WaID == msg.From {
						ev.Title = contact.Profile.Name
					}
				}
				media := msg.Image
				if msg.Document != nil {
					media = msg.Document
				}
				if msg.Audio != nil {
					media = msg.Audio
				}
				if msg.Video != nil {
					media = msg.Video
				}
				if media != nil {
					ev.Text = media.Caption
					ev.Media, e = m.Download(ctx, media.ID)
					if e != nil {
						ev.Text += " [attachment unavailable]"
						ev.Metadata = json.RawMessage(`{"attachment_unavailable":true}`)
					} else if media.Filename != "" {
						ev.Media.Name = media.Filename
					}
				}
				if msg.Type != "text" && media == nil {
					ev.Kind = "unsupported"
					ev.Text = "Unsupported WhatsApp message"
				}
				if e = a.Store.Ingest(ctx, c, ev); e != nil {
					return e
				}
			}
			for _, status := range v.Statuses {
				if e = a.Store.Ingest(ctx, c, provider.Event{ReceiptIDs: []string{status.ID}, Status: status.Status}); e != nil {
					return e
				}
			}
		}
		for _, msg := range entry.Messaging {
			var id string
			if e = a.Store.DB.QueryRow(ctx, `SELECT id FROM connections WHERE provider='instagram' AND external_id=$1 AND status<>'disconnected'`, entry.ID).Scan(&id); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					continue
				}
				return e
			}
			c, e := a.Store.Connection(ctx, id, store.Workspace)
			if e != nil {
				return e
			}
			if msg.Message.ID == "" {
				continue
			}
			chat := msg.Sender.ID
			direction := "in"
			if msg.Message.Echo {
				chat = msg.Recipient.ID
				direction = "out"
			}
			ev := provider.Event{ExternalID: msg.Message.ID, ChatID: chat, Sender: msg.Sender.ID, Direction: direction, Kind: "text", Text: msg.Message.Text, At: time.UnixMilli(msg.Timestamp), Delete: msg.Message.Deleted}
			if len(msg.Message.Attachments) > 0 {
				x := msg.Message.Attachments[0]
				ev.Kind = x.Type
				if secure.ValidateURL(x.Payload.URL) == nil {
					req, _ := http.NewRequestWithContext(ctx, "GET", x.Payload.URL, nil)
					resp, err := secure.PublicClient().Do(req)
					if err == nil {
						if resp.StatusCode == 200 {
							data, err := secure.ReadLimited(resp.Body, 25<<20)
							if err == nil {
								ev.Media = &provider.Media{Name: "instagram-attachment", MIME: resp.Header.Get("Content-Type"), Data: data}
							}
						}
						resp.Body.Close()
					}
				}
				if ev.Media == nil {
					ev.Text += " [attachment unavailable]"
				}
			}
			if e = a.Store.Ingest(ctx, c, ev); e != nil {
				return e
			}
		}
	}
	return nil
}
func (a *Server) mediaURL(id string) string {
	expires := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	sig := strings.TrimPrefix(signature(a.Config.Key, []byte("media:"+id+":"+expires)), "sha256=")
	return a.Config.Origin + "/api/v1/provider-media/" + id + "?expires=" + expires + "&signature=" + sig
}
func (a *Server) providerMedia(w http.ResponseWriter, r *http.Request, p Principal) error {
	id := r.PathValue("id")
	exp := r.URL.Query().Get("expires")
	seconds, e := strconv.ParseInt(exp, 10, 64)
	if e != nil || time.Now().Unix() > seconds || seconds > time.Now().Add(10*time.Minute).Unix() {
		return problem{403, "media URL expired"}
	}
	provided, e := hex.DecodeString(r.URL.Query().Get("signature"))
	if e != nil {
		return problem{403, "invalid signature"}
	}
	expected, _ := hex.DecodeString(strings.TrimPrefix(signature(a.Config.Key, []byte("media:"+id+":"+exp)), "sha256="))
	if !hmac.Equal(provided, expected) {
		return problem{403, "invalid signature"}
	}
	m, e := a.Store.Media(r.Context(), id, store.Workspace)
	if e != nil {
		return e
	}
	serveMedia(w, m)
	return nil
}
