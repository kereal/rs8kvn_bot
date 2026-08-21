# Аудит метрик subserver

Статус: проверено по текущему коду

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
| `xui_requests_total{operation,result}` | counter | `xui/client.go` — live XUI request instrumentation |
| `xui_request_duration_seconds{operation}` | histogram | `xui/client.go` — live XUI request timing |
| `circuit_breaker_state{target}` | gauge | `xui/breaker.go` — updated by the tested breaker; no live production caller |
| `active_subscriptions` | gauge | `service/subscription.go` — `.Set()` после мутаций |
| `subscription_creates_total` | counter | `service/subscription.go` — `Create`, `GetOrCreateSubscription` |
| `subscription_renewals_total` | counter | `service/subscription.go` — `RenewSubscription` |
| `subscription_sync_total` | counter | `scheduler/subscription_sync_worker.go:44` |
| `subscription_sync_duration_seconds` | histogram | `scheduler/subscription_sync_worker.go:44` |
| `subscription_expire_total` | counter | `scheduler/subscription_expire_worker.go:45` |
| `subscription_expire_duration_seconds` | histogram | `scheduler/subscription_expire_worker.go:45` |
| `reconcile_orphaned_duration_seconds` | histogram | `service/subscription.go:480` |
| `bot_orphaned_clients_revoked_total` | counter | `service/subscription.go` |
| `db_queries_total{operation,result}` | counter | `metrics/db.go` — GORM callbacks |
| `db_query_duration_seconds{operation}` | histogram | `metrics/db.go` — GORM callbacks |
| `subserver_source_fetch_total{result,format}` | counter | `subscription_handler_split.go:225,242` |
| `subserver_source_fetch_duration_seconds{result}` | histogram | `subscription_handler_split.go:226,243` |
| `subserver_cache_invalidations_total{reason}` | counter | `subscription_handler_split.go:38,62` |
| `subserver_no_items_total` | counter | `subscription_handler_split.go:394` |
| `subserver_cache_hit_duration_seconds` | histogram | `subscription_handler.go:29` |
| `subserver_cache_miss_duration_seconds` | histogram | `subscription_handler.go:34` |

### Удалённые или ранее ошибочно классифицированные метрики

| Метрика | Статус |
|---|---|
| `trial_conversions_total` | Удалена: явного trial→paid флоу нет. |
| `subserver_partial_sources_total{sub_id}` | Удалена из-за опасной cardinality. |

XUI-метрики не являются мёртвыми: `xui_requests_total{operation,result}` и `xui_request_duration_seconds{operation}` инкрементируются в `internal/xui/client.go`.

## Ситуация по subserver конкретно

```text
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
