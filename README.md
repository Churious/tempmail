# Self-hosted Temp Mail

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go) ![License](https://img.shields.io/badge/license-MIT-green)

개인 서버에서 직접 운영하는 수신 전용 임시 이메일 서비스입니다.

> Self-hosted, receive-only temporary email service built with Go, SQLite, and an embedded SMTP server.

## 주요 기능 · Features

- 무작위 임시 메일함 발급 / Instant disposable mailbox creation
- 생성 시 10분, 1시간, 24시간, Lifetime 선택 / Selectable retention on creation
- 개별 메일 삭제(연결된 첨부파일 포함) / Delete individual messages with their attachments
- TTL 만료 및 백그라운드 자동 삭제 / Automatic TTL cleanup
- 비밀번호 기반 보존·복원 / Password-protected preservation and restore
- 내장 SMTP 수신 서버 / Embedded receive-only SMTP server
- MIME 본문·HTML·첨부파일 파싱 / MIME body, HTML, and attachment parsing
- 메일 목록과 개별 상세 보기 / Inbox list and individual message view
- 서버 및 브라우저 HTML 정화 / Server- and browser-side HTML sanitization
- WebSocket 실시간 알림 / Real-time WebSocket notifications
- SQLite 단일 파일 저장소 / Single-file SQLite storage

## 빠른 시작 · Quick start

아래 예시는 `example.com`에서 메일을 받고 서버 공인 IP가 `203.0.113.10`인 경우입니다.

1. DNS에 다음 레코드를 등록합니다.

   | Type | Name | Value | Priority |
   | --- | --- | --- | --- |
   | `A` | `mail.example.com` | `203.0.113.10` | - |
   | `MX` | `example.com` | `mail.example.com` | `10` |

   MX 값에는 IP 주소가 아니라 A/AAAA 레코드가 있는 호스트명을 사용해야 합니다.
2. 서버와 클라우드 보안 그룹에서 인바운드 TCP 25를 허용합니다. 일부 클라우드 사업자와 ISP는 방화벽 설정과 별도로 TCP 25를 차단하므로 제한 해제가 필요합니다.
3. `docker-compose.yml`의 `DOMAIN`을 메일 주소에 사용할 도메인(예: `example.com`)으로 변경합니다.
4. 서비스를 시작합니다.

```bash
docker compose up -d --build
```

웹 UI는 `http://서버주소:7200/`에서 확인합니다. Open the UI at `http://your-server:7200/`.

운영 환경에서는 웹 포트 7200을 그대로 공개하지 말고 HTTPS 리버스 프록시 뒤에 두는 것을 권장합니다. 프록시는 `/ws/`의 WebSocket 업그레이드도 전달해야 합니다. SMTP 포트 25는 HTTP 리버스 프록시가 아닌 서버로 직접 연결되어야 합니다. 자세한 내용은 [Deployment Guide](docs/DEPLOYMENT.md)를 참고하세요.

## 보존 시간과 복원 · Retention and recovery

- `10m`, `1h`, `24h` 메일함은 지정 시간이 지나면 자동 삭제됩니다.
- `lifetime`으로 바로 생성한 메일함은 자동 만료되지 않지만 복원 비밀번호가 설정되지 않습니다. 인증 토큰은 서버 메모리에만 있으므로 서버 재시작, 브라우저 저장소 삭제 또는 토큰 유실 후에는 데이터가 남아 있어도 다시 접근할 수 없습니다.
- 임시 메일함에서 **영구 보존**을 실행하고 비밀번호를 설정한 경우에만 `POST /api/v1/mailboxes/restore`로 새 인증 토큰을 발급받을 수 있습니다. 복원 API 사용법은 [API Reference](docs/API.md)를 참고하세요.

## 환경변수 · Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `DOMAIN` | `localhost` | 수신 도메인 / Mail domain |
| `DB_PATH` | `./data/tempmail.db` | SQLite 경로 / SQLite path |
| `ATTACHMENT_DIR` | `./data/attachments` | 첨부파일 경로 / Attachment path |
| `DEFAULT_TTL_HOURS` | `1` | 생성 요청에서 `retention`을 생략했을 때 적용할 TTL(양의 정수, 시간 단위) |
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

SQLite 드라이버는 순수 Go로 빌드되므로 로컬에 GCC가 필요하지 않습니다.

## 운영 주의사항 · Production notes

- 클라우드 사업자와 ISP의 TCP/25 제한을 해제해야 외부 메일을 받을 수 있습니다.
- `./data`에는 메일 데이터가 저장되므로 백업 및 접근 권한 정책을 적용하세요.
- 수신 전용 서비스이며 외부 메일 릴레이를 제공하지 않습니다.
- 웹 UI는 Tailwind Play CDN과 jsDelivr의 DOMPurify를 사용합니다. 브라우저가 두 CDN에 접근할 수 없는 완전한 오프라인 환경에서는 스타일과 HTML 메일 본문 표시가 정상 동작하지 않을 수 있습니다. 현재 배포물은 완전한 오프라인 self-hosted 프런트엔드 구성이 아닙니다.

## 문서 · Documentation

- [API Reference](docs/API.md)
- [Deployment Guide](docs/DEPLOYMENT.md)
- [Contributing](CONTRIBUTING.md)
- [Security Policy](SECURITY.md)
- [License](LICENSE)
