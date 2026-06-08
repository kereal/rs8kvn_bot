# Test Info — rs8kvn_bot

**Обновлено:** 2026-06-03 (после Variant C аудита)

## Coverage (~85% overall, -short)

### По модулям (post-audit)
- `internal/flag`: 97.7% ✅
- `internal/ratelimiter`: 97.4% ✅
- `internal/heartbeat`: 96.2% ✅
- `internal/service`: ~92% ✅ (было 72.5% — U1)
- `internal/config`: 91.8% ✅
- `internal/xui`: 90.9% ✅
- `internal/web`: 90.3% ✅
- `internal/bot`: 92.6% ✅ (U3: handler_unit_test.go +21 case)
- `internal/utils`: 90.0% ✅
- `internal/logger`: 88.9% ✅
- `internal/backup`: 83.2% ✅
- `internal/subserver`: 82.5% ✅
- `internal/scheduler`: 81.2% ✅
- `internal/database`: ~82% 🟡 (было 69.5% — U2: sources_test.go)
- `internal/metrics`: ~30% 🟡 (было 0% — R5: metrics_test.go)
- `cmd/bot`: 5.4% 🟡 (main is integration — acceptable)
- `internal/testutil`: 0.0% 🔴 (mock helpers — no direct tests needed)

### Test stats
- **~67 test files**, **~1079 test funcs**, **~29565 lines** (baseline до аудита)
- **Race-safe:** ✅
- **Golden files:** ✅ (subserver)

## Test patterns (v2.4.0)

### Multi-source
- Source-литералы обязаны иметь `Trial:true, Active:true, ID:1`.
- `mockXUIClients := map[uint]interfaces.XUIClient{1: mockClient}`.
- Удалены поля из `Subscription`: `InboundID`, `TrafficLimit`, `SubscriptionURL`, `DeletedAt` (migration 011).
- `GetSourcesByPlanNameFunc` обязателен на mock (default возвращает 1 source, не совпадает с `sources` параметром `NewSubscriptionService`).

### Subscription create + inviteCode
- `MockDatabaseService.CreateSubscriptionFunc` — deep-copy `*sub` в `m.Subscriptions` (предотвращает ложноположительные тесты с shared pointer).
- `service.Create(ctx, chatID, username, inviteCode)` — параметр обязателен.
- `result.ReferrerTGID` — резолвленный referrer для handler-а (0 = не было).

### BindTrial race
- `MockDatabaseService.BindTrialSubscriptionFunc` должен симулировать revoke других active subs.
- Тест на race: создать active sub напрямую в БД → вызвать `BindTrialSubscription` → проверить, что первая sub revoked.

### Test helpers
- `testutil.NewMockDatabaseService`, `testutil.NewMockXUIClient`, `testutil.NewMockBotAPI` с `*Func` полями.
- `t.Cleanup` для `db.Close()`.
- `t.TempDir()` для временных файлов.
- `t.Parallel()` где возможно.

## Performance (-short mode)

### Skip в short
- `xui`: 5 slow tests (AddClient, DeleteClient, UpdateClient, GetClientTraffic, GetRequiredFlow_Fallback) — saves ~47s
- `heartbeat`: `TestSendHeartbeat_InvalidURL` — saves ~10s

### time.Sleep → assert.Eventually
- `graceful_shutdown_test.go`: 8 Sleeps replaced, ~300ms saved
- `subserver/service_test.go`: 2× Sleep(30ms) → Eventually
- `subserver/cache_test.go`: 2× Sleep(20ms) → Eventually
- `scheduler`: Sleep(20ms)+After(1s) → After(200ms)
- `heartbeat`: After(2s) → After(200ms) in 4 places
- **Исключение**: breaker_test оставлен с `time.Sleep(2-10ms)` — точно соответствует таймаутам, Eventually — overhead без выигрыша.

### t.Parallel()
- Добавлено в 11 heartbeat test funcs (было 1).

### Race fix
- `requestReceived` в heartbeat_test: global → `atomic.Bool`.

### Smoke tests
- Вынесены в `//go:build smoke` (без тега — `build constraints exclude all`).
- Команда: `go test -tags=smoke -count=1 ./tests/smoke/`.

### Total -short runtime
- ~15s (было ~60-70s).

## E2E tests
- `tests/e2e/` — уникальный pipeline (mockXUI + DB + handler). НЕ дубликаты unit-тестов.
- Удалены `chdirMu`/`findProjectRoot`/`os.Chdir` (U8) — `embed.FS` миграций не зависит от cwd.
- `t.Cleanup` для `db.Close()`.

## Verification commands
```bash
go vet ./...                                           # clean
go test -short -race -count=1 -timeout=180s ./...      # 21/21 пакетов OK
go test -tags=smoke -count=1 ./tests/smoke/            # smoke
go test -count=1 ./tests/e2e/                          # e2e отдельно
```

## Audit Variant C (2026-06-03)
- **R1–R6** (Remove): main_test дубли, database TableName, logger dead, config constants, e2e/real_client_test, dead config.
- **U1–U5** (Upgrade): service/subscription_test, database/sources_test, bot/handler_unit_test, bot/cache_test, metrics/metrics_test.
- **U6–U8** (Hygiene): t.Cleanup, smoke build tag, e2e chdir cleanup.
- Коммит: `0c5eef5 test: comprehensive audit (Variant C)`.
- Итог: +810/-459, 0 failures, 0 races.
