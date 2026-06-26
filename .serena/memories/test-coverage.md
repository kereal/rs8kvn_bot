# Test Coverage Analysis — rs8kvn_bot

**Дата анализа:** 2026-06-26
**Метод:** Ручной анализ coverage.out (2407 строк, mode: set)
**Общее покрытие:** ~55-60% (целевой показатель из code_style: ~51%)

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
| internal/service | ~65% | 🟡 |
| internal/backup | ~55% | 🟡 |
| internal/database | ~45% | 🔴 |
| internal/metrics | ~35% | 🔴 |
| internal/subserver | ~30% | 🔴 |
| internal/testutil | 0% | ⚪ (ожидаемо) |

## Файлы с 0% покрытием (критические!)

- internal/subserver/subscription_handler.go (~80+ stmts) — ВЕСЬ хендлер подписок
- internal/subserver/subscription_helpers.go (~40+ stmts) — хелперы конвертации
- internal/subserver/subscription.go
- internal/database/orders.go — CRUD заказов
- internal/database/products.go — CRUD продуктов
- internal/utils/markdown.go — EscapeMarkdown

## Приоритеты улучшения

### P1 — Критическое
1. database/orders.go + products.go — нет НИ ОДНОГО теста
2. subserver/subscription_handler.go — самый большой пробел (~80 stmts)

### P2 — Высокое
3. subserver/servers.go — новые функции не покрыты
4. database/subscriptions.go — непокрытые ветки CreateSubscription, BindTrial
5. service/subscription.go — ветки ошибок с xui

### P3 — Среднее
6. utils/markdown.go — EscapeMarkdown
7. metrics/metrics.go — Prometheus-обёртки
8. database/models.go — новые модели SubscriptionNode

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
