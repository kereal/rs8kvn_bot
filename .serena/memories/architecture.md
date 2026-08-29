# Architecture — rs8kvn_bot

**Branch:** `dev`

## 3x-ui v3.7.0 auto-renew (2026-08-29)
- Free clients are provisioned/updated with `reset=30`, `resetDay=0`, `resetMax=0`, `trafficReset=monthly`, `trafficResetDay=1`, matching the panel payload supplied by the operator.
- `resetDay=0` intentionally preserves rolling 30-day renewal; calendar renewal is not enabled. `resetMax=0` is unlimited. Trials use `reset=0`.
- **Verified against 3x-ui sources (main, ~v3.7.0)**: `model.Client` validates `trafficReset ∈ {never,hourly,daily,weekly,monthly}` (`omitempty` → `""` accepted and normalized to `never`), `resetDay ∈ [0,31]` (0 = interval/rolling), `resetMax ≥ 0` (0 = unlimited). `autoRenewClients` (`internal/web/service/inbound_traffic.go`) selects clients by `(reset > 0 or reset_day > 0) and expiry_time <= now` + `reset_max <= 0 or reset_count < reset_max`, renews rolling by `reset*86400000`, resets up/down on renew, and `depletedClientsClause` excludes renew-enabled clients from the operator's purge.
- The auto-renew profile fields are sent **conditionally**: only when `TrafficReset != ""` (set by the caller in `xui.ClientRequest`). Zero/empty values are never sent, so trials and unlimited plans cannot accidentally overwrite panel settings. **Important**: the panel does NOT merge — an update without `trafficReset` writes `never` into the client (normalizeClientTrafficReset on Create/Update), so every bot update of a limit-plan client must carry the full profile.
- The panel's `/clients/traffic/{email}` response does NOT include `trafficReset/trafficResetDay` (absent from its `xray.ClientTraffic`), so `reenableAndNotify` reads the profile back via `xui.Client.GetClientByEmail` (inbound settings) before re-enabling; best-effort — on failure it warns and proceeds without the profile.
- Recovery fallback lives **only in `xui.doUpdateClient`**: an expired client with `resetDays > 0` gets its expiry moved to now + 30 days. Trials (`resetDays == 0`) are never extended; the sync layer (`internal/service/sync.go`) does NOT move expiries itself.

## Subscription traffic notifications (2026-08)
- **`SubscriptionTrafficWorker`** (`internal/scheduler/subscription_traffic_worker.go`): performs its first scan immediately after application startup and repeats every **60 min**, scans `GetActiveSubscriptionsWithTrafficLimit`, calls `SubscriptionService.ProcessTrafficNotifications` per subscription. Best-effort: `Error` on repo query failure (aborts scan), `Warn` on per-subscription failure (continues). Negative `telegram_id` (trial) targets are skipped.
- **`SubscriptionService.ProcessTrafficNotifications`** (`internal/service/subscription_traffic_notifications.go`): polls live 3x-ui nodes (`GetClientTraffic` via node bindings, mirrors `GetWithTraffic`), sums up/down, and switches:
  1. **90%** (`used ≥ 90% limit`, client enabled): sales text «Осталось меньше 10% трафика» + button `buy_premium_list`.
  2. **Exhausted** (`used ≥ 100% limit`): message «Доступ приостановлен» (traffic spent + VPN disabled) + Premium button. Client is **NOT** re-enabled (buy Premium CTA instead).
  3. **Reset+reenable** (`used < limit` but client still disabled): re-enables via `UpdateClient(Enable=true)` and sends simple «трафик сброшен» come-back text **without** Premium button.
  Below 90% it releases the 90% bit so the warning can fire again if usage climbs back.
- **Idempotency**: `subscriptions.traffic_reminders_sent` bitmask (migration **038**): `TrafficBit90`=1, `TrafficBitExhausted`=2, `TrafficBitReset`=4. Atomic `ClaimTrafficReminder`/`ReleaseTrafficReminder`; on Telegram send failure the claim is released for retry. Only plans with `traffic_limit > 0`; free/premium (unlimited) plans untouched.
- **xui Enable flag**: `ClientRequest.Enable *bool` wired into `doUpdateClient` so the worker can set enable/disable; `ClientTraffic.Enable` surfaced by `GetClientTraffic`.
- **Metrics**: `traffic_notifications_total{kind,result}` (kind: `ninety`/`exhausted`/`reset`), `traffic_notification_runs_total`.
- **Lifecycle wiring**: registered in `cmd/bot/main.go` (`startBackgroundWorkers`, `recoverAndReport("Subscription traffic worker")`).
- Note: works with clients the panel **disables but keeps listed** (Enable=false). Clients the panel fully removes return not-found and are skipped/logged.

## Changes since 2026-07-22
- **Atomic Platega payment confirm** — `ConfirmOrderPaidCAS` now spans order CAS + subscription update + plan reconciliation in ONE DB transaction via the new `ApplyPlanInTxFn` callback; previously `applyPlan` ran *after* the tx, which let notification fire while plan rows were missing.
- **Platega callback hardening** — `web.PaymentConfig` (503 when missing), `X-MerchantId`/`X-Secret` constant-time auth with empty-credential rejection, `MaxBytesReader(256 KiB)`, `UseNumber`, UUID-formatted payment ID, strict body (single JSON value). See `internal/web/payment_test.go`.
- **Bot rename callbacks** — `menu_payment` → `buy_premium_list` (list) + `buy_product_{id}` (per-product payment link). `KeyboardBuilder.SetPaymentEnabled` dropped; the flag is passed per-call into `MainMenu(hasSub, paymentEnabled)` instead.
- **Admin payment alerts** — on activation `notifyAdminPaid` (Markdown: tariff, amount, clickable buyer link via `utils.FormatUserLink`, purchase `🆕` vs renewal `🔄` from `PricePaidCents`/`ProductID` before CAS); on paid-order chargeback a single `notifyAdminChargeback` (tariff, amount, buyer link, access status). Both best-effort after per-order payment lock release; integration problems (mismatches, DB failures, provider errors) go through `NotifyPaymentIssue` → `notifyAdmin`.
- **Per-order payment lock** — `OrderService.lockPayment(ctx, providerPaymentID)` keeps a capacity-1 token channel per `provider_payment_id`. Confirms/cancels for the SAME order serialize; confirms/cancels for DIFFERENT orders run in parallel (verified by `TestConfirmPayment_DifferentOrdersRunInParallel` / `TestCancelPaymentByProvider_DifferentOrdersRunInParallel`). Lock is released BEFORE best-effort VPN sync so post-commit work does not block other orders.

## Broadcast campaigns (2026-08, branch feat/broadcast-campaigns)
- **Durable `BroadcastWorker`** (`internal/bot/broadcast_worker.go`): tick 15 s, per-campaign timeout 5 min, batches of 100 (concurrency 10), survives process restarts. Audience snapshot + per-recipient state live in the `broadcasts.recipients_state` JSON column; leases (2 min) recover after crashes.
- Admin session flow: name → text → filters (`bfilter_*`) → confirm; campaign lifecycle `scheduled|running|completed|failed|canceled`; cancel (`broadcast_cancel_<id>`) and retry-failed (`broadcast_retry_<id>`) actions.
- Delivery: blocked vs unreachable classification, per-message retries (2, = broadcastRetries повторов сверх первой попытки), campaign-level exponential backoff (`retry_at`, 5 s → 15 min).
- **Flood (HTTP 429)**: `processRecipient` распознаёт `isFloodError` и ждёт `retry_after` (`floodRetryDelay`, кап `broadcastFloodMaxDelay`=90 s, дефолт 5 s) ДО `broadcastFloodMaxWaits`=5 раз в рамках той же обычной попытки; только когда ожидания исчерпаны, сообщение уходит в общий счётчик retry и фиксируется как failed. Финиш получателя вынесен в `finishRecipientState`, ожидание с учётом ctx — в `waitBroadcastDelay`.
- Logging: `Info` for create/start/finish/cancel/retry and planned resume after timeout; `Warn` for background failures; `Error` for panics, retry-persistence, cancel/retry callback errors.

## Audit follow-up (2026-08-21/26)
- Expired active subscriptions are excluded at the DB cache-miss query boundary.
- Subserver cache revalidation fails closed on DB errors; upstream bodies are capped at 2 MiB (`config.MaxResponseSize`).
- Trial cleanup claims rows, deprovisions external clients, and deletes only after success so failures remain retryable.
- Trial binding updates the panel before the DB bind and rolls back the panel rename if the DB race loses.
- Subscription sync plan/removal transitions hold the per-subscription lock; graceful shutdown waits up to 90 seconds.
- **`ResetTraffic`** on plan change: new `vpn.Client.ResetTraffic` (3x-ui `POST /panel/api/clients/resetTraffic/{email}`, proxman/fetch no-op) runs best-effort in `SyncService.processPendingUpdate`.
- `ReconcileOrphanedClients` recreates a missing `pending_add` node queue for active non-trial subscriptions instead of revoking them.

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
       ├── SQLite/GORM + embedded migrations
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
- Schema: embedded migrations are applied automatically at startup; the current repository includes migrations through 038.
- `subscriptions.reminders_sent` stores reminder bitmask.
- `subscription_nodes` machine: `active`, `pending_add`, `pending_remove`, `pending_update`.
- Paid expiry excludes free/trial plans; reminder query excludes free/trial plans.

## Service layer
- `SubscriptionService` owns create/bind/renew/remind flows.
- `SubscriptionReminderService` owns reminder window model + Telegram delivery.
- `SyncService` owns node reconciliation and retry/backoff.
- `subscription_traffic.go` owns presentation helpers.

## Scheduler
- Daily backup, trial cleanup at startup and every 3h, sync workers, expiry reminders worker.

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
