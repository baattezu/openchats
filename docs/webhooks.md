# CRM webhooks

Admins add a public HTTPS/443 URL in Workspace → Webhooks. The secret is shown once. Deliveries retry with bounded backoff (six attempts), and failed deliveries remain visible. v1 does not automatically replay failures forever.

Example envelope:

```json
{"id":"EVENT_UUID","type":"message.created","created_at":"2026-10-13T10:00:00Z","data":{"id":"MESSAGE_UUID","connection_id":"CONNECTION_UUID","conversation_id":"CONVERSATION_UUID","direction":"in","kind":"text","text":"Hello"}}
```

Headers:

- `X-Messaging-Event-ID`: stable event UUID.
- `X-Messaging-Timestamp`: current attempt’s Unix timestamp.
- `X-Messaging-Signature`: `sha256=` followed by HMAC-SHA256 hex of `timestamp + "." + raw_request_body`, using the subscription secret as UTF-8 bytes.

Verify the signature before decoding JSON. Compare in constant time, reject timestamps outside a reasonable tolerance (e.g. five minutes), and deduplicate event IDs. Timestamps/signatures change on retries, but event IDs do not. Keep the raw body unchanged while verifying. Deduplicate provider message IDs too when correlating an early outbound echo with its local send.

Events currently include `message.created`, `message.status`, `message.changed`, `connection.status`, and `history.import`. Different event types contain different fields; `message.status` can identify a local UUID or provider external IDs. Fetch the current message/conversation through the API when you need authoritative state. Do not assume event arrival order or full historical coverage.

The URL client rejects credentials, fragments, non-HTTPS ports, private/loopback/link-local/CGNAT addresses, proxies and redirects. Every DNS address must be public and the socket connects directly to the checked address. A redirect is a failed delivery, not followed. Keep endpoint response bodies small; the sender only uses the HTTP status.
