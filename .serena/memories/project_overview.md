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
- Триггер жизненного цикла: `SubscriptionReminderWorker` запускается в `cmd/bot/main.go` (startBackgroundWorkers).

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
- Админский флоу (сессия с TTL 15 мин): `/broadcast` → **название** → **текст** (MarkdownV2, спецсимволы авто-экранируются `utils.EscapeMarkdownV2`) → превью → **фильтры** (`bfilter_*`: plan paid/free, status active/all/revoked, дата регистрации 90/180/365, inactive 0/30/90 дн., ever_paid) → подтверждение (`broadcast_confirm` → счётчик получателей → `broadcast_final_confirm`).
- **Durable BroadcastWorker** (`internal/bot/broadcast_worker.go`): тик 15 с, таймаут кампании 5 мин, батчи по 100 (concurrency 10), независим от update-loop, переживает рестарт процесса (кампании `scheduled|running` резюмируются через `retry_at`).
- Аудитория снапшотится в JSON **`recipients_state`** той же строки `broadcasts` (миграция 036): snapshot + per-recipient `pending|sending|sent|blocked|unreachable|failed`, lease 2 мин, восстановление после краха (`RecoverStaleBroadcastRecipients`). Строка `broadcasts`: `filters` (JSON), статус (`scheduled|running|completed|failed|canceled`, CHECK-constraint), `planned_at`/`started_at`/`finished_at`, счётчики `recipients_total`/`sent_count`/`blocked_count`/`unreachable_count`/`failed_count`, `delivery_report` (JSON: `delivered`/`blocked`/`unreachable` — списки telegram_id, `errors` — `{telegram_id, error}`), `last_error`/`retry_at`/`retry_count`.
- **Ретраи**: per-message 2 попытки (300/600 мс); `blocked` — только явная блокировка бота («bot was blocked by the user»), deactivated/недоступный/удалённый чат (вкл. «chat not found») → `unreachable`, не ретраются. Инфраструктурные/персистентные сбои кампании → `retry_at` с exponential backoff (5 с → 15 мин), статус остаётся `running`.
- Управление: кнопки «📋 Детали рассылки» (`broadcast_details_<id>`), «⏹ Отменить» (`broadcast_cancel_<id>`), «🔁 Повторить ошибки» (`broadcast_retry_<id>`) в карточке; история — `/broadcasts` (10 последних).
- Отчёт админу: счётчики `Отправлено/Заблокировали/Недоступны/Ошибок`; полные списки ID остаются в `delivery_report`. `UpdateBroadcast` обновляет только изменяемые поля, карточку не затирает.
- **Логирование**: `Info` — создание/старт/завершение/отмена/retry кампании и черновика, плановый resume после таймаута (`DeadlineExceeded`/`errBroadcastIncomplete`); `Warn` — фоновые сбои (загрузка runnable, launch failed, markCampaignFailed, report load); `Error` — паника получателя, сбой scheduleRetry/создания, ошибки cancel/retry из callback.

## Последние изменения (2026-08-26, ветка feat/broadcast-campaigns)
- **ResetTraffic при смене тарифа**: `vpn.Client.ResetTraffic` (3x-ui POST `/panel/api/clients/resetTraffic/{email}`; proxman/fetch — no-op). Вызывается в `processPendingUpdate` после update, **best-effort** (Warn при ошибке, не блокирует переход в `active`).
- **CleanupExpiredTrials двухфазный**: `ClaimExpiredTrials` (атомарный claim) → deprovision внешних клиентов → `DeleteClaimedTrial` только после успеха; сбой оставляет строку для ретрая, ошибки агрегируются (`errors.Join`).
- **BindTrial**: панель обновляется ДО DB-bind; если DB-bind проиграл гонку и строка всё ещё не привязана (`telegram_id < 0`) — панель откатывается к анонимной `trial_`-идентичности; при неопределённости (check error) rollback не делается.
- **ReconcileOrphanedClients**: активная non-trial подписка без node bindings — пересоздаётся `pending_add`-очередь (`ensureSubscriptionNodes`), а не ревокация.
- **Subserver**: кэш-ревалидация **fail-closed** (Error + 5xx вместо stale после revoke/chargeback); лимит тела 2 MiB (`config.MaxResponseSize`); `analyticsMu` сериализует `UpdateDevices`/`UpdateIPs`; base64-агрегация поддерживает RawStdEncoding (без padding) и разбивает по строкам.
- **Web**: check-and-record trial rate-limit под `trialRateMu`; `SecurityHeadersMiddleware` (nosniff/DENY/no-referrer/Permissions-Policy).
- **Тесты**: `internal/bot/broadcast_admin_flow_test.go` (callback ID, cancel/retry, фильтры, форматтеры), `internal/xui/reset_traffic_test.go`, sync ResetTraffic best-effort, BindTrial rollback, CleanupExpiredTrials deprovision-failure, ReconcileOrphaned queue recovery.

## Стек
- **Go 1.25** (go.mod)
- **Bot**: telegram-bot-api/v5
- **DB**: SQLite + GORM + golang-migrate (embedded, current schema migrations are applied at startup)
- **Logging**: Zap (с ротацией)
- **Tests**: testify
- **QR**: piglig/go-qr
- **Errors**: getsentry/sentry-go

## Subserver
- Clash/Mihomo конвертер покрывает VMess, VLESS, Trojan, Hysteria2, TUIC, Shadowsocks SIP002.
- Транспортная нормализация сведена в `normaliseTransportNetwork` + `setPacketEncoding`.

## Scheduler
- Backup ежедневно, trial cleanup сразу при старте и затем каждые 3 часа, sync workers фоном, reminders — каждые 30 минут (`SubscriptionReminderWorker`).

## Базовый worker set
- Backup ежедневно, trial cleanup каждый час, sync workers фоном, reminders — каждые 30 минут.

## Структура
```text
cmd/bot/                     — точка входа, graceful shutdown, lifecycle workers
internal/bot/                — handlers, commands, callbacks, referral cache, keyboard/menu
internal/database/           — GORM-модели, embedded migrations, транзакции
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
