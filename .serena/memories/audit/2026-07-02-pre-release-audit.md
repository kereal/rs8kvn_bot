# Аудит ветки plans_and_pricing перед релизом (2026-07-02)

## Состояние ветки
- 78 коммитов впереди `dev`, 0 позади (fast-forward merge возможен)
- 153 файла изменено (+13208 / -9492), из них 98 Go-файлов
- Это мажорный рефакторинг v3.0.0

## Проверки готовности
- ✅ `go build ./...` — OK
- ✅ `go vet ./...` — OK
- ✅ `go test ./...` — 20 пакетов OK, 2 без тестов
- ✅ `go test -race` (scheduler, service, database, bot, subserver, web) — OK
- ✅ E2E тесты — OK
- ✅ Миграции БД (021–027) — OK
- ✅ `gofmt -l` — все файлы отформатированы
- ⚠️ golangci-lint: 139 issues, из них в prod-коде устранены contextcheck/nosprintfhostport/unused/staticcheck/whitespace; nilerr (2) — false positives (ожидаемые бизнес-состояния); остаток — в тестах (tparallel, errcheck)

## Что было исправлено в рамках аудита
1. **gofmt** — отформатировано 7 файлов (models.go, sync.go, handlers_test.go, subscription_expire_worker_test.go, subscription_test.go, subserver_test.go, servers.go)
2. **Удалён unused `excludedHeaders`** из `internal/subserver/servers.go` — был мёртвым кодом, дублировал локальный `excluded` map внутри `FilterHeaders` (subscription_helpers.go:13-17)
3. **Удалена unused `newTestSubServiceForSyncWorker`** + неиспользуемый импорт `config` из `subscription_sync_worker_test.go`
4. **nosprintfhostport** — `fmt.Sprintf("tuic://%s:%d", ...)` → `net.JoinHostPort` в servers.go
5. **contextcheck (3)** — handler.go: проброс `ctx` вместо `context.Background()`; access_log.go и web.go: убраны nil-guards `ctx == nil → context.Background()` (вызовы всегда передают не-nil ctx)
6. **staticcheck QF1001** — De Morgan в format.go: `!(r >= '0' && r <= '9')` → `r < '0' || r > '9'`
7. **staticcheck ST1023** — `var response http.ResponseWriter = w` → `var response = w` в web.go
8. **whitespace** — unnecessary leading newline в subscription.go GetWithTraffic
9. **Документация** — обновлены architecture.md, handover.md, security.md: убраны устаревшие ссылки на WebhookSender, proxy_manager_webhook; XUI_API_TOKEN сохранён; Telegram deleteWebhook сохранён

## excludedHeaders и FilterHeaders — дубликат устранён
`var excludedHeaders` в servers.go был **неиспользуемой package-level переменной** — дубликатом локального map внутри `FilterHeaders`. Реальная фильтрация заголовков `x-forwarded-proto`, `x-forwarded-for`, `x-real-ip` происходит в `FilterHeaders` (subscription_helpers.go:11-29), который вызывается из `web.go:615` перед сохранением в колонку `devices`. Удаление не сломало фильтрацию.

## Колонка devices
JSON-массив `[]map[string]string` — заголовки HTTP-запроса клиента (lowercased key→value) + `timestamp`. Пример:
```json
[{"x-hwid":"device-abc","user-agent":"v2rayng","timestamp":"2025-01-01T00:00:00Z"}]
```
Цепочка: `web.go:615 FilterHeaders` → `HandleSubscription` → `UpdateDevices` (subscription_handler.go:251) → `db.UpdateDevices`. Лимит — `Plan.DevicesLimit`. Rotate: существующая запись с тем же `x-hwid` заменяется.

## XUI_INBOUND_ID (singular) vs XUI_INBOUND_IDS (plural)
- `XUI_INBOUND_ID` (singular, integer) — env-переменная, используется **только** в `cmd/bot/main.go:409` для первичного seeding пустой таблицы nodes
- `XUIInboundIDs` (plural, JSON array `[1]`) — поле `config.Node`, загружается из БД; валидируется в `config.go:255-264`
- `.env.example:6-7` корректно документирует: "ONLY used for initial seed when sources table is empty"
- Не ломается и не мешает; в след.релизе будет удалено

## Не сделано (по решению)
- CHANGELOG не создан
- nilerr (2) — false positives, не трогать
- Тестовые lint issues (tparallel, errcheck) — не блокируют
- task-bot-integration.md — не трогать (согласно AGENTS.md)
