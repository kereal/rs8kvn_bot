# Security Policy — rs8kvn_bot

**Version:** 2.3.0  
**Last updated:** 2026-07-02

---

## Supported Versions

| Version | Supported | Security Updates |
|---------|-----------|------------------|
| v3.0.x  | ✅ Yes | ✅ Active |
| v2.3.x  | ⚠️ Partial (critical only) | ⚠️ Limited |
| < v2.3  | ❌ No | ❌ End-of-life |

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
| VPN panels (3x-ui / proxman) | API token per node (`nodes.api_token`) via Bearer header over HTTPS |
| Fetch nodes | No API token; `subscription_url` fetched via HTTP GET (no client management) |
| Trial Telegram IDs | Positive = bound users, Negative = unbound trial subscriptions, 0 = unused |

### 2. Input Validation

- **Extra servers file:** Path traversal checks (`..`, system dirs) before opening
- **URLs (S3):** `validateURL()` restricts schemes to `http` and `https` only — prevents `file://`, `gopher://`, and other schemes that could enable SSRF
- **HTTPS enforcement:** All sensitive VPN panel endpoints (`nodes.host`) require HTTPS
- **Invite codes:** Alphanumeric regex validation
- **Telegram IDs:** Integer parsing with overflow protection
- **Type-safe env vars:** `internal/flag/` typed registry validates and parses all env vars at config load time

### 3. Network Security

- **Non-root Docker container** (UID 1000)
- **`no-new-privileges:true`** — prevents privilege escalation
- **Read-only config volume** (`/.env:ro`)
- **Health checks** exposed only on localhost by default
- **RetryWithBackoff** on VPN client handles transient failures
- **Rate limiting** per Telegram user (30 tokens, 5/sec refill)
- **IP rate limiting** on trial endpoint (3/hour per IP)
- **Trusted proxy IP extraction (S2):** `getClientIP()` uses the **rightmost** IP from `X-Forwarded-For` (set by the trusted reverse proxy) instead of the leftmost value (which is client-controllable and spoofable). This prevents IP spoofing to bypass rate limits.

### 4. Data Protection

- **Database:** SQLite with WAL mode (atomic commits)
- **Migrations:** Embedded, no external files at runtime
- **Backups:** Daily rotation (14 days), same directory (consider off-site)
- **Logging:** Sensitive data masked in `Config.String()`; no secrets in logs
- **Secrets:** Stored in `.env` (not in code), file permissions recommended `600`
- **Node API tokens:** Stored and managed only via `nodes.api_token` in the database (no env-var dependency)

### 5. Error Handling

- **Panic recovery** in all goroutines (`recoverAndReport`)
- **Graceful shutdown** — in-flight requests allowed to complete
- **RetryWithBackoff** retries VPN panel calls on transient errors (exponential backoff + jitter, up to 3 retries)
- **Stale cache fallback** for Subscription server if panel is down
- **Circuit breaker** state tracked via Prometheus metrics (`circuit_breaker_state`)

### 6. Code Quality

- **Static analysis:** `golangci-lint` with strict config, `gosec` security scanner
- **Race detection:** All tests run with `-race`
- **Fuzzing:** Markdown sanitization fuzz tests
- **Leak detection:** Goroutine leak tests in `tests/leak/`
- **Test coverage:** ~85%
- **Prometheus metrics** (`internal/metrics/`): HTTP requests, bot updates, XUI/VPN panel requests, DB queries, cache hits/misses, circuit breaker state, active subscriptions, trial conversions, orphaned clients removed — full observability of security-relevant behavior

### 7. Architecture Security

- **Decoupled web layer (A1):** The `web` package no longer imports the `bot` package. `NewServer` accepts a `botUsername string` instead of `*bot.BotConfig`, reducing coupling and attack surface — a compromise of the web/subscription server cannot reach into bot internals.
- **VPN abstraction layer:** `internal/vpn/` defines a `Client` interface (`CreateSubscription`, `UpdateSubscription`, `DeleteSubscription`) with a factory (`NewClient()`) that routes by `NodeType` (3x-ui, proxman, fetch). Panel-specific logic is isolated; adding a new panel type does not touch service code. Fetch nodes use a no-op client (all methods return `nil`) and serve proxy data via direct HTTP GET to `subscription_url`.
- **Error classification:** Typed errors (`ErrSubscriptionAlreadyExists`, `ErrSubscriptionNotFound`) prevent information leakage through generic error messages.

### 8. VPN Panel API Token Management

#### Token Lifecycle

1. **Creation:** Generate token in panel (3x-ui: Security settings → API Token; proxman: equivalent). Copy immediately — token is shown only once.
2. **Storage:** Stored in `nodes.api_token` in DB. `.env` file permissions must be `600` if used for other secrets.
3. **Usage:** Sent as `Authorization: Bearer $token` header on every API call.
4. **Rotation:** Rotate every 90 days. Generate new token in panel, update `nodes.api_token` in DB, restart bot.
5. **Revocation:** Generate a new token in panel — the old token is immediately invalidated.

#### Audit & Monitoring

- Enable panel access logs to record all API actions
- `SUBSERVER_ACCESS_LOG` env var enables subscription server access logging
- Monitor bot logs for `XUI API error` or `401 Unauthorized` responses
- If a token is suspected compromised:
  1. Generate new token in panel immediately (old one stops working)
  2. Update DB / `.env` and restart bot
  3. Check panel access logs for unauthorized activity
  4. Review the time window between compromise and revocation

#### Least Privilege

- Panel API tokens grant full panel API access — no scope/restriction mechanism exists in 3x-ui or proxman
- Mitigation: restrict network access to panel (firewall, private VLAN) so token can only be used from bot host
- Future: if panels add scoped tokens, use the most restrictive scope needed

---

## Security Hardening Checklist

### Before Deployment

- [ ] **Use HTTPS** for all node hosts (`nodes.host`, no HTTP in prod)
- [ ] Set `GLOBAL_SUB_URL` to your subscription server's public HTTPS URL
- [ ] Set `TELEGRAM_ADMIN_ID` to real admin user ID
- [ ] `.env` file permissions: `chmod 600 .env`
- [ ] Docker volumes: owned by non-root user (UID 1000)
- [ ] Enable `SENTRY_DSN` for error monitoring
- [ ] Configure firewall: block all except 127.0.0.1:8880 (or proxy through nginx)
- [ ] Run `gosec ./...` and fix HIGH severity findings
- [ ] Run `govulncheck ./...` and update vulnerable dependencies

### Operational Security

- [ ] **Rotate secrets** every 90 days (panel API tokens)
- [ ] Monitor `/healthz` with external service (UptimeRobot, healthchecks.io)
- [ ] Monitor Prometheus metrics for anomalies (circuit breaker trips, error rate spikes)
- [ ] Review logs daily for `error`/`warn` level
- [ ] Watch for `401` / `XUI API error` patterns indicating token issues
- [ ] Backup verification: monthly restore test
- [ ] OS security updates: at least monthly
- [ ] Enable audit logging on all VPN panels
- [ ] Use VPN/private network for panel hosts (not exposed to internet)
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
- [ ] **Network segmentation:** Place bot and VPN panels on private VLAN
- [ ] **Audit trail:** Forward logs to SIEM (Graylog, ELK)
- [ ] **WAF** (ModSecurity) if exposed to internet

---

## Known Security Limitations

| Limitation | Reason | Mitigation |
|------------|--------|------------|
| SQLite single-writer bottleneck | DB engine choice | Monitor write latency; migrate to PostgreSQL if >10k writes/day |
| No built-in DDoS protection | Layer 7 | Use nginx rate limiting or cloud WAF |
| No request signing for API | Bearer token only | Use HTTPS + rotate token regularly |
| Trial rate limit per IP (not per user) | IPs can be changed via VPN | Acceptable for free tier; paid users get unique links |
| Extra servers file path validation | Could be stricter (currently prevents traversal but allows any absolute path within readable FS) | Future: restrict to `./data/` subdirectory only |
| Fetch node SSRF | `subscription_url` can point to arbitrary internal addresses | Restrict via firewall; do not use fetch nodes with untrusted URLs; validate `subscription_url` points to expected hosts |

---

## Incident Response

### If Bot is Compromised

1. **Isolate:** Stop container / kill process
2. **Preserve evidence:** Copy logs (`data/bot.log`), DB (`tgvpn.db`), memory dump if possible
3. **Analyze:** Check logs for unauthorized access, unusual API calls, new admin users in Telegram
4. **Rotate secrets:**
   - New Telegram bot token (via @BotFather)
   - New VPN panel credentials (all nodes)
5. **Restore from clean backup:** If DB tampered, restore from known-good backup
6. **Update:** Deploy latest version with security fixes
7. **Notify:** Inform users if personal data potentially exposed

### If VPN Panel Compromised

1. Panel is separate system — follow its security procedures
2. **Revoke the panel API token** immediately in panel Security settings → generate a new one
3. Update `nodes.api_token` in DB and restart the bot
4. Check panel logs for unauthorized client modifications
5. Audit all recent changes made via the compromised token (panel access logs)

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

# VPN Panel configuration is managed via the `nodes` table in the database.
# Add nodes via SQL or admin commands after first startup.

# Subscription server (REQUIRED in v3.0+)
GLOBAL_SUB_URL=https://sub.example.com  # Public HTTPS URL for subscription links
SUBSERVER_ACCESS_LOG=true               # Enable access logging on sub server

# Database (on persistent volume)
DATABASE_PATH=/app/data/tgvpn.db

# Logging
LOG_LEVEL=warn  # Less verbose in prod
LOG_FILE_PATH=/app/data/bot.log

# Monitoring
HEARTBEAT_URL=https://healthchecks.io/ping/your-uuid
HEARTBEAT_INTERVAL=300
SENTRY_DSN=https://your-key@o123456.ingest.sentry.io/1234567
WEB_SERVER_PORT=8880

# Trial
TRIAL_DURATION_HOURS=3
TRIAL_RATE_LIMIT=3
```

> **Note:** Node configuration lives in the `nodes` table (`host`, `api_token`, `inbound_ids` JSON array, `type`, `subscription_url`), managed via the database. `GLOBAL_SUB_URL` is required and provides the subscription URL prefix.

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
| `golang.org/x/sync` | v0.16.0 | None |
| `gorm.io/gorm` | v1.31.1 | None |
| `github.com/mattn/go-sqlite3` | v1.14.28 | None (CVE-2023-32636 patched) |
| `go.uber.org/zap` | v1.27.1 | None |
| `github.com/prometheus/client_golang` | v1.22.0 | None |

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
- 2026-07-02: Pre-release audit (v2.3.0) — S2 (X-Forwarded-For spoofing: `getClientIP` now reads rightmost IP from trusted proxy), S3 (URL scheme restriction: `validateURL` now restricts to http/https only, preventing SSRF via `file://`/`gopher://`), A1 (web→bot dependency break: `web` package no longer imports `bot`, reducing attack surface)

**Future audits:** Schedule annual external pentest if handling >10k users.

---

## Contact

Security issues: **security@kereal.me** (example)  
General questions: GitHub Issues (public)

*Last updated: 2026-07-02*
