# rs8kvn_bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

## Project Purpose
This is a Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features
- Get subscription on demand
- View current subscription status (via service layer, single source of truth)
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
- **O(1) LRU subscription cache** (container/list, RLock for concurrent reads)
- **Subscription status check in /sub/{subID}** — revoked/expired subscriptions return 404
- **Unified soft delete for all subscription deletions** (GORM `deleted_at`)
- **ExpiryTime saved in DB on Create** — admin sees actual reset date
- **Merged referral cache** (counts + dirty in one map)
- **pendingInvites periodic cleanup** — prevents memory leak from expired share-link entries
- **MarkdownV2 proper escaping** — backslash-first escaping prevents double-escape and broken formatting
- **Broadcast 5min timeout** — handles thousands of users without early termination
- **Subscription plan field** — `plan` column on subscriptions (free/basic/premium/vip), exposed in API and webhooks
- **Admin /plan command** — `/plan @username <plan>` to change user's plan (looks up by username)

## Tech Stack
- **Language**: Go 1.25 (проект всегда был на Go, никогда не был на Python)
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
- `internal/webhook/` - Webhook sender for Proxy Manager
- `internal/heartbeat/` - Heartbeat monitoring
- `internal/backup/` - Database backup functionality
- `internal/ratelimiter/` - Rate limiting logic
- `internal/web/` - Web endpoints (health, invite/trial, subscription proxy, API)
- `internal/subproxy/` - Subscription proxy (cache, merge, extra config, InvalidateCache for status changes)
- `internal/interfaces/` - Interface definitions
- `internal/testutil/` - Test utilities and mocks

## Subscription Plan System (v2.3.0)

### Plan Field
- **Field**: `plan TEXT NOT NULL DEFAULT 'free'` on `subscriptions` table
- **Migration**: `005_add_plan_column.up.sql`
- **Constants**: `database.PlanFree`, `database.PlanBasic`, `database.PlanPremium`, `database.PlanVIP`
- **Validation**: `database.ValidPlans` map

### API
- `GET /api/v1/subscriptions` — returns `plan` field per subscription

### Webhook Events
- All events (`subscription.activated`, `subscription.expired`) include `plan` field

### Admin Command
- `/plan @username <plan>` — changes plan by username lookup, invalidates cache
- Example: `/plan @user premium`, `/plan @testuser vip`
- Strips @ prefix automatically, both `/plan user premium` and `/plan @user premium` work

## Development Workflow

### Terminal Tool Usage
**Important**: When using the `terminal` tool, use the basename of the root directory as `cd` parameter:
- ✅ Correct: `cd: "rs8kvn_bot"` 
- ❌ Wrong: `cd: "/home/kereal/rs8kvn_bot"` (causes worktree error)

### Git Workflow
**ВАЖНО: Всегда использовать Pull Requests!**
- **Main branch**: Только через PR, никаких прямых коммитов в main
- **Feature branches**: `feature/description`, `fix/description`
- **Workflow**: Создать ветку → Изменения → Push → PR → Review → Merge
- **Подробности**: См. память `git-workflow`

**При старте работы ИИ агент ДОЛЖЕН:**
1. Активировать проект Serena: `activate_project("rs8kvn_bot")`
2. Проверить onboarding: `check_onboarding_performed()`
3. Прочитать памяти: `git-workflow`, `project_overview`, `code_style`

### ⚠️ .gitignore Issue
The `.gitignore` has `bot` pattern that matches `internal/bot/` directory.
When adding new files to `internal/bot/`, use `git add -f internal/bot/newfile.go`
Some Serena tools (replace_content, insert_after_symbol) cannot access files in `internal/bot/` — use `edit_file` or `terminal` instead.
