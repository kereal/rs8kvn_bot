# fix: broadcast splitMessage entity-aware + raised draft limit (2026-07-19)

## Что изменено
- `internal/bot/admin.go`: `HandleBroadcastDraft` теперь принимает черновики до
  `maxBroadcastLen = config.MaxTelegramMessageLen * 20` (~81920). Старый гард
  `len(text) > MaxTelegramMessageLen` запрещал >4096, из-за чего ветка
  хард-сплита в `splitMessage` была недостижима (мёртвый код).
- `splitMessage(text, maxLen)` переписан entity-aware: режет на `\n` (перенос
  внутри открытой сущности оставляет `\n` внутри чанка, иначе сброс), затем на
  пробелах. Over-long токен режется по рунной границе (hardSplitToken) ТОЛЬКО
  если нет открытой сущности и токен не содержит delimiter (`* _ ` ~ [ ]`,
  проверка `containsEntityChar`). Иначе токен оставляется целиком — чанк может
  превысить maxLen, но MarkdownV2-сущность не ломается (такой ввод в любом
  случае непарсируем в MarkdownV2).
- Превью (`HandleBroadcastDraft`) и отправка (`runBroadcast`) оба вызывают
  `splitMessage(text, MaxTelegramMessageLen)` и шлют чанки по отдельности,
  каждый экранируется `utils.EscapeMarkdownV2`.

## Контракт (зафиксирован тестом)
- Ни один чанк не рвёт инлайн-сущность `*bold*`, `_italic_`, `~strike~`,
  `` `code` ``, `[text](url)`; многострочная открытая сущность сохраняется
  (перенос внутри).
- Обычные over-long токены без delimiter режутся по maxLen рун.
- Over-long токен с delimiter (напр. `a*`+60b) оставляется целиком — жертвуем
  форматированием, чтобы не сломать сущность.

## Тесты
- `internal/bot/split_message_test.go` (package bot, обычный build):
  `TestSplitMessage_EntitySafe`, `TestSplitMessage_NoEntitySplitAcrossChunks`.
  Заменяет мёртвый `admin_test.go:TestSplitMessage_DoesNotBreakMarkdownV2Entities`
  (тот в `package e2e` + `//go:build integration`, не собирается вместе с
  `package bot` в той же dir).
- `handlers_test.go:TestHandleBroadcast_MessageTooLong` обновлён: приём при
  `Max+1`, отказ только при `Max*20+1`.

## Регрессия устранена (2026-07-19, follow-up)
- `internal/bot/admin_test.go` был `package e2e` + `//go:build integration` в той
  же директории, что `package bot` → integration-сборка `internal/bot` падала
  (`found packages bot (admin.go) and e2e (admin_test.go)`). И `setupE2EEnv`/
  `e2eTestEnv` живут в `tests/e2e/e2e_test.go`, поэтому e2e-тесты в файле и так
  не собирались.
- Исправлено: файл переведён в `package bot`, все дублирующие `TestE2E_*` тесты
  удалены (их полные копии уже есть в `tests/e2e/admin_test.go`), оставлены
  только 2 теста на неэкспортируемые символы:
  - `TestSplitMessage_DoesNotBreakMarkdownV2Entities` — убран подтест
    `long_markdown_is_hard-split_safely`: нынешний entity-aware `splitMessage`
    намеренно оставляет over-long токен с delimiter (`a*`+N) целиком (чанк > maxLen
    — задокументированный trade-off из памяти строки 24-25), утверждение
    `len(chunk) ≤ maxLen` ему противоречит. Остальные 3 подтеста валидны.
  - `TestBroadcastSession_TTLExpiry` — переписан с `setupE2EEnv` на bot-уровневый
    `NewTestFixture` (есь в `integration_test.go`); использует неэкспортируемые
    `startBroadcastSession`/`getBroadcastSession`/`broadcastMu`/`broadcastSessions`/
    `broadcastSessionActive`.
- Проверено: `go vet -tags integration ./...` в `internal/bot` и `tests/e2e`
  проходят; оба теста проходят (`go test -tags integration -run ...`).
