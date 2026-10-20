# Operations

v1 has one API session owner, one PostgreSQL instance and persistent media. Provider status, pending/failed jobs and webhook deliveries are visible in the UI. `/healthz` checks PostgreSQL; `/readyz` reports database availability, not provider connectivity. Inspect connection status separately.

## Secrets and access

Keep `.env` permission-restricted and outside version control. Protect `ENCRYPTION_KEY` in a separate secret manager and back it up independently. Never replace it without migrating existing encrypted data. `.env.example` contains placeholders only. Credentials, code challenges, tokens and message bodies are omitted from routine logs. Treat database message/contact metadata as sensitive plaintext. Operator/admin host access can read decrypted data through the application.

For a lost admin password, an operator must arrange a supervised password reset directly in the database with a bcrypt hash and revoke sessions; there is no unauthenticated reset API. Do not rerun bootstrap to overwrite accounts. MFA, SSO, managed password reset and per-user unread state are future work.

## Pause, revoke and delete

Use channel Pause to stop a live client without revoking its remote session. Reconnect resumes it. Official Meta inbound webhooks continue to be ingested while a channel is paused; pausing prevents sends. Set `STOP_ALL_SENDS=true` and restart for the global send kill switch; queued work is bounded and may eventually fail while paused. Disable `ENABLE_WHATSAPP_WEB` to stop unofficial sessions after restart. There is no automatic switch to another provider.

Disconnect attempts provider logout or app subscription removal before clearing local secrets. If remote revocation fails, the API reports failure and retains the local connection for retry. For full Meta app-grant revocation use the account’s Meta settings; remove Telegram/WhatsApp devices from the official app as needed. Delete stored data only after disconnecting. Old backups and CRM exports require independent deletion.

## Backups

`make backup` uses the production Compose stack and requires age plus `AGE_RECIPIENT`. It stops the API, captures a PostgreSQL custom dump and encrypted attachment files into an age-encrypted archive, then restarts the API even if backup fails. Temporary files are under a private temporary directory. The master key is deliberately excluded from the archive. Copy encrypted backups off the server and keep a retention schedule.

Restore uses `AGE_IDENTITY` and an explicit `RESTORE` confirmation. It stops the API, restores the database, copies media and starts the API after success. On failure it stays stopped for inspection. Surplus old media files are inaccessible without corresponding database rows but should be removed in a supervised storage cleanup. Test restoration on an isolated host with the matching master key before treating a backup as usable.

## Upgrades and incidents

Read the pinned SDK/API changes; rerun unit, PostgreSQL integration, browser and live tests before deployment. WhatsApp encrypted SQL bindings must be audited for newly introduced queries/secret columns. Take a backup before migrations. Keep Graph version/app onboarding settings aligned with Meta’s configuration.

Repeated provider bans/errors: pause, inspect authorized account state, do not retry evasively. `unknown` outbound status: check the official conversation before any manual resend. Lost connection history: use the provider’s bounded import where available; report coverage, never promise full recovery. Follow [incident templates](incidents/README.md).
