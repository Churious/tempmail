# API Reference

Protected endpoints use `Authorization: Bearer <token>`.

| Method | Endpoint | Description |
|---|---|---|
| POST | `/api/v1/mailboxes` | Create mailbox |
| POST | `/api/v1/mailboxes/preserve` | Preserve mailbox |
| POST | `/api/v1/mailboxes/restore` | Restore mailbox |
| GET | `/api/v1/mailboxes/:address/messages` | List messages |
| GET | `/api/v1/messages/:id` | Read message |
| WS | `/ws/:address?token=<token>` | Live events |
