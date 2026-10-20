# OpenChats

An internal shared inbox and CRM messaging API. React + Vite, Go, and **PostgreSQL from day one**. The interface follows the supplied wireframe: user/API controls, channels, message feed, and a conversation pane.

## Run locally

Requirements: Go 1.27.1+, Node 24+, Docker Compose, Python 3.12+, and optionally mprocs.

```sh
make init
make install
make infra-up
make bootstrap
make dev
```

To run it the way the server does, use `make local` instead: the client is built into the web image and Caddy serves it with `/api` on http://localhost:5175 (`make local-down` stops it). Without mprocs, run `make dev-api` and `make dev-client` in separate terminals. Open http://localhost:5175 (API on :8002, dev Postgres on :5436). `make init` generates unique local secrets and never replaces an existing `.env`. Bootstrap prompts for the first admin’s credentials; it cannot overwrite existing accounts. Admins invite teammates through Workspace → Team. Invitations are shared manually and expire in 48 hours.

The API applies versioned migrations on startup; `make migrate` applies them explicitly. There is one internal workspace with admin/member roles and workspace IDs throughout storage. Public registration, billing, campaigns, calls, group administration, secret chats, and a specific CRM connector are outside v1.

## Connect accounts

| Connector | Setup | Notes |
|---|---|---|
| WhatsApp unofficial | Operator sets `ENABLE_WHATSAPP_WEB=true`; admin accepts risks and scans linked-device QR | Reply-only, throttled, no guarantee against restrictions or bans |
| WhatsApp official | Meta Embedded Signup | Requires configured Meta app, business assets, permissions, HTTPS and approvals |
| Instagram | Instagram Login OAuth | Professional business/creator accounts; supported direct-message operations |
| Telegram personal | API ID/hash, phone code, optional 2FA | Uses the client API, not Bot API; cloud chats only |

Follow the [provider guides](docs/providers/README.md). Real OAuth and messaging require your credentials and provider permissions. An unconfigured integration shows setup requirements; it does not return fake success. Telegram permits a bounded import of up to 50 dialogs × 100 messages. WhatsApp Web consumes provider-delivered history. Official connectors do not promise historical completeness.

Supported operations differ by provider: consult the [capability matrix](docs/providers/capabilities.md). Voice notes are sent by attaching a supported audio file. The UI supports approved text-only WhatsApp templates; the REST API accepts provider template parameters for richer existing templates. There is no template creation UI or audio recorder.

## CRM API

Create a key under Workspace → API keys. Full values are shown once, only hashes are stored, and revocation is immediate. CRM keys can read and reply but cannot manage connections, users, or secrets.

```sh
curl -H "Authorization: Bearer $MESSAGING_API_KEY" \
  https://messaging.example.com/api/v1/conversations

curl -X POST \
  -H "Authorization: Bearer $MESSAGING_API_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $MESSAGE_UUID" \
  -d '{"text":"Hello"}' \
  https://messaging.example.com/api/v1/conversations/CONVERSATION_UUID/messages
```

Send requests are durably queued. Reuse the same UUID when retrying the same HTTP request. Different content with the same key is rejected. Outbound messages progress through queued/sent/delivered/read where the provider reports them; `unknown` means acceptance could not be determined and **is not automatically resent**. Messages received from another linked client are correlated by provider IDs; external consumers should deduplicate both event IDs and message/provider IDs.

See [OpenAPI](docs/openapi.yaml) and [webhook verification](docs/webhooks.md). Fetch an individual message with `GET /api/v1/messages/{id}`. Quoted reply references (`reply_to`) currently apply only to official WhatsApp; ordinary conversation replies work on all connectors. The UI uses authenticated SSE and refreshes stored state after reconnecting. Pagination uses the last item’s UUID as `before` (100 items per page).

## Deploy

Set `DOMAIN`, production Meta/Telegram credentials, and unique encryption/database secrets in `.env`. Point the domain to your server and allow ports 80/443. Then:

```sh
make prod-up
docker compose --env-file .env -f infra/compose.prod.yaml run --rm \
  -e BOOTSTRAP_EMAIL=admin@example.com -e BOOTSTRAP_NAME=Admin \
  -e BOOTSTRAP_PASSWORD api bootstrap
```

Set `BOOTSTRAP_PASSWORD` in the calling environment; never put it in command arguments/history. Production Caddy provisions HTTPS and serves the client/API on one origin. PostgreSQL has no published production port. v1 supports **one API replica**; a database advisory lock prevents simultaneous session owners. Database/media volumes survive container replacement.

Set `STOP_ALL_SENDS=true` and restart the API for the global kill switch. Admins can pause a single connection without logging it out, or disconnect to revoke the provider session/subscription. Disconnect keeps conversation history; deleting a disconnected connection removes associated messages and attachments. Previously exported data and old backups are not automatically erased.

Use encrypted backups: install `age`, set `AGE_RECIPIENT`, then `make backup`. Restore with `AGE_IDENTITY=/path/private-key make restore ARCHIVE=/path/backup.tar.age`. Restore asks for explicit confirmation and replaces data. Keep the service’s `ENCRYPTION_KEY` and age identity in a separate secret manager: losing the service key makes stored provider sessions and attachments unreadable. See [operations](docs/operations.md).

## Checks and current limits

```sh
make build
make test-api
cd client && npx playwright install chromium
make test-client
```

PostgreSQL integration tests run when `TEST_DATABASE_URL` points to an isolated database named `messaging_service_test`; each test uses its own schema. Without that variable they skip explicitly. CI runs them using PostgreSQL. Browser tests use deterministic API fixtures and do not contact providers.

Live provider login/send/receive, Meta app review, public HTTPS deployment and production backup restoration need your accounts/infrastructure. Automated tests cannot certify those external prerequisites. Provider limits change; the pinned SDKs and Graph version require maintenance. No unofficial connection can be guaranteed ban-free. [Risk register](docs/providers/risks.md) documents residual risks and mitigations.

Decisions are in [docs/decisions](docs/decisions/README.md), operational incidents in [docs/incidents](docs/incidents/README.md), and future work in [docs/ideas](docs/ideas/README.md). The implementation is independent; it does not copy Wazzup code, assets, or private endpoints.
