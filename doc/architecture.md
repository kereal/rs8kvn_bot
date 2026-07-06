# Architecture — rs8kvn_bot

**Version:** v2.3.0
**Date:** 2026-07-02

## Multi-outbounds per node

A single 3x-ui node can now expose multiple inbounds. Inbound IDs are stored as a JSON array in `nodes.inbound_ids` and sent to the panel as `inboundIds` during client creation/update.

rs8kvn_bot — production-ready Telegram bot for distributing VLESS+Reality+Vision VPN subscriptions via 3x-ui and proxman panels. Built with Go, following Clean Architecture principles.

**Key characteristics:**
- Event-driven with bounded concurrency (worker pool)
- Circuit breaker pattern for external dependencies
- Comprehensive caching (in-memory LRU, TTL)
- Graceful shutdown with coordinated cleanup
- 85%+ test coverage (unit, e2e, fuzz, leak detection)
- Payment/order tracking for subscription purchases
- Node-based subscription synchronization with 4-state sync machine (`subscription_nodes`)
- Dynamic plan resolution by name (no hardcoded IDs)

---

## System Context Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                         EXTERNAL SYSTEMS                            │
├─────────────────────────────────────────────────────────────────────┤
│  Telegram Bot API       3x-ui Panel         proxman Panel      Optional Monitoring    │
│  (users interact)       (VPN backend)       (VPN backend)       (Sentry, Heartbeat)    │
│         │                     │                     │               │
│         │  GET updates        │  HTTP API           │  POST /ping   │
│         │◄────────────────────┼────────────────────►│              │
│         │                     │                     │               │
└─────────┴─────────────────────┴─────────────────────┴───────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         rs8kvn_bot (single binary)                   │
├─────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │ main.go — Event Loop & Orchestration                           │ │
│  │ • Signal handling (SIGINT, SIGTERM, SIGQUIT)                   │ │
│  │ • Update semaphore (10 concurrent handlers max)                 │ │
│  │ • Background workers startup (backup, heartbeat, trial cleanup) │ │
│  │ • Web server run in goroutine                                   │ │
│  │ • Graceful shutdown: stop receiving, drain updates, wait WG     │ │
│  └─────────────────────────────────┬───────────────────────────────┘ │
│                                    │                                   │
│       ┌────────────────────────────┼────────────────────────────┐   │
│       ▼                            ▼                            ▼   │
│  ┌──────────┐              ┌──────────────┐              ┌──────────┐│
│  │ Bot API  │              │  Web Server  │              │ Subserver││
│  │ Layer    │              │  (port 8880) │              │ Service  ││
│  │          │              │              │              │          ││
│  │ Handler  │              │ /healthz     │              │ cache    ││
│  │ Commands │              │ /readyz      │              │ extra    ││
│  │ Callbacks│              │ /i/{code}    │              │ servers  ││
│  │ RateLim  │              │ /sub/{subID} │              │ merge    ││
│  │ Cache    │              │ /api/v1/*    │              │ reload   ││
│  │          │              │ /metrics     │              │          ││
│  └────┬─────┘              └──────┬───────┘              └────┬─────┘│
│       │                          │                           │      │
│       │    ┌─────────────────────┼─────────────────────┐     │      │
│       │    │                     │                     │     │      │
│       ▼    ▼                     ▼                     ▼     ▼      │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │           Service Layer (Business Logic)                        │ │
│  │  ┌──────────────────┐              ┌─────────────────────────┐ │ │
│  │  │SubscriptionService│              │    ReferralCache        │ │ │
│  │  │ • Create         │              │ • In-memory counts      │ │ │
│  │  │ • Delete         │              │ • Hourly DB sync        │ │ │
│  │  │ • GetWithTraffic │              │ • Increment/Decrement   │ │ │
│  │  │ • CreateTrial    │              └─────────────────────────┘ │ │
│  │  └────────┬─────────┘                                         │ │
│  │           │                                                    │ │
│  │  ┌────────▼─────────┐              ┌─────────────────────────┐ │ │
│  │  │  SyncService     │              │      VPN Clients        │ │ │
│  │  │ • Reconcile      │              │  • 3x-ui (ThreeXUI)     │ │ │
│  │  │ • SyncPending    │              │  • proxman (Proxman)    │ │ │
│  │  │ • process*       │              │  • fetch (FetchClient)  │ │ │
│  │  └────────┬─────────┘              └─────────────┬───────────┘ │ │
│  │           │                                      │               │ │
│  │  ┌────────▼─────────┐                         ┌─┴─────────────┐ │ │
│  │  │  XUIClient       │ (3x-ui API wrapper)     │  Database     │ │ │
│  │  │ • AddClient      │ • CircuitBreaker        │  Service      │ │ │
│  │  │ • GetTraffic     │ • Retry+Jitter          │  (GORM+SQLite)│ │ │
│  │  │ • DeleteClient   │ • Singleflight          │  • CRUD       │ │ │
│  │  │ • Login          │ • Session mgmt          │  • Queries    │ │ │
│  │  └──────────────────┘                         └───────────────┘ │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│                                 │                                   │
│                                 ▼                                   │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │              Infrastructure & Cross-cutting                     │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐ │ │
│  │  │   Logger     │  │  RateLimiter │  │  SubscriptionSync   │ │ │
│  │  │ (Zap+Sentry) │  │(token bucket)│  │  Worker (pending)   │ │ │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘ │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐ │ │
│  │  │   Backup     │  │  Heartbeat   │  │   Scheduler         │ │ │
│  │  │ (WAL+rotate) │  │(POST /ping)  │  │(cron: backup, trial)│ │ │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘ │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│                                                                       │
└───────────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
                   ┌─────────────────────────┐
                   │  External Resources     │
                   ├─────────────────────────┤
│ • Telegram Bot API      │
│ • 3x-ui Panel (REST)    │
│ • proxman Panel (REST)  │
│ • Sentry (error track)  │
                   │ • Filesystem (data/)    │
                   │ • Network (ports 8880)  │
                   └─────────────────────────┘
```

---

## Package Structure

```
internal/
├── bot/              # Telegram layer
│   ├── handler.go           # Main router, update loop
│   ├── commands.go          # /start, /help, /invite
│   ├── callbacks.go         # Inline keyboard callbacks
│   ├── admin.go             # /del, /broadcast, /send, /refstats
│   ├── subscription_handler.go # Create/view/QR subscription
│   ├── menu.go              # Navigation: donate, help, back
│   ├── cache.go             # LRU cache (1000 entries, 5min TTL)
│   ├── referral_cache.go    # Referral count cache (sync hourly)
│   ├── keyboard_builder.go  # Telegram inline keyboards
│   └── message_sender.go    # Rate-limited send wrapper
├── web/              # HTTP server (no longer imports internal/bot — A1 fix)
│   ├── web.go               # Server struct, routes, health, getClientIP
│   ├── middleware.go        # Access-log response recording
│   └── templates/           # trial.html, error.html
├── subserver/         # Subscription server (aggregation + proxy)
│   ├── service.go           # Hot reload loop (5 min)
│   ├── fetch.go             # Fetch from upstream nodes + format detection (JSON/Clash/Base64/Plain)
│   ├── subscription_handler.go # Subscription request handler
│   ├── subscription_helpers.go # Header filtering (FilterHeaders)
│   ├── clash.go             # Clash YAML → serverConfig normaliser
│   ├── access_log.go        # Optional async /sub/{id} access log
│   └── servers.go           # Load extra config file
├── service/          # Business logic
│   ├── subscription.go      # Use cases: Create, Delete, Trial
│   ├── sync.go              # Multi-node subscription sync (Reconcile, SyncPendingNodes)
│   ├── order.go             # Order lifecycle (Create, Activate, Expire)
│   └── subscription_nodes.go # Subscription-node join table CRUD
├── vpn/                # VPN client abstraction (multi-node, multi-type)
│   ├── client.go            # Client interface, Config, NewClient factory, error classification
│   ├── threex_ui.go         # 3x-ui specific client implementation
│   ├── proxman.go           # proxman client implementation
│   └── fetch.go             # fetch client (read-only HTTP, no-op provisioning)
├── xui/              # Legacy 3x-ui API client (used by vpn/threex_ui.go)
│   ├── client.go            # API + RetryWithBackoff + singleflight
│   ├── breaker.go           # Circuit breaker (5/30s/3-half-open)
│   └── errors.go            # Sentinel errors (ErrClientNotFound)
├── database/         # Persistence
│   ├── service.go           # GORM service + connection pool
│   ├── migrations.go        # Embedded migration runner
│   ├── migrations/          # 000..029 SQL files (embedded)
│   ├── models.go            # Subscription, Plan, Node, Product, Order, Invite, SubscriptionNode
│   ├── trials.go            # Trial subscription logic, generateTrialTelegramID
│   ├── subscriptions.go     # Subscription CRUD
│   ├── nodes.go             # Node CRUD
│   ├── plans.go             # Plan CRUD
│   ├── products.go          # Product CRUD
│   ├── orders.go            # Order CRUD
│   └── subscription_nodes.go # SubscriptionNode CRUD
├── config/           # Configuration
│   ├── config.go            # Load + validate (URL scheme allowlist via S3)
│   └── constants.go         # All defaults & limits
├── flag/             # Typed environment variable registry
│   └── flag.go              # Registry, typed flags (string, int, int64)
├── metrics/          # Prometheus metrics
│   └── metrics.go           # HTTP, bot, XUI, DB, cache, circuit breaker metrics
├── logger/           # Logging
│   └── logger.go            # Zap + Sentry + lumberjack
├── ratelimiter/      # Rate limiting
│   ├── ratelimiter.go       # Token bucket core
│   └── per_user.go          # Per-chatID wrapper
├── scheduler/        # Background jobs
│   ├── backup.go            # Daily backup (03:00)
│   ├── trial_cleanup.go     # Hourly expired trial cleanup
│   ├── subscription_sync_worker.go  # SyncPendingNodes worker
│   └── subscription_expire_worker.go # Subscription expiry worker
├── backup/           # Backup engine
│   └── backup.go            # WAL checkpoint + atomic copy + rotate
├── heartbeat/        # External monitoring
│   └── heartbeat.go         # Periodic POST to HEARTBEAT_URL
├── interfaces/       # DI interfaces
│   └── interfaces.go        # BotAPI, DatabaseService, XUIClient, etc.
├── utils/            # Utilities
│   ├── uuid.go              # Crypto-random UUID v4, SubID, invite code
│   ├── qr.go                # QR code generation
│   ├── retry.go             # Retry helper
│   ├── format.go            # Progress bar, date formatting
│   └── markdown.go          # Markdown sanitization
└── testutil/         # Test helpers
    └── testutil.go          # Mock DB, XUI, Bot + Setenv
```

---

## Component Deep Dive

### 1. Main Event Loop (cmd/bot/main.go)

**Concurrency model:** Bounded worker pool via semaphore.

```go
updateSem := make(chan struct{}, config.MaxConcurrentHandlers) // capacity = 10

for update := range updates {
    select {
    case updateSem <- struct{}{}: // acquire slot (blocks if full)
        updatesWg.Add(1)
        go func(u tgbotapi.Update) {
            defer func() {
                <-updateSem // release
                updatesWg.Done()
            }()
            handleUpdateSafely(ctx, handler, u)
        }(update)
    case <-ctx.Done():
        break eventLoop
    }
}
```

**Why semaphore?** Prevents unbounded goroutine creation under load (Telegram can send bursts).

**Graceful shutdown sequence:**
1. Signal received → `ctx.Done()` closes
2. `botAPI.StopReceivingUpdates()` — stops long polling
3. Drain updates channel (empty it)
4. Wait for `updatesWg` (in-flight handlers) with 30s timeout
5. Wait for background workers (`wg.Wait()`) with 30s timeout
6. Close logger, database

**Result:** No updates lost, all handlers complete or timeout, clean exit.

---

### 2. XUI Client (internal/xui/client.go)

**Features:**
- Session-based auth (cookie jar)
- Auto-relogin on 401 with double-checked locking
- Singleflight dedup concurrent logins
- Circuit breaker: 5 failures → 30s open → half-open (3 attempts) → close
- Retry with exponential backoff + jitter (max 3, initial 2s)
- DNS errors classified as non-retryable

**Session lifecycle:**

```
ensureLoggedIn(ctx, force)           // Public entry point
    ├─ RLock: check lastLogin < sessionValidity?
    │   └─ Yes → return (session OK)
    │   └─ No  → Lock: re-check (double-checked)
    │            ├─ Still valid? → return
    │            └─ Expired? → login()
    │                       ├─ POST /login
    │                       ├─ Save cookies to jar
    │                       ├─ Update lastLogin timestamp
    │                       └─ Verify: GET /panel/api/server/status
    │
    ├─ If login fails → circuit breaker.RecordFailure()
    └─ On success → circuit breaker.RecordSuccess()
```

**Singleflight for logins:**
```go
// Multiple goroutines call ensureLoggedIn simultaneously
// Only ONE actually executes login(), others wait for result
result, err, _ := loginGroup.Do("login", func() (interface{}, error) {
    return c.login(ctx)
})
```

---

### 3. Rate Limiter (internal/ratelimiter/)

**Token bucket per user:**

```go
type TokenBucket struct {
    tokens     float64
    lastRefill time.Time
    mu         sync.RWMutex
}

// Allow() consumes 1 token if available, refills based on elapsed time.
// Returns true if token taken, false if would block.
```

**Per-user wrapper:**

```go
type PerUserRateLimiter struct {
    buckets map[int64]*TokenBucket // chatID → bucket
    mu      sync.RWMutex
    maxTokens  float64 // 30
    refillRate float64 // 5/sec
}
```

**Usage:**
```go
if !rateLimiter.Allow(chatID) {
    // reject command (return silently or send "rate limited")
    return
}
```

**Cleanup:** Stale buckets removed every `CacheTTL*2` (10 min) based on `lastRefill`.

---

### 4. Caching Strategy

**Two-level caching:**

| Cache | Purpose | TTL | Max Size | Eviction |
|-------|---------|-----|----------|----------|
| `SubscriptionCache` (bot/cache.go) | Cached subscriptions by `telegramID` | 5 min | 1000 entries | LRU + periodic cleanup |
| `ReferralCache` (bot/referral_cache.go) | Referral counts per referrer | 1 hour sync | unlimited (bounded by users) | N/A (full reload) |
| `SubserverCache` (subserver/cache.go) | Merged subscription bodies by `subID` | 240s (4 min) | 1000 entries | TTL expiration |

**Cache invalidation points:**
- Subscription created → `invalidateCache(telegramID)`
- Subscription deleted → `invalidateCache(telegramID)`
- Trial bound → `invalidateCache(telegramID)`
- Subserver reload → entire cache cleared on config change

**Pattern:** Cache-Aside with stale-as-fallback (proxy returns stale if XUI down).

---

### 5. Database Layer

**ORM:** GORM + SQLite (mattn/go-sqlite3)

**Connection pool:**
```go
MaxOpenConns = 1   // SQLite single-writer constraint
MaxIdleConns = 1
ConnMaxLifetime = 5m
ConnMaxIdleTime = 2m
```

**Migrations:** Embedded SQL files, applied via `golang-migrate` at startup.

**Transactions used for:**
- `CreateSubscription`: revoke old + create new (atomic)
- `BindTrialSubscription`: check telegram_id=0 → update (race-safe)

**Orders/Products support:**
- `Product` — purchasable subscription product bound to a plan (name, price, duration)
- `Order` — purchase event with payment tracking (pending/paid/expired/canceled)
- `UpdateOrderStatus`, `GetActiveByPlanID`, `GetOrdersBySubscriptionID`
- Migration 017: `orders` table with CHECK constraint on status

**Indexes:**
```sql
CREATE INDEX idx_subscriptions_telegram_id    ON subscriptions(telegram_id);
CREATE INDEX idx_subscriptions_subscription_id ON subscriptions(subscription_id);
CREATE INDEX idx_subscriptions_expires_at     ON subscriptions(expires_at);
CREATE INDEX idx_subscriptions_last_request    ON subscriptions(last_request);
CREATE INDEX idx_subscriptions_invite_code     ON subscriptions(invite_code);
CREATE INDEX idx_subscriptions_referred_by     ON subscriptions(referred_by);
CREATE UNIQUE INDEX idx_invites_referrer_unique ON invites(referrer_tg_id);
CREATE INDEX idx_trial_requests_ip             ON trial_requests(ip);
CREATE INDEX idx_products_plan_id              ON products(plan_id);
CREATE INDEX idx_orders_subscription_id        ON orders(subscription_id);
CREATE INDEX idx_orders_status                 ON orders(status);
CREATE INDEX idx_orders_created_at             ON orders(created_at);
```

**Race-safe patterns:**
- `BindTrialSubscription`: `UPDATE WHERE telegram_id=0 AND is_trial=true` + `RowsAffected` check
- `CleanupExpiredTrials`: `DELETE ... RETURNING` to atomically fetch deleted rows
- `GetOrCreateInvite`: always returns the oldest (canonical) code for the referrer.
  The UNIQUE constraint + "one code per referrer" guarantee is enforced by migration 004
  (aggressive deduplication of historical duplicates that accumulated because 004 was
  never applied on legacy DBs due to the old runMigrations hack). Migration 005 is now
  a no-op placeholder to maintain linear migration history. Old codes deleted by 004
  are gone forever (old shared links using them will 404).

---

### 6. Circuit Breaker (internal/xui/breaker.go)

**State machine:**

```
      ┌─────────────┐
      │   CLOSED    │ ← Normal operation
      └──────┬──────┘
             │ 5 consecutive failures
             ▼
      ┌─────────────┐
      │    OPEN     │ ← Reject requests for 30s
      └──────┬──────┘
             │ Timeout (30s) expires
             ▼
      ┌─────────────┐
      │  HALF_OPEN  │ ← Allow up to 3 test requests
      └──────┬──────┘
             │ If all 3 succeed → CLOSED
             │ If any fail    → OPEN (reset timeout)
             ▼
      ┌─────────────┐
      │   CLOSED    │ (back to normal)
      └─────────────┘
```

**Implementation:**
```go
type CircuitBreaker struct {
    state            CircuitState // closed, open, halfOpen
    failures         int
    maxFailures      int // 5
    timeout          time.Duration // 30s
    lastFailure      time.Time
    halfOpenMax      int // 3
    halfOpenAttempts int
    mu               sync.RWMutex
}
```

**Used by:** XUI client on every API call (wrapped in `RetryWithBackoff` which also calls `breaker.Allow()` before request).

---

### 7. Graceful Shutdown

**Signal handling:**
```bash
SIGINT  (Ctrl+C)  → graceful shutdown
SIGTERM (docker stop) → graceful shutdown
SIGQUIT (kill -3) → core dump (not handled by us)
```

**Shutdown sequence** (`cmd/bot/main.go:386-424`):

1. `ctx.Done()` received → break event loop
2. `botAPI.StopReceivingUpdates()` — stops long polling, channel closes
3. Drain updates channel (discard remaining updates)
4. Wait for `updatesWg` (max 30s) — all handlers finish or timeout
5. Wait for background `wg` (backup, heartbeat, trial cleanup) (max 30s)
6. Stop `subProxy` cache cleanup
7. Set `webServer.ready = false`
8. `webServer.Stop(ctx)` — shutdown HTTP server (5s timeout)
9. Close logger, database

**Timeouts:**
- `ShutdownTimeout = 30s` (config constant)
- Web server stop: 5s
- Total shutdown: ~60s worst-case

**Safety:** In-flight requests complete, no new updates accepted.

---

## Data Model

### ER Diagram (text)

```
┌─────────────────────────────────────────────────────────────┐
│                     subscriptions                           │
├─────────────────────────────────────────────────────────────┤
│ telegram_id          int64    UNIQUE INDEX                   │
│ username             string   INDEX                          │
│ client_id            string   NOT NULL UNIQUE                │
│ subscription_id      string   NOT NULL UNIQUE INDEX          │
│ expires_at          *time     INDEX (NULL = perpetual)       │
│ status               string   default: "active"  INDEX       │
│ invite_code         *string   INDEX                          │
│ plan_id              uint     INDEX (FK → plans)             │
│ referred_by         *int64    INDEX                          │
│ product_id          *uint     INDEX                          │
│ started_at          *time                                     │
│ price_paid_cents     int64    default: 0                     │
│ currency            *string   size:3                         │
│ devices              text     default:'[]' (JSON array)      │
│ ips                  text     default:'[]' (JSON array)      │
│ last_request        *time     INDEX (NULL until first /sub)  │
│ created_at           time     autoCreate                     │
│ updated_at           time     autoUpdate                     │
└─────────────────────────────────────────────────────────────┘
                               │
                               │ referred_by
                               ▼
                     ┌─────────────────────┐
                     │      invites        │
                     ├─────────────────────┤
                     │ code (PK)           │
                     │ referrer_tg_id (FK) │
                     │ created_at          │
                     └─────────────────────┘
                               │
                               │ 1:N (referrer → referrals)
                               ▼
                     (subscriptions.referred_by points here)

                     ┌─────────────────────┐
                     │   trial_requests    │
                     ├─────────────────────┤
                     │ id (PK)             │
                     │ ip (INDEX)          │
                     │ created_at          │
                     └─────────────────────┘
                               │
                               │ IP-based rate limit
                               ▼
                     (checked before trial creation)

┌─────────────────────────────────────────────────────────────┐
│                       products                              │
├─────────────────────────────────────────────────────────────┤
│ id (PK)              uint                                   │
│ plan_id              uint     INDEX  (FK → plans)           │
│ name                 string   VARCHAR(255) NOT NULL         │
│ duration_days        int      NOT NULL                      │
│ price_cents          int64    NOT NULL                      │
│ currency             char(3)  DEFAULT 'RUB'                 │
│ is_active            bool     DEFAULT true                  │
│ created_at           time     autoCreate                    │
│ updated_at           time     autoUpdate                    │
└─────────────────────────────────────────────────────────────┘
                               │
                               │ 1:N (product → orders)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                       orders                                │
├─────────────────────────────────────────────────────────────┤
│ id (PK)              uint                                   │
│ subscription_id      uint     NOT NULL  (FK → subscriptions)│
│ product_id           uint     NOT NULL  (FK → products)     │
│ status               text     NOT NULL                      │
│                     CHECK (status IN                       │
│                       'pending','paid','expired','canceled')│
│ amount_cents         int64    NOT NULL                      │
│ currency             char(3)  DEFAULT 'RUB'                 │
│ payment_provider     text                                  │
│ provider_payment_id  text     External payment ID          │
│ created_at           datetime NOT NULL                      │
│ paid_at              datetime  When payment confirmed       │
│ activated_at         datetime  When subscription activated  │
│ expires_at           datetime  Payment expiry (e.g. 30 min) │

┌─────────────────────────────────────────────────────────────┐
│                        nodes                                │
├─────────────────────────────────────────────────────────────┤
│ id (PK)              uint                                   │
│ name                 string   size:255                      │
│ is_active            bool     default: true                 │
│ host                 string   size:255                      │
│ api_token            string   size:255                      │
│ inbound_ids          text     JSON array (e.g. [1,2,3])     │
│ subscription_url     string   size:512                      │
│ type                 varchar  default: '3x-ui'              │
│ created_at           time     autoCreate                    │
│ updated_at           time     autoUpdate                    │
└─────────────────────────────────────────────────────────────┘
                               │
                               │ M2M via plan_nodes
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                        plans                                │
├─────────────────────────────────────────────────────────────┤
│ id (PK)              uint                                   │
│ name                 string   size:50 UNIQUE                │
│ is_active            bool     not null default: true        │
│ devices_limit        int      default: 1                    │
│ traffic_limit        int64    default: 0 (0=unlimited)      │
│ created_at           time     autoCreate                    │
│ updated_at           time     autoUpdate                    │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                     plan_nodes (M2M)                        │
├─────────────────────────────────────────────────────────────┤
│ plan_id (PK, FK)     uint → plans.id                        │
│ node_id (PK, FK)     uint → nodes.id                        │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                  subscription_nodes                         │
├─────────────────────────────────────────────────────────────┤
│ subscription_id (PK, FK)  uint → subscriptions.id           │
│ node_id (PK, FK)          uint → nodes.id                   │
│ status                    SyncStatus (active/pending_*)     │
│ retry_count               int      default: 0               │
│ retry_at                 *time                               │
│ last_error               *text                               │
│ updated_at                time     autoUpdate               │
└─────────────────────────────────────────────────────────────┘

**Sync states:** active | pending_add | pending_remove | pending_update
└─────────────────────────────────────────────────────────────┘

**Order statuses:**
- `pending` — payment initiated, awaiting confirmation
- `paid` — payment confirmed, subscription activated
- `expired` — payment window expired (e.g. 30 min unpaid)
- `canceled` — user or system canceled

**Indexes:**
- `idx_orders_subscription_id` — fast lookup of orders by subscription
- `idx_orders_status` — filter by status
- `idx_orders_created_at` — chronological ordering
- `idx_products_plan_id` — fast lookup of products by plan
```

**Indexes rationale:**
- `telegram_id + status` → fast lookup of user's active subscription
- `subscription_id` → fast `/sub/{subID}` lookup
- `expires_at` → cleanup of expired subs
- `referred_by` → referral stats query
- `last_request` → "last active" queries (when a client last fetched its subscription)

---

## Configuration Schema

```yaml
# Full .env schema (matches internal/config/config.go)
telegram:
  bot_token: "string (required, format: number:token)"
  admin_id: "int64 (required, must be positive)"
  contact_username: "string (default '')"

# Seed-only — read on first run to populate nodes table if empty
xui_seed:
  host: "URL (seed-only, default http://localhost:2053)"
  api_token: "string (seed-only)"
  inbound_id: "int (seed-only, default 1)"

subscription_server:
  global_sub_url: "URL (required, e.g. https://vpn.example.com/sub/)"
  subserver_access_log: "path (optional, empty=disabled)"

database:
  path: "string (default ./data/rs8kvn.db)"

logging:
  file_path: "string (default ./data/bot.log)"
  level: "enum: debug|info|warn|error (default info)"

monitoring:
  heartbeat_url: "URL (optional, http/https only — S3 scheme allowlist)"
  heartbeat_interval: "int seconds, min 10 (default 300)"
  sentry_dsn: "URL (optional, http/https only — S3 scheme allowlist)"
  health_check_port: "int 1-65535 (default 8880)"

trial:
  duration_hours: "int 1-168 (default 3)"
  rate_limit: "int 1-100 (default 3)"

site:
  url: "URL (default https://vpn.site, http/https only)"
  donate_card_number: "string (default '')"
  donate_url: "string (default '')"
```

---

## Sequence Diagrams

### User gets subscription

```
User           Telegram       Bot (main)     Handler      XUI Panel       DB
  │                │              │              │             │             │
  │ /start         │              │              │             │             │
  │───────────────►│              │              │             │             │
  │                │ update       │              │             │             │
  │                │─────────────►│              │             │             │
  │                │              │ route        │             │             │
  │                │              │─────────────►│             │             │
  │                │              │              │ HandleStart │             │
  │                │              │              │────────────►│             │
  │                │              │              │            SendMessage  │
  │                │              │              │ (main menu) │           │
  │                │              │              │◄────────────┤             │
  │                │              │              │             │             │
  │ Click "Get sub"│              │              │             │             │
  │                │ callback     │              │             │             │
  │───────────────►│              │              │             │             │
  │                │ cb query     │              │             │             │
  │                │─────────────►│              │             │             │
  │                │              │ HandleCallback│             │             │
  │                │              │─────────────►│             │             │
  │                │              │              │ createSub   │             │
  │                │              │              │────────────►│             │
  │                │              │              │            GenerateUUID │
  │                │              │              │            ┌───────────┘
  │                │              │              │            │ Resolve Plan/Nodes
  │                │              │              │            │───────────►
  │                │              │              │            │   Plan + Nodes
  │                │              │              │            │◄───────────
  │                │              │              │ BuildURLs  │
  │                │              │              │            │ XUI.AddClient (per node)
  │                │              │              │            │───────────►
  │                │              │              │            │  201 Created
  │                │              │              │            │◄───────────
  │                │              │              │ Create subscription_nodes (pending_add)
  │                │              │              │            │ DB Create
  │                │              │              │            │───────────►
  │                │              │              │            │  INSERT OK
  │                │              │              │            │◄───────────
  │                │              │              │ Cache Set  │
  │                │              │              │ Sync ↑     │
  │                │              │              │ Notify ↓   │
  │                │              │              │◄───────────┤
  │                │              │ SendMessage  │             │
  │                │              │ (with URL+QR)│             │
  │                │              │◄─────────────┤             │
  │                │              │              │             │
  │                │ Message      │              │             │
  │◄───────────────│              │              │             │
  │
  │ (Background queue workers sync subscription_nodes -> active)
```
---

## VPN Client Abstraction (internal/vpn/)

The `vpn` package provides a unified interface for provisioning VPN subscriptions across different panel types.

```go
type Client interface {
    CreateSubscription(ctx context.Context, provision SubscriptionProvision) error
    UpdateSubscription(ctx context.Context, provision SubscriptionProvision) error
    DeleteSubscription(ctx context.Context, provision SubscriptionProvision) error
    Close() error
}
```

**Factory:** `NewClient(cfg Config) (Client, error)` routes by `NodeType`:
- `NodeType3xUI` → `ThreeXUIClient` (wraps `xui.Client`, iterates inbound IDs)
- `NodeTypeProxman` → `ProxmanClient` (direct HTTP webhook, no-op update)
- `NodeTypeFetch` → `FetchClient` (read-only HTTP fetch, all methods no-op)

**Error classification:**
- `ErrSubscriptionAlreadyExists` — wrapped when create fails with "already exists"/"duplicate"
- `ErrSubscriptionNotFound` — wrapped when delete fails with "not found"/"does not exist"
- `ErrNotImplemented` — unsupported operation for node type

**Per-node clients:** Each `database.Node` gets its own `vpn.Client` instance created at startup. The `SyncService` holds a `map[uint]vpn.Client` keyed by node ID.

---

## Node Types Reference

The system supports three node types, stored in the `nodes.type` column (`VARCHAR(10)`). Each type defines how the node provisions VPN clients and how the subserver fetches proxy configuration.

### `3x-ui` — 3X-UI Panel

Full lifecycle management via the 3x-ui REST API.

| Operation | Behaviour |
|-----------|-----------|
| Create | `AddClientWithID` on each inbound ID via `xui.Client` |
| Update | `UpdateClient` (traffic limits, expiry) |
| Delete | `DeleteClient` by email |
| Subserver URL | `subscription_url + "/" + subID` |

**End-to-end:**
1. Subscription created → `pending_add` record in `subscription_nodes`
2. `SyncService` calls `ThreeXUIClient.CreateSubscription` → adds client on panel → status `active`
3. Subserver request `/sub/{id}` → HTTP GET to `subscription_url/subID` → 3x-ui returns proxy configs
4. Subscription deleted → `pending_remove` → `DeleteClient` on panel → row removed

### `proxman` — Proxman Webhook

Provisioning via HTTP webhook events. No update operation (webhook payload carries no traffic/expiry fields).

| Operation | Behaviour |
|-----------|-----------|
| Create | POST `subscription.create` event to node host |
| Update | No-op (returns `nil`) |
| Delete | POST `subscription.delete` event to node host |
| Subserver URL | `subscription_url + "/" + subID` |

**End-to-end:**
1. Subscription created → `pending_add` → `ProxmanClient.CreateSubscription` sends webhook → status `active`
2. Subserver request → HTTP GET to `subscription_url/subID` → proxman returns proxy configs
3. Subscription deleted → `pending_remove` → `ProxmanClient.DeleteSubscription` sends webhook → row removed

### `fetch` — Read-Only HTTP Fetch

No provisioning at all. The `subscription_url` points directly to an HTTP endpoint that returns proxy configuration. All `vpn.Client` methods are no-ops.

| Operation | Behaviour |
|-----------|-----------|
| Create | No-op (returns `nil`) |
| Update | No-op (returns `nil`) |
| Delete | No-op (returns `nil`) |
| Subserver URL | `subscription_url` used as-is (no `subID` appended) |

**End-to-end:**
1. Subscription created → `pending_add` → `FetchClient.CreateSubscription` (no-op) → status `active`
2. Subserver request `/sub/{id}` → HTTP GET to `subscription_url` directly → upstream returns proxy configs
3. Subscription deleted → `pending_remove` → `FetchClient.DeleteSubscription` (no-op) → row removed

The `fetch` node type is useful for aggregating external subscription sources that do not support client management — the bot simply proxies their response to the end user.

## Subscription Sync Pipeline (internal/service/sync.go)

The `SyncService` manages synchronization of subscriptions with VPN nodes via a 4-state machine.

### State Machine

```
┌──────────────┐
│ pending_add  │ ── CreateSubscription() ──→ ┌──────────┐
└──────────────┘                              │  active  │
┌──────────────┐ ── UpdateSubscription() ──→ └──────────┘
│pending_update│                                   │
└──────────────┘                                   │ DeleteSubscription()
┌──────────────┐ ◄────────────────────────────────┘
│pending_remove│ ── delete row ──→ (gone)
└──────────────┘
```
### Operations

| Method | Description |
|--------|-------------|
| `SyncSubscription` | Sync a single subscription across all its nodes |
| `SyncPendingNodes` | Scan all `pending_*` records, process with retry |
| `ReconcilePlanNodes` | Add/remove nodes when plan changes |
| `ReconcileOrphanedClients` | Find XUI clients without DB subscription, delete them |

### Concurrency
- Per-subscription locking via `lockSubscription(subscriptionID)` — prevents concurrent sync of the same subscription
- Lock entries cleaned up when last waiter releases (prevents unbounded map growth)

### Background Workers

| Worker | Schedule | Description |
|--------|----------|-------------|
| `SubscriptionSyncWorker` | Continuous | Processes `pending_*` states with exponential backoff |
| `SubscriptionExpireWorker` | Periodic | Expires subscriptions past `expires_at` |
| `OrphanReconciler` | Every 6h | Cleans up orphaned XUI clients |

### Retry Behavior
- Transient failures: `retry_count` incremented, `retry_at` set with exponential backoff
- `last_error` column stores the last error message
- `SyncPendingNodes` returns aggregate error (`errors.Join`) on partial failures
- Only `context.Cancelled`/`DeadlineExceeded` abort the scan early

---

## Prometheus Metrics (internal/metrics/)

The bot exposes a `/metrics` endpoint (via `promhttp.Handler()`) on the HTTP server port.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | Counter | method, path, status | Total HTTP requests |
| `http_request_duration_seconds` | Histogram | method, path | HTTP request latency |
| `http_requests_in_flight` | Gauge | method, path | Current in-flight requests |
| `bot_updates_total` | Counter | command, result | Bot updates (success/error/rate_limited) |
| `bot_update_errors_total` | Counter | type | Bot update errors |
| `bot_update_duration_seconds` | Histogram | — | Bot update processing time |
| `subserver_source_fetch_total` | Counter | `result`, `format` | Upstream source fetch results |
| `subserver_source_fetch_duration_seconds` | Histogram | `result` | Upstream source fetch duration |
| `cache_hits_total` / `cache_misses_total` | Counter | cache | Cache hit/miss |
| `circuit_breaker_state` | Gauge | target | CB state (0=closed, 1=open, 2=half-open) |
| `bot_orphaned_clients_removed_total` | Counter | — | Orphaned clients removed |
| `subserver_cache_invalidations_total` | Counter | `reason` | Cache invalidations by reason |
| `subserver_no_items_total` | Counter | — | Requests returning no items |

**Path normalization:** `/i/{code}` → `/i/:code`, `/sub/{id}` → `/sub/:id` (reduces cardinality).

---

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-01 | Use SQLite over PostgreSQL | Simpler deployment, single file, adequate for <10k users |
| 2026-01 | In-memory caches vs Redis | No external dependency; cache sizes bounded (1000 entries) |
| 2026-02 | Long polling vs Webhook | Easier deployment (no public HTTPS needed), single instance ok |
| 2026-02 | GORM vs sqlx | Faster dev, migrations built-in, relationship support |
| 2026-03 | Separate subserver package | Reusable subscription server logic, clean separation |
| 2026-03 | Circuit breaker on XUI | Prevent cascade failures if panel down |
| 2026-04 | 5-min subserver reload | Balance between config freshness and file I/O |
| 2026-04 | Token bucket rate limiting | Standard algorithm, per-user isolation, tunable |
| 2026-04 | Daily backup at 03:00 | Low-traffic period, WAL checkpoint ensures consistency |
| 2026-05 | Multi-node VPN abstraction (vpn/) | Support multiple 3x-ui/proxman panels, node-type routing |
| 2026-05 | Plans & pricing model | Plans with devices_limit, traffic_limit; products with duration/price |
| 2026-06 | Subscription sync pipeline | 4-state sync machine (subscription_nodes) with per-sub locking |
| 2026-06 | Prometheus metrics | `/metrics` endpoint with HTTP, bot, XUI, DB, cache metrics |
| 2026-07 | Security hardening (S2/S3/A1) | X-Forwarded-For rightmost IP, URL scheme allowlist, web→bot dependency break |

---

## Future Considerations

| Area | Potential Improvement | Priority |
|------|----------------------|----------|
| **Database** | Migrate to PostgreSQL for horizontal scaling | P2 (when >10k users) |
| **Cache** | Redis for shared cache across multiple bot instances | P3 (if horizontal scaling) |
| **Rate limiting** | Distributed rate limiting via Redis (per-IP) | P2 (DDoS protection) |
| **Proxy** | Support for multiple XUI panels (sharding) | Done — multi-node via vpn/ abstraction |
| **Auth** | OAuth for admin panel (web UI) | P3 (convenience) |
| **Testing** | Property-based testing (quickcheck) | P2 (quality) |
| **CI/CD** | Automated security scanning (Trivy, gosec in CI) | P1 (security) |
| **Deployment** | Helm chart for Kubernetes | P2 (if using k8s) |
| **Storage** | Real Cloudflare R2 backend for UploadStore | P2 (WIRED-not-PROVEN) |
| **Transcription** | Live HTTP transcription endpoint | P2 (WIRED-not-PROVEN) |

---

*End of architecture documentation.*
