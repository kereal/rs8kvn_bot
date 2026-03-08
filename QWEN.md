# TGVPN Go - Project Context

## Project Overview

**TGVPN Go** is a Telegram bot that distributes VLESS+Reality+Vision proxy subscriptions from a 3x-ui panel. The bot allows users to request and manage proxy subscriptions with automatic traffic limits and monthly renewal.

### Key Features
- On-demand subscription generation via Telegram inline buttons
- 100GB traffic limit per month (configurable)
- Auto-renewal on the last day of each month
- Admin notifications for new subscriptions
- SQLite database for persistence
- Daily automated database backups at 3 AM
- Docker support with multi-architecture builds (amd64, arm64)

### Architecture
- **Language**: Go 1.21+
- **Database**: SQLite with GORM ORM
- **Telegram API**: `go-telegram-bot-api/v5`
- **Logging**: Uber Zap with lumberjack rotation
- **External Integration**: 3x-ui panel HTTP API

## Project Structure

```
tgvpn_go/
├── cmd/bot/
│   └── main.go              # Application entry point, signal handling, backup scheduler
├── internal/
│   ├── config/
│   │   └── config.go        # Environment configuration loading and validation
│   ├── database/
│   │   └── database.go      # GORM models, migrations, CRUD operations
│   ├── logger/
│   │   └── logger.go        # Zap logger with file rotation (lumberjack)
│   ├── xui/
│   │   └── client.go        # 3x-ui API client (login, add/remove clients)
│   ├── bot/
│   │   └── handlers.go      # Telegram bot command/callback handlers
│   ├── ratelimiter/
│   │   └── ratelimiter.go   # Token bucket rate limiter for API calls
│   └── backup/
│       └── backup.go        # Database backup and rotation utilities
├── .env.example             # Environment template
├── Dockerfile               # Multi-stage build (builder + runtime)
├── docker-compose.yml       # Docker Compose configuration
├── go.mod                   # Go module definition
└── .github/workflows/
    └── docker.yml           # CI/CD: Build and push to GHCR
```

## Building and Running

### Prerequisites
- Go 1.21+ (for local development)
- Docker & Docker Compose (recommended for deployment)
- 3x-ui panel instance
- Telegram Bot Token from @BotFather

### Environment Configuration

Copy `.env.example` to `.env` and configure:

```env
# Required
TELEGRAM_BOT_TOKEN=your_bot_token
XUI_HOST=http://your-panel-ip:2053
XUI_USERNAME=admin
XUI_PASSWORD=your_password
XUI_INBOUND_ID=1

# Optional (with defaults)
TELEGRAM_ADMIN_ID=0          # Admin ID for notifications
XUI_SUB_PATH=sub
DATABASE_PATH=./data/tgvpn.db
LOG_FILE_PATH=./data/bot.log
LOG_LEVEL=info
TRAFFIC_LIMIT_GB=100
```

### Local Development

```bash
# Install dependencies
go mod tidy

# Run directly
go run ./cmd/bot

# Build optimized binary
go build -ldflags="-s -w" -o tgvpn_bot ./cmd/bot

# Run binary
./tgvpn_bot
```

### Docker Deployment

```bash
# Using Docker Compose (recommended)
docker-compose up -d

# View logs
docker-compose logs -f

# Using Docker directly
docker run -d \
  --name tgvpn_bot \
  --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  ghcr.io/USERNAME/tgvpn_go:latest
```

### Testing

No formal test suite exists. Manual testing involves:
1. Starting the bot
2. Sending `/start` command in Telegram
3. Testing inline buttons: "Получить подписку", "Моя подписка"

## Development Conventions

### Code Style
- Standard Go formatting (`gofmt`)
- Package-level organization under `internal/`
- Error handling with wrapped context using `fmt.Errorf("%w", err)`
- Context-aware operations for cancellation support

### Logging
- Uses Zap structured logging with console encoder
- Log levels: debug, info, warn, error, fatal
- Dual output: console (stdout) + rotating file (lumberjack)
- File rotation: 10MB max, 3 backups, 30 days retention

### Database Patterns
- GORM for ORM with SQLite
- Transactions for atomic operations (e.g., `CreateSubscription`)
- Connection pool limited to 1 (single-user app)
- Soft deletes enabled via `gorm.DeletedAt`

### API Client Patterns
- Session management with 15-minute validity
- Automatic re-login with exponential backoff
- Context-aware HTTP requests with 10s timeout
- Response size limit: 1MB

### Rate Limiting
- Token bucket algorithm in `internal/ratelimiter`
- Default: 30 tokens burst, 5 tokens/second refill
- Context-aware `Wait()` method for cancellation

### Graceful Shutdown
- Signal handling for SIGINT, SIGTERM, SIGQUIT
- Backup scheduler goroutine tracked via WaitGroup
- 30-second timeout for goroutine cleanup

## Key Configuration Values

| Variable | Default | Description |
|----------|---------|-------------|
| `TRAFFIC_LIMIT_GB` | 100 | Monthly traffic per user |
| `Expiry Time` | End of month | Subscriptions expire at 23:59:59 on last day |
| `Reset Parameter` | 31 | Auto-renewal interval in 3x-ui |
| `Backup Time` | 03:00 | Daily backup schedule |
| `Session Validity` | 15 min | 3x-ui API session duration |

## Database Schema

### Subscriptions Table
| Column | Type | Description |
|--------|------|-------------|
| `id` | uint | Primary key |
| `telegram_id` | int64 | User's Telegram ID (unique index with status) |
| `username` | string | Telegram username |
| `client_id` | string | 3x-ui client UUID |
| `xui_host` | string | Panel URL for this subscription |
| `inbound_id` | int | 3x-ui inbound ID |
| `traffic_limit` | int64 | Traffic limit in bytes |
| `expiry_time` | time.Time | Subscription expiry |
| `status` | string | active/revoked/expired |
| `subscription_url` | string | Full subscription link |
| `created_at` | time.Time | Auto-generated |
| `updated_at` | time.Time | Auto-updated |
| `deleted_at` | gorm.DeletedAt | Soft delete timestamp |

## Common Operations

### Add Debug Logging
Modify `LOG_LEVEL=debug` in `.env` to enable debug logs for 3x-ui API responses.

### Change Traffic Limit
Set `TRAFFIC_LIMIT_GB` in `.env` (valid range: 1-1000).

### Manual Backup
The `backup.DailyBackup(dbPath, keepDays)` function can be called programmatically.

### Admin Stats
Admin users (configured via `TELEGRAM_ADMIN_ID`) see an additional "📊 Статистика" button showing:
- Total users
- Active subscriptions
- Expired subscriptions
- Database record count

## CI/CD Pipeline

GitHub Actions workflow (`.github/workflows/docker.yml`):
- **Triggers**: Push to `main`, tags (`v*`), pull requests
- **Platforms**: linux/amd64, linux/arm64
- **Registry**: GitHub Container Registry (ghcr.io)
- **Tags**: Branch name, semver, commit SHA
- **Caching**: GitHub Actions cache for build layers

## Known Limitations

- Single database connection (by design for single-user scenario)
- No formal test suite
- 3x-ui session requires re-login every 15 minutes
- Subscription links use panel's configured host (no dynamic detection)
