# rs8kvn_bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

## Project Purpose
This is a Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features
- Get subscription on demand
- View current subscription status  
- QR code for easy subscription import
- Invite/trial landing page with one-click setup
- Referral system
- Configurable traffic limit (default 100GB/month)
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

### Git Workflow Skill
Project includes `.agents/skills/git-workflow-skill/` with best practices for:
- Conventional Commits (`feat:`, `fix:`, `docs:`, etc.)
- Branch naming conventions (`feature/TICKET-123-desc`)
- Pull Request workflow
- Release management

### Available Tools
- `git` - version control
- `gh` CLI (v2.46.0) - GitHub operations
- `golangci-lint` - linting
- `go` (v1.25.0) - Go toolchain
