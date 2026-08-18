# Telegram Bot for 3x-ui VLESS Subscription Distribution

[![GitHub release](https://img.shields.io/github/v/release/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/kereal/rs8kvn_bot/docker.yml?branch=main)](https://github.com/kereal/rs8kvn_bot/actions)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](https://go.dev/)
[![Coverage](https://img.shields.io/badge/coverage-65.3%25-green)](https://github.com/kereal/rs8kvn_bot/actions)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Fkereal%2Frs8kvn_bot-blue?logo=docker)](https://github.com/kereal/rs8kvn_bot/actions)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL%203.0-blue.svg)](LICENSE)

Нужен VPN? » telegram: [@kereal](https://t.me/kereal)  
По всем вопросам пишите в telegram: [@kereal](https://t.me/kereal)  
Ставьте звездочки! =)

## Features

- 📥 Get subscription on demand with QR code import
- 🔗 Invite/trial landing page (`/i/{code}`) with one-click Happ setup
- 👥 Referral system — users generate invite codes with in-memory cache + periodic sync
- 📊 Plans & pricing — plan-based traffic/device limits, products, orders, multi-node via `nodes`/`plan_nodes` schema
- 💳 **Platega payments** — in-bot purchase flow (💎 Купить Premium), provider payment links, authenticated webhook (`X-MerchantId`/`X-Secret`) with atomic confirmation, renewals, and chargeback auto-downgrade to the free plan
- 🔗 Subscription server endpoint (`/sub/{subID}`) with multi-source aggregation, devices/IPs tracking, and profile headers, node-state synchronization via subscription_nodes table
- 🌐 Multi-node VPN abstraction — `internal/vpn/` with `Client` interface, 3x-ui, proxman, and fetch support, per-node client provisioning
- 📈 Prometheus metrics — `/metrics` endpoint with HTTP, bot, XUI, DB, cache, circuit breaker, subscription metrics
- 🗄️ Daily database backups with rotation, embedded SQLite migrations
- 🐛 Sentry error tracking (+ performance traces)
- 🐳 Docker support with health checks, non-root user, UPX compression
- 🧪 Unit + E2E tests (~63.1% aggregate coverage, race-safe, fuzzing)
- 🔒 Security hardening — X-Forwarded-For rightmost IP (S2), URL scheme allowlist http/https (S3), web↔bot dependency isolation (A1)

## Quick Start

```bash
docker pull ghcr.io/kereal/rs8kvn_bot:2.4.0

docker run -d \
  --name rs8kvn_bot \
  --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  -p 127.0.0.1:8880:8880 \
  ghcr.io/kereal/rs8kvn_bot:2.4.0
```

> Registry tags are SemVer (`2.4.0`, `2.4`) and commit SHA — there is no `latest` tag.

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
     - **💎 Купить Premium** — Buy or renew a paid plan (shown when `PAYMENT_ENABLED=true`)
     - **☕ Донат** — View donation info
     - **❓ Помощь** — View VPN setup instructions
     - **📑 Документы** — Documents / legal menu
   - **For users without subscription:**
     - **📥 Получить подписку** — Create a new subscription
3. Admin users also see **📊 Стат** — View bot statistics

> All menu buttons are inline (shown under the message). Each submenu has a "🏠 В начало" button to return.

## User Commands

| Command | Description |
|---------|-------------|
| `/start` | Start working with the bot / main menu |
| `/mysub` | Show your subscription info (traffic usage, subscription link) |
| `/help` | Show bot help |
| `/invite` | Get your referral link |

## Admin Commands

| Command | Description |
|---------|-------------|
| `/lastreg` | Show the last 10 registered users |
| `/del <id>` | Delete a subscription by database ID |
| `/setplan <subscription_id> <plan_id> [days]` | Change a subscription's plan through the service layer (reconciles VPN nodes, extends expiry; defaults to 30 days when none given) |
| `/broadcast <message>` | Send a message to all users who have a subscription (MarkdownV2, special chars auto-escaped) |
| `/send <id or @username> <message>` | Send a message to a specific user |
| `/refstats` | Show referral statistics (count per user from cache) |

**Examples:**

```text
/del 5                                    # Delete subscription with DB ID 5
/setplan 5 3 30                            # Change subscription 5 to plan 3 for 30 days
/broadcast 🔔 Важное обновление!          # Broadcast to all subscribers (MarkdownV2 supported)
/send 123456789 Привет!                   # Private message by Telegram ID
/send @username Привет!                   # Private message by username
```

**Broadcast formatting:** messages are sent as MarkdownV2. Special characters
(`.`, `!`, `_`, `*`, etc.) are escaped automatically, so plain text needs no
manual escaping — but `*bold*`, `_italic_`, `` `code` `` and `[text](url)` are
preserved. At the end the admin gets a report splitting successful deliveries,
users who blocked the bot, and other errors.

## Health Check & Web Endpoints

The bot exposes HTTP endpoints on port 8880:

| Endpoint | Description | Status Codes |
|----------|-------------|--------------|
| `GET /healthz` | Basic health (process alive, DB status) | 200/503 |
| `GET /readyz` | Ready state (accepting requests after init) | 200/503 |
| `GET /i/{code}` | Trial invites landing page | 200/404/429/500 |
| `GET /metrics` | Prometheus metrics endpoint | 200 |
| `GET /sub/{subID}` | Subscription server | 200/404/502/405 |
| `GET /static/logo.png` | Logo image (mobile-optimized PNG) | 200/404 |
| `POST /payment/callback` | Platega payment webhook (X-MerchantId / X-Secret) | 200/400/401/405/503 |

### Invite/Trial Landing Page (`/i/{code}`)

Each user can generate an invite code via the referral flow. The landing page validates the code, applies IP-based rate limiting (429 if exceeded), creates a trial subscription in 3x-ui, and renders a mobile-friendly page with:
- Happ app download links (Android / iOS)
- One-click "Добавить в Happ" button (`happ://add/` deep-link)
- Copy-to-clipboard subscription URL
- Telegram activation link

### Subscription Server (`/sub/{subID}`)

Serves subscriptions with optional extra servers and custom headers. Validates `subID`, checks cache (240s TTL), fetches from all active nodes (3x-ui, proxman, fetch), merges responses, returns combined output. Fetch nodes use `subscription_url` directly; other types append `subID`.

When `SUBSERVER_ACCESS_LOG` is set, each `/sub/{id}` request is appended to the configured access log file in a zap-console line without a message, caller, or field keys. The record includes timestamp, level, method, URL, response status, client IP, device headers, and User-Agent as space-separated values; values containing spaces are quoted, and empty optional values are written as `-`. The main log also records an INFO message when access logging is enabled. Access log writes are buffered asynchronously; if the file cannot be opened, the bot continues without the access log and writes an error to the main log.

## Traffic and Expiry

- **Auto-reset**: Every 30 days from creation date — 3x-ui resets traffic to 0 and extends `expiresAt` by 30 days automatically when `expiresAt` > 0
- **Source**: [3x-ui inbound.go - autoRenewClients()](https://github.com/mhsanaei/3x-ui/blob/main/web/service/inbound.go#L888-L912)

### Payment Callback (`/payment/callback`)

Receives Platega payment notifications on port 8880. Guard chain: POST-only → readiness (503) → `X-MerchantId`/`X-Secret` constant-time auth (401) → body ≤ 256 KiB, single JSON object → UUID `id` and amount/currency/status validation (400).

- `CONFIRMED` → atomic `pending → paid` (or `expired → paid` within 5 minutes after `PaymentExpiresAt`); plan sync is prepared in the same DB transaction. Duplicate callbacks are idempotent no-ops.
- `CANCELED|CHARGEBACKED` → order cancellation; a chargeback on a paid order auto-downgrades the subscription to the free plan unless another paid order exists.
- Mismatches, unknown statuses and other issues alert the admin (`NotifyPaymentIssue`).

Expose it at `https://<domain>/payment/callback`: locally via `ngrok http 8880`, in production via nginx (see [doc/operations.md](doc/operations.md)). Enabled by `PAYMENT_ENABLED`, `PLATEGA_MERCHANT_ID`, `PLATEGA_SECRET`.

## Releases

Current release: **[v2.4.0](https://github.com/kereal/rs8kvn_bot/releases/tag/v2.4.0)**

Releases are fully automated:

1. Tag `main` with a SemVer tag and push it (e.g. `git tag -a v2.4.1 -m "Release v2.4.1" && git push origin v2.4.1`).
2. CI/CD Pipeline runs tests, `go vet`, `go build`, golangci-lint and the **gosec** security scan.
3. The Docker image is built and pushed to `ghcr.io/kereal/rs8kvn_bot` (tags: `2.4.0`, `2.4`, commit SHA).
4. A GitHub Release is created with an auto-generated changelog.

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

Test suite: ~65.3% aggregate coverage (generated with `go test -coverprofile`), race-safe, fuzzing, table-driven tests, integration tests with mock HTTP server.

### Build

```bash
go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot
```

### Project Documentation

- **[Installation & Configuration](doc/installation.md)** — All setup methods, env vars, and 3x-ui instructions
- **[Architecture](doc/architecture.md)** — System architecture, data model, component deep dives, sync pipeline
- **[Handover](doc/handover.md)** — Architecture overview, stack, current state, nuances
- **[Security Policy](doc/security.md)** — Security measures, hardening checklist, incident response
- **[API Reference](doc/api.md)** — HTTP endpoints, error codes, rate limits
- **[Operations Guide](doc/operations.md)** — Monitoring, troubleshooting, scaling, backup/restore
- **[.serena/instructions.md](.serena/instructions.md)** — AI assistant workflow and memory structure
