# Test Coverage Analysis — rs8kvn_bot

**Дата анализа:** 2026-06-26
**Метод:** Ручной анализ coverage.out (2407 строк, mode: set)
**Общее покрытие:** ~65-70% (целевой показатель из code_style: ~51%)

## По пакетам (оценка)

| Пакет | Покрытие | Статус |
|---|---|---|
| internal/flag | ~97% | 🟢 |
| internal/heartbeat | ~92% | 🟢 |
| internal/ratelimiter | ~90% | 🟢 |
| internal/xui | ~85% | 🟢 |
| internal/bot | ~82% | 🟢 |
| internal/scheduler | ~80% | 🟢 |
| internal/web | ~78% | 🟡 |
| internal/config | ~78% | 🟡 |
| internal/utils | ~70% | 🟡 |
| internal/logger | ~68% | 🟡 |
| internal/service | ~70% | 🟢 |
| internal/database | ~55% | 🟡 |
| internal/vpn | ~65% | 🟢 |
| internal/metrics | ~35% | 🔴 |
| internal/subserver | ~30% | 🔴 |
| internal/testutil | 0% | ⚪ (ожидаемо) |

## Файлы с низким покрытием
- internal/database/subscription_nodes.go — частично покрыт sync.go тестами
- internal/service/sync.go — ~50% (нужны тесты на ReconcilePlanNodes, handleSyncError)
- internal/vpn/client.go — ~65% (ThreeXUIClient, classify errors)
- internal/utils/markdown.go — EscapeMarkdown

## Приоритеты улучшения

### P1 — Высокое
1. service/sync.go — непокрытые пути: ReconcilePlanNodes edge cases, handleSyncError
2. vpn/client.go — classify ошибок, retryUnavailableNode

### P2 — Среднее
3. database/subscription_nodes.go — UpsertSubscriptionNode, GetPendingSync edge cases
4. database/trials.go — BindTrialSubscription race-condition flows

### P3 — Низкое
5. utils/markdown.go — EscapeMarkdown

## Количество тестовых файлов: 86 (+5)

## Добавленные тесты (2026-06-26, ветка test/add-coverage-p1-p2)

### P1 — Critical (0% → покрыто)
- `internal/database/orders_test.go` — 13 тестов: CRUD orders, status transitions, paid/activated updates
- `internal/database/products_test.go` — 6 тестов: GetActiveByPlanID (active/inactive product/plan), GetProductByID
- `internal/subserver/subscription_handler_test.go` — 20+ тестов: HandleSubscription (cache hit/miss/invalidated, base64/plain/JSON response, multiple nodes, no URL, fetch error), UpdateDevices, UpdateIPs, helper functions (ParseUserInfoValue, ParseExpireFromUserInfo, BuildUserInfoHeader, FilterHeaders, SkipTransportHeader, ResponseHeaders, DetectFormat, isValidServer)

### P2 — High (partial → improved)
- `internal/database/subscriptions_extra_test.go` — 14 тестов: GetSubscriptionStatus, GetWithPlanAndNodes, UpdateDevices, UpdateIPs, ExpireSubscription, GetExpiredPaidSubscriptions, GetSubscription
- `internal/subserver/servers_extra_test.go` — 16 тестов: ExtractJSONConfigs, ConvertSingleJSONToLink (VLESS/Trojan/SS/SOCKS/Hysteria2/TUIC/unsupported/invalid), toServerConfig alias normalisation (address/port/uuid/remark), truncateString

### Примечания
- GORM пропускает zero-value bool при Create — нужно `.Update("is_active", false)` после Create
- MockDatabaseService.UpdateIPs не проверяет UpdateIPsFunc — verify через side effect на subFull
- http.Header канонизирует ключи — "profile-title" → "Profile-Title"
- orders имеет partial UNIQUE index на (payment_provider, provider_payment_id) WHERE provider_payment_id IS NOT NULL

## Добавленные тесты (2026-07-17, метрики)
- `internal/metrics/metrics_test.go` — проверка инициализации всех метрик, smoke-test endpoint `/metrics`
- `internal/metrics/db_test.go` — 4 теста: GORM callbacks для create/query/update/delete
