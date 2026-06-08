# rs8kvn_bot — Telegram Bot для раздачи VLESS-подписок

## Назначение
Telegram-бот для продажи и управления VLESS+Reality+Vision подписками через панели 3x-ui.
Production-grade: миграции, мониторинг, rate-limiting, circuit breaker, graceful shutdown.

## Текущая версия
**v2.3.0** — рефакторинг, разбивка database.go на 9 доменных файлов, вынос escapeMarkdown в internal/utils, удаление мёртвого кода подписки, удаление `duration` из plans (migration 019), products/orders, nodes/plan_nodes, subscription_nodes.

## Ключевые фичи
- Планы (trial/free/paid) без `duration`, без `price` (duration/price вынесены в products)
- Мульти-источник 3x-ui: trial-подписки создаются на всех trial-нодах, BindTrial — первый успешный, Reconcile — все
- Таблица `subscription_nodes` — очередь реальной синхронизации подписки×нода (`active|pending_add|pending_remove`)
- Авто-продление на 30-й день (через `SubscriptionResetDay` в x-ui)
- Реферальная система: in-memory cache + периодический sync
- Админ-уведомления, heartbeat, health endpoints (`/healthz`, `/readyz`)
- Ротация логов (zap), ежедневные бэкапы БД
- Sentry, rate-limiting per-user, circuit breaker для x-ui
- O(1) LRU кэш подписок (RLock для concurrent reads)
- Subscription status check в `/sub/{subID}` — revoked/expired → 404
- Subscription expiration хранится в БД на момент Create

## Стек
- **Go 1.25** (go.mod)
- **Bot**: telegram-bot-api/v5
- **DB**: SQLite + GORM + golang-migrate (embedded)
- **Logging**: Zap (с ротацией)
- **Tests**: testify
- **QR**: piglig/go-qr
- **Errors**: getsentry/sentry-go

## Структура
```
cmd/bot/                     — точка входа, graceful shutdown
internal/bot/                 — handlers, commands, callbacks, referral cache
internal/database/           — GORM-модели, миграции 000-019, транзакции (9 файлов: models, migrations, service, subscriptions, nodes, invites, trials, orders, products)
internal/service/            — SubscriptionService (Create, BindTrial, CreateTrial, ReconcileOrphanedClients)
internal/xui/                — 3x-ui HTTP-клиент + circuit breaker, multi-source map
internal/interfaces/         — контракты (XUIClient, SubscriptionDatabase, SubscriptionService)
internal/testutil/           — моки (MockDatabaseService, MockXUIClient, MockBotAPI)
internal/utils/              — time, UUID, QR, Markdown (EscapeMarkdown)
internal/config/             — загрузка, валидация
internal/logger/             — zap setup
internal/heartbeat/          — мониторинг
internal/backup/             — ежедневные бэкапы с WAL checkpoint
internal/scheduler/          — backup + trial cleanup
internal/ratelimiter/        — per-user token bucket
internal/web/                — /healthz, /readyz, /i/{code}, /sub/{subID} + singleflight
internal/subserver/           — кэш + merge подписок
internal/metrics/            — Prometheus (обёрнутый zap-логом, не реальный Prometheus)
```

## Bootstrap для AI-агента
При старте сессии **обязательно**:
1. `activate_project("rs8kvn_bot")`
2. Прочитать памяти: `project_overview` (этот), `git-workflow`, `architecture`, `code_style`
3. При работе с x-ui API — прочитать `xui/auth-mechanism` + `xui/client-crud`
4. При работе с trial/referral/subscription-nodes flows — прочитать `fixes/2026-06-03-*` + `subscription-nodes/state-machine`
5. **Отвечать на русском** (AGENTS.md)
6. **Не удалять** legacy-код без явного запроса (см. `roadmap`)

## Подробности
- Архитектура: см. `architecture`
- Стиль кода: см. `code_style`
- Git workflow: см. `git-workflow`
- Дорожная карта: см. `roadmap`
- Тесты: см. `test-info`
- Аудиты: см. `audit/*`
- Исторические фиксы: см. `fixes/*`
- x-ui протокол: см. `xui/*`
- Subscription Nodes: см. `subscription-nodes/state-machine` + `subscription-nodes/orders-table`
