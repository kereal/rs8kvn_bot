# Метрики Prometheus — текущее состояние

## Полная ссылка

**Актуальная документация всех метрик**: `doc/betterstack-metrics-reference.md`

Там:
- Полный инвентарь 40+ application метрик (bot, HTTP, DB, cache, payments, subscriptions, workers)
- Docker container метрики (CPU, memory, network, disk I/O)
- Host infrastructure метрики (CPU, RAM, disk, network, uptime)
- Query patterns для counters/gauges/histograms/money
- Инвентарь и query patterns: `doc/betterstack-metrics-reference.md`; 7 telemetry dashboards плюс `Bot Activity`.

## Ключевые точки

- `active_subscriptions`, `premium_subscriptions`, `free_subscriptions`, `trial_subscriptions` — gauges, обновляются через `SubscriptionService.RefreshActiveSubscriptionsMetric()` после мутаций
- `subscription_creates_total` — process-local first-time creations; `subscription_reanimations_total` — revoked→free recovery; `subscription_renewals_total` — paid renewals confirmed through callback
- Воркеры: `subscription_sync_total/duration`, `subscription_expire_total/duration`, `reconcile_orphaned_duration`, `subscription_reminder*`, `traffic_notification*`
- `bot_orphaned_clients_revoked_total` — orphan subscriptions revoked (не deleted)

### Database & Cache
- `db_queries_total{operation,result}`, `db_query_duration_seconds{operation}` — GORM callbacks в `internal/metrics/db.go`
- `cache_hits_total{cache}`, `cache_misses_total{cache}` — кэши `subscription`, `subserver`; `referral` не используется

### Payments (финансовая аналитика)
- `payment_operations_total{operation,result}` — counter операций: request/confirm/cancel × success/error
- `payment_operation_duration_seconds{operation}` — histogram latencies
- **`payment_amount_cents_total{operation,currency}`** — **КРИТИЧНО**: counter **суммы в центах**, не количество!
  - `operation`: `confirmed` (успешный CONFIRMED, только Activated), `chargeback` (CHARGEBACKED)
  - `currency`: RUB, USD, EUR (ISO 4217)
  - Query pattern: `derivativeMerge(rate_avg)` для rate cents/sec ИЛИ `sum(avgMerge(value_avg)) / 100` для total amount за период
- `payment_issues_total{event}` — операционные проблемы (те же события → админу в Telegram)

### APIs & Integrations
- HTTP API: `http_requests_total{method,path,status}`, `http_request_duration_seconds{method,path}`
- Telegram Bot: `bot_updates_total{command,result}`, `bot_update_duration_seconds{command}`, `telegram_api_calls_total{method,result}`, `telegram_api_duration_seconds{method}`
- 3x-ui panel: `xui_requests_total{operation,result,node}`, `xui_request_duration_seconds{operation,node}`; `node` is configured VPN node name
- Subserver: `subserver_source_fetch_total{result,format}`, `subserver_source_fetch_duration_seconds{result}`, `subserver_cache_invalidations_total{reason}`, `subserver_no_items_total`

- Better Stack: 7 telemetry/domain dashboards (IDs 1165127–1165134 excluding unused 1165130) plus `Bot Activity` (1165167); process-local counters are not durable history.
- Process-local counters reset on restart and are not durable billing/audit source.

1. **🎯 Overview** — критические KPI, быстрый снимок системы
2. **📊 Subscriptions & Lifecycle** — полная аналитика подписок, воркеры
3. **💰 Payments & Revenue** — платежи, деньги, финансовая аналитика
4. **🔌 APIs & Integrations** — HTTP API, Telegram Bot, 3x-ui, subserver
5. **🗄️ Database & Cache** — SQLite, latencies, pool, кэши
6. **🐳 Container & Runtime** — Go runtime, Docker контейнер
7. **🖥️ Host Infrastructure** — хост CPU/RAM/disk/network

Все дашборды готовы к мультисерверности (группировка по `label('host')` или `label('container_name')`).
