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
   export XUI_API_TOKEN=your_panel_api_token
   curl -H "Authorization: Bearer $XUI_API_TOKEN" http://your-panel-ip:2053/panel/api/server/status
   ```
   Should return JSON with `success: true`.

> **Note (v3.0+):** `XUI_HOST`, `XUI_API_TOKEN`, and `XUI_INBOUND_ID` are **not** read from the environment at runtime. They are used **only on first run** to seed the `nodes` table (when it is empty). After that, node configuration — host, API token, inbound IDs, type, subscription URL — is managed via the database. See [Initial Node Seeding](#initial-node-seeding) below.

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
GLOBAL_SUB_URL=https://vpn.example.com/sub/

# Initial node seed (only used on first run to populate the nodes table):
XUI_HOST=http://your-panel-ip:2053
XUI_API_TOKEN=your_panel_api_token
XUI_INBOUND_ID=1
```

`GLOBAL_SUB_URL` is the base URL for subscription links. The bot constructs full subscription URLs as `GLOBAL_SUB_URL + <subscription_id>` (e.g. `https://vpn.example.com/sub/abc123`). It must be set, or subscription links will be broken.

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
| `TELEGRAM_ADMIN_ID` | Admin Telegram ID for notifications | `0` | ✅ | Must be positive; get from @userinfobot |
| `CONTACT_USERNAME` | Support Telegram username | *(empty)* | ❌ | Without `@` |
| **Subscription Server** |
| `GLOBAL_SUB_URL` | Base URL for subscription links | — | ✅ | Constructed as `GLOBAL_SUB_URL + <sub_id>` (e.g. `https://vpn.example.com/sub/abc123`); must be valid http/https URL, HTTPS in production |
| `SUBSERVER_ACCESS_LOG` | `/sub/{id}` access log file path | *(empty)* | ❌ | Set empty to disable; zap-console line without message/caller/field keys, values are space-separated, values with spaces are quoted, empty optional values are `-`; startup continues if the file cannot be opened |
| **Database** |
| `DATABASE_PATH` | SQLite database file path | `./data/rs8kvn.db` | ❌ | Directory must exist |
| **Logging** |
| `LOG_FILE_PATH` | Log file path | `./data/bot.log` | ❌ | Rotated automatically |
| `LOG_LEVEL` | Log level | `info` | ❌ | `debug`, `info`, `warn`, `error` |
| **Monitoring** |
| `HEARTBEAT_URL` | URL for heartbeat POST (optional) | — | ❌ | Receives `{}` every 5 min; must be valid http/https URL if set |
| `HEARTBEAT_INTERVAL` | Heartbeat interval (seconds) | `300` | ❌ | Min 10s |
| `SENTRY_DSN` | Sentry DSN for error tracking | — | ❌ | https://sentry.io/...; must be valid http/https URL if set |
| `HEALTH_CHECK_PORT` | HTTP server port for health checks | `8880` | ❌ | 1–65535 |
| **Trial & Referral** |
| `SITE_URL` | Base URL for landing pages | `https://vpn.site` | ❌ | Must be valid http/https URL; used in Telegram links |
| `TRIAL_DURATION_HOURS` | Trial subscription duration | `3` | ❌ | 1–168 hours (7 days max) |
| `TRIAL_RATE_LIMIT` | Max trial requests per IP per hour | `3` | ❌ | 1–100 |
| **Donation** |
| `DONATE_CARD_NUMBER` | Donation card (T-Bank) | *(empty)* | ❌ | Shown in donate menu |
| `DONATE_URL` | Donation collection link | *(empty)* | ❌ | T-Bank or other |

### Initial Node Seeding

The following environment variables are **not** part of the runtime configuration. They are read via `os.Getenv` **only on first run**, when the `nodes` table is empty, to seed a default node. After the first run, nodes are managed entirely through the database (`nodes` table: `host`, `api_token`, `inbound_ids` JSON array, `type`, `subscription_url`).

| Variable | Description | Default | Notes |
|----------|-------------|---------|-------|
| `XUI_HOST` | Seed node panel URL | — | Seed-only; e.g. `http://your-panel-ip:2053` |
| `XUI_API_TOKEN` | Seed node panel API token | — | Seed-only; generated in panel Security settings |
| `XUI_INBOUND_ID` | Seed node inbound ID (singular integer) | `1` | Seed-only; populates `nodes.inbound_ids` as a JSON array `[1]` |

> Once the `nodes` table is populated, these env vars are ignored on subsequent starts. To add or modify nodes, edit the `nodes` table directly (or via the bot's admin tooling). At runtime the bot loads node configuration from the DB, not from env.

### Security Notes

- **`GLOBAL_SUB_URL`** must use **HTTPS** in production — subscription links are distributed to users and must not leak over plain HTTP. The URL is validated to be a well-formed `http` or `https` URL.
- **`XUI_HOST`** is seed-only; it is not validated at runtime. When seeding, prefer HTTPS for the panel URL.
- `.env` file should have permissions `600` (readable only by owner)
- Never commit `.env` to version control

---

## Post-Installation

### 1. Verify installation

```bash
# Health check
curl http://localhost:8880/healthz

# Expected: {"database":"ok","status":"ok"}

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

---

## Upgrade from Older Version

### From v2.x → v3.0.0

1. Backup database: `cp data/rs8kvn.db data/rs8kvn.db.backup`
2. Pull new image or rebuild from source — migrations (021–027) run automatically
3. Update `.env`:
   - **New required:** `GLOBAL_SUB_URL` (base URL for subscription links)
   - **New optional:** `SUBSERVER_ACCESS_LOG` (subscription access log)
   - **Removed:** `XUI_SUB_PATH`, `SUB_EXTRA_SERVERS_ENABLED`, `SUB_EXTRA_SERVERS_FILE` (no longer used)
   - **Changed:** `XUI_HOST`, `XUI_API_TOKEN`, `XUI_INBOUND_ID` are now **seed-only** — kept for first-run population of the `nodes` table, then ignored. Node configuration lives in the `nodes` table.
4. Restart bot. On first start with an empty `nodes` table, the default node is seeded from `XUI_HOST`/`XUI_API_TOKEN`/`XUI_INBOUND_ID`.

**Breaking changes:**
- `GLOBAL_SUB_URL` is now **required**. Without it the bot will not start.
- `XUI_SUB_PATH` is removed — subscription path is now derived from `GLOBAL_SUB_URL`.
- Extra servers config file (`SUB_EXTRA_SERVERS_FILE`) feature has been removed.

### From v1.x → v2.x

1. Backup database: `cp data/rs8kvn.db data/rs8kvn.db.backup`
2. Pull new image — migrations run automatically
3. Update `.env`:
   - New required: `XUI_INBOUND_ID`
   - New optional: `TRIAL_DURATION_HOURS`
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
2. Verify `.env` settings (especially `GLOBAL_SUB_URL`)
3. Test 3x-ui connectivity: `curl -H "Authorization: Bearer $XUI_API_TOKEN" http://your-panel-ip:2053/panel/api/server/status`
4. Include bot version from logs (`rs8kvn_bot@v3.0.0`)

---

*This document covers installation up to v3.0.0 (2026-07-02). For architecture details, see [handover.md](../handover.md).*
