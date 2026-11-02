# Better Stack: дашборды, алерты и метрики

Обновлено: 2026-11-02. Источник: `hip-01 docker` (ID `2288221`, хост `<prod-host>`, team `t513087`).
Один источник объединяет метрики хоста, Docker-контейнеров и приложения `rs8kvn_bot`, логи всех контейнеров и heartbeat'ы.

## Структура дашбордов

| Dashboard | ID | Назначение |
|-----------|----|------------|
| 🎯 Overview | 1165127 | KPI, доход, деплой, 5xx — снимок «как дела» |
| 🖥️ Server (host) | 1165134 | CPU/load, память, swap, OOM, диск, иноды, сеть, uptime |
| 🐳 Containers & Runtime | 1165133 | По контейнерам: CPU, память, лимиты, PIDs, IO; Go runtime |
| 🤖 Telegram Bot | 1165167 | Updates по командам, latency handler'ов и Telegram API |
| 🔌 API & VPN Panel | 1165131 | HTTP-маршруты, 3x-ui, subserver (форматы, 404, кеш) |
| 📊 Subscriptions & Lifecycle | 1165129 | Состояния, жизненный цикл, воркеры, биндинги |
| 💰 Payments & Revenue | 1165128 | Выручка по валютам, операции, инциденты платежей |
| 🗄️ Database & Cache | 1165132 | Запросы, latency, ошибки по таблицам, пул, размер БД, кеш |

Все дашборды привязаны к источнику `2288221`; фильтры — по labels, которые реально приходят (`node`, `container_name`, `path`, `format`, `status_code`).

## ⚠️ Алерты (включены)

Все восемь активны, уведомления — email + push текущей команде (`current_team`), политик эскалации нет.

| Алерт | ID | Условие | График |
|---|---|---|---|
| VPN node panel is down | 2988725001 | `node_up < 1` за 5 мин | Node panel availability (1165131) |
| Parked subscription bindings | 2988725002 | `subscription_nodes_parked > 0` | Parked bindings (1165129) |
| Background worker restarted after panic | 2988725003 | `worker_restarts_total` растёт | Worker restarts (1165129) |
| Database query errors | 2988725004 | > 0.05 ошибок/с за 5 мин | DB Query Errors by Table (1165132) |
| HTTP 5xx responses | 2988725005 | > 0.05 5xx/с за 5 мин | HTTP 5xx rate (1165127) |
| SQLite file too large | 2988725006 | `db_file_bytes > 200 MiB` за час | SQLite size (1165132) |
| Sync worker stopped | 2988725010 | rate `subscription_sync_total{result="ok"}` < 0.0005 за 30 мин, серия `ok` | Sync runs by result (1165129) |
| Payment integration issue | 2988725011 | `payment_issues_total` растёт | Payment issues by event (1165128) |

Логика отбора: алерт нужен там, где без него о поломке узнаёшь от пользователя. Остальное смотрят глазами.
Алерты на `node_up`, `subscription_nodes_parked`, `worker_restarts_total`, `db_file_bytes`, `sync stopped` начнут срабатывать **после релиза** — этих серий нет в прод-сборке `9d04409`.

---

## 🎯 Overview (1165127)

### KPIs (number)
- **Active Subs / Premium / Free / Trial** — текущие гейджи (`active_subscriptions`, `premium_subscriptions`, `free_subscriptions`, `trial_subscriptions`)
- **RUB/USD/EUR Revenue (period)** — сумма подтверждённых оплат **за выбранный диапазон** (`maxMerge(value_max) - minMerge(value_min)) / 100`)
- **Successful Confirms (period)** — дельта `payment_operations_total{operation=confirm,result=success}`

### Trends
- **Creates / Renewals** — rate `subscription_creates_total`, `subscription_renewals_total`
- **Container CPU %**, **Go Goroutines**, **Host Load 1m** — быстрый health snapshot

### Health
- **Deployed build** — `app_info{version,revision}`: единственный способ увидеть устаревший деплой
- **HTTP 5xx rate** — `http_requests_total` с числовым `code`

---

## 🖥️ Server (host) (1165134)

Покрывает сервер, а не только бота. CPU-проценты по контейнерам — на 1165133, здесь агрегаты и состояние хоста.

### CPU
- **Load 1m / 5m / 15m** — `load1/load5/load15` (`maxMerge(value_max)`)
- **CPU cores** — `logical_cpus` / `physical_cpus`, база для оценки load average

### Memory
- **Used / Available / Cached** — `memory_used_bytes` (серия хоста, без `container_name`), `memory_available_bytes`, `memory_cached_bytes`
- **Swap used / total** — `memory_swap_used_bytes` / `memory_swap_total_bytes`
- **OOM kills** — `memory_oom_kill_events_total`, любое значение больше нуля означает, что кого-то убили по памяти

### Disk
- **Root Used / Root Free** — `filesystem_used_bytes` / `filesystem_free_bytes` для `mountpoint='/'`
- **Root inodes used** — `filesystem_inodes_used_ratio`, иноды кончаются раньше места

### Network
- **ens3 RX / ens3 TX** — `network_receive_bytes_total` / `network_transmit_bytes_total` по устройству

### System
- **Uptime** — `uptime`

Шум источника: коллектор снимает `filesystem_*` со всех 25+ точек монтирования (tmpfs, overlay-слои, sysfs). В графики попадает только `/`. Если шум мешает — фильтровать `mountpoint` в конфиге коллектора, код приложения тут не участвует.

---

## 🐳 Containers & Runtime (1165133)

### Container
Все графики разбиты по `label('container_name')` и исключают серию хоста (`label('container_name') != ''`): netbird-client, beszel-agent, beszel, 3xui_app, rs8kvn_bot.

- **CPU Percentage** — `cpu_percentage`
- **Memory Usage** — `memory_used_bytes`
- **Container memory limit** — `memory_limit_bytes`; рядом с usage даёт запас до OOM
- **Container PIDs** — `pids_count`; утечка форков видна сразу
- **Network RX / TX** — `network_in_bytes` / `network_out_bytes`
- **Container disk IO** — `block_in_bytes` / `block_out_bytes`, серия `контейнер направление`; рост записи у `rs8kvn_bot` — это WAL SQLite

### Go Runtime
- **Goroutines** — `go_goroutines`
- **Threads** — `go_threads`
- **Heap Alloc / Heap In Use** — `go_memstats_heap_alloc_bytes` / `go_memstats_heap_inuse_bytes`
- **GC cycles and pause time** — `go_gc_duration_seconds_count` / `go_gc_duration_seconds_sum` (rate)
- **Open file descriptors** — `process_open_fds` / `process_max_fds`
- **Metrics scrape health** — `promhttp_metric_handler_requests_total{code}`; 500 здесь = дыра в данных

---

## 🤖 Telegram Bot (1165167)

### Telegram Updates
- **Updates by Command / Result** — `bot_updates_total{command,result}`; `command` ограничен whitelist'ом в `normalizeCommand` (start/help/mysub/callback/text/…), кардинальность под контролем
- **Update Handler Latency (mean)** — `bot_update_duration_seconds` по командам

### Telegram API
- **Telegram API Errors** — `telegram_api_calls_total{result=error}`
- **Telegram API calls by result** — `telegram_api_calls_total{method,result}`
- **Telegram API Latency (mean)** — `telegram_api_duration_seconds`

### HTTP API
- **HTTP Requests by Status** — по `coalesce(nullIf(label('code'),''), label('status'))`, поэтому график живёт и на старой, и на новой сборке
- **HTTP Request Latency (mean)** — по `path`

Секция Payments из этого дашборда удалена: деньги живут в 💰 Payments & Revenue, дубль показывал накопленную сумму (`avgMerge(value_avg)` по counter'у) вместо потока.

---

## 🔌 API & VPN Panel (1165131)

### HTTP API
- **HTTP Requests by Status** — rate по `status_code`
- **HTTP Requests by path** — `/sub/:id`, `/i/:code`, `/payment/callback`, `other`. Сканерные пути схлопываются в `/sub/:id`, кардинальность не растёт (проверено: 3 серии на проде)

### 3x-ui Panel
- **XUI Requests by Node / Result** — `xui_requests_total{operation,result,node}`, `operation` = `ресурс.действие` (`clients.add`, `clients.update`, `clients.del`, `clients.traffic`, `clients.get`, `inbounds.get`, `server.status`)
- **XUI Latency (mean)** — `xui_request_duration_seconds`
- **Node panel availability** — `node_up{node}` (1/0) по проходам синка

### Subserver
- **Source Fetches** — `subserver_source_fetch_total{result,format}`
- **Source fetch latency (mean)** — `subserver_source_fetch_duration_seconds{format}`
- **No Items** — `subserver_no_items_total`
- **Subscriber requests by format** — `subserver_requests_total{xray|singbox|clash}`
- **Dead subscription requests** — `subserver_not_found_total`: клиенты, у которых остался конфиг удалённой подписки (после массовых `/del` это норма, а не сбой)
- **Cache invalidations by reason** — `subserver_cache_invalidations_total{reason}`

---

## 📊 Subscriptions & Lifecycle (1165129)

### Active Counts
- **Total / Premium / Free / Trial** — гейджи, пересчитываются каждым 5-минутным проходом синка (раньше обновлялись только на мутациях и показывали 0 после рестарта)

### Operations
- **Creates Rate / Renewals Rate / Reanimations Rate** — rate соответствующих счётчиков
- **Creates (period) / Renewals (period) / Reanimations (period)** — дельты за диапазон

### Workers
- **Reminder Runs / Expire runs by result / Sync runs by result** — прогоны воркеров; `result="degraded"` = прогон вернул ошибку по части элементов
- **Reminders sent / Traffic notifications sent** — реальные отправки, а не сканы
- **Subscriptions expired** — `subscriptions_expired_total`, сколько подписок реально отключено
- **Parked bindings** — `subscription_nodes_parked`
- **Bindings by status** — `subscription_nodes{status}`: очередь провижнинга и её застой
- **Worker restarts** — `worker_restarts_total{worker}`

### Cleanup
- **Orphans Revoked** — `bot_orphaned_clients_revoked_total`
- **Orphan reconcile latency (mean)** — `reconcile_orphaned_duration_seconds`

---

## 💰 Payments & Revenue (1165128)

### 💵 RUB / USD / EUR
- **Confirmed per interval (валюта)** — сколько подтверждено **в каждом бакете**: `(maxMerge(value_max) - minMerge(value_min)) / 100`
- **… Revenue (period)** — сумма за выбранный диапазон тем же выражением

### 📋 Operations
- **Success (by operation) / Errors (by operation)** — `payment_operations_total`
- **Successful Confirms (period)** — дельта по `operation=confirm,result=success`

### 💸 Chargebacks
- **By Currency** — `payment_amount_cents_total{operation=chargeback}`
- **Chargebacks (period)** — дельта суммы chargeback за диапазон

### 💳 Payment Operations
- **Payment Operations Rate** — rate по `result`
- **Payment operation latency (mean)** — `payment_operation_duration_seconds{operation}`
- **Payment reconcile runs** — `payment_reconcile_total`

### Operations
- **Payment issues by event** — `payment_issues_total{event}` (стабильные имена событий без ID)

---

## 🗄️ Database & Cache (1165132)

### Database
- **Query Rate** — `db_queries_total` по `result`
- **DB Query Latency (mean)** — `sumMerge(bucket_sum) / sumMerge(bucket_count)` по операциям
- **DB Query Errors by Table** — `db_queries_total{result=error}` с меткой `table`: адрес проблемы, а не «query error» вообще
- **DB Pool Waits (rate)** — `db_pool_waits_total` (counter)
- **DB pool wait time** — `db_pool_wait_seconds_total` (counter), секунды ожидания в секунду
- **SQLite size (file + WAL)** — `db_file_bytes` / `db_wal_bytes`

### Cache
- **Cache Hits Rate / Cache Misses Rate** — `cache_hits_total{cache}` / `cache_misses_total{cache}` для `subscription` и `subserver`; сравнивать как два rate, не как процент

---

## Экспортируемые метрики приложения

50 коллекторов в `internal/metrics`. Полный справочник запросов — `doc/betterstack-metrics-reference.md`.

### Subscriptions
- `active_subscriptions` / `premium_subscriptions` / `free_subscriptions` / `trial_subscriptions` (gauges) — текущее состояние
- `subscription_nodes{status}`, `subscription_nodes_parked` (gauges) — биндинги
- `subscription_creates_total`, `subscription_reanimations_total`, `subscription_renewals_total` — события жизненного цикла
- `subscription_sync_total{result}` / `subscription_sync_duration_seconds` — прогоны синка
- `subscription_expire_total{result}` / `subscription_expire_duration_seconds` / `subscriptions_expired_total`
- `subscription_reminder_runs_total` / `subscription_reminders_total{window,result}`
- `traffic_notification_runs_total` / `traffic_notifications_total{kind,result}`
- `bot_orphaned_clients_revoked_total` / `reconcile_orphaned_duration_seconds`
- `node_up{node}` — доступность панели ноды, `worker_restarts_total{worker}`

### Payments
- `payment_operations_total{operation,result}`, `payment_amount_cents_total{operation,currency}`
- `payment_operation_duration_seconds{operation}` (histogram), `payment_issues_total{event}`, `payment_reconcile_total`

### HTTP API
- `http_requests_total{method,path,code}` — `code` числовой (`200`, `404`, `503`)
- `http_request_duration_seconds{path}` (histogram), `app_info{version,revision}` (gauge=1)

### Telegram
- `bot_updates_total{command,result}`, `bot_update_duration_seconds{command}`
- `telegram_api_calls_total{method,result}` (инструментированы отправки), `telegram_api_duration_seconds{method}`

### 3x-ui
- `xui_requests_total{operation,result,node}` — `operation` = `ресурс.действие`
- `xui_request_duration_seconds{operation,node}`

### Subserver
- `subserver_requests_total{format}` — `xray`/`singbox`/`clash`, нулевые серии публикуются сразу
- `subserver_not_found_total` — запросы по удалённым подпискам
- `subserver_source_fetch_total{result,format}`, `subserver_source_fetch_duration_seconds{result}`
- `subserver_cache_invalidations_total{reason}`, `subserver_no_items_total`

### Database / Cache
- `db_queries_total{operation,result,table}`, `db_query_duration_seconds{operation}`
- `db_pool_waits_total`, `db_pool_wait_seconds_total` (counters, приращение `sql.DBStats`)
- `db_file_bytes`, `db_wal_bytes`
- `cache_hits_total{cache}` / `cache_misses_total{cache}`

### Host и контейнеры (имена коллектора Better Stack, не node_exporter)
- `cpu_percentage{container_name}`, `cpu_seconds_total{container_name}`
- `memory_used_bytes`, `memory_limit_bytes`, `pids_count` — по `container_name`; серия без `container_name` — сам хост
- `network_in_bytes`, `network_out_bytes`, `block_in_bytes`, `block_out_bytes` — по контейнерам
- `load1` / `load5` / `load15`, `logical_cpus`, `physical_cpus`, `uptime`, `boot_time`
- `memory_total_bytes`, `memory_available_bytes`, `memory_cached_bytes`, `memory_swap_*`, `memory_oom_kill_events_total`
- `filesystem_used_bytes`, `filesystem_free_bytes`, `filesystem_inodes_used_ratio` — по `device`/`filesystem`/`mountpoint`
- `network_receive_bytes_total`, `network_transmit_bytes_total`, `network_*_errs_total`, `network_transmit_packets_drop_total` — по устройству
- `disk_read_bytes_total`, `disk_written_bytes_total`, `disk_*_completed_total`

Важно: префиксов `node_*` / `container_*` в источнике **нет** — это имена коллектора Better Stack. Запросы с `node_memory_*` или `container_*` вернут пустоту.

## Мультисерверная поддержка

Приложение экспортирует `node=<имя ноды из БД>` для запросов к панели. Это не метка хоста: при нескольких инстансах бота нужна отдельная стабильная метка `host`/`service` в конфигурации коллектора.

Второй сервер = второй Better Stack source; дальше либо агрегаты по выбранным source, либо breakdown по фактически присутствующим labels.

## Ограничения, проверенные на живой базе

1. **Перцентили не считаются.** `histogramQuantile(p)`, `quantileMerge` и `quantilePrometheusHistogramArrayMerge(p)(bucket_quantiles)` возвращают верхнюю границу последнего бакета для любого квантиля (DB — всегда `0.5`, HTTP — всегда `10`, XUI — всегда `5`). Среднее (`sumMerge(bucket_sum)/sumMerge(bucket_count)`) считается верно: DB-запрос ≈ 0.32 мс, `/sub` ≈ 17.7 мс. Все latency-графики переведены на среднее.
2. **Counter читается только через rate.** `avgMerge(rate_avg)` для «сколько в секунду», `max−min` для «сколько за период». `maxMerge(value_max)` в одиночку — накопленное с рестарта процесса, а не за диапазон.
3. **Пустая серия ≠ ноль.** Permetheus не отдаёт детей `CounterVec` до первого события. Для воркеров и форматов выдачи приложение публикует нулевые серии в `init()`, остальные (платежи, кеш-инвалидации) появятся после первого события. Графики с `treat_missing_values: zero` показывают ноль, а не разрыв.
4. **`derivativeMerge` отсутствует** в Better Stack, хотя фигурировал в старых записях.
5. **`series_column` обязан совпадать с алиасом в SQL.** Если запрос отдаёт `operation`/`command`, а настройки ждут `series`, легенда пустая, хотя график рисуется. Проверяй пару «алиас в запросе ↔ `series_column`».
6. **RUM-дашборды `Web vitals` (1077180) и `Website analytics` (1077181) удалить через API нельзя** — `remove_dashboard` возвращает 404, хотя они видны в списке. Не наши данные, оставлены как есть.

## История изменений

### 2026-11-02: полная ревизия под аудит

- Дашборды переименованы по домену: `🖥️ Server (host)`, `🐳 Containers & Runtime`, `🤖 Telegram Bot`, `🔌 API & VPN Panel`, `🗄️ Database & Cache`.
- Покрытие расширено с бота на **сервер**: swap, OOM kills, иноды `/`, ядра CPU; контейнеры — CPU/память/лимиты/PIDs/disk IO разбиты по `container_name` (раньше усреднялись в одну серию).
- Найдено и исправлено 8 графиков с теми же дефектами, что в первом проходе: три «p95» на Bot Activity, три «hourly» revenue на Payments (`value_avg` вместо дельты), старый лейбл `status` на Bot Activity, накопительный revenue.
- Добавлены графики: CPU cores, Swap, OOM kills, Root inodes, Container memory limit, Container PIDs, Container disk IO, GC cycles/pause, Open FDs, Metrics scrape health, HTTP Requests by path, Cache invalidations by reason, Source fetch latency, Bindings by status, Reminders sent, Traffic notifications sent, Orphan reconcile latency, Payment operation latency, Payment reconcile runs, DB pool wait time, Telegram API calls by result.
- Удалены: «Sync Runs» (дубль «Sync runs by result»), «Cumulative Revenue (process lifetime)» на Overview, пустая секция «Telegram Bot» в 1165131, секция Payments на 🤖 Telegram Bot (дубль + чтение counter'а как значения), а также починен `series_column` у «Updates by Command / Result».
- Все 8 алертов **включены** (ранее 6 были в paused).
- Зафиксированы ограничения Better Stack по перцентилям и чтению counters.

### 2026-10-26: полная реорганизация
- Структура из 7 дашбордов по доменам; добавлена финансовая аналитика; поддержка мультисерверности через labels.
- Удалены старые дашборды: 1164939, 1164943–1164948, пустые RUM (Web vitals, Website analytics).

### 2026-10-26: аудит метрик
- Исправлены запросы по labels/result и семантика cumulative counters; удалены дубли cache charts.
- Добавлен Bot Activity (1165167), графики WAL/пула и labels кеша, `node` для XUI.

### Старые дашборды (удалены)
- Host + Docker — обзор (1165002), Host (Vector) (942771), Docker (942772)
- rs8kvn_bot — обзор (1164980), API и интеграции (1164981), платежи (1164982), runtime и инфраструктура (1164983)
