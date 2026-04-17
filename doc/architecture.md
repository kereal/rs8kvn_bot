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
│   ├── admin.go             # /del, /broadcast, /send, /refstats, /plan
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
│   ├── subproxy_test.go     # Proxy handler tests
│   └── templates/           # trial.html, error.html
├── subproxy/         # Subscription proxy
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
│   └── migrations/          # 000..003 SQL files (embedded)
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
| `SubProxyCache` (subproxy/cache.go) | Merged subscription bodies by `subID` | 240s (4 min) | 1000 entries | TTL expiration |

**Cache invalidation points:**
- Subscription created → `invalidateCache(telegramID)`
- Subscription deleted → `invalidateCache(telegramID)`
- Plan updated → `invalidateCache(telegramID)`
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
- `BindTrialSubscription`: `UPDATE WHERE telegram_id=0 AND is_trial=true` + `RowsAffected` check
- `CleanupExpiredTrials`: `DELETE ... RETURNING` to atomically fetch deleted rows
- `GetOrCreateInvite`: `INSERT OR IGNORE` + `SELECT` (unique index on referrer_tg_id)

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

### 7. Subscription Proxy Extra Servers

**Config format** (`extra_servers.txt`):

```
# Headers section (optional)
X-Custom-Header: my-value
X-Server-Name: RS8-KVN Backup

# End of headers (blank line required)

# Server lines (one per line)
vless://uuid@backup1.example.com:443?security=reality&...
trojan://password@backup2.example.com:443?...
vmess://another-uuid@backup3.com:443?...
```

**Parsing rules:**
- Lines starting with `#` are comments (ignored)
- Blank line ends header section
- Header lines require `Key: Value` format
- Server lines recognized by scheme prefix (case-insensitive)
- Headers override 3x-ui headers (last-wins)
- Servers appended after 3x-ui servers (client selects first working)

**Supported schemes:**
`vless://`, `vmess://`, `trojan://`, `ss://`, `ssr://`, `hysteria://`, `hysteria2://`, `hy2://`, `tuic://`, `wg://`, `wireguard://`

**Security:** Path validated before `os.Open` — no `..`, no system dirs, must be absolute within allowed base.

**Reload loop:**
```go
ticker := time.NewTicker(5 * time.Minute)
for {
    select {
    case <-ticker.C:
        svc.ReloadConfig() // keeps old config on error
    case <-stopCh:
        return
    }
}
```

---

### 8. Graceful Shutdown

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
│ subscription_id      string   INDEX (unique)                 │
│ inbound_id           int      INDEX                         │
│ traffic_limit        int64    default: 107374182400 (100GB)  │
│ expiry_time          time     INDEX                         │
│ status               string   default: "active"  INDEX       │
│ plan                 string   default: "free"    INDEX       │
│ subscription_url     string                                  │
│ invite_code          string   INDEX                         │
│ is_trial             bool     default: false  INDEX         │
│ referred_by          int64    INDEX                         │
│ created_at           time     autoCreate                    │
│ updated_at           time     autoUpdate                    │
│ deleted_at           gorm.DeletedAt  INDEX (soft delete)    │
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
```

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
  extra_servers_enabled: "bool (default true)"
  extra_servers_file: "string (default ./data/extra_servers.txt)"

monitoring:
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
| 2026-03 | Separate subproxy package | Reusable subscription proxy logic, clean separation |
| 2026-03 | Circuit breaker on XUI | Prevent cascade failures if panel down |
| 2026-04 | 5-min subproxy reload | Balance between config freshness and file I/O |
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
