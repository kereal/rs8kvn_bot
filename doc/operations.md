# Operations Guide — rs8kvn_bot

**Version:** 2.3.0  
**Last updated:** 2026-07-02

---

## Table of Contents

1. [Quick Reference](#1-quick-reference)
2. [Upgrade](#2-upgrade)
3. [Backup & Restore](#3-backup--restore)
4. [Monitoring](#4-monitoring)
5. [Troubleshooting](#5-troubleshooting)
6. [Scaling](#6-scaling)
7. [Security Hardening](#7-security-hardening)

---

## 1. Quick Reference

### Essential Commands

```bash
# View logs (Docker)
docker logs -f rs8kvn_bot

# View logs (binary)
journalctl -u rs8kvn_bot -f  # if using systemd

# Restart service
docker restart rs8kvn_bot
# or
systemctl restart rs8kvn_bot

# Check health
curl http://localhost:8880/healthz
curl http://localhost:8880/readyz

# Check database
sqlite3 ./data/tgvpn.db "SELECT COUNT(*) FROM subscriptions;"

# List backups
ls -lh ./data/*.backup*

# Test bot manually
go run ./cmd/bot
# or
./rs8kvn_bot
```

### Configuration Files

| File | Purpose |
|------|---------|
| `.env` | Environment variables (secrets) |
| `data/tgvpn.db` | SQLite database |
| `data/bot.log` | Application logs (rotated) |
| `data/subserver.log` | Optional `/sub/{id}` access log |
| `internal/database/migrations/` | DB migration SQL files |

---

## 2. Upgrade

### 2.1 Docker (recommended)

**Zero-downtime upgrade:**

```bash
# 1. Pull new image
docker pull ghcr.io/kereal/rs8kvn_bot:latest

# 2. Stop old container
docker stop rs8kvn_bot

# 3. Remove old container
docker rm rs8kvn_bot

# 4. Start new container (same command as installation)
docker run -d \
  --name rs8kvn_bot \
  --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  -p 127.0.0.1:8880:8880 \
  ghcr.io/kereal/rs8kvn_bot:latest
```

**Using Docker Compose:**

```bash
docker-compose pull
docker-compose up -d
```

**Rollback:** If something goes wrong, specify previous tag:
```bash
docker run ... ghcr.io/kereal/rs8kvn_bot:v2.2.0
```

### 2.2 Binary (manual build)

```bash
# 1. Pull latest code
git pull origin dev

# 2. Build
go build -ldflags="-s -w -X main.version=$(git describe --tags --abbrev=0) -X main.commit=$(git rev-parse --short HEAD) -X main.buildTime=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" -o rs8kvn_bot ./cmd/bot

# 3. Stop old process (find PID)
pkill -f rs8kvn_bot
# or systemctl stop rs8kvn_bot

# 4. Start new
./rs8kvn_bot &
```

### 2.3 Database Migrations

Migrations run automatically on startup. If migration fails:
- Bot logs error and exits (fatal)
- Database remains in previous state (safe)
- Check `data/tgvpn.db` integrity

**Manual migration:**
```bash
# Apply migrations using golang-migrate CLI
migrate -path internal/database/migrations -database "sqlite3://data/tgvpn.db" up
```

**Rollback migration:**
```bash
migrate -path internal/database/migrations -database "sqlite3://data/tgvpn.db" down 1
```

---

## 3. Backup & Restore

### 3.1 Automatic Backups

**Configured:**
- **Schedule:** Daily at 03:00 (config `DefaultBackupHour`)
- **Retention:** 14 days
- **Location:** Same directory as database (`./data/`)
- **Naming:** `tgvpn.db.backup.YYYYMMDD_HHMMSS`

**What gets backed up:**
- WAL checkpoint executed first → consistent snapshot
- Full copy of `tgvpn.db`
- Not compressed (fast restore)

### 3.2 Manual Backup

```bash
# Trigger immediately (if bot running, send signal)
# Or run backup tool directly:
go run ./internal/backup/backup.go -db ./data/tgvpn.db -out ./data/manual.backup

# Or copy file manually (ensure WAL checkpoint first):
sqlite3 ./data/tgvpn.db "PRAGMA wal_checkpoint(TRUNCATE);"
cp ./data/tgvpn.db ./data/backup.manual
```

### 3.3 Restore from Backup

```bash
# 1. Stop bot
docker stop rs8kvn_bot

# 2. Rename current DB (for safety)
mv ./data/tgvpn.db ./data/tgvpn.db.before-restore-$(date +%s)

# 3. Copy backup over
cp ./data/tgvpn.db.backup.20260417_030000 ./data/tgvpn.db

# 4. Fix permissions (if needed)
chown 1000:1000 ./data/tgvpn.db  # Docker user
# or chown current-user if running binary

# 5. Start bot
docker start rs8kvn_bot
```

**Verify:**
```bash
sqlite3 ./data/tgvpn.db "SELECT COUNT(*) FROM subscriptions;"
docker logs rs8kvn_bot | grep -i "database initialized"
```

### 3.4 Off-site Backups (recommended)

Currently backups are local only. For production, configure remote storage:

**Options:**
1. **S3-compatible** (MinIO, Backblaze B2, AWS S3):
   ```bash
   # Sync to S3 daily (cron)
   aws s3 cp ./data/tgvpn.db.backup.* s3://your-bucket/backups/ --storage-class STANDARD_IA
   ```

2. **rsync to remote server:**
   ```bash
   rsync -avz ./data/tgvpn.db.backup.* user@remote:/backup/rs8kvn/
   ```

3. **Encrypted backup** (GPG):
   ```bash
   gpg --symmetric --cipher-algo AES256 -o backup.gpg ./data/tgvpn.db.backup
   ```

---

## 4. Monitoring

### 4.1 Health Checks

**Endpoints** (port 8880):

| Endpoint | Description | Success | Failure |
|----------|-------------|---------|---------|
| `GET /healthz` | Liveness: DB ping + XUI health | 200 | 503 |
| `GET /readyz` | Readiness: all components initialized | 200 | 503 |

**Usage:**
```bash
curl -f http://localhost:8880/healthz || echo "DOWN"
curl -f http://localhost:8880/readyz || echo "NOT READY"
```

**Kubernetes probes:**
```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8880
  initialDelaySeconds: 30
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 8880
  initialDelaySeconds: 5
  periodSeconds: 5
```

### 4.2 Heartbeat

If `HEARTBEAT_URL` configured, bot POSTs `{}` every `HEARTBEAT_INTERVAL` seconds (default: 300s).

**Verify:**
```bash
# Check recent heartbeat in logs
docker logs rs8kvn_bot 2>&1 | grep "Heartbeat sent"

# Monitor endpoint (UptimeRobot, healthchecks.io)
# Provide URL: http://your-host:8880/healthz
```

### 4.3 Subscription Access Log

If `SUBSERVER_ACCESS_LOG` is set, the bot appends every `/sub/{id}` request to that file as a tab-separated (TSV) line (no zap-console encoding, no message/caller/field keys). Fields are separated by tab characters; empty optional values are written as empty fields (not `-`). Access log writes are buffered asynchronously so disk I/O does not block subscription responses. If the file cannot be opened, the bot continues without the access log and writes an error to the main application log. Each line contains, tab-separated:

- `timestamp` — request time in UTC (`2006-01-02T15:04:05.000Z0700`)
- `level` — always `INFO`
- `method` — HTTP method (e.g. `GET`)
- `request_uri` — full request URI (e.g. `/sub/{id}`)
- `status_code` — response status code
- `client_ip` — client IP address
- `X-Hwid`, `X-Device-Os`, `X-Ver-Os`, `X-Device-Model`, and `User-Agent` request headers (sanitised: CR/LF/TAB replaced with spaces)

The main application log records an INFO message when this access log is enabled. To disable it, set `SUBSERVER_ACCESS_LOG` to an empty value and restart the bot.

### 4.4 Logs

**Location:** `./data/bot.log` (rotated: max 100MB × 3 files)

**Log rotation:**
- Daily rotation at midnight (if using lumberjack)
- Manual rotation: `kill -USR1 <pid>` (not currently configured)
- Or via logrotate (system):

```conf
# /etc/logrotate.d/rs8kvn_bot
./data/bot.log {
    daily
    rotate 7
    compress
    missingok
    notifempty
    copytruncate
}
```

**Log levels:** Set `LOG_LEVEL=debug` for verbose, `info` for production.

**View logs:**
```bash
# Follow
tail -f ./data/bot.log | jq .  # if JSON

# Search errors
grep '"level":"error"' ./data/bot.log

# Last 100 lines
tail -n 100 ./data/bot.log
```

### 4.5 Sentry

If `SENTRY_DSN` configured, errors and panics are reported automatically.

**Features:**
- Release tracking (`rs8kvn_bot@v2.3.0`)
- Performance traces (enabled via `SENTRY_TRACES_SAMPLE_RATE`)
- Stack traces on panic

**Test:**
```bash
# Trigger test panic (temporary)
kill -ABRT <pid>
# Check Sentry dashboard for new issue
```

### 4.6 Prometheus Metrics

The bot exposes a `/metrics` endpoint on the same port as health checks (default 8880).

**Endpoint:** `GET /metrics`

**Key metrics:**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | Counter | method, path, status | Total HTTP requests |
| `http_request_duration_seconds` | Histogram | method, path | HTTP request latency |
| `http_requests_in_flight` | Gauge | method, path | Current in-flight requests |
| `bot_updates_total` | Counter | command, result | Bot updates processed (success/error/rate_limited) |
| `bot_update_errors_total` | Counter | type | Bot update errors |
| `bot_update_duration_seconds` | Histogram | — | Bot update processing time |
| `cache_hits_total` / `cache_misses_total` | Counter | cache | Cache hit/miss (subscription, referral, subserver) |
| `circuit_breaker_state` | Gauge | target | Circuit breaker state (0=closed, 1=open, 2=half-open) |
| `bot_orphaned_clients_removed_total` | Counter | — | Orphaned XUI clients removed |
| `subserver_source_fetch_total` | Counter | result, format | Upstream source fetch results (success/error by format) |
| `subserver_source_fetch_duration_seconds` | Histogram | result | Upstream source fetch duration |
| `subserver_cache_invalidations_total` | Counter | reason | Cache invalidations by reason |
| `subserver_no_items_total` | Counter | — | Requests returning no items |

**Prometheus scrape config:**
```yaml
scrape_configs:
  - job_name: 'rs8kvn_bot'
    static_configs:
      - targets: ['localhost:8880']
    metrics_path: /metrics
```

**Grafana dashboard ideas:**
- HTTP error rate: `rate(http_requests_total{status=~"5.."}[5m]) / rate(http_requests_total[5m])`
- Bot error rate: `rate(bot_updates_total{result="error"}[5m])`
- XUI latency p99: `histogram_quantile(0.99, rate(xui_request_duration_seconds_bucket[5m]))`
- Active subscriptions: `active_subscriptions`


## 5. Troubleshooting

### 5.1 Bot won't start

**Symptom:** Process exits immediately, logs show "Failed to ..."

**Common causes:**

| Error | Cause | Fix |
|-------|-------|-----|
| `Failed to load config: TELEGRAM_BOT_TOKEN is required` | Missing env var | Set in `.env` or export |
| `Failed to initialize database` | Permission denied or disk full | Check `data/` dir, permissions |
| `Failed to connect to 3x-ui panel` | Wrong node host, panel down | Verify `nodes.host` in DB, then `curl <panel_host>` |
| `listen tcp :8880: bind: address already in use` | Port occupied | Change `HEALTH_CHECK_PORT` or kill process using port |

**Diagnostic:**
```bash
# Check config
cat .env | grep -v '^#'

# Test DB connection
sqlite3 ./data/tgvpn.db "SELECT 1;"

# Test XUI panel
curl -H "Authorization: Bearer <api_token>" "<panel_host>/panel/api/server/status"

# Check port
lsof -i :8880
netstat -tlnp | grep 8880
```

---

### 5.2 Bot starts but doesn't respond to messages

**Symptom:** `/start` sends no reply.

**Check:**
1. **Bot token valid?** Verify with @BotFather
2. **Webhook configured?** Bot uses `GetUpdates`, so webhook must be deleted:
   ```bash
   curl "https://api.telegram.org/bot$TOKEN/deleteWebhook"
   ```
3. **Bot is running?** `docker ps`, `ps aux | grep rs8kvn_bot`
4. **Updates received?** Check logs: `logger.Info("Received update")`
5. **Rate limited?** Check `ratelimiter` metrics (not exposed yet)

**Fix:**
```bash
# Reset webhook
curl "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/deleteWebhook"

# Restart bot
docker restart rs8kvn_bot
```

---

### 5.3 Subscription creation fails

**Symptom:** User clicks "Get subscription" → error message.

**Common errors:**

| Error | Cause | Fix |
|-------|-------|-----|
| `failed to create client` | 3x-ui unreachable | Check `nodes.host` and `nodes.api_token` in DB |
| `context deadline exceeded` | XUI slow response | Increase timeout in `config` |
| `cannot allocate UUID` | DB full or read-only | Check disk space, DB permissions |

**Debug steps:**
```bash
# Check XUI directly
curl -H "Authorization: Bearer <api_token>" "<panel_host>/panel/api/inbounds/list"

# Check DB state
sqlite3 ./data/tgvpn.db "SELECT * FROM subscriptions WHERE telegram_id = <user_id> ORDER BY created_at DESC LIMIT 5;"

# Watch logs while testing
docker logs -f rs8kvn_bot 2>&1 | grep -E "error|failed|subscription"
```

> **Note:** Node configuration is managed via the `nodes` table in the database. Check node state:
```sql
SELECT id, name, type, is_active, host, subscription_url FROM nodes;
SELECT sn.* FROM subscription_nodes sn WHERE sn.subscription_id = (SELECT id FROM subscriptions WHERE subscription_id = 'subID');
```

---

### 5.4 Trial page returns 500

**Symptom:** `/i/INVITECODE` shows "Internal Server Error".

**Check:**
1. **Invite code valid?** Query DB:
   ```sql
   SELECT * FROM invites WHERE code = 'INVITECODE';
   ```
2. **IP rate limited?** Check `trial_requests` table:
   ```sql
   SELECT COUNT(*) FROM trial_requests WHERE ip = 'user_ip' AND created_at > datetime('now','-1 hour');
   ```
3. **XUI reachable?** (same as above)
4. **File permission:** `data/` writable for creating trial clients

**Logs to check:**
```bash
grep "trial" ./data/bot.log | tail -50
```

---

### 5.5 `/sub/{subID}` returns 502/empty

**Symptom:** Subscription link doesn't work.

**Possible causes:**
- XUI panel down
- `subID` invalid/expired
- Cache corrupted

**Fix:**
```bash
# Check subID in DB
sqlite3 ./data/tgvpn.db "SELECT subscription_id, status, expires_at FROM subscriptions WHERE subscription_id = 'abc123';"

# Invalidate cache (restart bot)
docker restart rs8kvn_bot

# Check proxy logs
docker logs rs8kvn_bot | grep "subserver"
```

**Check node state:**
```sql
-- Check subscription and its nodes
SELECT s.subscription_id, s.status, n.name, n.type, n.subscription_url, n.is_active
FROM subscriptions s
JOIN subscription_nodes sn ON sn.subscription_id = s.id
JOIN nodes n ON n.id = sn.node_id
WHERE s.subscription_id = 'your-sub-id';

-- Check sync state
SELECT subscription_id, node_id, status, retry_count, last_error
FROM subscription_nodes
WHERE subscription_id = (SELECT id FROM subscriptions WHERE subscription_id = 'your-sub-id');
```

---

### 5.6 High memory usage

**Symptom:** Container/binary uses >200MB RAM.

**Common causes:**
- Large broadcast in progress
- Cache size too large (default 1000, could increase)
- Memory leak (goroutine leak)

**Diagnose:**
```bash
# Docker stats
docker stats rs8kvn_bot

# Go heap (if binary, send SIGUSR1 to get heap profile)
kill -USR1 <pid>
# profile in /tmp

# Count goroutines
curl http://localhost:8880/debug/pprof/goroutine?debug=1  # if enabled
```

**Mitigation:**
- Increase `GOGC` (default 100, lower = more frequent GC)
- Reduce `CacheMaxSize` (recompile)
- Restart bot (graceful shutdown respects in-flight handlers)

---

### 5.7 Database locked / slow queries

**Symptom:** Logs show "database is locked" or timeouts.

**Cause:** SQLite allows only 1 writer. Concurrent writes queue.

**Solutions:**
1. **Tune connection pool:** Already set `MaxOpenConns=1`. Check if other processes accessing DB.
2. **Reduce write frequency:** Trial cleanup runs hourly, backup daily — OK.
3. **Consider PostgreSQL** if >10k writes/day.

**Check locking:**
```bash
sqlite3 ./data/tgvpn.db "PRAGMA wal_checkpoint(TRUNCATE);"
sqlite3 ./data/tgvpn.db "PRAGMA journal_mode=WAL;"
```

---

### 5.8 XUI API errors (retry with backoff)

**Symptom:** XUI API calls failing with transient errors.

**Log:** `"XUI API error"`, `"retrying after backoff"`, or similar.

**Note:** The system uses `RetryWithBackoff` with exponential backoff + jitter (up to 3 retries) and a circuit breaker (5 failures → 30s open → half-open with 3 attempts). Monitor `circuit_breaker_state` metric (0=closed, 1=open, 2=half-open).

**Fix:**
1. Check 3x-ui panel is up: `curl -H "Authorization: Bearer <api_token>" "<panel_host>/panel/api/server/status"`
2. Verify `nodes.host` and `nodes.api_token` in database are correct
3. Check panel logs for errors
4. Retries happen automatically — check logs after ~30s for recovery
5. DNS errors will fast-fail without retries

**For persistent issues:** Restart bot to clear any cached state.

---

### 5.9 Logs fill disk

**Symptom:** `data/bot.log` grows beyond 100MB.

**Cause:** Rotation misconfigured or log level `debug` with high traffic.

**Fix:**
```bash
# Truncate log (safe, lumberjack will continue)
> ./data/bot.log

# Adjust rotation in logger config (code: internal/logger/logger.go)
# MaxSize: 100 MB, MaxBackups: 3, MaxAge: 7 days
```

**Logrotate alternative:** See section 4.3.

---

### 5.10 Subscription sync stuck in pending state

**Symptom:** Subscription nodes remain in `pending_add`, `pending_remove`, or `pending_update` status.

**Check:**
```sql
-- Find stuck subscription nodes
SELECT subscription_id, node_id, status, retry_count, retry_at, last_error, updated_at
FROM subscription_nodes
WHERE status LIKE 'pending_%'
ORDER BY updated_at DESC;
```

**Common causes:**
- VPN node unreachable (check node host and API token in `nodes` table)
- XUI panel rejecting client creation (inbound ID mismatch)
- Network timeout to VPN node

**Fix:**
1. Check node connectivity: verify `nodes` table has correct host/api_token
2. Review `last_error` column for specific error messages
3. The background `SyncPendingNodes` worker retries with exponential backoff automatically
4. For persistent stuck states, restart bot to reset worker state
5. Check `bot_orphaned_clients_removed_total` metric for orphan reconciliation activity

---

## 6. Scaling

### 6.1 Current Limitations

| Resource | Limit | Notes |
|----------|-------|-------|
| DB writes | ~1/sec | SQLite single-writer |
| Concurrent handlers | 10 | Config `MaxConcurrentHandlers` |
| Cache size | 1000 subs | `CacheMaxSize` constant |
| Users | ~10k comfortably | Memory: ~100 bytes/user |

### 6.2 When to Scale

- **>10k active users:** Consider PostgreSQL
- **>50k users:** Consider read replica, sharding by inbound
- **High write load:** Multiple XUI panels (shard by user ID hash)

### 6.3 Horizontal Scaling (multiple bot instances)

**Not supported for polling mode** — Telegram only allows one `getUpdates` connection per bot token.

**To scale horizontally:**
1. Switch to **webhook mode** (requires public HTTPS URL)
2. Put multiple bot instances behind load balancer
3. Share same database (PostgreSQL required)
4. Use Redis for distributed cache (instead of in-memory)

**Complexity:** High. Not recommended for <100k users.

### 6.4 Vertical Scaling

**Increase resources:**
- **Memory:** 128MB → 512MB (if cache increased)
- **CPU:** 0.5 cores → 1-2 cores (if high traffic)
- **Disk:** SSD recommended for SQLite performance

**Docker:**
```yaml
deploy:
  resources:
    limits:
      memory: 512M
      cpus: "1.0"
```

---

## 7. Security Hardening

### 7.1 Checklist

- [ ] Use HTTPS for node panel URLs (`nodes.host`, no HTTP in production)
- [ ] Set strong `TELEGRAM_BOT_TOKEN` (20+ chars, random)
- [ ] Restrict `.env` permissions: `chmod 600 .env`
- [ ] Run Docker with `--read-only` (not possible due to SQLite writes)
- [ ] Enable `no-new-privileges:true` (already in docker-compose)
- [ ] Use non-root user (already in Dockerfile)
- [ ] Regular OS security updates
- [ ] Enable Sentry for error monitoring
- [ ] Configure firewall: only 8880 port open to internet (optional, health checks)
- [ ] Backup encryption (if using S3, enable SSE)
- [ ] Monitor `/healthz` with external service (UptimeRobot, healthchecks.io)
- [ ] Review logs daily for `error`/`warn` level

### 7.2 Secret Management

**Do NOT:**
- Commit `.env` to git (in `.gitignore`)
- Share secrets in logs
- Use default credentials or tokens

**Do:**
- Use secrets manager (HashiCorp Vault, AWS Secrets Manager)
- Rotate XUI API token every 90 days
- Rotate Telegram bot token if exposed

### 7.3 Network Security

**Recommended reverse proxy (nginx):**
```nginx
server {
    listen 443 ssl http2;
    server_name vpn.example.com;

    location /healthz {
        proxy_pass http://localhost:8880;
        allow 127.0.0.1;  # Only local
        deny all;          # Block external health check probes
    }

    location /readyz {
        proxy_pass http://localhost:8880;
        allow 127.0.0.1;
        deny all;
    }

    # /sub and /i endpoints can be public
    location /sub/ {
        proxy_pass http://localhost:8880;
        limit_req zone=sub burst=10 nodelay;
    }

    location /i/ {
        proxy_pass http://localhost:8880;
        limit_req zone=trial burst=5 nodelay;
    }
}
```

**Rate limiting nginx:**
```nginx
limit_req_zone $binary_remote_addr zone=trial:10m rate=3r/m;
limit_req_zone $binary_remote_addr zone=sub:10m rate=30r/m;
```

---

## 8. Support

**Issues:** https://github.com/kereal/rs8kvn_bot/issues  
**Security:** security@kereal.me (example — configure real email)

**Before reporting:**
1. Check logs in `data/bot.log`
2. Verify configuration (`.env`)
3. Test 3x-ui panel connectivity
4. Include version: `./rs8kvn_bot --version` or logs startup line

---

*Last updated: 2026-07-02*
