# Handover Summary - TGVPN Go Bot

**Version:** v1.8.10
**Github:** https://github.com/kereal/rs8kvn_bot

## Architecture

```
tgvpn_go/
├── cmd/bot/main.go              # Entry point, command routing, graceful shutdown
├── internal/
│   ├── bot/handlers.go          # Telegram handlers, subscription logic, admin commands
│   ├── xui/client.go            # 3x-ui API: Login, AddClient, DeleteClient, GetClientTraffic
│   ├── database/database.go     # GORM models, CRUD, package-level funcs + Service methods
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

## Current State (v1.8.10)

**Bot Commands:**
- `/start` - Greeting + deep link `?start=donate` for donation info
- `/help` - Help text
- `/lastreg` - Last 10 registrations (admin only)
- `/del <id>` - Delete subscription by DB ID (admin only)
- `/broadcast <msg>` - Send message to all users (admin only)
- `/send <id|username> <msg>` - Send message to specific user (admin only)

**Reply Keyboard (3 buttons, shown only if user has subscription):**
- "☕ Донат" - Shows donation text with T-Bank link
- "📋 Подписка" - Shows subscription info with traffic usage
- "❓ Помощь" - Shows VPN usage instructions

**Features:**
- VLESS+Reality+Vision subscription creation
- Real traffic usage display from 3x-ui panel
- Loading indicator ("⏳ Загрузка..." message, then edited to content)
- Automatic rollback on DB save failure
- Monthly auto-renewal (reset=31)
- Link previews disabled for all messages (DisableWebPagePreview = true)
- "🏠 В начало" button after subscription creation (executes /start)

## Recent Changes (v1.8.10)

1. **Reply Keyboard** - 3 buttons: "☕ Донат", "📋 Подписка", "❓ Помощь"
   - Shown only when user has active subscription
   - Replaced inline buttons "Получить подписку" and "Моя подписка" in main menu

2. **"🏠 В начало" Button** - Inline button after subscription creation
   - Uses zero-width space (`\u200B`) as invisible text
   - Callback `back_to_start` executes HandleStart

3. **Link Previews Disabled** - `DisableWebPagePreview = true` everywhere:
   - `send()` method
   - `sendWithRetry()` method
   - `HandleBroadcast()`
   - `HandleSend()`
   - `handleMySubscription()` EditMessageText
   - `createSubscription()` EditMessageText

4. **Loading Indicator** - "⏳ Загрузка..." message when creating subscription
   - Message sent first, then edited to show subscription instructions
   - Same behavior as "Подписка" button

5. **Typing Indicator Removed** - Removed ChatTyping action and delays

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

### Database:
- Package-level functions: `GetAllTelegramIDs()`, `GetTelegramIDByUsername()`
- Service methods exist for DI but not used in handlers

### Config Defaults:
- `TRAFFIC_LIMIT_GB=100`
- Backup retention: 14 days
- Log max size: 10MB

### Message Sending:
- All messages use `DisableWebPagePreview = true`
- EditMessageText also requires `DisableWebPagePreview = true` for link control

### Reply Keyboard Logic:
- Only shown when user has active subscription
- If no subscription: show inline button "📥 Получить подписку"
- Admin always sees "📊 Статистика" button

## Current Task

**Status:** All requested features implemented and tested. Version v1.8.10 released.

**Next Steps (if needed):**
1. Donations - Discussed options: Telegram Stars, CryptoBot API, hybrid approach
2. Monitoring - Add metrics/metrics endpoint
3. Tests - All tests pass

## Tests

```bash
go test ./... -v
```
All tests pass.

## Deploy

```bash
git tag v1.x.x && git push --tags
```
Auto Docker build + GitHub Release via CI/CD.

## Admin Notification Format

When subscription created, admin receives:
- Username, Telegram ID
- Expiry date
- Full subscription URL