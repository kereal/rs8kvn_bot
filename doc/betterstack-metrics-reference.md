# Better Stack Metrics Reference


## Dashboard IDs (Better Stack)

| ID      | Dashboard                  | Purpose                                          |
|---------|----------------------------|--------------------------------------------------|
| 1165127 | 🎯 Overview                 | Critical KPI, revenue, trends, health |
| 1165128 | 💰 Payments & Revenue       | Payment operations, amounts, chargebacks |
| 1165129 | 📊 Subscriptions & Lifecycle | Current state, lifecycle, workers |
| 1165131 | 🔌 APIs & Integrations      | HTTP, 3x-ui, subserver |
| 1165132 | 🗄️ Database & Cache         | SQLite operations, connection pool, caches |
| 1165133 | 🐳 Container & Runtime      | Container and Go runtime |
| 1165134 | 🖥️ Host Infrastructure      | Host resource metrics |
| 1165167 | 🤖 Bot Activity             | Telegram, HTTP and payments |

## Dashboard Structure

### 🎯 Overview (1165127)

**Быстрый снимок системы** — подписки, cumulative revenue, successful payment confirms и health.

**Sections:**
- **📊 State**: Active, Premium, Free, Trial gauges
- **💰 Revenue**: cumulative confirmed counters by currency; confirm count is total for the selected range
- **📈 Trends**: process-local create/renewal rates and cumulative revenue
- **⚡ Health**: container CPU%, goroutines, host load 1m

### 🤖 Bot Activity (1165167)

Операционные ряды по Telegram updates/API, HTTP status/latency и платежам. Метрики давно были объявлены; новый дашборд заменил некорректные, недифференцированные графики API dashboard.

### 💰 Payments & Revenue (1165128)

**Полная финансовая аналитика** — платежи по операциям, деньги по валютам, latencies.

**Sections:**
- **💵 Revenue by Currency**: Confirmed amounts (RUB/USD/EUR) — line charts cumulative
- **💳 Payment Operations**: Request/confirm/cancel по result (success/error) — rate charts
- **💸 Chargebacks**: Chargeback amounts по валютам
- **⏱️ Latencies**: Payment operation p50/p95/p99 (request, confirm, cancel)
- **📢 Traffic Notifications**: Traffic exhausted/re-enabled notifications

### 📊 Subscriptions & Lifecycle (1165129)

**Полная аналитика подписок** — state gauges, operations, workers, orphan reconciliation.

**Sections:**
- **📊 Active State**: Active/Premium/Free/Trial subscriptions (gauges)
- **🔄 Operations**: Creates, renewals (rate)
- **🔧 Workers**: Sync/expire/reminder worker runs + durations
- **🧹 Reconciliation**: Orphaned clients revoked, reconcile duration

### 🔌 APIs & Integrations (1165131)

**Все внешние интеграции** — HTTP API, Telegram Bot, 3x-ui panel, subserver.

**Sections:**
- **🌐 HTTP API**: Requests total rate, errors, latency p50/p95/p99
- **💬 Telegram Bot**: Updates by command, API calls, latencies
- **🔌 3x-ui Panel**: Requests by operation/result, latencies
- **📡 Subserver**: Source fetches (success/error), no-items, cache invalidations

### 🗄️ Database & Cache (1165132)

**SQLite операции и кэши** — query rate, latencies, pool wait, cache hit rates.

**Sections:**
- **💾 Database Operations**: Query rate (total), query latency p50/p95/p99, pool wait
- **🔄 Cache Performance**: Hits/misses по типу кэша, hit rate %

### 🐳 Container & Runtime (1165133)

**Docker контейнер + Go runtime** — CPU/memory/network контейнера, goroutines, GC, heap.

**Sections:**
- **🐳 Container**: CPU%, memory usage, network RX/TX
- **🔧 Go Runtime**: Goroutines, threads, heap alloc, heap in use

### 🖥️ Host Infrastructure (1165134)

**Хост и системные ресурсы** — CPU, memory, disk, network, load average.

**Sections:**
- **🔥 CPU**: Load 1m/5m/15m
- **💾 Memory**: Used, available, cached
- **💿 Disk**: Root FS used/free
- **🌐 Network**: ens3 RX/TX
- **⏰ System**: Uptime

## Application Metrics (bot экспортирует)

### Build (`app_*`)

- **`app_info{version,revision}`** — gauge, всегда 1: что именно крутится. `revision` — короткий VCS-хэш или ldflag-commit. Единственный способ увидеть со стороны дашборда, что прод крутит старый образ.

### HTTP API (`http_*`)

- **`http_requests_total{method,path,code}`** — counter: всего HTTP запросов (без `/metrics` и static assets)
  - `code`: **числовой** HTTP-код (`200`, `404`, `503`) — можно фильтровать `label('code') = '503'` или класс через префикс
- **`http_request_duration_seconds{method,path}`** — histogram: latency HTTP запросов

### Telegram Bot (`bot_*`, `telegram_*`)

- **`bot_updates_total{command,result}`** — counter: обработанные bot updates
  - `result`: `success`, `error`, `rate_limited`
- **`bot_update_duration_seconds{command}`** — histogram: время обработки update
- **`telegram_api_calls_total{method,result}`** — counter: вызовы Telegram Bot API
- **`telegram_api_duration_seconds{method}`** — histogram: latency Telegram Bot API

### 3x-ui Panel Integration (`xui_*`)

- **`xui_requests_total{operation,result,node}`** — counter: запросы к панели; `node` — имя настроенной VPN-ноды
  - `operation`: `ресурс.действие` — `clients.add`, `clients.update`, `clients.del`, `clients.traffic`, `clients.get`, `inbounds.get`, `server.status`
  - `result`: `success`, `error`
- **`xui_request_duration_seconds{operation,node}`** — histogram: latency запросов по ноде

### Database (`db_*`)

- **`db_queries_total{operation,result,table}`** — counter: всего database queries
  - `table`: имя таблицы из GORM-модели (`subscriptions`, `orders`, …) или `other`. Именно по ней видно, какой запрос падает
- **`db_query_duration_seconds{operation}`** — histogram: latency database queries
- **`db_pool_waits_total`** — counter: сколько раз вызов ждал свободное соединение с момента старта (`sql.DBStats.WaitCount`)
- **`db_pool_wait_seconds_total`** — counter: сколько секунд суммарно ушло на ожидание (`sql.DBStats.WaitDuration`)

Оба — **counters** и снимаются с `sql.DBStats` раз в 10 секунд: сборщик добавляет приращение, поэтому повторный сбор ничего не удваивает. Читать через `avgMerge(rate_avg)`. Абсолютное значение смысла не несёт; текущей длины очереди среди метрик нет.
### Cache (`cache_*`)

- **`cache_hits_total{cache}`** — counter: cache hits
  - `cache`: `subscription`, `subserver`
- **`cache_misses_total{cache}`** — counter: cache misses

### Subscriptions — Gauges (`*_subscriptions`)

- **`active_subscriptions`** — gauge: текущие активные подписки (любые)
- **`premium_subscriptions`** — gauge: текущие активные paid подписки
- **`free_subscriptions`** — gauge: текущие активные free подписки
- **`trial_subscriptions`** — gauge: текущие trial подписки (telegramID=0)

### Subscriptions — Operations (`subscription_*`)

- **`subscription_creates_total`** — counter: successful first-time subscription creations
- **`subscription_reanimations_total`** — counter: revoked subscriptions reactivated on the free tier
- **`subscription_renewals_total`** — counter: paid subscription renewals confirmed through payment callback

These are process-local counters; they reset on restart and are not durable business history.

### Subscriptions — Workers (`subscription_*`, `reconcile_*`, `bot_orphaned_*`)

- **`subscription_sync_total{result}`** — counter: прогоны sync worker, `result`: `ok` / `degraded` (прогон вернул агрегированную ошибку)
- **`subscription_sync_duration_seconds`** — histogram: длительность sync worker
- **`subscription_expire_total{result}`** — counter: прогоны expire worker, `result`: `ok` / `degraded` (часть подписок не отключилась)
- **`subscription_expire_duration_seconds`** — histogram: worker duration
- **`subscriptions_expired_total`** — counter: сколько подписок реально отключил expire worker (не путать с числом прогонов)
- **`subscription_reminder_runs_total`** — counter: reminder scans, including empty scans
- **`subscription_reminders_total{window,result}`** — counter: attempted reminder sends; no rows means no eligible user/sends, not necessarily worker outage
- **`traffic_notification_runs_total`** — counter: traffic scans, including empty scans
- **`traffic_notifications_total{kind,result}`** — counter: attempted traffic notifications; no rows means no eligible transitions, not necessarily worker outage
- **`reconcile_orphaned_duration_seconds`** — histogram: orphan reconciliation duration
- **`bot_orphaned_clients_revoked_total`** — counter: subscriptions revoked during reconciliation

### Subscription Reminders (`subscription_reminder*`)

- **`subscription_reminders_total{window,result}`** — counter: отправки expiry reminders
  - `window`: срок до истечения
  - `result`: `success`, `error`
- **`subscription_reminder_runs_total`** — counter: прогоны reminder worker

### Traffic Notifications (`traffic_notification*`)

- **`traffic_notifications_total{kind,result}`** — counter: отправки traffic notifications
  - `kind`: тип уведомления (exhausted/re-enabled)
  - `result`: `success`, `error`
- **`traffic_notification_runs_total`** — counter: прогоны traffic notification worker

### Subserver (`subserver_*`)

- **`subserver_requests_total{format}`** — counter: выдачи по форматам (`xray`, `singbox`, `clash`). Нулевые серии публикуются сразу, поэтому «нет данных» означает «нет трафика», а не «метрика не работает»
- **`subserver_not_found_total`** — counter: запросы по удалённым/отозванным подпискам (клиент, у которого остался конфиг, продолжает опрашивать ссылку)
- **`subserver_source_fetch_total{result,format}`** — counter: fetches upstream sources
  - `result`: `success`, `error`
  - `format`: `json`, `base64`, `plain`, `unknown`
- **`subserver_source_fetch_duration_seconds{result}`** — histogram: latency source fetch
- **`subserver_cache_invalidations_total{reason}`** — counter: cache invalidations
  - `reason`: `revoked`, `expired`, `status_error`, `not_found`
- **`subserver_no_items_total`** — counter: запросы где не удалось получить subscription items ни от одного source

### Payments — Operations (`payment_operations_*`, `payment_operation_duration_*`)

- **`payment_operations_total{operation,result}`** — counter: payment service operations
  - `operation`: `request`, `confirm`, `cancel`
  - `result`: `success`, `error`
- **`payment_operation_duration_seconds{operation}`** — histogram: latency операций payment service

### Payments — Money (`payment_amount_cents_total`)

- **`payment_amount_cents_total{operation,currency}`** — counter: денежные суммы в копейках
  - `operation`: `confirmed` (успешный CONFIRMED, только Activated), `chargeback` (CHARGEBACKED на оплаченном заказе)
  - `currency`: ISO 4217 (RUB, USD, EUR, ...)
  - **КРИТИЧНО**: это counter суммы в центах (копейках), не количество операций

### Payments — Issues (`payment_issues_total`)

- **`payment_issues_total{event}`** — counter: операционные проблемы платежей
  - `event`: стабильное имя события без IDs

## Host и контейнеры (фактические имена коллектора)

Коллектор Better Stack отдаёт **свои** имена метрик: префиксов `node_*`, `container_*`, `host_*` в источнике нет. Запросы с ними вернут пустоту (проверено на живой базе 2026-11-02).

### Контейнеры (`container_name` = `netbird-client`, `beszel-agent`, `beszel`, `3xui_app`, `rs8kvn_bot`)

- **`cpu_percentage{container_name}`** — gauge: процент CPU контейнера
- **`cpu_seconds_total{container_name}`** — counter: накопленное CPU-время
- **`memory_used_bytes{container_name}`** — gauge: потребление памяти
- **`memory_limit_bytes{container_name}`** — gauge: лимит памяти
- **`memory_percentage{container_name}`** — gauge: процент от лимита
- **`pids_count{container_name}`** — gauge: число процессов
- **`network_in_bytes{container_name}`** / **`network_out_bytes{container_name}`** — сеть контейнера
- **`block_in_bytes{container_name}`** / **`block_out_bytes{container_name}`** — дисковый IO контейнера
- **`disk_read_bytes_total`**, **`disk_written_bytes_total`**, **`disk_reads_completed_total`**, **`disk_writes_completed_total`** — тоже с `container_name`

Важно: серия `memory_used_bytes` **без** `container_name` — это сам хост. Для контейнеров всегда фильтруй `label('container_name') != ''`, иначе хост попадёт в общий график.

### Хост

- **`load1` / `load5` / `load15`** — gauge: load average (читать через `maxMerge(value_max)` — метрика приходит с несколькими сэмплами на бакет)
- **`logical_cpus`**, **`physical_cpus`** — gauge: ядра, база для оценки load
- **`memory_total_bytes`**, **`memory_used_bytes`**, **`memory_available_bytes`**, **`memory_cached_bytes`**, **`memory_buffers_bytes`**, **`memory_free_bytes`**, **`memory_active_bytes`**, **`memory_shared_bytes`**, **`memory_percentage`**
- **`memory_swap_total_bytes`**, **`memory_swap_used_bytes`**, **`memory_swap_free_bytes`**, **`memory_swapped_in_bytes_total`**, **`memory_swapped_out_bytes_total`**
- **`memory_oom_kill_events_total`** — counter: убийства по OOM; любое значение > 0 требует разбора
- **`filesystem_total_bytes`**, **`filesystem_used_bytes`**, **`filesystem_free_bytes`**, **`filesystem_used_ratio`**, **`filesystem_inodes_total`**, **`filesystem_inodes_used`**, **`filesystem_inodes_free`**, **`filesystem_inodes_used_ratio`** — labels `device`, `filesystem`, `mountpoint`
  - На хосте 25+ точек монтирования (tmpfs, overlay-слои, sysfs, proc). Реальные для наблюдения: `mountpoint='/'` (ext4 на `/dev/sda1`) и `/boot/efi`. Остальное — шум, фильтруй в конфиге коллектора или в запросе.
- **`network_receive_bytes_total`**, **`network_transmit_bytes_total`**, **`network_receive_packets_total`**, **`network_transmit_packets_total`**, **`network_receive_errs_total`**, **`network_transmit_errs_total`**, **`network_transmit_packets_drop_total`** — label `device` (на проде интерфейс `ens3`)
- **`uptime`**, **`boot_time`** — gauge

### Приложение (стандартные коллекторы)

- **`go_goroutines`**, **`go_threads`**, **`go_memstats_*`**, **`go_gc_duration_seconds_count`**, **`go_gc_duration_seconds_sum`**, **`go_gc_gogc_percent`**, **`go_gc_gomemlimit_bytes`**, **`go_sched_gomaxprocs_threads`**, **`go_info`**
- **`process_resident_memory_bytes`**, **`process_virtual_memory_bytes`**, **`process_cpu_seconds_total`**, **`process_open_fds`**, **`process_max_fds`**, **`process_start_time_seconds`**, **`process_network_receive_bytes_total`**, **`process_network_transmit_bytes_total`**
- **`promhttp_metric_handler_requests_total{code}`**, **`promhttp_metric_handler_requests_in_flight`** — здоровье самого scrape; 500 здесь означает дыру в данных

## Query Patterns

### Counters (rate per second)

Для всех `*_total` метрик используй **avgMerge(rate_avg)** для rate/sec. Функции `derivativeMerge` в Better Stack нет — старые примеры с ней не работали:

```sql
SELECT
  {{time}} AS time,
  avgMerge(rate_avg) AS "Rate"
FROM {{source}}
WHERE dt BETWEEN {{start_time}} AND {{end_time}}
  AND name = 'payment_operations_total'
  AND label('operation') = 'confirm'
  AND label('result') = 'success'
GROUP BY time
ORDER BY time
```

### Gauges (current value)

Для gauge метрик используй **avgMerge(value_avg)**:

```sql
SELECT
  {{time}} AS time,
  avgMerge(value_avg) AS "Active"
FROM {{source}}
WHERE dt BETWEEN {{start_time}} AND {{end_time}}
  AND name = 'active_subscriptions'
GROUP BY time
ORDER BY time
```

### Histograms (percentiles)

Для `*_seconds` histogram метрик используй **quantileMerge** для percentiles:

```sql
SELECT
  {{time}} AS time,
  quantileMerge(0.50)(value_quantiles) AS "p50",
  quantileMerge(0.95)(value_quantiles) AS "p95",
  quantileMerge(0.99)(value_quantiles) AS "p99"
FROM {{source}}
WHERE dt BETWEEN {{start_time}} AND {{end_time}}
  AND name = 'payment_operation_duration_seconds'
  AND label('operation') = 'confirm'
GROUP BY time
ORDER BY time
```

### Money Totals (cumulative counter)

`payment_amount_cents_total` — counter суммы в minor units. В Better Stack `value_avg` содержит сохранённое значение counter; `rate_avg` — rate по окнам. Для суммы периода используй last minus first cumulative sample, а не суммирование samples. Для **счётчиков** то же правило: «сколько за период» = `maxMerge(value_max) - minMerge(value_min)`, а `maxMerge(value_max)` в одиночку — это накопленное с момента рестарта процесса, а не за интервал.

```sql
SELECT
  (maxMerge(value_max) - minMerge(value_min)) / 100 AS "Total Amount"
FROM {{source}}
WHERE dt >= {{start_time}} AND dt < {{end_time}}
  AND name = 'payment_amount_cents_total'
  AND label('operation') = 'confirmed'
  AND label('currency') = 'RUB'
```

Для временного ряда cumulative amount (не revenue-per-hour):

```sql
SELECT
  {{time}} AS time,
  avgMerge(value_avg) / 100 AS "Cumulative Amount"
FROM {{source}}
WHERE dt >= {{start_time}} AND dt < {{end_time}}
  AND name = 'payment_amount_cents_total'
  AND label('operation') = 'confirmed'
  AND label('currency') = 'RUB'
GROUP BY time
ORDER BY time
```

## Multi-Server Support

Все дашборды готовы к мультисерверности:
- Используй `label('host')` или `label('container_name')` для группировки по серверам
- Aggregate views для общей картины (без group by host)
- Per-host breakdown для детализации (group by host, series = host)

Пример multi-host query:

```sql
SELECT
  {{time}} AS time,
  label('host') AS series,
  avgMerge(value_avg) AS "Active"
FROM {{source}}
WHERE dt BETWEEN {{start_time}} AND {{end_time}}
  AND name = 'active_subscriptions'
GROUP BY time, series
ORDER BY time
```

## Maintenance

- **Код метрик**: `internal/metrics/metrics.go`
- **GORM callbacks**: `internal/metrics/db.go`
- **Тесты**: `internal/metrics/metrics_test.go`, `internal/metrics/db_test.go`
- **Документация проекта**: `.serena/memories/metrics.md`
- **Subserver audit**: `doc/subserver_metrics_audit.md`

При добавлении новой метрики:
1. Определить в `internal/metrics/metrics.go` через `promauto`
2. Инкрементировать/обновлять в нужном сервисе/handler
3. Добавить smoke-test в `metrics_test.go`
4. Обновить этот файл (`doc/betterstack-metrics-reference.md`)
5. Создать график в соответствующем дашборде Better Stack
