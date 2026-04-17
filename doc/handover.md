# Handover Summary — rs8kvn_bot

**Repo:** https://github.com/kereal/rs8kvn_bot
**Module:** `rs8kvn_bot` (Go 1.25+)
**Version:** v2.3.0
**Branch:** `dev` (GitFlow: `main` = production, `dev` = integration)

---

## Architecture

### High-Level Component Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Telegram Bot API                             │
└─────────────────────────────┬───────────────────────────────────────┘
                              │ GetUpdates (long polling)
                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     cmd/bot/main.go (Entry Point)                    │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │ • Config loading                                                │ │
│  │ • Service initialization (DB, XUI, Bot, Web, SubProxy)          │ │
│  │ • Graceful shutdown coordination (signal handling)              │ │
│  │ • Worker pool semaphore (10 concurrent handlers)                │ │
│  └────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────┬───────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
┌───────────────┐    ┌───────────────┐    ┌───────────────┐
│   Bot Layer   │    │  Web Layer    │    │  Background   │
│ internal/bot/ │    │  internal/web/│    │  Jobs         │
│               │    │               │    │               │
│ • Handler     │    │ • /healthz    │    │ • Backup      │
│ • Commands    │    │ • /readyz     │    │   (03:00)     │
│ • Callbacks   │    │ • /i/{code}   │    │ • Trial       │
│ • Cache       │    │ • /sub/{subID}│    │   cleanup     │
│ • RateLimit   │    │ • /api/v1/*   │    │   (hourly)    │
└───────────────┘    └───────────────┘    └───────────────┘
        │                     │                     │
        └─────────────────────┼─────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
┌───────────────┐    ┌───────────────┐    ┌───────────────┐
│   Database    │    │   XUI Panel   │    │ External      │
│  SQLite DB    │    │  3x-ui API    │    │ Services      │
│               │    │               │    │               │
│ • Subscriptions│   │ • AddClient   │    │ • Telegram    │
│ • Invites      │   │ • GetTraffic  │    │ • Sentry      │
│ • TrialReqs    │   │ • Delete      │    │ • Heartbeat   │
│ • Indexes      │   │ • CircuitBrkr │    │   endpoint    │
└───────────────┘    └───────────────┘    └───────────────┘
```

### Data Flow: Subscription Creation

```
User clicks "Get subscription"
        │
        ▼
Telegram Update → main.go event loop (semaphore acquire)
        │
        ▼
Handler.HandleUpdate → HandleCallback ("create_subscription")
        │
        ▼
1. Check inProgressSyncMap (prevent double-click)
2. Check cache (SubscriptionCache.Get)
        │
        ├─ Cache hit → return cached sub
        │
        └─ Cache miss → proceed
        │
        ▼
SubscriptionService.Create(ctx, telegramID)
        │
        ├─ Generate UUID, SubID (14 random bytes → 28 hex)
        ├─ Build subscription URL: {XUI_HOST}/sub/{SubID}?...
        │
        ▼
XUI Client: AddClientWithID(...)
        │
        ├─ Ensure logged in (circuit breaker check)
        ├─ POST /panel/api/inbounds/addClient
        └─ Return client ID (UUID)
        │
        ▼ (on success)
Database: CreateSubscription (transaction)
        │
        ├─ Revoke old active subs for this user (UPDATE status='revoked')
        └─ INSERT new subscription
        │
        ▼
Cache: Set(telegramID, subscription)
        │
        ▼
Webhook: async POST to Proxy Manager (if URL configured)
        │
        ▼
Notify admin: "New subscription: @username"
        │
        ▼
Send message to user: subscription URL + QR code

On DB error (but XUI success):
    → Retry XUI.DeleteClient (rollback, best-effort)
```

### Data Flow: Trial Activation via Landing Page

```
User visits: https://vpn.site/i/ABC123DEF
        │
        ▼
GET /i/{code} Handler
        │
        ├─ Validate invite code (regex + DB lookup)
        ├─ Check cookie "rs8kvn_trial_{code}" (already used?)
        ├─ Rate limit: CountTrialRequestsByIPLastHour() < TRIAL_RATE_LIMIT
        │
        ▼ (passed)
CreateTrialSubscription(ctx, inviteCode)
        │
        ├─ XUI: AddClientWithID
        │   • Email: "trial_{subID}"
        │   • Traffic: 1 GB (hardcoded)
        │   • Expiry: now + TRIAL_DURATION_HOURS
        │
        ▼
Database: CreateTrialSubscription (is_trial=true, telegram_id=0)
        │
        ▼
Set cookie: rs8kvn_trial_{code} = {subID}; HttpOnly; Secure; SameSite=Strict
        │
        ▼
Render trial.html with:
  • HappLink: "happ://add/{subscriptionURL}"
  • SubURL (copyable)
  • TelegramLink: "tg://resolve?domain=bot_username?start=trial_{subID}"
        │
        ▼
User clicks "Добавить в Happ" → opens Happ app with subscription
User clicks "Активировать" → opens Telegram bot, binds trial to account
```

### Subscription Proxy Flow

```
User opens subscription link in client
        │
        ▼
GET /sub/{subID}
        │
        ├─ Validate subID format (regex: ^[a-zA-Z0-9_-]+$, no '/')
        ├─ Check cache (Cache.Get, TTL 240s)
        │
        ├─ Cache hit?
        │   ├─ Yes → validate subscription still active in DB (optional but done)
        │   │         If inactive → fetch from XUI
        │   └─ No  → fetch from XUI
        │
        ▼
Fetch from XUI (singleflight.Do — dedup concurrent requests)
        │
        ├─ GET /panel/api/subscriptions/:subID?...
        └─ Parse format (VLESS/VMESS/Trojan/etc.)
        │
        ▼
Merge:
  1. XUI subscription lines
  2. Extra servers from SUB_EXTRA_SERVERS_FILE (if enabled)
  3. Headers: extra headers override XUI headers
        │
        ▼
Cache.Set(240s)
        │
        ▼
Write response:
  • Content-Type based on format (usually text/plain)
  • Extra headers (X-Custom-*, Profile-Title)
  • No Content-Length (chunked)
  • Body: merged subscription lines
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

## Current State (v2.3.0)

### Working Features

**User Features:**
- `/start`, `/help` — start commands with inline keyboards
- 📋 Subscription view — traffic usage, subscription link, QR code
- ☕ Donate, ❓ Help — auxiliary menus
- 🔗 Referral system — share links with in-memory cache + periodic DB sync
- 🎁 Trial subscriptions via `/i/{code}` landing page with Happ deep-links
- 📊 Plan display (basic/premium/vip) per user (admin-settable via `/plan`)

**Admin Features:**
- `/del <id>` — delete subscription by ID
- `/broadcast <msg>` — send message to all users (respects shutdown context)
- `/send <id|username> <msg>` — private message (30s cooldown per admin)
- `/refstats` — referral statistics (top 10 from cache)
- `/plan` — set subscription plan for user
- 📊 Stats — bot statistics

**Infrastructure:**
- 3x-ui integration — auto-login, client CRUD, circuit breaker (5-fail/30s), retry with jitter, singleflight dedup
- Health endpoints — `/healthz` (503 when Down), `/readyz` (503 during init)
- Invite/trial landing — `/i/{code}` with IP rate limit (3/hour), cookie dedup (3h)
- Per-user rate limiting — chatID token bucket (30 tokens, 5/sec refill, 10-min idle cleanup)
- Subscription proxy — `GET /sub/{subID}` with extra servers + headers, 240s TTL cache, singleflight
- Daily backups — WAL checkpoint, atomic copy, 14-day rotation
- Sentry error tracking (+ traces), Zap structured JSON logging with rotation
- Docker: multi-stage build (UPX compression), non-root user, healthcheck, GHCR images
- CI/CD: GitHub Actions — golangci-lint, gosec, tests (race), Docker build/push

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
| `cmd/bot` | **5.4%** | 🟡 (integration tests cover indirectly) |
| **Overall** | **~85%** | ✅ |

All tests pass with `-race` detector. Fuzzing enabled for critical functions.

---

## Critical Nuances

### 3x-ui Integration
- **Session:** 12h validity (configurable via `XUI_SESSION_MAX_AGE_MINUTES`, default 720), verified via `/panel/api/server/status`
- **Auto-relogin:** On HTTP 401/redirect, re-authenticates then retries failed request
- **Connection pool cleanup:** Before re-auth to prevent dead connections
- **Circuit breaker:** 5 failures → 30s open state, then half-open (3 attempts) before full close
- **Subscription defaults:** `reset: 30` (days from creation), `expiryTime: now + 30 days`, `flow: "xtls-rprx-vision"`
- **Auto-reset:** Only works when `ExpiryTime > 0`. Traffic resets every 30 days, expiry extends (3x-ui auto-renew logic)
- **Client email:** `trial_{subID}` for trial, `{username}` for regular, `plan_{subID}` for plan-based (future)
- **Ping vs Login:** Health checks use `Ping()` → `ensureLoggedIn(ctx, false)` — no forced re-auth if session valid
- **Singleflight:** Deduplicates concurrent login attempts and subscription fetches
- **DNS error fast-fail:** Non-retryable errors fail immediately (no retry spam)

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → DB trial record (telegram_id=0) → user clicks link in Telegram → `BindTrialSubscription` sets telegram_id, removes is_trial, sets referred_by if from invite
- **Regular:** `create_subscription` callback → XUI client (30GB, expiryTime: now+30d, reset:30) → DB record → cache invalidate → admin notify → webhook
- **Trial cookie:** `rs8kvn_trial_{code}` prevents duplication for 3 hours (HttpOnly, Secure, SameSite=Strict)
- **Atomic cleanup:** `DELETE ... RETURNING` for expired trials (prevent race with bind)
- **Share referral:** `pendingInvites[chatID]` cached 60 min (in-memory, periodic cleanup prevents leak)
- **Plan management:** Admin `/plan <username> <plan>` updates user plan (free/basic/premium/vip)

### Subscription Deletion (v2.2.0+)
- **Order:** DB-first, then XUI-best-effort
- **Rationale:** If DB delete fails → XUI untouched (safe to retry). If XUI fails after DB success → orphaned client (less critical, manual cleanup).
- **Webhook:** Sent on successful DB deletion regardless of XUI outcome.
- **Referral cache:** `DecrementReferralCount` called after successful deletion.

### Subscription Proxy (v2.3.0+)
- **Endpoint:** `GET /sub/{subID}` — subID = SubscriptionID from DB (14 random bytes → 28 hex chars)
- **Extra config:** Headers section → blank line → server links. Headers override 3x-ui.
- **Cache:** 240s TTL hardcoded (`config.SubProxyCacheTTL`)
- **Reload:** Every 5 minutes, graceful — keeps old config if file read fails
- **Singleflight:** First request fetches, others wait and get same result (prevents thundering herd)
- **Content-Length:** Removed after merge (body size changes, Go uses chunked encoding)
- **Rate limiting:** Currently none — 240s cache TTL mitigates abuse; future: per-IP limit
- **Path traversal protection:** `extra_servers.txt` path validated before opening (no `..`, no system dirs)

### Referral Cache
- **Source of truth:** subscriptions table (`SELECT referred_by, COUNT(*) GROUP BY referred_by`)
- **Cache purpose:** Read-through for real-time display (`/refstats`) without hitting DB
- **Save() is no-op:** DB already reflects correct count after changes (Created/Deleted/Bound)
- **Sync():** Calls `Load()` hourly to refresh from DB
- **Admin rate limit:** 30s cooldown between `/send` commands per admin (sync.Map)

### Database
- **Engine:** SQLite (mattn/go-sqlite3, WAL mode)
- **Soft deletes:** `deleted_at` column (GORM)
- **Trial subscriptions:** `telegram_id = 0` (not NULL) until activated via `/start trial_{subID}`
- **Migrations:** Auto-applied on startup from `internal/database/migrations/` (embedded via `go:embed`)
- **Legacy support:** Auto-migration for pre-embedded databases (adds `subscription_id` column, drops `x_ui_host`)
- **trial_requests cleanup:** 1-hour cutoff (matching rate-limit window) + 1s buffer to avoid boundary race
- **Connection pool:** `MaxOpenConns=1` (SQLite single-writer), `MaxIdle=1`, `ConnMaxLifetime=5m`

### Configuration
- **Required:** `TELEGRAM_BOT_TOKEN`, `XUI_USERNAME`, `XUI_PASSWORD` (NO defaults)
- **Validated:** 
  - `XUI_SUB_PATH` — only `a-zA-Z0-9_-`, no `..` or `/`
  - `XUI_HOST` — must be valid URL, **HTTPS enforced** (except localhost)
  - `SUB_EXTRA_SERVERS_FILE` — path traversal check
- **Web server:** Runs on `HEALTH_CHECK_PORT` (default 8880), bound to all interfaces
- **Init failure:** Fatal exit for DB, XUI, and Bot API init errors (cannot operate without them)

### Rate Limiting
- **Per-user (Telegram):** Each `chatID` gets own token bucket (max 30 tokens, refill 5/sec)
  - Applied to all command/callback handlers
  - `MaxConcurrentHandlers=10` semaphore prevents goroutine explosion
- **Trial endpoint (IP-based):** 3 requests/hour per IP (DB-tracked, auto-cleanup)
- **Admin `/send`:** 30-second cooldown per admin (in-memory sync.Map)
- **Broadcast:** 50ms delay between messages (~20 msg/sec, respects Telegram limits)

### Security
- **Input validation:** Path traversal checks (XUI_SUB_PATH, extra_servers.txt), regex invite codes
- **IP spoofing:** X-Forwarded-For trusted only from loopback (127.0.0.1, ::1). Private IPs NOT trusted.
- **API auth:** Timing-safe token comparison via `crypto/subtle.ConstantTimeCompare`
- **No secrets in code:** `.env` only, `.env.example` has placeholders
- **HTTP timeouts:** ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s, IdleTimeout 60s
- **Port binding:** Verified before goroutine launch — `net.Listen()` then `Serve()` in separate goroutine
- **Non-root Docker:** UID 1000, `no-new-privileges:true`
- **Circuit breaker:** XUI client protected — 5 failures → 30s open, then half-open (3 attempts)

### Health Checks
- **`/healthz`:** Composite: DB ping + XUI status check → 200 (ok|degraded) or 503 (down)
- **`/readyz`:** Simple flag — set to true only after all services initialized → 200 or 503

### Docker
- **Base:** Alpine 3.21 (runtime), golang:1.25-alpine (builder)
- **Binary:** UPX compressed (-9) — ~30–40% smaller
- **Migrations:** Embedded via `COPY internal/database/migrations`
- **Data volume:** `./data:/app/data` (persistent)
- **Health check:** `wget --spider http://localhost:8880/healthz`
- **Resource limits:** 0.5 CPU, 128MB memory (2× GOMEMLIMIT for GC headroom)
- **Stop grace period:** 30s, SIGTERM

---

## Quick Commands

```bash
# Run all tests with race detector
go test -race -count=1 ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Build binary
go build -ldflags="-s -w -X main.version=v2.3.0 -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) -X main.buildTime=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" -o rs8kvn_bot ./cmd/bot

# Run linters
golangci-lint run ./...
gosec ./...

# Run locally
go run ./cmd/bot
```
