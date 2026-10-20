# ADR 004: Encrypt provider secrets and attachments

Status: accepted · 2026-10-13

Decision: AES-256-GCM with random nonces and authenticated context strings protects credential blobs, Telegram sessions/checkpoints, temporary Meta webhook jobs, attachment files and WhatsApp secret bytea fields. The key is supplied by deployment and is never stored in the database or repository. Message text/contact metadata remains plaintext in PostgreSQL so the inbox can query it; protect the database/host and backups accordingly.

WhatsApp’s upstream SQL store remains in PostgreSQL. A restricted database/sql connector seals non-index secret fields on writes and decrypts returned secret columns. Upstream plaintext-length constraints on those fields are removed; AEAD authentication validates ciphertext at read time. Queryable IDs/index hashes stay unencrypted. INSERT…SELECT migrations copy existing ciphertext without re-encryption. The adapter targets the pinned upstream SQL grammar; new SDK schema/query changes require tests before upgrade.

Alternatives: plaintext library SQL storage would violate the session-key requirement; encrypted SQLite snapshots would complicate the stack. Full database transparent encryption can supplement but does not replace these application protections.

Consequences: database theft alone does not expose provider keys or attachments. Host compromise, an exposed encryption key, privileged application access or memory inspection still can. No claim of end-to-end encryption for data viewed in the CRM. Key rotation requires a supervised re-encryption migration; do not simply change ENCRYPTION_KEY. Backup encryption uses age with separately managed keys.
