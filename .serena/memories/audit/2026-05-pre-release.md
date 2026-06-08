# Pre-release Audit (24-26 May 2026, branch `dev`)

## Critical bugs (исправлены)
- `internal/bot/subscription.go:269` — `notifyAdmin()` показывал `01.01.0001` вместо реальной `ExpiryTime`. **Fix**: передаётся `result.Subscription.ExpiryTime`.
- `internal/service/subscription.go:196-199` — `DeleteByID()` молча игнорировал XUI DeleteClient error. **Fix**: добавлен `logger.Error`.
- `internal/bot/subscription.go` (`ReconcileOrphanedClients`) — вызывал `DeleteSubscription(ctx, 0)` для trial-подписок (TelegramID=0), что удаляло ВСЕ trial-rows. **Fix**: переход на `DeleteSubscriptionByID(sub.ID)`. Добавлена метрика `bot_orphaned_clients_removed_total`.

## Multi-source review (26 May 2026, branch `feature/sources-table`)
- **HIGH**: при миграции от единичного XUI к `Sources []Source` была потеряна валидация `XUIHost`/`XUIAPIToken`/`XUIInboundID` в `config.validate()`. **Fix**: добавлена per-source валидация. Тесты `EmptyXUIHost`/`EmptyXUIAPIToken`/`InvalidInboundID_Zero` теперь проходят.
- **MEDIUM**: дубль `assert.NotEmpty(sub.SubscriptionID)` в `subscription_crud_test.go:51` удалён. Тест переименован: `TestE2E_CreateSubscription_SubscriptionURLFormat` → `TestE2E_CreateSubscription_SubscriptionID_Set` (URL больше не проверяется).
- **LOW**: мёртвый параметр `xuiClient` удалён из структуры `Server`, сигнатуры `NewServer()` и 160+ call sites (8 файлов) — всегда передавался `nil` и не использовался.

## Pre-existing минорные проблемы (НЕ блокеры, на ваше усмотрение)
- `internal/bot/keyboard_builder.go:152` — `KeyboardBuilder.FromConfig()` определён, но **не вызывается нигде** (мёртвый код). Проверено: 0 references в `*.go`. **Решение**: оставить (возможно, будет использоваться в v2.5+) ИЛИ удалить.
- ~5-7 неиспользуемых ключей в `internal/bot/messages.go` (`MsgAdminDelUsage`, `MsgErrClientExists` и др.) — заменены inline-строками или другими путями.
- Один коммит с неанглийским сообщением: `269cc34` (про метрики).

## Положительные результаты
- 0 TODO/FIXME/XXX/HACK в `*.go`.
- 0 закоммиченных секретов (только `.env.example`).
- `go build` + `go vet` — clean.
- `golangci-lint` (через proxy) — 0 issues.
- 1450+ тестов в `-short` режиме прошли, 0 race conditions.
- P1-1..P1-4 (handler decomposition, race fixes, leak fixes, singleflight, metrics, broadcast, contexts) — все завершены.
- См. `doc/CHANGELOG.md` для полной истории.
