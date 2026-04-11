# Handover Summary — rs8kvn_bot

**Module:** `rs8kvn_bot` (Go 1.24+) · **Version:** v2.1.6 · **Next:** v2.2.0
**Branch:** `dev` (main = production, dev = integration, PR-only merge)

---

## Architecture

```
cmd/bot/main.go                 # Entry, signals, goroutine lifecycle
internal/
  bot/                          # Telegram handlers
    handler.go                  # Handler struct, routing, keyboards, helpers
    callbacks.go                # Callback query routing
    commands.go                 # /start, /help, share/trial bind
    subscription.go             # Subscription CRUD, QR code
    menu.go                     # back_to_start, donate, help
    admin.go                    # /del, /broadcast, /send, /refstats
    message.go                  # Per-user rate-limited sending
    referral_cache.go           # Read-through cache (DB = source of truth)
    cache.go                    # LRU subscription cache (RLock reads)
  web/                          # HTTP server
    web.go                      # /healthz, /readyz, /i/{code}, /sub/{subID}
    middleware.go                # Bearer auth (timing-safe)
    api.go                      # /api/v1/subscriptions
  subproxy/                     # Subscription proxy
    cache.go                    # TTL cache (240s) + InvalidateCache()
    servers.go                  # Extra servers/headers from file
    proxy.go                    # FetchFromXUI, DetectFormat, Merge
    service.go                  # Hot reload loop (5 min)
  service/                      # DB+XUI orchestration
    subscription.go             # Create, Delete, GetWithTraffic, CreateTrial
  xui/                          # 3x-ui API client
    client.go                   # Login, CRUD, retry+jitter, singleflight
    breaker.go                  # Circuit breaker (5 fail → 30s open)
  database/                     # GORM SQLite + golang-migrate
  config/                       # Env loader + constants
  logger/                       # Zap + Sentry + lumberjack
  interfaces/                   # DI interfaces
  utils/format.go               # DaysUntilReset, FormatDateRu, GenerateProgressBar
  ratelimiter/                  # Token bucket + PerUserRateLimiter
  heartbeat/                    # Periodic health pings
  backup/                       # Daily SQLite backup (14 days)
tests/e2e/                      # 66+ E2E scenarios
data/                           # Runtime: tgvpn.db, backups, bot.log, extra_servers.txt
```

## Stack

Go 1.24+ · telegram-bot-api/v5 · GORM + SQLite (CGO) · golang-migrate · Zap + Sentry · testify · singleflight · Docker + GHCR

---

## Current State

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

**All P0-P2 bugs fixed.** Next steps (priority order):

1. **Merge 5 branches** to dev → create PR for v2.2.0 release
2. **Re-enable linters** — errcheck, gosec (73 issues, mostly in tests)
3. **Multi-arch Docker** — amd64 + arm64
4. **Traffic alerts** — 80%/95% usage notifications
5. **Monetization** — payments, promo codes (see roadmap memory)

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
