# Incidents

No production incident is asserted by this scaffold. Add a dated incident record when an actual issue occurs. Development/build failures belong in [validation](../validation.md), unless they reveal an operational incident worth preserving.

Use [the template](template.md). Include impact, evidence, timeline, root cause, recovery, follow-up owner and linked ADRs. Never paste tokens, encryption keys, login codes, phone numbers or message bodies. Preserve sanitized IDs only.

Immediate procedures:

- Account restriction: pause the affected connector; check official provider notices; follow the provider’s recovery process. Do not rotate accounts or evade restrictions.
- Session leak: disconnect/revoke in the official app; revoke affected API keys; rotate compromised operator secrets with a supervised re-encryption plan; investigate access.
- Delivery uncertainty: inspect the official conversation before retrying. Preserve the local message ID and job metadata.
- Database outage: stop dispatch; restore connectivity or validate an encrypted backup on an isolated instance. Restart recovery marks interrupted sends unknown.
