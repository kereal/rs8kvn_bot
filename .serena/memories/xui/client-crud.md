# 3x-ui Client CRUD API (post-update 2026-05)

Все операции над клиентами переехали с legacy `/inbounds/*` на новые `/panel/api/clients/*` endpoints.
**Email-centric** — большинство операций идентифицируют клиента по email (без inboundID).

## Add: POST /panel/api/clients/add
- Body: `{client: {id, email, subId, totalGB, expiryTime, ...}, inboundIds: [N]}`
- Old: `/inbounds/addClient` + escaped settings string (УДАЛЁН)
- Single-inbound-per-sub semantics сохранён для backward compat с bot service layer.

## Delete: POST /panel/api/clients/del/{email}
- Path: email (url.PathEscape).
- Query (opt): `keepTraffic=1` — НЕ используется (traffic record дропается).
- Response: `{success:true, msg:"Client deleted"}`.
- **Delete by email removes client from ALL attached inbounds automatically.**
- Сигнатура: `XUIClient.DeleteClient(ctx, email string) error` (было: `+inboundID+clientID/UUID`).

## Update: POST /panel/api/clients/update/{email}
- Path: current email (identifier).
- Body: **full client object** (replaces the row, not patch). Поля: `id, email (new), totalGB, expiryTime, tgId, enable, subId, limitIp, flow, reset, comment`.
- Сигнатура: `UpdateClient(ctx, currentEmail, clientID, email, subID string, trafficBytes, expiryTime, tgID int64, comment string) error`.
- `clientID` (uuid) — для поля `id` в body, сохраняет внутренний ID при replace.
- Fallback `flow="xtls-rprx-vision"` чтобы не затереть protocol settings при full replace.
- Callers (только trial → full upgrade):
  - `service/subscription.go:BindTrial` — `currentEmail = "trial_" + subscriptionID`
  - `bot/command.go:handleBindTrial` — то же вычисление (дубликат bind logic)

## Traffic: GET /panel/api/clients/traffic/{email}
- Response: `{success:true, obj: {email, up, down, total, expiryTime, ...}}`
- Сигнатура: `GetClientTraffic(ctx, email string)`.
- Используется:
  - `service/subscription.go:GetWithTraffic` (показ трафика юзеру).
  - `service/subscription.go:ReconcileOrphanedClients` (проверка существования клиента; "client not found" в Msg при `!success`).
- Старый endpoint `/inbounds/getClientTraffics` полностью удалён.

## ErrClientNotFound sentinel
- `xui.ErrClientNotFound` — для `errors.Is` в Reconcile.

## Миграция
- Обновлено: `interfaces/interfaces.go`, `testutil/Mock*`, все `*_test.go` (bot, database, scheduler, xui, e2e), integration mocks, real handler prefixes.
- Scheduler `XUICleanupTarget` и anonymous interfaces в `TrialRepository` — приведены к email-only `DeleteClient`.
- 1619 short-тестов зелёные.

## См. также
- Auth: `xui/auth-mechanism.md`
- Reset (auto-renew): `xui/reset-mechanism.md`
