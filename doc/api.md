# API Reference — rs8kvn_bot

**Version:** v3.0.0  
**Date:** 2026-07-02  
**Base URL:** `http://localhost:8880` (configurable via `HEALTH_CHECK_PORT`)

---

## Table of Contents

1. [Health Checks](#1-health-checks)
2. [Trial Landing Page](#2-trial-landing-page)
3. [Subscription Proxy](#3-subscription-proxy)
4. [Prometheus Metrics](#4-prometheus-metrics)
5. [Payment Callback](#5-payment-callback)
6. [Static Files](#6-static-files)
7. [Error Codes](#7-error-codes)
8. [Rate Limits](#8-rate-limits)
9. [cURL Examples](#9-curl-examples)
10. [Versioning](#10-versioning)

---

## 1. Health Checks

### `GET /healthz`

Liveness probe — returns overall service health, aggregating registered component checkers (`database`, `xui`).

**Response 200 OK** (all components healthy):
```json
{
  "status": "ok",
  "components": {
    "database": {"status": "ok"},
    "xui": {"status": "ok"}
  },
  "timestamp": "2026-07-02T05:30:00Z",
  "uptime": "4h32m11s"
}
```

**Response 503 Service Unavailable** (degraded — a component is down or degraded):
```json
{
  "status": "degraded",
  "components": {
    "database": {"status": "ok"},
    "xui": {"status": "degraded", "message": "3x-ui panel unreachable"}
  },
  "timestamp": "2026-07-02T05:30:00Z",
  "uptime": "4h32m11s"
}
```

> `status` is `ok` only when every component is `ok`; it becomes `degraded` if any component is `degraded`, and `down` if any component is `down`. The `xui` checker pings the legacy XUI client; if no legacy client is configured it reports `ok` with message `"xui not configured"`.

**Headers:**
```
Content-Type: application/json
Cache-Control: no-cache
```

**Usage:** Kubernetes liveness probe, monitoring systems (UptimeRobot, healthchecks.io).

---

### `GET /readyz`

Readiness probe — returns 200 only when bot has finished initialization and is ready to accept traffic.

**Response 200 OK:**
```
OK
```

**Response 503 Service Unavailable:**
```
NOT READY
```

**Headers:**
```
Content-Type: text/plain
Cache-Control: no-cache
```

**Usage:** Kubernetes readiness probe — prevents traffic during startup.

---

## 2. Trial Landing Page

### `GET /i/{code}`

Trial invitation page. Validates invite code, applies IP rate limit, creates trial subscription via the multi-node VPN abstraction (`internal/vpn/`), renders mobile-friendly page with Happ deep-link and Telegram activation link.

**Path Parameters:**
| Parameter | Description |
|-----------|-------------|
| `code` | Invite code (alphanumeric, `_`, `-`, max 16 chars) |

**Query Parameters:**
| Parameter | Description |
|-----------|-------------|
| `debug` | (optional) Set to `true` to bypass rate limit for testing |

**Cookies:**
- `rs8kvn_trial_{code}` — set after successful trial creation to prevent duplicate trials

**Response 200 OK:**

Returns HTML page with:
- Happ download buttons (Android/iOS)
- "Add to Happ" button (`happ://add/` deep-link)
- Copy-to-clipboard subscription URL
- Telegram activation deep-link: `https://t.me/{botUsername}?start=trial_{subID}`

The `botUsername` is the Telegram bot username passed to `web.NewServer(...)` (extracted from the bot config at startup, decoupling the `web` package from the `bot` package — see security fix A1). The link uses the `https://t.me/` format with a `start=trial_{subID}` payload so Telegram opens the bot chat and the bot receives the `/start trial_{subID}` command to bind the user.

Example body (simplified):
```html
<!DOCTYPE html>
<html>
  <head><title>RS8 KVN</title></head>
  <body>
    <div class="container">
      <h2>📱 Скачайте Happ</h2>
      <a href="https://play.google.com/...">Android</a>
      <a href="https://apps.apple.com/...">iOS</a>

      <h2>➕ Добавьте подписку</h2>
      <a href="happ://add/vless%3A%2F%2Fuser%40server%3A443%3F...">📥 Добавить в Happ</a>

      <h2>🔌 Нажмите большую кнопку включения</h2>
      <h2>📱 Активируйте в Telegram</h2>
      <a href="https://t.me/your_bot?start=trial_abc123def456">🚀 Активировать</a>
    </div>
  </body>
</html>
```

**Response 404 Not Found:**

Invalid invite code (not in `invites` table).

```json
{
  "error": "invite not found",
  "code": "INVITE_NOT_FOUND"
}
```

**Response 429 Too Many Requests:**

IP rate limit exceeded (default: 3 trials/hour per IP).

```json
{
  "error": "rate limit exceeded",
  "code": "RATE_LIMIT_EXCEEDED",
  "retry_after_seconds": 3600
}
```

**Response 500 Internal Server Error:**

VPN node failure or database error.

```json
{
  "error": "failed to create trial",
  "code": "TRIAL_CREATION_FAILED",
  "details": "VPN client error message"
}
```

**Security Headers (auto-set):**
```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
```

**IP extraction (S2 fix):** `getClientIP()` uses the **rightmost** IP from `X-Forwarded-For` (set by the trusted reverse proxy) instead of the leftmost (which is client-controlled and spoofable). Falls back to `r.RemoteAddr` when no proxy header is present or the request is not from a loopback address.

**Future improvements:** CSP, HSTS headers.

---

## 3. Subscription Proxy

### `GET /sub/{subID}`

Returns the merged subscription configuration aggregated from **all active nodes** for the subscription. This is a multi-node flow: the subserver (`internal/subserver/`) fetches the subscription's plan and active node sources from the database, requests each node's subscription URL in parallel, detects the response format (JSON / Base64 / plain), converts JSON server configs to share links, aggregates `subscription-userinfo` headers across sources (earliest expiry, summed upload/download), and returns the combined body.

**Path Parameters:**
| Parameter | Description |
|-----------|-------------|
| `subID` | Subscription ID (alphanumeric, `_`, `-`; matched by `^[a-zA-Z0-9_-]+$`) |

**Query Parameters:** None

**Flow:**
1. Check the per-`subID` response cache (240s TTL). On hit, verify the subscription is still active via a cheap status lookup; invalidate stale entries.
2. On cache miss, load the subscription with its plan and active node sources (`db.GetWithPlanAndNodes`).
3. Track the requesting device (HWID, Device-OS, Ver-OS, Device-Model from request headers) and client IP.
4. For each active node source, fetch the upstream subscription URL. Request headers are filtered via `subserver.FilterHeaders` (excludes `X-Forwarded-Proto`, `X-Forwarded-For`, `X-Real-Ip`).
5. Detect format (JSON / Base64 / plain), convert JSON configs to share links if mixed mode detected.
6. Aggregate `subscription-userinfo` headers across all sources.
7. Cache and return the final body with appropriate `Content-Type`.

**Response 200 OK:**

Raw subscription body (VLESS, VMESS, Trojan, etc.) — servers from all active nodes merged.

**Example response** (VLESS):
```
vless://uuid@node1.example.com:443?security=reality&ps=RS8+KVN+Node1&flow=xtls-rprx-vision&... # node 1
vless://uuid@node2.example.com:443?security=reality&ps=RS8+KVN+Node2&...                       # node 2
vless://uuid@node3.example.com:443?security=reality&ps=RS8+KVN+Node3&...                       # node 3
```

**Headers:**
- `Content-Type: text/plain` (or `application/octet-stream` depending on upstream)
- `Subscription-Userinfo: upload=...; download=...; total=...; expire=...` (aggregated across nodes)
- `Profile-Update-Interval: 24` (if provided by upstream)

**Cache:** 240 seconds (4 minutes) — subsequent requests served from memory.

**Response 404 Not Found:**

Subscription not found, inactive, or expired.

```json
{
  "error": "subscription not found",
  "code": "SUBSCRIPTION_NOT_FOUND"
}
```

**Response 503 Service Unavailable:**

Subserver not initialized.

**Response 405 Method Not Allowed:**

Non-GET request.

**Notes:**
- If an upstream node fails, partial results from remaining nodes are still served; the `subserver_source_fetch_total{result="error"}` metric is incremented
- `Content-Length` header removed after merge (chunked encoding)
- Access logging is enabled when `SUBSERVER_ACCESS_LOG` is configured

---

## 4. Prometheus Metrics

### `GET /metrics`

Prometheus exposition endpoint — served via `promhttp.Handler()` on the same mux as all other routes. Returns metrics in Prometheus text exposition format for scraping by Prometheus/Grafana Agent.

**Response 200 OK:**
```
Content-Type: text/plain; version=0.0.4; charset=utf-8
```

**Key metrics:**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | counter | `method`, `path`, `status` | Total HTTP requests processed |
| `http_request_duration_seconds` | histogram | `method`, `path` | HTTP request duration |
| `http_requests_in_flight` | gauge | `method`, `path` | Current in-flight HTTP requests |
| `bot_updates_total` | counter | `command`, `result` | Bot updates processed (`result`: success, error, rate_limited) |
| `bot_update_errors_total` | counter | `type` | Errors during bot update processing |
| `bot_update_duration_seconds` | histogram | — | Bot update processing duration |
| `cache_hits_total` | counter | `cache` | Cache hits (`cache`: subscription, referral, subserver) |
| `cache_misses_total` | counter | `cache` | Cache misses |
| `circuit_breaker_state` | gauge | `target` | Circuit breaker state (0=closed, 1=open, 2=half-open) |
| `bot_orphaned_clients_removed_total` | counter | — | Orphaned clients removed during reconciliation |
| `subserver_source_fetch_total` | counter | `result`, `format` | Upstream source fetch results (success/error by format) |
| `subserver_source_fetch_duration_seconds` | histogram | `result` | Upstream source fetch duration |
| `subserver_cache_invalidations_total` | counter | `reason` | Subscription cache invalidations (not_found, status_error, revoked, expired) |
| `subserver_no_items_total` | counter | — | Subscription requests with no items returned |

**Path normalization:** Dynamic path segments are normalized for cardinality control — `/i/{code}` → `/i/:code`, `/sub/{subID}` → `/sub/:id`.

---

## 5. Payment Callback

### `POST /payment/callback`

Payment provider webhook callback endpoint. Currently returns a stub success response.

**Response 200 OK:**
```json
{
  "ok": true,
  "provider_payment_id": "fake",
  "status": "paid"
}
```

**Response 405 Method Not Allowed:** Non-POST request.

---

## 6. Static Files

### `GET /static/logo.png`

Bot logo (PNG, 512×512, optimized for mobile). Also responds to `HEAD`.

**Response 200 OK:**

Binary PNG image.

**Headers:**
```
Content-Type: image/png
Cache-Control: public, max-age=86400
```

**Usage:** Used in trial landing page.

---

## 7. Error Codes

All errors follow this format:

```json
{
  "error": "human readable message",
  "code": "UPPER_SNAKE_CASE_CODE",
  "details": "optional technical details"
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|------------|-------------|
| `INVITE_NOT_FOUND` | 404 | Invite code invalid |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many trial requests from IP |
| `SUBSCRIPTION_NOT_FOUND` | 404 | Subscription not found (inactive/expired) |
| `TRIAL_CREATION_FAILED` | 500 | VPN node failed to create trial |
| `XUI_UNAVAILABLE` | 502 | VPN node unreachable |
| `DATABASE_ERROR` | 500 | Database query failed |
| `INTERNAL_ERROR` | 500 | Unexpected panic or system error |

---

## 8. Rate Limits

| Endpoint | Limit | Enforcement |
|----------|-------|-------------|
| `/i/{code}` (trial) | 3 requests/hour per IP | In-memory DB counter |
| `/sub/{subID}` | None (cached, 240s TTL) | No explicit rate limit; cache TTL mitigates abuse |
| `/metrics` | None | Prometheus scrape interval governs load |
| Telegram bot commands | 30 tokens/user, 5/sec refill | In-memory token bucket |

**Burst capacity:** Token bucket allows short bursts up to 30 requests.

**IP extraction:** Trial rate limiting uses `getClientIP()` which reads the **rightmost** IP from `X-Forwarded-For` (trusted proxy) — not the leftmost (spoofable). See security fix S2.

---

## 9. cURL Examples

**Health check:**
```bash
curl -s http://localhost:8880/healthz | jq
```

**Readiness check:**
```bash
curl -s http://localhost:8880/readyz
```

**Trial page:**
```bash
curl -i http://localhost:8880/i/ABC123def
```

**Check subscription proxy (multi-node):**
```bash
curl -s http://localhost:8880/sub/abc123def456 | base64 -d | head -20
```

**Prometheus metrics:**
```bash
curl -s http://localhost:8880/metrics | grep -E 'http_requests_total|active_subscriptions|bot_updates_total'
```

**Payment callback (stub):**
```bash
curl -s -X POST http://localhost:8880/payment/callback | jq
```

---

## 10. Versioning

API version is implicit in endpoint paths:
- `/sub/{subID}` — subscription proxy (multi-node, current)
- No breaking changes expected without major version bump

Bot version in logs: `rs8kvn_bot@v3.0.0`

---

*Documentation last updated: 2026-07-02*
