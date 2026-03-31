# Handover Summary — TGVPN Go Bot

**Repo:** https://github.com/kereal/rs8kvn_bot  
**Module:** `rs8kvn_bot` (Go 1.25+)  
**Version:** v2.0.0  
**Last Updated:** 2026-03-31

---

## Architecture

```
tgvpn_go/
├── cmd/bot/main.go              # Entry point, signal handling, goroutine lifecycle
├── internal/
│   ├── bot/                      # Telegram bot handlers
│   │   ├── handler.go           # Handler struct, keyboards, helper functions
│   │   ├── callbacks.go         # Callback query routing
│   │   ├── commands.go          # /start, /help, share referral handling
│   │   ├── subscription.go      # Subscription CRUD, QR code
│   │   ├── menu.go              # back_to_start, donate, help
│   │   ├── admin.go             # /lastreg, /del, /broadcast, /send
│   │   └── message.go           # Message sending utilities
│   ├── web/                      # HTTP server (health + invite/trial pages)
│   │   ├── web.go               # /healthz, /readyz, /i/{code} — trial invite landing
│   │   └── web_test.go
│   ├── xui/                      # 3x-ui panel API client + circuit breaker
│   │   ├── client.go            # Login, CreateClient, GetClientTraffic, etc.
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
├── doc/                          # PLAN.md, HANDOVER.md, share-and-referral.md
├── Dockerfile, docker-compose.yml, .env.example
└── .github/                      # CI: lint, gosec, test, Docker build + GHCR push
```

## Stack

| Component | Library/DB | Version |
|-----------|------------|---------|
| Go | `go` | 1.25.0 |
| Telegram API | `go-telegram-bot-api/telegram-bot-api/v5` | v5.5.1 |
| ORM | `gorm.io/gorm` + `gorm.io/driver/sqlite` | v1.31.1 |
| DB engine | SQLite | `./data/tgvpn.db` |
| Migrations | `golang-migrate/migrate/v4` | v4.19.1 |
| QR Code | `piglig/go-qr` | v0.2.6 |
| Logging | `go.uber.org/zap` + `lumberjack.v2` | v1.27.1 |
| Error tracking | `getsentry/sentry-go` | v0.44.1 |
| DI config | `joho/godotenv` | v1.5.1 |
| Testing | `stretchr/testify` | v1.11.1 |
| CI/CD | GitHub Actions → golangci-lint, gosec, test, Docker → GHCR | — |

## Current State

### Working Features

**User Features:**
- ✅ `/start`, `/help` — start commands with inline keyboards
- ✅ 📋 Subscription view — traffic usage, reset date, subscription link
- ✅ 📱 QR code generation — scannable by Happ app
- ✅ ☕ Donate, ❓ Help — auxiliary menus
- ✅ 🔗 Referral system — share links (`t.me/rs8kvn_bot?start=share_{code}`)

**Admin Features:**
- ✅ `/lastreg` — last 10 registered users
- ✅ `/del <id>` — delete subscription by ID
- ✅ `/broadcast <msg>` — send message to all users with subscription
- ✅ `/send <id|username> <msg>` — send private message
- ✅ 📊 Stats — bot statistics (total users, active subscriptions)

**Infrastructure:**
- ✅ 3x-ui integration — auto-login, client CRUD, traffic check, circuit breaker (5 failures → 30s open)
- ✅ Health endpoints — `/healthz`, `/readyz` with JSON response
- ✅ Invite/trial landing — `/i/{code}` with Happ deep-links
- ✅ Rate limiting — token bucket (30 tokens, 5/sec refill)
- ✅ Daily backups — 14 days retention
- ✅ Sentry error tracking, Zap structured logging
- ✅ Docker: multi-stage build, healthcheck, GHCR images
- ✅ CI: lint, gosec, test, build, push

### Test Coverage

| Module | Coverage | Status |
|--------|----------|--------|
| `internal/bot` | **95.3%** | ✅ Excellent |
| `internal/ratelimiter` | **100%** | ✅ Excellent |
| `internal/heartbeat` | **95.8%** | ✅ Excellent |
| `internal/health` | **90.3%** | ✅ Excellent |
| `internal/xui` | **86.8%** | ✅ Good |
| `internal/config` | **83.2%** | ✅ Good |
| `internal/web` | **83.0%** | ✅ Good |
| `internal/logger` | **82.3%** | ✅ Good |
| `internal/backup` | **81.4%** | ✅ Good |
| `internal/database` | **79.2%** | ✅ Good |
| `internal/utils` | **75.0%** | ✅ Good |
| `cmd/bot` | **19.6%** | 🟡 Low (main is 0%) |
| **Overall** | **~80%** | ✅ Good |

---

## Last Changes (This Session)

### 1. Share Referral Handling (`internal/bot/commands.go`)
- Added `handleShareStart()` for processing `t.me/rs8kvn_bot?start=share_{invite_code}`
- Users with existing subscription see main menu (share code ignored)
- New users: invite code cached for 60 minutes in `pendingInvites` map
- On subscription creation: `referred_by` set from invite's `referrer_tg_id`

### 2. Subscription Expiry Changes (`internal/bot/subscription.go`, `internal/xui/client.go`)
- Non-trial subscriptions: `expiryTime = 0` (no expiry), `reset = 30` (last day of month)
- Added `getExpiryTimeMillis()` helper — returns 0 for `time.Time{}`
- Removed `sub.IsExpired()` check from `handleCreateSubscription`
- Removed expiry date from user-facing messages

### 3. Bot Message Improvements
- **Invite link message** — added emoji icons (🔗, 📱, 🌐, 📤, 💎), section headers
- **Removed expiry mentions** — no "Сброс", "Истекает", "Истекшие" in messages
- **Admin notification** — removed expiry date
- **Admin stats** — removed "Истекшие" count
- **Error messages for `/del`** — shortened (removed detailed error text)

### 4. Test Improvements
- Added 3 tests for `handleShareStart` (coverage: 0% → 100%)
- Consolidated 11 `FirstSecondOfNextMonth` tests into 1 table-driven test (52% reduction)
- Added 6 tests for `cmd/bot/main.go` (coverage: 10.8% → 19.6%)
- Fixed `TestCircuitBreaker_HalfOpen_MaxAttempts`

### 5. Configuration Validation (`internal/config/config.go`)
- Added `XUI_SUB_PATH` validation: no `..` or `/`, only `a-zA-Z0-9_-`
- Fixed 4 failing tests (changed `"/xui"` → `"xui"`)

### 6. Documentation
- Updated `doc/share-and-referral.md` — removed "Known Issues" and "Technical Details" sections
- Updated `doc/HANDOVER.md` — this file

---

## Current Problem / Task

**Status:** All tests passing (13/13 packages), coverage ~80%.

**Completed:**
- ✅ Share referral handling with 60-minute cache
- ✅ Subscription expiry set to 0 (no expiry) with monthly reset (day 30)
- ✅ Removed expiry date mentions from all user-facing messages
- ✅ Test coverage improved from ~51% to ~80%
- ✅ Configuration validation for `XUI_SUB_PATH`

**Next Steps** (per `doc/PLAN.md`):
- **Phase 1:** Expiry notifications (7, 3, 1 days before), retry with jitter, audit logging, orphan xui client cleanup
- **Phase 2:** Admin dashboard (`/dashboard`, `/extend`, `/settraffic`), multi-lang, YAML config, Prometheus metrics
- **Phase 3:** Subscription caching, batch operations, multi-admin support
- **Phase 4:** Multi-server subscription architecture

---

## Critical Nuances

### 3x-ui Integration
- **Session:** 15-minute validity, auto re-login with exponential backoff
- **Circuit breaker:** 5 failures → 30s open state
- **Subscription reset:** `reset: 30` (last day of month), `expiryTime: 0` (no expiry)
- **Client email:** `trial_{subID}` for trial, `{username}` for regular

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → xui client (1GB, 3h) → bind via `/start trial_{subID}`
- **Regular:** `create_subscription` callback → xui client (100GB, no expiry, reset:30)
- **Share referral:** `pendingInvites[chatID]` cached for 60 minutes, `referred_by` set on creation

### Database
- **Soft deletes:** `deleted_at` column used (GORM)
- **Trial subscriptions:** `telegram_id = 0` (not NULL) until activated
- **Cleanup scheduler:** Hourly, removes unactivated trials after `TRIAL_DURATION_HOURS`

### Configuration
- **Required:** `XUI_USERNAME`, `XUI_PASSWORD` (NO defaults)
- **Validation:** `XUI_SUB_PATH` — only `a-zA-Z0-9_-`, no `..` or `/`
- **Web server:** Runs on `HEALTH_CHECK_PORT` (default 8880)

### Telegram Callbacks
- `create_subscription`, `qr_code`, `back_to_subscription`, `menu_*`, `back_to_start`, `admin_*`, `share_invite`

### Security
- **No secrets in code:** `.env` only, `.env.example` provides template
- **Rate limiting:** Token bucket (30 tokens, 5/sec) per user
- **Input validation:** Markdown injection prevention, path traversal protection

---

## Quick Commands

```bash
# Run all tests
go test ./... -v

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Build binary
go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot

# Run linters
golangci-lint run ./...
gosec ./...

# Run locally
go run ./cmd/bot
```

---

**Generated:** 2026-03-31  
**Session:** Share referral handling, expiry removal, test improvements
