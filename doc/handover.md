# Handover Summary — rs8kvn_bot

**Repo:** https://github.com/kereal/rs8kvn_bot
**Module:** `rs8kvn_bot` (Go 1.25+)
**Version:** v2.3.4
**Branch:** `dev` (GitFlow: `main` = production, `dev` = integration, feature branches from dev or `plans_and_pricing`)

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
│  │ • Service initialization (DB, XUI, Bot, Web, Subserver)          │ │
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
│ • RateLimit   │    │ • /metrics    │    │   (hourly)    │
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
│ • Indexes      │   │ • RetryWithBk │    │   endpoint    │
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
        ├─ Build subscription URL: GLOBAL_SUB_URL + SubID
        │
        ▼
ensureSubscriptionNodes(ctx, sub)
        │
        ├─ Load active nodes for plan (GetNodesByPlanID)
        ├─ Create pending_add records in subscription_nodes
        ├─ Best-effort SyncSubscription (async, errors don't rollback)
        │
        ▼
Database: CreateSubscription (transaction)
        │
        ├─ Revoke old active subs for this user (UPDATE status='revoked')
        └─ INSERT new subscription
        │
        ▼
Cache: Set(telegramID, subscription)
        │
        ▼
Notify admin: "New subscription: @username"
        │
        ▼
Send message to user: subscription URL + QR code

Background sync (eventual consistency):
    SyncSubscription → vpn.Client.CreateSubscription per node
    pending_add → active on success, retry on failure

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
       Database: CreateTrialSubscription (отрицательный telegram_id до активации)
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
        ├─ Validate subID format (regex: ^[a-zA-Z0-9_-]+$)
        ├─ Check cache (TTL 240s)
        │
        ├─ Cache hit? → verify subscription active in DB → serve cached
        └─ Cache miss → proceed
        │
        ▼
GetWithPlanAndNodes(subID) — load subscription + plan + active nodes
        │
        ▼
For each active node:
  ├─ Build sourceURL:
  │   • 3x-ui/proxman: subscription_url + "/" + subID
  │   • fetch: subscription_url as-is
  ├─ FetchFromNode(ctx, sourceURL) — HTTP GET
  ├─ DetectFormat (JSON / Clash / Base64 / Plain)
  └─ Aggregate subscription-userinfo headers
        │
        ▼
Merge all sources:
  • JSON configs → share links if mixed mode
  • Base64/Plain → decode and join
  • Aggregate upload/download/expire across nodes
        │
        ▼
Cache.Set(240s) → return body with Content-Type + Subscription-Userinfo
```

## Stack

| Component | Library/DB | Version |
|-----------|------------|---------|
| Go | `go` | 1.25+ |
| Telegram API | `go-telegram-bot-api/telegram-bot-api/v5` | v5.5.1 |
| ORM | `gorm.io/gorm` + `gorm.io/driver/sqlite` | v1.31.2 |
| DB engine | SQLite (mattn/go-sqlite3, CGO) | `./data/rs8kvn.db` |
| Migrations | `golang-migrate/migrate/v4` | v4.19.1 |
| QR Code | `piglig/go-qr` | v1.1.0 |
| Logging | `go.uber.org/zap` + `lumberjack.v2` | v2.2.1 |
| Error tracking | `getsentry/sentry-go` | v0.48.0 |
| Metrics | `prometheus/client_golang` | v1.23.2 |
| Concurrency | `golang.org/x/sync/singleflight` | — |
| Testing | `stretchr/testify` | v1.11.1 |
| CI/CD | GitHub Actions → golangci-lint, gosec, test, Docker → GHCR | — |

## Current State (v2.3.4)

### Working Features

**User Features:**
- `/start`, `/help` — start commands with inline keyboards
- 📋 Subscription view — traffic usage, subscription link, QR code
- ☕ Donate, ❓ Help — auxiliary menus
- 🔗 Referral system — share links with in-memory cache + periodic DB sync
- 🎁 Trial subscriptions via `/i/{code}` landing page with Happ deep-links


**Admin Features:**
- `/del <id>` — delete subscription by ID
- `/broadcast <msg>` — draft → MarkdownV2 preview (special chars auto-escaped, `*bold*`/`_italic_`/`` `code` ``/`[text](url)` preserved) → inline confirm → batched send to all subscribers (100/batch, concurrency 10); final report splits delivered / blocked-the-bot / other errors
- `/send <id|username> <msg>` — private message (30s cooldown per admin)
- `/refstats` — referral statistics (top 10 from cache)
- 📊 Stats — bot statistics

| **Infrastructure** |
| - 3x-ui integration — Bearer token auth (no session/login/CSRF), client CRUD, RetryWithBackoff (3 retries, jitter), flow detection
| - Multi-node subscription synchronization — `subscription_nodes` table with 4-state sync machine (active/pending_add/pending_remove/pending_update), per-subscription locking, retry with exponential backoff |
| - Health endpoints — `/healthz` (503 when Down), `/readyz` (503 during init)
| - Invite/trial landing — `/i/{code}` with IP rate limit (3/hour), cookie dedup (3h) |
| - Per-user rate limiting — chatID token bucket (30 tokens, 5/sec refill, 10-min idle cleanup) |
| - Subscription proxy — `GET /sub/{subID}` with extra servers + headers, 240s TTL cache, singleflight |
| - Daily backups — WAL checkpoint, atomic copy, 14-day rotation |
| - Sentry error tracking (+ traces), Zap structured JSON logging with rotation
| - Order/Product tracking — payment lifecycle (pending/paid/expired/canceled) with 30-min payment window
| - Docker: multi-stage build (UPX compression), non-root user, healthcheck, GHCR images
| - CI/CD: GitHub Actions — golangci-lint, gosec, tests (race), Docker build/push
| - Prometheus metrics — `/metrics` endpoint with HTTP, bot, XUI, DB, cache, circuit breaker, subscription metrics |
| - VPN client abstraction — `internal/vpn/` package with `Client` interface, `NewClient` factory routing by node type (3x-ui, proxman, fetch) |
| - Plans & pricing — `plans`, `products`, `orders` tables for subscription plan management and payment lifecycle |
| - Orphan reconciliation — `ReconcileOrphanedClients` runs every 6h to clean up orphaned XUI clients |
| - Subscription expire worker — background worker handling subscription expiration |

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
| `internal/subserver` | **82.5%** | ✅ |
| `internal/scheduler` | **81.2%** | ✅ |
| `internal/database` | **77.8%** | 🟡 |
| `cmd/bot` | **5.4%** | 🟡 (integration tests cover indirectly) |
| **Overall** | **~85%** | ✅ |

All tests pass with `-race` detector. Fuzzing enabled for critical functions.

---

## Critical Nuances

### 3x-ui Integration
- **API Token auth:** Bearer token via `Authorization` header (no session/login/CSRF/cookiejar)
- **Node configuration:** Managed entirely via the `nodes` DB table (host, API token, inbound IDs, type, subscription URL). Multi-node support via `nodes` table.
- **VPN client abstraction:** `internal/vpn/` package provides `Client` interface with `NewClient` factory routing by `NodeType` (3x-ui, proxman, fetch). Each node gets its own VPN client instance. Fetch nodes use no-op client — subscription_url fetched directly by subserver.
- **Circuit breaker:** 5 failures → 30s open → half-open (3 attempts) → close. Monitor via `circuit_breaker_state` metric.
- **RetryWithBackoff:** 3 retries with exponential backoff + jitter. DNS errors fast-fail.
- **Subscription defaults:** `reset: 30` (days from creation), `expiresAt: now + 30 days`
- **Auto-reset:** Only works when `expiresAt` > 0. Traffic resets every 30 days, expiry extends (3x-ui auto-renew logic)
- **Client email:** `trial_{subID}` for trial, `{username}` for regular
- **Flow detection:** When creating/updating clients, fetches inbound config via `GET /panel/api/inbounds/get/{id}` to determine transport type. Flow is set based on transport: `tcp` → `"xtls-rprx-vision"`, `xhttp/h2/ws/grpc` → `""` (empty). Falls back to `"xtls-rprx-vision"` if inbound cannot be fetched.
- **Multi-inbound:** Nodes store `inbound_ids` as a JSON array. Client creation iterates all inbounds for the node.

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → DB trial record (negative telegram_id) → user clicks link in Telegram → `BindTrialSubscription` sets telegram_id (из отрицательного в реальный), sets referred_by if from invite
- **Regular:** `create_subscription` callback → VPN client provisioning (per-node) → `subscription_nodes` records created as `pending_add` → DB record → cache invalidate → admin notify
- **Sync pipeline:** Background `SyncPendingNodes` worker processes `pending_add`/`pending_remove`/`pending_update` states with per-subscription locking and exponential backoff retry
- **Trial cookie:** `rs8kvn_trial_{code}` prevents duplication for 3 hours (HttpOnly, Secure, SameSite=Strict)
- **Atomic cleanup:** `DELETE ... RETURNING` for expired trials (prevent race with bind)
- **Share referral:** `pendingInvites[chatID]` cached 60 min (in-memory, periodic cleanup prevents leak)
- **TelegramID conventions:** Positive = bound users, Negative = unbound trial subscriptions

### Subscription Deletion (v2.2.0+)
- **Order:** Mark as revoked → best-effort deprovision VPN access → physical deletion of DB row
- **Rationale:** Subscription is immediately inaccessible after revoked status is set. VPN deprovisioning is best-effort (background sync retries on failure). Physical deletion happens last. See AGENTS.md for detailed flow description.
- **Referral cache:** `DecrementReferralCount` called after successful deletion.

### Subscription Proxy (v2.3.4+)
- **Endpoint:** `GET /sub/{subID}` — subID = SubscriptionID from DB (14 random bytes → 28 hex chars)
- **Config:** Subserver агрегирует ответы нод как-is; кастомные extra-серверы (extra_servers.txt, hot reload) удалены в v2.3.0.
- **Cache:** 240s TTL hardcoded (`config.SubServerCacheTTL`)
- **Singleflight:** First request fetches, others wait and get same result (prevents thundering herd) — загрузка внешнего конфиг-файла не выполняется (фича удалена)
- **Content-Length:** Removed after merge (body size changes, Go uses chunked encoding)
- **Rate limiting:** Currently none — 240s cache TTL mitigates abuse; future: per-IP limit
- **Path traversal protection:** (исторически) `extra_servers.txt` path валидировался перед открытием — фича удалена в v2.3.0, проверка больше не применяется.

### Referral Cache
- **Source of truth:** subscriptions table (`SELECT referred_by, COUNT(*) GROUP BY referred_by`)
- **Cache purpose:** Read-through for real-time display (`/refstats`) without hitting DB
- **Save() is no-op:** DB already reflects correct count after changes (Created/Deleted/Bound)
- **Sync():** Calls `Load()` hourly to refresh from DB
- **Admin rate limit:** 30s cooldown between `/send` commands per admin (sync.Map)

### Database
- **Engine:** SQLite (mattn/go-sqlite3, WAL mode)
- **Soft deletes:** Отсутствуют — при удалении подписка сначала помечается `status='revoked'`, затем выполняется best-effort депровизионирование VPN-доступа, и только потом происходит физическое удаление строки из БД (см. AGENTS.md: Delete flow).
- **Trial subscriptions:** `telegram_id < 0` (negative value) until activated via `/start trial_{subID}`
- **Migrations:** Auto-applied on startup from `internal/database/migrations/` (embedded via `go:embed`)
- **Legacy support:** Auto-migration for pre-embedded databases (adds `subscription_id` column, drops `x_ui_host`)
- **trial_requests cleanup:** 1-hour cutoff (matching rate-limit window) + 1s buffer to avoid boundary race
- **Connection pool:** `MaxOpenConns=1` (SQLite single-writer), `MaxIdle=1`, `ConnMaxLifetime=5m`
- **Orders:** `orders` table tracks payment lifecycle: pending → paid → expired/canceled. Statuses enforced via CHECK constraint. 30-minute expiry window for unpaid invoices.
- **Nodes:** `nodes` table stores VPN panel sources (host, api_token, inbound_ids, type, subscription_url). Managed via DB only.
- **Plans:** `plans` table (name, devices_limit, traffic_limit), `plan_nodes` M2M join, `products` (duration, price), `subscription_nodes` (sync state machine: active/pending_add/pending_remove/pending_update)
- **Devices tracking:** `subscriptions.devices` column stores JSON array of client request header maps (HWID, Device-OS, etc.). `ips` column stores IP→timestamp entries. `last_request` column (*time, indexed) records the last time a client fetched its subscription via `/sub/{id}` (best-effort, updated on both cache hit and cache miss).

### Configuration
- **Required:** `TELEGRAM_BOT_TOKEN`, `TELEGRAM_ADMIN_ID` (must be positive), `GLOBAL_SUB_URL` (required, builds sub URLs)
- **Validated:**
  - `GLOBAL_SUB_URL` — must be valid URL with http/https scheme (S3: scheme allowlist)
  - `SENTRY_DSN`, `HEARTBEAT_URL` — must be valid URLs with http/https scheme
  - `SITE_URL` — must be valid URL
- **Web server:** Runs on `WEB_SERVER_PORT` (default 8880), bound to all interfaces
- **Init failure:** Fatal exit for DB, XUI, and Bot API init errors (cannot operate without them)
- **SUBSERVER_ACCESS_LOG:** Optional path for `/sub/{id}` access logging (space-separated format with upstream fetch stats)

### Rate Limiting
- **Per-user (Telegram):** Each `chatID` gets own token bucket (max 30 tokens, refill 5/sec)
  - Applied to all command/callback handlers
  - `MaxConcurrentHandlers=10` semaphore prevents goroutine explosion
- **Trial endpoint (IP-based):** 3 requests/hour per IP (DB-tracked, auto-cleanup)
- **Admin `/send`:** 30-second cooldown per admin (in-memory sync.Map)
- **Broadcast:** 50ms delay between messages (~20 msg/sec, respects Telegram limits)

### Security
- **Input validation:** Regex validation rejects path-separator/invalid characters in invite codes (`internal/web/web.go`) and subIDs (`^[a-zA-Z0-9_-]+$` in `internal/subserver/subscription.go`). (The historical `extra_servers.txt` file-path check was removed in v2.3.0 along with that feature.)
- **IP spoofing (S2):** `getClientIP` uses rightmost IP from `X-Forwarded-For` (set by trusted reverse proxy), NOT leftmost (client-controlled, spoofable). Only trusted from loopback.
- **URL scheme restriction (S3):** `validateURL` restricts all configured URLs to `http`/`https` schemes only — prevents `file://`, `gopher://`, etc. SSRF vectors
- **Web→bot dependency break (A1):** `internal/web` no longer imports `internal/bot` — `Server.botUsername string` instead of `*bot.BotConfig`, reducing coupling and attack surface
- **API auth:** Timing-safe token comparison via `crypto/subtle.ConstantTimeCompare`
- **No secrets in code:** `.env` only, `.env.example` has placeholders
- **HTTP timeouts:** ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s, IdleTimeout 60s
- **Port binding:** Verified before goroutine launch — `net.Listen()` then `Serve()` in separate goroutine
- **Non-root Docker:** UID 1000, `no-new-privileges:true`
- **RetryWithBackoff:** 3 retries with exponential backoff + jitter; DNS errors fast-fail


### Health Checks
- **`/healthz`:** DB ping → 200 (ok) or 503 (down)
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
go build -ldflags="-s -w -X main.version=v2.3.4 -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) -X main.buildTime=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" -o rs8kvn_bot ./cmd/bot

# Run linters
golangci-lint run ./...
gosec ./...

# Run locally
go run ./cmd/bot
```
