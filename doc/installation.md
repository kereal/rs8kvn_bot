# Installation and Setup of rs8kvn_bot

## Requirements

- Docker & Docker Compose (recommended)
- OR Go 1.24+
- 3x-ui panel (https://github.com/MHSanaei/3x-ui)
- Telegram Bot Token

## Get your Telegram Admin ID

Send a message to @userinfobot on Telegram to get your ID.

## Configure 3x-ui Panel

1. Create a VLESS+Reality+Vision inbound in your 3x-ui panel
2. Note the inbound ID (shown in the inbounds list)
3. Make sure the panel API is accessible from the bot

## Installation

### Option 1: Docker with GitHub Container Registry (Recommended)

#### 1. Create directory structure

```bash
mkdir -p rs8kvn_bot
cd rs8kvn_bot
```

#### 2. Configure environment

```bash
cp .env.example .env
# Edit .env with your values — see Configuration table below
```

#### 3. Create data directory

```bash
mkdir -p data
chmod 755 data
```

#### 4. Run with Docker

```bash
# Pull from GitHub Container Registry
docker pull ghcr.io/kereal/rs8kvn_bot:latest

# Run container
docker run -d \
  --name rs8kvn_bot \
  --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  -p 127.0.0.1:8880:8880 \
  ghcr.io/kereal/rs8kvn_bot:latest
```

#### 5. View logs

```bash
docker logs -f rs8kvn_bot
```

#### 6. Stop/Start

```bash
docker stop rs8kvn_bot
docker start rs8kvn_bot
```

### Option 2: Docker Compose

```bash
mkdir -p data
chmod 755 data
docker-compose up -d
docker-compose logs -f
```

### Option 3: Build from Source

```bash
git clone https://github.com/kereal/rs8kvn_bot.git
cd rs8kvn_bot
go mod tidy
cp .env.example .env
# Edit .env with your values

# Build
go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot

# Run
./rs8kvn_bot
```

Or run directly: `go run ./cmd/bot`

### Option 4: Development with Air (Hot Reload)

```bash
go install github.com/air-verse/air@latest
air
```

Air will automatically rebuild and restart the bot when you save changes to Go files.

## Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `TELEGRAM_BOT_TOKEN` | Telegram bot token from @BotFather | - | ✅ |
| `TELEGRAM_ADMIN_ID` | Admin Telegram ID for notifications | 0 | ❌ |
| `CONTACT_USERNAME` | Telegram username for support/contact | kereal | ❌ |
| `XUI_HOST` | 3x-ui panel URL | http://localhost:2053 | ✅ |
| `XUI_USERNAME` | 3x-ui admin username | - | ✅ |
| `XUI_PASSWORD` | 3x-ui admin password | - | ✅ |
| `XUI_INBOUND_ID` | VLESS+Reality inbound ID | 1 | ✅ |
| `XUI_SUB_PATH` | Subscription URL path | sub | ❌ |
| `XUI_SESSION_MAX_AGE_MINUTES` | Panel session lifetime in minutes (must match panel's `sessionMaxAge` setting) | 720 | ❌ |
| `DATABASE_PATH` | SQLite database path | ./data/tgvpn.db | ❌ |
| `LOG_FILE_PATH` | Log file path | ./data/bot.log | ❌ |
| `LOG_LEVEL` | Log level (debug/info/warn/error) | info | ❌ |
| `TRAFFIC_LIMIT_GB` | Traffic limit in GB | 30 | ❌ |
| `HEARTBEAT_URL` | URL for heartbeat monitoring | - | ❌ |
| `HEARTBEAT_INTERVAL` | Heartbeat interval in seconds | 300 | ❌ |
| `SENTRY_DSN` | Sentry DSN for error tracking | - | ❌ |
| `HEALTH_CHECK_PORT` | Port for health check HTTP server | 8880 | ❌ |
| `SITE_URL` | Base URL for invite/trial landing pages | https://vpn.site | ❌ |
| `TRIAL_DURATION_HOURS` | Trial subscription duration (hours) | 3 | ❌ |
| `TRIAL_RATE_LIMIT` | Max trial requests per IP per hour | 3 | ❌ |
| `DONATE_CARD_NUMBER` | Donation card number (T-Bank) | (set your own) | ❌ |
| `DONATE_URL` | Donation URL (T-Bank collection link) | (set your own) | ❌ |
| `SUB_EXTRA_SERVERS_ENABLED` | Enable extra servers in subscription proxy | true | ❌ |
| `SUB_EXTRA_SERVERS_FILE` | Path to extra config file (headers + servers) | ./data/extra_servers.txt | ❌ |

**Note:** `XUI_USERNAME` and `XUI_PASSWORD` have no defaults — they must be set explicitly.

## Extra Servers Config File

The subscription proxy can merge additional servers and headers. Config file format (`SUB_EXTRA_SERVERS_FILE`):

```
# Optional headers (Key: Value)
X-Custom-Header: custom-value
Profile-Title: My VPN

# Server links (one per line, after blank line)
vless://user@server1.example.com:443
trojan://pass@server2.example.com:443
```

- Headers from config file override 3x-ui headers
- Extra servers are appended to the subscription body
- Config file is reloaded automatically every 5 minutes
- Stale cache is used as fallback if 3x-ui is unavailable

## Database Migrations

The bot uses [golang-migrate](https://github.com/golang-migrate/migrate). Migration files are stored in `internal/database/migrations/` and embedded into the binary via `go:embed` — no need to ship migration files separately.

Migrations are applied automatically on startup.

### Adding a New Migration

```bash
# Example: creating migration 004
touch internal/database/migrations/004_add_new_column.up.sql
```

Write your SQL in the file — it will be applied on next startup.

### Current Migration Files

- `000_create_subscriptions.up.sql` — initial subscriptions table
- `001_replace_xuihost_with_subscription_id.up.sql` — replaces x_ui_host with subscription_id
- `002_add_invites_and_trials.up.sql` — adds invites and trial_requests tables
- `003_add_referral_columns.up.sql` — adds referral tracking columns

## Database Backups

- **Automatic**: Daily at 03:00
- **Retention**: 14 days
- **Location**: Same directory as database file with `.backup` extension
- **Rotation**: Old backups are automatically cleaned up

## Error Tracking

Set `SENTRY_DSN` to enable Sentry: automatic error capture, fatal error reporting, panic recovery.
