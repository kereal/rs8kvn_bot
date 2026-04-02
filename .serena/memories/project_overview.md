# rs8kvn_bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

## Project Purpose
This is a Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features
- Get subscription on demand
- View current subscription status  
- QR code for easy subscription import
- Invite/trial landing page with one-click setup
- Referral system with in-memory cache + periodic sync
- Configurable traffic limit (default 30GB/month)
- Auto-renewal on the last day of each month
- Admin notifications on new subscriptions
- Heartbeat monitoring support
- Health check endpoint (/healthz, /readyz)
- File logging with rotation (zap)
- Daily database backups with rotation
- Database migrations system
- Sentry error tracking
- Rate limiting per user
- Graceful shutdown with goroutine tracking
- Circuit breaker for 3x-ui panel
- Donate message with card number in config (constants.go)
- Friendly and inviting donation message tone

## Tech Stack
- **Language**: Go 1.25.0
- **Bot Framework**: telegram-bot-api/v5
- **Database**: SQLite with GORM
- **Logging**: Zap
- **Testing**: testify
- **Migration**: golang-migrate/migrate/v4
- **QR Codes**: piglig/go-qr
- **Error Tracking**: getsentry/sentry-go

## Code Structure
- `cmd/bot/` - Main application entry point
- `internal/bot/` - Bot logic (handlers, commands, callbacks, menus)
- `internal/database/` - Database operations and migrations
- `internal/xui/` - 3x-ui panel client with circuit breaker
- `internal/utils/` - Utility functions (time, UUID, QR codes)
- `internal/config/` - Configuration management
- `internal/logger/` - Logging setup
- `internal/health/` - Health check endpoints
- `internal/heartbeat/` - Heartbeat monitoring
- `internal/backup/` - Database backup functionality
- `internal/ratelimiter/` - Rate limiting logic
- `internal/web/` - Web endpoints (invite/trial pages)
- `internal/interfaces/` - Interface definitions
- `internal/testutil/` - Test utilities and mocks

## Development Workflow

### Terminal Tool Usage
**Important**: When using the `terminal` tool, use the basename of the root directory as `cd` parameter:
- ✅ Correct: `cd: "tgvpn_go"` 
- ❌ Wrong: `cd: "/home/kereal/tgvpn_go"` (causes worktree error)

### Git Workflow
**Simplified workflow without Pull Requests:**
- Feature branches: `feature/description`
- Workflow: `feature/* → merge → dev → merge → main`
- Direct merge to dev (no PRs)
- Merge dev to main for releases
- Tag releases: `v2.1.0`, etc.
- Sync dev with main after release

**Commit conventions:**
- Conventional Commits (`feat:`, `fix:`, `docs:`, etc.)
- Branch naming: `feature/description`

Project includes `.agents/skills/git-workflow-skill/` with best practices.

### Available Tools
- `git` - version control
- `gh` CLI (v2.46.0) - GitHub operations
- `golangci-lint` - linting
- `go` (v1.25.0) - Go toolchain

## Recent Changes (v2.1.0)

### Referral Cache System
- **Implementation**: Full referral cache with database methods and in-memory sync
- **Admin command**: `/refstats` shows referral count per user
- **Files**: `database/database.go`, `bot/handler.go`, `bot/admin.go`, `cmd/bot/main.go`
- **Commit**: `66f5d86`

### Trial Atomic Rollback
- **Problem**: Trial creation could leave orphaned client in 3x-ui if cleanup failed
- **Solution**: `RetryWithBackoff` for rollback with up to 3 retries
- **Commit**: `d2d8c16`

### Subscription Locking
- **Problem**: Manual lock/unlock via map could deadlock on panic
- **Solution**: Replaced with `sync.Map` using `LoadOrStore`
- **Commit**: `113f2bf`

### Singleflight for XUI Login
- **Problem**: Concurrent requests after session expiry triggered multiple logins
- **Solution**: `singleflight.Group` to deduplicate concurrent login attempts
- **Commit**: `1857ae8`

### SQLite Connection Pool
- **Configuration**: Set `MaxOpenConns(1)`, `MaxIdleConns(2)`, `ConnMaxLifetime(1h)`
- **Purpose**: Prevents `database is locked` errors in SQLite

### Donate Improvements
- **Card number added to config**: `DonateCardNumber = "2200702156780864"` (T-Bank)
- **Donate constants**: `DonateURL`, `DonateContactUsername` in `internal/config/constants.go`
- **Improved donate message text**:
  - Friendly and inviting tone (no pressure)
  - Emojis: 😊 (call to action), ❤️ (gratitude)
  - Card number in code blocks for easy copying
  - Better formatting with line breaks

### Traffic Limit
- **Default traffic limit**: Changed from 100GB to 30GB (`DefaultTrafficLimitGB = 30`)

### Release Management
- **Tag**: v2.1.0 created on commit a69c7f6
- **Workflow**: Clean git history, removed empty merge commits
- **Branches**: main and dev synchronized on same commit
