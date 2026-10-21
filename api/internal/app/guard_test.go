package app

import (
	"context"
	"encoding/json"
	"stepline/messaging-service/internal/provider"
	"stepline/messaging-service/internal/secure"
	"strings"
	"testing"
	"time"
)

func guardConnection(t *testing.T, a *Server, status string, session bool) provider.Connection {
	t.Helper()
	ctx := context.Background()
	id := secure.ID()
	if _, e := a.Store.DB.Exec(ctx, `INSERT INTO connections(id,workspace_id,provider,label,status) VALUES($1,$2,'whatsapp_web','WA',$3)`, id, "00000000-0000-0000-0000-000000000001", status); e != nil {
		t.Fatal(e)
	}
	if session {
		if e := a.Store.SaveBlob(ctx, id, "session_data", []byte("77000000000@s.whatsapp.net")); e != nil {
			t.Fatal(e)
		}
	}
	c, e := a.Store.Connection(ctx, id, "00000000-0000-0000-0000-000000000001")
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestGuardLimitsAndRelinkCooldown(t *testing.T) {
	a := testApp(t)
	a.Manager.cfg.WAMaxResumesPerHour = 2
	a.Manager.cfg.WAMaxPairingsPerHour = 1
	a.Manager.cfg.WARelinkCooldown = 24 * time.Hour
	ctx := context.Background()

	// Возобновление: два раза можно, третий — пауза до ручного действия.
	c := guardConnection(t, a, "connected", true)
	for i := 0; i < 2; i++ {
		if e := a.Manager.guardStart(ctx, c, "resume"); e != nil {
			t.Fatal(e)
		}
	}
	if e := a.Manager.guardStart(ctx, c, "resume"); e == nil {
		t.Fatal("resume loop was not stopped")
	}
	c, _ = a.Store.Connection(ctx, c.ID, c.WorkspaceID)
	if c.Status != "paused" {
		t.Fatal("crash loop must pause the connection, got", c.Status)
	}

	// Привязка: лимит попыток в час.
	p := guardConnection(t, a, "needs_auth", false)
	if e := a.Manager.guardStart(ctx, p, "pair"); e != nil {
		t.Fatal(e)
	}
	if e := a.Manager.guardStart(ctx, p, "pair"); e == nil {
		t.Fatal("pairing attempts are not limited")
	}

	// После отвязки новая привязка запрещена для всего пространства, даже на новом подключении.
	a.Manager.blockPairing(ctx, p.WorkspaceID, provider.UseRelinkCooldown, "unlinked by administrator")
	fresh := guardConnection(t, a, "needs_auth", false)
	e := a.Manager.guardStart(ctx, fresh, "pair")
	if e == nil || !strings.Contains(e.Error(), "locked") {
		t.Fatal("relink cooldown bypassed", e)
	}
	// Повторная блокировка короче не сокращает действующую.
	a.Manager.blockPairing(ctx, p.WorkspaceID, time.Hour, "short")
	until, _, _ := a.Manager.pairingBlockedUntil(ctx, p.WorkspaceID)
	if time.Until(until) < 23*time.Hour {
		t.Fatal("cooldown was shortened", until)
	}

	// Состояние видно CRM по ключу без админских прав.
	cookie := loginCookie(t, a)
	w := request(a, "POST", "/api/v1/api-keys", map[string]any{"name": "CRM"}, cookie, "")
	var key struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &key)
	w = request(a, "GET", "/api/v1/wa-guard", nil, nil, key.Token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "pair_blocked_until\":\"") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestManageConnectionsKeyScope(t *testing.T) {
	a := testApp(t)
	cookie := loginCookie(t, a)
	mk := func(manage bool) string {
		w := request(a, "POST", "/api/v1/api-keys", map[string]any{"name": "CRM", "manage_connections": manage}, cookie, "")
		var key struct{ Token string }
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &key) != nil {
			t.Fatal(w.Body.String())
		}
		return key.Token
	}
	plain, manager := mk(false), mk(true)
	body := map[string]any{"provider": "telegram", "label": "TG", "credentials": map[string]string{"api_id": "1", "api_hash": "x"}}
	if w := request(a, "POST", "/api/v1/connections", body, nil, plain); w.Code != 403 {
		t.Fatal("plain key managed connections", w.Code)
	}
	if w := request(a, "POST", "/api/v1/connections", body, nil, manager); w.Code == 403 {
		t.Fatal("manage key rejected", w.Body.String())
	}
	// Остальное админское ключу по-прежнему закрыто.
	if w := request(a, "GET", "/api/v1/members", nil, nil, manager); w.Code != 403 {
		t.Fatal("manage key reached team administration", w.Code)
	}
}
