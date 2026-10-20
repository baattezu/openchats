# ADR 002: Internal team, workspace boundaries

Status: accepted · 2026-10-13

Decision: one internal workspace; individual accounts, admin/member roles, explicit invitations and no public registration. Passwords use bcrypt. Browser sessions expire after 24 hours, use HttpOnly/SameSite cookies, and require exact-origin checks on mutations. Login/invitation attempts are rate limited. Invitations are stored hashed and consumed transactionally. CRM API keys are revocable hashed bearer tokens with read/reply permissions only.

Admins manage account connections, keys, invitations, and webhook subscriptions. Members share conversation read state; personal unread state and assignments are deferred. Workspace IDs are applied to every user-facing storage query and resource access. Secrets are shown only once. Removing a member revokes their browser sessions through cascading deletion; self-removal is forbidden.

Alternatives: a shared admin token was rejected because the user chose team login. A complete organization-management/public signup system was deferred to keep v1 small.

Consequences: tenant-aware storage, not a claim of a finished multi-tenant SaaS. Role administration, identity-provider login, password reset, MFA and user-managed workspace creation need a later release. Invitation delivery is manual; the service sends no email.
