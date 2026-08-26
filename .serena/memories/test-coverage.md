# Test Coverage Analysis — rs8kvn_bot

**Последняя проверка:** 2026-08-21
**Метод:** `go test -coverprofile=/tmp/coverage.out ./...` и `go tool cover -func=/tmp/coverage.out`
**Общее покрытие:** 66.2% в последнем запуске

Пакетные значения ниже — исторический снимок от 2026-06-26, а не актуальный CI-отчёт. Для текущих значений всегда использовать команду выше.

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

## Количество тестовых файлов

Количество тестовых файлов меняется вместе с кодом; не использовать историческое число ниже как текущую метрику.

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
- (2026-08-11) Тесты `GetActiveByPlanID`, `CreateOrder`, `GetOrdersBySubscriptionID` удалены вместе с мёртвыми методами (см. orders-table.md)

## Добавленные тесты (2026-07-17, метрики)
- `internal/metrics/metrics_test.go` — проверка инициализации всех метрик, smoke-test endpoint `/metrics`
- `internal/metrics/db_test.go` — 4 теста: GORM callbacks для create/query/update/delete

## Добавленные тесты (2026-08-26, ветка feat/broadcast-campaigns)
- `internal/bot/broadcast_admin_flow_test.go` — parseBroadcastCallbackID (0/overflow/нечисловой), callbacks broadcast_cancel_/broadcast_retry_ (+не-админ), handleBroadcastFilter (plan/status/date/inactive/ever_paid + toggle off), handleBroadcastBackToFilters, форматтеры truncateRunes/formatIDList/formatErrorList
- `internal/xui/reset_traffic_test.go` — ResetTraffic: пустой email, успех (метод/путь/экранирование), Success:false с msg панели, невалидный JSON, не-2xx
- `internal/service/sync_test.go` — processPendingUpdate вызывает ResetTraffic при смене тарифа; сбой ResetTraffic — best-effort (нода всё равно active)
- `internal/service/subscription_test.go` — BindTrial rollback при DB-bind failure (rollback и conservative skip), CleanupExpiredTrials deprovision-failure оставляет строку, ReconcileOrphanedClients пересоздаёт node queue
