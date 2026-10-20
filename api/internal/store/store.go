package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"stepline/messaging-service/internal/provider"
	"stepline/messaging-service/internal/secure"
)

const Workspace = "00000000-0000-0000-0000-000000000001"

type Store struct {
	DB       *pgxpool.Pool
	Vault    *secure.Vault
	MediaDir string
}

func (s *Store) JSON(ctx context.Context, q string, a ...any) (json.RawMessage, error) {
	var b []byte
	e := s.DB.QueryRow(ctx, q, a...).Scan(&b)
	return b, e
}
func (s *Store) Audit(ctx context.Context, w, actor, action, id string) {
	_, _ = s.DB.Exec(ctx, `INSERT INTO audit(id,workspace_id,actor,action,resource_id) VALUES($1,$2,$3,$4,$5)`, secure.ID(), w, actor, action, id)
}
func (s *Store) Connection(ctx context.Context, id, w string) (provider.Connection, error) {
	var c provider.Connection
	e := s.DB.QueryRow(ctx, `SELECT id,workspace_id,provider,label,identity,status,detail,COALESCE(external_id,'') FROM connections WHERE id=$1 AND workspace_id=$2`, id, w).Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.Label, &c.Identity, &c.Status, &c.Detail, &c.ExternalID)
	c.Capabilities = provider.Caps(c.Provider)
	return c, e
}
func (s *Store) LoadBlob(ctx context.Context, id, col string) ([]byte, error) {
	var b []byte
	switch col {
	case "credentials", "session_data", "checkpoint":
	default:
		return nil, errors.New("invalid blob")
	}
	e := s.DB.QueryRow(ctx, `SELECT `+col+` FROM connections WHERE id=$1`, id).Scan(&b)
	if e != nil || len(b) == 0 {
		return nil, e
	}
	return s.Vault.Open(b, id+":"+col)
}
func (s *Store) SaveBlob(ctx context.Context, id, col string, b []byte) error {
	switch col {
	case "credentials", "session_data", "checkpoint":
	default:
		return errors.New("invalid blob")
	}
	_, e := s.DB.Exec(ctx, `UPDATE connections SET `+col+`=$2 WHERE id=$1`, id, s.Vault.Seal(b, id+":"+col))
	return e
}
func (s *Store) Credentials(ctx context.Context, id string) (map[string]string, error) {
	b, e := s.LoadBlob(ctx, id, "credentials")
	if e != nil {
		return nil, e
	}
	m := map[string]string{}
	if len(b) > 0 {
		e = json.Unmarshal(b, &m)
	}
	return m, e
}
func (s *Store) SaveMedia(ctx context.Context, w, conn string, m provider.Media) (string, error) {
	return s.saveMedia(ctx, w, conn, m, s.DB)
}

type mediaExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (s *Store) saveMedia(ctx context.Context, w, conn string, m provider.Media, db mediaExecer) (string, error) {
	if len(m.Data) > 25<<20 {
		return "", errors.New("attachment exceeds 25 MiB")
	}
	id := secure.ID()
	path := filepath.Join(s.MediaDir, id)
	if e := os.MkdirAll(s.MediaDir, 0700); e != nil {
		return "", e
	}
	if e := os.WriteFile(path, s.Vault.Seal(m.Data, "media:"+id), 0600); e != nil {
		return "", e
	}
	_, e := db.Exec(ctx, `INSERT INTO media(id,workspace_id,connection_id,name,mime,size,path) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7)`, id, w, conn, filepath.Base(m.Name), m.MIME, len(m.Data), id)
	if e != nil {
		os.Remove(path)
	}
	return id, e
}
func (s *Store) Media(ctx context.Context, id, w string) (provider.Media, error) {
	var m provider.Media
	var path string
	e := s.DB.QueryRow(ctx, `SELECT id,name,mime,path FROM media WHERE id=$1 AND workspace_id=$2`, id, w).Scan(&m.ID, &m.Name, &m.MIME, &path)
	if e != nil {
		return m, e
	}
	b, e := os.ReadFile(filepath.Join(s.MediaDir, path))
	if e != nil {
		return m, e
	}
	m.Data, e = s.Vault.Open(b, "media:"+id)
	return m, e
}
func emit(ctx context.Context, tx pgx.Tx, w, kind string, data any) error {
	b, e := json.Marshal(data)
	if e != nil {
		return e
	}
	id := secure.ID()
	if _, e = tx.Exec(ctx, `INSERT INTO events(id,workspace_id,type,data) VALUES($1,$2,$3,$4)`, id, w, kind, b); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, `INSERT INTO webhook_deliveries(id,workspace_id,subscription_id,event_id) SELECT gen_random_uuid(),workspace_id,id,$1 FROM webhook_subscriptions WHERE workspace_id=$2 AND enabled RETURNING id`, id, w)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var x string
		if e = rows.Scan(&x); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, d := range ids {
		p, _ := json.Marshal(map[string]string{"delivery_id": d})
		if _, e = tx.Exec(ctx, `INSERT INTO jobs(id,workspace_id,kind,payload) VALUES($1,$2,'webhook',$3)`, secure.ID(), w, p); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Emit(ctx context.Context, w, kind string, data any) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = emit(ctx, tx, w, kind, data); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// EmitTx makes downstream events atomic with their authoritative state change.
func (s *Store) EmitTx(ctx context.Context, tx pgx.Tx, w, kind string, data any) error {
	return emit(ctx, tx, w, kind, data)
}
func (s *Store) Ingest(ctx context.Context, c provider.Connection, v provider.Event) error {
	if v.At.IsZero() {
		v.At = time.Now()
	}
	if v.Direction == "" {
		v.Direction = "in"
	}
	if v.Kind == "" {
		v.Kind = "text"
	}
	if v.Status == "" {
		v.Status = "received"
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if v.ExternalID != "" {
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, c.ID+":"+v.ExternalID); e != nil {
			return e
		}
	}
	if len(v.ReceiptIDs) > 0 {
		for _, id := range v.ReceiptIDs {
			if _, e = tx.Exec(ctx, `UPDATE messages SET status=CASE WHEN status='read' AND $3<>'failed' THEN status WHEN status='delivered' AND $3='sent' THEN status ELSE $3 END WHERE connection_id=$1 AND external_id=$2`, c.ID, id, v.Status); e != nil {
				return e
			}
		}
		if e = emit(ctx, tx, c.WorkspaceID, "message.status", map[string]any{"connection_id": c.ID, "external_ids": v.ReceiptIDs, "status": v.Status}); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if v.Edit || v.Delete {
		_, e = tx.Exec(ctx, `INSERT INTO message_mutations(connection_id,external_id,edited_text,edited_at,deleted_at) VALUES($1,$2,CASE WHEN $4 THEN $3 ELSE NULL END,CASE WHEN $4 THEN $6::timestamptz ELSE NULL END,CASE WHEN $5 THEN $6::timestamptz ELSE NULL END) ON CONFLICT(connection_id,external_id) DO UPDATE SET edited_text=CASE WHEN excluded.edited_at IS NOT NULL AND (message_mutations.edited_at IS NULL OR excluded.edited_at>=message_mutations.edited_at) THEN excluded.edited_text ELSE message_mutations.edited_text END,edited_at=GREATEST(message_mutations.edited_at,excluded.edited_at),deleted_at=GREATEST(message_mutations.deleted_at,excluded.deleted_at)`, c.ID, v.ExternalID, v.Text, v.Edit, v.Delete, v.At)
		if e != nil {
			return e
		}
		var id string
		var text string
		var deleted bool
		e = tx.QueryRow(ctx, `UPDATE messages m SET text=CASE WHEN mm.deleted_at IS NOT NULL THEN '' ELSE COALESCE(mm.edited_text,m.text) END,edited_at=mm.edited_at,deleted_at=mm.deleted_at FROM message_mutations mm WHERE m.connection_id=$1 AND m.external_id=$2 AND mm.connection_id=m.connection_id AND mm.external_id=m.external_id RETURNING m.id,m.text,m.deleted_at IS NOT NULL`, c.ID, v.ExternalID).Scan(&id, &text, &deleted)
		if errors.Is(e, pgx.ErrNoRows) {
			return tx.Commit(ctx)
		}
		if e != nil {
			return e
		}
		if e = emit(ctx, tx, c.WorkspaceID, "message.changed", map[string]any{"id": id, "connection_id": c.ID, "deleted": deleted, "text": text}); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	var exists bool
	if v.ExternalID != "" {
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE connection_id=$1 AND external_id=$2)`, c.ID, v.ExternalID).Scan(&exists); e != nil {
			return e
		}
		if exists {
			if v.Direction == "out" {
				tag, err := tx.Exec(ctx, `UPDATE messages SET status='sent' WHERE connection_id=$1 AND external_id=$2 AND direction='out' AND status IN('queued','unknown','failed')`, c.ID, v.ExternalID)
				if err != nil {
					return err
				}
				if tag.RowsAffected() > 0 {
					if err = emit(ctx, tx, c.WorkspaceID, "message.status", map[string]any{"connection_id": c.ID, "external_ids": []string{v.ExternalID}, "status": "sent"}); err != nil {
						return err
					}
				}
			}
			return tx.Commit(ctx)
		}
	}
	var editedText *string
	var editedAt, deletedAt *time.Time
	e = tx.QueryRow(ctx, `SELECT edited_text,edited_at,deleted_at FROM message_mutations WHERE connection_id=$1 AND external_id=$2`, c.ID, v.ExternalID).Scan(&editedText, &editedAt, &deletedAt)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if deletedAt != nil {
		v.Text = ""
		v.Media = nil
	} else if editedText != nil {
		v.Text = *editedText
	}
	title := v.Title
	if title == "" {
		title = v.ChatID
	}
	var conv string
	e = tx.QueryRow(ctx, `INSERT INTO conversations(id,workspace_id,connection_id,external_id,title,is_group,last_message_at,last_inbound_at) VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $8='in' THEN $7::timestamptz ELSE NULL END) ON CONFLICT(connection_id,external_id) DO UPDATE SET last_message_at=GREATEST(conversations.last_message_at,excluded.last_message_at),last_inbound_at=CASE WHEN $8='in' THEN GREATEST(conversations.last_inbound_at,excluded.last_inbound_at) ELSE conversations.last_inbound_at END RETURNING id`, secure.ID(), c.WorkspaceID, c.ID, v.ChatID, title, v.Group, v.At, v.Direction).Scan(&conv)
	if e != nil {
		return e
	}
	mediaID := ""
	keepMedia := false
	defer func() {
		if mediaID != "" && !keepMedia {
			_ = os.Remove(filepath.Join(s.MediaDir, mediaID))
		}
	}()
	if v.Media != nil {
		mediaID, e = s.saveMedia(ctx, c.WorkspaceID, c.ID, *v.Media, tx)
		if e != nil {
			return e
		}
	}
	if len(v.Metadata) == 0 {
		v.Metadata = json.RawMessage(`{}`)
	}
	id := secure.ID()
	tag, e := tx.Exec(ctx, `INSERT INTO messages(id,workspace_id,conversation_id,connection_id,external_id,direction,sender,kind,text,media_id,status,reply_to,metadata,sent_at,edited_at,deleted_at) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,NULLIF($10,'')::uuid,$11,$12,$13,$14,$15,$16) ON CONFLICT(connection_id,external_id) DO NOTHING`, id, c.WorkspaceID, conv, c.ID, v.ExternalID, v.Direction, v.Sender, v.Kind, v.Text, mediaID, v.Status, v.ReplyTo, v.Metadata, v.At, editedAt, deletedAt)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if e = emit(ctx, tx, c.WorkspaceID, "message.created", map[string]any{"id": id, "conversation_id": conv, "connection_id": c.ID, "external_id": v.ExternalID, "direction": v.Direction, "kind": v.Kind, "text": v.Text, "media_id": mediaID, "sent_at": v.At}); e != nil {
		return e
	}
	keepMedia = true
	return tx.Commit(ctx)
}
