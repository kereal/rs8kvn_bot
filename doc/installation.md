# Installation and Setup of rs8kvn_bot

## Requirements

- **Docker & Docker Compose** (recommended) OR **Go 1.25+**
- **3x-ui panel** (https://github.com/MHSanaei/3x-ui) — up and running
- **Telegram Bot Token** from @BotFather
- **Minimum resources:** 1 CPU core, 128MB RAM, 1GB disk (for up to 10k users)

## Get your Telegram Admin ID

Send a message to [@userinfobot](https://t.me/userinfobot) on Telegram to get your ID.

## Configure 3x-ui Panel

1. Create a **VLESS+Reality+Vision** inbound in your 3x-ui panel
2. Note the **inbound ID** (shown in the inbounds list, usually `1`)
3. Make sure the panel API is accessible from the bot host:
   ```bash
   curl -u admin:password http://your-panel-ip:2053/panel/api/server/status
   ```
   Should return JSON with `success: true`.

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
nano .env  # or use your editor
```

**Important:** Set at minimum:
```env
TELEGRAM_BOT_TOKEN=your_bot_token_here
TELEGRAM_ADMIN_ID=123456789
XUI_HOST=http://your-panel-ip:2053
XUI_USERNAME=admin
XUI_PASSWORD=your_panel_password
XUI_INBOUND_ID=1
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
  --security-opt no-new-privileges:true \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  -p 127.0.0.1:8880:8880 \
  ghcr.io/kereal/rs8kvn_bot:latest
```

#### 5. View logs

```bash
docker logs -f rs8kvn_bot
```

Look for: `Bot started successfully`

#### 6. Test bot

Open Telegram, start the bot: `/start`

#### 7. Stop/Start

```bash
docker stop rs8kvn_bot
docker start rs8kvn_bot
```

### Option 2: Docker Compose (with full example)

Create `docker-compose.yml`:

```yaml
version: '3.8'

services:
  rs8kvn_bot:
    image: ghcr.io/kereal/rs8kvn_bot:latest
    container_name: rs8kvn_bot
    restart: unless-stopped

    # Security: Run as non-root user (matches UID/GID in Dockerfile)
    user: "1000:1000"

    volumes:
      # Read-only config file
      - ./.env:/app/.env:ro
      # Persistent data directory (must be writable)
      - ./data:/app/data

    environment:
      - TZ=Europe/Moscow
      # Go runtime memory optimization (optional, already set in Dockerfile)
      - GOMEMLIMIT=67108864
      - GOGC=40

    # Expose health check port for monitoring (optional)
    ports:
      - "127.0.0.1:8880:8880"

    # Security hardening
    security_opt:
      - no-new-privileges:true

    # Health check
    healthcheck:
      test: ["CMD", "wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:8880/healthz"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 30s

    # Resource limits for production
    deploy:
      resources:
        limits:
          cpus: "0.5"
          memory: 128M
        reservations:
          cpus: "0.1"
          memory: 32M

    # Logging
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"

    # Graceful shutdown
    stop_grace_period: 30s
    stop_signal: SIGTERM
```

Run:
```bash
docker-compose up -d
docker-compose logs -f
```

### Option 3: Build from Source

```bash
# Clone repository
git clone https://github.com/kereal/rs8kvn_bot.git
cd rs8kvn_bot

# Install dependencies
go mod download

# Copy and configure environment
cp .env.example .env
nano .env  # set required vars

# Build
go build -ldflags="-s -w -X main.version=$(git describe --tags --abbrev=0 2>/dev/null || echo dev) -X main.commit=$(git rev-parse --short HEAD) -X main.buildTime=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" -o rs8kvn_bot ./cmd/bot

# Run
./rs8kvn_bot
```

**Or run directly without building:**
```bash
go run ./cmd/bot
```

### Option 4: Development with Air (Hot Reload)

```bash
go install github.com/air-verse/air@latest
air
```

Air will automatically rebuild and restart the bot when you save changes to Go files.

---

## Configuration

### Full Configuration Table

| Variable | Description | Default | Required | Notes |
|----------|-------------|---------|----------|-------|
| **Telegram** |
| `TELEGRAM_BOT_TOKEN` | Bot token from @BotFather | — | ✅ | Format: `123456:ABC-def...` |
| `TELEGRAM_ADMIN_ID` | Admin Telegram ID for notifications | `0` | ❌ | Get from @userinfobot |
| `CONTACT_USERNAME` | Support Telegram username | `kereal` | ❌ | Without `@` |
| **3x-ui Panel** |
| `XUI_HOST` | Panel URL | `http://localhost:2053` | ✅ | Must be reachable from bot |
| `XUI_USERNAME` | Admin username | — | ✅ | Panel login |
| `XUI_PASSWORD` | Admin password | — | ✅ | Panel login |
| `XUI_INBOUND_ID` | VLESS inbound ID | `1` | ✅ | Integer |
| `XUI_SUB_PATH` | Subscription URL path segment | `sub` | ❌ | Alphanumeric, `_`, `-` only |
| `XUI_SESSION_MAX_AGE_MINUTES` | Panel session lifetime | `720` (12h) | ❌ | Must match panel setting |
| **Database** |
| `DATABASE_PATH` | SQLite database file path | `./data/tgvpn.db` | ❌ | Directory must exist |
| **Logging** |
| `LOG_FILE_PATH` | Log file path | `./data/bot.log` | ❌ | Rotated automatically |
| `LOG_LEVEL` | Log level | `info` | ❌ | `debug`, `info`, `warn`, `error` |
| **Subscription** |
| `TRAFFIC_LIMIT_GB` | Monthly traffic limit (GB) | `30` | ❌ | 1–1000 GB |
| **Health & Monitoring** |
| `HEARTBEAT_URL` | URL for heartbeat POST (optional) | — | ❌ | Receives `{}` every 5 min |
| `HEARTBEAT_INTERVAL` | Heartbeat interval (seconds) | `300` | ❌ | Min 10s |
| `SENTRY_DSN` | Sentry DSN for error tracking | — | ❌ | https://sentry.io/... |
| `HEALTH_CHECK_PORT` | HTTP server port for health checks | `8880` | ❌ | 1–65535 |
| **Trial & Referral** |
| `SITE_URL` | Base URL for landing pages | `https://vpn.site` | ❌ | Used in Telegram links |
| `TRIAL_DURATION_HOURS` | Trial subscription duration | `3` | ❌ | 1–168 hours (7 days max) |
| `TRIAL_RATE_LIMIT` | Max trial requests per IP per hour | `3` | ❌ | 1–100 |
| **Donation** |
| `DONATE_CARD_NUMBER` | Donation card (T-Bank) | *(empty)* | ❌ | Shown in donate menu |
| `DONATE_URL` | Donation collection link | *(empty)* | ❌ | T-Bank or other |
| **Subscription Proxy** |
| `SUB_EXTRA_SERVERS_ENABLED` | Enable extra servers in proxy | `true` | ❌ | `true`/`false` |
| `SUB_EXTRA_SERVERS_FILE` | Path to extra servers config | `./data/extra_servers.txt` | ❌ | See below |
| **API** |
| `API_TOKEN` | Bearer token for `/api/v1/subscriptions` | — | ✅ if endpoint used | Random string |
| **Webhook** |
| `PROXY_MANAGER_WEBHOOK_SECRET` | Secret for webhook auth | — | ❌ | Bearer token |
| `PROXY_MANAGER_WEBHOOK_URL` | Webhook URL for external notifications | — | ❌ | Must be HTTPS |

### Security Notes

- **XUI_HOST** must use **HTTPS** in production (HTTP only allowed for localhost)
- All webhook URLs must use **HTTPS** (except localhost)
- `.env` file should have permissions `600` (readable only by owner)
- Never commit `.env` to version control

---

## Extra Servers Config File

**Path:** `SUB_EXTRA_SERVERS_FILE` (default: `./data/extra_servers.txt`)

**Format:**
```
# Optional headers (Key: Value) — appear at top of subscription
X-Custom-Header: custom-value
Profile-Title: My VPN

# Blank line separates headers from server list

# Server lines (one per line, supported schemes):
vless://user@server1.example.com:443
trojan://pass@server2.example.com:443
ss://加密:server3.example.com:8388
vmess://uuid@server4.example.com:443
```

**Features:**
- Headers override 3x-ui headers
- Extra servers appended to subscription body
- Config reloaded automatically every 5 minutes
- Invalid lines ignored

**Security:** Path is validated — no directory traversal allowed.

---

## Database Migrations

The bot uses [golang-migrate](https://github.com/golang-migrate/migrate). Migration files are stored in `internal/database/migrations/` and embedded into the binary via `go:embed`.

Migrations are applied automatically on startup. If migration fails, bot exits with error.

### Current Migration Files

| Version | Description |
|---------|-------------|
| `000_create_subscriptions.up.sql` | Initial subscriptions table |
| `001_replace_xuihost_with_subscription_id.up.sql` | Replaces `x_ui_host` column with `subscription_id` |
| `002_add_invites_and_trials.up.sql` | Adds `invites` and `trial_requests` tables |
| `003_add_referral_columns.up.sql` | Adds `invite_code`, `is_trial`, `referred_by` columns |

### Adding a New Migration

```bash
# Create migration files
touch internal/database/migrations/004_add_new_column.up.sql
touch internal/database/migrations/004_add_new_column.down.sql

# Write SQL in files, then rebuild:
go build -o rs8kvn_bot ./cmd/bot
```

**Up migration:** Add/modify columns  
**Down migration:** Reverse changes (optional but recommended)

---

## Post-Installation

### 1. Verify installation

```bash
# Health check
curl http://localhost:8880/healthz

# Expected: {"database":"ok","xui":"ok","status":"ok"}

# Bot logs
docker logs rs8kvn_bot | tail -20
```

### 2. Test subscription flow

1. Open Telegram
2. Start bot: `/start`
3. Click "📥 Получить подписку"
4. Should receive subscription link + QR code

### 3. Admin commands

Set `TELEGRAM_ADMIN_ID` in `.env` to your Telegram user ID.

Admin-only commands:
- `/lastreg` — last 10 subscribers
- `/del <id>` — delete subscription by DB ID
- `/broadcast <msg>` — message all users
- `/send <id|@username> <msg>` — private message
- `/refstats` — referral statistics
- `/plan` — set user plan

---

## Upgrade from Older Version

### From v2.2.0 → v2.3.0

1. Pull new image or rebuild from source
2. Stop old container, start new (same data volume)
3. Migrations auto-applied (no manual action needed)
4. New features available immediately

**Breaking changes:** None

### From v1.x → v2.x

1. Backup database: `cp data/tgvpn.db data/tgvpn.db.backup`
2. Pull new image — migrations run automatically
3. Update `.env`:
   - New required: `XUI_INBOUND_ID`
   - New optional: `TRAFFIC_LIMIT_GB`, `TRIAL_DURATION_HOURS`
4. Restart bot

---

## Uninstall

```bash
# Stop and remove container
docker stop rs8kvn_bot && docker rm rs8kvn_bot

# Remove image
docker rmi ghcr.io/kereal/rs8kvn_bot:latest

# Remove data (⚠️  THIS DELETES ALL SUBSCRIPTIONS)
rm -rf ./data

# Remove .env
rm .env
```

---

## Support

**Issues:** https://github.com/kereal/rs8kvn_bot/issues  
**Documentation:** `doc/handover.md`, `doc/operations.md`

**Before reporting:**
1. Check logs: `docker logs rs8kvn_bot`
2. Verify `.env` settings
3. Test 3x-ui connectivity: `curl $XUI_HOST/panel/api/server/status`
4. Include bot version from logs (`rs8kvn_bot@v2.3.0`)

---

*This document covers installation up to v2.3.0. For architecture details, see [handover.md](../handover.md).*
