package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"stepline/messaging-service/internal/provider"
	"stepline/messaging-service/internal/secure"
	"stepline/messaging-service/internal/store"
	"stepline/messaging-service/migrations"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	u, e := url.Parse(dsn)
	if e != nil || u.Path != "/messaging_service_test" {
		t.Fatal("tests require messaging_service_test database")
	}
	ctx := context.Background()
	base, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "app_" + secure.Hash(secure.Token())[:12]
	_, e = base.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, e := pgxpool.New(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = base.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		base.Close()
	})
	if e = migrations.Run(ctx, db); e != nil {
		t.Fatal(e)
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	vault, _ := secure.NewVault(key)
	s := &store.Store{DB: db, Vault: vault, MediaDir: t.TempDir()}
	if e = Bootstrap(ctx, s, "admin@example.com", "Admin", "long-test-password"); e != nil {
		t.Fatal(e)
	}
	a := New(ctx, s, Config{DSN: u.String(), Key: key, Origin: "http://localhost:5173", MetaSecret: "test-meta-secret", WAInterval: 15})
	t.Cleanup(a.Manager.Close)
	return a
}
func request(a *Server, method, path string, body any, cookie *http.Cookie, token string) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Origin", a.Config.Origin)
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	return w
}
func loginCookie(t *testing.T, a *Server) *http.Cookie {
	w := request(a, "POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "long-test-password"}, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}
func TestTeamAuthorizationAndKeys(t *testing.T) {
	a := testApp(t)
	cookie := loginCookie(t, a)
	if cookie.HttpOnly != true || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe session cookie")
	}
	w := request(a, "GET", "/api/v1/me", nil, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(a, "POST", "/api/v1/api-keys", map[string]string{"name": "CRM"}, cookie, "")
	var key struct{ ID, Token string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &key) != nil {
		t.Fatal(w.Body.String())
	}
	w = request(a, "GET", "/api/v1/conversations", nil, nil, key.Token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(a, "POST", "/api/v1/api-keys", map[string]string{"name": "bad"}, nil, key.Token)
	if w.Code != 403 {
		t.Fatal("CRM key administered workspace", w.Code)
	}
	w = request(a, "GET", "/api/v1/api-keys", nil, cookie, "")
	if strings.Contains(w.Body.String(), key.Token) {
		t.Fatal("secret returned in key listing")
	}
	w = request(a, "POST", "/api/v1/invitations", map[string]string{"email": "member@example.com", "role": "member"}, cookie, "")
	var invite struct{ URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &invite)
	token := strings.Split(invite.URL, "#invite=")[1]
	w = request(a, "POST", "/api/v1/auth/accept-invitation", map[string]string{"token": token, "password": "another-long-password", "name": "Member"}, nil, "")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	member := w.Result().Cookies()[0]
	w = request(a, "POST", "/api/v1/connections", map[string]string{"provider": "telegram", "label": "bad"}, member, "")
	if w.Code != 403 {
		t.Fatal("member administered accounts")
	}
	w = request(a, "POST", "/api/v1/auth/accept-invitation", map[string]string{"token": token, "password": "another-long-password", "name": "Member"}, nil, "")
	if w.Code != 400 {
		t.Fatal("invitation reusable")
	}
	w = request(a, "DELETE", "/api/v1/api-keys/"+key.ID, nil, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if request(a, "GET", "/api/v1/conversations", nil, nil, key.Token).Code != 401 {
		t.Fatal("revoked key accepted")
	}
}

type fakeAdapter struct {
	sent int
	err  error
}

func (f *fakeAdapter) Start(ctx context.Context) error { <-ctx.Done(); return nil }
func (f *fakeAdapter) Send(context.Context, provider.Send) (string, error) {
	f.sent++
	return "external-out", f.err
}
func (f *fakeAdapter) Edit(context.Context, provider.Send) error   { return nil }
func (f *fakeAdapter) Delete(context.Context, provider.Send) error { return nil }
func (f *fakeAdapter) Disconnect(context.Context) error            { return nil }
func (f *fakeAdapter) Challenge(context.Context, map[string]string) (map[string]any, error) {
	return nil, nil
}
func (f *fakeAdapter) Import(context.Context) error { return nil }
func TestDurableSendDedupAndReceipts(t *testing.T) {
	a := testApp(t)
	cookie := loginCookie(t, a)
	ctx := context.Background()
	c := provider.Connection{ID: secure.ID(), WorkspaceID: store.Workspace, Provider: "whatsapp_web", Status: "connected"}
	_, e := a.Store.DB.Exec(ctx, `INSERT INTO connections(id,workspace_id,provider,label,status) VALUES($1,$2,$3,'Test','connected')`, c.ID, c.WorkspaceID, c.Provider)
	if e != nil {
		t.Fatal(e)
	}
	ev := provider.Event{ExternalID: "incoming", ChatID: "15550000002@s.whatsapp.net", Text: "Hello", At: time.Now()}
	if e = a.Store.Ingest(ctx, c, ev); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Ingest(ctx, c, ev); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = a.Store.DB.QueryRow(ctx, `SELECT count(*) FROM messages`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate inbound persisted")
	}
	var conv string
	_ = a.Store.DB.QueryRow(ctx, `SELECT id FROM conversations`).Scan(&conv)
	id := secure.ID()
	send := func(text string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"text": text})
		r := httptest.NewRequest("POST", "/api/v1/conversations/"+conv+"/messages", bytes.NewReader(b))
		r.AddCookie(cookie)
		r.Header.Set("Origin", a.Config.Origin)
		r.Header.Set("Idempotency-Key", id)
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		return w
	}
	if w := send("Reply"); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("Reply"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("Different"); w.Code != 409 {
		t.Fatal("idempotency mismatch accepted", w.Code)
	}
	f := &fakeAdapter{}
	a.Manager.items[c.ID] = &live{adapter: f, cancel: func() {}, done: make(chan struct{})}
	if !a.runJob(ctx) {
		t.Fatal("job not claimed")
	}
	if f.sent != 1 {
		t.Fatal("send not dispatched once")
	}
	if e = a.Store.Ingest(ctx, c, provider.Event{ReceiptIDs: []string{"external-out"}, Status: "read"}); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Ingest(ctx, c, provider.Event{ReceiptIDs: []string{"external-out"}, Status: "delivered"}); e != nil {
		t.Fatal(e)
	}
	var status string
	_ = a.Store.DB.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, id).Scan(&status)
	if status != "read" {
		t.Fatal("receipt regressed", status)
	}
}
func TestMetaWebhookVerificationAndPersistence(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	id := secure.ID()
	_, e := a.Store.DB.Exec(ctx, `INSERT INTO connections(id,workspace_id,provider,label,status,external_id) VALUES($1,$2,'whatsapp_cloud','Official','connected','123')`, id, store.Workspace)
	if e != nil {
		t.Fatal(e)
	}
	body := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"456","changes":[{"value":{"metadata":{"phone_number_id":"123"},"messages":[{"id":"wamid.test","from":"15550000003","timestamp":"1780000000","type":"text","text":{"body":"private webhook text"}}]}}]}]}`)
	send := func(sig string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/provider-webhooks/meta", bytes.NewReader(body))
		r.Header.Set("X-Hub-Signature-256", sig)
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		return w
	}
	if send("bad").Code != 403 {
		t.Fatal("forged signature accepted")
	}
	for i := 0; i < 2; i++ {
		if w := send(signature(a.Config.MetaSecret, body)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var count int
	var payload []byte
	_ = a.Store.DB.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate webhook job")
	}
	_ = a.Store.DB.QueryRow(ctx, `SELECT payload FROM jobs`).Scan(&payload)
	if bytes.Contains(payload, []byte("private webhook text")) {
		t.Fatal("raw webhook stored unencrypted")
	}
	a.runJob(ctx)
	_ = a.Store.DB.QueryRow(ctx, `SELECT count(*) FROM messages WHERE external_id='wamid.test'`).Scan(&count)
	if count != 1 {
		var detail string
		_ = a.Store.DB.QueryRow(ctx, `SELECT error FROM jobs`).Scan(&detail)
		t.Fatal("webhook ingestion failed", detail)
	}
	var m provider.Media
	m.Name = "a.txt"
	m.MIME = "text/plain"
	m.Data = []byte("private attachment")
	mid, e := a.Store.SaveMedia(ctx, store.Workspace, id, m)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := a.Store.Media(ctx, mid, store.Workspace)
	if e != nil || !bytes.Equal(m.Data, stored.Data) {
		t.Fatal("attachment encryption failed", e)
	}
	w := request(a, "GET", "/api/v1/media/"+mid, nil, nil, "")
	if w.Code != 401 {
		t.Fatal("unauthenticated media access")
	}
	_ = fmt.Sprint(mid)
}

func TestOutOfOrderMutationAndPausedMeta(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	c := provider.Connection{ID: secure.ID(), WorkspaceID: store.Workspace, Provider: "telegram"}
	_, e := a.Store.DB.Exec(ctx, `INSERT INTO connections(id,workspace_id,provider,label,status) VALUES($1,$2,'telegram','Mutation test','connected')`, c.ID, c.WorkspaceID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Ingest(ctx, c, provider.Event{ExternalID: "later", Delete: true, At: time.Now()}); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Ingest(ctx, c, provider.Event{ExternalID: "later", ChatID: "user:1", Text: "must stay deleted"}); e != nil {
		t.Fatal(e)
	}
	var text string
	var deleted bool
	e = a.Store.DB.QueryRow(ctx, `SELECT text,deleted_at IS NOT NULL FROM messages WHERE external_id='later'`).Scan(&text, &deleted)
	if e != nil || text != "" || !deleted {
		t.Fatal("late message resurrected deleted content", e)
	}
	if e = a.Store.Ingest(ctx, c, provider.Event{ExternalID: "edit-first", Edit: true, Text: "edited text", At: time.Now()}); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Ingest(ctx, c, provider.Event{ExternalID: "edit-first", ChatID: "user:1", Text: "original"}); e != nil {
		t.Fatal(e)
	}
	e = a.Store.DB.QueryRow(ctx, `SELECT text FROM messages WHERE external_id='edit-first'`).Scan(&text)
	if e != nil || text != "edited text" {
		t.Fatal("out-of-order edit lost", e)
	}
	id := secure.ID()
	_, e = a.Store.DB.Exec(ctx, `INSERT INTO connections(id,workspace_id,provider,label,status,external_id) VALUES($1,$2,'instagram','Paused','paused','456')`, id, store.Workspace)
	if e != nil {
		t.Fatal(e)
	}
	body := []byte(`{"object":"instagram","entry":[{"id":"456","messaging":[{"sender":{"id":"789"},"recipient":{"id":"456"},"timestamp":1780000000000,"message":{"mid":"ig-paused","text":"inbound while paused"}}]}]}`)
	jobID := secure.ID()
	payload, _ := json.Marshal(map[string]any{"body": a.Store.Vault.Seal(body, "meta-job:"+jobID)})
	if e = a.processMeta(ctx, jobID, payload); e != nil {
		t.Fatal(e)
	}
	var count int
	_ = a.Store.DB.QueryRow(ctx, `SELECT count(*) FROM messages WHERE external_id='ig-paused'`).Scan(&count)
	if count != 1 {
		t.Fatal("paused official connector discarded inbound event")
	}
}

func TestUncertainSendIsNotRetried(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	cookie := loginCookie(t, a)
	c := provider.Connection{ID: secure.ID(), WorkspaceID: store.Workspace, Provider: "telegram"}
	_, e := a.Store.DB.Exec(ctx, `INSERT INTO connections(id,workspace_id,provider,label,status) VALUES($1,$2,'telegram','Uncertainty test','connected')`, c.ID, c.WorkspaceID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Ingest(ctx, c, provider.Event{ExternalID: "first", ChatID: "user:1", Text: "hello"}); e != nil {
		t.Fatal(e)
	}
	var conv string
	_ = a.Store.DB.QueryRow(ctx, `SELECT id FROM conversations WHERE connection_id=$1`, c.ID).Scan(&conv)
	id := secure.ID()
	body, _ := json.Marshal(map[string]string{"text": "reply"})
	r := httptest.NewRequest("POST", "/api/v1/conversations/"+conv+"/messages", bytes.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Origin", a.Config.Origin)
	r.Header.Set("Idempotency-Key", id)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	f := &fakeAdapter{err: &provider.SendError{Err: errors.New("transport failed"), Uncertain: true}}
	a.Manager.items[c.ID] = &live{adapter: f, cancel: func() {}, done: make(chan struct{})}
	a.runJob(ctx)
	var status string
	_ = a.Store.DB.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, id).Scan(&status)
	if status != "unknown" {
		t.Fatal("uncertainty hidden", status)
	}
	a.runJob(ctx)
	if f.sent != 1 {
		t.Fatal("ambiguous send was retried")
	}
}
