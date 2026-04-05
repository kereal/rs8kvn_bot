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
│   │   ├── admin.go             # /del, /broadcast, /send, /refstats
│   │   ├── message.go           # Message sending with per-user rate limiting
│   │   └── cache.go             # Subscription LRU cache (lastAccess eviction)
│   ├── web/                      # HTTP server (health + invite + subscription proxy)
│   │   ├── web.go               # /healthz, /readyz, /i/{code}, /sub/{subID}
│   │   ├── web_test.go          # Web endpoint tests
│   │   └── subproxy_test.go     # Subscription proxy handler tests
│   ├── subproxy/                 # Subscription proxy (NEW in v2.2.0)
│   │   ├── cache.go             # In-memory cache with TTL cleanup (240s)
│   │   ├── servers.go           # LoadExtraConfig: headers + servers from file
│   │   ├── proxy.go             # FetchFromXUI, DetectFormat, MergeSubscriptions
│   │   └── service.go           # Service struct, hot reload loop (5 min)
│   ├── xui/                      # 3x-ui panel API client + circuit breaker
│   │   ├── client.go            # Login, CreateClient, GetClientTraffic, retry+jitter, singleflight
│   │   └── breaker.go           # Circuit breaker (5 failures → 30s open)
│   ├── database/                 # GORM SQLite + migrations
│   ├── config/                   # Env var loader + constants
│   ├── logger/                   # Zap + Sentry + lumberjack rotation
│   ├── interfaces/               # Service interfaces for DI
│   ├── utils/                    # UUID, SubID, QR, time helpers
│   ├── ratelimiter/              # Token bucket + PerUserRateLimiter
│   ├── heartbeat/                # Periodic health pings
│   ├── backup/                   # Daily SQLite backup rotation (14 days)
│   └── health/                   # Health checker (legacy, unused)
├── tests/e2e/                    # E2E test suite (66+ scenarios)
├── data/                         # Runtime: tgvpn.db, backups, bot.log, extra_servers.txt
├── doc/                          # PLAN.md, HANDOVER.md, ideas.md
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
- ✅ `/start`, `/help` — start commands with inline keyboards
- ✅ 📋 Subscription view — traffic usage, subscription link, QR code
- ✅ ☕ Donate, ❓ Help — auxiliary menus
- ✅ 🔗 Referral system — share links with in-memory cache + periodic DB sync
- ✅ 🎁 Trial subscriptions via `/i/{code}` landing page with Happ deep-links

**Admin Features:**
- ✅ `/del <id>` — delete subscription by ID
- ✅ `/broadcast <msg>` — send message to all users (detached context)
- ✅ `/send <id|username> <msg>` — send private message
- ✅ `/refstats` — referral statistics
- ✅ 📊 Stats — bot statistics

**Infrastructure:**
- ✅ 3x-ui integration — auto-login, client CRUD, circuit breaker, retry with jitter, singleflight
- ✅ Health endpoints — `/healthz`, `/readyz`
- ✅ Invite/trial landing — `/i/{code}` with IP rate limiting, cookie dedup
- ✅ Per-user rate limiting — per-chatID token bucket (30 tokens, 5/sec refill)
- ✅ **Subscription proxy** — `GET /sub/{subID}` with extra servers + headers, cache, singleflight
- ✅ Daily backups — 14 days retention
- ✅ Sentry error tracking, Zap structured logging
- ✅ Docker: multi-stage build, healthcheck, GHCR images
- ✅ CI: lint, gosec, test, build, push

### Test Coverage

| Module | Coverage | Status |
|--------|----------|--------|
| `internal/ratelimiter` | **97.5%** | ✅ Excellent |
| `internal/bot` | **94.2%** | ✅ Excellent |
| `internal/heartbeat` | **95.8%** | ✅ Excellent |
| `internal/service` | **95.7%** | ✅ Excellent |
| `internal/web` | **90.7%** | ✅ Excellent |
| `internal/xui` | **91.1%** | ✅ Excellent |
| `internal/subproxy` | **82.5%** | ✅ Good |
| `internal/logger` | **87.6%** | ✅ Good |
| `internal/config` | **87.3%** | ✅ Good |
| `internal/database` | **82.9%** | ✅ Good |
| `internal/backup` | **82.3%** | ✅ Good |
| `internal/utils` | **75.0%** | ✅ Good |
| `cmd/bot` | **14.9%** | 🟡 Low (main is integration) |
| **Overall** | **~80%** | ✅ Good |

All tests pass with `-race` detector. golangci-lint: 0 new issues (2 pre-existing).

---

## Last Changes (v2.2.0 — 2026-04-05)

### Subscription Proxy (`GET /sub/{subID}`)
- **New package:** `internal/subproxy/` (cache, servers, proxy, service)
- **Endpoint:** validates subID in DB → fetches from 3x-ui → merges extra config → caches → returns
- **Extra config file format:**
  ```
  # Headers (Key: Value) — override 3x-ui headers
  X-Custom-Header: value
  Profile-Title: My VPN

  # Server links (one per line, after blank line)
  vless://user@server:443
  trojan://pass@server:443
  ```
- **Cache:** 240s TTL, in-memory map + RWMutex, background cleanup every 120s
- **Singleflight:** `singleflight.Group` deduplicates concurrent requests to same subID
- **Hot reload:** Config file reloaded every 5 minutes via background goroutine
- **Fallback:** Stale cache returned if 3x-ui unavailable
- **Format:** Detects base64 (decode → merge → re-encode) or plain text
- **Headers:** Config headers override 3x-ui headers; Content-Length removed after merge
- **Error handling:** 404 (not in DB), 502 (3x-ui down, no cache), 405 (wrong method)
- **Config:** `SUB_EXTRA_SERVERS_ENABLED` (bool, default true), `SUB_EXTRA_SERVERS_FILE` (path)
- **Tests:** 50 new tests (18 handler, 5 service, 7 cache, 7 servers, 10 proxy, 3 format)
- **Commits:** `9c7d2e4` (feat), `acaf15b` (docs)

### Previous Sessions (v2.0.3 — v2.1.0)
- Referral cache system with `/refstats` admin command
- Trial atomic rollback with `RetryWithBackoff`
- Subscription locking with `sync.Map`
- Singleflight for XUI login
- Per-user rate limiter with cleanup goroutine
- Cache LRU fix (lastAccess-based eviction)
- Auto-reset traffic mechanism (expiryTime = now + 30 days)
- Command routing moved to `bot.Handler.HandleUpdate()`
- Atomic trial cleanup with `DELETE ... RETURNING`
- CI PR trigger, docker-compose tag fix, HTTP timeouts

---

## Current Problem / Task

**Status:** ✅ All tests passing, build clean, lint clean. Subscription proxy feature is **complete**.

**Remaining tasks (prioritized):**
1. **Re-enable linters** — errcheck, gosec in `.golangci.yml` (P1)
2. **Multi-arch Docker** — linux/amd64 + linux/arm64 (P2)
3. **Docker image on push to main** — CI build `main`/`dev` tag (P2)
4. **Traffic alerts** — 80%/95% usage notifications (P3)
5. **Multi-admin** — list of admin IDs instead of single (P3)

---

## Critical Nuances

### 3x-ui Integration
- **Session:** 12-hour validity (configurable via `XUI_SESSION_MAX_AGE_MINUTES`, default 720), verified via `/panel/api/server/status`
- **Auto-relogin:** On HTTP 401/redirect, re-authenticates then retries failed request
- **Connection pool cleanup:** Before re-auth to prevent dead connections
- **Circuit breaker:** 5 failures → 30s open state
- **Subscription:** `reset: 30` (days from creation), `expiryTime: now + 30 days`
- **Auto-reset:** Only works when `ExpiryTime > 0`. Traffic resets every 30 days, expiry extends.
- **Client email:** `trial_{subID}` for trial, `{username}` for regular
- **Ping vs Login:** Health checks use `Ping()` (calls `ensureLoggedIn(ctx, false)`) — no forced re-auth
- **Singleflight:** Deduplicates concurrent login attempts
- **DNS error fast-fail:** Non-retryable errors fail immediately

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → xui client (1GB, 3h) → bind via `/start trial_{subID}`
- **Regular:** `create_subscription` callback → xui client (30GB, expiryTime: now + 30 days, reset: 30)
- **Trial cookie:** `rs8kvn_trial_{code}` prevents duplication for 3 hours (HttpOnly)
- **Atomic cleanup:** `DELETE ... RETURNING` for expired trials
- **Share referral:** `pendingInvites[chatID]` cached for 60 minutes

### Subscription Proxy
- **Endpoint:** `GET /sub/{subID}` — subID = SubscriptionID field from DB
- **Extra config:** Headers section → blank line → server links. Headers override 3x-ui.
- **Cache:** 240s TTL hardcoded (`config.SubProxyCacheTTL`), not configurable via env
- **Reload:** Every 5 minutes, graceful — keeps old config if file read fails
- **Singleflight:** First request fetches, others wait and get same result
- **Content-Length:** Removed after merge (body size changes, Go uses chunked encoding)
- **No rate limiting** on `/sub/` endpoint — 240s cache TTL mitigates abuse

### Database
- **Soft deletes:** `deleted_at` column (GORM)
- **Trial subscriptions:** `telegram_id = 0` (not NULL) until activated
- **Migrations:** Auto-applied on startup from `internal/database/migrations/`

### Configuration
- **Required:** `XUI_USERNAME`, `XUI_PASSWORD` (NO defaults)
- **Validation:** `XUI_SUB_PATH` — only `a-zA-Z0-9_-`, no `..` or `/`
- **Web server:** Runs on `HEALTH_CHECK_PORT` (default 8880)
- **Bot username:** Auto-populated from `botAPI.Self.UserName`

### Rate Limiting
- **Per-user:** Each `chatID` gets own token bucket (30 tokens, 5/sec refill)
- **Cleanup:** Stale buckets removed every 5 minutes (maxIdle = 10 minutes)
- **Admin rate limit:** `sync.Map` tracking — 6s min interval between `/send` commands

### Security
- **IP spoofing:** X-Forwarded-For trusted only from local/private addresses
- **No secrets in code:** `.env` only
- **Input validation:** Markdown injection prevention, path traversal protection
- **HTTP timeouts:** ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s, IdleTimeout 60s

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

---

**Generated:** 2026-04-05  
**Session:** Subscription Proxy feature complete  
**Version:** v2.2.0  
**Commits:** `9c7d2e4` (feat), `acaf15b` (docs)
