# Installation & Configuration — rs8kvn_bot

Detailed installation, configuration, and database reference. See [README.md](../README.md) for quick start.

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
  -p 127.0.0.1:8880:8880 \
  ghcr.io/kereal/rs8kvn_bot:latest
```

#### 7. View logs

```bash
docker logs -f rs8kvn_bot
```

#### 8. Stop/Start

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
git clone https://github.com/kereal/rs8kvn_bot.git
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

### Option 4: Development with Air (Hot Reload)

#### 1. Install Air

```bash
go install github.com/air-verse/air@latest
```

#### 2. Run with hot reload

```bash
air
```

Air will automatically rebuild and restart the bot when you save changes to Go files.

## Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `TELEGRAM_BOT_TOKEN` | Telegram bot token from @BotFather | - | ✅ |
| `TELEGRAM_ADMIN_ID` | Admin Telegram ID for notifications | 0 | ❌ |
| `CONTACT_USERNAME` | Telegram username for support/contact | kereal | ❌ |
| `CONTACT_USERNAME` | Telegram username for support/contact | kereal | ❌ |
| `XUI_HOST` | 3x-ui panel URL | http://localhost:2053 | ✅ |
| `XUI_USERNAME` | 3x-ui admin username | - | ✅ |
| `XUI_PASSWORD` | 3x-ui admin password | - | ✅ |
| `XUI_INBOUND_ID` | VLESS+Reality inbound ID | 1 | ✅ |
| `XUI_SUB_PATH` | Subscription URL path | sub | ❌ |
| `XUI_SESSION_MAX_AGE_MINUTES` | Panel session lifetime in minutes (must match panel's sessionMaxAge setting) | 720 | ❌ |
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

**Note:** `XUI_USERNAME` and `XUI_PASSWORD` have no defaults - they must be set explicitly.

## Database

### Database Schema

#### subscriptions

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER | Primary key (autoincrement) |
| `telegram_id` | BIGINT | Telegram chat ID |
| `username` | VARCHAR(255) | Telegram username |
| `client_id` | VARCHAR(255) | 3x-ui client UUID |
| `subscription_id` | VARCHAR(255) | Subscription SubID |
| `inbound_id` | INTEGER | 3x-ui inbound ID |
| `traffic_limit` | BIGINT | Traffic limit in bytes |
| `expiry_time` | DATETIME | Subscription expiry date |
| `status` | VARCHAR(50) | active/revoked |
| `subscription_url` | VARCHAR(512) | Subscription URL |
| `created_at` | DATETIME | Creation timestamp |
| `updated_at` | DATETIME | Update timestamp |
| `deleted_at` | DATETIME | Soft delete timestamp |
| `invite_code` | VARCHAR(16) | Referral invite code (nullable) |
| `is_trial` | INTEGER | 1 if trial subscription |
| `referred_by` | BIGINT | Telegram ID of referrer (nullable) |

#### invites

| Column | Type | Description |
|--------|------|-------------|
| `code` | VARCHAR(16) | Primary key (invite code) |
| `referrer_tg_id` | BIGINT | Telegram ID of the user who generated the code |
| `created_at` | DATETIME | Creation timestamp |

#### trial_requests

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER | Primary key (autoincrement) |
| `ip` | VARCHAR(45) | Client IP address |
| `created_at` | DATETIME | Creation timestamp |

#### schema_migrations

| Column | Type | Description |
|--------|------|-------------|
| `version` | INTEGER | Migration version |
| `dirty` | INTEGER | Dirty flag (0/1) |

### Database Migrations

The bot uses [golang-migrate](https://github.com/golang-migrate/migrate) for database migrations. Migration files are stored in the `internal/database/migrations/` directory and embedded into the binary via `go:embed` — no need to ship migration files separately.

#### How Migrations Work

1. Migration files are named with a numeric prefix followed by a description and `.up.sql` extension (e.g., `000_create_subscriptions.up.sql`)
2. On application startup, the bot automatically applies any pending migrations
3. The migration system tracks which migrations have been applied using its own internal schema

#### Adding a New Migration

1. Create a new SQL file in `internal/database/migrations/` with the next sequential number:
   ```bash
   # Example: creating migration 004
   touch internal/database/migrations/004_add_new_column.up.sql
   ```
2. Write your SQL migration statements in the file
3. The migration will be automatically applied on the next application startup

#### Example Migration File

```sql
-- internal/database/migrations/004_add_new_column.up.sql
ALTER TABLE subscriptions ADD COLUMN new_column VARCHAR(255);
```

#### Migration Files Currently in the Project

- `000_create_subscriptions.up.sql` - Creates the initial subscriptions table
- `001_replace_xuihost_with_subscription_id.up.sql` - Replaces x_ui_host column with subscription_id
- `002_add_invites_and_trials.up.sql` - Adds invites and trial_requests tables
- `003_add_referral_columns.up.sql` - Adds referral tracking columns (invite_code, is_trial, referred_by)

### Database Backups

- **Automatic**: Daily at 03:00
- **Retention**: 14 days by default
- **Location**: Same directory as database file with `.backup` extension
- **Rotation**: Old backups are automatically cleaned up

## Security Features

- **Circuit breaker**: Automatically stops calling 3x-ui after 5 failures, with 30s timeout
- **Auto-relogin on session expiry**: Detects HTTP 401/redirect responses and automatically re-authenticates, then retries the failed request
- **Session verification**: Health checks verify the session with a real API call (`/panel/api/server/status`) instead of relying on timers
- **Configurable session lifetime**: `XUI_SESSION_MAX_AGE_MINUTES` must match the panel's `sessionMaxAge` setting (default: 720 = 12h)
- **Stale connection cleanup**: Connection pool is cleared before re-authentication to prevent using dead connections
- **DNS error fast-fail**: Non-retryable errors (like "no such host") fail immediately instead of wasting time on retries
- **Rate limiting**: Token bucket rate limiter (30 tokens, refill 5/sec)
- **No default credentials**: XUI_USERNAME/XUI_PASSWORD must be explicitly set
- **Input validation**: Markdown injection prevention (backslash-first escaping for MarkdownV2), path traversal protection
- **XSS prevention**: html/template for all web pages (automatic context-aware escaping)
- **Graceful shutdown**: Waits for in-flight requests with 30s timeout
- **Startup retry**: Bot retries panel connection up to 5 times with jitter before failing

## Traffic and Expiry

- **Traffic**: Configurable via `TRAFFIC_LIMIT_GB` (default: 30GB)
- **Expiry**: Set to creation time + 30 days for auto-reset to work
- **Auto-reset**: Every 30 days from creation date (configurable via `SubscriptionResetIntervalDays`)
- **Mechanism**: When `ExpiryTime` is set, 3x-ui automatically:
  1. Resets traffic to 0 when `ExpiryTime` is reached
  2. Extends `ExpiryTime` by `reset` days (30 days)
  3. Re-enables the client if disabled

**Important**: Auto-reset only works when `ExpiryTime > 0`. If `ExpiryTime = 0`, no automatic reset occurs.

**Source**: [3x-ui inbound.go - autoRenewClients()](https://github.com/mhsanaei/3x-ui/blob/main/web/service/inbound.go#L888-L912)

## Error Tracking

The bot supports Sentry for error tracking. Set `SENTRY_DSN` to enable:
- Automatic error capture
- Fatal error reporting
- Panic recovery

## Resource Usage

- **Memory**: ~17 MB RSS
- **Binary size**: ~10 MB
- **CPU**: Minimal (idle most of the time)