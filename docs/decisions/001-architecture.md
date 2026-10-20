# ADR 001: PostgreSQL and a modular monolith

Status: accepted · 2026-10-13

Context: an empty repository needs a small internal service with several long-lived messaging clients and eventual SaaS potential.

Decision: one Go service, React/Vite/TypeScript client and PostgreSQL. Durable send/import/webhook jobs live in PostgreSQL. No Redis, queue broker, Kubernetes, SQLite or CGO. Migrations run transactionally behind an advisory lock. Provider ownership uses a separate process-lifetime advisory lock; a second replica fails explicitly.

Alternatives: separate integration workers and a broker were rejected as unnecessary operational overhead. An in-memory SQLite snapshot was explored for WhatsApp key encryption, but was not implemented; it introduced a second database engine and snapshot durability complexity. The user confirmed a preference for PostgreSQL directly.

Consequences: straightforward deployment and transactional persistence. One worker handles queued work serially; long history imports can delay dispatch. Horizontal scaling requires partitioned provider ownership and worker scheduling before raising replica count.
