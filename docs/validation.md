# Validation record

2026-10-13: local implementation validation.

- Go application and all four provider adapters compile against pinned dependencies. `go vet ./...` passes. Runtime dependencies contain no SQLite or SQLCipher packages.
- Final PostgreSQL integration tests and `go test -race ./...` pass. Coverage includes auth/roles/invitations/API keys, idempotent queueing, inbound deduplication, receipt ordering, out-of-order edits/deletions, paused Meta ingestion, uncertain sends without automatic retry, signed Meta ingestion, protected encrypted media, and encrypted WhatsApp SQL device/session/key/buffer round trips.
- Native application smoke checks pass: startup, health, bootstrap, login, authenticated API access, graceful SIGTERM shutdown, and rejection of a second application instance. The smoke test uses a temporary schema that is removed afterward.
- Final React/TypeScript production build and formatting checks pass. Fonts are bundled locally.
- Final browser suite: **10/10 tests pass**. Coverage includes sign-in, reply queueing, one-time keys, role-dependent controls, unofficial risk acknowledgement and layouts at 320/375/414/768/1440 px. A regression assertion verifies that profile action buttons do not overlap at any tested width. Desktop, tablet and mobile screenshots were visually inspected.
- Development and production Docker Compose configurations validate successfully.

External validation still required: authorized live provider connect/send/receive/edit/delete/reconnect, Meta app review/configuration, public HTTPS OAuth/webhook callbacks, production Compose images/runtime and encrypted backup restoration on a separate host. Tests use provider fixtures, not real credentials.

Environment constraints encountered: sandboxed registry access and PostgreSQL shared-memory/socket access failed. Dependency downloads and the temporary test PostgreSQL server use explicitly approved commands. The temporary server/database are isolated from application/production data.

Production Compose configuration validation passed. Docker image builds were attempted with existing and isolated anonymous Docker configurations; Docker Hub returned HTTP 401 while fetching public base-image authorization tokens. Container build/runtime validation remains blocked by that external registry failure.
