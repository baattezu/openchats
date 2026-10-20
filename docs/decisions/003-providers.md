# ADR 003: Provider adapters and onboarding

Status: accepted · 2026-10-13

Decision: whatsmeow for WhatsApp Web; Meta Cloud API/Embedded Signup for official WhatsApp; Instagram Login OAuth for professional accounts; gotd MTProto for Telegram personal accounts. Use official APIs by default. The user explicitly selected personal Telegram access and Meta OAuth/signup in v1.

Adapters expose capabilities and lifecycle/challenge/send/edit/delete/import operations. Unsupported features are rejected server-side and hidden/disabled in the UI. Richer common messaging includes private/groups where available, attachments, voice-note files and supported edits/deletes. Calls, group administration, secret chats and campaigns are excluded.

WhatsApp Web requires deployment enablement plus risk acknowledgement, allows replies only, and never takes over automatically from Cloud API. Telegram login codes/passwords remain transient; only the resulting encrypted session persists. Instagram Login has its own configured app ID/secret. Meta tokens are exchanged server-side; OAuth state is hashed, short-lived and bound to the initiating user/connection. Signup account IDs are verified using the granted token before number registration/subscription. The user supplies the number’s six-digit two-step PIN, kept transient and never logged/stored.

Consequences: no universal one-click onboarding and no universal message feature parity. Live Meta availability depends on app configuration, permissions and approvals. There is no provider billing estimate or fixed price table; consult current Meta pricing. See provider guides and risk register for primary sources.
