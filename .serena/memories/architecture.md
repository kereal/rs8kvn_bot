# Architecture — rs8kvn_bot

**Версия:** v2.3.4
**Обновлено:** 2026-07-19
**Ветка:** `dev` (HEAD dd27907; продакшн — `main`, последний тег v2.3.4)

## Subserver share-link conversion overhaul
- Поддержка ALPN list→comma string (v2rayN spec)
- Поддержка VMess HTTP-obfs/http-opts/h2-opts (headerType, path, host)
- Поддержка Shadowsocks SIP002 plugin (Clash "obfs"→"obfs-local" alias, plugin-opts serialisation)
- Поддержка VLESS xhttp/splithttp→xhttp normalisation + mode param
- `allowInsecure`/`skip-cert-verify` propagation для VLESS/Trojan/Hysteria/TUIC
- `security=tls` для Trojan/VLESS (3x-ui flat format) вместо legacy `tls` field
- `net.JoinHostPort` для IPv6-safe addresses в Hysteria/TUIC/SS links
- VMess port encoded as string для v2rayNG compatibility
- Добавлены поля в `serverConfig`: `Mode`, `Plugin`, `PluginOpts`
- Удалены неподдерживаемые схемы: SSR, WireGuard, SOCKS

## Access log format change
- С таб-разделителя на space-separated (`appendAccessLogPart`)
- Удалён `INFO` level field
- Добавлено поле `success/total` (- если нет источников)
- Значения с пробелами оборачиваются в кавычки ("`iPhone 15`")
- `fetchAndAggregateSources` возвращает (agg, successCount, totalCount)
- `HandleSubscription` пробрасывает success/total в web handler
- `statusRecorder` tracks per-request source success/total counts
- `logSubscriptionAccess` включает success/total в structured log

## Refactoring 2026-06-08
- Монолит `database.go` разбит на 9 файлов по доменам
- `escapeMarkdown` перемещён в `internal/utils/markdown.go` (экспортирован как `EscapeMarkdown`)
- Удалён мёртвый код: `AddTrialClient`, `Login`, `TestForceSessionExpiry` (xui), `sourceHost` (web)
- Удалён неиспользуемый XUI health-check: `RegisterChecker("xui", ...)` + параметр `xuiClients` в `startWebServer` (`cmd/bot/main.go`, 2026-07-07)
- `ConvertJSONToShareLinks` перенесён в `convert_test_helpers_test.go`
- Убран избыточный `if xuiHeaders != nil` в `subscription_handler.go`
- Проведён `go fmt` по 20 файлам

## Refactoring 2026-07-09 (кандидаты архитектурного обзора)
- **Кандидат 1** — узкий шов `DatabaseService` (`c3c92bf`): `subserver`/`scheduler` берут `interfaces.SubscriptionRepository`, `web` — составной `interfaces.WebRepository`. Мелкие срезы определены в `internal/interfaces/interfaces.go`; `DatabaseService` оставлен composition root в `main.go`. Добавлены пер-срезовые фейки `internal/testutil/db_slice_fakes.go`. См. `docs/adr/0001-narrow-database-service-seam.md`.
- **Кандидат 2** — дедупликация двухфазного удаления (`155bb92`): `Delete`/`DeleteByID` объединены на `revokeAndDeprovisionThenDelete`.
- **Кандидат 3** — вынос презентации трафика (`155bb92`): `TrafficInfo`, `GetWithTraffic`, `formatExpiresAt` перенесены в `internal/service/subscription_traffic.go`.
- **Кандидат 4** — классификация ошибок `vpn` (`183cb85`): классификаторы идемпотентны, применены ко всем Create/Update/Delete клиентов 3x-ui и proxman.
- Итог: `go build ./...` зелёный, `go vet` чистый, 1776 тестов в 22 пакетах.
- `doc/architecture.md` актуализирован: дерево каталогов (`service/`, `vpn/`, `interfaces/`, `testutil/`), описания интерфейсов.

## Applied fixes on dev (2026-07-19)
- **Fix A**: `ActivateProduct` для платных продуктов не активирует подписку сразу. При `PriceCents > 0` создаётся ордер в статусе `pending`, подписка не модифицируется, план не синхронизируется.
- **Fix C**: Broadcast перешёл на безопасный сплит MarkdownV2 без разрыва сущностей. Добавлен `splitMessage`: предпочитает границы строк, fallback — UTF-8 hard-split. В `runBroadcast` восстановлен per-user подсчёт через `userBlocked/userFailed`.
- **Fix E**: Удалены `//go:build integration` теги из `tests/e2e/*.go`.
- **Fix G**: `broadcastSession` получил TTL 15 минут. `startBroadcastSession` проставляет `createdAt`. `getBroadcastSession` автоматически удаляет устаревшие сессии.

## Общая схема
```
Telegram Bot (Go, single binary)
  ├── cmd/bot/main.go         — entry point, graceful shutdown
  ├── internal/bot/           — handlers, referral cache, singleflight
  ├── internal/service/       — SubscriptionService (orchestration) + SyncService (state machine)
  ├── internal/database/      — SQLite + GORM + migrations 000-029
  ├── internal/xui/           — multi-source 3x-ui client + circuit breaker
  ├── internal/vpn/           — VPN client abstraction (3x-ui, proxman, fetch)
  ├── internal/subserver/      — LRU cache, merge, /sub/{id} endpoint, proxy, servers (legacy, не используется), optional async access log
  ├── internal/web/           — /healthz, /readyz, /i/{code}, /sub/{subID}, /metrics, /payment/callback, /static/logo.png + soft-fail startup
  ├── internal/scheduler/     — backup (daily 03:00) + trial cleanup (hourly) + sync workers
  ├── internal/backup/        — SQLite backup with WAL checkpoint
  ├── internal/heartbeat/     — monitoring pings
  ├── internal/metrics/       — Prometheus (через zap-обёртку)
  ├── internal/ratelimiter/   — per-user token bucket
  ├── internal/logger/        — zap setup
  ├── internal/config/        — env loading + validation
  └── internal/flag/          — typed env registry
```

Версия схемы: after migration 029
**Ревизия:** 2026-07-19

### Tab Subscription Nodes — состояние синхронизации
Подробности: см. память `subscription-nodes/state-machine`.
```sql
CREATE TABLE subscription_nodes (
    subscription_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active','pending_add','pending_remove','pending_update')),
    retry_count INTEGER NOT NULL DEFAULT 0,
    retry_at DATETIME,
    last_error TEXT,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (subscription_id, node_id)
);
```
Go-модель: `database.SubscriptionNode` с typed status `SyncStatus`.
Статусы: `active`, `pending_add`, `pending_remove`, `pending_update`.

### Текущие активные таблицы
- `plans` (без `duration`, без `price`), `products` (migration 013+021), `orders` (migration 017)
- `nodes` (migration 014, замена `sources`), `plan_nodes` (M:N), `subscription_nodes` (M:N, migration 018+027)
- `subscriptions`, `invites`, `trial_requests`, `metrics_counters`

### `subscriptions` (после migration 023)
```
telegram_id  int64  INDEX
username     string INDEX
client_id    string NOT NULL UNIQUE
subscription_id string NOT NULL UNIQUE
expires_at   time   INDEX (NULL = perpetual)
status       string default: "active" INDEX (active|revoked|expired)
invite_code  string INDEX
plan_id      uint   INDEX (FK → plans)
referred_by  int64  INDEX
product_id   uint   INDEX (FK → products)
started_at   time
price_paid_cents int64 default: 0
currency     string size:3
devices      json (HWID rotation)
ips          json (ip:timestamp history, max 100)
last_request time   INDEX (migration 028)
created_at   time autoCreate
updated_at   time autoUpdate
```
**Удалено:** `is_trial`, `deleted_at` (soft delete заменён на `status='revoked'`), `traffic_limit`, `inbound_id`, `subscription_url`.

Миграции лежат в `internal/database/migrations/000-029_*.up.sql` (embedded через go:embed). Версия схемы: **after migration 029**.

## Ключевые компоненты
### VPN Client (`internal/vpn/`)
- **`Client` interface**: `CreateSubscription`, `UpdateSubscription`, `DeleteSubscription`, `Close`
- **`ThreeXUIClient`**: адаптер над `interfaces.XUIClient` для 3x-ui нод
- **`Config`**: Host, APIToken, InboundIDs, XUIClient, Type
- Sentinel errors: `ErrSubscriptionAlreadyExists`, `ErrSubscriptionNotFound`, `ErrNotImplemented`
- Классификация ошибок идемпотентна, применяется ко ВСЕМ Create/Update/Delete у ThreeXUIClient и ProxmanClient.

### Sync Service (`internal/service/sync.go`)
- State machine: `pending_add → active`, `pending_update → active`, `pending_remove → row deletion`
- Retry: exponential backoff (1m→2m→5m→15m→30m→45m→60m кап)
- Lock per subscription: `sync.Map`
- `ReconcilePlanNodes`, `SyncPendingNodes`, `ReconcileOrphanedClients`

### Service Layer (`internal/service/subscription.go`)
- `Create`, `CreateTrial`, `BindTrial`, `ReconcileOrphanedClients`, `LinkNodeToPlan`
- `Delete(ctx, telegramID)` / `DeleteByID(ctx, id)` → `revokeAndDeprovisionThenDelete`

### Database (`internal/database/`)
- Монолит `database.go` разбит на 9 файлов (2026-06-08)
- `CreateSubscription` — атомарная транзакция: revoke active + resolve invite + insert
- `BindTrialSubscription` — UPDATE WHERE `telegram_id < 0 AND plan_id = <trial_plan_id>` (колонки `is_trial` нет)
- `CleanupExpiredTrials` — DELETE WHERE expiry_time < now() RETURNING
- Sentinel errors: `database.ErrSubscriptionNotFound`, `database.ErrInviteNotFound`, `database.ErrPlanNotFound`

### Миграции (на dev, 2026-07-19)
- 024_add_plan_is_active, 025_add_retry_check_constraint, 026_remove_subscription_nodes_cascade
- 027_add_pending_update_sync_status, 028_add_last_request_to_subscriptions, 029_rename_subscriptions_expiry_index

### X-UI Client (`internal/xui/client.go`)
- Multi-source: `xuiClients map[uint]interfaces.XUIClient` (key = node ID)
- Circuit breaker: 5 failures → 30s open → half-open (НЕ подключён в prod path — см. CONTEXT.md открытые вопросы)
- Retry: `RetryWithBackoff` (3 retries, exponential + jitter). DNS errors fast-fail.
- Auth: Bearer token, без сессий, singleflight в `internal/web/singleflight.go`

### Bot (`internal/bot/`)
- Referral cache (`referral_cache.go`): in-memory map[tgID]int, Increment/Decrement/Sync (1h)
- Rate limiter (`internal/ratelimiter/`): per-user token bucket
- Singleflight (`internal/web/singleflight.go`)

## Race-safe patterns
### Trial bind (защита double-active)
1. Web-биндинг: `UPDATE trial-row WHERE telegram_id<0 AND plan_id=<trial_plan_id>` (атомарно через `RowsAffected`)
2. Defensive revoke: после успешного UPDATE — revoke всех `active` subs для этого telegram_id
3. Если параллельный `service.Create` выиграет гонку: он ревоукит trial-бинденную sub ДО bind. BindTrial получит `ErrAlreadyActivated`.

## Технические ограничения
### SQLite scale
- Окей до 500-1000 пользователей. `database is locked` при >500 concurrent writes.

### Multi-server config
- Одна подписка = несколько нод, каждому клиенту выдаётся свой inbound на каждой ноде

### Web UI
- Весь UI через Telegram inline-кнопки. Нет классического e-commerce, нет графиков.

## Мониторинг
### Текущий
- `/healthz` (liveness), `/readyz` (DB ready), Sentry, zap, heartbeat scheduler
- Prometheus метрики на `/metrics`: HTTP, bot, DB (GORM callbacks), cache, subserver, circuit breaker, subscription

## Remnawave (out of scope, отложено)
- 3x-ui выбран как проверенный. Критерий пересмотра: >200 клиентов И Remnawave доказал стабильность 6+ мес.
