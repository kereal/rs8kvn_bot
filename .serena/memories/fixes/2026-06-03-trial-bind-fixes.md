# Fix: Trial bind — propagate updates to all sources, aggregate errors, prevent double-activation race

## Что было сломано
- **#6 (LOW):** `service.SubscriptionService.BindTrial` после успешного `UpdateClient` на одном trial-источнике
  делал `break` — остальные trial-источники оставались со старым email "trial_<subID>" и
  лимитами trial-плана. `ReconcileOrphanedClients` мог позже удалить бинденный sub.
- **#5 (MEDIUM):** `CreateTrial` агрегировал только **первую** XUI-ошибку (`firstErr`), остальные
  шли в `logger.Warn` без сводки. При all-fail возвращал обёрнутую первую ошибку. При partial
  success пользователь получал trial-подписку, но фактически с частично неработающими источниками,
  и это никак не логировалось на уровне Error.
- **#7 (LOW):** Гонка `Create` vs `BindTrial`. Сценарий:
  1. Web-биндинг trials: trial-row обновляется на `telegram_id=A, plan_id=free` атомарно через
     `RowsAffected`.
  2. Параллельно `service.Create` для `telegram_id=A` через `db.CreateSubscription` делает
     `revoke всех active subs для A` + insert нового.
  3. Если Create выигрывает гонку: он ревоукит только что забинденный sub, вставляет новый.
     BindTrial потом пытается обновить свой ряд (id=other, telegram_id=0, plan_id=trial) — это
     не ревоучит sub из Create. У пользователя оказывается ДВЕ active подписки.

## Что починено

### #6 BindTrial обновляет ВСЕ trial-источники
`internal/service/subscription.go` (BindTrial) — убран `break` после успешного `UpdateClient`.
Цикл теперь проходит по всем `trialSources`. Идемпотентно: каждый Update использует один и тот
же `currentEmail` (trial email), `sub.ClientID`, `sub.SubscriptionID`, `sub.Username`.

### #5 CreateTrial агрегированные ошибки
`internal/service/subscription.go:399-421` — заменена `firstErr` на `xuiErrs []error`.
- В цикле по sources: на ошибке `append(xuiErrs, err)`, на успехе — счётчик.
- После цикла: `logger.Warn("trial XUI partial failure", zap.Int("succeeded", n), zap.Int("failed", len(xuiErrs)))`
  при partial success.
- При all-fail (`!anySuccess && len(xuiErrs) > 0`): `logger.Error("trial XUI all sources failed", ...)`
  и возврат `errors.Join(xuiErrs...)` с обёрткой.
- `deleteClientFromAllSources` на rollback остаётся best-effort (logger.Warn per source) — это
  не часть основного пути ошибок.

### #7 BindTrialSubscription revoke other active subs
`internal/database/database.go:BindTrialSubscription` — после успешного UPDATE trial-row
добавлен revoke всех остальных active subs с тем же `telegram_id` (кроме только что
забинденной). Это гарантирует, что после BindTrial у пользователя ровно ОДНА active
подписка — та, что была только что забиндена. Делается в той же транзакции, атомарно.

```go
result := tx.Model(&Subscription{}).
    Where("id = ? AND telegram_id = ? AND plan_id = ?", sub.ID, 0, planID).
    Updates(map[string]interface{}{...})
if result.RowsAffected == 0 { return ErrAlreadyActivated }
if err := tx.Model(&Subscription{}).
    Where("telegram_id = ? AND status = ? AND id <> ?", telegramID, "active", sub.ID).
    Update("status", "revoked").Error; err != nil {
    return err
}
return nil
```

## Тесты

### `internal/service/subscription_test.go`
- **`TestSubscriptionService_BindTrial_UpdatesAllSources`** — 2 trial sources, первый fails, второй
  succeeds. Проверяется: `xuiClient.UpdateClient` вызван 2 раза (counter==2).
- **`TestSubscriptionService_CreateTrial_PartialFailuresSucceed`** — 3 sources, 2 fail, 1 success.
  Проверяется: `result != nil` (есть успех), `SubID`/`ClientID` сгенерированы.
- **`TestSubscriptionService_CreateTrial_AllFailsAggregatesErrors`** — 2 sources, оба fail.
  Проверяется: `err != nil`, `errors.Is(err, errA) && errors.Is(err, errB)`.

### `internal/database/database_test.go`
- **`TestService_BindTrialSubscription_RevokesExistingActiveSub`** — создаётся active sub
  для `telegram_id=12345` с `plan_id=free` напрямую в БД. Затем создаётся trial sub и
  выполняется `BindTrialSubscription`. Проверяется: первая sub переведена в `revoked`,
  trial-bound sub — `active` с правильным `telegram_id`.

## Gotcha для тестов
`s.trialSources(ctx)` вызывает `s.db.GetSourcesByPlanName(ctx, "trial")`. Default mock
возвращает один source — это **не совпадает** с `sources` параметром `NewSubscriptionService`.
Поэтому в новых тестах обязательно `GetSourcesByPlanNameFunc` на mock:
```go
GetSourcesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Source, error) {
    if planName == database.TrialPlanName {
        return []database.Source{{ID: 1, Active: true, ...}, {ID: 2, ...}}, nil
    }
    return nil, nil
},
```

## Verification
- `go test -count=1 -short -timeout 120s ./...` — все 22 пакета OK
- `go test -count=1 -short -race -timeout 180s ./internal/{service,database}` — OK
- `go vet ./...` — clean

## Files изменены
- internal/service/subscription.go (фиксы #5, #6)
- internal/database/database.go (фикс #7)
- internal/service/subscription_test.go (3 новых теста)
- internal/database/database_test.go (1 новый тест)
