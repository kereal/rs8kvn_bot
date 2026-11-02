# Метрики Prometheus — текущее состояние

## Полная ссылка

- **Инвентарь метрик и query patterns**: `doc/betterstack-metrics-reference.md`
- **Карта дашбордов, алертов и ограничений**: `doc/betterstack-dashboards.md`
- **Эксплуатация (что смотреть, когда сломалось)**: `doc/operations.md` §4.6

50 прикладных коллекторов в `internal/metrics` (+ стандартные `go_*`, `process_*`, `promhttp_*`).
Источник Better Stack: `hip-01 docker`, ID `2288221`, team `t513087`, хост `<prod-host>`.

## Жёсткие правила хранилища (проверено на живой базе 2026-11-02)

1. **Перцентили не считаются.** `histogramQuantile(p)`, `quantileMerge`, `quantilePrometheusHistogramArrayMerge(p)(bucket_quantiles)` возвращают верхнюю границу последнего бакета для любого квантиля (DB — всегда `0.5`, HTTP — `10`, XUI — `5`). Среднее считается верно: `sumMerge(bucket_sum) / sumMerge(bucket_count)` (DB ≈ 0.32 мс, `/sub` ≈ 17.7 мс).
2. **Counters читаются только через rate/дельту.** `avgMerge(rate_avg)` — «в секунду»; `maxMerge(value_max) - minMerge(value_min)` — «за период». `maxMerge(value_max)` в одиночку — накопленное с рестарта процесса.
3. **`derivativeMerge` в Better Stack отсутствует**, хотя упоминался в старых записях.
4. **Условный лейбл — только через `coalesce(nullIf(...))`.** `if(label('code') = '', label('status'), ...)` возвращает NULL; правильный вид: `coalesce(nullIf(label('code'), ''), label('status'))`. Алиас не должен совпадать с именем лейбла (иначе самоссылка → NULL).
5. **Имена хостовых/контейнерных метрик — без `node_*`/`container_*`**: `cpu_percentage{container_name}`, `memory_used_bytes`, `memory_limit_bytes`, `pids_count`, `network_in_bytes`, `block_in_bytes`, `filesystem_used_bytes{mountpoint}`, `load1/5/15`, `uptime`.
6. **Пустая серия ≠ ноль.** Серия `CounterVec` не появляется до первого события; поэтому воркеры и форматы выдачи публикуют нули в `init()`.

## Дашборды (8, все активны)

| Dashboard | ID | Содержимое |
|-----------|----|------------|
| 🎯 Overview | 1165127 | KPI, revenue period, Deployed build, HTTP 5xx |
| 🖥️ Server (host) | 1165134 | Load, CPU cores, memory, swap, OOM, `/` + иноды, ens3, uptime |
| 🐳 Containers & Runtime | 1165133 | По контейнерам CPU/память/лимит/PIDs/IO; Go runtime, GC, FDs, scrape |
| 🤖 Telegram Bot | 1165167 | Updates по командам, latency handler/API, HTTP |
| 🔌 API & VPN Panel | 1165131 | HTTP по path/status, 3x-ui, node_up, subserver (форматы, 404, кеш) |
| 📊 Subscriptions & Lifecycle | 1165129 | Гейджи, жизненный цикл, воркеры, биндинги, очистка |
| 💰 Payments & Revenue | 1165128 | RUB/USD/EUR, операции, chargebacks, issues, reconcile |
| 🗄️ Database & Cache | 1165132 | Запросы, latency (mean), ошибки по таблицам, пул, размер, кеш |

## Алерты (8, включены)

`VPN node panel is down` (2988725001, `node_up < 1`), `Parked subscription bindings` (2988725002), `Background worker restarted after panic` (2988725003), `Database query errors` (2988725004, >0.05/с), `HTTP 5xx responses` (2988725005), `SQLite file too large` (2988725006, >200 MiB), `Sync worker stopped` (2988725010, rate `subscription_sync_total{result="ok"}` < 0.0005 за 30 мин), `Payment integration issue` (2988725011).

Принцип: алерт только там, где без него о поломке узнаешь от пользователя.

## Ключевые точки

- `active_subscriptions`, `premium_subscriptions`, `free_subscriptions`, `trial_subscriptions` — gauges; пересчитываются каждым 5-минутным проходом `SubscriptionSyncWorker` (раньше только на мутациях → 0 после рестарта)
- `subscription_creates_total`, `subscription_reanimations_total`, `subscription_renewals_total` — события жизненного цикла
- `subscription_sync_total{result}` / `subscription_expire_total{result}` — `degraded` = прогон вернул агрегированную ошибку; `subscriptions_expired_total` — реально отключённые подписки
- `subscription_nodes{status}`, `subscription_nodes_parked` — очередь провижнинга; `node_up{node}` — доступность панели
- `worker_restarts_total{worker}` — перезапуск воркера после паники
- `app_info{version,revision}` — единственный способ увидеть устаревший деплой (прод 2026-11-02 крутил `9d04409` при свежем `dev`)
- `subserver_not_found_total`, `subserver_requests_total{format}` — мёртвые подписки (после массовых `/del` — норма) и микс форматов xray/singbox/clash
- `bot_orphaned_clients_revoked_total` — orphan-подписки отзываются, не удаляются

### Database & Cache
- `db_queries_total{operation,result,table}`, `db_query_duration_seconds{operation}` — GORM callbacks в `internal/metrics/db.go`; `table` из `Statement` и ограничена схемой
- `db_pool_waits_total`, `db_pool_wait_seconds_total` — **counters**, `CollectDBPoolMetrics` добавляет приращение `sql.DBStats` (повторный сбор не удваивает)
- `db_file_bytes`, `db_wal_bytes` — размер SQLite и WAL
- `cache_hits_total{cache}`, `cache_misses_total{cache}` — `subscription`, `subserver`

### Payments
- `payment_operations_total{operation,result}`, `payment_operation_duration_seconds{operation}`
- **`payment_amount_cents_total{operation,currency}`** — counter **суммы в центах**, не количество; `operation`: `confirmed`/`chargeback`; `currency`: RUB/USD/EUR
- `payment_issues_total{event}`, `payment_reconcile_total{result}`

### APIs
- HTTP: `http_requests_total{method,path,code}` (code числовой), `http_request_duration_seconds{method,path}`
- Telegram: `bot_updates_total{command,result}`, `bot_update_duration_seconds{command}`, `telegram_api_calls_total{method,result}`, `telegram_api_duration_seconds{method}`
- 3x-ui: `xui_requests_total{operation,result,node}` — `operation` = `ресурс.действие` (`clients.add`, `clients.update`, `clients.del`, `server.status`)
- Subserver: `subserver_source_fetch_total{result,format}`, `subserver_source_fetch_duration_seconds{result}`, `subserver_cache_invalidations_total{reason}`, `subserver_no_items_total`

## Шум и бюджет

- Логи: ~1 700 строк/сутки на бота — хранилище не под угрозой. `Subscription not found` троттлится: счётчик считает всё, WARN — первая пара `sub_id+IP` за час, остальное DEBUG (`utils.IntervalGate`).
- `filesystem_*` снимается с 25+ точек монтирования (tmpfs, overlay, sysfs) — в графики попадает только `/`; при желании фильтровать `mountpoint` в конфиге коллектора.
- Process-local counters не являются durable-источником для биллинга/аудита; аудит — таблица `audit_log`.

## Мультисерверность

Приложение экспортирует `node=<имя ноды из БД>` для 3x-ui. Это не метка хоста: второй инстанс = второй Better Stack source, агрегаты по выбранным source или breakdown по фактическим labels.
