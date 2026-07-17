# Аудит метрик subserver

Дата: 2026-07-03 (обновлено 2026-07-17)
Статус: актуализировано

## Что есть в `internal/metrics/metrics.go`

### Живые метрики (инкрементируются в коде)

| Метрика | Тип | Где используется |
|---|---|---|
| `http_requests_total{method,path,status}` | counter | `web/web.go:135` — `InstrumentHTTP` middleware |
| `http_request_duration_seconds{method,path}` | histogram | там же |
| `http_requests_in_flight{method,path}` | gauge | там же |
| `bot_updates_total{command,result}` | counter | `bot/handler.go:647,654,656` |
| `bot_update_errors_total{type}` | counter | `bot/handler.go:653` |
| `bot_update_duration_seconds` | histogram | `bot/handler.go:622` |
| `cache_hits_total{cache}` | counter | `subserver/cache.go:61`, `bot/cache.go:81` |
| `cache_misses_total{cache}` | counter | `subserver/cache.go:56`, `bot/cache.go:54,71` |
| `circuit_breaker_state{target}` | gauge | `xui/breaker.go:49,85,113,129` |
| `active_subscriptions` | gauge | `service/subscription.go` — `.Set()` после мутаций |
| `subscription_creates_total` | counter | `service/subscription.go` — `Create`, `GetOrCreateSubscription` |
| `subscription_renewals_total` | counter | `service/subscription.go` — `RenewSubscription` |
| `subscription_sync_total` | counter | `scheduler/subscription_sync_worker.go:44` |
| `subscription_sync_duration_seconds` | histogram | `scheduler/subscription_sync_worker.go:44` |
| `subscription_expire_total` | counter | `scheduler/subscription_expire_worker.go:45` |
| `subscription_expire_duration_seconds` | histogram | `scheduler/subscription_expire_worker.go:45` |
| `reconcile_orphaned_duration_seconds` | histogram | `service/subscription.go:480` |
| `bot_orphaned_clients_removed_total` | counter | `service/subscription.go:575` |
| `db_queries_total{operation,result}` | counter | `metrics/db.go` — GORM callbacks |
| `db_query_duration_seconds{operation}` | histogram | `metrics/db.go` — GORM callbacks |
| `subserver_source_fetch_total{result,format}` | counter | `subscription_handler_split.go:225,242` |
| `subserver_source_fetch_duration_seconds{result}` | histogram | `subscription_handler_split.go:226,243` |
| `subserver_cache_invalidations_total{reason}` | counter | `subscription_handler_split.go:38,62` |
| `subserver_no_items_total` | counter | `subscription_handler_split.go:394` |
| `subserver_cache_hit_duration_seconds` | histogram | `subscription_handler.go:29` |
| `subserver_cache_miss_duration_seconds` | histogram | `subscription_handler.go:34` |

### Мёртвые метрики (объявлены, нигде не инкрементируются)

| Метрика | Тип | Проблема |
|---|---|---|
| `xui_requests_total{operation,result}` | counter | Объявлена, но `fetch.go` не инструментирован. |
| `xui_request_duration_seconds{operation}` | histogram | Аналогично. |
| `trial_conversions_total` | counter | Удалена — нет явного trial→paid флоу. |

### Удалённые метрики

- `subserver_partial_sources_total{sub_id}` — deprecated, опасная cardinality, заменена на `subserver_source_fetch_total{result="error"}`.
- `trial_conversions_total` — удалена, нет явного trial→paid флоу.

## Ситуация по subserver конкретно

```
subserver/cache.go ──── cache_hits_total{cache="subserver"}      живая
                        cache_misses_total{cache="subserver"}    живая

subscription_handler.go ─ cache_hit/miss duration_seconds       живая
subscription_handler_split.go ─ cache_invalidations_total{reason}  живая
                                source_fetch_total{result,format}  живая
                                source_fetch_duration_seconds{result} живая
                                no_items_total                    живая
```

## Тесты

`internal/metrics/metrics_test.go` — проверяет инициализацию всех метрик и доступность на `/metrics`.
`internal/metrics/db_test.go` — smoke-test GORM callbacks для create/query/update/delete.
