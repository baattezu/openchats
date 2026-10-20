package provider

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/base64"
	"github.com/jackc/pgx/v5"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"net/url"
	"os"
	"stepline/messaging-service/internal/secure"
	"testing"
	"time"
)

func TestSecretBindings(t *testing.T) {
	v, _ := secure.NewVault(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	args := []driver.NamedValue{{Ordinal: 1, Value: "me"}, {Ordinal: 2, Value: "them"}, {Ordinal: 3, Value: []byte("secret")}}
	out, e := sealArgs(v, `INSERT INTO whatsmeow_sessions (our_jid,their_id,session) VALUES ($1,$2,$3) ON CONFLICT (our_jid,their_id) DO UPDATE SET session=excluded.session`, args)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(out[2].Value.([]byte), args[2].Value.([]byte)) {
		t.Fatal("unsealed binding")
	}
	p, e := v.Open(out[2].Value.([]byte), "whatsmeow_sessions.session")
	if e != nil || string(p) != "secret" {
		t.Fatal(e)
	}
	_, e = sealArgs(v, `INSERT INTO whatsmeow_sessions (our_jid,their_id,session) VALUES ($1,$2,decode($3,'hex'))`, args)
	if e == nil {
		t.Fatal("unrecognized secret expression accepted")
	}
}
func TestWAEncryptedPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	u, _ := url.Parse(dsn)
	if u.Path != "/messaging_service_test" {
		t.Fatal("integration tests require messaging_service_test database")
	}
	ctx := context.Background()
	db, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close(ctx)
	schema := "wa_" + secure.Hash(secure.Token())[:12]
	if _, e = db.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	defer db.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	v, _ := secure.NewVault(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	s, e := NewWAStore(ctx, u.String(), v)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d := s.NewDevice()
	jid := types.NewADJID("15550000001", 0, 1)
	d.ID = &jid
	d.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if e = d.Save(ctx); e != nil {
		t.Fatal(e)
	}
	d2, e := s.GetDevice(ctx, jid)
	if e != nil {
		t.Fatal(e)
	}
	if *d2.NoiseKey.Priv != *d.NoiseKey.Priv {
		t.Fatal("device key round-trip failed")
	}
	if e = d2.Sessions.PutSession(ctx, "peer", []byte("session-secret")); e != nil {
		t.Fatal(e)
	}
	b, e := d2.Sessions.GetSession(ctx, "peer")
	if e != nil || string(b) != "session-secret" {
		t.Fatal("session round-trip failed", e)
	}
	var ciphertext []byte
	e = db.QueryRow(ctx, "SELECT noise_key FROM "+pgx.Identifier{schema, "whatsmeow_device"}.Sanitize()).Scan(&ciphertext)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(ciphertext, d.NoiseKey.Priv[:]) || len(ciphertext) == 32 {
		t.Fatal("private key is not encrypted")
	}
	if e = d2.Identities.PutIdentity(ctx, "identity-peer", [32]byte{1}); e != nil {
		t.Fatal(e)
	}
	trusted, e := d2.Identities.IsTrustedIdentity(ctx, "identity-peer", [32]byte{1})
	if e != nil || !trusted {
		t.Fatal("identity round-trip failed", e)
	}
	keys, e := d2.PreKeys.GetOrGenPreKeys(ctx, 2)
	if e != nil {
		t.Fatal(e)
	}
	if e = d2.PreKeys.MarkPreKeysAsUploaded(ctx, keys[len(keys)-1].KeyID); e != nil {
		t.Fatal(e)
	}
	keyID := []byte{1}
	if e = d2.AppStateKeys.PutAppStateSyncKey(ctx, keyID, store.AppStateSyncKey{Data: []byte("sync-key"), Fingerprint: []byte("fingerprint"), Timestamp: 1}); e != nil {
		t.Fatal(e)
	}
	syncKey, e := d2.AppStateKeys.GetAppStateSyncKey(ctx, keyID)
	if e != nil || string(syncKey.Data) != "sync-key" {
		t.Fatal("sync key encryption failed", e)
	}
	if e = d2.AppState.PutAppStateVersion(ctx, "critical", 1, [128]byte{1}); e != nil {
		t.Fatal(e)
	}
	if e = d2.AppState.PutAppStateMutationMACs(ctx, "critical", 1, []store.AppStateMutationMAC{{IndexMAC: make([]byte, 32), ValueMAC: bytes.Repeat([]byte{2}, 32)}}); e != nil {
		t.Fatal(e)
	}
	mac, e := d2.AppState.GetAppStateMutationMAC(ctx, "critical", make([]byte, 32))
	if e != nil || !bytes.Equal(mac, bytes.Repeat([]byte{2}, 32)) {
		t.Fatal("mutation encryption failed", e)
	}
	if e = d2.SenderKeys.PutSenderKey(ctx, "group", "user", []byte("sender-key")); e != nil {
		t.Fatal(e)
	}
	sender, e := d2.SenderKeys.GetSenderKey(ctx, "group", "user")
	if e != nil || string(sender) != "sender-key" {
		t.Fatal("sender key encryption failed", e)
	}
	if e = d2.NCTSalt.PutNCTSalt(ctx, []byte("salt")); e != nil {
		t.Fatal(e)
	}
	if e = d2.EventBuffer.DoDecryptionTxn(ctx, func(txctx context.Context) error {
		return d2.EventBuffer.PutBufferedEvent(txctx, [32]byte{1}, []byte("buffer plaintext"), time.Now())
	}); e != nil {
		t.Fatal(e)
	}
	buffered, e := d2.EventBuffer.GetBufferedEvent(ctx, [32]byte{1})
	if e != nil || string(buffered.Plaintext) != "buffer plaintext" {
		t.Fatal("buffer encryption failed", e)
	}
	if e = d2.EventBuffer.ClearBufferedEventPlaintext(ctx, [32]byte{1}); e != nil {
		t.Fatal(e)
	}
	if e = d2.EventBuffer.AddOutgoingEvent(ctx, jid, "out", "protobuf", []byte("retry plaintext")); e != nil {
		t.Fatal(e)
	}
	_, retry, e := d2.EventBuffer.GetOutgoingEvent(ctx, jid, types.EmptyJID, "out")
	if e != nil || string(retry) != "retry plaintext" {
		t.Fatal("retry buffer encryption failed", e)
	}
	if e = d2.Delete(ctx); e != nil {
		t.Fatal(e)
	}
}
