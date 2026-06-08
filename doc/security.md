# Security Policy — rs8kvn_bot

**Version:** 1.0  
**Last updated:** 2026-04-17

---

## Supported Versions

| Version | Supported | Security Updates |
|---------|-----------|------------------|
| v2.3.x  | ✅ Yes | ✅ Active |
| v2.2.x  | ⚠️ Partial (critical only) | ⚠️ Limited |
| < v2.2  | ❌ No | ❌ End-of-life |

We recommend always running the latest stable version.

---

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, contact us privately:

📧 **Email:** security@kereal.me (example — replace with actual email)  
🔐 **PGP Key:** [Download our PGP key](https://kereal.me/pgp.asc) (optional)

**Include in report:**
1. Affected version(s)
2. Steps to reproduce
3. Proof of concept (if applicable)
4. Potential impact assessment
5. Suggested fix (optional)

**Response time:**
- Initial acknowledgment: **within 48 hours**
- Fix timeline: Depends on severity (critical: 72h, high: 7d, medium: 30d)
- Coordinated disclosure: We'll work with you on public announcement timing

---

## Security Measures Implemented

### 1. Authentication & Authorization

| Component | Protection |
|-----------|------------|
| Telegram bot | User identification via `chat_id`; admin commands check `TELEGRAM_ADMIN_ID` |
| API endpoint | Bearer token (`API_TOKEN`) with constant-time comparison |
| Webhook | Bearer secret (`PROXY_MANAGER_WEBHOOK_SECRET`) |
| 3x-ui panel | API token (`XUI_API_TOKEN`) via Bearer header over HTTPS |

### 2. Input Validation

- **XUI_SUB_PATH:** Regex validation `^[a-zA-Z0-9_-]+$` — no path traversal
- **Extra servers file:** Path traversal checks (`..`, system dirs) before opening
- **URLs:** HTTPS enforced for all sensitive endpoints (XUI_HOST, webhooks)
- **Invite codes:** Alphanumeric regex validation
- **Telegram IDs:** Integer parsing with overflow protection

### 3. Network Security

- **Non-root Docker container** (UID 1000)
- **`no-new-privileges:true`** — prevents privilege escalation
- **Read-only config volume** (`/.env:ro`)
- **Health checks** exposed only on localhost by default
- **RetryWithBackoff** on 3x-ui client handles transient failures
- **Rate limiting** per Telegram user (30 tokens, 5/sec refill)
- **IP rate limiting** on trial endpoint (3/hour per IP)

### 4. Data Protection

- **Database:** SQLite with WAL mode (atomic commits)
- **Migrations:** Embedded, no external files at runtime
- **Backups:** Daily rotation (14 days), same directory (consider off-site)
- **Logging:** Sensitive data masked in `Config.String()`; no secrets in logs
- **Secrets:** Stored in `.env` (not in code), file permissions recommended `600`

### 5. Error Handling

- **Panic recovery** in all goroutines (`recoverAndReport`)
- **Graceful shutdown** — in-flight requests allowed to complete
- **RetryWithBackoff** retries XUI calls on transient errors (exponential backoff + jitter, up to 3 retries)
- **Stale cache fallback** for Subscription server if XUI down

### 6. Code Quality

- **Static analysis:** `golangci-lint` with strict config, `gosec` security scanner
- **Race detection:** All tests run with `-race`
- **Fuzzing:** Markdown sanitization fuzz tests
- **Leak detection:** Goroutine leak tests in `tests/leak/`
- **Test coverage:** ~85%

### 7. XUI API Token Management

#### Token Lifecycle

1. **Creation:** Generate token in 3x-ui panel → Security settings → API Token. Copy immediately — token is shown only once.
2. **Storage:** Store in `.env` as `XUI_API_TOKEN`. File permissions must be `600`.
3. **Usage:** Sent as `Authorization: Bearer $XUI_API_TOKEN` header on every API call.
4. **Rotation:** Rotate every 90 days. Generate new token in panel, update `.env`, restart bot.
5. **Revocation:** Generate a new token in panel — the old token is immediately invalidated.

#### Audit & Monitoring

- Enable panel access logs (Security settings → Logging) to record all API actions
- Monitor bot logs for `XUI API error` or `401 Unauthorized` responses
- If a token is suspected compromised:
  1. Generate new token in panel immediately (old one stops working)
  2. Update `.env` and restart bot
  3. Check panel access logs for unauthorized activity
  4. Review the time window between compromise and revocation

#### Least Privilege

- XUI API token grants full panel API access — no scope/restriction mechanism exists in 3x-ui
- Mitigation: restrict network access to XUI panel (firewall, private VLAN) so token can only be used from bot host
- Future: if panel adds scoped tokens, use the most restrictive scope needed

---

## Security Hardening Checklist

### Before Deployment

- [ ] **Use HTTPS** for `XUI_HOST` (no HTTP in prod)
- [ ] Set `TELEGRAM_ADMIN_ID` to real admin user ID
- [ ] Generate strong `API_TOKEN` (32+ random chars)
- [ ] Set `PROXY_MANAGER_WEBHOOK_SECRET` if webhook enabled
- [ ] `.env` file permissions: `chmod 600 .env`
- [ ] Docker volumes: owned by non-root user (UID 1000)
- [ ] Enable `SENTRY_DSN` for error monitoring
- [ ] Configure firewall: block all except 127.0.0.1:8880 (or proxy through nginx)
- [ ] Run `gosec ./...` and fix HIGH severity findings
- [ ] Run `govulncheck ./...` and update vulnerable dependencies

### Operational Security

- [ ] **Rotate secrets** every 90 days (XUI API token, API_TOKEN, webhook secrets)
- [ ] Monitor `/healthz` with external service (UptimeRobot, healthchecks.io)
- [ ] Review logs daily for `error`/`warn` level
- [ ] Watch for `401` / `XUI API error` patterns indicating token issues
- [ ] Backup verification: monthly restore test
- [ ] OS security updates: at least monthly
- [ ] Enable audit logging on 3x-ui panel
- [ ] Use VPN/private network for XUI_HOST (not exposed to internet)
- [ ] Set `GOGC=40` (already in Dockerfile) to limit memory
- [ ] Limit container resources (memory 128M, CPU 0.5)

### Advanced Hardening

- [ ] **Reverse proxy (nginx)** in front with:
  - SSL termination
  - Rate limiting on `/i/` and `/sub/` endpoints
  - IP allowlist for `/healthz` (monitoring only)
  - Request size limits
- [ ] **SELinux/AppArmor** profile for container
- [ ] **Read-only root filesystem** (except `/app/data`)
- [ ] **Seccomp** profile: block unnecessary syscalls
- [ ] **Network segmentation:** Place bot and 3x-ui on private VLAN
- [ ] **Audit trail:** Forward logs to SIEM (Graylog, ELK)
- [ ] **WAF** (ModSecurity) if exposed to internet

---

## Known Security Limitations

| Limitation | Reason | Mitigation |
|------------|--------|------------|
| SQLite single-writer bottleneck | DB engine choice | Monitor write latency; migrate to PostgreSQL if >10k writes/day |
| No built-in Prometheus metrics | Not implemented yet | External monitoring via healthz + logs |
| No built-in DDoS protection | Layer 7 | Use nginx rate limiting or cloud WAF |
| No request signing for API | Bearer token only | Use HTTPS + rotate token regularly |
| Trial rate limit per IP (not per user) | IPs can be changed via VPN | Acceptable for free tier; paid users get unique links |
| Extra servers file path validation | Could be stricter (currently prevents traversal but allows any absolute path within readable FS) | Future: restrict to `./data/` subdirectory only |

---

## Incident Response

### If Bot is Compromised

1. **Isolate:** Stop container / kill process
2. **Preserve evidence:** Copy logs (`data/bot.log`), DB (`tgvpn.db`), memory dump if possible
3. **Analyze:** Check logs for unauthorized access, unusual API calls, new admin users in Telegram
4. **Rotate secrets:**
   - New Telegram bot token (via @BotFather)
   - New 3x-ui credentials
   - New `API_TOKEN`
   - New `PROXY_MANAGER_WEBHOOK_SECRET`
5. **Restore from clean backup:** If DB tampered, restore from known-good backup
6. **Update:** Deploy latest version with security fixes
7. **Notify:** Inform users if personal data potentially exposed

### If XUI Panel Compromised

1. XUI panel is separate system — follow its security procedures
2. **Revoke the XUI API token** immediately in panel Security settings → generate a new one
3. Update `XUI_API_TOKEN` in `.env` and restart the bot
4. Check panel logs for unauthorized client modifications
5. Audit all recent changes made via the compromised token (panel access logs)
6. If shared/leaked token is suspected, also rotate `API_TOKEN` and `PROXY_MANAGER_WEBHOOK_SECRET`

### Data Breach Notification

If subscriber data (Telegram IDs, usernames, subscription links) is exposed:
- Assess scope: which tables affected?
- Notify affected users via Telegram (if possible)
- Rotate all subscription links (force re-issue — requires DB migration)
- Review access controls

---

## Secure Configuration Example

`.env` production example:
```bash
# Telegram
TELEGRAM_BOT_TOKEN=1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi
TELEGRAM_ADMIN_ID=123456789

# 3x-ui (HTTPS mandatory!)
XUI_HOST=https://vpn.example.com:2053
XUI_API_TOKEN=your_xui_api_token_here  # Replace with your API token from panel Security settings
XUI_INBOUND_ID=1
XUI_SUB_PATH=rs8vn4876  # Random string, not "sub"

# Database (on persistent volume)
DATABASE_PATH=/app/data/tgvpn.db

# Logging
LOG_LEVEL=warn  # Less verbose in prod
LOG_FILE_PATH=/app/data/bot.log

# Subscription

# Monitoring
HEARTBEAT_URL=https://healthchecks.io/ping/your-uuid
HEARTBEAT_INTERVAL=300
SENTRY_DSN=https://your-key@o123456.ingest.sentry.io/1234567
HEALTH_CHECK_PORT=8880

# Trial
TRIAL_DURATION_HOURS=3
TRIAL_RATE_LIMIT=3

# API (generate random 64-char hex)
API_TOKEN=$(openssl rand -hex 32)

# Webhook (if used)
PROXY_MANAGER_WEBHOOK_SECRET=$(openssl rand -hex 32)
PROXY_MANAGER_WEBHOOK_URL=https://api.example.com/webhook
```

**Docker run:**
```bash
docker run -d \
  --name rs8kvn_bot \
  --restart unless-stopped \
  --security-opt no-new-privileges:true \
  --pids-limit=100 \
  --memory=128M \
  --memory-swap=256M \
  --cpus=0.5 \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  -p 127.0.0.1:8880:8880 \
  ghcr.io/kereal/rs8kvn_bot:latest
```

**Docker daemon hardening** (`/etc/docker/daemon.json`):
```json
{
  "userns-remap": "default",
  "no-new-privileges": true,
  "default-ulimits": {
    "nofile": {"Name": "nofile", "Hard": 65535, "Soft": 65535}
  },
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
```

---

## Dependency Security

**Current dependencies** (selected):

| Package | Version | Known vulns? |
|---------|---------|--------------|
| `golang.org/x/sync` | v0.20.0 | None |
| `gorm.io/gorm` | v1.31.1 | None |
| `github.com/mattn/go-sqlite3` | v1.14.42 | None (CVE-2023-32636 patched) |
| `go.uber.org/zap` | v1.27.1 | None |

**Check regularly:**
```bash
govulncheck ./...
```

**Update dependencies:**
```bash
go get -u ./...
go mod tidy
go mod verify
```

---

## Security Audits

**Past audits:**
- 2026-04-17: Internal audit (SEC-01..SEC-12) — see `docs/review-sf35.md`

**Future audits:** Schedule annual external pentest if handling >10k users.

---

## Contact

Security issues: **security@kereal.me** (example)  
General questions: GitHub Issues (public)

*Last updated: 2026-04-17*
