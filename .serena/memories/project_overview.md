# rs8kvn_bot — Telegram Bot для раздачи VLESS-подписок

## Назначение
Telegram-бот для продажи и управления VLESS+Reality+Vision подписками через панели 3x-ui.
Production-grade: миграции, мониторинг, rate-limiting, circuit breaker, graceful shutdown.

## Subscription expiry reminders
- Окна: 3 дня, 1 день, 3 часа до истечения подписки (`internal/service/subscription_reminders.go`)
- Фоновая задача: `SubscriptionReminderWorker` запускается раз в 30 минут, сканирует активные paid-подписки в окне ±30 минут от целевого срока.
- Напоминание отправляется ровно один раз на окно: `reminders_sent` bitmask в `subscriptions` + атомарный `ClaimReminder`/`ReleaseReminder` по `expires_at`.
- При `RenewSubscription` битмасп сбрасывается в 0, чтобы новое поколение подписки получило напоминания заново.
- Исключаются free и trial планы (`GetSubscriptionsExpiringInRange`).
- Пушечные метрики: `SubscriptionRemindersTotal` (`name`, `status`), `SubscriptionReminderRunsTotal` (`internal/metrics/metrics.go`).
- Миграция 030: добавлено поле `subscriptions.reminders_sent`.
- Триггер жизненного цикла: `SubscriptionReminderWorker` запускается в `cmd/bot/lifecycle.go`.

## Subscription expiration
- Paid-подписки автоматически помечаются `expired`, если истёк `expires_at` и план не free/trial (`GetExpiredPaidSubscriptions`).
- Таймер истечения хранится в БД на момент Create.

## Типы подписок и фильтр планов
- Trial-подписки теперь **не** считаются paid при автоматическом истечении и не получают напоминания: `GetExpiredPaidSubscriptions` и `GetSubscriptionsExpiringInRange` исключают `plans.name = TrialPlanName`.

## Ключевые фичи
- Планы (trial/free/paid) без `duration`, без `price` (duration/price вынесены в products)
- `subscriptions.client_id` и `subscriptions.subscription_id` имеют `NOT NULL UNIQUE` enforcement через migration 023 и GORM-модель
- Мульти-источник 3x-ui: trial-подписки создаются на всех trial-нодах, BindTrial — первый успешный, Reconcile — все
- Таблица `subscription_nodes` — очередь реальной синхронизации подписки×нода (`active|pending_add|pending_remove|pending_update`)
- SyncService с state machine, retry logic (exponential backoff), per-subscription locking
- VPN Client abstraction (`internal/vpn`) — поддержка 3x-ui и proxman нод
- Авто-продление на 30-й день (через `SubscriptionResetDay` в x-ui)
- Реферальная система: in-memory cache + периодический sync
- Админ-уведомления, heartbeat, health endpoints (`/healthz`, `/readyz`)
- Hysteria2/Clash port-hopping нормализация: `firstPortFromPorts` берёт первый конкретный порт из `ports`/диапазона
- Ротация логов (zap), ежедневные бэкапы БД (имя `rs8kvn.db.backup.YYYYMMDD_HHMMSS`)
- Sentry, rate-limiting per-user, circuit breaker для x-ui
- O(1) LRU кэш подписок (RLock для concurrent reads)
- Subscription status check в `/sub/{subID}` — revoked/expired → 404
- Subscription expiration хранится в БД на момент Create

## Subserver share-link conversion
- Поддерживаемые схемы: `vmess://`, `trojan://`, `ss://`, `hysteria://`, `hysteria2://`, `hy2://`, `tuic://`
- ALPN list→comma string (Clash YAML list → v2rayN share-link param)
- Shadowsocks SIP002 plugin: `obfs`→`obfs-local` alias + `plugin-opts` serialisation
- VLESS xhttp/splithttp normalisation + `mode` param
- `security=tls` для Trojan/VLESS (3x-ui flat format)
- IPv6-safe addresses через `net.JoinHostPort`
- VLESS/Trojan/Hysteria/TUIC: TLS flag нормализуется так, чтобы `security=tls` не потеряться при `tls` nil
- VMess port as string для v2rayNG
- Clash Meta/V2rayN `packetEncoding` support через `setPacketEncoding` (`none`/`packet`/`xudp`)

## Access log
- Space-separated format, async writer с bounded queue (1024 records)
- Fields: timestamp, method, URI, status, success/total, client IP, hwid, os, ver, model, user-agent
- Quote-wrapping для значений с пробелами
- `statusRecorder` tracks per-request source success/total counts

## Broadcast (рассылка `/broadcast`)
- Поток: `/broadcast` → черновик (текст) → превью (MarkdownV2) → подтверждение inline-кнопками → массовая отправка батчами (100, concurrency 10) всем `telegram_id` из БД.
- Текст отправляется как **MarkdownV2**. Спецсимволы (`.`, `!`, `_`, `*`, `()` и т.д.) **авто-экранируются** в `utils.EscapeMarkdownV2`, форматирование сохраняется — ручное экранирование юзеру не нужно.
- Отчёт в конце разделяет счётчики: `Отправлено` / `Заблокировали бота` / `Ошибок` / `Всего`. Ошибки «bot was blocked by the user» / «user is deactivated» / «chat not found» считаются отдельно (`isUserBlockedError`), не смешиваясь с реальными сбоями.
- Таймаут рассылки 5 мин; при отмене/ошибке БД отправляется частичный отчёт.

## Стек
- **Go 1.25** (go.mod)
- **Bot**: telegram-bot-api/v5
- **DB**: SQLite + GORM + golang-migrate (embedded, migration 030)
- **Logging**: Zap (с ротацией)
- **Tests**: testify
- **QR**: piglig/go-qr
- **Errors**: getsentry/sentry-go

## Subserver
- Clash/Mihomo конвертер покрывает VMess, VLESS, Trojan, Hysteria2, TUIC, Shadowsocks SIP002.
- Транспортная нормализация сведена в `normaliseTransportNetwork` + `setPacketEncoding`.

## Scheduler
- Backup ежедневно, trial cleanup каждый час, sync workers фоном, reminders — каждые 30 минут (`SubscriptionReminderWorker`).

## Базовый worker set
- Backup ежедневно, trial cleanup каждый час, sync workers фоном, reminders — каждые 30 минут.

## Структура
```
cmd/bot/                     — точка входа, graceful shutdown, lifecycle workers
internal/bot/                — handlers, commands, callbacks, referral cache, keyboard/menu
internal/database/           — GORM-модели, миграции 000-030, транзакции
internal/service/            — SubscriptionService + SyncService + reminders
internal/vpn/                — VPN client abstraction (3x-ui, proxman, fetch)
internal/xui/                — 3x-ui HTTP-клиент + circuit breaker
internal/interfaces/         — контракты (XUIClient, SubscriptionRepository, reminder repr.)
internal/testutil/           — моки (db slice fakes, testutil helpers)
internal/utils/              — time, UUID, QR, Markdown EscapeMarkdownV2/EscapeMarkdown
internal/config/             — загрузка, валидация
internal/logger/             — zap setup
internal/heartbeat/          — мониторинг
internal/backup/             — ежедневные бэкапы БД с WAL checkpoint
internal/scheduler/          — backup + trial cleanup + subscription sync + expiry reminders
internal/ratelimiter/        — per-user token bucket
internal/web/                — /healthz, /readyz, /i/{code}, /sub/{subID}, /metrics, /payment/callback, ... + singleflight
internal/subserver/           — кэш + merge подписок; Clash/Mihomo нормализация share-ссылок; optional access log
internal/metrics/            — Prometheus/обёртка; напоминания имеют выделенные счётчики
```

## Bootstrap для AI-агента
При старте сессии **обязательно**:
1. `activate_project("rs8kvn_bot")`
2. Прочитать памяти: `project_overview` (этот), `git-workflow`, `architecture`, `code_style`
3. При работе с x-ui API — прочитать `xui/auth-mechanism` + `xui/client-crud`
4. При работе с reminders/subscriptions/subscription-nodes — `architecture`, `subscription-nodes/state-machine`, `fixes/2026-07-...`
5. **Отвечать на русском** (AGENTS.md)
6. **Не удалять** legacy-код без явного запроса

---
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
