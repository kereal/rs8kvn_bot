# Fix: handleBindTrial did not increment referrer cache (stale up to 1h)

## Что было сломано
- `command.handleBindTrial` после успешного `BindTrial` (через `/start trial_{subID}`)
  правильно устанавливал `sub.ReferredBy` в БД (`BindTrialSubscription` транзакция),
  отправлял referrer'у уведомление "По вашей ссылке новый пользователь активировал
  подписку" — но **не вызывал** `h.IncrementReferralCount(referrer)`.
- Сравнение с `createSubscription` (subscription_handler.go:273): там
  `if result.ReferrerTGID > 0 { sh.h.IncrementReferralCount(...) }` — есть.
- `ReferralCache` обновляется вручную при `Create` (free-plan) и при decrement
  (admin remove); на trial bind пропускалось. Sync из БД раз в час — поэтому
  счётчик на `/invite` был stale до часа.

## Симптом
- Реферер создаёт invite, делится им в trial-формате.
- Другой пользователь кликает `/start trial_X` → подписка активируется, referrer
  получает уведомление "новый пользователь активировал" — но в `/invite` счётчик
  показывает старое значение. После 1h `ReferralCache.Sync` (раз в час) — обновляется.

## Что починено
`internal/bot/command.go` (handleBindTrial) — добавлен
`c.h.IncrementReferralCount(sub.ReferredBy)` ВНУТРИ блока `if sub.ReferredBy > 0`,
после отправки уведомления. БД остаётся source of truth, cache синхронизирован.

## Тесты
### `internal/bot/commands_test.go`
- **`TestHandleBindTrial_IncrementsReferrerCacheCount`** — bind с `ReferredBy=888777`.
  Проверяется: `handler.GetReferralCount(888777) == 1` после bind. **Failing до фикса,
  passing после.**
- **`TestHandleBindTrial_NoReferrerLeavesCacheUntouched`** — bind без referrer
  (`ReferredBy=0`, `InviteCode=""`, mock `GetInviteByCode → ErrInviteNotFound`).
  Проверяется: `referralCache.GetAll()` не содержит ненулевых count'ов.

## Гэп
`TestHandleBindTrial_WithReferrerNotification` (commands_test.go:402) проверял
отправку сообщений referrer'у, но не проверял cache count — поэтому баг жил
незамеченным. Добавлены отдельные тесты с явной проверкой `GetReferralCount`.

## Verification
- `go vet ./...` — clean
- `go test -count=1 -short -race -timeout=180s ./...` — 21/21 пакетов OK
- `go test -count=1 -run 'TestHandleBindTrial' ./internal/bot/` — 8/8 тестов OK

## Файлы изменены
- internal/bot/command.go (handleBindTrial: +1 строка)
- internal/bot/commands_test.go (2 новых теста)

## Не исправлено (вне scope)
- `handleBindTrial` не уведомляет admin при organic trial bind (sub.InviteCode="").
  Это feature decision, не bug — пропускаю.
- `service.Create` при `activeSources()` пуст → `firstErr == nil` → error
  `"failed to create client on any source: %!w(<nil>)"`. Cosmetic, LOW.
- Trial cleanup TOCTOU: `BindTrialSubscription` find → CleanupExpiredTrials
  delete race при истечении `TrialDurationHours`. Узкое окно, юзер получит
  "already activated" и может запросить trial снова. Не критично.
