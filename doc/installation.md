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
   curl -H "Authorization: Bearer <api_token>" http://your-panel-ip:2053/panel/api/server/status
   ```
   Should return JSON with `success: true`. The `<api_token>` is the panel API token generated in 3x-ui Security settings; in production it is stored in the `nodes` table (`nodes.api_token`).

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
```

Node configuration (panel host, API token, inbound IDs) is stored in the `nodes` DB table — see [Adding Nodes via SQL](#adding-nodes-via-sql) below.

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
| `SUBSERVER_ACCESS_LOG` | `/sub/{id}` access log file path | *(empty)* | ❌ | Set empty to disable; tab-separated (TSV) line, fields separated by tabs with empty optional values as empty fields (no zap-console encoding); startup continues if the file cannot be opened |
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


### Adding Nodes via SQL

Nodes are managed through the `nodes` table. To add a new node, insert a row and link it to a plan:

```sql
-- Add a 3x-ui node
INSERT INTO nodes (name, is_active, host, api_token, inbound_ids, subscription_url, type)
VALUES ('main', 1, 'http://panel:2053', 'your-token', '[1]', 'http://panel:2053/sub/', '3x-ui');

-- Add a proxman node
INSERT INTO nodes (name, is_active, host, api_token, inbound_ids, subscription_url, type)
VALUES ('proxman1', 1, 'http://proxman:8080', 'your-token', '[]', 'http://proxman:8080/sub/', 'proxman');

-- Add a fetch node (read-only HTTP source, no API token needed)
INSERT INTO nodes (name, is_active, host, api_token, inbound_ids, subscription_url, type)
VALUES ('external', 1, '', '', '[]', 'https://external-source.com/raw-proxy', 'fetch');

-- Link node to a plan (replace IDs as needed)
INSERT INTO plan_nodes (plan_id, node_id) VALUES (1, <node_id>);
```

**Node types:**
|Type|Host|API Token|Subscription URL|Description|
|---|---|---|---|---|
|`3x-ui`|Required|Required|`http://panel/sub/`|Full CRUD via 3x-ui API|
|`proxman`|Required|Required|`http://proxman/sub/`|Webhook create/delete|
|`fetch`|Empty|Empty|`https://source/raw`|Read-only HTTP fetch, URL used as-is|

### Security Notes

- **`GLOBAL_SUB_URL`** must use **HTTPS** in production — subscription links are distributed to users and must not leak over plain HTTP. The URL is validated to be a well-formed `http` or `https` URL.
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

### From v2.x → v2.3.0

1. Backup database: `cp data/rs8kvn.db data/rs8kvn.db.backup`
2. Pull new image or rebuild from source — migrations (021–029) run automatically
3. Update `.env`:
   - **New required:** `GLOBAL_SUB_URL` (base URL for subscription links)
   - **New optional:** `SUBSERVER_ACCESS_LOG` (subscription access log)
   - **Removed:** `SUB_EXTRA_SERVERS_ENABLED`, `SUB_EXTRA_SERVERS_FILE` (no longer used)
4. Restart bot. Node configuration lives in the `nodes` table — add nodes via SQL (see [Adding Nodes via SQL](#adding-nodes-via-sql)).

**Breaking changes:**
- `GLOBAL_SUB_URL` is now **required**. Without it the bot will not start.
- Extra servers config file (`SUB_EXTRA_SERVERS_FILE`) feature has been removed.

### From v1.x → v2.x

1. Backup database: `cp data/rs8kvn.db data/rs8kvn.db.backup`
2. Pull new image — migrations run automatically
3. Update `.env`:
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
3. Test 3x-ui connectivity: `curl -H "Authorization: Bearer <api_token>" http://your-panel-ip:2053/panel/api/server/status`
4. Include bot version from logs (`rs8kvn_bot@v2.3.0`)

---

*This document covers installation up to v2.3.0 (2026-07-02). For architecture details, see [handover.md](../handover.md).*
