package app

// Guard неофициального WhatsApp. Риск бана растёт от «подозрительной» жизни
// привязанного устройства: частые переподключения, циклы перезапусков, привязка
// сразу после отвязки, две копии одной сессии на разных серверах. Поэтому:
//
//   - любой запуск сессии проходит через guardStart: привязка (QR) и возобновление
//     считаются по журналу connection_starts и режутся по лимитам в час;
//   - после отвязки (нами или самим WhatsApp) новая привязка запрещена на
//     WA_RELINK_COOLDOWN_HOURS на уровне рабочего пространства (wa_guard);
//   - при старте сервиса сами поднимаются только уже привязанные сессии — новая
//     привязка всегда начинается с явного действия администратора;
//   - события «сессию открыли в другом месте», «временный бан», «отказ в
//     подключении» останавливают сессию через halt, и сама она не поднимется.

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"net/http"
	"stepline/messaging-service/internal/provider"
	"time"
)

const guardedProvider = "whatsapp_web"

// pairingBlockedUntil — до какого момента запрещена новая привязка WhatsApp.
func (m *Manager) pairingBlockedUntil(ctx context.Context, workspace string) (time.Time, string, error) {
	var until *time.Time
	var reason string
	e := m.s.DB.QueryRow(ctx, `SELECT pair_blocked_until,reason FROM wa_guard WHERE workspace_id=$1`, workspace).Scan(&until, &reason)
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return time.Time{}, "", nil
		}
		return time.Time{}, "", e
	}
	if until == nil {
		return time.Time{}, "", nil
	}
	return *until, reason, nil
}

// blockPairing продлевает запрет привязки (никогда не сокращает уже действующий).
func (m *Manager) blockPairing(ctx context.Context, workspace string, d time.Duration, reason string) {
	if d == provider.UseRelinkCooldown {
		d = m.cfg.WARelinkCooldown
	}
	if d <= 0 {
		return
	}
	_, e := m.s.DB.Exec(ctx, `INSERT INTO wa_guard(workspace_id,pair_blocked_until,reason) VALUES($1,now()+$2::interval,$3)
		ON CONFLICT(workspace_id) DO UPDATE SET pair_blocked_until=GREATEST(COALESCE(wa_guard.pair_blocked_until,now()),EXCLUDED.pair_blocked_until), reason=EXCLUDED.reason`,
		workspace, fmt.Sprintf("%d seconds", int(d.Seconds())), reason)
	if e != nil {
		slog.Error("wa guard persistence failed", "workspace_id", workspace)
	}
}

// guardStart пропускает или отклоняет запуск сессии WhatsApp и записывает его в журнал.
// kind: "pair" — новая привязка по QR, "resume" — подъём уже привязанной сессии.
func (m *Manager) guardStart(ctx context.Context, c provider.Connection, kind string) error {
	if c.Provider != guardedProvider {
		return nil
	}
	if kind == "pair" {
		until, reason, e := m.pairingBlockedUntil(ctx, c.WorkspaceID)
		if e != nil {
			return e
		}
		if time.Now().Before(until) {
			return bad(fmt.Sprintf("WhatsApp pairing is locked until %s (%s). This protects the number from being restricted.", until.UTC().Format(time.RFC3339), reason))
		}
	}
	limit := m.cfg.WAMaxResumesPerHour
	if kind == "pair" {
		limit = m.cfg.WAMaxPairingsPerHour
	}
	var recent int
	if e := m.s.DB.QueryRow(ctx, `SELECT count(*) FROM connection_starts WHERE connection_id=$1 AND kind=$2 AND at>now()-interval '1 hour'`, c.ID, kind).Scan(&recent); e != nil {
		return e
	}
	if recent >= limit {
		if kind == "resume" {
			// Цикл перезапусков (падающий контейнер, кнопка «переподключить» по кругу) —
			// ставим на паузу: дальше только человек, разобравшись в причине.
			m.forceStatus(ctx, c, "paused", "Too many reconnects in the last hour; the session was paused to protect the number. Resume manually later.")
		}
		return bad(fmt.Sprintf("too many %s attempts in the last hour (limit %d); wait before trying again", kind, limit))
	}
	_, e := m.s.DB.Exec(ctx, `INSERT INTO connection_starts(connection_id,kind) VALUES($1,$2)`, c.ID, kind)
	return e
}

// forceStatus пишет статус в обход фильтра status(), который не трогает
// paused/disconnected — guard как раз и переводит в paused.
func (m *Manager) forceStatus(ctx context.Context, c provider.Connection, status, detail string) {
	if _, e := m.s.DB.Exec(ctx, `UPDATE connections SET status=$2,detail=$3 WHERE id=$1`, c.ID, status, detail); e != nil {
		slog.Error("connection status persistence failed", "connection_id", c.ID)
		return
	}
	_ = m.s.Emit(ctx, c.WorkspaceID, "connection.status", map[string]any{"id": c.ID, "status": status, "detail": detail, "identity": c.Identity})
}

// halt — реакция на событие провайдера, после которого сессия не должна
// подниматься сама. Вызывается из обработчика событий whatsmeow, поэтому работу
// уводим в отдельную горутину: остановка клиента изнутри его же обработчика
// не должна ждать сама себя.
func (m *Manager) halt(c provider.Connection, status, detail string, block time.Duration, wipe bool) {
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		slog.Warn("provider halted connection", "connection_id", c.ID, "status", status)
		m.forceStatus(ctx, c, status, detail)
		if wipe {
			// Номер больше не привязан: стираем и сессию, и «чей это номер» — иначе
			// отмена следующей привязки выглядела бы как отвязка живого номера.
			_ = m.s.SaveBlob(ctx, c.ID, "session_data", nil)
			_, _ = m.s.DB.Exec(ctx, `UPDATE connections SET identity='' WHERE id=$1`, c.ID)
		}
		if block != 0 {
			m.blockPairing(ctx, c.WorkspaceID, block, detail)
		}
		_ = m.Stop(ctx, c.ID, false)
	}()
}

// waGuard — состояние защиты для интерфейсов (CRM показывает, когда можно привязать снова).
func (a *Server) waGuard(w http.ResponseWriter, r *http.Request, p Principal) error {
	until, reason, e := a.Manager.pairingBlockedUntil(r.Context(), p.Workspace)
	if e != nil {
		return e
	}
	out := map[string]any{"pair_blocked_until": nil, "reason": "", "relink_cooldown_hours": int(a.Config.WARelinkCooldown.Hours()), "enabled": a.Config.EnableUnofficial}
	if time.Now().Before(until) {
		out["pair_blocked_until"] = until.UTC()
		out["reason"] = reason
	}
	write(w, 200, out)
	return nil
}
