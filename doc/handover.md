# Handover Summary — rs8kvn_bot

**Repo:** https://github.com/kereal/rs8kvn_bot
**Module:** `rs8kvn_bot` (Go 1.24+)
**Version:** v2.2.0
**Branch:** `dev` (GitFlow: `main` = production, `dev` = integration)

---

## Architecture

```
rs8kvn_bot/
├── cmd/bot/main.go              # Entry point, signal handling, goroutine lifecycle
├── internal/
│   ├── bot/                      # Telegram bot handlers
│   │   ├── handler.go           # Handler struct, routing, keyboards, helpers
│   │   ├── callbacks.go         # Callback query routing
│   │   ├── commands.go          # /start, /help, share/trial bind
│   │   ├── subscription.go      # Subscription CRUD, QR code
│   │   ├── menu.go              # back_to_start, donate, help
│   │   ├── admin.go             # /del, /broadcast, /send, /refstats
│   │   ├── message.go           # Message sending with per-user rate limiting
│   │   ├── referral_cache.go   # Referral count cache (read-through, DB is source of truth)
│   │   └── cache.go             # Subscription LRU cache (lastAccess eviction)
│   ├── web/                      # HTTP server (health + invite + subscription proxy)
│   │   ├── web.go               # /healthz, /readyz, /i/{code}, /sub/{subID}
│   │   ├── middleware.go        # Bearer auth middleware (timing-safe comparison)
│   │   ├── api.go               # API endpoints (/api/v1/subscriptions)
│   │   ├── web_test.go          # Web endpoint tests
│   │   └── subproxy_test.go     # Subscription proxy handler tests
│   ├── subproxy/                 # Subscription proxy
│   │   ├── cache.go             # In-memory cache with TTL cleanup (240s)
│   │   ├── servers.go           # LoadExtraConfig: headers + servers from file
│   │   ├── proxy.go             # FetchFromXUI, DetectFormat, MergeSubscriptions
│   │   └── service.go           # Service struct, hot reload loop (5 min)
│   ├── service/                  # Subscription service layer (DB+XUI orchestration)
│   │   └── subscription.go      # Create, Delete, GetWithTraffic, CreateTrial
│   ├── xui/                      # 3x-ui panel API client + circuit breaker
│   │   ├── client.go            # Login, CreateClient, GetClientTraffic, retry+jitter, singleflight
│   │   └── breaker.go           # Circuit breaker (5 failures → 30s open)
│   ├── database/                 # GORM SQLite + migrations
│   ├── config/                   # Env var loader + constants
│   ├── logger/                   # Zap + Sentry + lumberjack rotation
│   ├── interfaces/               # Service interfaces for DI
│   ├── utils/                    # UUID, SubID, QR, time, format helpers
│   │   ├── format.go            # DaysUntilReset, FormatDateRu, GenerateProgressBar (shared)
│   │   └── format_test.go       # Shared format tests
│   ├── ratelimiter/              # Token bucket + PerUserRateLimiter
│   ├── heartbeat/                # Periodic health pings
│   ├── backup/                   # Daily SQLite backup rotation (14 days)
│   ├── webhook/                  # Webhook sender for Proxy Manager
│   └── scheduler/                # Cron: backup (03:00), trial cleanup (hourly)
├── tests/e2e/                    # E2E test suite (66+ scenarios)
├── data/                         # Runtime: tgvpn.db, backups, bot.log, extra_servers.txt
├── doc/                          # handover.md, feature specs, research
├── Dockerfile, docker-compose.yml, .env.example
└── .github/                      # CI: lint, gosec, test, Docker → GHCR
```

## Stack

| Component | Library/DB | Version |
|-----------|------------|---------|
| Go | `go` | 1.24+ |
| Telegram API | `go-telegram-bot-api/telegram-bot-api/v5` | v5.5.1 |
| ORM | `gorm.io/gorm` + `gorm.io/driver/sqlite` | v1.31.1 |
| DB engine | SQLite (mattn/go-sqlite3, CGO) | `./data/tgvpn.db` |
| Migrations | `golang-migrate/migrate/v4` | v4.19.1 |
| QR Code | `piglig/go-qr` | v0.2.6 |
| Logging | `go.uber.org/zap` + `lumberjack.v2` | v1.27.1 |
| Error tracking | `getsentry/sentry-go` | v0.44.1 |
| Concurrency | `golang.org/x/sync/singleflight` | — |
| Testing | `stretchr/testify` | v1.11.1 |
| CI/CD | GitHub Actions → golangci-lint, gosec, test, Docker → GHCR | — |

## Current State

### Working Features

**User Features:**
- `/start`, `/help` — start commands with inline keyboards
- 📋 Subscription view — traffic usage, subscription link, QR code
- ☕ Donate, ❓ Help — auxiliary menus
- 🔗 Referral system — share links with in-memory cache + periodic DB sync
- 🎁 Trial subscriptions via `/i/{code}` landing page with Happ deep-links

**Admin Features:**
- `/del <id>` — delete subscription by ID
- `/broadcast <msg>` — send message to all users (respects shutdown context)
- `/send <id|username> <msg>` — send private message
- `/refstats` — referral statistics
- 📊 Stats — bot statistics

**Infrastructure:**
- 3x-ui integration — auto-login, client CRUD, circuit breaker, retry with jitter, singleflight
- Health endpoints — `/healthz` (503 when Down), `/readyz`
- Invite/trial landing — `/i/{code}` with IP rate limiting, cookie dedup
- Per-user rate limiting — per-chatID token bucket (30 tokens, 5/sec refill)
- Subscription proxy — `GET /sub/{subID}` with extra servers + headers, cache, singleflight
- Daily backups — 14 days retention
- Sentry error tracking, Zap structured logging
- Docker: multi-stage build, healthcheck, GHCR images
- CI: lint, gosec, test, build, push

### Test Coverage

| Module | Coverage | Status |
|--------|----------|--------|
| `internal/flag` | **97.7%** | ✅ |
| `internal/ratelimiter` | **97.4%** | ✅ |
| `internal/heartbeat` | **96.2%** | ✅ |
| `internal/service` | **95.2%** | ✅ |
| `internal/config` | **91.8%** | ✅ |
| `internal/xui` | **90.9%** | ✅ |
| `internal/web` | **90.3%** | ✅ |
| `internal/bot` | **92.6%** | ✅ |
| `internal/utils` | **90.0%** | ✅ |
| `internal/logger` | **88.9%** | ✅ |
| `internal/backup` | **83.2%** | ✅ |
| `internal/subproxy` | **82.5%** | ✅ |
| `internal/scheduler` | **81.2%** | ✅ |
| `internal/database` | **77.8%** | 🟡 |
| `cmd/bot` | **5.4%** | 🟡 (integration) |
| **Overall** | **~85%** | ✅ |

All tests pass with `-race` detector. golangci-lint: 0 issues in production code.

---

## Critical Nuances

### 3x-ui Integration
- **Session:** 12h validity (configurable via `XUI_SESSION_MAX_AGE_MINUTES`, default 720), verified via `/panel/api/server/status`
- **Auto-relogin:** On HTTP 401/redirect, re-authenticates then retries failed request
- **Connection pool cleanup:** Before re-auth to prevent dead connections
- **Circuit breaker:** 5 failures → 30s open state
- **Subscription:** `reset: 30` (days from creation), `expiryTime: now + 30 days`
- **Auto-reset:** Only works when `ExpiryTime > 0`. Traffic resets every 30 days, expiry extends.
- **Client email:** `trial_{subID}` for trial, `{username}` for regular
- **Ping vs Login:** Health checks use `Ping()` → `ensureLoggedIn(ctx, false)` — no forced re-auth
- **Singleflight:** Deduplicates concurrent login attempts
- **DNS error fast-fail:** Non-retryable errors fail immediately

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → xui client (1GB, 3h) → bind via `/start trial_{subID}`
- **Regular:** `create_subscription` callback → xui client (30GB, expiryTime: now + 30 days, reset: 30)
- **Trial cookie:** `rs8kvn_trial_{code}` prevents duplication for 3 hours (HttpOnly)
- **Atomic cleanup:** `DELETE ... RETURNING` for expired trials
- **Share referral:** `pendingInvites[chatID]` cached 60 min (in-memory, periodic cleanup prevents leak)

### Subscription Deletion (v2.2.0+)
- **Order:** DB-first, then XUI-best-effort
- **Rationale:** If DB delete fails → XUI untouched (safe to retry). If XUI fails after DB success → orphaned client (less critical, manual cleanup).
- **Webhook:** Sent on successful DB deletion regardless of XUI outcome.
- **Referral cache:** `DecrementReferralCount` called after successful deletion.

### Subscription Proxy
- **Endpoint:** `GET /sub/{subID}` — subID = SubscriptionID from DB
- **Extra config:** Headers section → blank line → server links. Headers override 3x-ui.
- **Cache:** 240s TTL hardcoded (`config.SubProxyCacheTTL`)
- **Reload:** Every 5 minutes, graceful — keeps old config if file read fails
- **Singleflight:** First request fetches, others wait and get same result
- **Content-Length:** Removed after merge (body size changes, Go uses chunked encoding)
- **No rate limiting** on `/sub/` — 240s cache TTL mitigates abuse

### Referral Cache
- **Source of truth:** subscriptions table (`SELECT referred_by, COUNT(*) GROUP BY referred_by`)
- **Cache purpose:** Read-through for real-time display without hitting DB
- **Save() is no-op:** DB already reflects correct count after changes
- **Sync():** Calls `Load()` hourly to refresh from DB
- **Admin rate limit:** 30s cooldown between `/send` commands per admin (sync.Map)

### Database
- **Soft deletes:** `deleted_at` column (GORM)
- **Trial subscriptions:** `telegram_id = 0` (not NULL) until activated
- **Migrations:** Auto-applied on startup from `internal/database/migrations/`
- **trial_requests cleanup:** 1-hour cutoff (matching rate-limit window)

### Configuration
- **Required:** `XUI_USERNAME`, `XUI_PASSWORD` (NO defaults)
- **Validation:** `XUI_SUB_PATH` — only `a-zA-Z0-9_-`, no `..` or `/`
- **Web server:** Runs on `HEALTH_CHECK_PORT` (default 8880)
- **Init failure:** Fatal exit for DB, XUI, and Bot API init errors

### Rate Limiting
- **Per-user:** Each `chatID` gets own token bucket (30 tokens, 5/sec refill)
- **Cleanup:** Stale buckets removed every 5 minutes (maxIdle = 10 minutes)

### Security
- **IP spoofing:** X-Forwarded-For trusted only from **loopback** (127.0.0.1, ::1). Private IPs NOT trusted.
- **API auth:** Timing-safe token comparison via `crypto/subtle.ConstantTimeCompare`
- **No secrets in code:** `.env` only
- **HTTP timeouts:** ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s, IdleTimeout 60s
- **Port binding:** Verified before goroutine launch — `net.Listen()` then `Serve()`

### Health Checks
- **`/healthz`:** Returns 200 (OK/Degraded) or 503 (Down)
- **`/readyz`:** Returns 200 when all components initialized and ready flag is set

### Docker
- **Migrations:** Embedded via `COPY internal/database/migrations`
- **Data volume:** `./data:/app/data`
- **Health check:** HTTP `/healthz` on port 8880

---

## Quick Commands

```bash
# Run all tests with race detector
go test -race -count=1 ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Build binary
go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot

# Run linters
golangci-lint run ./...

# Run locally
go run ./cmd/bot
```
