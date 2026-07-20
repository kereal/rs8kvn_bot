# Метрики Prometheus — текущее состояние (2026-07-17)

## Обновлённые/оживлённые метрики

- `active_subscriptions` — gauge, теперь обновляется через `SubscriptionService.RefreshActiveSubscriptionsMetric()` после каждой мутации (create/delete/renew/reconcile) + стартовая инициализация в `cmd/bot/main.go`.
- `db_queries_total{operation,result}` и `db_query_duration_seconds{operation}` — добавлены GORM callbacks в `internal/metrics/db.go`, регистрируются в `initDatabase`.
- `subscription_creates_total` — инкрементируется в `Create` и `GetOrCreateSubscription`.
- `subscription_renewals_total` — инкрементируется в `RenewSubscription`.
- `subscription_sync_total` + `subscription_sync_duration_seconds` — инкрементируются в `SubscriptionSyncWorker.process`.
- `subscription_expire_total` + `subscription_expire_duration_seconds` — инкрементируются в `SubscriptionExpireWorker.process`.
- `reconcile_orphaned_duration_seconds` — инкрементируется в `ReconcileOrphanedClients`.
- `subserver_cache_hit_duration_seconds` + `subserver_cache_miss_duration_seconds` — замеры в `HandleSubscription`.

## Удалённые метрики

- `subserver_partial_sources_total{sub_id}` — deprecated, опасная cardinality.
- `trial_conversions_total` — удалена, нет явного trial→paid флоу.

## Оставлены dead на будущее

- `xui_requests_total{operation,result}` и `xui_request_duration_seconds{operation}` — объявлены, но не используются. Оставлены для будущего инструментирования `fetch.go`.

## Тесты

- `internal/metrics/metrics_test.go` — проверяет инициализацию всех метрик и доступность на `/metrics`.
- `internal/metrics/db_test.go` — smoke-test GORM callbacks для CRUD операций.
- Все метрики покрыты базовыми проверками, сборка и тесты зелёные.

## Документация

Актуальный отчёт: `doc/subserver_metrics_audit.md`.
