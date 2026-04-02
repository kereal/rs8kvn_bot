# Handover Summary — TGVPN Go Bot

**Repo:** https://github.com/kereal/rs8kvn_bot  
**Module:** `rs8kvn_bot` (Go 1.25+)  
**Version:** v2.0.9  
**Last Updated:** 2026-04-02

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
- ✅ 📋 Subscription view — traffic usage, subscription link
- ✅ 📱 QR code generation — scannable by Happ app
- ✅ ☕ Donate, ❓ Help — auxiliary menus
- ✅ 🔗 Referral system — share links (`t.me/{bot}?start=share_{code}`)
- ✅ 🎁 Trial subscriptions via `/i/{code}` landing page

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
- ✅ Trial duplication prevention via HttpOnly cookie (3 hours)

### Test Coverage

| Module | Coverage | Status |
|--------|----------|--------|
| `internal/bot` | **97.7%** | ✅ Excellent |
| `internal/ratelimiter` | **100%** | ✅ Excellent |
| `internal/web` | **97.7%** | ✅ Excellent |
| `internal/heartbeat` | **95.8%** | ✅ Excellent |
| `internal/service` | **95.7%** | ✅ Excellent |
| `internal/health` | **93.5%** | ✅ Excellent |
| `internal/xui` | **91.1%** | ✅ Excellent |
| `internal/logger` | **87.6%** | ✅ Good |
| `internal/config` | **86.1%** | ✅ Good |
| `internal/backup` | **82.3%** | ✅ Good |
| `internal/database` | **80.1%** | ✅ Good |
| `internal/utils` | **75.0%** | ✅ Good |
| `cmd/bot` | **21.6%** | 🟡 Low (main is integration) |
| **Overall** | **~84%** | ✅ Good |

All tests pass with `-race` detector (0 failures, 15 packages).

---

## Last Changes (v2.0.3 — v2.0.9)

### E2E Test Suite (v2.0.5 — v2.0.6)
- **66 E2E tests** covering full subscription lifecycle:
  - Invite → trial → bind → QR code flow
  - Commands: `/start`, `/help`, `/invite`
  - Admin commands: `/del` (6 tests), `/broadcast` (4), `/send` (5)
  - Callbacks: `share_invite`, `qr_telegram`, `qr_web`, `back_to_invite`
  - Concurrency: race conditions, mutex protection, atomic operations
  - Rollback: XUI failure → no DB record, DB failure → XUI cleanup
  - Full cycles: InviteToQR, ShareToSubscription, MultipleUsers
- **Integration tests**: 7 tests (HandleStart, HandleHelp, HandleInvite, callbacks)
- **Database migration tests**: 11 edge cases (corrupted SQL, partial, duplicate, concurrent)
- **Fuzz tests**: config env vars, invite code regex, escapeMarkdown, xui truncate

### Test Coverage Improvements (v2.0.3 — v2.0.4)
- `internal/bot`: 95.1% → 97.7% — NewBotConfig, Self, QR error paths, StoreConversation, GetUserContext
- `internal/web`: 83.0% → 97.7% — handleInvite error paths, method not allowed, rate limit, XUI/DB failures
- `internal/database`: 79.2% → 80.1% — migration edge cases (corrupted SQL, partial, duplicate, empty, legacy, concurrent)
- `internal/health`: 90.3% → 93.5% — handleIndex, Stop nil server, healthz all down, readyz not ready
- `internal/backup`: 81.4% → 82.3% — validatePath (root, /dev, /var/run, relative, absolute, tmp)
- `internal/logger`: 82.3% → 87.6% — isStdoutError, Sentry hub paths, Close error aggregation
- `internal/config`: 83.2% → 86.1% — maskAPIKey
- `cmd/bot`: 19.6% → 21.6% — unknown command, non-command messages, panic recovery, callback query, version
- **Overall**: ~82% → ~84%

### golangci-lint Fixes (v2.0.7 — v2.0.8)
- **0 issues** remaining (was ~200)
- Production code: sentry.Flush before os.Exit, notifyAdmin error check, sqlDB.Close/Scan errors, errors.Is, %w wrapping
- Test code: removed unused mockBotAPIWithCounter, contains helpers, dbPath field, empty branches, De Morgan's law
- Created `.golangci.yml` with sensible defaults for test files

### Reliability Fixes (v2.0.9)
- **Cache cleanup goroutine** — `StartCacheCleanup()` now called in main.go, prevents memory leaks from expired entries
- **Trial rollback logging** — orphan XUI client errors now logged with client_id and warning
- **IP spoofing protection** — `getClientIP()` only trusts X-Forwarded-For from local/private addresses

---

## Last Changes (This Session - v2.0.2)

### Test Suite Optimization
- **Removed ~25 trivial/duplicate/skipped tests** — tests that verified Go language features instead of app logic
- **Added behavioral assertions** to all handler/callback/subscription tests — verify actual message content, not just "not panic"
- **Added missing test coverage** — QR code error paths, back navigation, notifyAdmin, createSubscription errors, admin stats
- **Fixed data races in mock infrastructure** — added mutex-protected accessors (`SendCalledSafe()`, `LastSentTextSafe()`, `RequestCalledSafe()`, `SendCountSafe()`) to MockBotAPI
- **Added call tracking to MockXUIClient** — `AddClientCalled`, `AddClientWithIDCalled`, `DeleteClientCalled`, `UpdateClientCalled` bool fields
- **Improved integration tests** — replaced recursive O(n²) `contains()` with `strings.Contains`, improved MockXUIServer with dedicated endpoint handlers
- **Added fuzzing tests** — `FuzzEscapeMarkdown`, `FuzzTruncateString`, `FuzzInviteCodeRegex`
- **All 14 packages pass with `-race` detector, 0 failures**
- **Bot package coverage: 95.1%**

---

## Last Changes (Previous Session - v2.0.1)

### 1. Trial Subscription Duplication Fix (`internal/web/web.go`)
- **Problem:** Refresh page = new trial subscription created
- **Solution:** HttpOnly cookie `rs8kvn_trial_{code}` for 3 hours
- **Effect:** Same user refresh = same trial, different users = different trials

### 2. Dynamic Bot Username (`internal/bot/handler.go`)
- **Problem:** Hardcoded `@rs8kvn_bot` in messages
- **Solution:** `botUsername` field in Handler, populated from `botAPI.Self.UserName`
- **Effect:** Works for any bot, no hardcoded usernames

### 3. Database Methods
- Added `GetTrialSubscriptionBySubID()` for trial lookup
- Updated interfaces and mocks

### 4. Docker Image
- Added `COPY internal/database/migrations` to Dockerfile
- Migrations now embedded in image (no volume mount needed)

---

## Current Problem / Task

**Status:** All tests passing, v2.0.1 ready.

**Completed:**
- ✅ Trial duplication prevention via cookie
- ✅ Dynamic bot username (no hardcoded values)
- ✅ Docker migrations fix
- ✅ Test coverage ~80%

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
- **Subscription:** `reset: 30` (last day of month), `expiryTime: 0` (no expiry)
- **Client email:** `trial_{subID}` for trial, `{username}` for regular

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → xui client (1GB, 3h) → bind via `/start trial_{subID}`
- **Regular:** `create_subscription` callback → xui client (30GB, no expiry, reset:30)
- **Share referral:** `pendingInvites[chatID]` cached for 60 minutes, `referred_by` set on creation
- **Trial cookie:** `rs8kvn_trial_{code}` prevents duplication for 3 hours

### Database
- **Soft deletes:** `deleted_at` column used (GORM)
- **Trial subscriptions:** `telegram_id = 0` (not NULL) until activated
- **Cleanup scheduler:** Hourly, removes unactivated trials after `TRIAL_DURATION_HOURS`

### Configuration
- **Required:** `XUI_USERNAME`, `XUI_PASSWORD` (NO defaults)
- **Validation:** `XUI_SUB_PATH` — only `a-zA-Z0-9_-`, no `..` or `/`
- **Web server:** Runs on `HEALTH_CHECK_PORT` (default 8880)
- **Bot username:** Auto-populated from `botAPI.Self.UserName`

### Telegram Callbacks
- `create_subscription`, `qr_code`, `back_to_subscription`, `menu_*`, `back_to_start`, `admin_*`, `share_invite`

### Security
- **No secrets in code:** `.env` only, `.env.example` provides template
- **Rate limiting:** Token bucket (30 tokens, 5/sec) per user
- **Input validation:** Markdown injection prevention, path traversal protection
- **Graceful shutdown:** Waits for in-flight requests with 30s timeout

### Docker
- **Migrations:** Embedded in image via `COPY internal/database/migrations`
- **No volume mount needed** for migrations
- **Data volume:** `./data:/app/data` for persistence

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

# Development with hot reload
air
```

---

**Generated:** 2026-04-02  
**Session:** E2E tests, golangci-lint fixes, reliability improvements  
**Version:** v2.0.9
