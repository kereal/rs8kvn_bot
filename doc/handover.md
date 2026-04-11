# Handover Summary — TGVPN Go Bot

**Repo:** https://github.com/kereal/rs8kvn_bot  
**Module:** `rs8kvn_bot` (Go 1.24+)  
**Version:** v2.3.3  
**Last Updated:** 2026-04-12  
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
│   └── health/                   # Health checker (legacy, unused)
├── tests/e2e/                    # E2E test suite (66+ scenarios)
├── data/                         # Runtime: tgvpn.db, backups, bot.log, extra_servers.txt
├── doc/                          # handover.md, ideas.md, feature specs
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
- ✅ `/broadcast <msg>` — send message to all users (respects shutdown context)
- ✅ `/send <id|username> <msg>` — send private message
- ✅ `/refstats` — referral statistics
- ✅ 📊 Stats — bot statistics

**Infrastructure:**
- ✅ 3x-ui integration — auto-login, client CRUD, circuit breaker, retry with jitter, singleflight
- ✅ Health endpoints — `/healthz` (503 when Down), `/readyz`
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
| `internal/flag` | **97.7%** | ✅ Excellent |
| `internal/ratelimiter` | **97.4%** | ✅ Excellent |
| `internal/heartbeat` | **96.2%** | ✅ Excellent |
| `internal/service` | **95.2%** | ✅ Excellent |
| `internal/config` | **91.8%** | ✅ Excellent |
| `internal/xui` | **90.9%** | ✅ Excellent |
| `internal/web` | **90.3%** | ✅ Excellent |
| `internal/bot` | **92.6%** | ✅ Excellent |
| `internal/utils` | **90.0%** | ✅ Excellent |
| `internal/logger` | **88.9%** | ✅ Good |
| `internal/backup` | **83.2%** | ✅ Good |
| `internal/subproxy` | **82.5%** | ✅ Good |
| `internal/scheduler` | **81.2%** | ✅ Good |
| `internal/database` | **77.8%** | 🟡 Moderate |
| `cmd/bot` | **5.4%** | 🟡 Low (main is integration) |
| `internal/testutil` | **0.0%** | 🔴 No tests |
| **Overall** | **~85%** | ✅ Good |

All tests pass with `-race` detector. golangci-lint: 0 new issues (pre-existing: nilerr, gocritic).

---

## Last Changes (v2.3.3 — 2026-04-12)

### Bugfixes

1. **Fix #1+#2 (P0+P1): ExpiryTime not stored in DB** (`fix/expiry-time-db`, `72f5d02`) — `service.Create()` sent `ExpiryTime: time.Time{}` to DB instead of the actual `expiryTime`. This caused admin notifications to show "—" instead of the real reset date, and inaccurate subscription display. Changed to `ExpiryTime: expiryTime`. Preserved `GetWithTraffic` fallback for backward compatibility with existing rows.

2. **Fix #3 (P1): `/sub/{subID}` serves revoked subscriptions** (`fix/sub-status-check`, `e538906`) — The subscription proxy handler returned content for revoked/expired subscriptions without checking status. Added `IsActive()` check in `handleSubscription` after DB lookup. Added cache hit status verification with cache invalidation. Added `InvalidateCache(key)` method to subproxy Service. 4 new tests covering revoked, expired, revoked+cache, and expired-by-time+cache scenarios.

3. **Fix #4 (P2): Hard vs soft delete inconsistency** (`fix/soft-delete-unify`, `27052ab`) — `DeleteSubscriptionByID` used `Unscoped()` (hard delete) while `DeleteSubscriptionByTelegramID` used soft delete. Removed `Unscoped()` so both methods now use GORM soft delete consistently. Updated test to verify soft delete behavior (`deleted_at` is set, record still queryable via `Unscoped`).

4. **Fix #6 (P3): `SubscriptionCache.Get` uses exclusive Lock** (`fix/cache-rlock`, `e133eac`) — `Get()` used `c.mu.Lock()` (exclusive/writer lock) for all operations, blocking concurrent reads. Changed to RLock → Lock upgrade pattern: fast path uses `c.mu.RLock()` for concurrent reads; slow path upgrades to `c.mu.Lock()` only for mutations (eviction, LRU promotion). Re-check after lock upgrade ensures correctness.

---

## Last Changes (v2.3.2 — 2026-04-11)

### Bugfixes & Refactoring

1. **escapeMarkdown missing backslash** — `\` was not in the MarkdownV2 escape list, causing broken rendering when user input contained backslashes (e.g. `C:\Users`). Backslash must be escaped **first** to prevent double-escaping of subsequent chars.

2. **HandleBroadcast 30s timeout** — `HandleBroadcast` used `withTimeout(ctx)` (30s), but broadcast iterates through users with 50ms delay. 1000 users = ~50s, causing early termination. Replaced with `context.WithTimeout(ctx, 5*time.Minute)`.

3. **GetOrCreateInvite ignored INSERT error** — `s.db.Exec("INSERT OR IGNORE ...")` silently swallowed errors (including DB connection failures). Now checks `err` before the SELECT query.

4. **pendingInvites memory leak** — `pendingInvites` map entries were only removed when a user created a subscription. Expired entries were never purged, causing unbounded memory growth. Added `cleanupPendingInvites()` method and periodic cleanup goroutine via `startPendingInvitesCleanup()`.

5. **handleMySubscription duplicated GetWithTraffic logic** — `handleMySubscription` manually computed traffic percentage, progress bar, reset info — all of which already existed in `service.GetWithTraffic()`. Replaced ~40 lines of duplicated code with a single `GetWithTraffic()` call. This ensures a single source of truth for subscription display logic.

6. **CleanupExpiredTrials used wrong cutoff for trial_requests** — `trial_requests` (rate-limit records, 1-hour window) were cleaned up with the same cutoff as trial subscriptions (3+ hours). This caused stale rate-limit entries to accumulate. Now uses a separate `rateLimitCutoff = now - 1h` matching the actual rate-limit window.

---

## Last Changes (v2.3.1 — 2026-04-11)

### Test Optimization & Refactoring

1. **Test optimization** — Reduced iterations in stress/entropy tests:
   - uuid_test.go: stress tests 100k → 10k, uniqueness tests 10k → 1k
   - Concurrency tests: 100x100 → 50x50 goroutines
   - Removed duplicates: TestGenerateInviteCode_Format, TestGenerateUUID_Entropy (covered by Properties_*)
   - Test time improved: ~41s → ~38s

2. **Code refactoring** — if-else → switch statements:
   - internal/bot/subscription.go:156 - reset info string building
   - internal/service/subscription.go:258 - reset info in GetWithTraffic

3. **Lint fixes** — Fixed nilerr warning:
   - internal/service/subscription.go:225 - added `_ = err` to suppress unused error

---

## Last Changes (v2.3.0 — 2026-04-10)

### Bugfixes (Critical/High)

1. **ReferralCache.Save() noop** — Removed broken dirty tracking. Referral counts are derived from subscriptions table (`SELECT referred_by, COUNT(*) GROUP BY referred_by`), so there is nothing to persist. `Save()` is now a no-op, `Sync()` simply calls `Load()` to refresh from DB. `Increment/Decrement` update the in-memory cache for real-time display until the next sync.

2. **Nil pointer dereference on init failure** — `logger.Warn` → `logger.Fatal` for DB, XUI client, and Bot API initialization errors. Previously the app continued with nil pointers → guaranteed panic on first use.

3. **Non-atomic deletion order** — Changed deletion order in `SubscriptionService.Delete()` and `DeleteByID()`: DB-first, then XUI-best-effort. If DB delete fails → XUI is untouched (safe to retry). If XUI delete fails after DB success → logged but not returned as error (orphaned XUI client is less critical than orphaned DB record).

4. **context.WithoutCancel for broadcast** — Removed `context.WithoutCancel(ctx)` from broadcast dispatch. `HandleBroadcast` already handles `ctx.Done()` in its loop, so the detached context only prevented graceful shutdown during broadcast.

5. **Missing cache invalidation on trial bind** — Added `h.invalidateCache(chatID)` after successful trial subscription binding in `handleBindTrial`. Previously, stale "no subscription" cache entry caused incorrect UI state.

6. **formatDateRu zero-time** — Added `if t.IsZero() { return "—" }` check. Previously showed "1 января 0001" for zero/nil time values.

7. **Dead code in verifySession()** — Removed unreachable `if err != nil` block after successful `io.ReadAll` in `internal/xui/client.go`.

8. **sync.Map unsafe type assertion** — Changed `lastSend.(time.Time)` to two-value form `lastSend, ok := lastSend.(time.Time)` in `ReferralCache.CheckAdminSendRateLimit`.

### Security Hardening

- **Timing-safe token comparison** — `BearerAuthMiddleware` now uses `crypto/subtle.ConstantTimeCompare` instead of `!=` to prevent timing side-channel attacks on API tokens.
- **Loopback-only trusted proxies** — `isLocalAddress()` now only trusts loopback addresses (`127.0.0.1`, `::1`), not all private IPs. In cloud environments, other VMs on the same VPC could spoof `X-Forwarded-For` to bypass IP rate limiting.
- **Web server port binding** — `Start()` now uses `net.Listen()` before `Serve()` to catch "port already in use" errors immediately instead of silently failing in a goroutine.
- **getClientIP malformed fallback** — When `SplitHostPort` fails on `RemoteAddr`, tries once more to strip the port before falling back to raw address (which includes port and bypasses rate limiting).
- **Health endpoint 503** — `writeJSON()` now returns HTTP 503 when health status is Down, allowing Kubernetes liveness probes to detect and restart unhealthy pods.

### Code Deduplication

- **Extracted shared format helpers** — `DaysUntilReset`, `FormatDateRu`, `GenerateProgressBar` moved from `internal/bot/subscription.go` and `internal/service/subscription.go` to `internal/utils/format.go`. Both packages now call `utils.DaysUntilReset()` etc. Associated tests moved to `internal/utils/format_test.go`.

### Test Coverage Improvements

- **Service layer** — Coverage improved from 24.8% → 95.2% (+30 new tests covering Create, Delete, DeleteByID, GetWithTraffic, CreateTrial, CalcTrialTraffic scenarios including DB-first deletion order, XUI best-effort, rollback failures).
- **ReferralCache** — 15 new tests covering Get, GetAll, Increment, Decrement, Save noop, Sync refresh, concurrent safety, admin rate limiting.

### Previous Sessions (v2.2.0 — 2026-04-05)
- Subscription proxy feature (`GET /sub/{subID}`)
- 50 new tests for subproxy

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

**Status:** ✅ All tests passing (race-safe), build clean. v2.3.3 bugfixes **complete**.

**Remaining tasks (prioritized):**
1. **Re-enable linters** — errcheck, gosec in `.golangci.yml` (P1) — partially done, 73 issues remaining (mostly in tests)
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
- **Share referral:** `pendingInvites[chatID]` cached for 60 minutes (in-memory only, lost on restart — acceptable trade-off at current scale). Periodic cleanup via `startPendingInvitesCleanup()` prevents unbounded memory growth.

### Subscription Deletion (v2.3.0+, unified soft delete in v2.3.3)
- **Order:** DB-first, then XUI-best-effort
- **Soft delete:** Both `DeleteSubscriptionByID` and `DeleteSubscriptionByTelegramID` use GORM soft delete (`deleted_at` column). No hard deletes — records remain queryable via `Unscoped()`.
- **Rationale:** If DB delete fails, XUI is untouched and the operation can be safely retried. If XUI delete fails after DB success, the orphaned XUI client is less critical than an orphaned DB record and can be cleaned up manually.
- **Webhook:** Sent on successful DB deletion regardless of XUI outcome.
- **Referral cache:** `DecrementReferralCount` called after successful deletion.

### Subscription Proxy
- **Endpoint:** `GET /sub/{subID}` — subID = SubscriptionID field from DB
- **Status check (v2.3.3):** `IsActive()` called after DB lookup — revoked/expired subscriptions return 404. Cache hits are also verified for status; stale entries are invalidated via `InvalidateCache(key)`.
- **Extra config:** Headers section → blank line → server links. Headers override 3x-ui.
- **Cache:** 240s TTL hardcoded (`config.SubProxyCacheTTL`), not configurable via env. Uses RLock for concurrent reads (v2.3.3), exclusive Lock only for mutations (eviction, LRU promotion).
- **Reload:** Every 5 minutes, graceful — keeps old config if file read fails
- **Singleflight:** First request fetches, others wait and get same result
- **Content-Length:** Removed after merge (body size changes, Go uses chunked encoding)
- **No rate limiting** on `/sub/` endpoint — 240s cache TTL mitigates abuse

### Referral Cache
- **Source of truth:** The subscriptions table (`SELECT referred_by, COUNT(*) GROUP BY referred_by`)
- **Cache purpose:** Read-through optimization for real-time display without hitting DB
- **Increment/Decrement:** Update in-memory cache immediately when subscriptions are created/deleted
- **Save() is no-op:** Nothing to persist — DB already reflects correct count after subscription changes
- **Sync():** Calls `Load()` hourly to refresh from DB, overwriting any stale in-memory values
- **Admin rate limit:** 30s cooldown between `/send` commands per admin (tracked in `sync.Map`)

### Database
- **Soft deletes:** `deleted_at` column (GORM) — all delete methods use soft delete consistently (v2.3.3)
- **ExpiryTime:** Stored in DB on `Create()` (v2.3.3) — admin notifications show actual reset date instead of "—"
- **Trial subscriptions:** `telegram_id = 0` (not NULL) until activated
- **Migrations:** Auto-applied on startup from `internal/database/migrations/`
- **trial_requests cleanup:** Uses 1-hour cutoff (matching `CountTrialRequestsByIPLastHour` window), separate from trial subscription cutoff (which may be 3+ hours)

### Configuration
- **Required:** `XUI_USERNAME`, `XUI_PASSWORD` (NO defaults)
- **Validation:** `XUI_SUB_PATH` — only `a-zA-Z0-9_-`, no `..` or `/`
- **Web server:** Runs on `HEALTH_CHECK_PORT` (default 8880)
- **Bot username:** Auto-populated from `botAPI.Self.UserName`
- **Init failure:** Fatal exit for DB, XUI, and Bot API init errors (no nil pointer continuation)

### Rate Limiting
- **Per-user:** Each `chatID` gets own token bucket (30 tokens, 5/sec refill)
- **Cleanup:** Stale buckets removed every 5 minutes (maxIdle = 10 minutes)
- **Admin rate limit:** `sync.Map` tracking — 30s min interval between `/send` commands

### Security
- **IP spoofing:** X-Forwarded-For trusted only from **loopback** addresses (127.0.0.1, ::1). Private IPs (10.x, 172.16.x, 192.168.x) are NOT trusted — in cloud environments other VMs on the same VPC could spoof headers.
- **API auth:** Timing-safe token comparison via `crypto/subtle.ConstantTimeCompare`
- **No secrets in code:** `.env` only
- **Input validation:** Markdown injection prevention, path traversal protection
- **HTTP timeouts:** ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s, IdleTimeout 60s
- **Port binding:** Verified before goroutine launch — `net.Listen()` then `Serve()`

### Health Checks
- **`/healthz`:** Returns 200 (OK/Degraded) or 503 (Down). Kubernetes can use this for liveness probes.
- **`/readyz`:** Returns 200 when all components initialized and ready flag is set.
- **Components:** `database` (Ping), `xui` (Ping → Degraded on failure)

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

**Generated:** 2026-04-12  
**Session:** v2.3.3 bugfixes (ExpiryTime DB storage, subscription status check, soft delete unification, cache RLock)  
**Version:** v2.3.3  
