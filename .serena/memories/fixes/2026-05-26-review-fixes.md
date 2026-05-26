# Review Fixes — feature/sources-table branch (26.05.2026)

## Проблемы и исправления

### HIGH — Сломанные тесты валидации + потеря валидации Sources
- **Причина:** При переходе от единичного XUI к `Sources []Source` была удалена проверка `XUIHost`, `XUIAPIToken`, `XUIInboundID` в `config.validate()`
- **Исправление:** Добавлена валидация для каждого source (`config.go:271-284`). Тесты `TestConfig_Validate_EmptyXUIHost`, `EmptyXUIAPIToken`, `InvalidInboundID_Zero` теперь проходят

### MEDIUM — Дублирующийся assert
- `subscription_crud_test.go:51` — удалён дубль `assert.NotEmpty(sub.SubscriptionID)`
- Тест `TestE2E_CreateSubscription_SubscriptionURLFormat` переименован в `TestE2E_CreateSubscription_SubscriptionID_Set` (больше не проверяет URL)

### LOW — Мёртвый параметр xuiClient
- Удалён из структуры `Server`, сигнатуры `NewServer()` и всех 160+ call sites (8 файлов)
- Параметр всегда передавался как `nil` и не использовался

## Статус
- `go build ./...` — ✅
- `go vet ./...` — ✅ (кроме pre-existing ошибок в web_invite_test.go)
- `go test ./internal/config/...` — все 49+ тестов ✅
