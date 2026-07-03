# Аудит метрик subserver

Дата: 2026-07-03
Статус: отчёт создан, действия по очистке отложены

## Что есть в `internal/metrics/metrics.go`

Всего объявлено 17 метрик. Разбивка по фактическому использованию:

### Живые метрики (инкрементируются в коде)

| Метрика | Тип | Где используется |
|---|---|---|
| `http_requests_total{method,path,status}` | counter | `web/web.go:135` — `InstrumentHTTP` middleware (покрывает `/sub/:id`, `/i/:code`, `/metrics`) |
| `http_request_duration_seconds{method,path}` | histogram | там же |
| `http_requests_in_flight{method,path}` | gauge | там же |
| `bot_updates_total{command,result}` | counter | `bot/handler.go:647,654,656` |
| `bot_update_errors_total{type}` | counter | `bot/handler.go:653` |
| `bot_update_duration_seconds` | histogram | `bot/handler.go:622` |
| `cache_hits_total{cache}` | counter | `subserver/cache.go:61` (`subserver`), `bot/cache.go:81` (`subscription`) |
| `cache_misses_total{cache}` | counter | `subserver/cache.go:56`, `bot/cache.go:54,71` |
| `circuit_breaker_state{target}` | gauge | `xui/breaker.go:49,85,113,129` (`xui`) |
| `bot_orphaned_clients_removed_total` | counter | `service/subscription.go:732` |
| `subserver_source_fetch_total{result,format}` | counter | `subscription_handler.go:128,146` (добавлено 2026-07-03) |
| `subserver_source_fetch_duration_seconds{result}` | histogram | `subscription_handler.go:129,147` (добавлено 2026-07-03) |
| `subserver_cache_invalidations_total{reason}` | counter | `subscription_handler.go:43,46,59` (добавлено 2026-07-03) |
| `subserver_no_items_total` | counter | `subscription_handler.go:244` (добавлено 2026-07-03) |

### Мёртвые метрики (объявлены, нигде не инкрементируются)

| Метрика | Тип | Проблема |
|---|---|---|
| `xui_requests_total{operation,result}` | counter | Объявлена для подсчёта запросов к 3x-ui панели, но ни `xui/`, ни `subserver/proxy.go` её не инкрементируют. `FetchFromXUI` делает прямые `http.Client.Do` без метрик. |
| `xui_request_duration_seconds{operation}` | histogram | Аналогично — таймер не навешен. |
| `db_queries_total{operation,result}` | counter | Объявлена для инструментирования БД-слоя, но `database/` не импортирует `metrics` вообще. |
| `db_query_duration_seconds{operation}` | histogram | То же — не используется. |
| `active_subscriptions` | gauge | Объявлена, но `.Set()` нигде не вызывается. Есть `CountActiveSubscriptions` в БД, но результат не отправляется в gauge. |
| `trial_conversions_total` | counter | Не инкрементируется. Логика trial->paid есть в `service/subscription.go`, но метрика не вызывается. |
| `subserver_partial_sources_total{sub_id}` | counter | Объявлена с лейблом `sub_id` — высокая кардинальность (каждый subID = новый ряд временного ряда). Не инкрементируется. Двойная проблема: мёртвая + опасная если оживает. |

Итого: 7 из 17 метрик — мёртвые (~41% объявленных метрик).

## Ситуация по subserver конкретно

```
subserver/cache.go ──── cache_hits_total{cache="subserver"}      живая
                        cache_misses_total{cache="subserver"}    живая

subscription_handler.go ─ cache_invalidations_total{reason}      живая (новая)
                          source_fetch_total{result,format}      живая (новая)
                          source_fetch_duration_seconds{result}  живая (новая)
                          no_items_total                          живая (новая)
                          partial_sources_total{sub_id}          мёртвая + опасная

proxy.go (FetchFromXUI) ─ xui_requests_total{operation,result}  мёртвая
                          xui_request_duration_seconds{op}       мёртвая
```

До изменений 2026-07-03 subserver имел наблюдаемость только на уровне HTTP-middleware
и cache hit/miss. Никаких метрик на:
- fetch от upstream (была чёрная дыра — только `logger.Error`)
- инвалидации кэша (только `logger.Warn`)
- пустые ответы (только `ErrNoSubscriptionItems` без метрики)

После изменений — 4 новые метрики закрывают основные слепые зоны.
Но осталась мёртвая `subserver_partial_sources_total{sub_id}`.

## Рекомендации (отложены на потом)

### 1. Удалить мёртвые метрики (чистка)

Удалить из `metrics.go`:
- `subserver_partial_sources_total` — мёртвая + `sub_id` как лейбл = risk
  неконтролируемого роста временных рядов. Частичные сбои источников теперь видны
  через `subserver_source_fetch_total{result="error"}`.
- `xui_requests_total`, `xui_request_duration_seconds` — мёртвые. Upstream-fetch
  теперь покрыт `subserver_source_fetch_total` / `subserver_source_fetch_duration_seconds`.
- `db_queries_total`, `db_query_duration_seconds` — мёртвые, БД-слой не
  инструментирован и это отдельная задача.
- `active_subscriptions` — мёртвый gauge. Либо удалить, либо ожить (вызывать
  `.Set()` из scheduler/worker).
- `trial_conversions_total` — мёртвый. Либо удалить, либо ожить в
  `service/subscription.go` в месте апгрейда trial->paid.

### 2. Оживить (опционально, если нужны)

- `active_subscriptions`: добавить в `SubscriptionSyncWorker` или scheduler вызов
  `metrics.ActiveSubscriptions.Set(float64(count))` раз в N минут.
- `trial_conversions_total`: инкремент в `service/subscription.go` при успешном
  `BindTrial`->paid переходе.
