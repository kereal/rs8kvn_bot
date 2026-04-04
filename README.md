# rs8kvn_bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

[![GitHub release](https://img.shields.io/github/v/release/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot/releases)
[![GitHub Release Date](https://img.shields.io/github/release-date/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot/releases)
[![GitHub commits](https://img.shields.io/github/commits-since/kereal/rs8kvn_bot/latest?logo=github)](https://github.com/kereal/rs8kvn_bot/commits/dev)
[![GitHub last commit](https://img.shields.io/github/last-commit/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot/commits/dev)
[![Coverage](https://img.shields.io/badge/coverage-~75%25-green)]()
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen)]()
[![Go](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/kereal/rs8kvn_bot)](https://goreportcard.com/report/github.com/kereal/rs8kvn_bot)
[![GitHub stars](https://img.shields.io/github/stars/kereal/rs8kvn_bot?style=flat&logo=github)](https://github.com/kereal/rs8kvn_bot/stargazers)
[![GitHub issues](https://img.shields.io/github/issues/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot/issues)
[![Code size](https://img.shields.io/github/languages/code-size/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot)
[![License](https://img.shields.io/github/license/kereal/rs8kvn_bot)](LICENSE)

Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features

- 📥 Get subscription on demand
- 📋 View current subscription status
- 📱 QR code for easy subscription import
- 🔗 Invite/trial landing page (`/i/{code}`) with one-click Happ setup
- 👥 Referral system — users generate invite codes (`t.me/{bot}?start=share_{code}`) with in-memory cache + periodic sync
- 📊 Configurable traffic limit (default 30GB/month)
- 🔄 Monthly auto-renewal (last day of month, no expiry)
- 🔔 Admin notifications on new subscriptions
- 💓 Heartbeat monitoring support
- 🏥 Health check endpoint (/healthz, /readyz)
- 📝 File logging with rotation (zap)
- 🗄️ Daily database backups with rotation
- 🔄 Database migrations system (embedded via go:embed)
- 🐛 Sentry error tracking
- 🛡️ Rate limiting per user (per-user token buckets, 30 tokens, 5/sec refill)
- ⚡ Graceful shutdown with goroutine tracking
- 🔒 Circuit breaker for 3x-ui panel
- 🐳 Docker support with health checks
- 🧪 Unit + E2E tests (~75% coverage, race-safe, fuzzing, 66+ E2E scenarios)
- ✅ golangci-lint and gosec for code quality
- 🍪 Trial duplication prevention (3-hour cookie)

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

## Usage

1. Start the bot with `/start` command
2. Use the inline buttons (shown under the message):
   - **For users with subscription:**
     - **📋 Подписка** - View subscription info (traffic usage, subscription link)
       - **📱 QR-код** - Generate QR code for easy import (scannable by Happ app)
       - **🏠 В начало** - Return to main menu
     - **☕ Донат** - View donation info
     - **❓ Помощь** - View VPN setup instructions
   - **For users without subscription:**
     - **📥 Получить подписку** - Create a new subscription
3. Admin users also see:
   - **📊 Стат** - View bot statistics

**Note:** All menu buttons are inline (shown under the message), not at the bottom of the screen. Each submenu has a "🏠 В начало" button to return to the main menu.

### QR Code Flow

When viewing subscription info, users can click **📱 QR-код** to generate a scannable QR code:
1. QR code is sent as a photo (subscription message stays visible above)
2. User can scan the QR code with Happ app to import subscription
3. Click **⬅️ Назад** to close QR and return to subscription info

### Admin Commands

Admins (specified in `TELEGRAM_ADMIN_ID`) have access to additional commands:

| Command | Description |
|---------|-------------|
| `/lastreg` | Show the last 10 registered users |
| `/del <id>` | Delete a subscription by database ID |
| `/broadcast <message>` | Send a message to all users who have a subscription |
| `/send <id\|username> <message>` | Send a message to a specific user |
| `/refstats` | Show referral statistics (count per user from cache) |

**Examples:**
```
/del 5
```
Deletes the subscription with database ID 5 from both the 3x-ui panel and the local database.

```
/broadcast 🔔 Важное обновление: бот обновлен!
```
Sends the message to all users with subscriptions.

```
/send 123456789 Привет! Это личное сообщение.
```
Sends a private message to user with Telegram ID 123456789.

```
/send @username Привет!
```
Sends a private message to user with username "username".

## Health Check & Web Endpoints

The bot exposes HTTP endpoints on port 8880:

| Endpoint | Description | Status Codes |
|----------|-------------|--------------|
| `GET /healthz` | Basic health (process alive, DB and xui status) | 200/503 |
| `GET /readyz` | Ready state (accepting requests after init) | 200/503 |
| `GET /i/{code}` | Trial invite landing page | 200/404/429/500 |
| `GET /static/logo.png` | Logo image (mobile-optimized PNG) | 200/404 |

Example response:
```json
{
  "status": "ok",
  "timestamp": "2026-03-24T12:00:00Z",
  "uptime": "2h30m0s",
  "components": {
    "database": {"status": "ok"},
    "xui": {"status": "ok"}
  }
}
```

Health checks are integrated with Docker healthcheck in docker-compose.yml.

### Invite/Trial Landing Page

Each user can generate an invite code (`/start` → referral flow). The landing page at `/i/{code}`:
1. Validates the invite code (404 if invalid)
2. IP-based rate limiting (429 if exceeded)
3. Creates a trial subscription in 3x-ui
4. Renders a mobile-friendly page with:
   - Project logo
   - Happ app download links (Android / iOS)
   - One-click "Добавить в Happ" button (`happ://add/` deep-link)
   - Copy-to-clipboard subscription URL
   - Telegram activation link
   - Trial duration info

## CI/CD with GitHub Actions

This project includes a GitHub Actions workflow that automatically:
- Runs `golangci-lint` for code quality checks
- Runs `gosec` for security scanning
- Runs tests with coverage
- Builds and pushes Docker images to GitHub Container Registry

- **Trigger:** Push to `main`, pull_request, tags

### Images are tagged with

- Branch name (e.g., `main`)
- Semantic version (e.g., `2.0.0`, `2.0`)
- Commit SHA

## Project Structure

```
rs8kvn_bot/
├── cmd/
│   └── bot/
│       ├── main.go                  # Entry point
│       └── main_test.go            # Main tests
├── internal/
│   ├── backup/
│   │   ├── backup.go               # Database backup and rotation
│   │   └── backup_test.go          # Backup tests
│   ├── bot/
│   │   ├── admin.go                # Admin handlers (/lastreg, /del, /broadcast)
│   │   ├── callbacks.go            # Callback query routing
│   │   ├── commands.go             # Command handlers (/start, /help)
│   │   ├── handler.go              # Handler struct, helper functions
│   │   ├── handlers_test.go        # Handler tests
│   │   ├── integration_test.go     # Integration tests
│   │   ├── keyboard_builder.go     # Inline keyboard builder
│   │   ├── menu.go                 # Menu handlers (back, donate, help)
│   │   ├── message.go              # Message sending utilities
│   │   ├── message_sender.go       # Rate-limited message sender
│   │   ├── referral_cache.go       # Referral cache with persistence
│   │   └── subscription.go         # Subscription logic, QR code handler
│   ├── config/
│   │   ├── config.go               # Environment configuration
│   │   ├── config_test.go          # Config tests
│   │   └── constants.go            # Application constants
│   ├── database/
│   │   ├── database.go             # Database models and functions
│   │   ├── database_test.go        # Database tests
│   │   ├── migrations/             # SQL migration files
│   │   └── migrations.go           # Migration runner
│   ├── health/
│   │   ├── health.go               # Health check HTTP server
│   │   └── health_test.go          # Health check tests
│   ├── heartbeat/
│   │   ├── heartbeat.go            # Heartbeat monitoring
│   │   └── heartbeat_test.go       # Heartbeat tests
│   ├── interfaces/
│   │   ├── interfaces.go           # Service interfaces
│   │   └── interfaces_test.go      # Interface tests with mocks
│   ├── logger/
│   │   ├── logger.go               # Zap logger with Sentry integration
│   │   └── logger_test.go          # Logger tests
│   ├── ratelimiter/
│   │   ├── ratelimiter.go          # Token bucket rate limiter
│   │   └── ratelimiter_test.go     # Rate limiter tests
│   ├── scheduler/
│   │   ├── backup.go               # Database backup scheduler
│   │   └── trial_cleanup.go        # Trial subscription cleanup scheduler
│   ├── testutil/
│   │   └── testutil.go             # Test utilities and mocks
│   ├── utils/
│   │   ├── qr.go                   # QR code generation
│   │   ├── qr_test.go              # QR tests
│   │   ├── time.go                 # Time utilities
│   │   ├── time_test.go            # Time tests
│   │   ├── uuid.go                 # UUID and SubID generators
│   │   └── uuid_test.go            # UUID tests
│   ├── web/
│   │   ├── templates/              # HTML templates (embedded via go:embed)
│   │   │   ├── trial.html          # Trial invite landing page
│   │   │   ├── error.html          # Error page template
│   │   │   └── logo.png            # Logo image served as static file
│   │   ├── web.go                  # HTTP server, health + invite endpoints
│   │   └── web_test.go             # Web endpoint tests
│   └── xui/
│       ├── breaker.go              # Circuit breaker for x-ui
│       ├── breaker_test.go         # Circuit breaker tests
│       ├── client.go               # 3x-ui API client
│       └── client_test.go          # XUI client tests
├── data/                            # Data directory (created at runtime)
│   ├── tgvpn.db                    # SQLite database
│   ├── tgvpn.db.backup             # Latest backup
│   └── bot.log                     # Log file
├── doc/
│   ├── PLAN.md                     # Unified development plan
│   └── HANDOVER.md                 # Session handover summary
├── .env.example                     # Example environment configuration
├── .env                             # Your configuration (create from .env.example)
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── .golangci.yml                    # golangci-lint configuration
└── README.md
```

## Database Schema

### subscriptions

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

### invites

| Column | Type | Description |
|--------|------|-------------|
| `code` | VARCHAR(16) | Primary key (invite code) |
| `referrer_tg_id` | BIGINT | Telegram ID of the user who generated the code |
| `created_at` | DATETIME | Creation timestamp |

### trial_requests

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER | Primary key (autoincrement) |
| `ip` | VARCHAR(45) | Client IP address |
| `created_at` | DATETIME | Creation timestamp |

### schema_migrations

| Column | Type | Description |
|--------|------|-------------|
| `version` | INTEGER | Migration version |
| `dirty` | INTEGER | Dirty flag (0/1) |

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
| `DONATE_CARD_NUMBER` | Donation card number (T-Bank) | 2200702156780864 | ❌ |
| `DONATE_URL` | Donation URL (T-Bank collection link) | https://tbank.ru/cf/9J6agHgWdNg | ❌ |

**Note:** `XUI_USERNAME` and `XUI_PASSWORD` have no defaults - they must be set explicitly.

## Admin Notifications

When a new subscription is created, the admin receives:
- User username and ID
- Subscription expiry date
- Subscription link (full URL)

## Security Features

- **Circuit breaker**: Automatically stops calling 3x-ui after 5 failures, with 30s timeout
- **Rate limiting**: Token bucket rate limiter (30 tokens, refill 5/sec)
- **No default credentials**: XUI_USERNAME/XUI_PASSWORD must be explicitly set
- **Input validation**: Markdown injection prevention, path traversal protection
- **XSS prevention**: html/template for all web pages (automatic context-aware escaping)
- **Graceful shutdown**: Waits for in-flight requests with 30s timeout

## Database Migrations

The bot uses [golang-migrate](https://github.com/golang-migrate/migrate) for database migrations. Migration files are stored in the `internal/database/migrations/` directory and embedded into the binary via `go:embed` — no need to ship migration files separately.

### How Migrations Work

1. Migration files are named with a numeric prefix followed by a description and `.up.sql` extension (e.g., `000_create_subscriptions.up.sql`)
2. On application startup, the bot automatically applies any pending migrations
3. The migration system tracks which migrations have been applied using its own internal schema

### Adding a New Migration

1. Create a new SQL file in `internal/database/migrations/` with the next sequential number:
   ```bash
   # Example: creating migration 004
   touch internal/database/migrations/004_add_new_column.up.sql
   ```
2. Write your SQL migration statements in the file
3. The migration will be automatically applied on the next application startup

### Example Migration File

```sql
-- internal/database/migrations/004_add_new_column.up.sql
ALTER TABLE subscriptions ADD COLUMN new_column VARCHAR(255);
```

### Migration Files Currently in the Project

- `000_create_subscriptions.up.sql` - Creates the initial subscriptions table
- `001_replace_xuihost_with_subscription_id.up.sql` - Replaces x_ui_host column with subscription_id
- `002_add_invites_and_trials.up.sql` - Adds invites and trial_requests tables
- `003_add_referral_columns.up.sql` - Adds referral tracking columns (invite_code, is_trial, referred_by)

## Database Backups

- **Automatic**: Daily at 03:00
- **Retention**: 14 days by default
- **Location**: Same directory as database file with `.backup` extension
- **Rotation**: Old backups are automatically cleaned up

## Error Tracking

The bot supports Sentry for error tracking. Set `SENTRY_DSN` to enable:
- Automatic error capture
- Fatal error reporting
- Panic recovery

## Resource Usage

- **Memory**: ~17 MB RSS
- **Binary size**: ~10 MB
- **CPU**: Minimal (idle most of the time)

## Development

### Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Run specific package tests
go test ./internal/database/... -v
```

**Test Coverage by Package:**

| Package | Coverage |
|---------|----------|
| `internal/ratelimiter` | **97.5%** | ✅ Excellent |
| `internal/bot` | **94.2%** | ✅ Excellent |
| `internal/heartbeat` | **95.8%** | ✅ Excellent |
| `internal/service` | **95.7%** | ✅ Excellent |
| `internal/web` | **96.7%** | ✅ Excellent |
| `internal/xui` | **91.1%** | ✅ Excellent |
| `internal/config` | **87.3%** | ✅ Good |
| `internal/logger` | **87.6%** | ✅ Good |
| `internal/backup` | **82.3%** | ✅ Good |
| `internal/database` | **82.9%** | ✅ Good |
| `internal/utils` | **75.0%** | ✅ Good |
| `cmd/bot` | **14.9%** | 🟡 Low (main is integration) |
| **Overall** | **~75%** | ✅ Good |

All tests pass with `-race` detector (0 failures). Test suite includes:
- **66 E2E tests** — full subscription lifecycle: invite→trial→bind, commands, callbacks, admin operations, concurrency, rollback scenarios
- **Table-driven tests** for parameterized coverage
- **Behavioral assertions** verifying message content, not just "not panic"
- **Fuzzing tests** for `escapeMarkdown`, `truncateString`, `InviteCodeRegex`, `TruncateString`, `NewClient`
- **Integration tests** with mock HTTP server for 3x-ui endpoints
- **Database migration tests** — corrupted SQL, partial migrations, duplicates, concurrent access
- **Thread-safe mocks** with mutex-protected accessors for concurrent test safety

### Linting

```bash
# Run golangci-lint
golangci-lint run ./...

# Run gosec security scanner
gosec ./...
```

### Planning

See `doc/PLAN.md` for the unified development plan including:
- Bug fix status (P0-P4)
- Technical improvements
- New features roadmap
- Implementation phases

### Handover

See `doc/HANDOVER.md` for a session handover summary with architecture, stack, current state, and critical nuances.

### Работа с ИИ-ассистентом

Проект использует память Serena (`.serena/memories/`) для хранения контекста между сессиями работы с ИИ-ассистентом.

**Перед началом работы:**
1. Прочитайте `.serena/instructions.md` — инструкции для ИИ-ассистента
2. Ознакомьтесь с `.serena/memories/project_overview.md`
3. Проверьте `.serena/memories/roadmap.md` на актуальность

**После завершения работы:**
1. Обновите соответствующий файл в `.serena/memories/`:
   - Добавлена новая фича → обновите `project_overview.md`
   - Изменена архитектура → обновите `architecture.md`
   - Изменены планы → обновите `roadmap.md`
2. Закоммитьте изменения в памяти: `git add .serena/memories/`
3. Отправьте на GitHub: `git push origin dev`

**Структура памяти:**
```
.serena/memories/
├── instructions.md      # Инструкции для ИИ (читать первым)
├── project_overview.md  # Общая информация о проекте
├── architecture.md      # Архитектурные решения
├── roadmap.md           # Планы развития
├── code_style.md        # Стиль кода
└── test-info.md         # Информация о тестах
```

См. `.serena/instructions.md` для полного списка правил работы с ИИ-ассистентом.
