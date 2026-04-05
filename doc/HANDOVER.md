# Handover Summary — TGVPN Go Bot

**Repo:** https://github.com/kereal/rs8kvn_bot  
**Module:** `rs8kvn_bot` (Go 1.24+)  
**Version:** v2.2.0  
**Last Updated:** 2026-04-05  
**Branch:** `dev` (GitFlow: `main` = production, `dev` = integration)

---

## Architecture

```
tgvpn_go/
├── cmd/bot/main.go              # Entry point, signal handling, goroutine lifecycle
├── internal/
│   ├── bot/                      # Telegram bot handlers
│   │   ├── handler.go           # Handler struct, routing, keyboards, helpers
│   │   ├── callbacks.go         # Callback query routing
│   │   ├── commands.go          # /start, /help, share/trial bind
│   │   ├── subscription.go      # Subscription CRUD, QR code
│   │   ├── menu.go              # back_to_start, donate, help
│   │   ├── admin.go             # /del, /broadcast, /send, admin stats
│   │   ├── message.go           # Message sending with per-user rate limiting
│   │   └── cache.go             # Subscription LRU cache (lastAccess eviction)
│   ├── web/                      # HTTP server (health + invite/trial pages + subscription proxy)
│   │   └── web.go               # /healthz, /readyz, /i/{code}, /sub/{subID}
│   ├── xui/                      # 3x-ui panel API client + circuit breaker
│   │   ├── client.go            # Login, CreateClient, GetClientTraffic, retry+jitter
│   │   └── breaker.go           # Circuit breaker (5 failures → 30s open)
│   ├── database/                 # GORM SQLite + migrations
│   ├── config/                   # Env var loader + constants
│   ├── logger/                   # Zap + Sentry + lumberjack rotation
│   ├── interfaces/               # Service interfaces for DI
│   ├── utils/                    # UUID, SubID, QR, time helpers
│   ├── ratelimiter/              # Token bucket + PerUserRateLimiter
│   ├── subproxy/                 # Subscription proxy (cache, merge, extra config)
│   │   ├── cache.go             # In-memory cache with TTL cleanup
│   │   ├── servers.go           # Load extra headers + servers from file
│   │   ├── proxy.go             # FetchFromXUI, DetectFormat, MergeSubscriptions
│   │   └── service.go           # Service struct, reload loop
│   ├── heartbeat/                # Periodic health pings
│   ├── backup/                   # Daily SQLite backup rotation
│   └── health/                   # Health checker (legacy, unused — web/ replaces it)
├── tests/e2e/                    # E2E test suite (66+ scenarios)
├── data/                         # Runtime: tgvpn.db, backups, bot.log
├── doc/                          # PLAN.md, HANDOVER.md, IMPROVEMENTS.md
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
| DI config | `joho/godotenv` | v1.5.1 |
| Testing | `stretchr/testify` | v1.11.1 |
| CI/CD | GitHub Actions → golangci-lint, gosec, test, Docker → GHCR | — |

**Note:** `modernc.org/sqlite` is an indirect dependency from `golang-migrate`. It is not imported in code and does not affect the binary. Cannot be excluded (migrate pulls newer version).

## Current State

### Working Features

**User Features:**
- ✅ `/start`, `/help` — start commands with inline keyboards
- ✅ 📋 Subscription view — traffic usage, subscription link, QR code
- ✅ ☕ Donate, ❓ Help — auxiliary menus
- ✅ 🔗 Referral system — share links (`t.me/{bot}?start=share_{code}`) with in-memory cache + periodic DB sync
- ✅ 🎁 Trial subscriptions via `/i/{code}` landing page with Happ deep-links

**Admin Features:**
- ✅ `/del <id>` — delete subscription by ID
- ✅ `/broadcast <msg>` — send message to all users (detached context)
- ✅ `/send <id|username> <msg>` — send private message
- ✅ `/refstats` — referral statistics (count per user from cache)
- ✅ 📊 Stats — bot statistics

**Infrastructure:**
- ✅ 3x-ui integration — auto-login, client CRUD, traffic check, circuit breaker, retry with jitter
- ✅ Health endpoints — `/healthz`, `/readyz` with component checkers (DB ping, XUI ping)
- ✅ HTTP server timeouts — ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s (slowloris protection)
- ✅ Invite/trial landing — `/i/{code}` with IP rate limiting, trial creation, cookie dedup
- ✅ Per-user rate limiting — each user gets own token bucket (30 tokens, 5/sec refill)
- ✅ Daily backups — 14 days retention
- ✅ Sentry error tracking, Zap structured logging
- ✅ Docker: multi-stage build, healthcheck, GHCR images
- ✅ CI: lint, gosec, test, build, push (triggers: push to main, PR, tags)
- ✅ Trial duplication prevention via HttpOnly cookie (3 hours)
- ✅ IP spoofing protection — X-Forwarded-For trusted only from local/private addresses
- ✅ Cache cleanup goroutine — prevents memory leaks from expired entries
- ✅ Atomic trial cleanup — `DELETE ... RETURNING` eliminates race condition
- ✅ Referral cache — in-memory map with periodic sync, `/refstats` admin command
- ✅ Atomic trial rollback — retry with backoff on cleanup failure
- ✅ Atomic subscription locking — `sync.Map` with `LoadOrStore` for race-safe creation
- ✅ Singleflight for XUI login — deduplicates concurrent login attempts
- ✅ SQLite connection pool — configured for single-writer reliability

### Test Coverage

| Module | Coverage | Status |
|--------|----------|--------|
| `internal/ratelimiter` | **97.5%** | ✅ Excellent |
| `internal/bot` | **94.2%** | ✅ Excellent |
| `internal/web` | **96.7%** | ✅ Excellent |
| `internal/heartbeat` | **95.8%** | ✅ Excellent |
| `internal/service` | **95.7%** | ✅ Excellent |
| `internal/xui` | **91.1%** | ✅ Excellent |
| `internal/logger` | **87.6%** | ✅ Good |
| `internal/config` | **87.3%** | ✅ Good |
| `internal/database` | **82.9%** | ✅ Good |
| `internal/backup` | **82.3%** | ✅ Good |
| `internal/utils` | **75.0%** | ✅ Good |
| `cmd/bot` | **14.9%** | 🟡 Low (main is integration) |
| **Overall** | **~75%** | ✅ Good |

All tests pass with `-race` detector (0 failures, 15 packages). golangci-lint: 0 issues.

Test suite includes:
- **66+ E2E tests** — full subscription lifecycle: invite→trial→bind, commands, callbacks, admin operations, concurrency, rollback scenarios
- **Integration tests** — 7 tests (HandleStart, HandleHelp, HandleInvite, callbacks)
- **Database migration tests** — 11 edge cases (corrupted SQL, partial, duplicate, concurrent)
- **Fuzz tests** — config env vars, invite code regex, escapeMarkdown, xui truncate
- **Per-user rate limiter tests** — 16 tests (isolation, cleanup, concurrency, context cancellation)

---

## Last Changes (v2.2.0 - Current Session)

### Subscription Proxy (GET /sub/{subID})
- **Problem:** Need HTTP endpoint to serve subscriptions with extra servers and custom headers
- **Solution:** Full subscription proxy implementation:
  - `GET /sub/{subID}` — validates in DB, fetches from 3x-ui, merges extra config, caches
  - Extra config file format: headers section → blank line → server links
  - Headers from config file override 3x-ui headers
  - In-memory cache with 240s TTL, background cleanup
  - Singleflight for concurrent request deduplication
  - Hot reload of config file every 5 minutes
  - Stale cache fallback on 3x-ui error
  - Format detection: base64 (decode → merge → re-encode) and plain text
- **Files:** `internal/subproxy/` (new package), `internal/web/web.go`, `internal/config/`, `cmd/bot/main.go`
- **Tests:** 50 tests (82.5% subproxy coverage, 90.7% web coverage)
- **Config:** `SUB_EXTRA_SERVERS_ENABLED`, `SUB_EXTRA_SERVERS_FILE`

### Previous Sessions (v2.0.3 — v2.1.0)

### Referral Cache System
- **Problem:** Dead code `referrals` map existed but wasn't used
- **Solution:** Implemented full referral cache system with:
  - Database methods: `GetReferralCount`, `GetAllReferralCounts`
  - Handler cache methods: `LoadReferralCache`, `GetReferralCount`, `IncrementReferralCount`, `DecrementReferralCount`
  - Periodic sync: `StartReferralCacheSync` (every 5 minutes)
  - Admin command: `/refstats` shows referral count per user
- **Files:** `database/database.go`, `interfaces/interfaces.go`, `bot/handler.go`, `bot/subscription.go`, `bot/admin.go`, `testutil/testutil.go`, `cmd/bot/main.go`
- **Commit:** `66f5d86`

### Trial Atomic Rollback
- **Problem:** Trial creation could leave orphaned client in 3x-ui if cleanup failed
- **Solution:** Exported `RetryWithBackoff` from xui package, used for rollback with up to 3 retries
- **Files:** `xui/client.go`, `web/web.go`
- **Commit:** `d2d8c16`

### Subscription Locking with sync.Map
- **Problem:** Manual lock/unlock via map could deadlock on panic
- **Solution:** Replaced `map + sync.Mutex` with `sync.Map` using `LoadOrStore` for atomic operations
- **Files:** `bot/handler.go`, `bot/subscription.go`
- **Commit:** `113f2bf`

### Singleflight for XUI Login
- **Problem:** Concurrent requests after session expiry could trigger multiple logins
- **Solution:** Added `singleflight.Group` to deduplicate concurrent login attempts
- **Files:** `xui/client.go`
- **Commit:** `1857ae8`

### Per-User Rate Limiter (Previous)
- **Problem:** Global token bucket shared across all users — one active user can exhaust all 30 tokens, starving others
- **Solution:** `PerUserRateLimiter` in `internal/ratelimiter/per_user.go` — separate bucket per `chatID`
- **Files changed:** `ratelimiter/per_user.go` (new), `bot/handler.go`, `bot/message.go`, `cmd/bot/main.go`
- **Cleanup:** Background goroutine removes stale buckets (interval = CacheTTL, maxIdle = 2×CacheTTL)
- **Tests:** 16 new tests in `per_user_test.go`

### Cache LRU Fix
- **Problem:** Cache eviction was by `expiresAt` — frequently accessed entries could be evicted before rarely accessed ones
- **Solution:** Added `lastAccess` field to `cacheEntry`, eviction now by least recently used
- **Files changed:** `internal/bot/cache.go`
- **Tests:** Existing cache tests pass (behavior unchanged externally)

### modernc.org/sqlite Analysis
- **Finding:** Indirect dependency from `golang-migrate`, not imported in code, does not affect binary
- **Decision:** Keep as-is — cannot exclude without migrate pulling newer version

### Auto-Reset Traffic Mechanism (v2.1.0)
- **Research:** Investigated 3x-ui source code to understand `reset` field behavior
- **Finding:** `reset` is interval in days from creation date, NOT day of month
- **Key insight:** Auto-reset only works when `expiryTime > 0` (was zero before)
- **Fix:** Changed subscription creation to set `expiryTime = now + 30 days`
- **Rename:** `SubscriptionResetDay` → `SubscriptionResetIntervalDays` (clearer name)
- **Sync:** Added ExpiryTime synchronization from 3x-ui on subscription view
- **Tests:** 8 new tests for `daysUntilReset` function
- **Docs:** Updated README.md with correct explanation + source link

### Previous Sessions (v2.0.3 — v2.1.0)
- Command routing moved from `cmd/bot/main.go` to `bot.Handler.HandleUpdate()`
- Atomic trial cleanup with `DELETE ... RETURNING`
- `CONTACT_USERNAME` env var replaces hardcoded `@kereal`
- CI PR trigger, docker-compose tag fix, HTTP timeouts, broadcast detached context
- Health check uses `Ping()` instead of `Login()`
- Jitter in retry backoff, constants immutability, dead code removal
- Dependencies cleaned — `air` tool and `hugo` removed from go.mod
- EXPOSE 0 → 8880 in Dockerfile, username escaping in `/lastreg`

---

## Current Problem / Task

**Status:** All tests passing, golangci-lint: 0 new issues.

**Completed this session:**
- ✅ Subscription Proxy feature (GET /sub/{subID})
- ✅ Extra config file with headers + servers
- ✅ In-memory cache with TTL cleanup
- ✅ Singleflight for concurrent deduplication
- ✅ Hot reload of config file every 5 minutes
- ✅ Stale cache fallback on 3x-ui error
- ✅ 50 tests (82.5% subproxy, 90.7% web coverage)
- ✅ Logging throughout the proxy flow
- ✅ Content-Length header fix after merge
- ✅ Error differentiation (404 vs 502)
- ✅ .env.example updated with new config vars

**Remaining tasks (prioritized):**
1. **Re-enable linters** — errcheck, gosec in `.golangci.yml` (P1)
2. **Multi-arch Docker** — linux/amd64 + linux/arm64 (P2)
3. **Docker image on push to main** — CI build `main`/`dev` tag (P2)
4. **Traffic alerts** — 80%/95% usage notifications (P3)
5. **Multi-admin** — list of admin IDs instead of single (P3)

**Cancelled:**
- ~~Expiry notifications~~ — subscriptions are permanent (`expiryTime = 0`), not needed
- ~~Prometheus metrics~~ — out of scope for this project

---

## Critical Nuances

### 3x-ui Integration
- **Session:** 15-minute validity, auto re-login with exponential backoff + jitter
- **Circuit breaker:** 5 failures → 30s open state
- **Subscription:** `reset: 30` (last day of month), `expiryTime: 0` (no expiry)
- **Client email:** `trial_{subID}` for trial, `{username}` for regular
- **Ping vs Login:** Health checks use `Ping()` (calls `ensureLoggedIn(ctx, false)`) — no forced re-auth

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → xui client (1GB, 3h) → bind via `/start trial_{subID}`
- **Regular:** `create_subscription` callback → xui client (30GB, expiryTime: now + 30 days, reset: 30)
- **Auto-reset:** Traffic resets every 30 days, expiryTime extends automatically by 3x-ui
- **ExpiryTime sync:** Updated from 3x-ui on subscription view (see task #34 in IMPROVEMENTS.md)
- **Share referral:** `pendingInvites[chatID]` cached for 60 minutes, `referred_by` set on creation
- **Trial cookie:** `rs8kvn_trial_{code}` prevents duplication for 3 hours
- **Atomic cleanup:** `DELETE ... RETURNING` for expired trials (SQLite 3.35+, PostgreSQL)

### Database
- **Soft deletes:** `deleted_at` column used (GORM)
- **Trial subscriptions:** `telegram_id = 0` (not NULL) until activated
- **Cleanup scheduler:** Hourly, removes unactivated trials after `TRIAL_DURATION_HOURS`
- **Migrations:** Auto-applied on startup from `internal/database/migrations/`

### Configuration
- **Required:** `XUI_USERNAME`, `XUI_PASSWORD` (NO defaults)
- **Validation:** `XUI_SUB_PATH` — only `a-zA-Z0-9_-`, no `..` or `/`
- **Web server:** Runs on `HEALTH_CHECK_PORT` (default 8880)
- **Bot username:** Auto-populated from `botAPI.Self.UserName`
- **Contact username:** `CONTACT_USERNAME` env var (default `kereal`)

### Rate Limiting
- **Per-user:** Each `chatID` gets own token bucket (30 tokens, 5/sec refill)
- **Cleanup:** Stale buckets removed every 5 minutes (maxIdle = 10 minutes)
- **Admin rate limit:** Separate `sync.Map` tracking — 6s min interval between `/send` commands

### Telegram Callbacks
- `create_subscription`, `qr_code`, `back_to_subscription`, `menu_*`, `back_to_start`, `admin_*`, `share_invite`, `qr_telegram`, `qr_web`, `back_to_invite`

### Security
- **IP spoofing:** X-Forwarded-For trusted only from local/private addresses (127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
- **No secrets in code:** `.env` only, `.env.example` provides template
- **Input validation:** Markdown injection prevention, path traversal protection
- **Graceful shutdown:** Waits for in-flight requests with 30s timeout
- **HTTP timeouts:** ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s, IdleTimeout 60s

### Subscription Proxy
- **Endpoint:** `GET /sub/{subID}` where subID = SubscriptionID from DB
- **Extra config file format:** headers (`Key: Value`) → blank line → server links
- **Headers:** Config headers override 3x-ui headers, all original headers preserved
- **Cache:** 240s TTL, in-memory, background cleanup every 120s
- **Reload:** Config file reloaded every 5 minutes automatically
- **Singleflight:** Concurrent requests to same subID deduplicated
- **Fallback:** Stale cache returned if 3x-ui is unavailable
- **Content-Length:** Removed after merge (body size changes)

### Docker
- **Migrations:** Embedded in image via `COPY internal/database/migrations`
- **Data volume:** `./data:/app/data` for persistence
- **Health check:** HTTP `/healthz` on port 8880
- **Image tag:** `latest` in docker-compose.yml

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

---

**Generated:** 2026-04-05  
**Session:** Subscription Proxy feature (GET /sub/{subID})  
**Version:** v2.2.0
