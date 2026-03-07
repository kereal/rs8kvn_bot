# TGVPN Bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features

- 📥 Get subscription on demand
- 📋 View current subscription status
- 📊 100GB traffic per month
- 📅 Auto-renewal on the last day of each month
- 🔔 Admin notifications on new subscriptions
- 📝 File logging
- 🐳 Docker support

## Requirements

- Docker & Docker Compose (recommended)
- OR Go 1.21+
- 3x-ui panel (https://github.com/MHSanaei/3x-ui)
- Telegram Bot Token

## Installation

### Option 1: Docker with GitHub Container Registry (Recommended)

#### 1. Create directory structure

```bash
mkdir -p tgvpn_go
cd tgvpn_go
```

#### 2. Configure environment

Copy `.env.example` to `.env` and fill in your values:

```bash
cp .env.example .env
```

Edit `.env`:

```env
# Telegram Bot Configuration
TELEGRAM_BOT_TOKEN=your_bot_token_from_botfather
TELEGRAM_ADMIN_ID=your_telegram_id

# 3x-ui Panel Configuration
XUI_HOST=http://your-panel-ip:2053
XUI_USERNAME=your_panel_username
XUI_PASSWORD=your_panel_password
XUI_INBOUND_ID=1  # ID of your VLESS+Reality inbound
XUI_SUB_PATH=sub

# Database Configuration
DATABASE_PATH=./data/tgvpn.db

# Logging Configuration
LOG_FILE_PATH=./data/bot.log
LOG_LEVEL=info

# Subscription Configuration
TRAFFIC_LIMIT_GB=100
```

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
docker pull ghcr.io/YOUR_USERNAME/tgvpn_go:latest

# Run container
docker run -d \
  --name tgvpn_bot \
  --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  ghcr.io/YOUR_USERNAME/tgvpn_go:latest
```

#### 6. View logs

```bash
docker logs -f tgvpn_bot
```

#### 7. Stop/Start

```bash
docker stop tgvpn_bot
docker start tgvpn_bot
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
cd tgvpn_go
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
go build -ldflags="-s -w" -o tgvpn_bot ./cmd/bot

# Run
./tgvpn_bot
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

## CI/CD with GitHub Actions

This project includes a GitHub Actions workflow that automatically builds and pushes Docker images to GitHub Container Registry.

### Setup

1. Go to your GitHub repository settings
2. Enable "Packages" in Features
3. The workflow will automatically push to `ghcr.io/YOUR_USERNAME/tgvpn_go`

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
tgvpn_go/
├── cmd/
│   └── bot/
│       └── main.go          # Entry point
├── internal/
│   ├── config/
│   │   └── config.go        # Environment configuration
│   ├── database/
│   │   └── database.go      # Database models and functions
│   ├── logger/
│   │   └── logger.go        # File logging
│   ├── xui/
│   │   └── client.go        # 3x-ui API client
│   └── bot/
│       └── handlers.go      # Telegram bot handlers
├── .env.example
├── .env                     # Your configuration (create from .env.example)
├── Dockerfile
├── docker-compose.yml       # Optional
├── go.mod
├── go.sum
└── README.md
```

## Database Schema

### Users
- `id` - Telegram chat ID (primary key)
- `username` - Telegram username
- `created_at` - Registration date
- `updated_at` - Last update date

### Subscriptions
- `id` - Subscription ID (primary key)
- `user_id` - Reference to user
- `client_id` - 3x-ui client ID
- `inbound_id` - 3x-ui inbound ID
- `traffic_limit` - Traffic limit in bytes
- `expiry_time` - Subscription expiry date
- `status` - active/revoked/expired
- `subscription` - Subscription URL
- `created_at` - Creation date
- `updated_at` - Last update date

## Traffic and Expiry

- **Traffic**: 100GB per month (configurable via `TRAFFIC_LIMIT_GB`)
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

## Admin Notifications

When a new subscription is created, the admin (specified in `TELEGRAM_ADMIN_ID`) receives a notification with:
- User username and ID
- Subscription expiry date
- Subscription link

## Resource Usage

- **Memory**: ~17 MB RSS
- **Binary size**: 9.5 MB
- **CPU**: Minimal (idle most of the time)

## License

MIT
