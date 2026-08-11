# Architecture — rs8kvn_bot

**Version:** v2.4.0
**Updated:** 2026-08-09
**Branch:** `dev`

## Changes since 2026-07-22
- **Atomic Platega payment confirm** — `ConfirmOrderPaidCAS` now spans order CAS + subscription update + plan reconciliation in ONE DB transaction via the new `ApplyPlanInTxFn` callback; previously `applyPlan` ran *after* the tx, which let notification fire while plan rows were missing.
- **Platega callback hardening** — `web.PaymentConfig` (503 when missing), `X-MerchantId`/`X-Secret` constant-time auth with empty-credential rejection, `MaxBytesReader(256 KiB)`, `UseNumber`, UUID-formatted payment ID, strict body (single JSON value). See `internal/web/payment_test.go`.
- **Bot rename callbacks** — `menu_payment` → `buy_premium_list` (list) + `buy_product_{id}` (per-product payment link). `KeyboardBuilder.SetPaymentEnabled` dropped; the flag is passed per-call into `MainMenu(hasSub, paymentEnabled)` instead.
- **Admin payment alerts** — on activation `notifyAdminPaid` (Markdown: tariff, amount, clickable buyer link via `utils.FormatUserLink`, purchase `🆕` vs renewal `🔄` from `PricePaidCents`/`ProductID` before CAS); on paid-order chargeback a single `notifyAdminChargeback` (tariff, amount, buyer link, access status). Both best-effort after per-order payment lock release; integration problems (mismatches, DB failures, provider errors) go through `NotifyPaymentIssue` → `notifyAdmin`.
- **Per-order payment lock** — `OrderService.lockPayment(ctx, providerPaymentID)` keeps a capacity-1 token channel per `provider_payment_id`. Confirms/cancels for the SAME order serialize; confirms/cancels for DIFFERENT orders run in parallel (verified by `TestConfirmPayment_DifferentOrdersRunInParallel` / `TestCancelPaymentByProvider_DifferentOrdersRunInParallel`). Lock is released BEFORE best-effort VPN sync so post-commit work does not block other orders.

## Previous changes (2026-07-22)
- **Expiry reminders**: added 3-touch flow 3d/1d/3h, atomic bitmask (`subscriptions.reminders_sent`), standalone worker `SubscriptionReminderWorker` (30 min), plus DB/service/scheduler/test split (`subscription_reminders.go` + `subscription_reminders_test.go`).
- **Trial exclusion**: paid-only expiry and reminder flows now exclude free/trial plans (`GetExpiredPaidSubscriptions`, `GetSubscriptionsExpiringInRange`).
- **Clash/Mihomo hardening**: port-hopping support, `normaliseTransportNetwork`, `setPacketEncoding`, TLS defaulting when `tls` is nil, password encoding via `url.User`/`url.UserPassword`, non-positive port guard.
- **Migration 030**: `subscriptions.reminders_sent` added with down migration.
- **Metrics**: added reminder-specific counters.

## System Context

```text
Telegram Bot API  3x-ui / proxman panels  Sentry
       │                │                   │
       ▼                ▼                   ▼
   rs8kvn_bot single binary (Go)
       │
       ├── Bot API layer + web server
       ├── Service layer + VPN abstraction
       ├── SQLite/GORM + migrations 000-030
       ├── Subserver /sub/{subID} + Clash normalization
       └── Background workers
```

## Subscription expiry reminders
- Windows: 3 days / 1 day / 3 hours before expiry.
- Worker: `SubscriptionReminderWorker` ticks every 30 minutes; scans active paid subscriptions in ±30 min windows.
- Idempotency: `reminders_sent` bitmask + atomic `ClaimReminder`/`ReleaseReminder` keyed by `expires_at`.
- Renewal resets: `RenewSubscription` resets `reminders_sent=0`.
- Business rule: free and trial plans excluded.
- Metrics: `SubscriptionRemindersTotal{window,status}`, `SubscriptionReminderRunsTotal`.
- Lifecycle wiring: `cmd/bot/main.go` (startBackgroundWorkers).

## Subserver share-link normalization
- Supported schemes: VMess, VLESS, Trojan, Hysteria2, TUIC, Shadowsocks SIP002.
- Transport normalization unified under `normaliseTransportNetwork`.
- Clash/V2rayN `packetEncoding` support via `setPacketEncoding`.
- Hysteria2 port-hopping: `firstPortFromPorts` extracts first concrete port.
- TLS defaulting: `security=tls` when Clash omits `tls` for Trojan/VLESS/Hysteria/TUIC.
- Password encoding in server links via `url.User` / `url.UserPassword`.

## Schema
- Version: **after migration 030**.
- `subscriptions.reminders_sent` stores reminder bitmask.
- `subscription_nodes` machine: `active`, `pending_add`, `pending_remove`, `pending_update`.
- Paid expiry excludes free/trial plans; reminder query excludes free/trial plans.

## Service layer
- `SubscriptionService` owns create/bind/renew/remind flows.
- `SubscriptionReminderService` owns reminder window model + Telegram delivery.
- `SyncService` owns node reconciliation and retry/backoff.
- `subscription_traffic.go` owns presentation helpers.

## Scheduler
- Daily backup, hourly trial cleanup, sync workers, expiry reminders worker.

## Subserver
- `/sub/{subID}` serves merged subscription payloads.
- Clash/Mihomo normalization path for share links.
- Optional async access log.

## Web
- `/healthz`, `/readyz`, `/i/{code}`, `/metrics`, `/payment/callback`, `/static/logo.png`.
- Singleflight for hot endpoints.

## Observability
- `/metrics`: HTTP, bot, subscription, reminders, DB/GORM, cache, subserver, circuit breaker.
- `/healthz` + `/readyz`.

## Operational notes
- SQLite acceptable up to hundreds of users; limited concurrent write throughput.
- Multi-node subscriptions remain the core scaling mechanism.
- Reminder worker is best-effort per-window; send failures release the bitmask claim.
