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

## Предсуществующая регрессия (вне scope, НЕ чинил)
- `internal/bot/admin_test.go` — `package e2e` + `//go:build integration` в той
  же директории, что `package bot` → integration-сборка `internal/bot` падает
  (`found packages bot (admin.go) and e2e (admin_test.go)`). Введено коммитом
  `a75650a`. `setupE2EEnv`/`e2eTestEnv` живут в `tests/e2e/e2e_test.go`, поэтому
  e2e-тесты в `admin_test.go` не компилируются даже при integration-теге.
  Рекомендация: перенести e2e-тесты из `admin_test.go` в `tests/e2e/` или
  сделать файл `package bot` и заменить `setupE2EEnv` на bot-уровневый хелпер.
