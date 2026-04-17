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
| 3x-ui panel | Basic auth (username/password) over HTTPS (enforced) |

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
- **Circuit breaker** on 3x-ui client prevents DoS on upstream
- **Rate limiting** per Telegram user (30 tokens, 5/sec refill)
- **IP rate limiting** on trial endpoint (3/hour per IP)

### 4. Data Protection

- **Database:** SQLite with WAL mode (atomic commits)
- **Migrations:** Embedded, no external files at runtime
- **Backups:** Daily rotation (14 days), same directory (consider off-site)
- **Logging:** Sensitive data masked in `Config.String()`; no passwords in logs
- **Secrets:** Stored in `.env` (not in code), file permissions recommended `600`

### 5. Error Handling

- **Panic recovery** in all goroutines (`recoverAndReport`)
- **Graceful shutdown** — in-flight requests allowed to complete
- **Circuit breaker** opens after 5 consecutive XUI failures
- **Retry with exponential backoff** on transient XUI errors
- **Stale cache fallback** for subscription proxy if XUI down

### 6. Code Quality

- **Static analysis:** `golangci-lint` with strict config, `gosec` security scanner
- **Race detection:** All tests run with `-race`
- **Fuzzing:** Markdown sanitization fuzz tests
- **Leak detection:** Goroutine leak tests in `tests/leak/`
- **Test coverage:** ~85%

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

- [ ] **Rotate secrets** every 90 days (XUI password, API tokens)
- [ ] Monitor `/healthz` with external service (UptimeRobot, healthchecks.io)
- [ ] Review logs daily for `error`/`warn` level
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
2. Rotate all panel admin passwords
3. Check panel logs for unauthorized client modifications
4. Bot will auto-relogin on next request (session invalidated)

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
XUI_USERNAME=bot_admin
XUI_PASSWORD=${XUI_PASSWORD_FROM_VAULT}  # Use secret manager
XUI_INBOUND_ID=1
XUI_SUB_PATH=rs8vn4876  # Random string, not "sub"

# Database (on persistent volume)
DATABASE_PATH=/app/data/tgvpn.db

# Logging
LOG_LEVEL=warn  # Less verbose in prod
LOG_FILE_PATH=/app/data/bot.log

# Subscription
TRAFFIC_LIMIT_GB=30

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
