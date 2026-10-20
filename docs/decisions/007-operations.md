# ADR 007: Single-server deployment and explicit operations

Status: accepted · 2026-10-13

Decision: Compose with PostgreSQL, Go API, and Caddy serving the Vite bundle and HTTPS reverse proxy. Persistent database/media volumes, no published production database port, health checks, non-root API container, and one API replica. Development runs Go/Vite locally against Compose PostgreSQL using Makefile/mprocs.

Generate development secrets once. Production operators supply domain/provider credentials and protect .env. No cloud-specific IaC, email service or production deployment is performed automatically. Encrypted offline backups stop the API for a consistent database/media pair. Restore explicitly confirms replacement. The encryption key is backed up separately.

Consequences: a portable baseline with brief backup downtime, no high availability, no automatic certificate setup without a reachable domain, and no managed secrets service. CI tests deterministic provider fixtures and an isolated PostgreSQL database; live tests use authorized accounts separately.
