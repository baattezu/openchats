# WhatsApp unofficial

Set `ENABLE_WHATSAPP_WEB=true`, restart the service, choose WA unofficial, accept the risk notice, and scan the QR from the official phone app’s Linked devices screen. QR values are ephemeral and delivered only to authenticated admins. Use Pause for a local kill switch; Disconnect logs out the linked device. If the process stopped before pairing, reconnect to obtain a fresh QR. Logout/device removal requires new pairing.

The connector uses [whatsmeow](https://github.com/tulir/whatsmeow), a Go multi-device client library (MPL-2.0). Keep its license notices and review redistribution obligations for modified library files. No upstream source modifications are included here.

It processes live events and provider-delivered history, sends text/media to existing conversations, and supports private/groups, audio files as voice notes, supported edits/deletes and receipts. View-once and disappearing messages are excluded from the inbox; the library’s encrypted protocol buffers may briefly retain decrypted protocol data for retries. Message types that cannot be handled appear as unsupported. Large/unavailable attachments show a clear fallback.

Replies are serialized; the default minimum interval is 15 seconds (operator floor: 5 seconds). This is an operational throttle, **not a ban-safe threshold**. No cold-outreach API, campaigns, automated group management, contact scraping, stealth or account rotation. See the [risk register](risks.md).

[WhatsApp responsible-use guidance](https://faq.whatsapp.com/361005896189245/?eea=0&locale=en_US) explicitly lists unwanted messages, unauthorized automation and unpermitted harvesting as practices that may cause bans. The safest route for a business-critical number is an authorized official connection; even official accounts remain subject to enforcement.
