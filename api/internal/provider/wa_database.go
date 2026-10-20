package provider

// The upstream SQL store is retained, but its secret bytea values are sealed at
// the database/sql boundary. Indexed hashes/key IDs remain queryable. This adapter
// is deliberately restricted to the pinned upstream SQL grammar and fails closed
// for secret INSERTs it cannot classify.
import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"stepline/messaging-service/internal/secure"
)

var secretColumns = map[string]map[string]bool{
	"whatsmeow_device":        {"noise_key": true, "identity_key": true, "signed_pre_key": true, "signed_pre_key_sig": true, "adv_key": true, "adv_details": true, "adv_account_sig": true, "adv_account_sig_key": true, "adv_device_sig": true},
	"whatsmeow_identity_keys": {"identity": true}, "whatsmeow_pre_keys": {"key": true}, "whatsmeow_sessions": {"session": true}, "whatsmeow_sender_keys": {"sender_key": true}, "whatsmeow_app_state_sync_keys": {"key_data": true, "fingerprint": true}, "whatsmeow_app_state_version": {"hash": true}, "whatsmeow_app_state_mutation_macs": {"value_mac": true}, "whatsmeow_message_secrets": {"key": true}, "whatsmeow_privacy_tokens": {"token": true}, "whatsmeow_nct_salt": {"salt": true}, "whatsmeow_event_buffer": {"plaintext": true}, "whatsmeow_retry_buffer": {"plaintext": true},
}
var insertRE = regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+(whatsmeow_\w+)\s*\(([^)]+)\)\s*VALUES\s*(.*)`)
var valuesRE = regexp.MustCompile(`\(([^()]+)\)`)
var tableRE = regexp.MustCompile(`(?i)\b(?:FROM|INTO|UPDATE)\s+(whatsmeow_\w+)`)
var secretWriteRE = regexp.MustCompile(`(?is)^\s*(?:INSERT\s+INTO|UPDATE)\s+(whatsmeow_\w+)`)

type encryptedConnector struct {
	driver.Connector
	v *secure.Vault
}

func (c encryptedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	d, e := c.Connector.Connect(ctx)
	if e != nil {
		return nil, e
	}
	return &encryptedConn{Conn: d, v: c.v}, nil
}

type encryptedConn struct {
	driver.Conn
	v *secure.Vault
}

func (c *encryptedConn) CheckNamedValue(v *driver.NamedValue) error {
	if n, ok := c.Conn.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
func (c *encryptedConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}
func (c *encryptedConn) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }
func (c *encryptedConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c *encryptedConn) Prepare(query string) (driver.Stmt, error) {
	return nil, fmt.Errorf("prepared queries are unsupported in encrypted whatsmeow store")
}
func sealArgs(v *secure.Vault, q string, a []driver.NamedValue) ([]driver.NamedValue, error) {
	m := insertRE.FindStringSubmatch(q)
	if m == nil {
		if write := secretWriteRE.FindStringSubmatch(q); write != nil && len(secretColumns[strings.ToLower(write[1])]) > 0 {
			normal := strings.Join(strings.Fields(q), " ")
			// Pinned SDK migrations move existing ciphertext between peer addresses.
			copies := []string{
				"INSERT INTO whatsmeow_sessions (our_jid, their_id, session) SELECT our_jid, replace(their_id, $2, $3), session FROM whatsmeow_sessions",
				"INSERT INTO whatsmeow_identity_keys (our_jid, their_id, identity) SELECT our_jid, replace(their_id, $2, $3), identity FROM whatsmeow_identity_keys",
				"INSERT INTO whatsmeow_sender_keys (our_jid, chat_id, sender_id, sender_key) SELECT our_jid, chat_id, replace(sender_id, $2, $3), sender_key FROM whatsmeow_sender_keys",
			}
			allowed := normal == "UPDATE whatsmeow_event_buffer SET plaintext = NULL WHERE our_jid = $1 AND ciphertext_hash = $2" || normal == "UPDATE whatsmeow_pre_keys SET uploaded=true WHERE jid=$1 AND key_id<=$2"
			for _, prefix := range copies {
				if strings.HasPrefix(normal, prefix) {
					allowed = true
				}
			}
			if !allowed {
				return nil, fmt.Errorf("unrecognized WhatsApp secret write")
			}
		}
		return a, nil
	}
	m[1] = strings.ToLower(m[1])
	columns := strings.Split(m[2], ",")
	secrets := secretColumns[m[1]]
	out := append([]driver.NamedValue(nil), a...)
	tail := strings.Split(strings.ToUpper(m[3]), "ON CONFLICT")[0]
	groups := valuesRE.FindAllStringSubmatch(tail, -1)
	for _, g := range groups {
		parts := strings.Split(g[1], ",")
		if len(parts) != len(columns) {
			return nil, fmt.Errorf("unrecognized WhatsApp insert")
		}
		for i, p := range parts {
			col := strings.ToLower(strings.TrimSpace(columns[i]))
			if !secrets[col] {
				continue
			}
			n, e := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(p), "$"))
			if e != nil || n < 1 || n > len(out) {
				return nil, fmt.Errorf("unrecognized WhatsApp secret binding")
			}
			if out[n-1].Value == nil {
				continue
			}
			b, ok := out[n-1].Value.([]byte)
			if !ok {
				return nil, fmt.Errorf("WhatsApp secret must be bytea")
			}
			out[n-1].Value = v.Seal(b, m[1]+"."+col)
		}
	}
	return out, nil
}
func (c *encryptedConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	a, e := sealArgs(c.v, q, a)
	if e != nil {
		return nil, e
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, a)
}
func (c *encryptedConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	a, e := sealArgs(c.v, q, a)
	if e != nil {
		return nil, e
	}
	r, e := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, a)
	if e != nil {
		return nil, e
	}
	table := ""
	if m := tableRE.FindStringSubmatch(q); m != nil {
		table = m[1]
	}
	return &encryptedRows{Rows: r, v: c.v, table: table}, nil
}

type encryptedRows struct {
	driver.Rows
	v     *secure.Vault
	table string
}

func (r *encryptedRows) Next(dest []driver.Value) error {
	if e := r.Rows.Next(dest); e != nil {
		return e
	}
	for i, col := range r.Columns() {
		if !secretColumns[r.table][col] || dest[i] == nil {
			continue
		}
		b, ok := dest[i].([]byte)
		if !ok {
			return fmt.Errorf("invalid WhatsApp ciphertext")
		}
		p, e := r.v.Open(b, r.table+"."+col)
		if e != nil {
			return e
		}
		dest[i] = p
	}
	return nil
}

func NewWAStore(ctx context.Context, dsn string, v *secure.Vault) (*sqlstore.Container, error) {
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		return nil, e
	}
	db := sql.OpenDB(encryptedConnector{stdlib.GetConnector(*cfg), v})
	wrapped := sqlstore.NewWithDB(db, "postgres", nil)
	if e = wrapped.Upgrade(ctx); e != nil {
		db.Close()
		return nil, e
	}
	// Upstream length checks refer to plaintext lengths; replace those checks only
	// on encrypted columns. Other constraints and foreign keys remain intact.
	rows, e := db.QueryContext(ctx, `SELECT c.conname,t.relname,pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE c.contype='c' AND n.nspname=current_schema() AND t.relname LIKE 'whatsmeow_%'`)
	if e != nil {
		return nil, e
	}
	type constraint struct{ name, table, definition string }
	var all []constraint
	for rows.Next() {
		var c constraint
		if e = rows.Scan(&c.name, &c.table, &c.definition); e != nil {
			rows.Close()
			return nil, e
		}
		all = append(all, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, c := range all {
		for col := range secretColumns[c.table] {
			if strings.Contains(c.definition, "length("+col+")") {
				if _, e = db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s DROP CONSTRAINT %s`, pgx.Identifier{c.table}.Sanitize(), pgx.Identifier{c.name}.Sanitize())); e != nil {
					return nil, e
				}
				break
			}
		}
	}
	return wrapped, nil
}
