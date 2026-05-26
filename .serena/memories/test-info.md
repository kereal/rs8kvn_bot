## Test Coverage (May 2026)

**Overall Coverage:** ~85% (модули с мульти-сорс миграцией переписаны, покрытие восстановлено)

### By Module
- `internal/flag`: 97.7% ✅
- `internal/ratelimiter`: 97.4% ✅
- `internal/heartbeat`: 96.2% ✅
- `internal/service`: 95.2% ✅
- `internal/config`: 91.8% ✅
- `internal/xui`: 90.9% ✅
- `internal/web`: 90.3% ✅
- `internal/bot`: 92.6% ✅
- `internal/utils`: 90.0% ✅
- `internal/logger`: 88.9% ✅
- `internal/backup`: 83.2% ✅
- `internal/subproxy`: 82.5% ✅
- `internal/scheduler`: 81.2% ✅
- `internal/database`: 77.8% 🟡
- `cmd/bot`: 5.4% 🟡 (main is integration — acceptable)
- `internal/testutil`: 0.0% 🔴 (mock helpers, no direct tests needed)

### Test Statistics
- **Total test functions:** ~911
- **Test files:** 48
- **E2E test files:** 12
- **Race-safe:** ✅
- **Golden files:** ✅ (subproxy)

### Multi-Source Migration (2026-05-26)

**Config changes:**
- `XUIHost`, `XUIAPIToken`, `XUIInboundID`, `XUISubPath` → `Sources []Source` (каждый с URL, Token, InboundID, Active, Trial)
- `SubscriptionURL` → вычисляется из `GlobalSubURL + SubID`
- Добавлен `GLOBAL_SUB_URL` и `DEFAULT_SOURCE_SUB_URL`

**Service changes:**
- `NewSubscriptionService(xuiClients map[uint]interfaces.XUIClient, sources []database.Source, db interfaces.SubscriptionDatabase, cfg *config.Config, cache *cache.SubscriptionCache, invalidate cache.InvalidateFunc)`
- `CreateTrial()` — итерация trial-источников, best-effort
- `BindTrial()` — первый успешный источник
- `ReconcileOrphanedClients()` — проверка всех источников

**Test patterns:**
- Source-литералы обязаны иметь `Trial:true, Active:true, ID:1`
- `mockXUIClients := map[uint]interfaces.XUIClient{1: mockClient}`
- Удалены поля: `InboundID`, `TrafficLimit`, `SubscriptionURL`, `DeletedAt` из Subscription

## Test Architecture Notes
- `web_test.go` split into 3 files: `web_test.go` (583 lines), `web_health_test.go` (363), `web_invite_test.go` (784)
- `handlers_extended_test.go`: 8 duplicate/redundant tests removed
- `cmd/bot/main_test.go`: TestGetVersion (5→1) and TestHandleUpdateSafely (4→1) merged into table-driven
- `backup_test.go`: ValidatePath (11→1), RotateBackups (9→1), GetBackupInfo sort (3→1) consolidated
- `message_format_test.go`: removed dead validateMarkdownV2 function
- `test_helpers.go`: removed dead NewTestHandler function
- `logger_test.go`: removed 3 dead nil-logger skipped tests
- `interfaces/interfaces_test.go`: deleted (empty stub)

### Performance Optimizations (v2.3.1)
- **`-short` mode support:**
  - xui: 5 slow tests (AddClient, DeleteClient, UpdateClient, GetClientTraffic, GetRequiredFlow_Fallback) skip in short mode — saves ~47s
  - heartbeat: TestSendHeartbeat_InvalidURL skip in short — saves ~10s
  - All fast tests run with: `go test -short ./...`
- **time.Sleep → assert.Eventually/runtime.Gosched:**
  - graceful_shutdown_test.go: 8 Sleeps replaced, ~300ms saved
  - subproxy/service_test.go: 2× Sleep(30ms) → assert.Eventually
  - subproxy/cache_test.go: 2× Sleep(20ms) → assert.Eventually
  - scheduler: Sleep(20ms)+After(1s) → After(200ms)
  - heartbeat: After(2s)→After(200ms) in 4 places
- **t.Parallel() usage:** added to 11 heartbeat test functions (was 1)
- **Race fix:** `requestReceived` in heartbeat_test changed from global variable to `atomic.Bool`
- **Total `-short` runtime:** ~15s (was ~60-70s)

### Areas to Improve
1. 🟡 `internal/database` — improve coverage (77.8%)
2. 🟡 `cmd/bot` — main is integration (5.4% is acceptable)
3. 🟡 xui tests use real retry backoff even with mock servers — 45s in non-short mode. Consider mocking clock/timeout for faster execution.
4. 🔵 e2e tests (`tests/e2e/`) use 30s timeouts — should be excluded from `go test ./...`, run separately before release

---

**Updated:** 2026-05-24