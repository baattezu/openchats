# Provider and unofficial-access risk register

Baseline: 2026-10-13. Qualitative residual risks are judgments, not measured ban probabilities. This covers known material risks; unknown provider behavior remains possible. There is no defensible method to reduce unofficial WhatsApp account-ban risk to approximately zero.

| Risk | Mitigation in v1 / operating practice | Residual |
|---|---|---|
| WhatsApp restriction or permanent ban | Official connector default; unofficial opt-in; reply-only; no campaigns/scraping; kill switches | High and outside service control for unofficial use |
| Terms/authorization and commercial-use exposure | Explicit risk notice; independent implementation; documented official alternative; review before public SaaS launch | Unofficial access may remain impermissible regardless of traffic volume |
| Unwanted messages/reports | Human replies to existing inbound chats, no cold outreach/campaign endpoint | Replies can still be unwanted; users can report them |
| Business-number/account loss | Use a dedicated test/business account; retain other customer contact channels | No number recovery or guaranteed appeal outcome |
| SDK/protocol/API breakage | Pin versions; isolate adapters; regression tests; staged upgrades; no automatic provider fallback | Emergency maintenance/outages remain possible |
| Session/token theft | AES-GCM at rest, no secret listings/logs, admin-only pairing, encrypted backups, session revocation | Host/master-key compromise grants access |
| Exposure of unrelated personal messages | Dedicated accounts, explicit admin consent, private workspace access, deletion controls | Connected personal clients can receive broad account history |
| CRM copies of decrypted messages | Authentication, resource checks, encrypted attachments/backups, controlled keys | CRM exports leave the provider’s E2EE boundary; exported copies must be managed separately |
| Temporary-message privacy | WhatsApp view-once/disappearing content is excluded from inbox storage; protocol buffers are encrypted | Provider-specific cache/expiry behavior and exported copies remain outside a universal guarantee |
| Missing/out-of-order/duplicate messages | Persistence before ACK, provider-ID dedup, checkpoint recovery, monotonic known receipts, bounded history import | No exactly-once/full-history guarantee; early echoes can produce duplicate downstream events |
| Uncertain outbound acceptance | Show `unknown`; never automatically resend ambiguous sends | Human investigation may be needed; a message may already have arrived |
| Rate limits/reconnect storms | Serialized job dispatch, conservative WA throttle, SDK reconnect behavior, bounded retries, Telegram cooldowns | A throttle is not an anti-ban guarantee; outages/limits still happen |
| Official Meta app eligibility/review/token expiry | Real OAuth/signup, permission validation, refresh attempts, configuration guides | Approval and business registration are external prerequisites |
| WhatsApp Cloud charges/limits | Official terminology, approved templates/service-window enforcement, link current pricing | Final costs depend on Meta’s categories/markets and account setup |
| Telegram restrictions | Official client API, own API credentials, flood-wait handling, no abuse automation | Telegram monitors API use and may restrict accounts |
| Malicious webhook/media URLs | Public HTTPS/443 only, DNS/IP checking at dial time, no proxies/redirects, bounded bodies | Provider clients themselves have their own network/download behavior; keep them updated |
| Uploaded malware/unsafe formats | Authenticated download, nosniff/sandbox/disposition headers, 25 MiB cap | No antivirus scanner; operators should scan downloaded files before opening them |
| Data retention/deletion/backups | Explicit deletion, encrypted backups, documented indefinite v1 retention | Old backups and CRM exports require separate erasure; failed connection-bound webhook jobs are removed with connection deletion; exported copies need separate erasure |
| Host outage or storage loss | Persistent volumes, encrypted offline backup/restore procedure | One-server v1 has no HA; key loss makes encrypted data unrecoverable |
| Dependency licensing/supply chain | Pin Go/npm dependencies, keep license notices, CI and review | Automated checks do not replace source/license/security review |

Primary references: [WhatsApp responsible use](https://faq.whatsapp.com/361005896189245/?eea=0&locale=en_US), [WhatsApp terms](https://www.whatsapp.com/legal/terms-of-service?content_language=English), [Meta pricing](https://business.whatsapp.com/products/platform-pricing), [Telegram API setup](https://core.telegram.org/api/obtaining_api_id), [Telegram API terms](https://core.telegram.org/api/terms), [whatsmeow source](https://github.com/tulir/whatsmeow), [gotd source](https://github.com/gotd/td).
