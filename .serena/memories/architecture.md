# Архитектура — rs8kvn_bot

**Версия:** v2.4.0  
**Обновлено:** 2026-06-03

## Общая схема

```
Telegram Bot (Go, single binary)
  ├── cmd/bot/main.go         — entry point, graceful shutdown
  ├── internal/bot/           — handlers, referral cache, singleflight
  ├── internal/service/       — SubscriptionService (orchestration)
  ├── internal/database/      — SQLite + GORM + migrations 000-011
  ├── internal/xui/           — multi-source 3x-ui client + circuit breaker
  ├── internal/subserver/      — LRU cache, merge, /sub/{id} endpoint
  ├── internal/web/           — /healthz, /readyz, /i/{code}, /sub/{id}
  ├── internal/scheduler/     — backup (daily 03:00) + trial cleanup (hourly)
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

## Схема БД (v2.4.0, после migration 011)

### `subscriptions` (заменяет старую soft-delete модель)
```
telegram_id          int64    INDEX
username             string   INDEX
client_id            string
subscription_id      string   INDEX (unique)
expiry_time          time     INDEX
status               string   default: "active"  INDEX  (active|revoked|expired)
invite_code          string   INDEX
plan_id              uint     INDEX   (FK → plans)
referred_by          int64    INDEX
created_at           time
updated_at           time
```
**Удалено** в migration 011: `is_trial`, `traffic_limit`, `inbound_id`, `subscription_url`, `deleted_at`.
**Soft delete заменён** на `status='revoked'`.

### `sources` (multi-source 3x-ui панели)
```
id, x_ui_host, x_ui_api_token, x_ui_inbound_id, sub_url, active
```

### `plans` (тарифные планы)
```
id, name UNIQUE, price, devices_limit, traffic_limit, duration
```

### `plan_sources` (M:N: план → источники)
```
plan_id, source_id  (composite PK)
```

### `invites`, `broadcast_*`, `metrics_counters` — без изменений.

Миграции лежат в `internal/database/migrations/000-011_*.up.sql` (embedded через go:embed). Новая миграция: `012_*.up.sql`.

## Ключевые компоненты

### Service Layer (`internal/service/subscription.go`)
- **`Create(ctx, chatID, username, inviteCode string)`** — создание free/paid подписки. Параметр `inviteCode` пробрасывается в `db.CreateSubscription` для резолва referrer.
- **`CreateTrial(ctx, chatID, username, inviteCode string)`** — итерация по `trialSources`, **errors.Join** агрегация, partial success → Warn, all-fail → Error + return.
- **`BindTrial(ctx, subID, telegramID, username, inviteCode string)`** — обновляет ВСЕ trial-источники (`xui.UpdateClient`), в `db.BindTrialSubscription` — defensive revoke всех active subs для telegram_id (защита double-active race).
- **`ReconcileOrphanedClients(ctx)`** — проверяет ВСЕ источники, удаляет только если клиент не найден НИГДЕ.

### Database (`internal/database/database.go`)
- **`CreateSubscription(ctx, sub, inviteCode string)`** — атомарная транзакция: revoke всех active subs для telegram_id + resolve invite → заполнение `sub.InviteCode` и `sub.ReferredBy` + insert. Если inviteCode не найден — не фатально.
- **`BindTrialSubscription(ctx, sub, telegramID, username)`** — UPDATE trial-row WHERE telegram_id=0 AND plan_id=trial → revoke других active subs для этого telegram_id в той же транзакции. Возвращает `ErrAlreadyActivated` если RowsAffected=0.
- **`CleanupExpiredTrials(ctx)`** — DELETE WHERE expiry_time < now() RETURNING subscription_id (SQLite ≥ 3.35).
- **Sentinel errors**: `xui.ErrClientNotFound` (для `errors.Is` в Reconcile).

### X-UI Client (`internal/xui/client.go`)
- **Multi-source**: `xuiClients map[uint]interfaces.XUIClient` (key = source ID).
- **Circuit breaker**: `internal/xui/breaker.go` — 5 failures → 30s open → half-open. Метрика `metrics.CircuitBreakerState`.
- **Retry**: `RetryWithBackoff` (3 retries, exponential + jitter). DNS errors fast-fail.
- **Auth**: Bearer token, без сессий, без singleflight (singleflight перенесён в `internal/web/singleflight.go`).
- См. `xui/auth-mechanism` + `xui/client-crud`.

### Bot (`internal/bot/`)
- **Referral cache** (`referral_cache.go`): in-memory map[tgID]int, `Increment`/`Decrement`/`Sync` (1h interval из БД).
- **Rate limiter** (`internal/ratelimiter/`): per-user token bucket.
- **Singleflight** (`internal/web/singleflight.go`): дедупликация одновременных DB queries.

## Race-safe patterns

### Trial bind (защита double-active)
1. Web-биндинг: `UPDATE trial-row WHERE telegram_id=0 AND plan_id=trial` (атомарно через `RowsAffected`).
2. **Defensive revoke**: после успешного UPDATE — revoke всех `active` subs для этого telegram_id (кроме только что забинденной).
3. Если параллельный `service.Create` для того же telegram_id выиграет гонку: он ревоукит trial-бинденную sub ДО bind. BindTrial получит `ErrAlreadyActivated` (RowsAffected=0, т.к. telegram_id уже не 0).

### Create vs BindTrial TOCTOU
- Узкое окно: `BindTrialSubscription find → CleanupExpiredTrials delete`. Cleanup может удалить trial-row, который Bind собирается обновить. Bind получит `ErrAlreadyActivated`. Юзер может запросить trial снова. **Не критично** (узкое окно).

## Технические ограничения

### 🟡 SQLite scale
- Окей до 500-1000 пользователей. `database is locked` при >500 concurrent writes.
- Миграция на PostgreSQL: 2-4 часа (заменить драйвер, проверить миграции).

### 🟡 Multi-server config
- Сейчас одна подписка = один источник. Multi-server VLESS-конфиг (массив серверов) — отложено (см. `roadmap`).

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
- `config.Source` (URL, token, inbound_id, Active, Trial) → `map[uint]XUIClient` + `[]database.Source`.
- Trial — на всех trial-источниках. BindTrial — первый успешный. Reconcile — все.

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
