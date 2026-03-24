# Handover Summary - TGVPN Go Bot

**Version:** v1.9.2
**Github:** https://github.com/kereal/rs8kvn_bot

## Architecture

```
tgvpn_go/
├── cmd/bot/main.go              # Entry point, command routing, graceful shutdown
├── internal/
│   ├── bot/handlers.go          # Telegram handlers, subscription logic, admin commands
│   ├── xui/client.go            # 3x-ui API: Login, AddClient, DeleteClient, GetClientTraffic
│   ├── database/database.go     # GORM models, CRUD, package-level funcs
│   ├── config/
│   │   ├── config.go            # Env config loader with validation
│   │   └── constants.go         # All constants (backup 14d, log 10MB, etc.)
│   ├── logger/logger.go         # Zap structured logging
│   ├── ratelimiter/ratelimiter.go # Token bucket rate limiter
│   ├── backup/backup.go         # Daily DB backup scheduler
│   ├── heartbeat/heartbeat.go   # Health check monitoring
│   └── utils/uuid.go            # UUID v4, SubID generation
└── data/                        # Runtime data (tgvpn.db, bot.log, backups)
```

## Stack

- **Go 1.24+**
- **Telegram Bot API:** `github.com/go-telegram-bot-api/telegram-bot-api/v5`
- **ORM:** `gorm.io/gorm` + `gorm.io/driver/sqlite`
- **Logging:** `go.uber.org/zap` + `lumberjack` (rotation)
- **Errors:** `github.com/getsentry/sentry-go`
- **Database:** SQLite (`./data/tgvpn.db`)

## Current State (v1.9.0)

**Bot Commands:**
- `/start` - Greeting + main menu (InlineKeyboard)
- `/help` - Help text
- `/lastreg` - Last 10 registrations (admin only)
- `/del <id>` - Delete subscription by DB ID (admin only)
- `/broadcast <msg>` - Send message to all users (admin only)
- `/send <id|username> <msg>` - Send message to specific user (admin only)

**Inline Keyboard (v1.9.0 - replaces ReplyKeyboard):**

For users WITH subscription:
- **☕ Донат** - Shows donation text with T-Bank link
- **📋 Подписка** - Shows subscription info with traffic usage
- **❓ Помощь** - Shows VPN usage instructions

For users WITHOUT subscription:
- **📥 Получить подписку** - Create a new subscription

Admin also sees:
- **📊 Стат** - View bot statistics

**All submenus have "🏠 В начало" button to return to main menu.**

**Key UI Changes:**
- Buttons are INLINE (under the message), NOT at bottom of screen
- No keyboard flickering or auto-focus issues
- Messages are EDITED in place when navigating (not deleted + new)

**Features:**
- VLESS+Reality+Vision subscription creation
- Real traffic usage display from 3x-ui panel
- Loading indicator ("⏳ Загрузка...")
- Automatic rollback on DB save failure
- Monthly auto-renewal (reset=31)
- Link previews disabled for ALL messages

## Recent Changes (v1.9.0)

### 1. Replaced ReplyKeyboard with InlineKeyboard
- Removed ReplyKeyboard (3 buttons at bottom of screen)
- Added InlineKeyboard (buttons under message)
- Benefits: No keyboard flickering, no auto-focus on input field

### 2. New Menu Handlers
- `handleMenuDonate(chatID, username, messageID)` - Shows donate info with back button
- `handleMenuSubscription(chatID, username, messageID)` - Shows subscription info with back button
- `handleMenuHelp(chatID, username, messageID)` - Shows help with back button
- All use message EDITING (not new messages)

### 3. Removed Old Handlers
- Deleted: `HandleDonate()`, `HandleMySubscriptionButton()`, `HandleHelpButton()`
- These were for ReplyKeyboard which no longer exists

### 4. Updated Callback Handling
- Added callbacks: `menu_donate`, `menu_subscription`, `menu_help`
- `back_to_start` now edits message instead of delete+send new

### 5. DisableWebPagePreview Added Everywhere
- All `EditMessageText` now have `DisableWebPagePreview = true`
- Prevents link preview blocks in Telegram

### 6. Code Cleanup
- Removed ~130 lines of unused ReplyKeyboard handlers
- Removed ~120 lines of obsolete tests
- Updated test names and comments

## Critical Details

### 3x-ui API:
- `GET /panel/api/inbounds/getClientTraffics/{email}` - Returns **single object**
- Session validity: 15 minutes
- Auto re-login with exponential backoff

### ClientTraffic struct:
```go
type ClientTraffic struct {
    ID         int    `json:"id"`
    InboundID  int    `json:"inboundId"`
    Enable     bool   `json:"enable"`
    Email      string `json:"email"`
    UUID       string `json:"uuid"`
    SubID      string `json:"subId"`
    Up         int64  `json:"up"`
    Down       int64  `json:"down"`
    AllTime    int64  `json:"allTime"`
    ExpiryTime int64  `json:"expiryTime"`
    Total      int64  `json:"total"`
    Reset      int    `json:"reset"`
    LastOnline int64  `json:"lastOnline"`
}
```

### Message Flow (v1.9.0):
1. User sends `/start` or clicks "🏠 В начало"
2. Bot shows message with InlineKeyboard (3 menu buttons)
3. User clicks button → callback received
4. Bot EDITS the message (same messageID) with new content + back button
5. User clicks "🏠 В начало" → message edited back to main menu

### Database:
- Package-level functions: `GetAllTelegramIDs()`, `GetByTelegramID()`, `CreateSubscription()`
- SQLite with soft deletes (`deleted_at` column)

### Config Defaults:
- `TRAFFIC_LIMIT_GB=100`
- Backup retention: 14 days
- Log max size: 10MB
- Rate limiter: 30 tokens max, 5/sec refill

### Key Functions:
- `getMainMenuKeyboard()` - Returns InlineKeyboard with 3 buttons
- `getBackKeyboard()` - Returns InlineKeyboard with "🏠 В начало"
- `send()` - Sends message with DisableWebPagePreview=true, saves messageID
- `setLastMessageType()` - Tracks what type of message is displayed

## Current Task

**Status:** v1.9.0 released. All features working correctly.

**Completed:**
- InlineKeyboard UI (no flickering)
- Message type tracking (prevent duplicate updates)
- Code cleanup and tests

**Potential Next Steps:**
1. Donations - Telegram Stars, CryptoBot, or hybrid
2. Monitoring - Add metrics endpoint
3. Multi-language support
4. Subscription sharing prevention

## Tests

```bash
go test ./... -v
```
All tests pass.

## Deploy

```bash
git add -A && git commit -m "message"
git tag -a v1.x.x -m "version"
git push && git push --tags
```
Auto Docker build + GitHub Release via CI/CD.

## Admin Notification Format

When subscription created, admin receives:
- Username, Telegram ID
- Expiry date
- Full subscription URL