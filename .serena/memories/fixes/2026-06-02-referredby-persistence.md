# Fix: ReferredBy/InviteCode теперь персистятся в БД для обычных подписок

## Что было сломано
- `bot/subscription_handler.createSubscription` читал `pendingInvites[chatID]` ПОСЛЕ `service.Create`
  и мутировал `result.Subscription.ReferredBy` в памяти, не вызывая `UpdateSubscription`.
- `service.Create` не передавал inviteCode в `db.CreateSubscription`, поэтому `sub.InviteCode` и
  `sub.ReferredBy` оставались пустыми в БД.
- Существующий тест `TestCreateSubscription_WithPendingInvite` проходил ложноположительно:
  mock-метод хранил указатель sub, и handler мутировал тот же объект после возврата из mock.
- Следствие: `GetAllReferralCounts` (читает `referred_by` из БД) возвращал 0 для рефереров,
  счётчики рефералов расходились, `GetAllReferralCounts`-кеш терял данные.

## Что починено
1. `interfaces.SubscriptionRepository.CreateSubscription(ctx, sub, inviteCode string) error` —
   добавлен параметр inviteCode.
2. `database.Service.CreateSubscription` — внутри транзакции (revoke старых + insert нового)
   резолвит invite через `tx.Where("code = ?", inviteCode).First(&inv)`. Если найден — заполняет
   `sub.InviteCode` и `sub.ReferredBy` ДО `tx.Create(sub)`. Несуществующий код не фатален.
3. `service.Create(ctx, chatID, username, inviteCode)` — пробрасывает inviteCode в db.
   `CreateResult.ReferrerTGID` — резолвленный referrer для handler-а (0 = не было).
4. `bot/subscription_handler.createSubscription`:
   - Захватывает `inviteCode` под коротким `pendingMu.Lock()` ДО service.Create.
   - `pendingMu` НЕ держится на время HTTP/БД-вызовов (фикс #3 — избыточная блокировка).
   - После успешного Create — `delete pendingInvites[chatID]` под коротким lock.
   - `IncrementReferralCount(result.ReferrerTGID)` — после, на основании `result.ReferrerTGID`.
5. `testutil.MockDatabaseService.CreateSubscription` — добавлен параметр `inviteCode` в
   сигнатуру func-поля и метода. Default-реализация делает deep-copy `*sub` в `m.Subscriptions`
   (предыдущий ложноположительный тест-паттерн теперь невозможен).

## Новые/обновлённые тесты
- `service/subscription_test.go::TestSubscriptionService_Create_PropagatesInviteCodeToDB` — проверяет
  пробрасывание `inviteCode` параметра в `db.CreateSubscription`.
- `service/subscription_test.go::TestSubscriptionService_Create_EmptyInviteCodeIsNoop` — пустая
  строка проходит без изменений.
- `bot/subscription_test.go::TestCreateSubscription_WithPendingInvite` — переписан: mock
  симулирует production-поведение БД (выставляет InviteCode + ReferredBy по inviteCode).
  Проверяется: `savedSub.InviteCode == "ABC123"`, `savedSub.ReferredBy == 999999`,
  pendingInvites удалён, `handler.GetReferralCount(999999) == 1`.
- `bot/subscription_test.go::TestCreateSubscription_WithExpiredPendingInvite` — проверяет, что
  handler НЕ пробрасывает expired invite (inviteCode == "").
- `database_test.go::TestService_CreateSubscription_PersistsInviteCodeAndReferredBy` —
  интеграционный тест с реальной БД: invite создаётся через `GetOrCreateInvite`, sub создаётся
  с `CreateSubscription(ctx, sub, "REFER123")`, читается обратно — `InviteCode` и `ReferredBy`
  должны сохраниться. `GetReferralCount(777777) == 1`.
- `database_test.go::TestService_CreateSubscription_EmptyInviteCodeLeavesFieldsEmpty` — пустой
  код не заполняет поля.
- `database_test.go::TestService_CreateSubscription_UnknownInviteCodeDoesNotFail` —
  несуществующий код не ломает создание подписки.

## Совместимость
- Изменение `CreateSubscription` ломает все существующие mock-функции и вызовы — обновлено
  54 call sites: `internal/database/*_test.go`, `internal/scheduler/*_test.go`,
  `internal/bot/*_test.go`, `internal/service/*_test.go`, `internal/interfaces/*`,
  `internal/testutil/*`, `tests/leak/*`, `tests/e2e/*`. Везде добавлен `""` последним
  аргументом или параметром.
- В `service.Create` добавлен параметр `inviteCode string` — все вызовы в `tests/e2e/*`
  и `service/subscription_test.go` обновлены (37+ мест).

## Файлы изменены
- internal/interfaces/interfaces.go
- internal/database/database.go
- internal/testutil/testutil.go
- internal/service/subscription.go
- internal/service/subscription_test.go
- internal/bot/subscription_handler.go
- internal/bot/subscription_test.go
- internal/bot/callbacks_test.go
- internal/bot/database_test.go
- internal/bot/integration_test.go
- internal/database/database_test.go
- (sed bulk-update) tests/e2e/*_test.go
- tests/leak/leak_test.go

## Verification
- `go vet ./...` — clean
- `go test -count=1 -short -timeout 120s ./...` — все 22 пакета OK
- `go test -count=1 -short -race ./internal/bot/... ./internal/service/... ./internal/database/...` — OK
- `go test -count=3 -short ./internal/bot/` — OK (нет регрессий флаки)
- `golangci-lint` на изменённых файлах — 0 issues
- Pre-existing lint warnings (tparallel в subtests, gosec, errcheck) — не наши, не правил.
