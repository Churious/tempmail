# Deployment Guide

Open TCP ports 80 and 25. Cloud providers often block port 25 by default.

Mount `./data` to `/app/data`; it contains the SQLite database and attachments. Put HTTPS and a WebSocket-capable reverse proxy in front of port 8080 for production.
