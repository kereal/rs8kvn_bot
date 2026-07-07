# Fix: Удалён мёртвый XUI health-check из бота

## Что было
- `cmd/bot/main.go` регистрировал `webServer.RegisterChecker("xui", ...)`, который брал
  **первый** клиент из `xuiClients map[uint]interfaces.XUIClient` через `for/break` и пинговал
  только его. Порядок обхода map в Go рандомизирован → статус недетерминирован, остальные ноды
  не проверялись.
- Бизнес-контекст: `/healthz` отдаёт HTTP 200 даже при `degraded` (`internal/web/web.go:307-315`,
  только `StatusDown` → 503). Выяснили, что поле `status`/`degraded` по XUI **никем не читается**
  (нет мониторинга/алертинга), поэтому чекер — мёртвый груз.

## Что сделано (2026-07-07)
- Удалён блок `RegisterChecker("xui", ...)` целиком (`cmd/bot/main.go`).
- Удалён теперь-неиспользуемый параметр `xuiClients map[uint]interfaces.XUIClient` из
  `startWebServer(...)` и аргумент `deps.xuiClients` из вызова.
- Сам `deps.xuiClients` оставлен — он нужен для `client.Close()` в defer.
- `build` и `go vet ./cmd/bot/` проходят чисто.
- Обновлены доки: `doc/api.md`, `doc/operations.md`, `doc/handover.md`, `README.md`
  (убраны упоминания XUI-компонента health-check; `/healthz` теперь только DB ping).

## Решение
Править логику чекера (проверка всех нод) не стали — YAGNI, статус никто не потребляет.
Если позже появится реальный мониторинг/алертинг по нодам, добавить корректный per-node чек
с привязкой к алертингу, а не агрегатный флаг.
