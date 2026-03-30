# Handover Summary — TGVPN Go Bot

**Repo:** https://github.com/kereal/rs8kvn_bot
**Module:** `rs8kvn_bot` (Go 1.25+)

---

## Architecture

```
tgvpn_go/
├── cmd/bot/main.go              # Entry point, signal handling, goroutine lifecycle
├── internal/
│   ├── bot/                      # Telegram bot handlers
│   │   ├── handler.go           #   Handler struct, getMainMenuContent(), showLoadingMessage()
│   │   ├── callbacks.go         #   Callback query routing
│   │   ├── commands.go          #   /start, /help
│   │   ├── subscription.go      #   Subscription CRUD, QR code
│   │   ├── menu.go              #   back_to_start, donate, help
│   │   ├── admin.go             #   /lastreg, /del, /broadcast, /send
│   │   └── message.go           #   Message sending utilities
│   ├── web/                      # HTTP server (health + invite/trial pages)
│   │   ├── web.go               #   /healthz, /readyz, /i/{code} — trial invite landing
│   │   └── web_test.go
│   ├── xui/                      # 3x-ui panel API client + circuit breaker
│   │   ├── client.go            #   Login, CreateClient, GetClientTraffic, etc.
│   │   └── breaker.go
│   ├── database/                 # GORM SQLite, migrations
│   ├── config/                   # Env var loader + constants
│   ├── logger/                   # Zap + Sentry + lumberjack rotation
│   ├── interfaces/               # Service interfaces for DI
│   ├── utils/                    # UUID, SubID, QR, time helpers
│   ├── ratelimiter/              # Token bucket
│   ├── heartbeat/                # Periodic health pings
│   ├── backup/                   # Daily SQLite backup rotation
│   └── health/                   # Health checker (legacy, unused — web/ replaces it)
├── data/                         # Runtime: tgvpn.db, backups, bot.log
├── doc/                          # PLAN.md, HANDOVER.md, improvement docs
├── Dockerfile, docker-compose.yml, .env.example
└── .github/                      # CI: lint, gosec, test, Docker build + GHCR push
```

## Stack

| Component | Library/DB |
|-----------|------------|
| Go version | 1.25.0 |
| Telegram API | `go-telegram-bot-api/telegram-bot-api/v5` v5.5.1 |
| ORM | `gorm.io/gorm` + `gorm.io/driver/sqlite` |
| DB engine | SQLite (`./data/tgvpn.db`) |
| Migrations | `golang-migrate/migrate/v4` (custom in `database/migrations.go`) |
| QR Code | `piglig/go-qr` v0.2.6 |
| Logging | `go.uber.org/zap` + `lumberjack.v2` |
| Error tracking | `getsentry/sentry-go` v0.44.1 |
| DI config | `joho/godotenv` v1.5.1 |
| CI/CD | GitHub Actions → golangci-lint, gosec, test, Docker → GHCR |

## Current State (v2.0.0 area)

### Working Features
- Telegram bot: /start, /help, inline keyboards (subscription, QR, donate, help)
- Admin: /lastreg, /del, /broadcast, /send — with input validation & rate limiting
- 3x-ui integration: auto-login, client CRUD, traffic check, circuit breaker
- SQLite: subscriptions CRUD, soft deletes, daily backups (14d retention)
- Health endpoints: `/healthz`, `/readyz` — JSON with DB + xui latency
- Invite/trial landing: `/i/{code}` — renders HTML page with Happ deep-links
- QR code generation (in-memory PNG, 512px)
- Rate limiting (token bucket, 30 tokens, 5/sec)
- Sentry error tracking, zap structured logging
- Docker: multi-stage build, healthcheck, GHCR images
- CI: lint, gosec, test, build, push

### Test Coverage
| Module | Coverage |
|--------|----------|
| config | 88.6% |
| database | 84.3% |
| xui | 88.3% |
| logger | 85.6% |
| utils | 80.0% |
| health/heartbeat/ratelimiter | 95-100% |
| **cmd/bot** | 4.5% (low) |
| **internal/bot** | 9.9% (low) |
| **Overall** | ~51% |

## Last Changes (this session)

Edited `internal/web/web.go` — two modifications to the trial invite landing page:

1. **Removed "Тестовая подписка" badge** — deleted `<span class="trial-badge">Тестовая подписка</span>` from `renderTrialPage()` and its `.trial-badge` CSS class
2. **Removed hardcoded logo dimensions** — removed `width="120" height="120"` from both `<img class="logo">` tags in `renderTrialPage()` and `renderErrorPage()`

All tests pass after changes. No other files were modified.

## Current Problem / Task

**Status:** web.go changes are done and tested. No explicit next task defined by the user.

**Possible next steps** (per PLAN.md):
- Phase 1: Expiry notifications, retry with jitter, audit logging, orphan xui client cleanup
- Phase 2: Admin dashboard, multi-lang, YAML config, Prometheus metrics
- Phase 3: Subscription caching, batch operations, multi-admin
- Phase 4: Multi-server subscription architecture
- Low coverage in `cmd/bot` (4.5%) and `internal/bot` (9.9%) could be addressed

## Critical Nuances

- **3x-ui session**: 15-minute expiry, auto re-login with exponential backoff, circuit breaker (5 failures → 30s open)
- **Subscriptions**: soft deletes (`deleted_at`), auto-renewal on last day of month, configurable traffic (default 100GB/month)
- **Trial flow**: `/i/{code}` → IP rate limit → xui login → create client → render HTML with `happ://add/` deep-link + Telegram activation link
- **Config**: `XUI_USERNAME` and `XUI_PASSWORD` have NO defaults — must be set explicitly
- **Web server**: runs on `HEALTH_CHECK_PORT` (default 8880), handles health + invite routes
- **Migrations**: custom system in `database/migrations.go`, not using golang-migrate directly (despite the dependency)
- **Telegram callback names**: `create_subscription`, `qr_code`, `back_to_subscription`, `menu_*`, `back_to_start`, `admin_*`
- **No secrets in code**: `.env` only, `.env.example` provides template

## Quick Commands

```bash
go test ./... -v                           # Run all tests
go build -o rs8kvn_bot ./cmd/bot           # Build binary
golangci-lint run ./...                     # Lint
gosec ./...                                 # Security scan
```
