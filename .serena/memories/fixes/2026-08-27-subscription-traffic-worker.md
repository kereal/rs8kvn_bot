# Subscription Traffic Worker (2026-08-27)

## Что и зачем
Пользователям на **бесплатных тарифах с лимитом трафика** (`plans.traffic_limit > 0`) нужны уведомления о приближении/исчерпании лимита и авто-включение после сброса счётчика. Premium — безлимит, не участвует. Храним не историю трафика, а только битmask отправленных уведомлений (по принципу «проще и надёжнее»).

## Механика
- **`SubscriptionTrafficWorker`** (`internal/scheduler/subscription_traffic_worker.go`): тик **60 мин**, выборка `GetActiveSubscriptionsWithTrafficLimit`, на каждую — `SubscriptionService.ProcessTrafficNotifications`. Best-effort: `Error` при сбое выборки (прерывает проход), `Warn` на подписку (продолжает). Trial-таргеты с `telegram_id <= 0` пропускаются.
- **`SubscriptionService.ProcessTrafficNotifications`** (`internal/service/subscription_traffic_notifications.go`): опрашивает живые 3x-ui ноды (`GetClientTraffic` через node bindings, зеркалит `GetWithTraffic`), суммирует up+down и ветвится:
  1. **90%** — `used ≥ 0.9·limit`, клиент включён → текст «Осталось меньше 10% трафика. Ты использовал почти весь бесплатный лимит (90%).» + кнопка `buy_premium_list`.
  2. **Исчерпан** — `used ≥ limit` → текст «🚫 Доступ приостановлен. Ты использовал весь бесплатный лимит (100%), и твой VPN отключён.»; клиент **НЕ** включается (CTA — купить Premium).
  3. **Сброс + включён** — `used < limit`, клиент выключен → воркер **включает** (`UpdateClient(Enable=true)`) и шлёт «✅ Твой трафик сброшен — ты снова в сети!» **без** кнопки Premium.
  Ниже 90% — освобождает бит 90%, чтобы предупреждение сработало снова при повторном достижении.
- **Проценты фиксированы смысловыми** (90/100) — это пороги срабатывания, а не текущее заполнение (НЕ выводим 95%/101%).
- **Idempotency**: `subscriptions.traffic_reminders_sent` bitmask (migration **038**): `TrafficBit90`=1, `TrafficBitExhausted`=2, `TrafficBitReset`=4. Атомарные `ClaimTrafficReminder`/`ReleaseTrafficReminder`; при сбое Telegram — claim освобождается для повтора.
- **xui**: `ClientRequest.Enable *bool` с `Enable: boolPtr(true)` в `doUpdateClient`; `ClientTraffic.Enable` из `GetClientTraffic`.
- **Метрики**: `traffic_notifications_total{kind,result}` (kind `ninety`/`exhausted`/`reset`), `traffic_notification_runs_total`.
- **Lifecycle**: зарегистрирован в `cmd/bot/main.go` (`startBackgroundWorkers`, `recoverAndReport("Subscription traffic worker")`), `wg.Add(...)`.

## Кнопка Premium
Используется **готовая статичная кнопка** «💎 Перейти на Premium» с колбэком `buy_premium_list` (уже обрабатывается ботом и открывает меню выбора срока 30/60 дней). Никакой динамики с продуктами в сервисе уведомлений не нужно — кнопка одна, стабильная.

## Файлы
- `internal/database/migrations/038_add_traffic_reminders_sent.{up,down}.sql`
- `internal/database/subscription_traffic.go` (+ тесты)
- `internal/xui/client.go` (Enable) + `internal/xui/client_enable_test.go`
- `internal/service/subscription_traffic_notifications.go` (+ тесты)
- `internal/scheduler/subscription_traffic_worker.go` (+ тесты)
- `internal/interfaces`, `internal/metrics`, `cmd/bot/main.go`

## Тесты
- БД: claim/release/not-found/выборка с лимитом (active/revoked/unlimited).
- xui: Enable true/false в UpdateClient, декодирование Enable.
- service: 90%, исчерпан (не ре-enable), reset+reenable (с кнопкой отсутствует Premium), below-90 noop, nil/sub noop; helper `hasPremiumButton`.
- scheduler: итерация + skip trial, continue-on-error, stop на repo error.
- Миграционные тесты 032/033 переведены с позиционных `Steps(-N)` на абсолютные `Migrate(31/32/33)` из-за отсутствия миграции 037.

## Логирование и обработка ошибок (уровни, чтобы не шуметь)
- **Per-node `GetClientTraffic` failure** → `logger.Debug` (осознанный выбор «не шуметь»: при недоступной ноде у сотен подписок не заваливаем логами; повтор через час).
- **Per-node re-enable failure / release-ошибки битов / fallback-ошибка `GetBySubscriptionID`** → `logger.Warn` (важное, но не фатальное; обработка продолжается).
- **DB-сбой выборки в воркере** (`GetActiveSubscriptionsWithTrafficLimit`) → `logger.Error` (прерывает проход).
- **Per-subscription сбой в воркере** → `logger.Warn` (продолжает скан).
- **`Info` — только фактические действия**: успешная отправка (`traffic notification sent`, kind+subscription_id+telegram_id) и успешное включение клиента (`traffic client re-enabled`, subscription_id+node_id). Этого нет лишнего — это traceability.
- **`sendOnce`**: `!claimed` (уже отправлено) → тихий no-op без метрик; уfailure `textFn`/`Send` — `errors.Join` с ошибкой release, метрика `TrafficNotificationsTotal{kind,error}`, claim освобождается для повтора.
- Метрики остаются основным сигналом: `TrafficNotificationsTotal{kind,success|error}` инкрементится только на реально предпринятую попытку, не на no-op.

## Ограничение (согласовано с пользователем)
`totalGB` на панели оставлен как есть — панель сама выключает клиентов по лимиту («Remove Inbound User ... due to traffic limit»). Воркер работает для клиентов, которых панель **выключила, но оставила в списке** (`Enable=false`, попадают в `GetClientTraffic`). Если панель **полностью удалит** клиента из core — `GetClientTraffic` вернёт not-found, такой кейс пропускается и логируется. Не удалять клиентов ботом; legacy-коллапс удалён, физическое удаление — только админ `/del` и expired trials.