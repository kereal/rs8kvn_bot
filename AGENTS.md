# Instructions for AI Agents

## Before starting work

1. Отвечай всегда на русском
2. После окончания работы, если это требуется, обновляй документацию и память
3. Выбирай и применяй подходящие skills
4. Используй подходящие mcp-инструменты
5. Инструменты: структурные вопросы по коду (кто вызывает, что реализует, что
   объявлено в файле) — сначала граф кода; текстовые (литералы, SQL, конфиги) —
   grep/glob. Маршрутизация и подводные камни — в разделе
   «Codebase Knowledge Graph» ниже.

## Git commits

- Коммит-сообщения — Conventional Commits: `feat:`, `fix:`, `docs:`, `test:`, `chore:` и т.п.
- НИКОГДА не добавлять авто-футеры вида «Generated with Codebuff» / «Co-Authored-By: …»
  в сообщения коммитов — только описание изменения.

## Память проекта (Hindsight)

Долговременная память: Hindsight, self-hosted. Сервер — localhost:8888. Банк этого репозитория: omp-rs8kvn_bot (маппинг в ~/.hindsight/coding-agent.json)
Можешь искать и записывать данные

## RTK - Rust Token Killer

**Usage**: Token-optimized CLI proxy for shell commands.

### Rule

Always prefix shell commands with `rtk` to minimize token consumption.

Examples:

```bash
rtk git status
rtk ls src/
rtk grep "pattern" src/
rtk find "*.rs" .
rtk docker ps
rtk gh pr list
```

## Codebase Knowledge Graph (codebase-memory-mcp)

Project name in the graph: **`home-kereal-rs8kvn_bot`** (auto-generated from repo path).
The index is `ready` and auto-refreshes (git watch, ~5 s); never call
`index_repository` for this project.

One routing rule, and it is about the *question*, not about preference:

- **Structure** — who calls what, what implements what, what a change breaks, what
  a file declares → the graph, first call, before any grep.
- **Text** — literals, tags, SQL, configs, anything that is not a symbol → grep/glob.
  The graph is not a text index; a graph miss here proves nothing.

### Structural question → graph tool (mandatory first stop)

Every call takes `project="home-kereal-rs8kvn_bot"` (omitted in the table for width).
Suspicious results → `index_status()`: `indexed_at` plus `watch.last_scan_at` tell you
whether the answer describes the working tree you are looking at.

| Question | Call |
|---|---|
| Who calls / what does X call | `trace_path(function_name="X", direction="inbound"\|"outbound")` |
| Impact of changing a signature, deleting a symbol, narrowing an interface | `trace_path(function_name="X", direction="inbound", depth=2, include_tests=true, risk_labels=true)` |
| Who implements an interface / embeds a type | `query_graph(query="MATCH (t)-[:IMPLEMENTS]->(i) WHERE i.name='X' RETURN t.qn, t.file")` |
| What a file declares, without reading the file | `get_file_outline(file_path="internal/service/subscription.go")` |
| Find a symbol by approximate name | `search_graph(name_pattern=".*Handler.*")` or `query="revoke subscription"` |
| Read one symbol | `get_code_snippet(qualified_name="home-kereal-rs8kvn_bot.internal.service.markRevokedSub")` |
| Module map, layering, entry points | `get_architecture()` |
| Multi-hop / aggregation over calls | `query_graph(...)` |

Unqualified names resolve (`function_name="markRevokedSub"` works); `qn` is
`<project>.<package path>.<name>`, and tool output groups rows by `qn_prefix` + `file`.

### Text question → grep/glob (do not spend a graph call)

- string literals, log messages, error text, `json:"..."` / struct tags, regexes;
- SQL and everything under `internal/database/migrations/`;
- Dockerfiles, compose, YAML, shell, `.env`, `admin/dist`, docs;
- files whose parse is incomplete — `index_status(diagnostics="full")` currently flags
  exactly 19: 17 migration `*.up.sql`/`*.down.sql`, `internal/subserver/templates/xray_client.json`
  and `internal/database/orders_admin_test.go`. Constructs inside the flagged line
  ranges may be absent, so grep them instead of trusting a graph miss — that JSON
  template is the Xray profile template, a hot spot in this repo;
- `not_indexed` paths (`.env`, `bot`, `rs8kvn_bot`, `coverage.out`, `mcp.json`,
  `admin/dist`, `admin/node_modules`, `data`, `tmp`, `.planning`, `.serena/cache`, images) —
  by design, not an error.

### Verify before you act on a graph answer

- `search_graph` rows are name matches, not proofs: `in`/`out` counts say nothing
  about *which* edge. Read the cited symbol (`get_code_snippet`) before editing it.
- `trace_path` **excludes tests by default**, and in this repo tests are callers
  (`TestDeactivateBlockedBroadcastRecipients_*` → `markRevokedSub`). For impact
  analysis always pass `include_tests=true`; otherwise you will "safely" break a
  behaviour that is under test.
- A caller list is only as good as the parse: `check_index_coverage(paths=[...])`
  before treating "no callers" as "dead code".
- `cross_repo` has never been run — `mode="cross_service"` returns nothing here.
- Pages are real: check `has_more`/`next_cursor` instead of assuming a listing ended.

### Cost discipline

- Graph for discovery, `read` for proof — never call the graph to re-confirm what
  `read` already showed.
- One `trace_path(depth=2, include_tests=true)` beats five `search_graph` calls.
- Count/repetition questions stay in shell (`grep -c`); the graph is for reachability.

### Examples

- Who calls a revoke helper, tests included:
  `trace_path(project="home-kereal-rs8kvn_bot", function_name="markRevokedSub", direction="inbound", include_tests=true)`
- Declarations of a 500-line file without reading it:
  `get_file_outline(project="home-kereal-rs8kvn_bot", file_path="internal/service/subscription.go")`
- Find a handler: `search_graph(project="home-kereal-rs8kvn_bot", name_pattern=".*Handler.*")`
- Read one function: `get_code_snippet(project="home-kereal-rs8kvn_bot", qualified_name="home-kereal-rs8kvn_bot.internal.service.markRevokedSub")`
- Check architecture: `get_architecture(project="home-kereal-rs8kvn_bot")`


## Docs

Don't read and don't write
  * bypass_clients_comparison.md
  * bypass_research.md
  * marketing_strategy.md
  * nginx-xhttp-hysteria2-architecture.md
  * task-bot-integration.md


## Audit Log (таблица audit_log, миграция 039)

Универсальный append-only журнал аудита системы: `entity_type` + `entity_id`
адресуют любую сущность (subscription/order/broadcast/plan/node/product/system),
`actor_type` + `actor_id` — кто инициировал (user/admin/worker/payment_provider/system).

- **Константы событий** (`AuditEvent*`) и **конструкторы** (`AuditSubscriptionCreated`,
  `AuditPaymentConfirmed`, ...) — только в `internal/database/audit_log.go`.
  Новое событие = одна константа + один конструктор там; БЕЗ миграции
  (CHECK-констрейнтов на event/entity_type нет сознательно).
- **Best-effort по умолчанию**: сервисы пишут через `s.logAudit(ctx, ...)` —
  ошибка журнала логируется `Warn` и не влияет на бизнес-операцию.
  Внутри пакета database (где сервисного хелпера нет) — `insertAuditLogBestEffort`.
- **Исключение — платежные tx**: в `ConfirmOrderPaidCAS` и
  `CancelPaidOrderAndDowngradeCAS` аудит пишется через `InsertAuditLogInTx(tx, ...)`
  в той же транзакции — откат tx откатывает и событие. Best-effort INSERT
  ВНУТРИ tx-замыкания на SQLite дедлочится (write-lock до коммита) —
  события «по факту коммита» пишите после возврата `Transaction()`
  (см. `expiredOrder` в `FindOrCreatePendingPaymentOrder`).
- **Одна мутация = событие в каждой адресуемой сущности**: оплата — это
  `payment_confirmed` (order) И `subscription_renewed` (subscription).
- **Сводки за прогон — только при исходе**: `orphaned_clients_revoked`
  пишется при revoked > 0, нулевые прогоны журнал не засоряют.
- **Не логировать**: touches при /sub-запросах (devices/ips/last_request),
  per-item Warn-ретраи воркеров, трафик-сканы без исхода. Технические события —
  только сводки за прогон (`sync_run_degraded`, `orphaned_clients_revoked`).
- **Retention не удаляет платёжные события** (`payment_confirmed`,
  `payment_auto_refunded` — `auditRetentionExemptEvents`): по ним
  `autoRefundStarsCharge` отличает дубль charge от денег за уже оказанную
  услугу. Добавляя событие в этот список, помни: журнал для него — рабочее
  состояние, а не история.
- **FK нет**: история сущности переживает физическое удаление (admin /del).
- actor_id админа передаётся через context: `service.WithAuditAdmin(ctx, chatID)`
  в HandleDel/HandleSetPlan, читается `adminTelegramIDFromCtx(ctx)`.
- Чтение: `GetAuditLogByEntity` / `GetAuditLogByEvent` / `GetRecentAuditLog`.


## Back-button navigation (CRITICAL — was broken once)

Screens with content in a SEPARATE message (QR photo, invite QR) + Back button:
**Open** = send new message, keep card underneath. **Back** = delete only that
message (its id comes in the callback), never re-show the card — re-sending
spawns a stray duplicate. Guard: `TestNavigation_OpenAndBack`
(`internal/bot/repro_qr_test.go`). Ref: `handleQRCode`/`handleBackToSubscription`.

## Error Handling Conventions

This project distinguishes between user-initiated operations (must be reliable) and background best-effort work (can tolerate partial failure).

**Subscription deletion policy**: subscriptions are NEVER physically deleted, except two cases:
1. admin `/del` — `SubscriptionService.DeleteByID` (the only product deletion path, backed by `DeleteSubscriptionByID`);
2. expired anonymous trials — `CleanupExpiredTrials` (trials are not real user subscriptions).
Everything else only changes subscription status (`revoked`, free-plan downgrade, reanimation). There is no `Delete(telegramID)` anymore — it was a dead duplicate.

**Revocation lifecycle** (`revoked` is not just a DB flag): every revoke MUST go through the shared primitives in `internal/service/subscription.go` — `markRevokedSub` (durable status flip; subserver 404) and `deprovisionSub` (bindings → `pending_remove`, then best-effort `SyncSubscription`). Revoking without deprovisioning leaves the already-imported VPN profile working while the URL 404s. Callers: `DeleteByID` (`/del`) and `DeactivateBlockedBroadcastRecipients` (broadcast cleanup).

- **User-initiated** (`Create`, `BindTrial`, `RenewSubscription`, `DeleteByID`): return errors to the caller. The handler will surface the failure to the user. Do NOT log + continue silently.
- **Provisioning (two-phase, user-initiated)**: Provisioning splits into a **DB-setup phase** and an **external-sync phase**, with different reliability contracts.
  - **DB-setup phase** (`GetNodesByPlanID`, `MarkActiveNodesPendingUpdate`, `ReconcilePlanNodes`): pure DB operations that create/update `pending_add`/`pending_remove` records. These are **structural prerequisites** — without them the background worker has nothing to retry — so failures MUST be returned to the caller (the handler surfaces the error to the user). The user sees an error, but the subscription/order row is already committed (status `active`); the state is recoverable via `ReconcileOrphanedClients`/`SyncPendingNodes` once the DB issue is resolved.
  - **External-sync phase** (`SyncSubscription`): best-effort. Calls the XUI/proxman node API to materialize VPN clients. If this immediate sync fails, the subscription stays `active` and the background `SyncPendingNodes` worker retries with exponential backoff. The user may receive a "success" response before VPN access is fully provisioned; this is the documented trade-off. Sub URL is valid immediately (subserver serves config once the client is provisioned by the background worker).
  - This two-phase split replaces the previous blanket "provisioning is eventual-consistency" wording: DB-setup is synchronous-must, external-sync is best-effort.
- **Delete flow** (`DeleteByID` only — admin `/del`): two-phase. Phase 1 marks the subscription `revoked` (so `/sub/{id}` returns 404 immediately). Phase 2 deprovisions VPN access via sync (best-effort; background sync retries on failure). Phase 3 physically deletes the DB row. If deprovision fails, the subscription stays revoked and `SyncPendingNodes` finishes the VPN-client removal in the background.
- **Broadcast deactivation** (`DeactivateBlockedBroadcastRecipients`): user-initiated admin action. Per-recipient failures are aggregated (`errors.Join`) and returned together with `DeactivationResult{Targets, Revoked, Failed}`, so the handler shows a partial result instead of a false success. `subscription_revoked` is written right after the status flip (before external calls) — the revoke happened even if deprovisioning fails; a failed deprovision returns an error because no `pending_remove` row was created for the worker to retry.
- **Background sync** (`SyncSubscription` for single-sub, `SyncPendingNodes`, `ReconcilePlanNodes`): per-item failures are logged as `Warn` and processing continues. `ReconcileOrphanedClients` REVOKES orphaned subscriptions (status `revoked`) instead of deleting them — the row stays in the DB, the subserver serves 404. `SyncPendingNodes` returns an aggregate error (`errors.Join`) on partial failures so the caller can observe degraded runs; the scheduler (`SubscriptionSyncWorker`) treats this as best-effort (`logger.Warn`) and does NOT abort or change retry cadence. Only `context.Cancelled`/`DeadlineExceeded` abort the scan early.
- **Never** use `panic` for control flow in handlers or services. Panic recovery exists only at the top level (`main.go`, `handleUpdateSafely`).
- Always wrap errors with `%w` to preserve the chain for `errors.Is` / `errors.As` checks.
- Sentinel errors (`database.ErrSubscriptionNotFound`, `xui.ErrClientNotFound`) are the preferred way to signal expected "not found" states. Callers must use `errors.Is` to distinguish them from infrastructure errors.
- **Traffic notifications** (`SubscriptionService.ProcessTrafficNotifications` / `SubscriptionTrafficWorker`): best-effort on traffic-limited plans only (`traffic_limit > 0`). Per-node failures are `Warn`-logged and skipped; per-subscription failures are `Warn`-logged and the scan continues. The worker NEVER re-enables a client whose quota is still exceeded — exhausted is a pure revenue CTA. It re-enables (via `UpdateClient(Enable=true)`) only when the counter was reset below the limit but the client is still disabled. Idempotency via `traffic_reminders_sent` bitmask (migration 038).


## Xray JSON profiles: allowInsecure is DEAD (Xray-core 26.7.28+)

Новые ядра (happ/v2rayNG после обновления) **отказываются грузить весь
профиль**, если в любом outbound есть `tlsSettings.allowInsecure: true`
(«The feature "allowInsecure" has been removed»). Генерация Xray JSON
(`internal/subserver/xray.go`) НИКОГДА не испускает `allowInsecure`;
доверие к сертификату — только через `pinnedPeerCertSha256` (pcs/pinSHA256)
и `verifyPeerCertByName` (vcn), которые старые ядра молча игнорируют.
Вход: плоский JSON узла с ключами `pcs`/`pinSHA256`/`vcn` (новый 3x-ui их
уже отдаёт), нормализация в `serverConfig.PinSHA256`/`Vcn`. Share-ссылки
сохраняют `allowInsecure=1`/`insecure=1` — их конвертируют клиенты сами.
Sing-box (`tls.insecure`) и Mihomo (`skip-cert-verify`) полем не затронуты.
Регресс: `TestConvertSingleJSONToXray_NoAllowInsecure`.

## Xray JSON profiles: plaintext VLESS/Trojan — quarantine (2026-11+)

Следующее ограничение новых ядер: `vless`/`trojan` outbound **без transport
security** ядро собирает только на private IP / локальном домене
(`requiresTransportSecurity`, ошибка «vless without TLS … is prohibited
unless the server address is a private IP or domain») — снова падает ВЕСЬ
профиль. `buildXrayOutbound` возвращает ошибку на такой ноде, батч-конвертер
её скипает (Warn) — остальные узлы живы. Только Xray-путь: sing-box и Mihomo
plaintext принимают. Публичные CF-worker узлы (ws на IP Cloudflare без TLS)
на новых ядрах не работают в принципе — это не баг конвертера.
Регресс: `TestConvertJSONConfigsToXray_PlaintextQuarantine`.

## Admin panel (admin/)

Панель — тонкий клиент к тому же сервисному слою, что и бот: Go-хендлеры
(`admin/*.go`) + Svelte 5 + Vite без runtime-зависимостей
(`admin/src/**`, один бандл ~150 КБ JS, `dist/` закоммичен и встроен в бинарь).

- **Контракт**: `doc/adr/0002-admin-panel-contract.md` и `doc/specs/admin/*.md`
  (00-common + спека на экран). ТЗ на экран — источник истины по маршрутам,
  DTO и guard'ам.
- **Запреты**: SQL к таблицам домена из пакета `admin` (только через
  `internal/database/*_admin.go`), `tgbotapi` в панели, второй воркер
  рассылок. Панель не отправляет сообщения пользователям.
- **Мутации — через сервисы**: отзыв/удаление/продление/сброс трафика идут в
  `internal/service`, а не в репозитории. Аудит таких решений —
  `actor_type=admin` (`AuditSubscriptionRevokedByAdmin`, id панели = 0).
- **Сборка**: `cd admin && npm run build` (обязательно после правок вью —
  коммитится `dist/`). Харнесс для ручного прогона: `go run ./tools/adminserve` (копирует
  `data/rs8kvn.db` в `/tmp/rs8kvn-admin-dev.db`, узлов нет — внешние
  вызовы no-op).
- **Ловушки**, уже стоившие отладки: `onMount(load)` обязателен во вью;
  роут в `App.svelte` обёрнут в `{#key route.name/route.id}` (без ключа
  detail→detail показывает предыдущую запись); `bind:value` на
  `input[type=number]` даёт число, а не строку; секреты (api_token)
  никогда не возвращаются в GET.
