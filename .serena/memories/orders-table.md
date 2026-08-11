# Orders & Payment (Platega)

## Two-phase Provisioning
- Phase 1 (DB-setup, must-succeed): `ConfirmOrderPaidCAS` — single SQL transaction that (a) CAS-guards `orders.status='pending'→'paid'`, (b) updates the subscription, (c) invokes `applyPlanInTx` callback to materialize all `subscription_nodes` `pending_add`/`pending_remove`/`pending_update` rows in the same tx. Any error rolls the whole thing back.
- Phase 2 (external-sync, best-effort): `SyncSubscription` → `SyncPendingNodes` worker with retry/backoff. The bot may respond OK before VPN is fully provisioned.

## Web entrypoint
- `POST /payment/callback` (web.Server) — checks `web.PaymentConfig` first (else 503), then `X-MerchantId`/`X-Secret` (platega.VerifyHeaders), MaxBytesReader(256 KiB), UUID id, exact amount match.
- 503 fires when payments not configured; 401 on bad headers; 400 invalid body/UUID; 200 success.

## Service layer
- `OrderService.RequestPayment` products list confirm keyboard (`buy_product_{id}`), creates order row with status `pending`, calls `platega.Client.CreateTransaction` for the link.
- `OrderService.ConfirmPayment` (idempotent, CAS) → `NotifyPaidUser` (returns `0, "", nil` when `subscription.TelegramID<=0` so no telegram call is attempted).

## Admin notifications (TelegramAdminID, best-effort Markdown)
- On activation (`Activated=true`, only the pending→paid CAS caller) → `notifyAdminPaid`: tariff, formatted amount (`formatMoneyCents`), clickable buyer link (`utils.FormatUserLink` — `t.me/username` or `tg://user?id=…`), provider ID, sub/order IDs, expiry. Title distinguishes `🆕 Покупка подтверждена` vs `🔄 Продление подтверждено` — `isRenewal` captured **before** the CAS mutates `sub` (`PricePaidCents > 0 || ProductID != nil`).
- On `CHARGEBACKED` with `WasPaid=true` (money collected) → `notifyAdminChargeback`: tariff, amount, buyer link, access status (`понижен до бесплатного` / `сохранён` if another paid order exists). Sent after `paymentMu.Unlock()`.
- Every payment integration problem (mismatches, late/unknown callbacks, provider errors, uncertain outcomes) → `NotifyPaymentIssue` → `notifyAdmin` (plain-text) + `payment_issues_total{event}`.
- Duplicate CONFIRMED/CHARGEBACKED callbacks are silent (idempotent no-op) — no repeated alerts.

## Bot integration
- `buy_premium_list` → renders products via `KeyboardBuilder.BuyProductList` (callbacks `buy_product_{N}`).
- `buy_product_{N}` → `OrderService.RequestPayment`, keyboard `BuyProductConfirm` with "Оплатить" (URL) + "← Назад" → `buy_premium_list`.

## Test contract
- `internal/web/payment_test.go`: 503 when PaymentConfig missing, 405 GET, sanity UUID validators, MaxBytesReader smoke.
- `internal/bot/keyboard_test.go`: §7.1 labels (`💎 Купить Premium`), callbacks (`buy_premium_list`, `buy_product_*`), `BuyProductList` rows.
