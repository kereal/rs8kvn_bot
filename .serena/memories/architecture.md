# Architecture — rs8kvn_bot

**Branch:** `dev`

## 3x-ui v3.7.0 auto-renew (2026-08-29)
- Free clients are provisioned/updated with `reset=30`, `resetDay=0`, `resetMax=0`, `trafficReset=monthly`, `trafficResetDay=1`, matching the panel payload supplied by the operator.
- `resetDay=0` intentionally preserves rolling 30-day renewal; calendar renewal is not enabled. `resetMax=0` is unlimited. Trials use `reset=0`.
- **Verified against 3x-ui sources (main, ~v3.7.0)**: `model.Client` validates `trafficReset ∈ {never,hourly,daily,weekly,monthly}` (`omitempty` → `""` accepted and normalized to `never`), `resetDay ∈ [0,31]` (0 = interval/rolling), `resetMax ≥ 0` (0 = unlimited). `autoRenewClients` (`internal/web/service/inbound_traffic.go`) selects clients by `(reset > 0 or reset_day > 0) and expiry_time <= now` + `reset_max <= 0 or reset_count < reset_max`, renews rolling by `reset*86400000`, resets up/down on renew, and `depletedClientsClause` excludes renew-enabled clients from the operator's purge.
- The auto-renew profile fields are sent **as a full profile when `TrafficReset != ""`**: when the caller sets `TrafficReset` on `xui.ClientRequest`, the panel payload includes all four profile fields (`resetDay`, `resetMax`, `trafficReset`, `trafficResetDay`) verbatim — including zero values such as `resetDay=0`, `resetMax=0`. The legacy `reset` field is always sent with whatever value the caller passed, including `reset=0` for trials/rolling/unlimited plans. The whole profile is omitted only when `TrafficReset == ""`, so trial/unlimited callers that leave it empty do not overwrite panel settings.
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

## Subserver subscription output (JSON array of Xray configs, 2026-10-27)
- `/sub/{subID}` ALWAYS returns a **JSON array** of complete Xray-core client configs (`application/json; charset=utf-8`), one object per node — never share links, base64, or a single object.
- Format follows the XTLS Subscription Standards (discussion 4877): "A subscription is defined as a JSON array of connection objects".
- WHY the array: v2rayNG (`AngConfigManager.parseCustomConfigServer` → `CustomFmt.parse`) imports ONE profile per array element and, from a single config, keeps only the first proxy outbound — a single object with N outbounds shows the user a single node. v2rayN (`V2rayFmt.ResolveCommon`) also walks a `JsonArray`. Happ accepts both shapes.
- Each element is a full client config: `remarks` (profile name shown by the client, equal to the node tag), `log`, `dns`, `inbounds` (socks 10808 + http 10809 with sniffing), `outbounds` = [node, `direct`, `block`], and `routing` whose catch-all rule targets that element's OWN tag.
- Template: `internal/subserver/templates/xray_client.json` (`//go:embed` in `service.go`), rendered per node by `renderXrayConnection`, array assembled in `buildResponse`.
- Pipeline: source body → `DetectFormat` → `aggregateFormat` (JSON/Clash → `jsonConfigs`; base64/plain → `items`) → `buildResponse` normalises share links into `serverConfig` → `ConvertJSONConfigsToXray` → `tagProxyOutbounds` → per-node render → `json.Marshal`.
- `vmess://` links are decoded by `parseVMessLink` (port/aid may be string OR number; helpers `parseJSONNumber`/`parseJSONScalar`); empty `add` or a non-vmess scheme is an error the caller skips with a Warn. The scheme is matched with `EqualFold` and the payload is NEVER lower-cased — Base64, UUID, host and remark are case-sensitive. Other schemes go through `parseShareLink`.
- Tags (`tagProxyOutbounds`): an outbound keeps its source `remark` when unique and not reserved, otherwise falls back to `freeProxyTag`. `reservedOutboundTags = {"direct","block"}` is pre-seeded into the `used` set, because `renderXrayConnection` always emits freedom/blackhole with exactly those tags and Xray routing resolves tags BY NAME — a node remarked `direct` would silently take over the direct route (geoip:ru / geosite:category-ru stopped bypassing the tunnel). `freeProxyTag` scans forward for the first unused `proxy-N`, so the fallback cannot collide with a tag another outbound already claimed either. Profile name and catch-all tag come from the SAME value, so the route always lands on its own node. There is no balancer (a balancer rotates the exit IP per connection).
- Hysteria2 maps to the modern Xray `hysteria` outbound: `settings{version,address,port}` plus `streamSettings{network:"hysteria", hysteriaSettings{version,auth}, security:"tls", tlsSettings}`. When obfuscation is configured, it is emitted as `streamSettings.finalmask.udp: [{type: obfs, settings: {password: obfsPassword}}]`.
- CRITICAL: use `geosite:category-ru`, NOT `geosite:ru`. `geosite:ru` is absent from geosite.dat and makes Xray refuse to start ("illegal domain rule"). `geoip:ru` IS valid. Validate every array element with `xray -test -config`.
- Unsupported protocols (e.g. TUIC) are skipped; if nothing converts, the endpoint returns 404 (`ErrNoSubscriptionItems`).
- Verified: all 12 profiles from the live fetch node (`fetch.example.net:4433/sfetch/all.yaml`) pass `xray -test -config` on Xray 26.10.21.
- Legacy share-link builders (`ConvertSingleJSONToLink` + `build*ServerLink`) stay for Clash normalization tests but are no longer used by the subscription response path.

## Subserver share-link normalization (Clash → share links, legacy)
- Supported schemes: VMess, VLESS, Trojan, Hysteria2, TUIC, Shadowsocks SIP002.
- Transport normalization unified under `normaliseTransportNetwork`.
- Clash/V2rayN `packetEncoding` support via `setPacketEncoding`.
- Hysteria2 port-hopping: `firstPortFromPorts` extracts first concrete port.
- TLS defaulting: `security=tls` when Clash omits `tls` for Trojan/VLESS/Hysteria/TUIC.
- Password encoding in server links via `url.User` / `url.UserPassword`.

## Schema
- Schema: embedded migrations are applied automatically at startup; the current repository includes migrations through 039.

### Audit Log (2026-09-25, migration 039)
- Universal append-only audit journal: table `audit_log` (`internal/database/audit_log.go` + `audit_log_repo.go`). Entity-addressed: `entity_type` (subscription/order/broadcast/plan/node/product/system) + `entity_id` (TEXT, survives physical deletion — no FK). Actor: `actor_type` (user/admin/worker/payment_provider/system) + `actor_id` (e.g. admin telegram_id via `service.WithAuditAdmin(ctx, id)` context key).
- Event registry: constants `AuditEvent*` + constructors (`AuditSubscriptionCreated`, `AuditPaymentConfirmed`, `AuditChargebackReceived`, ...) live ONLY in `internal/database/audit_log.go`; no CHECK constraints on event/entity_type — new event = one constant, no migration.
- Write contract: best-effort via service `logAudit` wrappers (Warn on failure, never breaks business flow); inside the database package use `insertAuditLogBestEffort`. EXCEPTION: payment transactions (`ConfirmOrderPaidCAS`, `CancelPaidOrderAndDowngradeCAS`) write via `InsertAuditLogInTx(tx, ...)` — atomic with the order/subscription transition, rolled back together.
- **SQLite deadlock rule**: never do a best-effort `InsertAuditLog` INSIDE a tx closure — it deadlocks on the write lock (held until commit, which happens after the closure returns). Capture event data in a variable inside the closure and write AFTER `Transaction()` returns (see `expiredOrder` in `FindOrCreatePendingPaymentOrder`).
- One mutation = one event per addressed entity: payment confirmation writes `payment_confirmed` (order) AND `subscription_renewed` (subscription, added 2026-09-25). Full coverage also includes: `order_expired` (both `FindPendingPaymentOrder` and `FindOrCreatePendingPaymentOrder`), `payment_creation_uncertain` (`MarkPaymentCreationUncertain`), `trial_deleted` (`CleanupExpiredTrials`), `order_canceled` in `CancelOrderCAS` (non-chargeback branch), `subscription_created` in `GetOrCreateSubscription` (second create path).
- Read API: `GetAuditLogByEntity` / `GetAuditLogByEvent` / `GetRecentAuditLog`.
- NOT logged: /sub touches (devices/ips/last_request), per-item worker Warn retries, no-op traffic scans. Technical events are per-run aggregates only (`sync_run_degraded`, `orphaned_clients_revoked` — written only when revoked > 0, zero runs stay silent).
- Coverage tests: `internal/database/audit_payment_test.go` (6 db-level), `internal/service/subscription_audit_test.go` (11 service-level, incl. best-effort failure tolerance and admin actor propagation).
- Testutil fake records into `DatabaseService.AuditLogs` with helper `AuditLogsByEvent(event)`.
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
- `/sub/{subID}` renders a full Xray-core JSON config from the embedded template (see "Subserver subscription output" above).
- Clash/Mihomo normalization feeds the same Xray conversion path.
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

## Refactoring 2026-10-31 (panel, registry, transitions, provision, seams)
- **Panel contract**: `doc/panel-api-contract.md` pins 3x-ui **v3.11.0** (live-verified): envelope `{success,msg,obj}`, HTTP always 200, no structural codes; `clients/get/{email}` → `obj.client` + `obj.inboundIds` (only source of `trafficReset*`); `clients/traffic` returns `success=true, obj=null` for a missing client; `add` is idempotent on matching subId. Error texts classified in ONE place: `internal/xui/classify.go` → sentinels `ErrClientNotFound`/`ErrClientAlreadyExists`, mapped to `vpn.ErrSubscription*`. API tokens have scopes (v3.11.0): node-sync allowlist lacks `clients/get`/`clients/traffic` → node tokens must be `admin` scope (default).
- **Node access**: `service.NodeClients` (`node_clients.go`) — one registry `nodeID → NodeAccess{Provision vpn.Client; Reader interfaces.XUIClientReader | nil}`, built once in `cmd/bot/main.go` `buildRuntimeNodeClients`; Reader=nil expresses "no read capability" (proxman/fetch); the active-node+binding filter lives only in `activeReaders`/`activeBindingNodeIDs`.
- **subscription_nodes transitions**: single core `reconcilePlanNodesCore` + `planNodeStore` seam with two adapters (`dbServicePlanStore` over DatabaseService, `txPlanStore` over *gorm.DB) in `service/plan_reconcile.go`. Background path: per-sub lock + revoked guard + errors.Join; tx path (`ApplyPlanToSubscriptionInTx`): fail-fast, NO revoked guard (payment CAS sets active first — paying reactivates revoked; do NOT move the guard into the core). Parity test: `TestApplyPlan_Parity_BackgroundAndTxSameEndState`.
- **Provision build**: single `SyncService.buildNodeProvision(ctx, sub, nodeID, profilePolicy)`; add = defaults (`reset=30=config.SubscriptionResetDay, monthly/1, resetMax=0`), update = panel profile read-modify-write (admin edits preserved; unreadable profile → Warn+retry, never overwritten with defaults), trial = `trafficReset=never`.
- **«🔄 Сброс» card line**: computed from the panel profile (`service/subscription_reset.go` `describeTrafficReset`): never/"" → «не сбрасывается», monthly day D (0→1) → nearest future D, daily → tomorrow, weekly (1=Mon..7=Sun) → next weekday, else «неизвестно»; read only when `limitGB > 0`; profile read failure → «неизвестно» + Debug. Golden card test pins the full text.
- **Persistence seams (ADR-0001 executed)**: `interfaces.DatabaseService` kept only as composition-root composition (10 repositories); per-consumer seams: `SubscriptionStore`(32), `SyncStore`(16), `OrderStore`(18), `BotStore`(28), `ReferralCountSource`, `ReferralInviteStore`, `AuditLogWriter`. `Transaction`/`GetPoolStats` deleted (0 calls); `Ping`/`Close` live on concrete `*database.Service`. Test fakes split per consumer (`internal/testutil/fake_*.go`), NO domain mirroring — state-machine semantics are tested on real SQLite (`testutil.NewTestDatabaseService`).
- Not refactored by decision: subserver format pipeline (deep, parity-tested), BroadcastWorker (small interface).

## Refactoring 2026-10-31, wave 2 (D5: retry policy, create fallback, honest card)
- **Retry policy** (`SyncService.handleSyncError` in `internal/service/sync.go`): caller-ctx `Canceled`/`DeadlineExceeded` writes NO retry metadata (scan shutdown ≠ node failure). Retryable = `utils.IsRetryable(err)` (network timeouts, `url.Error`) OR `errors.Is(err, errNodeUnavailable)` — the marker wraps transient unavailability ("no VPN client", "node not found in runtime clients", "unsupported node type", inactive node): the runtime registry is a startup snapshot, the node may return. Ladder capped at `maxSyncRetries = 10` (~5.6h over the backoff intervals); exhausted → parked. Non-retryable (the panel answered and refused: non-200, record not found, already in use) → parked IMMEDIATELY (prod `retry_count=1373` was hourly retries against a permanently refusing panel).
- **Parking** = far-future `retry_at` (`parkedRetryAt`, 9999-12-31 23:59 UTC) + terminal cause in `last_error`, status stays pending; `GetPendingSync` (`retry_at IS NULL OR retry_at <= now`) stops selecting it. NOT a fifth state: any legitimate re-transition (`UpdateSubscriptionNodeStatus`, `UpsertSubscriptionNode`, `MarkActiveNodesPendingUpdate`) resets retry metadata and releases the node. No migration.
- **Update→create fallback** (`fallbackUpdateToCreate`): `pending_update` failing with `vpn.ErrSubscriptionNotFound` (from `buildNodeProvision(profilePreserve)` panel read OR `UpdateSubscription`) re-queues the node as `pending_add` (metadata reset by the status transition) and returns nil — the create path provisions on the next pass. Other update errors still retry.
- **Honest traffic card**: `TrafficInfo.Available` (subscription_traffic.go) — false when the subscription has active bindings but ZERO readers among them (proxman/fetch); `FormatSubscriptionMessage` renders «📊 Трафик: недоступен» (no progress bar) instead of a misleading "0.00 из N Гб". Partially readable fleets sum readable nodes with Available=true; `!anySuccess` branch sets Available = len(readers) > 0 (degradation ≠ absent data). Zero active bindings keeps legacy «0 ГБ».
- Commit `11297e7` (9 files, +528/−33); gates: build/vet/lint 0/**2308 tests**/race/gofmt. Tests: parking immediate+ladder+ctx-cancel, fallback two-pass, card scenarios; pre-existing tests re-pinned to retryable errors (`url.Error`/`net.OpError`) where they used bare errors.
