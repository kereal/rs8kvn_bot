# rs8kvn_bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

[![GitHub release](https://img.shields.io/github/v/release/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot/releases)
[![Coverage](https://img.shields.io/badge/coverage-85%25%2B-green)](https://github.com/kereal/rs8kvn_bot/actions)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/kereal/rs8kvn_bot)](https://goreportcard.com/report/github.com/kereal/rs8kvn_bot)
[![License](https://img.shields.io/github/license/kereal/rs8kvn_bot)](LICENSE)

Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features

- 📥 Subscription on demand with traffic tracking
- 📱 QR code for easy import (Happ, V2RayNG)
- 🔗 Invite/trial landing page (`/i/{code}`) with one-click setup
- 👥 Referral system with in-memory cache + periodic DB sync
- 🔗 Subscription proxy (`/sub/{subID}`) with extra servers and headers
- 🔄 Monthly auto-renewal, configurable traffic limit (30 GB default)
- 🛡️ Circuit breaker for 3x-ui with auto-relogin on session expiry
- 🔔 Admin notifications, `/broadcast`, `/send`, `/del`, `/refstats`
- 🏥 Health check endpoints (`/healthz`, `/readyz`)
- 🗄️ Daily DB backups, embedded migrations, Sentry error tracking
- 🧪 ~85% test coverage, race-safe, 100+ scenarios
- 🐳 Docker with GHCR images, golangci-lint + gosec in CI

## Quick Start

**Prerequisites:** Docker, 3x-ui panel, Telegram Bot Token

```bash
mkdir -p rs8kvn_bot && cd rs8kvn_bot
cp .env.example .env   # fill in your values
mkdir -p data && chmod 755 data
docker pull ghcr.io/kereal/rs8kvn_bot:latest
docker run -d --name rs8kvn_bot --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro -v $(pwd)/data:/app/data \
  -p 127.0.0.1:8880:8880 ghcr.io/kereal/rs8kvn_bot:latest
```

📖 **Full installation guide:** [doc/installation.md](doc/installation.md) — Docker Compose, build from source, Air hot reload, all config variables, DB schema, migrations, security details.

## Usage

1. `/start` — main menu with inline buttons
2. **📋 Подписка** — traffic usage, subscription link, QR code
3. **📥 Получить подписку** — create new subscription
4. **☕ Донат** / **❓ Помощь** — auxiliary menus

### Admin Commands

| Command | Description |
|---------|-------------|
| `/lastreg` | Last 10 registered users |
| `/del <id>` | Delete subscription by DB ID |
| `/broadcast <msg>` | Message all subscribers |
| `/send <id or @username> <msg>` | Message specific user |
| `/refstats` | Referral statistics |

## Web Endpoints

Port 8880 by default (`HEALTH_CHECK_PORT`):

| Endpoint | Description |
|----------|-------------|
| `GET /healthz` | Health check (DB + xui status) |
| `GET /readyz` | Ready after init |
| `GET /i/{code}` | Trial invite landing page |
| `GET /sub/{subID}` | Subscription proxy with extra servers |
| `GET /static/logo.png` | Logo image |

## Development

```bash
go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot
go test -race ./...
golangci-lint run ./...
```

📖 **Architecture & details:** [doc/handover.md](doc/handover.md) — project structure, stack, test coverage, critical nuances.

## Documentation

| File | Content |
|------|---------|
| [doc/installation.md](doc/installation.md) | Install methods, config vars, DB schema, migrations, security |
| [doc/handover.md](doc/handover.md) | Architecture, stack, test coverage, critical nuances |
| [doc/bypass_research.md](doc/bypass_research.md) | Bypass methods research for Russia |
| [doc/bypass_clients_comparison.md](doc/bypass_clients_comparison.md) | VPN client comparison (Android/iOS) |
| [doc/marketing_strategy.md](doc/marketing_strategy.md) | Business and marketing strategy |
| [doc/task-bot-integration.md](doc/task-bot-integration.md) | Proxy Manager integration task |