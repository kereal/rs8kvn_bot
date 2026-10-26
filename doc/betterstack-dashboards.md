# Better Stack: дашборды и метрики

Обновлено: 2026-10-26. Источник метрик: `hip-01 docker` (ID `2288221`). Объединяет данные хоста, Docker контейнеров и приложения `rs8kvn_bot`.

## Структура дашбордов

Все дашборды привязаны к источнику `hip-01 docker` (`2288221`); фильтры используют labels для разделения по серверу, ноде и контейнеру.

| Dashboard | ID | Назначение |
|-----------|----|------------|
| 🎯 Overview | 1165127 | Критические KPI, текущие подписки, доходы и здоровье |
| 💰 Payments & Revenue | 1165128 | Денежные суммы, платежные операции, ошибки, chargebacks |
| 📊 Subscriptions & Lifecycle | 1165129 | Состояния, создания/продления, workers |
| 🔌 APIs & Integrations | 1165131 | HTTP API, 3x-ui, subserver |
| 🗄️ Database & Cache | 1165132 | DB operations, pool waits, caches |
| 🐳 Container & Runtime | 1165133 | Docker container и Go runtime |
| 🖥️ Host Infrastructure | 1165134 | CPU, память, диск, сеть и uptime хоста |
| 🤖 Bot Activity | 1165167 | Telegram updates/API, HTTP API и финансовые события |


## 🎯 Overview

**Главный дашборд** — быстрый снимок всей системы.

### KPIs (число)
- **Active Subscriptions** — текущие активные подписки
- **Premium** / **Free** / **Trial** — breakdown по типам
- **Confirmed Revenue** — накопительная подтверждённая сумма в RUB/USD/EUR, process lifetime
- **Successful Confirms** — число успешных callback-активаций за выбранный период

### Trends (временные ряды)
- **Subscription Creates** — rate создания/реанимации подписок
- **Subscription Renewals** — rate успешных платных продлений; ноль до релиза, который начал инкрементировать метрику
- **Cumulative Revenue** — накопительный counter в выбранном временном диапазоне (не hourly/daily revenue)
- **Container CPU %**, **Go Goroutines**, **Host Load 1m** — быстрый health snapshot

---

## 🤖 Bot Activity (1165167)

Детальный просмотр Telegram updates по командам и результатам, latency handlers, ошибок/latency Telegram API, HTTP status/latency и финансовых счётчиков. Метрики были в приложении до аудита; dashboard заменяет некорректные undimensioned API charts.


## 📊 Subscriptions & Lifecycle

### Subscription KPIs
- Active, Premium, Free, Trial — current gauges
- Creates, reanimations, paid renewals — process-local counter rates

### Workers
- Sync/expire/reminder/traffic notification worker scans
- Available worker duration histograms
- Orphaned clients revoked and reconciliation duration

---

## 💰 Payments & Revenue

**Полная финансовая аналитика** — детальная информация о платежах и выручке.

### Financial KPIs
- **RUB/USD/EUR Confirmed Revenue** — cumulative counter values, not the amount for the selected interval
- **Successful Confirms** — successes seen in the selected time range
- **Chargebacks** — cumulative counter value, when present

### Operations
- Request/confirm/cancel rates by result
- Payment issues by event
- Payment latency histogram is exported; dashboard latency charts depend on histogram ingest/schema support

---

## 🔌 APIs & Integrations (1165131)

### 🌐 HTTP API
- Requests rate by status; latency currently available via Bot Activity dashboard (1165167).

### 🔌 VPN Panel & Subserver
- 3x-ui requests by operation/result/node, plus latency p95.
- Subserver source fetches by result/format and no-items count.

Telegram updates and Telegram API charts are in **🤖 Bot Activity (1165167)**.

## 🗄️ Database & Cache

### 💾 SQLite Operations
- **DB Operations by Type** — операции по типам (success)
- **DB Errors** — ошибки по операциям
- **DB Query Latency p50** — медиана latency
- **DB Query Latency p95**
- **DB Query Latency p99**
- **DB Latency by Operation p95** — breakdown по операциям
### 🗃️ Cache Performance
- `cache_hits_total{cache}` / `cache_misses_total{cache}` — rates split by `subscription`/`subserver`; compare as a hit/miss rate, do not interpret each chart as a percentage.
- **DB Pool Wait Count** — cumulative `sql.DBStats.WaitCount` (not active queue depth).
- **DB Pool Wait Duration** — cumulative wait duration in seconds; new metric absent from captured prod scrape.

---

## 🐳 Container & Runtime

### 🐳 Docker Container
- **Container CPU %** — процент CPU контейнера
- **Container Memory Usage MB** — используемая память в MB
- **Container Memory %** — процент от лимита
- **Container Network RX MB/s** — входящий трафик
- **Container Network TX MB/s** — исходящий трафик

### 🔧 Go Runtime
- **Go Goroutines** — текущее количество goroutines
- **Go Threads** — OS threads
- **Go Heap Alloc MB** — аллоцированная heap память
- **Go GC Pause p95 ms** — пауза GC в миллисекундах
- **Process RSS MB** — resident set size процесса
- **Process Open FDs** — открытые file descriptors

---

## 🖥️ Host Infrastructure

### 💻 CPU & Memory
- **Host CPU Busy %** — процент занятого CPU (1 - idle / total)
- **CPU by Mode** — breakdown по режимам (user, system, idle, iowait...)
- **Memory Used GB** — используемая память (total - available)
- **Memory Available GB** — доступная память
- **Memory Cached GB** — кэшированная память
- **Swap Used GB** — используемый swap

### 💾 Disk & Network
- **Root FS Used GB** — занятое место на `/`
- **Root FS Free GB** — свободное место
- **Disk Reads ops/s** — операции чтения с диска
- **Disk Writes ops/s** — операции записи
- **Network RX MB/s (ens3)** — входящий трафик на ens3
- **Network TX MB/s (ens3)** — исходящий трафик
- **Network Errors (ens3)** — ошибки сети
- **Load Average** — load average 1/5/15m

---

## Экспортируемые метрики бота

Все метрики приложения `rs8kvn_bot` экспортируются через `/metrics` endpoint и агрегируются в Better Stack.

### Subscriptions
- `active_subscriptions` / `premium_subscriptions` / `free_subscriptions` / `trial_subscriptions` (gauges) — current state
- `subscription_creates_total` — successful first-time creations; process-local counter
- `subscription_reanimations_total` — revoked subscriptions restored to free; process-local counter
- `subscription_renewals_total` — paid renewal callbacks; process-local counter
- `subscription_sync_total` / `subscription_sync_duration_seconds` — worker runs/duration
- `subscription_expire_total` / `subscription_expire_duration_seconds` — worker runs/duration
- `subscription_reminder_runs_total` / `subscription_reminders_total{window,result}` — scans and send attempts
- `traffic_notification_runs_total` / `traffic_notifications_total{kind,result}` — scans and send attempts
- `bot_orphaned_clients_revoked_total` / `reconcile_orphaned_duration_seconds` — reconciliation outcome and duration

### Payments
- `payment_operations_total{operation, result}` (counter) — операции (request/confirm/cancel) × (success/error)
- `payment_amount_cents_total{operation, currency}` (counter) — суммы в центах (confirmed/chargeback) × (RUB/USD/EUR)
- `payment_operation_duration_seconds` (histogram) — latency операций
- `payment_issues_total{event}` (counter) — issues по событиям

### HTTP API
- `http_requests_total{path, status}` (counter) — HTTP запросы
- `http_request_duration_seconds{path}` (histogram) — latency

### Telegram Bot
- `bot_updates_total{command,result}` (counter) — updates by command and outcome
- `bot_update_duration_seconds{command}` (histogram) — latency
- `telegram_api_calls_total{method,result}` (counter) — Telegram API calls; currently instrumented sends only
- `telegram_api_duration_seconds{method}` (histogram) — latency for instrumented API calls

### 3x-ui VPN Panel
- `xui_requests_total{operation,result,node}` (counter) — requests to panel by configured VPN node
- `xui_request_duration_seconds{operation,node}` (histogram) — latency by node

### Subserver
- `subserver_source_fetch_total{result,format}` (counter) — source fetches
- `subserver_source_fetch_duration_seconds{result}` (histogram) — fetch latency
- `subserver_cache_invalidations_total{reason}` (counter) — invalidations
- `subserver_no_items_total` (counter) — cases with no items

### Database
- `db_queries_total{operation,result}` (counter) — DB operations
- `db_query_duration_seconds{operation}` (histogram) — query latency
- `db_pool_wait` (gauge) — cumulative connection-pool waits since process start
- `db_pool_wait_duration_seconds` (gauge) — cumulative wait time, seconds, since process start. Not exported by the captured production build yet; chart becomes populated after rollout.

### Cache
- `cache_hits_total{cache}` / `cache_misses_total{cache}` — `subscription`, `subserver`

### Go Runtime (стандартные метрики)
- `go_goroutines` (gauge)
- `go_threads` (gauge)
- `go_memstats_heap_alloc_bytes` (gauge)
- `go_gc_duration_seconds` (summary)
- `process_resident_memory_bytes` (gauge)
- `process_open_fds` (gauge)

### Container & Host (от node_exporter и cAdvisor через vector)
- `container_cpu_usage_seconds_total`
- `container_memory_working_set_bytes`
- `container_spec_memory_limit_bytes`
- `container_network_receive_bytes_total`
- `container_network_transmit_bytes_total`
- `node_cpu_seconds_total{mode}`
- `node_memory_MemTotal_bytes`, `node_memory_MemAvailable_bytes`, `node_memory_Cached_bytes`
- `node_memory_SwapTotal_bytes`, `node_memory_SwapFree_bytes`
- `node_filesystem_size_bytes{mountpoint}`, `node_filesystem_free_bytes{mountpoint}`
- `node_disk_reads_completed_total`, `node_disk_writes_completed_total`
- `node_network_receive_bytes_total{device}`, `node_network_transmit_bytes_total{device}`
- `node_network_receive_errs_total{device}`, `node_network_transmit_errs_total{device}`
- `node_load1`, `node_load5`, `node_load15`

---

## Мультисерверная поддержка

Приложение XUI экспортирует `node=<database node name>` для разбивки запросов по VPN-нодам. Это не метка bot-host: несколько bot instances потребуют отдельной stable `host`/`service` resource label в collector configuration.

Метрики хоста и контейнеров разбиваются по labels, которые поступают от их exporters. Не предполагай `label('service')` на источнике, если exporter его не отправляет.

Для второго сервера добавь его Better Stack source, затем строй агрегаты по выбранным sources или breakdown по фактически присутствующим exporter labels.

---

## История изменений

### 2026-10-26: Полная реорганизация
- Создана новая структура из 7 дашбордов
- Разделение по доменам: Overview, Subscriptions, Payments, APIs, Database, Container, Host
- Добавлена детальная финансовая аналитика (Revenue Analytics, Operations & Performance)
- Поддержка мультисерверности через labels
- Удалены дублирующиеся графики
- Удалены старые дашборды (IDs: 1164939, 1164943-1164948)
- Удалены пустые RUM дашборды (Web vitals, Website analytics)

### 2026-10-26: Аудит метрик и исправление графиков
- Исправлены запросы Better Stack по labels/result и cumulative counter semantics; удалены дубли cache charts.
- Добавлен Bot Activity dashboard (1165167).
- Добавлены WAL + малый connection pool, графики pool waits и cache labels.
- Добавлены node labels для XUI request counts/latencies; видимость появится после deployment.

### Старые дашборды (удалены)
- Host + Docker — обзор (1165002)
- Host (Vector) (942771)
- Docker (942772)
- rs8kvn_bot — обзор (1164980)
- rs8kvn_bot — API и интеграции (1164981)
- rs8kvn_bot — платежи (1164982)
- rs8kvn_bot — runtime и инфраструктура (1164983)
