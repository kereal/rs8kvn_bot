# 🔄 Handover Summary - TGVPN Go Bot

## 📁 Архитектура

```
tgvpn_go/
├── cmd/bot/main.go              # Entry point, graceful shutdown, backup/heartbeat schedulers
├── internal/
│   ├── bot/handlers.go          # Telegram commands, subscription creation with rollback
│   ├── xui/client.go            # 3x-ui HTTP API client, login, AddClient, DeleteClient
│   ├── database/database.go     # GORM models, migrations, CRUD, Service struct (DI)
│   ├── config/
│   │   ├── config.go            # Environment config loader with validation
│   │   └── constants.go         # All project constants (NEW)
│   ├── logger/logger.go         # Zap structured logging, Service struct (DI)
│   ├── ratelimiter/ratelimiter.go # Token bucket, optimized wait calculation
│   ├── backup/backup.go         # Daily DB backup scheduler
│   ├── heartbeat/heartbeat.go   # Health check monitoring
│   └── utils/uuid.go            # Crypto/rand UUID v4 generation (FIXED)
├── .github/workflows/docker.yml # CI/CD: test, Docker build, auto-release
├── Dockerfile                   # Multi-stage build, Alpine 3.20, UPX compression
├── docker-compose.yml           # Production config with security hardening
└── docker-compose.local.yml     # Local development config
```

**Data Flow:**
1. Telegram → `bot/handlers.go` → command/callback handling
2. Create subscription → `xui/client.go` → add client to 3x-ui
3. Save to DB → `database/database.go` (SQLite + GORM)
4. If DB save fails → **rollback** (delete client from 3x-ui)
5. Logging → Zap (console + file rotation) + Sentry (errors)

## 🛠 Стек

**Go 1.25**

**Dependencies:**
- `telegram-bot-api/v5` - Telegram Bot API
- `gorm.io/gorm` + `gorm.io/driver/sqlite` - ORM, SQLite
- `go.uber.org/zap` - Structured logging
- `gopkg.in/natefinch/lumberjack.v2` - Log rotation
- `github.com/joho/godotenv` - .env loader
- `github.com/getsentry/sentry-go` - Error tracking
- `github.com/google/uuid` - Proper UUID v4 (NEW)

**Database:** SQLite (`./data/tgvpn.db`)

## ✅ Текущее состояние

**Реализовано и работает:**

1. **Telegram Bot:**
   - `/start`, `/help` commands
   - Inline keyboard: get subscription, my subscription, admin stats
   - Context propagation во всех handlers

2. **Subscriptions:**
   - ✅ VLESS+Reality+Vision creation
   - ✅ Automatic rollback при ошибке БД (NEW)
   - ✅ Monthly auto-renewal (reset=31)
   - ✅ Traffic limit: 100GB/month (configurable)

3. **3x-ui Integration:**
   - ✅ Login with session (15 min validity)
   - ✅ Auto re-login with exponential backoff
   - ✅ AddClient API
   - ✅ DeleteClient API для rollback (NEW)

4. **Infrastructure:**
   - ✅ Graceful shutdown
   - ✅ Daily backup (03:00, 7 days retention)
   - ✅ Heartbeat monitoring
   - ✅ Sentry error tracking
   - ✅ Rate limiting (optimized algorithm)

5. **Docker:**
   - ✅ Multi-stage build (Go 1.25, Alpine 3.20)
   - ✅ UPX binary compression
   - ✅ Non-root user
   - ✅ Memory optimization (GOMEMLIMIT, GOGC)
   - ✅ Security hardening (no-new-privileges)

6. **CI/CD:**
   - ✅ GitHub Actions workflow
   - ✅ Auto test on push
   - ✅ Auto Docker build on tag
   - ✅ Auto GitHub Release on tag (NEW)

## 📝 Последние изменения

### CRITICAL FIXES:
1. **UUID Generation** - Changed from timestamp-based to crypto/rand UUID v4 (prevents collisions)
2. **Race Condition Fix** - Added automatic rollback: if DB save fails, client is deleted from 3x-ui panel
3. **Context Propagation** - All public methods now accept `context.Context`

### ARCHITECTURE:
4. **Constants File** - Created `internal/config/constants.go` (all magic numbers → named constants)
5. **Rate Limiter** - Optimized: removed busy-waiting, calculates exact wait time
6. **DI Preparation** - Added Service structs to database and logger packages

### SECURITY:
7. **Sensitive Data Masking** - `Config.String()` masks passwords/tokens, `maskSubscriptionURL()` hides subscription IDs
8. **Config Validation** - Full validation: URL format, token format, ranges, log levels
9. **Admin Access Check** - Added verification in admin stats handler

### TESTING:
10. **+43 New Tests** - Added comprehensive tests:
    - database: 24.7% → 54.7% (+30.0%)
    - logger: 37.7% → 75.5% (+37.8%)
    - bot: 7.2% → 18.7% (+11.5%)
11. **All Tests Passing** - 100% success rate

### DOCKER:
12. **Alpine 3.20** - Updated from 3.23 for stability
13. **Build Arguments** - VERSION, COMMIT_SHA, BUILD_TIME for versioning
14. **Security Hardening** - `user: "1000:1000"`, `no-new-privileges:true`
15. **Memory Fix** - Fixed conflict between Docker memory limit and GOMEMLIMIT
16. **Logging Config** - Added max-size/max-file to prevent disk overflow

### CI/CD:
17. **Auto Release** - Added `ncipollo/release-action` for automatic GitHub Release creation
18. **Changelog Generation** - Auto-generates from git commits

### COMMITS:
- `d4ffa0d` - Main refactoring commit (21 files, 2963 insertions, 425 deletions)
- `d3e5a37` - CI/CD update (auto-release)

## ⚠️ Критичные нюансы

### Business Logic:

1. **Atomic Subscription Creation:**
   - Generate clientID + subID
   - Add client to 3x-ui panel
   - Save to DB
   - **If DB fails → delete client from 3x-ui (rollback)**
   - **If rollback fails → notify admin about orphan client**

2. **Subscription Lifecycle:**
   - Only one active subscription per user
   - Old subscription gets `status="revoked"` on new creation
   - Auto-renewal: `reset=31` (monthly)

3. **3x-ui API Quirks:**
   - Session valid 15 minutes
   - May return `success=false` with `msg="successfully"` → check message content
   - Endpoint: `/panel/api/inbounds/addClient`

### Configuration:

**Required env vars:**
```env
TELEGRAM_BOT_TOKEN=123456:token  # Must contain ":"
XUI_HOST=http://panel:2053       # Must include scheme
XUI_USERNAME=admin
XUI_PASSWORD=secret
XUI_INBOUND_ID=1                  # Must be >= 1
```

**Important defaults:**
- `TRAFFIC_LIMIT_GB=100` (1-1000 range)
- `DATABASE_PATH=./data/tgvpn.db`
- `GOMEMLIMIT=67108864` (64MB)
- `GOGC=50` (aggressive GC)

### Database Schema:

**Subscriptions table:**
- Unique constraint: `(telegram_id, status)` where status='active'
- Status values: `active`, `revoked`, `expired`
- Soft deletes enabled (`deleted_at`)
- Helper methods: `IsExpired()`, `IsActive()`

### Memory Optimization:

- Single DB connection (SQLite limitation)
- HTTP transport: `MaxIdleConns=1`
- GORM `PrepareStmt=false`
- Connection pool: lifetime 5min, idle 2min
- Memory limit: 64MB soft + 128MB hard (Docker)

### CI/CD Pipeline:

**Triggers:**
- Push to `main` → run tests
- Push tag `v*` → run tests + build Docker + create release

**Tag naming:**
- Must start with `v` (e.g., `v1.7.0`)
- Semantic versioning recommended

**Permissions needed:**
- `contents: write` - for creating releases
- `packages: write` - for pushing Docker images

### Rollback Mechanism:

```go
// In createSubscription()
if err := database.CreateSubscription(sub); err != nil {
    logger.Errorf("DB save failed: %v", err)
    
    // Rollback: remove from 3x-ui
    if rollbackErr := h.xui.DeleteClient(ctx, inboundID, clientID); rollbackErr != nil {
        h.notifyAdminError(ctx, "ORPHAN CLIENT: " + clientID)
    }
    return
}
```

### Testing:

**Run all tests:**
```bash
go test ./... -v -race -coverprofile=coverage.out
```

**Coverage report:**
```bash
go tool cover -html=coverage.out
```

### Docker Commands:

**Build local:**
```bash
docker compose -f docker-compose.local.yml build
```

**Run local:**
```bash
docker compose -f docker-compose.local.yml up -d
```

**View logs:**
```bash
docker compose -f docker-compose.local.yml logs -f
```

**Production:**
```bash
docker compose up -d
```
