# Handover Summary — rs8kvn_bot

**Repo:** https://github.com/kereal/rs8kvn_bot  
**Module:** `rs8kvn_bot` (Go 1.24+)  
**Version:** v2.2.0-dev
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
│   ├── webhook/                  # Proxy Manager webhook sender with retry
├── tests/e2e/                    # E2E test suite (66+ scenarios)
├── data/                         # Runtime: tgvpn.db, backups, bot.log, extra_servers.txt
├── doc/                          # handover.md, ideas.md, feature specs
├── Dockerfile, docker-compose.yml, .env.example
└── .github/                      # CI: lint, gosec, test, Docker → GHCR
```

## Stack

| Component | Library/DB | Version |
|-----------|------------|---------|
| Go | `go` | 1.25+ |
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

## Last Changes (v2.2.0 — 2026-04-12)

### Bugfixes (Merged from pending PRs)

1. **ExpiryTime not stored in DB** (P0) — `service.Create()` was sending the real `expiryTime` to 3x-ui but storing `time.Time{}` (zero) in the database, causing admin notifications to show "—" instead of the actual reset date. Fixed by storing the actual `expiryTime` value.

2. **`/sub/{subID}` serves revoked subscriptions** (P1) — The subscription proxy endpoint returned content for revoked/expired subscriptions without checking status. Added `IsActive()` check after DB lookup. Cache hits also verified for status with `InvalidateCache(key)`.

3. **Soft delete inconsistency** (P2) — `DeleteSubscriptionByID` used `Unscoped().Delete()` (hard delete) while `DeleteSubscription` used GORM soft delete. Removed `Unscoped()` — both methods now use soft delete consistently.

4. **Cache uses exclusive Lock for reads** (P3) — `SubscriptionCache.Get()` used `c.mu.Lock()` (exclusive write lock) serializing all concurrent reads. Changed to RLock → Lock upgrade pattern. Fast path: `RLock` for concurrent reads. Slow path: upgrade to `Lock` only for mutations (eviction, LRU MoveToBack).

### Project Cleanup

5. **Removed AI agent directories from git** — Removed 33 tracked files from `.agents/`, `.kilo/`, `.octocode/`, `.qwen/`, `.serena/`. Added these directories to `.gitignore`.

6. **Updated .gitignore** — Added `coverage.out`, `coverage.html`, `tmp/`, `temp/`, `.env.*`, `.DS_Store`, `Thumbs.db`, and AI agent tool directories.

7. **Fixed Dockerfile** — Removed outdated "Go 1.25 is a future/unreleased version" comment. Updated Alpine to 3.21.

8. **Fixed CI Go version** — Aligned `update-stats.yml` Go version from 1.24 to 1.25 (matching `docker.yml`).

9. **Fixed docker-compose GOGC** — Aligned GOGC value from 50 to 40 (matching Dockerfile).

10. **Version correction** — Fixed version numbers throughout documentation. The last git tag is v2.1.6; the next release is v2.2.0.

---

## Previous Changes (v2.3.2 — 2026-04-11)

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

**Working:** /start /help, subscription view+QR, donate, referral system, trial via /i/{code}, admin /del /broadcast /send /refstats, subscription proxy /sub/{subID}, health endpoints, per-user rate limiting, daily backups, circuit breaker, graceful shutdown.

**Tests:** ~85% coverage, all pass with `-race`. 21 packages.

---

## Last Changes (v2.2.0 — unmerged branches)

### 2026-04-12 (4 bugfixes, not yet merged to dev)

| # | Branch | Commit | Fix |
|---|--------|--------|-----|
| 1 | `fix/expiry-time-db` | `72f5d02` | `service.Create()` stored `ExpiryTime: time.Time{}` → now `expiryTime`. Admin sees real date. |
| 2 | `fix/sub-status-check` | `e538906` | `/sub/{subID}` served revoked subs → added `IsActive()` check + cache invalidation. |
| 3 | `fix/soft-delete-unify` | `27052ab` | `DeleteSubscriptionByID` used `Unscoped()` (hard) → removed, now soft delete like the rest. |
| 4 | `fix/cache-rlock` | `e133eac` | `SubscriptionCache.Get` used exclusive Lock → RLock fast path, Lock only for mutations. |
| — | `docs/update-v2.2.0` | `490c53b` | Updated handover.md, README.md, Serena memories. |

### 2026-04-11 (6 bugfixes, merged to dev)

- `escapeMarkdown` — added `\` as first char in MarkdownV2 escape list
- `HandleBroadcast` — 30s timeout → 5 min
- `GetOrCreateInvite` — INSERT error was silently swallowed
- `pendingInvites` — memory leak → periodic cleanup goroutine
- `handleMySubscription` — 40 lines dedup → single `GetWithTraffic()` call
- `CleanupExpiredTrials` — wrong cutoff for `trial_requests` → separate 1h window

### 2026-04-10 (refactoring + security)

- ReferralCache.Save() → no-op (DB is source of truth)
- Nil pointer on init fail → logger.Fatal (was Warn)
- Delete order → DB-first, XUI-best-effort
- handleBindTrial → cache invalidation added
- formatDateRu → zero-time returns "—"
- Timing-safe token comparison, loopback-only trusted proxies
- Health 503 on Down, port binding check, getClientIP fix
- Shared format helpers → `internal/utils/format.go`
- Service tests 24.8% → 95.2%, ReferralCache 15 tests

---

## Current Problem / Task

**Status:** ✅ All tests passing (race-safe), build clean. v2.2.0 release prep **complete**.

**Completed in v2.2.0:**
1. ✅ **ExpiryTime stored in DB** — `service.Create()` now saves the actual ExpiryTime (was storing zero value)
2. ✅ **`/sub/{subID}` status check** — Revoked/expired subscriptions are no longer served
3. ✅ **Soft delete unified** — `DeleteSubscriptionByID` now uses soft delete consistently
4. ✅ **Cache RLock** — `SubscriptionCache.Get()` uses RLock for concurrent reads

**Remaining tasks (prioritized):**
1. **Multi-arch Docker** — linux/amd64 + linux/arm64 (P2)
2. **Docker image on push to main** — CI build `main`/`dev` tag (P2)
3. **Traffic alerts** — 80%/95% usage notifications (P3)
4. **Multi-admin** — list of admin IDs instead of single (P3)
5. **Re-enable linters** — errcheck, gosec in `.golangci.yml` (P1) — partially done, 73 issues remaining (mostly in tests)

---

## Critical Nuances

### 3x-ui Auto-Reset
- `expiryTime > 0` AND `reset > 0` required — **both** must be set or auto-reset won't work
- `reset = 30` means "every 30 days from creation", NOT "on the 30th"
- Auto-renew: when `expiryTime ≤ now` → traffic resets to 0, expiry += reset×86400000ms, client re-enabled
- Bot sends `expiryTime = now + 30d` and `reset = 30` to XUI — both are required

### Subscription Deletion
- **Unified soft delete** (v2.2.0) — `deleted_at` column, no hard deletes
- **Order:** DB-first, XUI-best-effort
- If DB fails → XUI untouched (safe retry). If XUI fails → log only (orphaned client < orphaned DB record)

### Subscription Proxy
- `/sub/{subID}` checks `IsActive()` after DB lookup (v2.2.0) — revoked/expired → 404
- Cache hits also verified; stale entries invalidated via `InvalidateCache(key)`
- Cache TTL: 240s. Extra servers/headers from `data/extra_servers.txt`
- Singleflight deduplicates concurrent requests

### ExpiryTime in DB
- **v2.2.0:** `service.Create()` now stores real `expiryTime` (was `time.Time{}`)
- `GetWithTraffic` has backward-compat fallback: `if sub.ExpiryTime.IsZero() { resetTime = sub.CreatedAt + reset }`
- Old rows with zero ExpiryTime still work via fallback

### Referral Cache
- **DB is source of truth** — `SELECT referred_by, COUNT(*) GROUP BY referred_by`
- Cache = read-through optimization. `Save()` = no-op. `Sync()` = `Load()` from DB hourly
- Increment/Decrement update in-memory only for real-time display

### Trial Flow
- `/i/{code}` → IP rate limit (3/hour) → XUI client (1GB, 3h) → bind via `/start trial_{subID}`
- `trial_requests` cleanup uses 1h cutoff (matching rate-limit window), separate from subscription cleanup
- Cookie `rs8kvn_trial_{code}` prevents duplication (3h, HttpOnly)

### Security
- X-Forwarded-For trusted from **loopback only** (127.0.0.1, ::1) — not private IPs
- API auth: `crypto/subtle.ConstantTimeCompare`
- Init failure: `logger.Fatal` (no nil-pointer continuation)
- MarkdownV2: escape `\` **first** to prevent double-escaping

### Config
- Required: `XUI_USERNAME`, `XUI_PASSWORD` (no defaults)
- Web port: `HEALTH_CHECK_PORT` (default 8880)
- Session: 12h validity, auto-relogin on 401

---

## Quick Commands

```bash
go test -race -count=1 ./...          # all tests
go test -coverprofile=c.out ./...     # coverage
golangci-lint run ./...               # linters
go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot
```

---

**Generated:** 2026-04-12  
**Session:** v2.2.0 release prep (merge 4 pending PRs, cleanup .gitignore, remove agent dirs, fix Dockerfile/CI, version correction)  
**Version:** v2.2.0-dev
