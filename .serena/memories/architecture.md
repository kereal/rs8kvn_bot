# Architecture — rs8kvn_bot

**Версия:** v2.3.0  
**Обновлено:** 2026-06-28  
**Ветка:** `plans_and_pricing` (merge candidate)

## Рефакторинг 2026-06-08 (коммит 2a1e0fe)
- Монолит `database.go` разбит на 9 файлов по доменам
- `escapeMarkdown` перемещён в `internal/utils/markdown.go` (экспортирован как `EscapeMarkdown`)
- Удалён мёртвый код: `AddTrialClient`, `Login`, `TestForceSessionExpiry` (xui), `sourceHost` (web)
- `ConvertJSONToShareLinks` перенесён в `convert_test_helpers_test.go`
- Убран избыточный `if xuiHeaders != nil` в `subscription_handler.go`
- Проведён `go fmt` по 20 файлам

## Branch: plans_and_pricing

Эта память описывает текущую ветку `plans_and_pricing`, которая включает:
- Multi-outbound flow: группировка inboundIDs по совместимому flow для панели 3x-ui
- Миграция конфига `config.Source` → `config.Node`/`Nodes`
- Defensive copy в `Node.ResolveInboundIDs` (защита от мутации слайса)
- Валидация inbound-ID positivity в `xui/client.go`
- TgID через контекст (`WithTgID`/`TgIDFromContext`) при создании/обновлении клиентов в панель
- Актуализация roadmap и архитектурной памяти

## Общая схема

```
Telegram Bot (Go, single binary)
  ├── cmd/bot/main.go         — entry point, graceful shutdown
  ├── internal/bot/           — handlers, referral cache, singleflight
  ├── internal/service/       — SubscriptionService (orchestration) + SyncService (state machine)
  ├── internal/database/      — SQLite + GORM + migrations 000-027
  ├── internal/xui/           — multi-source 3x-ui client + circuit breaker
  ├── internal/vpn/           — VPN client abstraction (3x-ui, proxman)
  ├── internal/subserver/      — LRU cache, merge, /sub/{id} endpoint, proxy, servers, optional async access log
  ├── internal/web/           — /healthz, /readyz, /i/{code}, /sub/{subID}, access-log response recording and soft-fail startup
  ├── internal/scheduler/     — backup (daily 03:00) + trial cleanup (hourly) + sync workers
  ├── internal/backup/        — SQLite backup with WAL checkpoint
  ├── internal/heartbeat/     — monitoring pings
  ├── internal/metrics/       — Prometheus (через zap-обёртку)
  ├── internal/ratelimiter/   — per-user token bucket
  ├── internal/logger/        — zap setup
  └── internal/config/        — env loading + validation
```
Telegram Bot (Go, single binary)
  ├── cmd/bot/main.go         — entry point, graceful shutdown
  ├── internal/bot/           — handlers, referral cache
  ├── internal/service/       — SubscriptionService (orchestration) + SyncService (state machine)
  ├── internal/database/      — SQLite + GORM + migrations 000-027
  ├── internal/xui/           — multi-source 3x-ui client + circuit breaker
  ├── internal/vpn/           — VPN client abstraction (3x-ui, proxman)
  ├── internal/subserver/      — LRU cache, merge, /sub/{id} endpoint, proxy, servers, optional async access log
  ├── internal/web/           — /healthz, /readyz, /i/{code}, /sub/{subID}, access-log response recording and soft-fail startup
  ├── internal/scheduler/     — backup (daily 03:00) + trial cleanup (hourly) + sync workers
  ├── internal/backup/        — SQLite backup with WAL checkpoint
  ├── internal/heartbeat/     — monitoring pings
  ├── internal/metrics/       — Prometheus (через zap-обёртку)
  ├── internal/ratelimiter/   — per-user token bucket
  ├── internal/logger/        — zap setup
  └── internal/config/        — env loading + validation
        ↓
   3x-ui Panels (1..N, map[uint]XUIClient)
        ↓
   VLESS+Reality Servers
        ↓
   Client (Happ, V2RayNG, etc.)
```


Версия схемы: after migration 027  
**Ревизия:** 2026-06-28

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

### Таблицы подписок (существующие после migration 023)
- `subscriptions`, `invites`, `trial_requests` — структурно без новых колонок после migration 023; в `subscriptions` добавлены `NOT NULL UNIQUE` constraints/indexes для `client_id` и `subscription_id`.

### УСТАРЕВШИЕ таблицы
- `sources`, `plan_sources` — заменены на `nodes` и `plan_nodes` (migration 014). Остаются в БД для обратной совместимости, но не используются в новом коде.

### Текущие активные таблицы
- `plans` — тарифные планы (без `duration`, без `price`).
- `products` (migration 013) — покупаемые продукты, привязанные к планам.
- `orders` (migration 017) — факт покупки и обработка платежа.
- `nodes` (migration 014) — замена `sources`, VLESS+Reality серверы.
- `plan_nodes` (M:N: plan → nodes) — привязка серверов к тарифным планам.
- `subscription_nodes` (M:N: subscription → nodes) — серверы, выданные конкретной подписке.

### `subscriptions` (версия после migration 023)
```
telegram_id          int64    INDEX
username             string   INDEX
client_id            string   NOT NULL UNIQUE
subscription_id      string   NOT NULL UNIQUE
expires_at          time     INDEX
status               string   default: "active"  INDEX  (active|revoked|expired)
invite_code          string   INDEX
plan_id              uint     INDEX   (FK → plans)
referred_by          int64    INDEX
product_id           uint     INDEX   (FK → products)
started_at           time
price_paid_cents     int64    default: 0
currency             string   size:3
devices              json     devices list with hwid rotation
ips                  json     ip:timestamp history (max 100)
created_at           time
updated_at           time
```
**Удалено:** `is_trial`, `traffic_limit`, `inbound_id`, `subscription_url`, `deleted_at`.  
**Soft delete заменён** на `status='revoked'`.  
**Добавлено:** `devices`, `ips`, `plan_id`, `product_id`, `started_at`, `price_paid_cents`, `currency`; migration 023 добавила `NOT NULL UNIQUE` для `client_id` и `subscription_id`.

### `plans`
```
id, name UNIQUE, devices_limit, traffic_limit
```
**Удалено в migration 019:** `duration`.  
**Удалено в migration 016:** `price`.

### `products` (migration 013 + 021)
Покупаемые продукты, привязанные к планам.
- `id`, `plan_id` (FK → plans), `name` (VARCHAR(255) NOT NULL), `duration_days`, `price_cents`, `currency`, `is_active`, `created_at`, `updated_at`.

### `orders` (migration 017)
Факт покупки подписки и процесс обработки платежа.
- `id`, `subscription_id` (FK → subscriptions), `product_id` (FK → products), `status` (CHECK: `pending|paid|expired|canceled`), `amount_cents`, `currency`, `payment_provider`, `provider_payment_id`, `created_at`, `paid_at`, `activated_at`, `expires_at`.

### `invites`, `trial_requests`, `metrics_counters` — без изменений.

Миграции лежат в `internal/database/migrations/000-027_*.up.sql` (embedded через go:embed).  
Версия схемы: **after migration 027**.

## Ключевые компоненты

### VPN Client (`internal/vpn/`)
- **`Client` interface**: `CreateSubscription`, `UpdateSubscription`, `DeleteSubscription`, `Close`
- **`ThreeXUIClient`**: адаптер над `interfaces.XUIClient` для 3x-ui нод
- **`Config`**: Host, APIToken, InboundIDs, XUIClient, Type
- Sentinel errors: `ErrSubscriptionAlreadyExists`, `ErrSubscriptionNotFound`, `ErrNotImplemented`

### Sync Service (`internal/service/sync.go`)
- **State machine**: `pending_add → active`, `pending_update → active`, `pending_remove → row deletion`
- **Retry logic**: exponential backoff (1m→2m→5m→15m→30m→45m→60m кап)
- **Lock per subscription**: `sync.Map` для предотвращения race conditions
- **`ReconcilePlanNodes`**: сравнивает текущие ноды с целевыми по плану
- **`SyncPendingNodes`**: обрабатывает все pending nodes с автоматической очисткой orphan records

### Service Layer (`internal/service/subscription.go`)
- **`Create(ctx, chatID, username, inviteCode string)`** — создание free/paid подписки. Параметр `inviteCode` пробрасывается в `db.CreateSubscription` для резолва referrer.
- **`CreateTrial(ctx, chatID, username, inviteCode string)`** — итерация по trial-источникам, **errors.Join** агрегация, partial success → Warn, all-fail → Error + return.
- **`BindTrial(ctx, subID, telegramID, username, inviteCode string)`** — обновляет ВСЕ trial-источники (`xui.UpdateClient`), в `db.BindTrialSubscription` — defensive revoke всех active subs для telegram_id (защита double-active race).
- **`ReconcileOrphanedClients(ctx)`** — проверяет ВСЕ источники, удаляет только если клиент не найден НИГДЕ.
- **`LinkNodeToPlan(ctx, planName, nodeID)`** — создаёт план-ноду в `plan_nodes`.

### Database (`internal/database/` — 9 файлов)

Монолитный `database.go` (1216 строк) разбит на файлы по доменам (2026-06-08, коммит 2a1e0fe):
- `models.go` — все GORM-модели, `TableName()`, хелперы `Subscription`, `PoolStats`, константы `TrialPlanName`/`FreePlanName`
- `migrations.go` — `//go:embed`, `runMigrations()`
- `service.go` — `Service` struct, `NewService()`, `Close()`, `Ping()`, `GetPoolStats()`
- `subscriptions.go` — Subscription/TelegramID CRUD (19 функций)
- `nodes.go` — Node/Plan CRUD + `LinkNodeToPlan`
- `invites.go` — Invite/Referral CRUD (5 функций)
- `trials.go` — Trial CRUD (7 функций), включая `BindTrialSubscription`
- `orders.go` — Order CRUD (4 функции)
- `products.go` — `GetActiveByPlanID()`
- `subscription_nodes.go` — SubscriptionNode CRUD (10 функций)
- **`CreateSubscription(ctx, sub, inviteCode string)`** — атомарная транзакция: revoke всех active subs для telegram_id + resolve invite → заполнение `sub.InviteCode` и `sub.ReferredBy` + insert; `sub.ClientID` и `sub.SubscriptionID` должны быть непустыми и уникальными после migration 023.
- **`BindTrialSubscription(ctx, sub, telegramID, username)`** — UPDATE trial-row WHERE telegram_id=0 AND plan_id=trial → revoke других active subs для этого telegram_id в той же транзакции. Динамический поиск trial/free plans по имени (`TrialPlanName`/`FreePlanName`).
- **`CleanupExpiredTrials(ctx)`** — DELETE WHERE expiry_time < now() RETURNING subscription_id (SQLite ≥ 3.35).
- **Sentinel errors**: `database.ErrSubscriptionNotFound`, `database.ErrInviteNotFound`, `database.ErrPlanNotFound`
- **`CreateNode(ctx, node)`** — вставляет новую ноду в БД. Управление нодами только через DB (env seed удалён).

### НОВЫЕ миграции (plans_and_pricing)
- `024_add_plan_is_active` — добавление is_active в plans
- `025_add_retry_check_constraint` — CHECK constraint для retry_count
- `026_remove_subscription_nodes_cascade` — исправление внешних ключей
- `027_add_pending_update_sync_status` — добавление pending_update статуса

### X-UI Client (`internal/xui/client.go`)
- **Multi-source**: `xuiClients map[uint]interfaces.XUIClient` (key = node ID).
- **Circuit breaker**: `internal/xui/breaker.go` — 5 failures → 30s open → half-open. Метрика `metrics.CircuitBreakerState`.
- **Retry**: `RetryWithBackoff` (3 retries, exponential + jitter). DNS errors fast-fail.
- **Auth**: Bearer token, без сессий, без singleflight (singleflight перенесён в `internal/web/singleflight.go`).

### Bot (`internal/bot/`)
- **Referral cache** (`referral_cache.go`): in-memory map[tgID]int, `Increment`/`Decrement`/`Sync` (1h interval из БД).
- **Rate limiter** (`internal/ratelimiter/`): per-user token bucket.
- **Singleflight** (`internal/web/singleflight.go`): дедупликация одновременных DB queries.

## Race-safe patterns

### Trial bind (защита double-active)
1. Web-биндинг: `UPDATE trial-row WHERE telegram_id=0 AND plan_id=<trial_plan_id>` (атомарно через `RowsAffected`).
2. **Defensive revoke**: после успешного UPDATE — revoke всех `active` subs для этого telegram_id (кроме только что забинденной).
3. Если параллельный `service.Create` для того же telegram_id выиграет гонку: он ревоукит trial-бинденную sub ДО bind. BindTrial получит `ErrAlreadyActivated` (RowsAffected=0, т.к. telegram_id уже не 0).

### Create vs BindTrial TOCTOU
- Узкое окно: `BindTrialSubscription find → CleanupExpiredTrials delete`. Cleanup может удалить trial-row, который Bind собирается обновить. Bind получит `ErrAlreadyActivated`. Юзер может запросить trial снова. **Не критично** (узкое окно).

## Технические ограничения

### 🟡 SQLite scale
- Окей до 500-1000 пользователей. `database is locked` при >500 concurrent writes.
- Миграция на PostgreSQL: 2-4 часа (заменить драйвер, проверить миграции).

### ✅ Multi-server config
- Одна подписка = несколько нод, каждому клиенту выдаётся свой inbound на каждой ноде
- VLESS поддерживает массив серверов нативно, клиенты переключаются автоматически

### 🟡 Web UI
- Весь UI через Telegram inline-кнопки. Нет классического e-commerce, нет графиков.

## Паттерны

### Circuit Breaker (`internal/xui/breaker.go`)
- State: Closed → Open (5 fails) → Half-Open (30s) → Closed
- Защита от DDOS упавшего x-ui, быстрый fail вместо таймаута.

### Graceful Shutdown (`cmd/bot/main.go`)
- SIGTERM → cancel context → stop HTTP server (30s) → stop cron → WaitGroup goroutines → flush logs → exit 0.

### Race-safe Bind (см. выше)

## Решения

### ✅ Multi-source 3x-ui (v2.4.0, реализовано)
- Конфиг переведён на `config.Node` + `[]config.Node` (`config.Source` удалён).
- Trial — на всех trial-нодах. BindTrial — первый успешный. Reconcile — все.

### ✅ Orders + Products (migration 013/017, реализовано)
- `Product` — покупаемый продукт, привязанный к плану.
- `Order` — факт покупки, статусы `pending|paid|expired|canceled` (CHECK constraint).
- ORM-связи: `Plan.Products`, `Product.Orders`, `Subscription.Orders`.

### ✅ Subscription Nodes state machine (migration 018 + 027)
- `active|pending_add|pending_remove|pending_update`
- `SyncService` реализует state machine с retry и lock-ом

### ✅ VPN Client abstraction (v2.3.0)
- Интерфейс `Client` в `internal/vpn/client.go`
- `ThreeXUIClient` адаптер для 3x-ui
- Поддержка `NodeType3xUI` и `NodeTypeProxman`

### ⏸ Кастомный генератор подписок (отложено)
- VLESS поддерживает массив серверов нативно. Клиенты (Happ, V2RayNG) переключаются автоматически. Не нужно переписывать бота.

### ✅ SQLite для старта
- Простой деплой, ок для 10-1000 юзеров. Пересмотреть при >500.

## Безопасность

### Реализовано
- Circuit breaker, rate limiting, input validation, no hardcoded secrets (.env only), graceful shutdown, HTTP timeouts, X-Forwarded-For validation, html/template (XSS prevention).

### Нужно добавить (см. `roadmap`)
- Encryption at rest, audit logs, 2FA для админа, backup encryption.

## Мониторинг

### Текущий
- `/healthz` (liveness), `/readyz` (DB ready), Sentry, zap, heartbeat scheduler.

### Нужно добавить
- Prometheus, Grafana, алерты, real-time активность.

## Remnawave (out of scope, отложено)
- 3x-ui выбран как проверенный (годы развития, огромное комьюнити). Remnawave — молодой, рискованно. **Критерий пересмотра**: >200 клиентов И Remnawave доказал стабильность 6+ мес.
