# rs8kvn_bot — Telegram Bot для раздачи VLESS-подписок

## Назначение
Telegram-бот для продажи и управления VLESS+Reality+Vision подписками через панели 3x-ui.
Production-grade: миграции, мониторинг, rate-limiting, circuit breaker, graceful shutdown.

## Текущая версия
**v2.4.0** — мульти-источник 3x-ui (sources/plans/plan_sources), план-based подписки, миграции 000-012.

## Ключевые фичи
- Запрос подписки по требованию, QR-код
- Invite/trial landing с one-click биндингом
- Реферальная система: in-memory cache + периодический sync
- Планы (trial, free, paid) с M:N связью к источникам через `plan_sources`
- Мульти-источник 3x-ui: trial-подписки создаются на всех trial-источниках, BindTrial — первый успешный, Reconcile — все источники
- Авто-продление на 30-й день (через `SubscriptionResetDay` в x-ui)
- Админ-уведомления, heartbeat, health endpoints (`/healthz`, `/readyz`)
- Ротация логов (zap), ежедневные бэкапы БД
- Sentry, rate-limiting per-user, circuit breaker для x-ui
- O(1) LRU кэш подписок (RLock для concurrent reads)
- Subscription status check в `/sub/{subID}` — revoked/expired → 404
- Subscription expiration хранится в БД на момент Create (не "—")

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
internal/database/           — GORM-модели, миграции 000-011, transactions
internal/service/            — SubscriptionService (Create, BindTrial, CreateTrial, ReconcileOrphanedClients)
internal/xui/                — 3x-ui HTTP-клиент + circuit breaker, multi-source map
internal/interfaces/         — контракты (XUIClient, SubscriptionDatabase, SubscriptionService)
internal/testutil/           — моки (MockDatabaseService, MockXUIClient, MockBotAPI)
internal/utils/              — time, UUID, QR
internal/config/             — загрузка, валидация
internal/logger/             — zap setup
internal/heartbeat/          — мониторинг
internal/backup/             — ежедневные бэкапы с WAL checkpoint
internal/scheduler/          — backup + trial cleanup
internal/ratelimiter/        — per-user token bucket
internal/web/                — /healthz, /readyz, /i/{code}, /sub/{subID} + singleflight
internal/subproxy/           — кэш + merge подписок
internal/metrics/            — Prometheus (обёрнутый zap-логом, не реальный Prometheus)
```

## Bootstrap для AI-агента
При старте сессии **обязательно**:
1. `activate_project("rs8kvn_bot")`
2. Прочитать памяти: `project_overview` (этот), `git-workflow`, `architecture`, `code_style`
3. При работе с x-ui API — прочитать `xui/auth-mechanism` + `xui/client-crud`
4. При работе с trial/referral flows — прочитать `fixes/2026-06-03-*`
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
