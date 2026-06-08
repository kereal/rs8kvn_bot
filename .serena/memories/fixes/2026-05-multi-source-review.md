# Multi-source 3x-ui migration review (25-26 May 2026)

Два связанных исправления в рамках перехода на `feature/sources-table`.

## #1: `ReconcileOrphanedClients` не должен удалять чужие trial-rows (25 May)

**Проблема:** `ReconcileOrphanedClients` вызывал `DeleteSubscription(ctx, 0)` для trial-подписок (TelegramID=0). Это удаляло ВСЕ trial-rows (где `telegram_id=0`), не только orphaned.

**Fix:** заменено на `DeleteSubscriptionByID(sub.ID)` — удаляет конкретный sub (trial или normal). Cache invalidation только для реальных TGID. Добавлена метрика `bot_orphaned_clients_removed_total`.

**Тесты:**
- `TestSubscriptionService_ReconcileOrphanedClients_RemovesMissing` — удаляет 1 normal + 1 trial через DeleteByID, проверяет count=2, cache invalidated только для normal.
- `TestSubscriptionService_ReconcileOrphanedClients_NoActive` — 0 deletions edge case.

## #2: `MockDatabaseService` incomplete (25 May)

**Проблема:** после добавления trial/referral методов в `interfaces.SubscriptionDatabase` mock в `testutil` не был обновлён — `go build` сломался.

**Fix:** добавлены отсутствующие `*Func` поля. Билд и тесты снова зелёные.

## #3: Sources validation потеряна при миграции (26 May, HIGH)

**Проблема:** при переходе `XUIHost/XUIAPIToken/XUIInboundID` → `Sources []Source` была удалена валидация в `config.validate()`. Тесты `EmptyXUIHost`/`EmptyXUIAPIToken`/`InvalidInboundID_Zero` падали.

**Fix:** добавлена per-source валидация (`config.go:271-284`). 49+ тестов в `internal/config/...` зелёные.

## #4: Дубль assert + переименование теста (26 May, MEDIUM)

- Удалён дубль `assert.NotEmpty(sub.SubscriptionID)` в `subscription_crud_test.go:51`.
- Тест `TestE2E_CreateSubscription_SubscriptionURLFormat` → `TestE2E_CreateSubscription_SubscriptionID_Set` (URL больше не часть контракта).

## #5: Мёртвый параметр xuiClient (26 May, LOW)

Удалён из структуры `Server`, сигнатуры `NewServer()` и 160+ call sites (8 файлов). Параметр всегда передавался `nil` и не использовался.

## Verification
- `go build ./...` — clean
- `go vet ./...` — clean (кроме pre-existing в `web_invite_test.go`)
- `go test ./internal/config/...` — все 49+ тестов ✅
- `go test -count=1 -short ./internal/service/... ./internal/database/...` — 116+ тестов ✅

## Не вошло в этот PR
- Массивный рефактор `web.go` (1349 строк) — не трогали. Suggestion по разделению — для будущих PR.
- Legacy bootstrap в `runMigrations` и race в `GetOrCreateInvite` — оставлены (защищены unique index из миграции 004, early-return убран в предыдущей итерации).
- `KeyboardBuilder.FromConfig()` — остаётся мёртвым (см. `audit/2026-05-pre-release`).
