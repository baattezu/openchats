# Operations

v1 has one API session owner, one PostgreSQL instance and persistent media. Provider status, pending/failed jobs and webhook deliveries are visible in the UI. `/healthz` checks PostgreSQL; `/readyz` reports database availability, not provider connectivity. Inspect connection status separately.

## Secrets and access

Keep `.env` permission-restricted and outside version control. Protect `ENCRYPTION_KEY` in a separate secret manager and back it up independently. Never replace it without migrating existing encrypted data. `.env.example` contains placeholders only. Credentials, code challenges, tokens and message bodies are omitted from routine logs. Treat database message/contact metadata as sensitive plaintext. Operator/admin host access can read decrypted data through the application.

For a lost admin password, an operator must arrange a supervised password reset directly in the database with a bcrypt hash and revoke sessions; there is no unauthenticated reset API. Do not rerun bootstrap to overwrite accounts. MFA, SSO, managed password reset and per-user unread state are future work.

## WhatsApp (unofficial) guard

The service treats every start of a linked WhatsApp device as something WhatsApp may judge. Rules (code: `api/internal/app/guard.go`, events in `api/internal/provider/whatsapp.go`):

- **No automatic pairing.** After a restart only already-linked sessions resume. A connection without a session waits in `needs_auth` until an administrator starts pairing. An expired QR code is not replaced automatically.
- **Relink cooldown.** After an unlink (by an administrator or by WhatsApp itself) new pairing is locked for `WA_RELINK_COOLDOWN_HOURS` (default 24) for the whole workspace; deleting and recreating the connection does not bypass it. `GET /api/v1/wa-guard` shows the lock.
- **Start limits.** At most `WA_MAX_RESUMES_PER_HOUR` (default 6) session resumes and `WA_MAX_PAIRINGS_PER_HOUR` (default 3) pairing attempts per connection per hour. Exceeding the resume limit (a crash loop, a reconnect button pressed in circles) pauses the connection.
- **Stop, never fight.** Session opened from another process (`StreamReplaced`), temporary ban, connect refusal (locked/banned), outdated client, or ~7 minutes without a successful reconnect: the session stops and stays stopped until resumed manually. Bans also lock pairing until they expire (at least 24 hours).
- **CRM access.** An API key created with "Manage connections" may pair, pause, resume and unlink accounts (the CRM settings screen); everything else administrative stays closed to keys.

### Moving to another server

1. On the old server: **Pause** WhatsApp/Telegram connections (pause keeps the linked device; do not unlink).
2. `make backup`, copy the archive and keep the same `ENCRYPTION_KEY` (sessions are encrypted with it).
3. Stop the old stack completely (`docker compose down`).
4. On the new server: `make restore`. Personal sessions come up **paused** on purpose.
5. Confirm the old server is down, then resume each connection once.

Never run two servers with the same database copy at the same time: they would take the session from each other, which is the fastest way to get the number restricted.

## Pause, revoke and delete

Use channel Pause to stop a live client without revoking its remote session. Reconnect resumes it. Official Meta inbound webhooks continue to be ingested while a channel is paused; pausing prevents sends. Set `STOP_ALL_SENDS=true` and restart for the global send kill switch; queued work is bounded and may eventually fail while paused. Disable `ENABLE_WHATSAPP_WEB` to stop unofficial sessions after restart. There is no automatic switch to another provider.

Disconnect attempts provider logout or app subscription removal before clearing local secrets. If remote revocation fails, the API reports failure and retains the local connection for retry. For full Meta app-grant revocation use the account’s Meta settings; remove Telegram/WhatsApp devices from the official app as needed. Delete stored data only after disconnecting. Old backups and CRM exports require independent deletion.

## Backups

`make backup` uses the production Compose stack and requires age plus `AGE_RECIPIENT`. It stops the API, captures a PostgreSQL custom dump and encrypted attachment files into an age-encrypted archive, then restarts the API even if backup fails. Temporary files are under a private temporary directory. The master key is deliberately excluded from the archive. Copy encrypted backups off the server and keep a retention schedule.

Restore uses `AGE_IDENTITY` and an explicit `RESTORE` confirmation. It stops the API, restores the database, copies media and starts the API after success. On failure it stays stopped for inspection. Surplus old media files are inaccessible without corresponding database rows but should be removed in a supervised storage cleanup. Test restoration on an isolated host with the matching master key before treating a backup as usable.

## Upgrades and incidents

Read the pinned SDK/API changes; rerun unit, PostgreSQL integration, browser and live tests before deployment. WhatsApp encrypted SQL bindings must be audited for newly introduced queries/secret columns. Take a backup before migrations. Keep Graph version/app onboarding settings aligned with Meta’s configuration.

Repeated provider bans/errors: pause, inspect authorized account state, do not retry evasively. `unknown` outbound status: check the official conversation before any manual resend. Lost connection history: use the provider’s bounded import where available; report coverage, never promise full recovery. Follow [incident templates](incidents/README.md).
