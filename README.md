# rs8kvn_bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

[![GitHub release](https://img.shields.io/github/v/release/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot/releases)
[![Coverage](https://img.shields.io/badge/coverage-85%25%2B-green)](https://github.com/kereal/rs8kvn_bot/actions)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/kereal/rs8kvn_bot)](https://goreportcard.com/report/github.com/kereal/rs8kvn_bot)
[![License](https://img.shields.io/github/license/kereal/rs8kvn_bot)](LICENSE)

Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features

- 📥 Get subscription on demand with QR code import
- 🔗 Invite/trial landing page (`/i/{code}`) with one-click Happ setup
- 👥 Referral system — users generate invite codes with in-memory cache + periodic sync
- 📊 Configurable traffic limit (default 30GB/month)
- 🔄 Monthly auto-renewal (last day of month, no expiry)
- 🔔 Admin notifications on new subscriptions
- 🛡️ Rate limiting, circuit breaker for 3x-ui, auto-relogin on session expiry
- 🏥 Health check endpoints (/healthz, /readyz)
- 🔗 Subscription proxy endpoint (`/sub/{subID}`) with extra servers and headers
- 🗄️ Daily database backups with rotation, embedded migrations
- 🐛 Sentry error tracking
- 🐳 Docker support with health checks
- 🧪 Unit + E2E tests (~85% coverage, race-safe, fuzzing)

## Quick Start

```bash
docker pull ghcr.io/kereal/rs8kvn_bot:latest

docker run -d \
  --name rs8kvn_bot \
  --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  -p 127.0.0.1:8880:8880 \
  ghcr.io/kereal/rs8kvn_bot:latest
```

See **[Installation Guide](doc/installation.md)** for:
- All 4 installation methods (Docker, Docker Compose, Build from Source, Air hot reload)
- Full configuration table (all env vars)
- 3x-ui panel setup instructions
- Database migrations and backups

## Usage

1. Start the bot with `/start`
2. Use the inline buttons:
   - **For users with subscription:**
     - **📋 Подписка** — View subscription info (traffic usage, subscription link)
       - **📱 QR-код** — Generate QR code for Happ app import
       - **🏠 В начало** — Return to main menu
     - **☕ Донат** — View donation info
     - **❓ Помощь** — View VPN setup instructions
   - **For users without subscription:**
     - **📥 Получить подписку** — Create a new subscription
3. Admin users also see **📊 Стат** — View bot statistics

> All menu buttons are inline (shown under the message). Each submenu has a "🏠 В начало" button to return.

## Admin Commands

| Command | Description |
|---------|-------------|
| `/lastreg` | Show the last 10 registered users |
| `/del <id>` | Delete a subscription by database ID |
| `/broadcast <message>` | Send a message to all users who have a subscription |
| `/send <id or @username> <message>` | Send a message to a specific user |
| `/refstats` | Show referral statistics (count per user from cache) |

**Examples:**

```
/del 5                                    # Delete subscription with DB ID 5
/broadcast 🔔 Важное обновление!          # Broadcast to all subscribers
/send 123456789 Привет!                   # Private message by Telegram ID
/send @username Привет!                   # Private message by username
```

## Health Check & Web Endpoints

The bot exposes HTTP endpoints on port 8880:

| Endpoint | Description | Status Codes |
|----------|-------------|--------------|
| `GET /healthz` | Basic health (process alive, DB and xui status) | 200/503 |
| `GET /readyz` | Ready state (accepting requests after init) | 200/503 |
| `GET /i/{code}` | Trial invites landing page | 200/404/429/500 |
| `GET /sub/{subID}` | Subscription proxy (extra servers + headers, status checked) | 200/404/502/405 |
| `GET /static/logo.png` | Logo image (mobile-optimized PNG) | 200/404 |

### Invite/Trial Landing Page (`/i/{code}`)

Each user can generate an invite code via the referral flow. The landing page validates the code, applies IP-based rate limiting (429 if exceeded), creates a trial subscription in 3x-ui, and renders a mobile-friendly page with:
- Happ app download links (Android / iOS)
- One-click "Добавить в Happ" button (`happ://add/` deep-link)
- Copy-to-clipboard subscription URL
- Telegram activation link

### Subscription Proxy (`/sub/{subID}`)

Serves subscriptions with optional extra servers and custom headers. Validates `subID`, checks cache (240s TTL), fetches from 3x-ui, merges extra servers/headers from config file, returns combined response. Stale cache is used as fallback if 3x-ui is unavailable.

## Traffic and Expiry

- **Traffic**: Configurable via `TRAFFIC_LIMIT_GB` (default: 30GB)
- **Auto-reset**: Every 30 days from creation date — 3x-ui resets traffic to 0 and extends `ExpiryTime` by 30 days automatically when `ExpiryTime > 0`
- **Source**: [3x-ui inbound.go - autoRenewClients()](https://github.com/mhsanaei/3x-ui/blob/main/web/service/inbound.go#L888-L912)

## Development

### Test & Lint

```bash
# Run all tests
go test ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Run specific package
go test ./internal/database/... -v

# Lint
golangci-lint run ./...
gosec ./...
```

Test suite: ~85% coverage, 66 E2E tests, race-safe, fuzzing, table-driven tests, integration tests with mock HTTP server.

### Build

```bash
go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot
```

### Project Documentation

- **[Installation & Configuration](doc/installation.md)** — All setup methods, env vars, and 3x-ui instructions
- **[Handover](doc/handover.md)** — Architecture, stack, current state, nuances
- **[.serena/instructions.md](.serena/instructions.md)** — AI assistant workflow and memory structure
