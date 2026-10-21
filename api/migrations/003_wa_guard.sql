-- Защита неофициального WhatsApp от блокировок (docs/providers/whatsapp-web.md, «Guard»).
-- Ключ с правом управлять подключениями: CRM привязывает/отвязывает номер из своих настроек.
ALTER TABLE api_keys ADD COLUMN manage_connections boolean NOT NULL DEFAULT false;
-- Запрет новой привязки WhatsApp на уровне рабочего пространства: после отвязки,
-- выхода по инициативе WhatsApp или бана. На уровне пространства, а не подключения,
-- чтобы удаление подключения и создание нового запрет не обходило.
CREATE TABLE wa_guard(
 workspace_id uuid PRIMARY KEY REFERENCES workspaces,
 pair_blocked_until timestamptz,
 reason text NOT NULL DEFAULT ''
);
-- Журнал запусков сессий: по нему режем частые переподключения и циклы перезапусков.
CREATE TABLE connection_starts(
 connection_id uuid NOT NULL REFERENCES connections ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN ('resume','pair')),
 at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX connection_starts_recent ON connection_starts(connection_id, at DESC);
