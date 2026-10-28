# API Reference — rs8kvn_bot

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

Returns a JSON **array** of Xray-core client configurations built from supported JSON, Clash, plain share-link, and Base64 share-link sources on all active nodes, following the XTLS Subscription Standards body shape (`application/json`, one complete config object per node). Every element is a self-contained config with `remarks` (the node name clients show), local `socks`/`http` inbounds, exactly one proxy outbound plus `direct`/`block`, DNS settings, and routing whose catch-all rule targets that element's own proxy outbound — so each node is independently selectable in the client UI. Proxy outbounds use the source remark as their tag when unique, with a free `proxy-N` as a fallback; the reserved `direct`/`block` tags are never handed to a proxy outbound, so a node remarked `direct` or `block` is renamed instead of shadowing the built-in outbounds. `Subscription-Userinfo` carries aggregated usage. Responses are cached for 240 seconds; inactive/expired subscriptions return 404. Sources that cannot be converted to supported Xray outbounds are skipped; if none remain, the endpoint returns 404.

Routing uses `geosite:category-ru` (the built-in Xray category name for Russian domains) and `geoip:ru`. Each array element carries its own routing table and one proxy outbound; Xray's JSON schema has no portable manual-selector outbound group, so node switching happens through the client's profile list rather than inside a single config.

The same aggregated nodes are also served in sing-box and Clash/Mihomo formats, selected by `?format=` or detected from the `User-Agent` (unknown/empty falls back to Xray):

- **sing-box** (`?format=singbox` or UA `sing-box`/`singbox`): one modern sing-box 1.12+ config with a `proxy` selector outbound listing every node, a `mixed` loopback inbound, and rule-set based routing mirroring the Xray policy (`internal/subserver/singbox.go`).
- **Clash/Mihomo** (`?format=clash` or UA `clash`/`mihomo`/`stash`): a complete local YAML profile — `mixed-port: 7890` loopback listener, a manual `PROXY` `select` group listing every node, fake-ip DNS with RU/private domains steered to local resolvers, and routing mirroring the Xray policy (ads GEOSITE REJECT; private/loopback and RU IP-CIDR/IP-CIDR6 direct; RU/private GEOSITE direct; `MATCH` to the PROXY group). No external rule-provider dependency. Node names are reserved against the `PROXY` group name so a node remarked `PROXY` is renamed instead of stealing the selector.

**Response 404:** Subscription not found, inactive, or expired.

**Response 503:** Subserver not initialized.

**Response 405:** Non-GET request.

---

## 4. Prometheus Metrics

### `GET /metrics`

Prometheus exposition endpoint. It exposes application, bot, cache, subserver, database, subscription, and payment metrics. Requests to `/metrics` itself and `/static/*` are deliberately excluded from `http_requests_total` and `http_request_duration_seconds`, so scrapes and asset 404s do not pollute application traffic metrics.

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

`id` is a provider transaction UUID. The JSON value is validated at the webhook boundary; the universal `orders.provider_payment_id` database column remains text for compatibility with historical IDs from other providers. `paymentMethod` и `payload` принимаются при наличии. Официальные страницы Platega противоречат друг другу по обязательности этих полей; интеграция сохраняет совместимость. Поддерживаются `PENDING`, `CANCELED`, `CONFIRMED` и `CHARGEBACKED`; неизвестные статусы подтверждаются без изменения заказа и отправляют администратору alert для проверки.

**Responses:**

- `200 {"ok":true}` — callback processed, ignored as a terminal-order no-op (including duplicate or late `CONFIRMED` callbacks), unknown provider ID, or manual-review event;
- `400` — malformed payload, invalid UUID/amount, invalid currency, or callback amount below the stored order amount; a larger callback amount is accepted because Platega may include its payment-method commission;
- `401` — invalid or missing provider credentials;
- `405` — method other than POST (`Allow: POST`);
- `503` — payments disabled, order service/bot not wired, or runtime payment readiness has not been enabled after real bot and SyncService initialization;
- `500` — temporary database/transaction failure. The provider should retry these callbacks.

`CONFIRMED` atomically changes `pending → paid`, updates the subscription and creates DB sync prerequisites in one transaction. Post-commit VPN sync and Telegram delivery are best-effort and do not roll back the payment. Only the callback that actually activated the order (`Activated=true`) sends the success notifications: the user gets a Markdown-formatted Premium welcome message with the benefits (unlimited traffic, more servers/options, experimental features and priority support), subscription details, expiry and link; the admin gets a Markdown alert (`notifyAdminPaid`) with tariff, formatted amount and a clickable buyer link (`utils.FormatUserLink` — `t.me/username` or `tg://user?id=…`). The title distinguishes a first purchase (`🆕 Покупка подтверждена`) from a renewal of an already-paid subscription (`🔄 Продление подтверждено`, detected from `PricePaidCents`/`ProductID` before the CAS mutates the subscription). Duplicate `CONFIRMED` callbacks are idempotent no-ops and send nothing. `CANCELED` cancels only pending orders. `CHARGEBACKED` cancels pending/paid orders; a chargeback on a previously-paid order automatically downgrades the subscription to the free plan (unless another paid order for the same subscription exists — access is then preserved), and when money was actually collected (`WasPaid=true`) sends a single `notifyAdminChargeback` Markdown alert with tariff, amount, buyer link and access status (downgraded to free vs preserved). Infrastructure-level failures (DB outage, mismatches, late/unknown callbacks, provider errors) continue to flow through `NotifyPaymentIssue` → `notifyAdmin` for ops visibility.

The payment link lifetime is taken from Platega `expiresIn` and stored as an absolute local UTC `payment_expires_at`. A still-valid saved link is reused. After local expiry, the pending order is terminalized as `expired`; its provider ID and URL are retained and the next request creates a new pending order. If the provider request has an uncertain outcome, automatic retry is blocked and the administrator receives a reconciliation alert. A late `CONFIRMED` for an already `expired`/`canceled` order never activates it: the money is flagged for manual review (refund or manual activation) and the admin gets a `late_confirmed_callback` alert.

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

*This reference is maintained against the current codebase.*
