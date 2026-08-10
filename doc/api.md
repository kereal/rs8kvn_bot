# API Reference — rs8kvn_bot

**Version:** v2.4.0
**Date:** 2026-08-10
**Base URL:** `http://localhost:8880` (configurable via `WEB_SERVER_PORT`)

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

Liveness probe — returns overall service health, aggregating registered component checkers (`database`).

**Response 200 OK** (all components healthy):
```json
{
  "status": "ok",
  "components": {
    "database": {"status": "ok"}
  },
  "timestamp": "2026-07-02T05:30:00Z",
  "uptime": "4h32m11s"
}
```

**Response 503 Service Unavailable** (a component is down):
```json
{
  "status": "down",
  "components": {
    "database": {"status": "down", "message": "connection refused"}
  },
  "timestamp": "2026-07-02T05:30:00Z",
  "uptime": "4h32m11s"
}
```

> `status` is `ok` only when every component is `ok`; it becomes `down` if any component is `down`.

---

## 2. Trial Landing Page

### `GET /i/{code}`

Trial invitation page. Validates invite code, applies IP rate limit, creates trial subscription, and renders the Happ/Telegram activation page.

**Path Parameters:** `code` — alphanumeric invite code with `_`/`-`.

**Response 200:** HTML page with Happ download links, subscription URL, and Telegram activation link.

**Response 404:** Invite code not found or invalid.

**Response 429:** IP rate limit exceeded.

**Response 500:** VPN node or database failure.

---

## 3. Subscription Proxy

### `GET /sub/{subID}`

Returns the merged subscription configuration from all active nodes. Responses are cached for 240 seconds; inactive/expired subscriptions return 404.

**Response 200:** Subscription body and aggregated `Subscription-Userinfo` headers.

**Response 404:** Subscription not found, inactive, or expired.

**Response 503:** Subserver not initialized.

**Response 405:** Non-GET request.

---

## 4. Prometheus Metrics

### `GET /metrics`

Prometheus exposition endpoint. It exposes HTTP, bot, cache, subserver, circuit-breaker, and database metrics.

---

## 5. Payment Callback

### `POST /payment/callback`

Receives Platega transaction status callbacks. Requests must include non-empty `X-MerchantId` and `X-Secret` headers matching the configured credentials. The body is limited to **256 KiB**, decoded with fixed-point JSON numbers, and must contain exactly one JSON document.

Required JSON fields:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440111",
  "amount": 230.00,
  "currency": "RUB",
  "status": "CONFIRMED"
}
```

`id` is a provider transaction UUID. The JSON value is validated at the webhook boundary; the universal `orders.provider_payment_id` database column remains text for compatibility with historical IDs from other providers. `paymentMethod` and `payload` are optional. Supported statuses are `PENDING`, `CANCELED`, `CONFIRMED`, and `CHARGEBACKED`; unknown statuses are acknowledged without changing the order.

**Responses:**

- `200 {"ok":true}` — callback processed, ignored as a no-op, unknown provider ID, or manual-review event;
- `400` — malformed payload, invalid UUID/amount, amount or currency mismatch, or forbidden state transition;
- `401` — invalid or missing provider credentials;
- `405` — method other than POST (`Allow: POST`);
- `503` — payments disabled, order service/bot not wired, or runtime payment readiness has not been enabled after real bot and SyncService initialization;
- `500` — temporary database/transaction failure. The provider should retry these callbacks.

`CONFIRMED` atomically changes `pending → paid`, updates the subscription and creates DB sync prerequisites in one transaction. Post-commit VPN sync and Telegram delivery are best-effort and do not roll back the payment. `CANCELED` cancels only pending orders. `CHARGEBACKED` records cancellation for pending/paid orders and sends an administrator alert with order, user, amount, currency, and provider transaction data; access changes require manual review.

The payment link lifetime is taken from Platega `expiresIn` and stored as an absolute local UTC `payment_expires_at`. A still-valid saved link is reused. After local expiry, the pending order is terminalized as `expired`; its provider ID and URL are retained and the next request creates a new pending order. If the provider request has an uncertain outcome, automatic retry is blocked and the administrator receives a reconciliation alert.

---

## 6. Static Files

### `GET /static/logo.png`

Returns the embedded 512×512 PNG logo. Also responds to `HEAD`.

---

## 7. Error Codes

| Code | HTTP Status | Description |
|------|------------|-------------|
| `INVITE_NOT_FOUND` | 404 | Invite code invalid |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many trial requests from IP |
| `SUBSCRIPTION_NOT_FOUND` | 404 | Subscription not found/inactive/expired |
| `TRIAL_CREATION_FAILED` | 500 | VPN node failed to create trial |
| `DATABASE_ERROR` | 500 | Database query failed |
| `INTERNAL_ERROR` | 500 | Unexpected system failure |

---

## 8. Rate Limits

| Endpoint | Limit | Enforcement |
|----------|-------|-------------|
| `/i/{code}` (trial) | 3 requests/hour per IP | Database counter |
| `/sub/{subID}` | None | 240-second response cache |
| `/metrics` | None | Prometheus scrape interval |
| Telegram bot commands | 30 tokens/user, 5/sec refill | In-memory token bucket |

---

## 9. cURL Examples

**Health check:**
```bash
curl -s http://localhost:8880/healthz | jq
```

**Trial page:**
```bash
curl -i http://localhost:8880/i/ABC123def456
```

**Payment callback:**
```bash
curl -i -X POST http://localhost:8880/payment/callback \
  -H 'X-MerchantId: <merchant-id>' \
  -H 'X-Secret: <secret>' \
  -H 'Content-Type: application/json' \
  -d '{"id":"550e8400-e29b-41d4-a716-446655440111","amount":230.00,"currency":"RUB","status":"CONFIRMED"}'
```

---

## 10. Versioning

API version is implicit in endpoint paths. Bot version in logs: `rs8kvn_bot@<version>`.

---

*Documentation last updated: 2026-08-10*
