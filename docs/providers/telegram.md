# Telegram personal accounts

Obtain your application’s API ID/hash from [my.telegram.org](https://my.telegram.org/apps) following [Telegram’s instructions](https://core.telegram.org/api/obtaining_api_id). Configure `TELEGRAM_API_ID` and `TELEGRAM_API_HASH`, or enter your application credentials in the connection modal. Create a Telegram connection, enter a phone number, the login code delivered by Telegram, then your 2FA password if requested. These transient challenges are not stored or logged. New-user registration is not performed.

The Go [gotd/td](https://github.com/gotd/td) client implements MTProto and persistent encrypted session storage. Cloud private/group messages, attachments, voice-note files, supported edits/deletes and a bounded history import are supported. Secret chats, calls, personal-account creation and bots are excluded. Peer hashes and update checkpoints persist encrypted; gap recovery is delegated to the SDK’s update manager. Large unrecoverable gaps are reported and can use bounded import.

Import loads up to 50 recent dialogs and 100 messages per dialog. It is deliberately not a full-account archive. `FLOOD_WAIT` cooldowns are respected; other permission/spam restrictions are surfaced without repeated evasion attempts. Disconnect uses Telegram logout; sessions can also be revoked in the official app.

Telegram says third-party API client accounts are observed for abuse and may be banned for spam/flooding. Read [API terms](https://core.telegram.org/api/terms) and the library’s linked safety guide. Personal accounts can expose unrelated private conversations to the workspace: use a business-dedicated account and obtain appropriate permission before connecting it. No zero-risk claim is made.
