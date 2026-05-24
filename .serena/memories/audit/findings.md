# Code Audit Findings (May 2026)

## Critical Bugs Fixed
1. **`internal/bot/subscription.go:269`** — `notifyAdmin()` was called with `time.Time{}` instead of `result.Subscription.ExpiryTime`, showing "01.01.0001" in admin notification. Fixed by passing `result.Subscription.ExpiryTime`.
2. **`internal/service/subscription.go:196-199`** — `DeleteByID()` had `_ = inboundID` dead code and silently discarded XUI DeleteClient error. Fixed by adding logger.Error call.

## Pre-release Audit (24 May 2026, branch dev)

**Status:** Готов к релизу (с минорными замечаниями)

### Разрешено с момента предыдущего аудита
- Большинство "неиспользуемых" ключей сообщений: `MsgStartGreeting` реально используется в handler.go.
- `ReferralCache.Save()` — намеренный no-op, покрыт тестом `TestReferralCache_Save_IsNoOp` (часть контракта периодической синхронизации).
- Логирование ошибок в `Delete()` / `DeleteByID()`: обе функции теперь логируют ошибки XUI (в DeleteByID добавлен logger.Error). Незначительная разница — в одном есть zap.Stack.
- Критическая silent-ошибка в DeleteByID исправлена.

### Оставшиеся минорные проблемы (не блокеры)
- `KeyboardBuilder.FromConfig()` — мёртвый код (определён в keyboard_builder.go:152, но не вызывается нигде).
- Несколько ключей сообщений всё ещё не используются (MsgAdminDelUsage, MsgErrClientExists и часть из оригинальных 14). Код использует inline-строки или другие пути.
- Покрытие тестами: 75.4% (internal, -short режим) против целевых ~85% в test-info. Часть новых/рефакторенных путей и skipped slow-тестов снижают цифру. Критические пути покрыты хорошо (1450+ тестов прошли).
- Один коммит с неанглийским сообщением: "269cc34 что-то про метрики".

### Положительные результаты полной проверки dev
- 0 TODO/FIXME/XXX/HACK в *.go файлах.
- Нет закоммиченных секретов (.env.example только).
- `go build` + `go vet` — чисто.
- golangci-lint установлен, ошибок не выявлено (через proxy).
- 1450 тестов в short-режиме прошли без ошибок и race conditions.
- Крупный рефактор (P1-1 handler decomposition) + исправления стабильности (гонки, утечки горутин, singleflight, метрики, broadcast, контексты) завершены.
- CHANGELOG уже описывает remediation P1-1..P1-4.

### Рекомендации перед релизом
- Обновить CHANGELOG: превратить [Unreleased] в [2.3.0] - YYYY-MM-DD.
- Рекомендуемая версия: **v2.3.0** (значительный рефактор + десятки исправлений стабильности, не патч).
- Workflow: PR dev → main → тег v2.3.0 на main.
- Перед тегом: прогнать полные тесты без -short + e2e (если есть время), обновить версии в docs/installation.md и memories при необходимости.
- После релиза: обновить памяти (architecture, project_overview, audit) на v2.3.0.

## Other Issues Found (not fixed)
- `KeyboardBuilder.FromConfig()` unused (мёртвый код)
- Несколько неиспользуемых ключей в messages.go (MsgAdminDelUsage, MsgErrClientExists и др.)
- Покрытие 75.4% (ниже цели 85% из-за -short и рефакторинга)
