# Architecture — rs8kvn_bot

## Карта документации

Документ без строки в этой таблице считается неактуальным. Новый документ
добавляется сюда же — иначе он просто ляжет и умрёт. Источник истины по
взаимодействию с панелью — раздел «Контракт API панели 3x-ui» в конце этого
документа + contract-тесты `internal/xui/panel_contract_test.go`.

| Документ | О чём | Когда читать |
|---|---|---|
| `architecture.md` | устройство системы, модули, потоки | перед изменением архитектуры |
| `operations.md` | эксплуатация: деплой, логи, health, инциденты | при инциденте или релизе |
| `installation.md` | развёртывание с нуля | при первичной установке |
| `api.md` | HTTP-эндпоинты бота и веба | при работе с API |
| `audit-log.md` | модель аудита и правила событий | при изменении `internal/database/audit_log.go` |
| `security.md` | требования безопасности | перед изменением auth/секретов |
| `broadcast.md`, `broadcast-deactivation.md` | рассылки и деактивация получателей | при работе с broadcast-флоу |
| `platega-T3.md` | платёжный провайдер Platega | при работе с оплатой |
| `doc/adr/` | принятые архитектурные решения | **до** предложения рефакторинга: не переоткрывать |
| `CONTEXT.md` (корень) | историческая очередь рефакторинга — закрыта 2026-11-01, см. «История рефакторинга» ниже | архив, не источник истины |
| `handover.md`, `subscription_sync_technical_spec.md`, `reports/`, `diagrams/` | исторические отчёты и визуализации | только по прямой необходимости, не как источник истины |

По правилам `AGENTS.md` не читать и не писать: `bypass_clients_comparison.md`,
`bypass_research.md`, `marketing_strategy.md`, `nginx-xhttp-hysteria2-architecture.md`,
`task-bot-integration.md`.

## Multi-outbounds per node

A single 3x-ui node can now expose multiple inbounds. Inbound IDs are stored as a JSON array in `nodes.inbound_ids` and sent to the panel as `inboundIds` during client creation/update.

rs8kvn_bot — production-ready Telegram bot for distributing VLESS+Reality+Vision VPN subscriptions via 3x-ui and proxman panels. Built with Go, following Clean Architecture principles.

**Key characteristics:**
- Event-driven with bounded concurrency (worker pool)
- Retry with exponential backoff for external dependencies
- Comprehensive caching (in-memory LRU, TTL)
- Graceful shutdown with coordinated cleanup
- 66.2% aggregate test coverage in the latest documented run (unit, e2e, fuzz, leak detection)
- Payment/order tracking for subscription purchases
- Node-based subscription synchronization with 4-state sync machine (`subscription_nodes`)
- 3-touch expiry reminders (3d/1d/3h) with atomic claim and bitmask (`subscriptions.reminders_sent`)
- Traffic quota notifications on traffic-limited plans (90%/exhausted with Premium CTA, reset-triggered re-enable) with atomic claim and bitmask (`subscriptions.traffic_reminders_sent`)
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
│  │ • Background workers startup (backup, heartbeat, trial cleanup, subscription reminders) │ │
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
│  │ Commands │              │ /readyz      │              │ merge    ││
│  │ Callbacks│              │ /i/{code}    │              │ headers  ││
│  │ RateLim  │              │ /sub/{subID} │              │          ││
│  │  Cache    │              │ /payment/cb │              │          ││
│  │          │              │ /static/logo│              │          ││
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
│  │  │ • AddClient      │ • Retry+Jitter          │  Service      │ │ │
│  │  │ • GetTraffic     │ • Retry+Jitter          │  (GORM+SQLite)│ │ │
│  │  │ • DeleteClient   │ • Singleflight          │  • CRUD       │ │ │
│  │  │ • Bearer auth  │ • Retry + timeout       │  • Queries    │ │ │
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
│   ├── command.go            # /start, /help, /invite
│   ├── callback.go           # Inline keyboard callbacks
│   ├── admin.go             # /del, /broadcast, /send, /refstats, broadcast cards
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
├── subserver/         # Subscription server (aggregation + Xray JSON rendering)
│   ├── service.go           # Thin cache adapter over SubscriptionCache (Get/Set/Invalidate)
│   ├── fetch.go             # Fetch from upstream nodes + format detection (JSON/Clash/Base64/Plain)
│   ├── xray.go              # serverConfig → Xray-core outbound conversion (VLESS/VMess/Trojan/SS/Hysteria2)
│   ├── subscription_handler_split.go # Handler pipeline; buildResponse renders one full config per node
│   ├── servers.go           # Subscription format helpers and server-config conversion
│   └── templates/xray_client.json # Embedded per-profile client template (remarks, inbounds, dns, routing)
├── service/          # Business logic
│   ├── subscription.go      # Use cases: Create, Delete, DeleteByID, Renew; unified two-phase teardown (revokeAndDeprovisionThenDelete)
│   ├── subscription_reminders.go # Reminder window model and Telegram delivery
│   ├── subscription_traffic.go # Presentation: TrafficInfo, GetWithTraffic
│   ├── subscription_reset.go # «🔄 Сброс» card line: next traffic reset from the panel profile
│   ├── node_clients.go       # NodeClients registry: per-node Provision + Reader slots, built in cmd/bot
│   ├── sync.go              # Multi-node subscription sync (Reconcile, SyncPendingNodes)
│   ├── plan_reconcile.go     # Single subscription_nodes transition core + db/tx planNodeStore adapters
│   ├── order.go             # Order lifecycle (Create, Activate, Expire)
│   └── subscription_nodes.go # Subscription-node join table CRUD
├── vpn/                # VPN client abstraction (multi-node, multi-type)
│   ├── client.go            # Client interface, Config, NewClient factory, idempotent error classification (ErrSubscriptionAlreadyExists / ErrSubscriptionNotFound)
│   ├── threex_ui.go         # 3x-ui specific client implementation (classifies Create/Update/Delete)
│   ├── proxman.go           # proxman client implementation (classifies Create/Delete)
│   └── fetch.go             # fetch client (read-only HTTP, no-op provisioning)
├── xui/              # Legacy 3x-ui API client (used by vpn/threex_ui.go)
│   ├── client.go            # 3x-ui REST API + RetryWithBackoff (Bearer auth, multi-outbound flow grouping; reads client profile via GET /clients/get/{email})
│   ├── classify.go          # Single panel-response classifier: {success,msg,obj} envelope → sentinel errors
│   └── errors.go            # Sentinel errors (ErrClientNotFound, ErrClientAlreadyExists)
├── database/         # Persistence
│   ├── service.go           # GORM service + connection pool
│   ├── migrations.go        # Embedded migration runner
│   ├── migrations/          # Embedded SQL migrations through the current schema version
│   ├── models.go            # Subscription, Plan, Node, Product, Order, Invite, SubscriptionNode
│   ├── trials.go             # Trial subscription logic, generateTrialTelegramID
│   ├── subscriptions.go      # Subscription CRUD and lifecycle queries
│   ├── subscription_reminders.go # Atomic reminder claim/release and expiry query
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
│   └── metrics.go           # HTTP, bot, XUI, DB, cache metrics
├── logger/           # Logging
│   └── logger.go            # Zap + Sentry + lumberjack
├── ratelimiter/      # Rate limiting
│   ├── ratelimiter.go       # Token bucket core
│   └── per_user.go          # Per-chatID wrapper
├── scheduler/        # Background jobs
│   ├── backup.go            # Daily backup (03:00)
│   ├── trial_cleanup.go     # Expired trial cleanup at startup and every 3h
│   ├── subscription_sync_worker.go  # SyncPendingNodes worker
│   ├── subscription_expire_worker.go # Subscription expiry worker
│   └── subscription_reminder_worker.go # Expiry reminders: 3d / 1d / 3h, idempotent bitmask
├── backup/           # Backup engine
│   └── backup.go            # WAL checkpoint + atomic copy + rotate
├── heartbeat/        # External monitoring
│   └── heartbeat.go         # Periodic POST to HEARTBEAT_URL
├── interfaces/       # DI interfaces
│   └── interfaces.go        # BotAPI, XUIClient; composition-root DatabaseService; per-consumer seams (ADR-0001): SubscriptionStore, SyncStore, OrderStore, BotStore, ReferralCountSource, ReferralInviteStore, AuditLogWriter, WebRepository
├── utils/            # Utilities
│   ├── uuid.go              # Crypto-random UUID v4, SubID, invite code
│   ├── qr.go                # QR code generation
│   ├── retry.go             # Retry helper
│   ├── format.go            # Progress bar, date formatting
│   └── markdown.go          # Markdown sanitization
└── testutil/         # Test helpers
    ├── testutil.go          # Config/XUI/Bot fakes + real-SQLite helper
    ├── fake_service_store.go # ServiceStoreFake: SubscriptionStore+SyncStore+OrderStore (no domain mirroring)
    ├── fake_bot_store.go     # BotStoreFake: *ServiceStoreFake + BotStore surface
    ├── fake_web_repository.go # SubscriptionRepositoryFake: *ServiceStoreFake + WebRepository
    └── fake_small.go         # ReferralCountSourceFake, ReferralInviteStoreFake, AuditLogWriterFake
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
4. Wait for `updatesWg` (in-flight handlers) with 90s timeout
5. Wait for background workers (`wg.Wait()`) with 90s timeout
6. Close logger, database

**Result:** No updates lost, all handlers complete or timeout, clean exit.

---

### 2. XUI Client (internal/xui/client.go)

**Features:**
- Stateless **Bearer-token auth**: every request sets `Authorization: Bearer <apiToken>` (no session, no cookie jar, no login/relogin).
- `doHTTPRequest` does raw HTTP + response-size cap (`MaxResponseSize`); non-200 → `ErrNon200Response` (no retry on server errors).
- Retry with exponential backoff + jitter via   `utils.RetryWithBackoff` (max 3, initial 2s). Retryability is governed by `utils.IsRetryable`.
- Multi-outbound aware: groups `inboundIDs` by required `flow` and issues one panel call per flow group.

**Request layer (`doHTTPRequest`):**

```go
req.Header.Set("Authorization", "Bearer "+c.apiToken)
req.Header.Set("Accept", "application/json")
// body present → Content-Type: application/json
resp, err := c.httpClient.Do(req)
// non-200 → ErrNon200Response (not retried)
// body capped at config.MaxResponseSize
```

**Resilience:**
- **Retry** — `AddClientWithID` / `UpdateClient` / `DeleteClient` / `GetClientTraffic` wrap the panel call in
  `utils.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, fn)` (3 attempts, 2s base,
  exponential ×2 with ±50% jitter). Only network-level errors are retried (`IsRetryable`); DNS errors and
  non-200 responses are treated as **non-retryable**. Context cancellation aborts immediately.

**Multi-outbound flow grouping:**
`groupInboundIDsByFlow` fetches each inbound's transport (`GET /panel/api/inbounds/get/{id}` → `GetRequiredFlow`)
and buckets inbound IDs by flow (e.g. `xtls-rprx-vision` vs transports that need none). Create uses the
`{client, inboundIds}` wrapper; update uses a flat client object. One panel call per flow group.

**Endpoints:**
| Op | Method + path |
|----|---------------|
| Add | `POST /panel/api/clients/add` |
| Delete | `POST /panel/api/clients/del/{email}` |
| Update | `POST /panel/api/clients/update/{currentEmail}` |
| Traffic | `GET /panel/api/clients/traffic/{email}` |
| Ping/verify | `GET /panel/api/server/status` |

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

**Pattern:** Cache-Aside with fail-closed status revalidation (a cache hit is not served when DB status cannot be verified).

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
- `BindTrialSubscription`: check telegram_id < 0 → update (race-safe)

**Orders/Products support:**
- `Product` — purchasable subscription product bound to a plan (name, price, duration)
- `Order` — purchase event with payment tracking (pending/paid/expired/canceled)
- `ListActiveProducts`, `GetProductByID`, `ConfirmOrderPaidCAS`
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
- `BindTrialSubscription`: `UPDATE WHERE status = active AND telegram_id < 0 AND plan_id = <trial_plan_id>` + `RowsAffected` check (trial-подписки используют отрицательные `telegram_id`, колонки `is_trial` нет)
- `CleanupExpiredTrials`: claim as `expired`, deprovision externally, then delete; failed panel operations remain retryable
- `ApplyPlanToSubscription` / `MarkAllForRemoval`: hold the per-subscription sync lock across the full DB state transition
- `GetOrCreateInvite`: always returns the oldest (canonical) code for the referrer.
  The UNIQUE constraint + "one code per referrer" guarantee is enforced by migration 004
  (aggressive deduplication of historical duplicates that accumulated because 004 was
  never applied on legacy DBs due to the old runMigrations hack). Migration 005 is now
  a no-op placeholder to maintain linear migration history. Old codes deleted by 004
  are gone forever (old shared links using them will 404).

---

### 6. Graceful Shutdown

**Signal handling:**
```bash
SIGINT  (Ctrl+C)  → graceful shutdown
SIGTERM (docker stop) → graceful shutdown
SIGQUIT (kill -3) → graceful shutdown (also handled)
```

**Shutdown sequence** (`cmd/bot/lifecycle.go` — `runEventLoop` + `gracefulShutdown`; deferred cleanup of web server, logger and DB in `main()`):

1. `ctx.Done()` received → break event loop
2. `botAPI.StopReceivingUpdates()` — stops long polling, channel closes
3. Drain updates channel (discard remaining updates)
4. Wait for `updatesWg` (max 90s) — all handlers finish or timeout
5. Wait for background `wg` (backup, heartbeat, trial cleanup, sync/expire/orphan workers) (max 90s)
6. Stop `subProxy` cache cleanup
7. Set `webServer.ready = false`
8. `webServer.Stop(ctx)` — shutdown HTTP server (5s timeout)
9. Close logger, database

**Timeouts:**
- `ShutdownTimeout = 90s` (config constant) for Telegram handlers and background workers
- Web server/access-log stop: 5s context in `main()`
- Docker `stop_grace_period`: 90s; the actual duration depends on the active shutdown phase

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
│ reminders_sent       int      NOT NULL default: 0 (bitmask: 3d/1d/3h)   │
│ traffic_reminders_sent int     NOT NULL default: 0 (bitmask: 90%/exhausted/reset) │
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

Configuration is loaded from **flat environment variables** (not nested YAML) via `internal/flag`
and validated in `internal/config/config.go`. Required vars must be set or `Load()` fails.

| Env var | Type | Default | Notes |
|---------|------|---------|-------|
| `TELEGRAM_BOT_TOKEN` | string | — (required) | Format `number:token` |
| `TELEGRAM_ADMIN_ID` | int64 | — (required) | Must be positive |
| `GLOBAL_SUB_URL` | URL | — (required) | e.g. `https://vpn.example.com/sub/` (trailing `/` appended) |
| `DATABASE_PATH` | string | `./data/rs8kvn.db` | SQLite file path |
| `LOG_FILE_PATH` | string | `./data/bot.log` | |
| `LOG_LEVEL` | enum | `info` | `debug`\|`info`\|`warn`\|`error` |
| `HEARTBEAT_URL` | URL | empty (disabled) | http/https only |
| `HEARTBEAT_INTERVAL` | int (s) | `300` | min `10` |
| `SENTRY_DSN` | URL | empty (disabled) | http/https only |
| `WEB_SERVER_PORT` | int | `8880` | `1`–`65535` |
| `SITE_URL` | URL | `https://vpn.site` | http/https only |
| `TRIAL_DURATION_HOURS` | int | `3` | `1`–`168` |
| `TRIAL_RATE_LIMIT` | int | `3` | `1`–`100` (per-IP per hour) |
| `CONTACT_USERNAME` | string | `''` | Support contact |
| `DONATE_CARD_NUMBER` | string | `''` | |
| `DONATE_URL` | string | `''` | |
| `DONATE_ENABLED` | bool | `true` | Toggle "☕ Донат" button in main menu |
| `SUBSERVER_ACCESS_LOG` | path | `''` (disabled) | Optional access log |

**Seeding:** `xui_seed` does **not** exist. Nodes are configured via admin commands / DB, not env.
Only the default **plans** (`trial`, `free`) are auto-seeded when the `plans` table is empty
(`internal/database/service.go`).

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
| `ReconcileOrphanedClients` | Recover missing node queues and revoke fully deprovisioned subscriptions |

The transition rules (mark / add / remove over `subscription_nodes`) live in one
implementation — `plan_reconcile.go` (`reconcilePlanNodesCore` + `planNodeStore`
seam with a `DatabaseService` adapter and a `*gorm.DB` adapter). The background
path runs it best-effort with a revoked guard and `errors.Join`; the payment CAS
path (`ApplyPlanToSubscriptionInTx`) runs it fail-fast without the guard (the CAS
already set the subscription `active` — paying reactivates a revoked
subscription). Parity is pinned by `TestApplyPlan_Parity_BackgroundAndTxSameEndState`.

### Concurrency
- Per-subscription locking via `lockSubscription(subscriptionID)` — prevents concurrent sync of the same subscription
- Lock entries cleaned up when last waiter releases (prevents unbounded map growth)

### Background Workers

| Worker | Schedule | Description |
|--------|----------|-------------|
| `BackupScheduler` | Daily at 03:00 | WAL checkpoint, database backup and rotation |
| `TrialCleanupScheduler` | Immediately at startup, then every 3h | Deprovisions and removes expired anonymous trials |
| `SubscriptionSyncWorker` | Immediately at startup, then every 5m | Processes `pending_*` states with exponential backoff |
| `SubscriptionExpireWorker` | Immediately at startup, then every 1h | Expires paid subscriptions past `expires_at` |
| `SubscriptionTrafficWorker` | Immediately at startup, then every 1h | Sends quota warnings (90%/exhausted) on traffic-limited plans and re-enables clients after traffic reset |
| `SubscriptionReminderWorker` | Immediately at startup, then every 30m | Sends idempotent 3d/1d/3h expiry reminders |
| `OrphanReconciler` | After 30s, then every 8h | Recovers missing queues and revokes fully deprovisioned subscriptions |
| `Heartbeat` | Configured interval, default 5m | Sends optional external monitoring heartbeat |

### Retry Behavior
- Caller-ctx cancellation (`context.Canceled`/`DeadlineExceeded`) writes no retry metadata — the scan aborts early, the next pass picks the record up
- Retryable (network timeouts per `utils.IsRetryable`, transient node unavailability `errNodeUnavailable`): `retry_count` incremented, `retry_at` set with exponential backoff, capped at `maxSyncRetries` (10, ~5.6h)
- Non-retryable (the panel answered and refused: non-200, record not found, already in use) — parked immediately, no ladder
- Exhausted ladder parks the node: `retry_at` set far-future (`parkedRetryAt`), the terminal cause stays in `last_error`; any legitimate status re-transition resets retry metadata and releases the node (no fifth state, no migration)
- `last_error` column stores the last error message
- `SyncPendingNodes` returns aggregate error (`errors.Join`) on partial failures
- A `pending_update` whose panel client is gone (`vpn.ErrSubscriptionNotFound`) is re-queued as `pending_add` (create path) instead of retrying an impossible update

---

## Prometheus Metrics (internal/metrics/)

The bot exposes a `/metrics` endpoint (via `promhttp.Handler()`) on the HTTP server port.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | Counter | method, path, status | Total HTTP requests |
| `http_request_duration_seconds` | Histogram | method, path | HTTP request latency |
| `bot_updates_total` | Counter | command, result | Bot updates (success/error/rate_limited) |
| `bot_update_duration_seconds` | Histogram | — | Bot update processing time |
| `subserver_source_fetch_total` | Counter | `result`, `format` | Upstream source fetch results |
| `subserver_source_fetch_duration_seconds` | Histogram | `result` | Upstream source fetch duration |
| `cache_hits_total` / `cache_misses_total` | Counter | cache | Cache hit/miss |
| `bot_orphaned_clients_revoked_total` | Counter | — | Orphaned subscriptions revoked during reconciliation |
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
| 2026-03 | Circuit breaker on XUI | Prevent cascade failures if panel down (removed as dead code in 406995b, 2026-11) |
| 2026-04 | 5-min subserver reload | Balance between config freshness and file I/O |
| 2026-04 | Token bucket rate limiting | Standard algorithm, per-user isolation, tunable |
| 2026-04 | Daily backup at 03:00 | Low-traffic period, WAL checkpoint ensures consistency |
| 2026-05 | Multi-node VPN abstraction (vpn/) | Support multiple 3x-ui/proxman panels, node-type routing |
| 2026-05 | Plans & pricing model | Plans with devices_limit, traffic_limit; products with duration/price |
| 2026-06 | Subscription sync pipeline | 4-state sync machine (subscription_nodes) with per-sub locking |
| 2026-06 | Prometheus metrics | `/metrics` endpoint with HTTP, bot, XUI, DB, cache metrics |
| 2026-07 | Security hardening (S2/S3/A1) | X-Forwarded-For rightmost IP, URL scheme allowlist, web→bot dependency break |
| 2026-10 | `/sub/{subID}` returns a JSON array of Xray configs | XTLS Subscription Standards body shape; clients (v2rayNG) import one profile per array element, so a single object collapses N nodes into one |

---

## Broadcast Filters

The broadcast system supports recipient filtering via JSON predicates stored in `broadcasts.filters`. Filters are applied server-side using SQL WHERE clauses, ensuring scalability.

### Filter Schema

```json
{
  "plan_type": "paid",
  "subscription_status": "active",
  "registered_after": "2026-01-01T00:00:00Z",
  "registered_before": "2026-06-01T00:00:00Z",
  "inactive_days": 30,
  "ever_paid": true
}
```

All fields are optional. Empty filter `{}` = all active users.

### Filter Fields

| Field | Type | SQL Condition | Notes |
|-------|------|---------------|-------|
| `plan_type` | `string` | `"paid"`: `product_id IS NOT NULL OR price_paid_cents > 0`; `"free"`: free plan AND `product_id IS NULL` AND `price_paid_cents <= 0` | `"paid"` or `"free"` only |
| `subscription_status` | `string` | `status = ?` | Default: `"active"` |
| `registered_after` | `*time.Time` | `created_at >= ?` | Inclusive |
| `registered_before` | `*time.Time` | `created_at <= ?` | Inclusive |
| `inactive_days` | `*int` | `last_request < datetime('now', '-N days')` | `0` = never accessed |
| `ever_paid` | `*bool` | `id IN (SELECT subscription_id FROM orders WHERE status='paid')` | Historical check |

### Filter Combination Logic

All active filters are combined with **AND**:

```
WHERE telegram_id > 0
  AND status = 'active'
  AND (product_id IS NOT NULL OR price_paid_cents > 0)
  AND created_at >= '2026-01-01'
  AND last_request < datetime('now', '-30 days')
  AND id IN (SELECT subscription_id FROM orders WHERE status = 'paid')
```

### UX Flow

```
/broadcast
→ Enter name
→ Enter text (MarkdownV2)
→ Filter keyboard appears:
  [👥 All] [💰 Paid] [🆓 Free]
  [📋 All] [✅ Active] [🚫 Revoked]
  [📅 Reg. 3m] [📅 Reg. 6m] [📅 Reg. 1y] [📅 Reg. all]
  [🚫 Never accessed] [⏰ >1m] [⏰ >3m] [👤 No filter]
  [💳 Paid] [🆓 Never paid] [👤 No filter]
  [✅ Send] [❌ Cancel]
→ Admin selects filters (toggle on/off)
→ Preview updates with filter description
→ Press "✅ Send" → Recipient count preview:
  📦 Broadcast: Promo
  👥 Recipients: 1234
  🔍 Filter: Paid · Active · Inactive >30d
  ⚡ Send to 1234 users?
  [✅ Send now] [⏰ Schedule]
  [🔙 Back to filters] [❌ Cancel]
→ Press "✅ Send now" → Broadcast starts immediately
→ Press "⏰ Schedule" → Day picker (today / tomorrow / +3d / +1w)
  → Hour picker (00:00–23:00, Moscow time)
  → Preview: "Scheduled for 15.08.2026 18:00"
  [✅ Confirm] [🔙 Change time] [❌ Cancel]
→ Press "✅ Confirm" → campaign is stored with `planned_at` set and stays
  `scheduled`; the worker claims it once `planned_at` is due (also after restart).
  `planned_at` is preserved, not overwritten at start time.
```

### Important Implementation Details

1. **`PricePaidCents` is NOT cumulative** — it stores the current product price, not total spent. For historical payment checks, the `orders` table is used (`ever_paid` filter).

2. **Plan detection uses `plans` table** — the `plan_type` filter resolves the free plan by stable `plans.name = 'free'`, but eligibility is decided by payment state: `"paid"` = `product_id IS NOT NULL OR price_paid_cents > 0` (even on the free plan), `"free"` = free plan AND `product_id IS NULL AND price_paid_cents <= 0`.

3. **SQLite compatibility** — date arithmetic uses `datetime('now', '-N days')` syntax, not PostgreSQL `INTERVAL`.

4. **Performance** — all filters are applied at the SQL level (subqueries for `ever_paid`), not in-memory filtering. Scales to thousands of users.

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

### N. Subscription Reminder Worker (internal/scheduler/subscription_reminder_worker.go)

Worker каждые 30 минут выбирает активные платные подписки, у которых `expires_at` попадает в окна 3д / 1д / 3ч. Для каждого окна `SubscriptionService.SendExpiryReminder()` атомарно claim-ит bit только при неизменном сроке и отправляет продающее MarkdownV2-сообщение с преимуществами Premium, ссылкой на подписку и inline-кнопкой `💎 Продлить Premium` (`buy_premium_list`). При ошибке Telegram claim освобождается для повторной попытки. Повторы при пересекающихся окнах и параллельных вызовах исключаются условным claim по `subscriptions.reminders_sent`. При продлении (`RenewSubscription`) маска сбрасывается в `0` в одной транзакции с обновлением подписки и созданием order. Подписки без `expires_at`, а также free/trial планы не участвуют. Метрики: `subscription_reminders_total{window,result}` и `subscription_reminder_runs_total`.

### O. Subscription Traffic Worker (internal/scheduler/subscription_traffic_worker.go)

Worker каждые 60 минут обрабатывает активные подписки, привязанные к тарифам с ненулевым лимитом трафика (`plans.traffic_limit > 0`). Для каждой подписки `SubscriptionService.ProcessTrafficNotifications()` опрашивает живые 3x-ui ноды (`GetClientTraffic`) и, не чаще одного раза на условие (атомарный claim по bitmask `subscriptions.traffic_reminders_sent`), выполняет один из трёх сценариев:

1. **90%** — клиент включён, исчерпано ≥90% лимита: продающее сообщение «осталось меньше 10%» с inline-кнопкой `💎 Перейти на Premium` (`buy_premium_list`).
2. **Исчерпан** — использовано ≥100% лимита, панель отключила клиента: сообщение «Доступ приостановлен» (трафик исчерпан и VPN отключён) с кнопкой Premium. Клиент **не** перевключается автоматически — предлагается покупка Premium.
3. **Сброшен + включён** — счётчик трафика сброшен (`used < limit`), но панель оставила клиента выключенным: воркер **включает клиента** через `UpdateClient(Enable=true)` и шлёт простое сообщение «трафик сброшен — ты снова в сети» без продажи (без кнопки Premium).

При ошибке Telegram claim освобождается для повторной попытки (best-effort; по-нодные сбои логируются как `Warn` и обработка продолжается). На клиентов без traffic-лимита и на free/trial планы воркер не действует. Метрики: `traffic_notifications_total{kind,result}` и `traffic_notification_runs_total`. Миграция: `internal/database/migrations/038_add_traffic_reminders_sent.{up,down}.sql`.

---

## История рефакторинга

Очередь кандидатов из `CONTEXT.md` (обзор 2026-07-09) закрыта 2026-11-01:

- кандидат 1 — circuit breaker XUI удалён как мёртвый код (коммит `406995b`);
- кандидат 2 — закрыт единым реестром `service.NodeClients` (`580e65a`);
- кандидат 3 — proxman получил Bearer + лимит размера ранее, retry сетевых сбоев
  добавлен (`utils.RetryWithBackoff`, идемпотентные EventID; `d744abc`). Отдельный
  общий пакет `httpx` не заведён — дублирование конфигурации транспорта в двух
  адаптерах сознательно принято;
- кандидат 4 — закрыт единым `SyncService.buildNodeProvision` (`11297e7`);
- кандидат 5 — закрыт `reconcilePlanNodesCore` (`ApplyPlanToSubscription`/`InTx`);
- кандидат 6 — `*Cache` встроен в `subserver.Service`, pass-through методы
  удалены (`e723650`);
- кандидат 7 — timeout применяется один раз в `HandleUpdate` на диспетчеризации
  (`withTimeout`), прологи в admin/command handlers снесены
  (см. `internal/bot/dispatch_timeout_test.go`).

---

## Контракт API панели 3x-ui (v3.11.0)

Проверенные факты о панели, с которой работает бот. Источники: живая панель
(`https://panel.example.net/pnl`, доступ через Bearer-токен ноды) и исходники
панели `MHSanaei/3x-ui` **тега `v3.11.0`** (`internal/web/service/client_crud.go`,
`internal/web/controller/api.go`, `internal/web/controller/client.go`,
`internal/web/service/inbound_traffic.go`, `internal/web/service/panel/api_token.go`).
Дата проверки: **2026-10-31**. Живая панель: `currentVersion: 3.11.0`
(`GET /panel/api/server/getPanelUpdateInfo`, Xray `26.10.21`) — проверка шла
прямо на целевой версии.

Документ существует, чтобы контракт не пришлось выяснять заново по живому
доступу к панели. Всё, что ниже помечено «проверено», воспроизводится
read-only запросами или из исходников панели.

## 1. Конверт ответа

Все клиентские эндпоинты отвечают **HTTP 200**, включая ошибки. Тела ответа:

```json
{ "success": true|false, "msg": "текст ошибки", "obj": {} | null }
```

Структурных кодов ошибок (`code`, `status`, тип ошибки) **нет** — только
`success` + `msg`. Поэтому классификация ошибок в боте опирается на текст `msg`;
это осознанный контракт, а не временная заглушка. Соответствующие строки
перечислены в разделе 3 и закреплены тестами в `internal/xui`.

## 2. Эндпоинты, которые использует бот

| Эндпоинт | Метод | Назначение в боте |
|---|---|---|
| `/panel/api/clients/add` | POST | создание клиента (`xui.Client.AddClientWithID`) |
| `/panel/api/clients/update/{email}` | POST | обновление клиента (`xui.Client.UpdateClient`) |
| `/panel/api/clients/del/{email}` | POST | удаление клиента (`xui.Client.DeleteClient`) |
| `/panel/api/clients/resetTraffic/{email}` | POST | сброс счётчика трафика (`xui.Client.ResetTraffic`) |
| `/panel/api/clients/traffic/{email}` | GET | чтение трафика (`xui.Client.GetClientTraffic`) |
| `/panel/api/clients/get/{email}` | GET | **полная карточка клиента**, включая профиль авто-renew (`xui.Client.GetClientByEmail`) |
| `/panel/api/inbounds/get/{id}` | GET | чтение инбаунда (`xui.Client.GetInbound`) |
| `/panel/api/server/status` | GET | проверка живости панели (`xui.Client.Ping`) |

### 2.1. Форма `GET /panel/api/clients/get/{email}` (проверено)

```json
{
  "success": true,
  "msg": "",
  "obj": {
    "client": { "email": "...", "reset": 30, "resetDay": 0, "resetMax": 0,
                "resetWeekday": 0, "trafficReset": "monthly", "trafficResetDay": 1,
                "enable": true, "totalGB": 128849018880, "expiryTime": 1795899600000,
                "subId": "...", "uuid": "...", "tgId": 0, "comment": "..." },
    "externalLinks": [],
    "inboundIds": [2, 4, 5],
    "tunnelAllowedIPs": null,
    "usedTraffic": null
  }
}
```

Клиент лежит **внутри `obj.client`**, не в `obj` — это обязательно к учёту при
разборе ответа. Эндпоинт отдаёт **полный профиль авто-renew**, поэтому
`GET /clients/get/{email}` — единственный правильный способ прочитать
`trafficReset`/`trafficResetDay`; знать `inboundId` для этого не требуется.

## 3. Точные тексты ошибок

Проверено на живой панели 2026-10-31 (для несуществующего email) и по
исходникам панели.

| Операция | Условие | `msg` | HTTP |
|---|---|---|---|
| `del/{email}` | клиента нет | `Something went wrong (client "<email>" not found in any inbound or client record\n)` | 200 |
| `update/{email}` | клиента нет | `Something went wrong (record not found)` | 200 |
| `resetTraffic/{email}` | клиента нет | `Something went wrong (record not found)` | 200 |
| `get/{email}` | клиента нет | `Obtain (record not found)` | 200 |
| `traffic/{email}` | клиента нет | `success=true`, `obj=null`, `msg=""` | 200 |
| `add` | email занят | `email already in use: <email>` (из исходников панели) | 200 |
| `add` | subId занят | `subId already in use: <subId>` (из исходников панели) | 200 |

**Особый случай `traffic/{email}`:** панель отвечает `success=true` и `obj=null`,
а не `success=false`. Отсутствие клиента выражается пустым `obj`, поэтому
`xui.doGetClientTraffic` обязан трактовать `obj == null` как «клиента нет»
(`xui.ErrClientNotFound`) — иначе вызывающий получит nil-указатель или нули
трафика вместо ошибки.

## 4. Особенности поведения панели

### 4.1. `add` идемпотентен при совпадающем subId

`ClientService.Create` (исходник панели) при уже существующем email:

- если `subId` **совпадает** — ошибки нет: панель переиспользует сохранённые
  `uuid`/`password`/`auth` и переписывает настройки клиента;
- если `subId` **отличается** — `email already in use: <email>`.

Бот всегда шлёт стабильный `subId` подписки, поэтому повторный `add` того же
клиента на той же ноде **не является ошибкой панели**. Ветка
`vpn.ErrSubscriptionAlreadyExists` в `SyncService.processPendingAdd` срабатывает
тогда, когда `subId` разошёлся (например, после ручной правки на панели) — и
это правильный сигнал «синхронизируй конфигурацию через update».

### 4.2. Профиль авто-renew и его нормализация

Панель валидирует и нормализует профиль (`client_crud.go`):

- `trafficReset` ∈ {`""`, `never`, `hourly`, `daily`, `weekly`, `monthly`};
  пустое значение нормализуется в `never`;
- `trafficResetDay` ∈ [0, 31], `resetDay` ∈ [0, 31], `resetMax` ≥ 0;
- недельное продление (`resetWeekday > 0`) несовместимо с `reset > 0` или
  `resetDay > 0`.

`GET /clients/traffic/{email}` **не отдаёт** `trafficReset` и
`trafficResetDay` (в ответе есть `reset`, `resetDay`, `resetMax`,
`resetWeekday`, но не `trafficReset*`). Поэтому перед `UpdateClient` профиль
обязательно перечитывается через `GET /clients/get/{email}` — иначе панель
затрёт его значением `never`, и месячный сброс трафика перестанет срабатывать.

### 4.3. Валидация на стороне панели

Панель отклоняет некорректные значения (`common.NewError`), а не чинит их:
пустой email, запрещённые символы в email/subId (`/`, `\`, пробелы, управляющие),
`resetMax < 0`, `trafficResetDay` вне [0, 31]. Ошибки приходят в `msg` с
HTTP 200 — поэтому панельный адаптер обязан разбирать `msg`, а не полагаться на
код ответа.

## 4.4. Скоупы API-токенов (v3.11.0)

Bearer-токены панели имеют скоуп: `admin` (по умолчанию, `gorm default:admin`),
`monitor`, `node-sync`, `node-admin`. Скоупы кроме `admin` режут запросы по
allowlist'у (`nodeSyncScopeAllow`/`monitorScopeAllow` в
`internal/web/controller/api.go`); путь вне allowlist'а → **HTTP 403** с
`msg: "this API token is not permitted to access this endpoint"`.

**В allowlist `node-sync` НЕ входят** `/clients/get/{email}` и
`/clients/traffic/{email}` — чтение карточки и трафика клиента на этих скоупах
невозможно. Бот читает профиль и трафик (группа B), поэтому:

- токен ноды в базе бота должен иметь скоуп `admin` (дефолт `NormalizeScope`);
  проверено живой пробой на v3.11.0: `clients/get` с токеном ноды отвечает 200;
- при смене скоупа на `node-sync`/`monitor` бот получит 403 на все операции с
  клиентами; адаптер классифицирует не-200 как `utils.ErrNon200Response`
  (не ретрабельный — раздел 5), синхронизация узла деградирует с Warn в логе.

## 5. Как это реализовано в боте

1. `xui.Client.GetClientByEmail(ctx, email) (*xui.ClientRecord, error)` читает
   `GET /panel/api/clients/get/{email}` и разбирает `obj.client` и
   `obj.inboundIds` (тип `xui.ClientRecord{Client ClientConfig; InboundIDs []int}`).
   Сканирования `settings` инбаунда больше нет; параметр `inboundID` из
   сигнатуры (`interfaces.XUIClientReader`) удалён.
2. Классификация ошибок панели живёт в одном месте — `internal/xui/classify.go`
   (функция `classifyFailure` + разбор конверта `parseEnvelope`), применяется
   на всех операциях add/update/del/resetTraffic/traffic/get и возвращает
   sentinel-ошибки `xui.ErrClientNotFound` и `xui.ErrClientAlreadyExists`.
   `internal/vpn` маппит sentinel'ы в `vpn.ErrSubscriptionNotFound` /
   `vpn.ErrSubscriptionAlreadyExists` (`mapPanelError` в `threex_ui.go`);
   proxman маппит статусы webhook напрямую, без строковой классификации.
3. Мёртвые sentinel-ошибки (`ErrUnauthorized`, `ErrForbidden`, `ErrBadRequest`,
   `ErrNotFound`, `ErrServerError`) из `internal/xui/errors.go` удалены.
   Sentinel не-200 ответа один: `utils.ErrNon200Response` (владелец —
   `internal/utils`; `internal/xui` импортирует utils, обратной зависимости
   нет). `utils.IsRetryable` возвращает для него `false` — не-200 не
   ретраится (решение о ретраях закреплено тестом).

## 6. Как читать профиль авто-renew

Профиль авто-renew (`reset`, `resetDay`, `resetMax`, `resetWeekday`,
`trafficReset`, `trafficResetDay`) отдаёт **только** `GET /clients/get/{email}`
(в `obj.client`, см. раздел 2.1).

`GET /clients/traffic/{email}` профиль **не отдаёт**: в `ClientTraffic` есть
`reset`, `resetDay`, `resetMax`, `resetWeekday`, но нет `trafficReset*`.
Правило чтения:

- для обновления клиента (`UpdateClient`) профиль всегда перечитывается через
  `GET /clients/get/{email}` и передаётся в запрос целиком; список inbound'ов
  для обновления берётся из `obj.inboundIds` этого же ответа;
- пропуск профиля недопустим: панель не мержит профиль, отсутствующий
  `trafficReset` записывается как `never` (раздел 4.2), и периодический сброс
  трафика перестаёт срабатывать.

В коде это `xui.ClientRecord` и путь восстановления после сброса трафика
`SubscriptionService.reenableResetClients` (internal/service/
subscription_traffic_notifications.go): профиль — из `record.Client`,
inbound'ы для `UpdateClient` — из `record.InboundIDs` с фолбэком на
сконфигурированные inbound'ы узла, если панель вернула пустой список.

## 7. Как проверить контракт без доступа к панели

Контракт закреплён тестами `internal/xui` на фикстурах-ответах панели
(`httptest`), см. `internal/xui/panel_contract_test.go`. Живая панель нужна
только для перепроверки при обновлении панели.


---

*End of architecture documentation.*
