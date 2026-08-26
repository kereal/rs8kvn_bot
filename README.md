# Telegram Bot for 3x-ui VLESS Subscription Distribution

[![GitHub release](https://img.shields.io/github/v/release/kereal/rs8kvn_bot?logo=github)](https://github.com/kereal/rs8kvn_bot/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/kereal/rs8kvn_bot/docker.yml?branch=main)](https://github.com/kereal/rs8kvn_bot/actions)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](https://go.dev/)
[![Coverage](https://img.shields.io/badge/coverage-66.2%25-green)](https://github.com/kereal/rs8kvn_bot/actions)
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
- 🧪 Unit + E2E tests (66.2% aggregate coverage, race-safe, fuzzing)
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

> The registry provides a `latest` tag as well as SemVer (`2.4.0`, `2.4`) and commit SHA tags. Pin a concrete SemVer or SHA tag when reproducible deployments are required.

See **[Installation Guide](doc/installation.md)** for:
- All 4 installation methods (Docker, Docker Compose, Build from Source, Air hot reload)
- Full configuration table (all env vars)
- 3x-ui panel setup instructions
- Database migrations and backups

## Usage

1. Start the bot with `/start`
2. Use the inline buttons:
   - **For users with subscription:**
     - **🚀 Подписка** — View subscription info (traffic usage, subscription link)
       - **📱 QR-код** — Generate QR code for Happ app import
       - **🏠 В начало** — Return to main menu
     - **💎 Купить Premium** — Buy or renew a paid plan (shown when `PAYMENT_ENABLED=true`)
     - **☕ Донат** — View donation info
     - **📖 Помощь** — View VPN setup instructions
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
| `/broadcast` | Start an asynchronous broadcast: name → message → filters → preview → confirm |
| `/broadcasts` | Show recent broadcasts; open details, cancel active campaigns, or retry failed recipients |
| `/send <id or @username> <message>` | Send a message to a specific user |
| `/refstats` | Show referral statistics (count per user from cache) |

**Examples:**

```text
/del 5                                    # Delete subscription with DB ID 5
/setplan 5 3 30                            # Change subscription 5 to plan 3 for 30 days
/broadcast                                 # Start a broadcast (name → message → preview → confirm)
/send 123456789 Привет!                   # Private message by Telegram ID
/send @username Привет!                   # Private message by username
```

**Broadcast flow:** `/broadcast` asks for a broadcast *name*, then the message and
filters. Confirmation creates a queued campaign; delivery runs in a durable
background worker. The admin can send immediately or schedule the campaign for a
future day and hour (day picker → hour picker, Moscow time); a scheduled campaign
stays in `scheduled` state and is claimed by the worker only when `planned_at` is
due. The audience is snapshotted in `broadcasts.recipients_state`, so
changes to subscriptions cannot shift pagination or add duplicate recipients.
Anonymous trials are excluded, `active` is the default status, and `all` is an
explicit status choice; `paid` uses payment state rather than plan name.
Messages preserve MarkdownV2 formatting and transient failures are retried twice
with backoff. `/broadcasts` opens recent cards with details, cancellation for
active campaigns, and retry for failed recipients. A restart resumes scheduled or
running campaigns and releases stale recipient leases. The worker polls every 15 seconds;
launch and persistence failures are stored in `broadcasts.last_error`, `retry_at`, and
`retry_count` with exponential backoff. Delivery outcomes are split into `blocked` (Telegram
explicitly says the user blocked the bot), `unreachable` (deactivated or unavailable chat),
and other errors. The audience snapshot and per-recipient state are stored in the
`broadcasts.recipients_state` JSON column; for the current user count this keeps recovery
simple, and no separate broadcast-recipient table is required. The details card sends one
compact admin message; complete delivered and blocked ID lists remain persisted in
`delivery_report` and can be inspected from the database.

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

Referral-based trial flow: validates the invite code, applies IP rate limiting, creates a trial subscription, and renders a mobile-friendly landing page with Happ download links, one-click deep-link import, copy-to-clipboard URL, and Telegram activation.

### Subscription Server (`/sub/{subID}`)

Serves merged subscriptions from all active nodes (3x-ui, proxman, fetch) with a 240s singleflight cache. Optional access logging is enabled via `SUBSERVER_ACCESS_LOG`.

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
3. The Docker image is built and pushed to `ghcr.io/kereal/rs8kvn_bot` (tags: `latest`, SemVer, and commit SHA).
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

Test suite: 66.2% aggregate coverage in the latest documented run (generated with `go test -coverprofile`), race-safe, fuzzing, table-driven tests, integration tests with mock HTTP server.

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
- **[Broadcast](doc/broadcast.md)** — Admin broadcast flow, filters, delivery worker
- **[.serena/instructions.md](.serena/instructions.md)** — AI assistant workflow and memory structure
