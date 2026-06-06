# Architecture — rs8kvn_bot

**Version:** 2.3.0  
**Date:** 2026-04-17

---

## Overview

rs8kvn_bot — production-ready Telegram bot for distributing VLESS+Reality+Vision VPN subscriptions via 3x-ui panel. Built with Go, following Clean Architecture principles.

**Key characteristics:**
- Event-driven with bounded concurrency (worker pool)
- Circuit breaker pattern for external dependencies
- Comprehensive caching (in-memory LRU, TTL)
- Graceful shutdown with coordinated cleanup
- 85%+ test coverage (unit, e2e, fuzz, leak detection)

---

## System Context Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                         EXTERNAL SYSTEMS                            │
├─────────────────────────────────────────────────────────────────────┤
│  Telegram Bot API       3x-ui Panel         Optional Monitoring    │
│  (users interact)       (VPN backend)       (Sentry, Heartbeat)    │
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
│  │ Bot API  │              │  Web Server  │              │ SubProxy ││
│  │ Layer    │              │  (port 8880) │              │ Service  ││
│  │          │              │              │              │          ││
│  │ Handler  │              │ /healthz     │              │ cache    ││
│  │ Commands │              │ /readyz      │              │ extra    ││
│  │ Callbacks│              │ /i/{code}    │              │ servers  ││
│  │ RateLim  │              │ /sub/{subID} │              │ merge    ││
│  │ Cache    │              │ /api/v1/*    │              │ reload   ││
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
│  │  ┌────────▼─────────┐                                         │ │
│  │  │   XUIClient      │ (3x-ui API wrapper)                     │ │
│  │  │ • AddClient      │ • CircuitBreaker                        │ │
│  │  │ • GetTraffic     │ • Retry+Jitter                          │ │
│  │  │ • DeleteClient   │ • Singleflight                           │ │
│  │  │ • Login          │ • Session mgmt                           │ │
│  │  └────────┬─────────┘                                         │ │
│  │           │                                                    │ │
│  │  ┌────────▼─────────┐                                         │ │
│  │  │  DatabaseService │ (GORM + SQLite)                         │ │
│  │  │ • CRUD           │ • Migrations (golang-migrate)           │ │
│  │  │ • Queries        │ • Connection pool (1 writer)             │ │
│  │  │ • Transactions   │ • Soft deletes                           │ │
│  │  └──────────────────┘                                         │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│                                 │                                   │
│                                 ▼                                   │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │              Infrastructure & Cross-cutting                     │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐ │ │
│  │  │   Logger     │  │  RateLimiter │  │    WebhookSender    │ │ │
│  │  │ (Zap+Sentry) │  │(token bucket)│  │(async+retry)        │ │ │
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
│   ├── subscription.go      # Create/view/QR subscription
│   ├── menu.go              # Navigation: donate, help, back
│   ├── cache.go             # LRU cache (1000 entries, 5min TTL)
│   ├── referral_cache.go    # Referral count cache (sync hourly)
│   ├── keyboard_builder.go  # Telegram inline keyboards
│   └── message_sender.go    # Rate-limited send wrapper
├── web/              # HTTP server
│   ├── web.go               # Server struct, routes, health
│   ├── middleware.go        # Bearer auth
│   ├── api.go               # /api/v1/subscriptions
│   ├── subserver_test.go     # Proxy handler tests
│   └── templates/           # trial.html, error.html
├── subserver/         # Subscription server
│   ├── service.go           # Hot reload loop (5 min)
│   ├── proxy.go             # Fetch+XUI+merge logic
│   ├── cache.go             # TTL cache (240s)
│   ├── servers.go           # Load extra config file
│   └── servers_test.go      # Parser tests
├── service/          # Business logic
│   └── subscription.go      # Use cases: Create, Delete, Trial
├── xui/              # 3x-ui client
│   ├── client.go            # Full API + retry + singleflight
│   └── breaker.go           # Circuit breaker (5/30s/3-half-open)
├── database/         # Persistence
│   ├── database.go          # GORM service + migrations
│   └── migrations/          # 000..011 SQL files (embedded)
├── config/           # Configuration
│   ├── config.go            # Load + validate
│   └── constants.go         # All defaults & limits
├── logger/           # Logging
│   └── logger.go            # Zap + Sentry + lumberjack
├── ratelimiter/      # Rate limiting
│   ├── ratelimiter.go       # Token bucket core
│   └── per_user.go          # Per-chatID wrapper
├── scheduler/        # Background jobs
│   ├── backup.go            # Daily backup (03:00)
│   └── trial_cleanup.go     # Hourly expired trial cleanup
├── backup/           # Backup engine
│   └── backup.go            # WAL checkpoint + atomic copy + rotate
├── heartbeat/        # External monitoring
│   └── heartbeat.go         # Periodic POST to HEARTBEAT_URL
├── webhook/          # Async notifications
│   └── sender.go            # Retry with classification
├── interfaces/       # DI interfaces
│   └── interfaces.go        # BotAPI, DatabaseService, XUIClient, etc.
├── utils/            # Utilities
│   ├── uuid.go              # Crypto-random UUID v4, SubID, invite code
│   ├── qr.go                # QR code generation
│   ├── time.go              # Month boundary calculation
│   └── format.go            # Progress bar, date formatting
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
| `SubProxyCache` (subserver/cache.go) | Merged subscription bodies by `subID` | 240s (4 min) | 1000 entries | TTL expiration |

**Cache invalidation points:**
- Subscription created → `invalidateCache(telegramID)`
- Subscription deleted → `invalidateCache(telegramID)`
- Trial bound → `invalidateCache(telegramID)`
- SubProxy reload → entire cache cleared on config change

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

**Indexes:**
```sql
CREATE INDEX idx_subscriptions_telegram_id    ON subscriptions(telegram_id);
CREATE INDEX idx_subscriptions_subscription_id ON subscriptions(subscription_id);
CREATE INDEX idx_subscriptions_expiry          ON subscriptions(expiry_time);
CREATE INDEX idx_subscriptions_invite_code     ON subscriptions(invite_code);
CREATE INDEX idx_subscriptions_referred_by     ON subscriptions(referred_by);
CREATE UNIQUE INDEX idx_invites_referrer_unique ON invites(referrer_tg_id);
CREATE INDEX idx_trial_requests_ip             ON trial_requests(ip);
```

**Race-safe patterns:**
- `BindTrialSubscription`: `UPDATE WHERE telegram_id=0 AND plan_id=trialPlanID` + `RowsAffected` check,
  plus defensive revoke of any pre-existing active subscriptions for the same
  `telegram_id` (prevents double-active race when a free sub and a trial sub
  are created concurrently for the same user)
- `CleanupExpiredTrials`: `DELETE ... RETURNING` to atomically fetch deleted rows
- `CreateTrial` (service layer): iterates over all trial sources, aggregates
  errors via `errors.Join`, continues on partial success (first `succeeded` ≥ 1
  ⇒ return success with `logger.Warn("partial failures")`)
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
│ id (PK)              uint                                   │
│ telegram_id          int64    INDEX                         │
│ username             string   INDEX                         │
│ client_id            string                                 │
│ subscription_id      string   INDEX (unique)                │
│ expiry_time          time     INDEX                         │
│ status               string   default: "active"  INDEX      │
│ invite_code          string   INDEX                         │
│ plan_id              uint     INDEX   (FK → plans)          │
│ referred_by          int64    INDEX                         │
│ created_at           time     autoCreate                    │
│ updated_at           time     autoUpdate                    │
└─────────────────────────────────────────────────────────────┘
                              │  plan_id
                              ▼
                    ┌─────────────────────┐
                    │       plans         │
                    ├─────────────────────┤
                    │ id (PK)             │
                    │ name (UNIQUE)       │   e.g. "free", "trial"
                    │ price               │   (cents)
                    │ devices_limit       │
                    │ traffic_limit       │   bytes (was 107374182400)
                    │ duration            │   hours (0 = no expiry)
                    │ created_at          │
                    │ updated_at          │
                    └──────────┬──────────┘
                               │  M:N
                               ▼
                    ┌─────────────────────┐
                    │    plan_sources     │
                    ├─────────────────────┤
                    │ plan_id    (PK,FK)  │
                    │ source_id  (PK,FK)  │
                    └──────────┬──────────┘
                               │  M:N
                               ▼
                    ┌─────────────────────┐
                    │      sources        │
                    ├─────────────────────┤
                    │ id (PK)             │
                    │ name                │
                    │ active              │   default: true
                    │ x_ui_host           │   3x-ui panel URL
                    │ x_ui_api_token      │
                    │ x_ui_inbound_id     │
                    │ sub_url             │   per-source subscription URL
                    │ created_at          │
                    │ updated_at          │
                    └─────────────────────┘

                    ┌─────────────────────┐
                    │      invites        │
                    ├─────────────────────┤
                    │ code (PK)           │
                    │ referrer_tg_id (UNIQUE)│ one canonical code per user
                    │ created_at          │
                    └─────────────────────┘
                              │
                              │ subscriptions.referred_by → referrer_tg_id
                              ▼
                    (referral attribution, see GetAllReferralCounts)

                    ┌─────────────────────┐
                    │   trial_requests    │
                    ├─────────────────────┤
                    │ id (PK)             │
                    │ ip (INDEX)          │
                    │ created_at          │
                    └─────────────────────┘
                              │
                              │ IP-based rate limit (1h window)
                              ▼
                    (checked before trial creation)
```

**Schema changes (v2.4.0, feature/sources-table):**
- Removed from `subscriptions`: `inbound_id`, `traffic_limit`, `subscription_url`, `is_trial`, `deleted_at` (soft delete replaced by `status='revoked'`)
- New tables: `sources` (3x-ui panel registry), `plans` (free/trial), `plan_sources` (M:N)
- `subscriptions.plan_id` foreign key to `plans` (replaces `is_trial` boolean)
- `Source.Trial` field removed; trial sources now resolved dynamically by
  `sources` JOINed with `plan_sources` JOIN `plans WHERE plans.name='trial'`
- `TRAFFIC_LIMIT_GB` config removed; traffic limit fetched from plan (`plan.traffic_limit`)
- Source columns renamed to `x_ui_host`, `x_ui_api_token`, `x_ui_inbound_id` (snake_case aligned with GORM tags)

**Indexes rationale:**
- `telegram_id + status` → fast lookup of user's active subscription
- `subscription_id` → fast `/sub/{subID}` lookup
- `expiry_time` → cleanup of expired subs
- `invite_code` → trial activation via invite
- `referred_by` → referral stats query

---

## Configuration Schema

```yaml
# Full .env schema
telegram:
  bot_token: "string (required, format: number:token)"
  admin_id: "int64 (optional, default 0)"

xui:
  host: "URL (required, must be HTTPS unless localhost)"
  username: "string (required)"
  password: "string (required)"
  inbound_id: "int (default 1, min 1)"
  sub_path: "string (default 'sub', alphanumeric+_-)"
  session_max_age_minutes: "int (default 720)"

database:
  path: "string (default ./data/tgvpn.db)"

logging:
  file_path: "string (default ./data/bot.log)"
  level: "enum: debug|info|warn|error (default info)"

subscription:
  traffic_limit_gb: "int 1-1000 (default 30)"
  trial_duration_hours: "int 1-168 (default 3)"
  trial_rate_limit: "int 1-100 (default 3)"

proxy:
  global_sub_url: "string (required, HTTPS or localhost)"

monitoring:
  # Removed extra_servers config — feature was dropped in v2.3.0
  heartbeat_url: "URL (optional, must be HTTPS)"
  heartbeat_interval: "int seconds, min 10 (default 300)"
  sentry_dsn: "URL (optional, must be HTTPS)"
  health_check_port: "int 1-65535 (default 8880)"

site:
  url: "URL (default https://vpn.site)"
  contact_username: "string (default '')"
  donate_card_number: "string (default '')"
  donate_url: "string (default '')"

api:
  token: "string (optional, 32+ random chars)"

webhook:
  proxy_manager_webhook_secret: "string (optional)"
  proxy_manager_webhook_url: "URL (optional, must be HTTPS)"
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
  │ Click "Get sub" │              │              │             │             │
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
  │                │              │              │            │ XUI.AddClient
  │                │              │              │            │───────────►
  │                │              │              │            │  201 Created
  │                │              │              │            │◄───────────
  │                │              │              │            BuildURL
  │                │              │              │            ┌───────────┘
  │                │              │              │            │ DB Create
  │                │              │              │            │───────────►
  │                │              │              │            │  INSERT OK
  │                │              │              │            │◄───────────
  │                │              │              │ Cache Set  │
  │                │              │              │ Webhook ↑  │
  │                │              │              │ Notify ↓   │
  │                │              │              │◄───────────┤
  │                │              │ SendMessage  │             │
  │                │              │ (with URL+QR)│             │
  │                │              │◄─────────────┤             │
  │                │ Message      │              │             │
  │◄───────────────│              │              │             │
```

---

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-01 | Use SQLite over PostgreSQL | Simpler deployment, single file, adequate for <10k users |
| 2026-01 | In-memory caches vs Redis | No external dependency; cache sizes bounded (1000 entries) |
| 2026-02 | Long polling vs Webhook | Easier deployment (no public HTTPS needed), single instance ok |
| 2026-02 | GORM vs sqlx | Faster dev, migrations built-in, relationship support |
| 2026-03 | Separate subserver package | Reusable Subscription server logic, clean separation |
| 2026-03 | Circuit breaker on XUI | Prevent cascade failures if panel down |
| 2026-04 | 5-min subserver reload | Balance between config freshness and file I/O |
| 2026-04 | Token bucket rate limiting | Standard algorithm, per-user isolation, tunable |
| 2026-04 | Daily backup at 03:00 | Low-traffic period, WAL checkpoint ensures consistency |

---

## Future Considerations

| Area | Potential Improvement | Priority |
|------|----------------------|----------|
| **Database** | Migrate to PostgreSQL for horizontal scaling | P2 (when >10k users) |
| **Cache** | Redis for shared cache across multiple bot instances | P3 (if horizontal scaling) |
| **Metrics** | Prometheus /metrics endpoint with counters/gauges | P1 (monitoring) |
| **Rate limiting** | Distributed rate limiting via Redis (per-IP) | P2 (DDoS protection) |
| **Webhook** | Retry queue with exponential backoff + dead-letter queue | P2 (reliability) |
| **Proxy** | Support formultiple XUI panels (sharding) | P3 (load balancing) |
| **Auth** | OAuth for admin panel (web UI) | P3 (convenience) |
| **Testing** | Property-based testing (quickcheck) | P2 (quality) |
| **CI/CD** | Automated security scanning (Trivy, gosec in CI) | P1 (security) |
| **Deployment** | Helm chart for Kubernetes | P2 (if using k8s) |

---

*End of architecture documentation.*
