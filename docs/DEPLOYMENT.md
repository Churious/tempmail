# Deployment Guide

Open TCP ports 7200 and 25. Cloud providers often block port 25 by default.

Mount `./data` to `/app/data`; it contains the SQLite database and attachments. Put HTTPS and a WebSocket-capable reverse proxy in front of host port 7200 for production.
