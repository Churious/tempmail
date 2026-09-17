# API Reference

Protected endpoints use `Authorization: Bearer <token>`.

| Method | Endpoint | Description |
|---|---|---|
| POST | `/api/v1/mailboxes` | Create mailbox |
| POST | `/api/v1/mailboxes/preserve` | Preserve mailbox |
| POST | `/api/v1/mailboxes/restore` | Restore mailbox |
| GET | `/api/v1/mailboxes/:address/messages` | List messages |
| GET | `/api/v1/messages/:id` | Read message |
| DELETE | `/api/v1/messages/:id` | Delete a message and its attachments |
| GET | `/api/v1/attachments/:id/download` | Download attachment |
| WS | `/ws/:address?token=<token>` | Live events |

## Mailbox retention

`POST /api/v1/mailboxes` accepts an optional `retention` value: `10m`, `1h`, `24h`, or `lifetime`. When the field is omitted, the server applies `DEFAULT_TTL_HOURS` and returns the effective value in the `retention` response field.

```json
{
  "local_part": "demo",
  "retention": "1h"
}
```

Creating a mailbox with `lifetime` disables automatic expiration but does not set a recovery password. Its in-memory token becomes invalid when the server restarts. To make a temporary mailbox recoverable, call `POST /api/v1/mailboxes/preserve` with its bearer token and a password of 8–128 characters before access is lost. A preserved mailbox can then obtain a new token through `POST /api/v1/mailboxes/restore`.

Deleting a message through `DELETE /api/v1/messages/:id` also deletes every attachment linked to that message. Individual attachment deletion is not supported.
