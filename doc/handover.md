# Handover Summary — rs8kvn_bot

**Repo:** https://github.com/kereal/rs8kvn_bot
**Module:** `rs8kvn_bot` (Go 1.25+)
**Version:** v3.0.0
**Branch:** `dev` (GitFlow: `main` = production, `dev` = integration)

---

## Architecture

### High-Level Component Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Telegram Bot API                            │
└─────────────────────────────┬───────────────────────────────────────┘
                              │ GetUpdates (long polling)
                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     cmd/bot/main.go (Entry Point)                   │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │ • Config loading                                               │ │
 │  │ • Service initialization (DB, XUI, Bot, Web, SubServer)        │ │
│  │ • Graceful shutdown coordination (signal handling)             │ │
│  │ • Worker pool semaphore (10 concurrent handlers)               │ │
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
        ├─ Build subscription URL: {XUI_HOST}/sub/{SubID}?...
        │
        ▼
XUI Client: AddClientWithID(...)
        │
        ├─ Authorize via Bearer token (no session needed)
        ├─ POST /panel/api/clients/add (client + inboundIds)
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

### Subscription server Flow (v2.3.0+)

```
User opens subscription link in client
        │
        ▼
GET /sub/{subID}
        │
        ├─ 1. Validate subID via regex ^[a-zA-Z0-9_-]+$
        ├─ 2. Check per-subID cache (Cache.Get, TTL 240s)
        │      └─ Hit → writeSubscriptionResponse (200)
        │
        └─ 3. Miss → DB: GetSubscriptionWithPlanAndSources (JOIN)
        │      Returns SubscriptionFull — Subscription + Plan + []Source
        │
        ├─ 4. Filter request headers:
        │      Exclude X-Forwarded-Proto, X-Forwarded-For, X-Real-Ip
        │      Lowercase keys+values → device entry
        │
        ├─ 5. Update Devices (x-hwid rotation):
        │      Parse Devices JSON → find by x-hwid → remove → append new
        │
        ├─ 6. Update IPs ({ip: timestamp} array):
        │      Parse Ips JSON → find by IP → remove → append {ip: now}
        │      Max 100 entries, oldest evicted
        │
         └─ 7. Fetch each active Source with SubURL:
               └─ subserver.FetchFromXUI(url) (no per-URL cache)
              ├─ Detect format: JSON / Base64 / Plain / Unknown
              └─ Accumulate userinfo across sources
                    • upload += source_upload
                    • download += source_download
                    • expire = min(existing_expire, source_expire)
                    • total = Plan.TrafficLimit

        └─ 8. Format & output
              ├─ All JSON sources → json.Marshal(configs) → JSON response
              │
              └─ Mixed / Base64 / Plain:
                    ├─ JSON configs → share links (ConvertSingleJSONToLink)
                    └─ All items → base64.StdEncoding → base64 response

        └─ 9. Forward source headers (profile-title, routing-*, etc.)
              Skip: content-length, content-encoding, transfer-encoding

        └─10. Set response headers:
              ├─ Subscription-UserInfo: upload=X; download=Y; total=Z; expire=W
              ├─ Content-Type (application/json or text/plain)
              └─ Forwarded headers from step 9

        └─11. Cache.Set(subID, body) + write 200 (chunked)
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
| Error tracking | `getsentry/sentry-go` | v0.45.0 |
| Concurrency | `golang.org/x/sync/singleflight` | — |
| Testing | `stretchr/testify` | v1.11.1 |
| CI/CD | GitHub Actions → golangci-lint, gosec, test, Docker → GHCR | — |

## Current State (v3.0.0)

### Working Features

**User Features:**
- `/start`, `/help` — start commands with inline keyboards
- 📋 Subscription view — traffic usage, subscription link, QR code
- ☕ Donate, ❓ Help — auxiliary menus
- 🔗 Referral system — share links with in-memory cache + periodic DB sync
- 🎁 Trial subscriptions via `/i/{code}` landing page with Happ deep-links


**Admin Features:**
- `/del <id>` — delete subscription by ID
- `/broadcast <msg>` — send message to all users (respects shutdown context)
- `/send <id|username> <msg>` — private message (30s cooldown per admin)
- `/refstats` — referral statistics (top 10 from cache)
- 📊 Stats — bot statistics

**Infrastructure:**
- 3x-ui integration — Bearer token auth (no session/login/CSRF), client CRUD, RetryWithBackoff (3 retries, jitter), flow detection
- Health endpoints — `/healthz` (503 when Down), `/readyz` (503 during init)
- Invite/trial landing — `/i/{code}` with IP rate limit (3/hour), cookie dedup (3h)
- Per-user rate limiting — chatID token bucket (30 tokens, 5/sec refill, 10-min idle cleanup)
- Subscription server — `GET /sub/{subID}` multi-source aggregation, JSON→share-link, devices/IPs tracking, 240s TTL cache
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
- **Token configuration:** `XUI_API_TOKEN` env var — no username, password, or session age needed
- **No connection pool cleanup needed:** No session state to invalidate
- **No circuit breaker:** Removed in favor of simple `RetryWithBackoff` with exponential backoff + jitter
- **Subscription defaults:** `reset: 30` (days from creation), `expiryTime: now + 30 days`
- **Auto-reset:** Only works when `ExpiryTime > 0`. Traffic resets every 30 days, expiry extends (3x-ui auto-renew logic)
- **Client email:** `trial_{subID}` for trial, `{username}` for regular
- **Ping:** `Ping()` sends GET `/panel/api/server/status` with Bearer token — no session verification needed
- **No singleflight:** Deduplication removed (no concurrent login to deduplicate)
- **DNS error fast-fail:** Non-retryable errors fail immediately (no retry spam)
- **Flow detection:** When creating/updating clients, fetches inbound config via `GET /panel/api/inbounds/get/{id}` to determine transport type. Flow is set based on transport: `tcp` → `"xtls-rprx-vision"`, `xhttp/h2/ws/grpc` → `""` (empty). Falls back to `"xtls-rprx-vision"` if inbound cannot be fetched.

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → DB trial record (telegram_id=0) → user clicks link in Telegram → `BindTrialSubscription` sets telegram_id, removes is_trial, sets referred_by if from invite
- **Regular:** `create_subscription` callback → XUI client (30GB, expiryTime: now+30d, reset:30) → DB record → cache invalidate → admin notify → webhook
- **Trial cookie:** `rs8kvn_trial_{code}` prevents duplication for 3 hours (HttpOnly, Secure, SameSite=Strict)
- **Atomic cleanup:** `DELETE ... RETURNING` for expired trials (prevent race with bind)
- **Share referral:** `pendingInvites[chatID]` cached 60 min (in-memory, periodic cleanup prevents leak)

### Subscription Deletion (v2.2.0+)
- **Order:** DB-first, then XUI-best-effort
- **Rationale:** If DB delete fails → XUI untouched (safe to retry). If XUI fails after DB success → orphaned client (less critical, manual cleanup).
- **Webhook:** Sent on successful DB deletion regardless of XUI outcome.
- **Referral cache:** `DecrementReferralCount` called after successful deletion.

### Subscription server (v2.3.0+)
- **Endpoint:** `GET /sub/{subID}` — subID = SubscriptionID from DB
- **DB fetch:** `GetSubscriptionWithPlanAndSources` — JOIN subscriptions+plans+plan_sources+sources
- **Cache-hit lookup:** `GetSubscriptionStatus` — cheap `SELECT status, expiry_time` (no JOIN); used to validate cached responses
- **Multi-source:** Each active Source with non-empty SubURL is fetched independently; results aggregated
- **Per-subID cache:** Raw source responses not cached; only final merged response cached 240s (`config.SubServerCacheTTL`) with response headers (Content-Type, Subscription-UserInfo, forwarded source headers) replayed verbatim
- **JSON config conversion:** Server configs from 3x-ui JSON are converted to share links via `ConvertJSONToShareLinks` (vless, vmess, trojan, ss, socks, hysteria, tuic). Pure-JSON mode returns raw objects as JSON array
- **Devices:** `Subscription.devices` — JSON array of `[{header_map}, ...]`, rotated by x-hwid value
- **IPs:** `Subscription.ips` — JSON array of `{"ip": "timestamp"}`, max 100 entries, LRU eviction
- **Userinfo aggregation:** `upload`/`download` summed across sources, `total` = `Plan.TrafficLimit`, `expire` = minimum across sources
- **Cache freshness:** `Plan.TrafficLimit` and the `Sources` list are baked into the cached response — changes to a subscription's plan or source set take up to TTL (240s) to propagate to clients. Status/expiry are re-checked on every hit
- **Cache:** 240s TTL (`config.SubServerCacheTTL`), per-subID (merged response with headers). Singleflight removed
- **Security:** All error responses return generic 404 "Subscription not found" (no info leakage). DB errors on cache-hit path also return 404 (sub likely deleted)
- **Headers forwarded:** Original response headers from first source (profile-title, profile-update-interval, routing-*) forwarded except transport headers (content-*, transfer-encoding). Subscription-UserInfo always overrides
- **Rate limiting:** None — 240s cache TTL mitigates abuse

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
- **Required:** `TELEGRAM_BOT_TOKEN`, `XUI_API_TOKEN` (NO defaults)
- **Validated:**
  - `XUI_SUB_PATH` — only `a-zA-Z0-9_-`, no `..` or `/`
  - `XUI_HOST` — must be valid URL, **HTTPS enforced** (except localhost)
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
- **Input validation:** Path traversal checks (XUI_SUB_PATH), regex invite codes
- **IP spoofing:** X-Forwarded-For trusted only from loopback (127.0.0.1, ::1). Private IPs NOT trusted.
- **API auth:** Timing-safe token comparison via `crypto/subtle.ConstantTimeCompare`
- **No secrets in code:** `.env` only, `.env.example` has placeholders
- **HTTP timeouts:** ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s, IdleTimeout 60s
- **Port binding:** Verified before goroutine launch — `net.Listen()` then `Serve()` in separate goroutine
- **Non-root Docker:** UID 1000, `no-new-privileges:true`
- **RetryWithBackoff:** 3 retries with exponential backoff + jitter; DNS errors fast-fail

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
go build -ldflags="-s -w -X main.version=v3.0.0 -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) -X main.buildTime=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" -o rs8kvn_bot ./cmd/bot

# Run linters
golangci-lint run ./...
gosec ./...

# Run locally
go run ./cmd/bot
```
