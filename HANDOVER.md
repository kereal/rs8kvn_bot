# Handover Summary - TGVPN Go Bot

**Version:** v2.0.0
**Github:** https://github.com/kereal/rs8kvn_bot

## Architecture

```
tgvpn_go/
├── cmd/bot/main.go              # Entry point, command routing, graceful shutdown
├── internal/
│   ├── bot/
│   │   ├── handler.go           # Handler struct, helper functions
│   │   ├── callbacks.go         # Callback routing
│   │   ├── commands.go          # Command handlers (/start, /help)
│   │   ├── subscription.go      # Subscription logic, QR code
│   │   ├── menu.go              # Menu handlers (back, donate, help)
│   │   ├── admin.go             # Admin commands
│   │   └── message.go           # Message sending utilities
│   ├── xui/client.go            # 3x-ui API client
│   ├── database/database.go     # GORM models, CRUD operations
│   ├── config/                  # Configuration loader, constants
│   ├── logger/                  # Zap structured logging
│   ├── utils/
│   │   ├── uuid.go              # UUID generation
│   │   ├── time.go              # Time utilities
│   │   └── qr.go                # QR code generation (NEW)
│   ├── backup/                  # Daily DB backup
│   ├── heartbeat/               # Health monitoring
│   └── ratelimiter/             # Token bucket rate limiter
└── data/                        # Runtime data (db, logs, backups)
```

## Stack

- **Go 1.25+**
- **Telegram Bot API:** `github.com/go-telegram-bot-api/telegram-bot-api/v5`
- **ORM:** `gorm.io/gorm` + `gorm.io/driver/sqlite`
- **QR Code:** `github.com/piglig/go-qr` (NEW)
- **Logging:** `go.uber.org/zap` + `lumberjack`
- **Errors:** `github.com/getsentry/sentry-go`
- **Database:** SQLite (`./data/tgvpn.db`)

## Current State (v2.0.0)

### Bot Commands
- `/start` - Greeting + main menu
- `/help` - Help text
- `/del <id>` - Delete subscription (admin)
- `/broadcast <msg>` - Broadcast message (admin)
- `/send <id|user> <msg>` - Send to user (admin)

### User Flow
```
/start → Main Menu
         ├── [📋 Подписка] → Subscription info + [📱 QR-код] [🏠 В начало]
         │                              ↓
         │                         [📱 QR-код] → QR photo + [⬅️ Назад]
         │                              ↓
         │                         [⬅️ Назад] → Delete QR, subscription visible
         │
         ├── [☕ Донат] → Donation info + [🏠 В начало]
         │
         └── [❓ Помощь] → Instructions + [🏠 В начало]
```

### Keyboard Layout (IMPORTANT)
- QR and "В начало" buttons are on **SEPARATE ROWS**
- QR keyboard has only "⬅️ Назад" button (callback: `back_to_subscription`)

### All Callbacks
```go
"create_subscription"   // Create/get subscription
"qr_code"               // Generate QR code
"back_to_subscription"  // Close QR, return to subscription
"menu_subscription"     // Show subscription info
"menu_donate"           // Show donation info
"menu_help"             // Show help
"back_to_start"         // Return to main menu (edits message)
"admin_stats"           // Admin statistics
"admin_lastreg"         // Last registrations
```

## Recent Changes (v2.0.0)

### 1. QR Code Feature
- Added `utils/qr.go` with `GenerateQRCodePNG()` function
- QR code generated in memory (512px), sent as photo
- Button "⬅️ Назад" deletes QR, subscription message stays visible
- UX: Modal-style QR overlay over subscription

### 2. Code Refactoring
- **`getMainMenuContent()`** - Returns text+keyboard for main menu (handler.go)
- **`showLoadingMessage()`** - Shows loading, returns messageID (handler.go)
- Removed ~100 lines of duplicate code
- Unified callback name: `create_subscription` (was `get_subscription`)

### 3. Message Flow Improvements
- QR photo is sent as NEW message (subscription stays visible)
- "back_to_subscription" DELETES QR photo only
- "back_to_start" EDITS message (instant, no delete delays)
- No more "message disappearing" delays

### 4. Logging Added
- Non-command messages now logged in main.go:
  ```go
  logger.Info("Received non-command message",
      zap.Int64("chat_id", ...),
      zap.String("username", ...),
      zap.String("text_preview", ...))  // First 50 chars
  ```

### 5. Tests Updated
- Fixed `TestCallbackQueryData` - updated callback list
- Added 6 new tests for helper functions and keyboard layouts
- Fixed pointer comparisons for `*string` CallbackData

## Critical Details

### QR Code Flow
```
1. User clicks [📱 QR-код]
2. Bot sends QR photo (NEW message) with [⬅️ Назад]
3. Subscription message remains VISIBLE above QR
4. User clicks [⬅️ Назад]
5. Bot DELETES QR photo
6. Subscription message is already visible
```

### Why Not Delete+Send?
- Telegram API delete → send causes visible delays
- User sees "message disappearing" effect
- Solution: Send new first, let user see content immediately
- QR is "modal" over subscription, closing it reveals subscription

### Helper Functions (handler.go)

```go
// Returns main menu text and keyboard
func (h *Handler) getMainMenuContent(username string, hasSubscription bool, chatID int64) (string, tgbotapi.InlineKeyboardMarkup)

// Shows loading message, returns messageID for subsequent edits
func (h *Handler) showLoadingMessage(chatID int64, messageID int) int
```

### 3x-ui API
- Session validity: 15 minutes
- Auto re-login with exponential backoff
- `GetClientTraffic()` returns up/down bytes for user

### Database
- SQLite with soft deletes (`deleted_at`)
- `Subscription` model: TelegramID, Username, ClientID, SubscriptionURL, ExpiryTime, Status

### Config
- `TRAFFIC_LIMIT_GB=100` (default)
- Backup retention: 14 days
- Rate limiter: 30 tokens, 5/sec refill

## Current Task

**Status:** v2.0.0 ready. Tests fixed and new tests added.

**Pending Verification:**
```bash
cd /home/kereal/tgvpn_go
go mod tidy
go test ./...
go build ./...
```

**Next Steps:**
1. Run tests and build to verify everything works
2. Deploy to production
3. Test QR code flow in real Telegram

## Business Context

- **Target:** Russian users needing VPN for blocked services
- **Legal:** VPN advertising prohibited in Russia → word-of-mouth only
- **Protocol:** VLESS+Reality+Vision (undetectable by DPI)
- **Payment:** T-Bank donations, planning CryptoBot integration

## Files Modified in Last Session

| File | Changes |
|------|---------|
| `go.mod` | Added `piglig/go-qr` dependency |
| `internal/utils/qr.go` | NEW - QR code generation |
| `internal/bot/handler.go` | Added `getMainMenuContent()`, `showLoadingMessage()` |
| `internal/bot/subscription.go` | QR code handler, refactored loading |
| `internal/bot/menu.go` | Refactored `handleBackToStart()` |
| `internal/bot/callbacks.go` | Added `back_to_subscription`, unified `create_subscription` |
| `cmd/bot/main.go` | Added non-command message logging |
| `internal/bot/handlers_test.go` | Fixed callbacks, added 6 new tests |

## Quick Reference

```bash
# Run tests
go test ./... -v

# Build
go build -o rs8kvn_bot ./cmd/bot

# Deploy
git add -A && git commit -m "v2.0.0: QR code feature"
git tag -a v2.0.0 -m "QR code support"
git push && git push --tags
```
