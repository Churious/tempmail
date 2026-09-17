# Self-hosted Temp Mail

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go) ![License](https://img.shields.io/badge/license-MIT-green)

개인 서버에서 직접 운영하는 수신 전용 임시 이메일 서비스입니다.

> Self-hosted, receive-only temporary email service built with Go, SQLite, and an embedded SMTP server.

## 주요 기능 · Features

- 무작위 임시 메일함 발급 / Instant disposable mailbox creation
- TTL 만료 및 백그라운드 자동 삭제 / Automatic TTL cleanup
- 비밀번호 기반 보존·복원 / Password-protected preservation and restore
- 내장 SMTP 수신 서버 / Embedded receive-only SMTP server
- WebSocket 실시간 알림 / Real-time WebSocket notifications
- SQLite 단일 파일 저장소 / Single-file SQLite storage

## 빠른 시작 · Quick start

`docker-compose.yml`의 `DOMAIN`을 실제 도메인으로 변경하고 DNS에 A/MX 레코드를 등록합니다.

```bash
docker compose up -d --build
```

웹 UI는 `http://서버주소:3000/`에서 확인합니다. Open the UI at `http://your-server:3000/`.

## 환경변수 · Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `DOMAIN` | `localhost` | 수신 도메인 / Mail domain |
| `DB_PATH` | `./data/tempmail.db` | SQLite 경로 / SQLite path |
| `ATTACHMENT_DIR` | `./data/attachments` | 첨부파일 경로 / Attachment path |
| `DEFAULT_TTL_HOURS` | `1` | 임시 메일함 TTL / Mailbox TTL |
| `HTTP_ADDR` | `:8080` | HTTP listen address |
| `SMTP_ADDR` | `:25` | SMTP listen address |

## 프로젝트 구조 · Project layout

```text
cmd/tempmail/   application entrypoint
web/            static frontend
docs/           API and operations docs
Dockerfile      container image
docker-compose.yml
```

## 개발 · Development

```bash
go mod download
go build ./cmd/tempmail
go test ./...
```

## 운영 주의사항 · Production notes

- 클라우드 사업자와 ISP의 TCP/25 제한을 해제해야 외부 메일을 받을 수 있습니다.
- `./data`에는 메일 데이터가 저장되므로 백업 및 접근 권한 정책을 적용하세요.
- 수신 전용 서비스이며 외부 메일 릴레이를 제공하지 않습니다.

## 문서 · Documentation

- [API Reference](docs/API.md)
- [Deployment Guide](docs/DEPLOYMENT.md)
- [Contributing](CONTRIBUTING.md)
- [Security Policy](SECURITY.md)
- [License](LICENSE)
