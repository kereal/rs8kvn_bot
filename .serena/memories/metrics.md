# Метрики Prometheus — текущее состояние

## Обновлённые/оживлённые метрики

- `active_subscriptions` — gauge, теперь обновляется через `SubscriptionService.RefreshActiveSubscriptionsMetric()` после каждой мутации (create/delete/renew/reconcile) + стартовая инициализация в `cmd/bot/main.go`.
- `db_queries_total{operation,result}` и `db_query_duration_seconds{operation}` — добавлены GORM callbacks в `internal/metrics/db.go`, регистрируются в `initDatabase`.
- `subscription_creates_total` — инкрементируется в `Create` и `GetOrCreateSubscription`.
- `subscription_renewals_total` — инкрементируется в `RenewSubscription`.
- `subscription_sync_total` + `subscription_sync_duration_seconds` — инкрементируются в `SubscriptionSyncWorker.process`.
- `subscription_expire_total` + `subscription_expire_duration_seconds` — инкрементируются в `SubscriptionExpireWorker.process`.
- `reconcile_orphaned_duration_seconds` — наблюдается в `ReconcileOrphanedClients`.
- `subserver_cache_hit_duration_seconds` + `subserver_cache_miss_duration_seconds` — замеры в `HandleSubscription`.
- `payment_operations_total{operation,result}` + `payment_operation_duration_seconds{operation}` — счётчики/гистограмма операций `OrderService` (request/confirm/cancel × success/error).
- `payment_amounts_cents_total{operation,currency}` — суммы в копейках по денежным переходам: `confirmed` (успешный CONFIRMED, только Activated) и `chargeback` (CHARGEBACKED на оплаченном заказе). Валюта нормализуется (upper), невалидные/неположительные суммы игнорируются.
- `payment_issues_total{event}` — операционные проблемы платежей по стабильному имени события (`NotifyPaymentIssue`); те же события уходят админу в Telegram.

## Удалённые метрики

- `subserver_partial_sources_total{sub_id}` — deprecated, опасная cardinality.
- `trial_conversions_total` — удалена, нет явного trial→paid флоу.

## Живые метрики XUI

- `xui_requests_total{operation,result}` — счётчик XUI-запросов, инкрементируется в `internal/xui/client.go`.
- `xui_request_duration_seconds{operation}` — длительность XUI-запросов, инкрементируется в `internal/xui/client.go`.

## Важно

- `bot_orphaned_clients_revoked_total` считает отозванные orphan subscriptions; старого имени `bot_orphaned_clients_removed_total` больше нет.

## Тесты

- `internal/metrics/metrics_test.go` — проверяет инициализацию всех метрик и доступность на `/metrics`.
- `internal/metrics/db_test.go` — smoke-test GORM callbacks для CRUD операций.
- Метрики имеют базовые smoke-проверки; актуальные имена следует сверять с `internal/metrics/metrics.go`.

## Документация

Актуальный отчёт: `doc/subserver_metrics_audit.md`.
