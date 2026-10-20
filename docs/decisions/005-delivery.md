# ADR 005: Durable delivery, honest uncertainty

Status: accepted · 2026-10-13

Decision: acknowledge Meta webhooks only after a verified, encrypted payload is persisted as a job. Deduplicate the original payload and normalized message IDs. Store canonical messages and downstream events transactionally. Persist edit/delete tombstones so an out-of-order original cannot resurrect deleted content. Persist the unofficial send throttle in PostgreSQL across restarts. HTTP send requests require a UUID idempotency key and reject reuse with different content. Queue sends and validate connection state/policy again at dispatch.

Confirmed provider IDs correlate echoes and local sends. Some providers can deliver an echo before a send response; the final stored message is merged, but downstream consumers should also deduplicate by connection/provider-message ID. Exactly-once external delivery is not promised.

Bound rate limits and safe retries to six attempts; respect provider cooldowns. Ambiguous transport acceptance and interrupted in-flight sends become `unknown` without automatic resend. Recover safe webhook/Meta jobs after restart. SSE supplies change notifications; reconnect refreshes persisted state.

Outgoing webhooks are at-least-once, have stable event IDs and timestamped HMAC signatures. Only public HTTPS/443 endpoints are allowed; DNS is resolved and checked at connection time, without proxies or redirects. Failed deliveries/jobs are visible under Workspace → Activity/Webhooks.

Consequences: fewer accidental duplicates and visible uncertainty. Outgoing CRM consumers must deduplicate. Failed Meta jobs require investigation; no automatic infinite retries or automatic uncertain-send replay. Meta envelopes are split into connection-bound encrypted jobs, so deleting one account also erases its failed raw jobs without dropping another account’s events. Successful raw payloads are purged after ingestion.
