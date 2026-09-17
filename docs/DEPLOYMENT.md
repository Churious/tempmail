# Deployment Guide

## DNS and network

For a service receiving mail for `example.com` at public IP `203.0.113.10`, configure:

```text
mail.example.com.  A   203.0.113.10
example.com.       MX  10 mail.example.com.
```

An MX record must point to a hostname with an A or AAAA record, not directly to an IP address. Set `DOMAIN=example.com` so generated recipient addresses match the MX domain.

Allow inbound TCP 25 through the host firewall and cloud security group. Many cloud providers and ISPs block port 25 separately; request removal of that restriction before testing external delivery. SMTP traffic connects directly to port 25 and must not be sent through an HTTP reverse proxy.

## Web access and HTTPS

The compose file publishes the web UI on port 7200. For production, terminate HTTPS at a reverse proxy and forward requests to port 7200. The proxy must support WebSocket upgrades for `/ws/` so live inbox notifications continue to work. Restrict direct public access to port 7200 when the proxy is in place; for example, bind it to `127.0.0.1:7200:8080` in `docker-compose.yml`.

## Persistent data

Mount `./data` to `/app/data`; it contains the SQLite database and attachments. Back up both together. Authentication tokens are stored only in server memory, while password hashes for explicitly preserved mailboxes are stored in SQLite.

## Frontend network dependency

The current web UI loads Tailwind Play CDN and DOMPurify from jsDelivr. Client browsers therefore need internet access to those CDNs. Bundle these dependencies locally before deploying into a fully offline network.
