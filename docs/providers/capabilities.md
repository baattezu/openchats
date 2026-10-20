# v1 capability matrix

These are implemented connector capabilities, not a claim that every provider supports the same features.

| Feature | WhatsApp Web | WhatsApp Cloud | Instagram | Telegram personal |
|---|---|---|---|---|
| Connection | QR linked device | Embedded Signup | Instagram OAuth | Phone code + optional 2FA |
| Private text | Yes, reply-only | Service window / approved template | Inbound reply window | Yes |
| Group conversations | Receive/reply | Not exposed | Not exposed | Receive/reply |
| Images/documents | Supported formats | Supported formats | Provider-supported attachments | Supported formats |
| Audio/voice-note files | Yes | Yes | Not advertised as voice-note support | Yes |
| Video files | Yes | Yes | Provider-supported | Yes |
| Edit/delete own text | Provider-supported | No | No | Provider-supported |
| Delivery/read receipts | Handled | Handled | Not exposed in v1 | Not exposed in v1 |
| Manual history import | No; provider sync only | No | No | 50 dialogs × 100 messages |
| Calls / group management | No | No | No | No |

All attachment operations have a service limit of 25 MiB and can have smaller provider limits. Format validation/rejection is also subject to provider rules. Browser recording, transcoding, thumbnails and full rich-message rendering are deferred. Unsupported message types and unavailable attachments are shown honestly.
