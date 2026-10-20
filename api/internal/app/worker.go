package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"stepline/messaging-service/internal/provider"
	"time"
)

func (a *Server) Worker(ctx context.Context) {
	_, e := a.Store.DB.Exec(ctx, `UPDATE messages SET status='unknown' WHERE id IN(SELECT (payload->>'message_id')::uuid FROM jobs WHERE state='running' AND kind='send'); UPDATE jobs SET state=CASE WHEN kind IN ('webhook','meta') THEN 'pending' ELSE 'failed' END,error='Service restarted while dispatch was in progress' WHERE state='running'`)
	if e != nil {
		slog.Error("job recovery failed")
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	refresh := time.NewTicker(6 * time.Hour)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			a.Manager.refreshTokens(ctx)
		case <-tick.C:
			for i := 0; i < 20; i++ {
				if !a.runJob(ctx) {
					break
				}
			}
		}
	}
}
func (a *Server) runJob(ctx context.Context) bool {
	var id, w, kind string
	var payload []byte
	var attempts int
	e := a.Store.DB.QueryRow(ctx, `UPDATE jobs SET state='running',locked_at=now(),attempts=attempts+1 WHERE id=(SELECT id FROM jobs WHERE state='pending' AND run_at<=now() ORDER BY run_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,workspace_id,kind,payload,attempts`).Scan(&id, &w, &kind, &payload, &attempts)
	if errors.Is(e, pgx.ErrNoRows) {
		return false
	}
	if e != nil {
		slog.Error("job claim failed")
		return false
	}
	var p map[string]string
	if json.Unmarshal(payload, &p) != nil {
		_, _ = a.Store.DB.Exec(ctx, `UPDATE jobs SET state='failed',error='Invalid job payload' WHERE id=$1`, id)
		return true
	}
	jctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	if kind == "import" {
		cancel()
		jctx, cancel = context.WithTimeout(ctx, 20*time.Minute)
	}
	defer cancel()
	switch kind {
	case "meta":
		e = a.processMeta(jctx, id, payload)
	case "webhook":
		e = a.deliver(jctx, p["delivery_id"])
	case "send":
		e = a.dispatch(jctx, p["connection_id"], p["message_id"], w)
	case "import":
		var adapter provider.Adapter
		adapter, e = a.Manager.Adapter(p["connection_id"])
		if e == nil {
			e = adapter.Import(jctx)
		}
	}
	state := "done"
	detail := ""
	runAt := time.Now()
	if e != nil {
		state = "failed"
		detail = "Operation failed"
		var se *provider.SendError
		if errors.As(e, &se) {
			detail = se.Error()
			if se.Uncertain {
				detail = "Provider acceptance unknown; no automatic resend"
			} else if !se.Permanent && attempts < 6 {
				state = "pending"
				delay := se.RetryAfter
				if delay == 0 {
					delay = time.Duration(1<<attempts) * time.Second
				}
				runAt = time.Now().Add(delay)
			}
		} else if (kind == "webhook" || kind == "meta") && attempts < 6 {
			state = "pending"
			runAt = time.Now().Add(time.Duration(1<<attempts) * 10 * time.Second)
		}
		if kind == "send" && state == "failed" {
			status := "failed"
			if errors.As(e, &se) && se.Uncertain {
				status = "unknown"
			}
			_, _ = a.Store.DB.Exec(ctx, `UPDATE messages SET status=$2 WHERE id=$1`, p["message_id"], status)
			_ = a.Store.Emit(ctx, w, "message.status", map[string]string{"id": p["message_id"], "connection_id": p["connection_id"], "status": status})
		}
		if kind == "webhook" && state == "failed" {
			_, _ = a.Store.DB.Exec(ctx, `UPDATE webhook_deliveries SET state='failed' WHERE id=$1`, p["delivery_id"])
		}
		if kind == "import" {
			_ = a.Store.Emit(ctx, w, "history.import", map[string]string{"connection_id": p["connection_id"], "status": "failed"})
		}
	}
	_, _ = a.Store.DB.Exec(ctx, `UPDATE jobs SET state=$2,error=$3,run_at=$4,locked_at=NULL,payload=CASE WHEN kind='meta' AND $2='done' THEN '{}'::jsonb ELSE payload END WHERE id=$1`, id, state, detail, runAt)
	return state != "pending"
}
func (a *Server) dispatch(ctx context.Context, conn, id, w string) error {
	c, e := a.Store.Connection(ctx, conn, w)
	if e != nil {
		return &provider.SendError{Err: errors.New("connection was removed"), Permanent: true}
	}
	if a.Config.StopSends || c.Status != "connected" {
		return &provider.SendError{Err: errors.New("connection paused or unavailable"), RetryAfter: 30 * time.Second}
	}
	if c.Provider == "whatsapp_web" {
		var next *time.Time
		if e = a.Store.DB.QueryRow(ctx, `SELECT next_send_at FROM connections WHERE id=$1`, conn).Scan(&next); e != nil {
			return e
		}
		if next != nil && time.Until(*next) > 0 {
			return &provider.SendError{Err: errors.New("reply throttle"), RetryAfter: time.Until(*next)}
		}
	}
	var s provider.Send
	var mediaID string
	var meta []byte
	var inbound *time.Time
	e = a.Store.DB.QueryRow(ctx, `SELECT m.id,c.external_id,m.text,m.kind,m.reply_to,COALESCE(m.media_id::text,''),m.metadata,c.last_inbound_at FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE m.id=$1 AND m.workspace_id=$2 AND m.status='queued'`, id, w).Scan(&s.ID, &s.ChatID, &s.Text, &s.Kind, &s.ReplyTo, &mediaID, &meta, &inbound)
	if e != nil {
		return &provider.SendError{Err: errors.New("queued message unavailable"), Permanent: true}
	}
	var metadata struct {
		Template json.RawMessage `json:"template"`
	}
	_ = json.Unmarshal(meta, &metadata)
	s.Template = metadata.Template
	if c.Provider == "whatsapp_web" && inbound == nil {
		return &provider.SendError{Err: errors.New("unofficial WhatsApp is reply-only"), Permanent: true}
	}
	if (c.Provider == "whatsapp_cloud" && len(s.Template) == 0 || c.Provider == "instagram") && (inbound == nil || time.Since(*inbound) > 24*time.Hour) {
		return &provider.SendError{Err: errors.New("reply window closed before dispatch"), Permanent: true}
	}
	if mediaID != "" {
		m, e := a.Store.Media(ctx, mediaID, w)
		if e != nil {
			return &provider.SendError{Err: errors.New("attachment unavailable"), Permanent: true}
		}
		s.Media = &m
	}
	adapter, e := a.Manager.Adapter(conn)
	if e != nil {
		return &provider.SendError{Err: errors.New("provider not running"), RetryAfter: 30 * time.Second}
	}
	if c.Provider == "whatsapp_web" {
		if _, e = a.Store.DB.Exec(ctx, `UPDATE connections SET next_send_at=now()+make_interval(secs=>$2) WHERE id=$1`, conn, a.Config.WAInterval); e != nil {
			return e
		}
	}
	ext, e := adapter.Send(ctx, s)
	if e != nil {
		var se *provider.SendError
		if errors.As(e, &se) && se.Uncertain {
			var status string
			if a.Store.DB.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, id).Scan(&status) == nil && (status == "sent" || status == "delivered" || status == "read") {
				return nil
			}
		}
		return e
	}
	tx, e := a.Store.DB.Begin(ctx)
	if e != nil {
		return &provider.SendError{Err: e, Uncertain: true}
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `DELETE FROM messages WHERE connection_id=$1 AND external_id=$2 AND id<>$3`, conn, ext, id); e != nil {
		return &provider.SendError{Err: e, Uncertain: true}
	}
	if _, e = tx.Exec(ctx, `UPDATE messages SET external_id=$2,status=CASE WHEN status='read' OR status='delivered' THEN status ELSE 'sent' END WHERE id=$1`, id, ext); e != nil {
		return &provider.SendError{Err: e, Uncertain: true}
	}
	if _, e = tx.Exec(ctx, `UPDATE conversations SET last_message_at=now() WHERE id=(SELECT conversation_id FROM messages WHERE id=$1)`, id); e != nil {
		return &provider.SendError{Err: e, Uncertain: true}
	}
	if e = a.Store.EmitTx(ctx, tx, w, "message.status", map[string]string{"id": id, "connection_id": conn, "external_id": ext, "status": "sent"}); e != nil {
		return &provider.SendError{Err: e, Uncertain: true}
	}
	if e = tx.Commit(ctx); e != nil {
		return &provider.SendError{Err: e, Uncertain: true}
	}
	return nil
}
