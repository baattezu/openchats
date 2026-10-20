package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"stepline/messaging-service/internal/provider"
	"stepline/messaging-service/internal/secure"
	"strconv"
	"strings"
	"time"
)

func timeContext(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
func (a *Server) conversations(w http.ResponseWriter, r *http.Request, p Principal) error {
	limit := 100
	b, e := a.Store.JSON(r.Context(), `SELECT COALESCE(jsonb_agg(x),'[]') FROM(SELECT c.*,co.provider,co.label AS account_label,(SELECT text FROM messages m WHERE m.conversation_id=c.id ORDER BY sent_at DESC,id DESC LIMIT 1) AS preview,(SELECT count(*) FROM messages m WHERE m.conversation_id=c.id AND m.direction='in' AND m.sent_at>COALESCE(c.read_at,'1970-01-01')) AS unread FROM conversations c JOIN connections co ON co.id=c.connection_id WHERE c.workspace_id=$1 AND ($2='' OR c.connection_id=NULLIF($2,'')::uuid) AND ($3='' OR (c.last_message_at,c.id)<(SELECT last_message_at,id FROM conversations WHERE id=NULLIF($3,'')::uuid AND workspace_id=$1)) ORDER BY c.last_message_at DESC,c.id DESC LIMIT $4)x`, p.Workspace, r.URL.Query().Get("connection_id"), r.URL.Query().Get("before"), limit)
	if e != nil {
		return e
	}
	write(w, 200, b)
	return nil
}
func (a *Server) messages(w http.ResponseWriter, r *http.Request, p Principal) error {
	var exists bool
	e := a.Store.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1 AND workspace_id=$2)`, r.PathValue("id"), p.Workspace).Scan(&exists)
	if e != nil {
		return e
	}
	if !exists {
		return missing()
	}
	b, e := a.Store.JSON(r.Context(), `SELECT COALESCE(jsonb_agg(x),'[]') FROM(SELECT m.*,CASE WHEN me.id IS NULL THEN NULL ELSE jsonb_build_object('id',me.id,'name',me.name,'mime',me.mime,'size',me.size) END AS media FROM messages m LEFT JOIN media me ON me.id=m.media_id WHERE m.conversation_id=$1 AND m.workspace_id=$2 AND ($3='' OR (m.sent_at,m.id)<(SELECT sent_at,id FROM messages WHERE id=NULLIF($3,'')::uuid AND workspace_id=$2)) ORDER BY m.sent_at DESC,m.id DESC LIMIT 100)x`, r.PathValue("id"), p.Workspace, r.URL.Query().Get("before"))
	if e != nil {
		return e
	}
	write(w, 200, b)
	return nil
}
func (a *Server) read(w http.ResponseWriter, r *http.Request, p Principal) error {
	tag, e := a.Store.DB.Exec(r.Context(), `UPDATE conversations SET read_at=now() WHERE id=$1 AND workspace_id=$2`, r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return missing()
	}
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) message(w http.ResponseWriter, r *http.Request, p Principal) error {
	b, e := a.Store.JSON(r.Context(), `SELECT to_jsonb(x) FROM(SELECT m.*,CASE WHEN me.id IS NULL THEN NULL ELSE jsonb_build_object('id',me.id,'name',me.name,'mime',me.mime,'size',me.size) END AS media FROM messages m LEFT JOIN media me ON me.id=m.media_id WHERE m.id=$1 AND m.workspace_id=$2)x`, r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	write(w, 200, b)
	return nil
}

type sendInput struct {
	Text     string          `json:"text"`
	Kind     string          `json:"kind"`
	MediaID  string          `json:"media_id"`
	ReplyTo  string          `json:"reply_to"`
	Template json.RawMessage `json:"template"`
}

func (a *Server) send(w http.ResponseWriter, r *http.Request, p Principal) error {
	if a.Config.StopSends {
		return problem{503, "all sends are paused by the operator"}
	}
	id := r.Header.Get("Idempotency-Key")
	if _, e := uuid.Parse(id); e != nil {
		return bad("Idempotency-Key must be a UUID")
	}
	var in sendInput
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.Kind == "" {
		in.Kind = "text"
	}
	if string(in.Template) == "null" {
		in.Template = nil
	}
	if len(in.Template) > 0 {
		var template struct {
			Name     string `json:"name"`
			Language struct {
				Code string `json:"code"`
			} `json:"language"`
		}
		if json.Unmarshal(in.Template, &template) != nil || template.Name == "" || template.Language.Code == "" {
			return bad("template name and language.code are required")
		}
		if in.MediaID != "" {
			return bad("choose a template or a separate attachment")
		}
	}
	if in.Kind != "text" && in.Kind != "image" && in.Kind != "document" && in.Kind != "audio" && in.Kind != "video" {
		return bad("unsupported message kind")
	}
	if len(in.Text) > 4096 {
		return bad("text exceeds 4096 bytes")
	}
	if strings.TrimSpace(in.Text) == "" && in.MediaID == "" && len(in.Template) == 0 {
		return bad("message is empty")
	}
	if in.Kind != "text" && in.MediaID == "" {
		return bad("media_id required for attachments")
	}
	if in.Kind == "text" && in.MediaID != "" {
		return bad("choose an attachment kind")
	}
	var connection, chat string
	var inbound *time.Time
	if e := a.Store.DB.QueryRow(r.Context(), `SELECT connection_id,external_id,last_inbound_at FROM conversations WHERE id=$1 AND workspace_id=$2`, r.PathValue("id"), p.Workspace).Scan(&connection, &chat, &inbound); e != nil {
		return e
	}
	c, e := a.Store.Connection(r.Context(), connection, p.Workspace)
	if e != nil {
		return e
	}
	if c.Status != "connected" {
		return bad("connection is not ready")
	}
	if c.Provider == "whatsapp_web" && inbound == nil {
		return bad("unofficial WhatsApp is reply-only; wait for an inbound message")
	}
	if c.Provider == "whatsapp_cloud" && (inbound == nil || time.Since(*inbound) > 24*time.Hour) && len(in.Template) == 0 {
		return bad("service window closed; select an approved template")
	}
	if c.Provider == "instagram" && (inbound == nil || time.Since(*inbound) > 24*time.Hour) {
		return bad("Instagram reply window closed")
	}
	if len(in.Template) > 0 && c.Provider != "whatsapp_cloud" {
		return bad("templates are only supported by official WhatsApp")
	}
	if in.ReplyTo != "" && c.Provider != "whatsapp_cloud" {
		return bad("quoted reply references are currently supported only by official WhatsApp")
	}
	if c.Provider == "instagram" && in.MediaID != "" && in.Text != "" {
		return bad("send attachment and text as separate messages on Instagram")
	}
	if in.MediaID != "" {
		media, e := a.Store.Media(r.Context(), in.MediaID, p.Workspace)
		if e != nil {
			return e
		}
		if !c.Capabilities.Media {
			return bad("attachments unsupported")
		}
		if in.Kind == "image" && !strings.HasPrefix(media.MIME, "image/") {
			return bad("image MIME required")
		}
		if in.Kind == "audio" && !strings.HasPrefix(media.MIME, "audio/") {
			return bad("audio MIME required")
		}
		if in.Kind == "video" && !strings.HasPrefix(media.MIME, "video/") {
			return bad("video MIME required")
		}
	}
	canonical, _ := json.Marshal(struct {
		Conversation string
		Input        sendInput
	}{r.PathValue("id"), in})
	hash := secure.Hash(string(canonical))
	tx, e := a.Store.DB.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	if _, e = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); e != nil {
		return e
	}
	var prior string
	e = tx.QueryRow(r.Context(), `SELECT metadata->>'request_hash' FROM messages WHERE id=$1 AND workspace_id=$2`, id, p.Workspace).Scan(&prior)
	if e == nil {
		if prior != hash {
			return problem{409, "idempotency key was used with a different message"}
		}
		write(w, 200, map[string]string{"id": id, "status": "existing"})
		return nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	meta := map[string]any{"request_hash": hash}
	if len(in.Template) > 0 {
		meta["template"] = in.Template
	}
	b, _ := json.Marshal(meta)
	ext := ""
	if c.Provider == "whatsapp_web" {
		ext = strings.ReplaceAll(id, "-", "")
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO messages(id,workspace_id,conversation_id,connection_id,direction,sender,kind,text,media_id,status,reply_to,metadata,external_id) VALUES($1,$2,$3,$4,'out',$5,$6,$7,NULLIF($8,'')::uuid,'queued',$9,$10,NULLIF($11,''))`, id, p.Workspace, r.PathValue("id"), c.ID, p.Name, in.Kind, in.Text, in.MediaID, in.ReplyTo, b, ext)
	if e != nil {
		return e
	}
	if in.MediaID != "" {
		var tag pgconn.CommandTag
		tag, e = tx.Exec(r.Context(), `UPDATE media SET connection_id=$2 WHERE id=$1 AND workspace_id=$3 AND (connection_id IS NULL OR connection_id=$2)`, in.MediaID, c.ID, p.Workspace)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return bad("attachment belongs to another account; upload it again")
		}
	}
	payload, _ := json.Marshal(map[string]string{"message_id": id, "connection_id": c.ID})
	if _, e = tx.Exec(r.Context(), `INSERT INTO jobs(id,workspace_id,kind,payload) VALUES($1,$2,'send',$3)`, secure.ID(), p.Workspace, payload); e != nil {
		return e
	}
	if e = a.Store.EmitTx(r.Context(), tx, p.Workspace, "message.created", map[string]any{"id": id, "connection_id": c.ID, "conversation_id": r.PathValue("id"), "direction": "out", "kind": in.Kind, "text": in.Text, "media_id": in.MediaID, "external_id": ext, "status": "queued"}); e != nil {
		return e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	write(w, 202, map[string]string{"id": id, "status": "queued"})
	return nil
}
func (a *Server) actionMessage(w http.ResponseWriter, r *http.Request, p Principal, delete bool) error {
	var s provider.Send
	var connection, conv, status, kind, direction string
	e := a.Store.DB.QueryRow(r.Context(), `SELECT m.id,m.connection_id,m.conversation_id,c.external_id,COALESCE(m.external_id,''),m.status,m.kind,m.direction FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE m.id=$1 AND m.workspace_id=$2 AND m.deleted_at IS NULL`, r.PathValue("id"), p.Workspace).Scan(&s.ID, &connection, &conv, &s.ChatID, &s.ExternalID, &status, &kind, &direction)
	if e != nil {
		return e
	}
	if direction != "out" || s.ExternalID == "" || status == "queued" || status == "unknown" {
		return bad("only confirmed outbound messages can be changed")
	}
	c, e := a.Store.Connection(r.Context(), connection, p.Workspace)
	if e != nil {
		return e
	}
	if a.Config.StopSends || c.Status != "connected" {
		return bad("connection is paused or unavailable")
	}
	if delete && !c.Capabilities.Delete || !delete && !c.Capabilities.Edit {
		return bad("operation unsupported by provider")
	}
	if !delete {
		if kind != "text" {
			return bad("only text messages can be edited")
		}
		var in struct {
			Text string `json:"text"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if strings.TrimSpace(in.Text) == "" || len(in.Text) > 4096 {
			return bad("text of 1–4096 bytes required")
		}
		s.Text = in.Text
	}
	adapter, e := a.Manager.Adapter(connection)
	if e != nil {
		return e
	}
	if delete {
		e = adapter.Delete(r.Context(), s)
	} else {
		e = adapter.Edit(r.Context(), s)
	}
	if e != nil {
		return bad("provider rejected the action or its result is uncertain; refresh before retrying")
	}
	if e = a.Store.Ingest(r.Context(), c, provider.Event{ExternalID: s.ExternalID, Text: s.Text, Edit: !delete, Delete: delete}); e != nil {
		return e
	}
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) editMessage(w http.ResponseWriter, r *http.Request, p Principal) error {
	return a.actionMessage(w, r, p, false)
}
func (a *Server) deleteMessage(w http.ResponseWriter, r *http.Request, p Principal) error {
	return a.actionMessage(w, r, p, true)
}
func (a *Server) upload(w http.ResponseWriter, r *http.Request, p Principal) error {
	r.Body = http.MaxBytesReader(w, r.Body, (25<<20)+(1<<20))
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		return bad("invalid upload or attachment exceeds 25 MiB")
	}
	defer r.MultipartForm.RemoveAll()
	f, h, e := r.FormFile("file")
	if e != nil {
		return bad("file required")
	}
	defer f.Close()
	b, e := secure.ReadLimited(f, 25<<20)
	if e != nil {
		return bad("attachment exceeds 25 MiB")
	}
	mt := http.DetectContentType(b)
	if strings.HasPrefix(mt, "text/html") || strings.Contains(mt, "svg") {
		mt = "application/octet-stream"
	}
	if typ := mime.TypeByExtension(filepath.Ext(h.Filename)); strings.HasPrefix(typ, "audio/") || strings.HasPrefix(typ, "video/") {
		mt = typ
	}
	id, e := a.Store.SaveMedia(r.Context(), p.Workspace, "", provider.Media{Name: h.Filename, MIME: mt, Data: b})
	if e != nil {
		return e
	}
	write(w, 201, map[string]any{"id": id, "name": filepath.Base(h.Filename), "mime": mt, "size": len(b)})
	return nil
}
func (a *Server) media(w http.ResponseWriter, r *http.Request, p Principal) error {
	m, e := a.Store.Media(r.Context(), r.PathValue("id"), p.Workspace)
	if e != nil {
		return e
	}
	serveMedia(w, m)
	return nil
}
func serveMedia(w http.ResponseWriter, m provider.Media) {
	w.Header().Set("Content-Type", m.MIME)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": m.Name}))
	w.Header().Set("Content-Length", strconv.Itoa(len(m.Data)))
	_, _ = w.Write(m.Data)
}
func (a *Server) deleteMedia(w http.ResponseWriter, r *http.Request, p Principal) error {
	var path string
	e := a.Store.DB.QueryRow(r.Context(), `DELETE FROM media WHERE id=$1 AND workspace_id=$2 AND NOT EXISTS(SELECT 1 FROM messages WHERE media_id=$1) RETURNING path`, r.PathValue("id"), p.Workspace).Scan(&path)
	if e != nil {
		return bad("attachment is in use or does not exist")
	}
	if e = os.Remove(filepath.Join(a.Store.MediaDir, path)); e != nil && !os.IsNotExist(e) {
		return e
	}
	write(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *Server) events(w http.ResponseWriter, r *http.Request, p Principal) error {
	f, ok := w.(http.Flusher)
	if !ok {
		return problem{500, "streaming unavailable"}
	}
	if p.API {
		return problem{403, "SSE is for browser sessions; use CRM webhooks"}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	f.Flush()
	cursor := r.Header.Get("Last-Event-ID")
	since := time.Now()
	if cursor != "" {
		_ = a.Store.DB.QueryRow(r.Context(), `SELECT created_at FROM events WHERE id=$1 AND workspace_id=$2`, cursor, p.Workspace).Scan(&since)
	}
	if _, e := uuid.Parse(cursor); e != nil {
		cursor = "00000000-0000-0000-0000-000000000000"
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	expires := time.NewTimer(5 * time.Minute)
	defer expires.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-expires.C:
			return nil
		case <-tick.C:
			if _, e := a.principal(r); e != nil {
				return nil
			}
			rows, e := a.Store.DB.Query(r.Context(), `SELECT id,type,data,created_at FROM events WHERE workspace_id=$1 AND (created_at,id)>($2,$3::uuid) ORDER BY created_at,id LIMIT 100`, p.Workspace, since, cursor)
			if e != nil {
				return nil
			}
			for rows.Next() {
				var id, kind string
				var data json.RawMessage
				var t time.Time
				if rows.Scan(&id, &kind, &data, &t) != nil {
					break
				}
				b, _ := json.Marshal(map[string]any{"type": kind, "data": data})
				_, _ = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", id, b)
				since = t
				cursor = id
			}
			rows.Close()
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			f.Flush()
		}
	}
}
