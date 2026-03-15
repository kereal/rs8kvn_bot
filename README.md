# rs8kvn_bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features

- 📥 Get subscription on demand
- 📋 View current subscription status
- 📊 Configurable traffic limit (default 100GB/month)
- 📅 Auto-renewal on the last day of each month
- 🔔 Admin notifications on new subscriptions
- 💓 Heartbeat monitoring support
- 📝 File logging with rotation (zap)
- 🗄️ Daily database backups with rotation
- 🔄 Database migrations system
- 🐛 Sentry error tracking
- 🛡️ Rate limiting
- ⚡ Graceful shutdown
- 🐳 Docker support

## Requirements

- Docker & Docker Compose (recommended)
- OR Go 1.24+
- 3x-ui panel (https://github.com/MHSanaei/3x-ui)
- Telegram Bot Token

## Installation

### Option 1: Docker with GitHub Container Registry (Recommended)

#### 1. Create directory structure

```bash
mkdir -p rs8kvn_bot
cd rs8kvn_bot
```

#### 2. Configure environment

Copy `.env.example` to `.env` and fill in your values:

```bash
cp .env.example .env
```

Edit `.env` with your configuration. See the Configuration section below for all available options.

#### 3. Get your Telegram Admin ID

Send a message to @userinfobot on Telegram to get your ID.

#### 4. Configure 3x-ui Panel

1. Create a VLESS+Reality+Vision inbound in your 3x-ui panel
2. Note the inbound ID (shown in the inbounds list)
3. Make sure the panel API is accessible from the bot

#### 5. Create data directory

```bash
mkdir -p data
chmod 755 data
```

#### 6. Run with Docker

```bash
# Pull from GitHub Container Registry
docker pull ghcr.io/kereal/rs8kvn_bot:latest

# Run container
docker run -d \
  --name rs8kvn_bot \
  --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  ghcr.io/kereal/rs8kvn_bot:latest
```

#### 6. View logs

```bash
docker logs -f rs8kvn_bot
```

#### 7. Stop/Start

```bash
docker stop rs8kvn_bot
docker start rs8kvn_bot
```

### Option 2: Docker Compose

#### 1. Create data directory

```bash
mkdir -p data
chmod 755 data
```

#### 2. Run with docker-compose

```bash
docker-compose up -d
```

#### 3. View logs

```bash
docker-compose logs -f
```

### Option 3: Build from Source

#### 1. Clone and install dependencies

```bash
cd rs8kvn_bot
go mod tidy
```

#### 2. Configure environment

```bash
cp .env.example .env
# Edit .env with your values
```

#### 3. Build and run

```bash
# Build
go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot

# Run
./rs8kvn_bot
```

Or run directly:

```bash
go run ./cmd/bot
```

## Usage

1. Start the bot with `/start` command
2. Use the inline buttons:
   - **📥 Получить подписку** - Get or create subscription
   - **📋 Моя подписка** - View current subscription info
3. Admin users also see:
   - **📊 Статистика** - View bot statistics

## CI/CD with GitHub Actions

This project includes a GitHub Actions workflow that automatically builds and pushes Docker images to GitHub Container Registry.

### Setup

1. Go to your GitHub repository settings
2. Enable "Packages" in Features
3. The workflow will automatically push to `ghcr.io/kereal/rs8kvn_bot`

### Triggers

- Push to `main` branch
- Git tags (e.g., `v1.0.0`)
- Pull requests

### Images are tagged with

- Branch name (e.g., `main`)
- Semantic version (e.g., `1.0.0`, `1.0`)
- Commit SHA

## Project Structure

```
rs8kvn_bot/
├── cmd/
│   └── bot/
│       └── main.go              # Entry point
├── internal/
│   ├── backup/
│   │   └── backup.go            # Database backup and rotation
│   ├── bot/
│   │   └── handlers.go          # Telegram bot handlers
│   ├── config/
│   │   └── config.go            # Environment configuration
│   ├── database/
│   │   ├── database.go          # Database models and functions
│   │   └── migrations.go        # Database migrations system
│   ├── heartbeat/
│   │   └── heartbeat.go         # Heartbeat monitoring
│   ├── logger/
│   │   └── logger.go            # Zap logger with Sentry integration
│   ├── ratelimiter/
│   │   └── ratelimiter.go       # Token bucket rate limiter
│   ├── utils/
│   │   └── uuid.go              # UUID and SubID generators
│   └── xui/
│       └── client.go            # 3x-ui API client
├── data/                        # Data directory (created at runtime)
│   ├── tgvpn.db                 # SQLite database
│   ├── tgvpn.db.backup          # Latest backup
│   └── bot.log                  # Log file
├── .env.example                 # Example environment configuration
├── .env                         # Your configuration (create from .env.example)
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
└── README.md
```

## Database Schema

### subscriptions

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER | Primary key |
| `telegram_id` | INTEGER | Telegram chat ID |
| `username` | TEXT | Telegram username |
| `client_id` | TEXT | 3x-ui client UUID |
| `xui_host` | TEXT | 3x-ui panel URL |
| `inbound_id` | INTEGER | 3x-ui inbound ID |
| `traffic_limit` | INTEGER | Traffic limit in bytes |
| `expiry_time` | DATETIME | Subscription expiry date |
| `status` | TEXT | active/revoked |
| `subscription_url` | TEXT | Subscription URL |
| `created_at` | DATETIME | Creation timestamp |
| `updated_at` | DATETIME | Update timestamp |
| `deleted_at` | DATETIME | Soft delete timestamp |

### schema_migrations

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER | Primary key |
| `name` | TEXT | Migration name |
| `applied_at` | DATETIME | When migration was applied |

## Traffic and Expiry

- **Traffic**: Configurable via `TRAFFIC_LIMIT_GB` (default: 100GB)
- **Expiry**: Last second of current month (e.g., 31.03.2026 23:59:59)
- **Auto-renewal**: 31 days (reset parameter in 3x-ui)

## Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `TELEGRAM_BOT_TOKEN` | Telegram bot token from @BotFather | - | ✅ |
| `TELEGRAM_ADMIN_ID` | Admin Telegram ID for notifications | 0 | ❌ |
| `XUI_HOST` | 3x-ui panel URL | http://localhost:2053 | ✅ |
| `XUI_USERNAME` | 3x-ui admin username | admin | ✅ |
| `XUI_PASSWORD` | 3x-ui admin password | admin | ✅ |
| `XUI_INBOUND_ID` | VLESS+Reality inbound ID | 1 | ✅ |
| `XUI_SUB_PATH` | Subscription URL path | sub | ❌ |
| `DATABASE_PATH` | SQLite database path | ./data/tgvpn.db | ❌ |
| `LOG_FILE_PATH` | Log file path | ./data/bot.log | ❌ |
| `LOG_LEVEL` | Log level (debug/info/warn/error) | info | ❌ |
| `TRAFFIC_LIMIT_GB` | Traffic limit in GB | 100 | ❌ |
| `HEARTBEAT_URL` | URL for heartbeat monitoring | - | ❌ |
| `HEARTBEAT_INTERVAL` | Heartbeat interval in seconds | 300 | ❌ |
| `SENTRY_DSN` | Sentry DSN for error tracking | - | ❌ |

## Admin Notifications

When a new subscription is created, the admin (specified in `TELEGRAM_ADMIN_ID`) receives a notification with:
- User username and ID
- Subscription expiry date
- Subscription link

## Database Migrations

The bot includes a simple migration system. To add a new migration:

1. Edit `internal/database/migrations.go`
2. Add a new migration to the `migrations` slice:

```go
var migrations = []Migration{
    {
        Name: "001_add_column",
        SQL:  "ALTER TABLE subscriptions ADD COLUMN new_field TEXT;",
    },
}
```

Migrations are applied automatically on startup. Already applied migrations are skipped.

## Database Backups

- **Automatic**: Daily at 03:00
- **Retention**: 7 days by default
- **Location**: Same directory as database file with `.backup` extension
- **Rotation**: Old backups are automatically cleaned up

## Error Tracking

The bot supports Sentry for error tracking. Set `SENTRY_DSN` in your environment to enable:
- Automatic error capture
- Fatal error reporting
- Panic recovery

## Resource Usage

- **Memory**: ~17 MB RSS
- **Binary size**: ~10 MB
- **CPU**: Minimal (idle most of the time)

## License

MIT
