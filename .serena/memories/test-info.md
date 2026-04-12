## Test Coverage (April 2026)

**Overall Coverage:** ~85%

### By Module
- `internal/flag`: 97.7% ✅
- `internal/ratelimiter`: 97.4% ✅
- `internal/heartbeat`: 96.2% ✅
- `internal/service`: 95.2% ✅ (was 24.8% — +30 tests in v2.3.0)
- `internal/config`: 91.8% ✅
- `internal/xui`: 90.9% ✅
- `internal/web`: 90.3% ✅
- `internal/bot`: 92.6% ✅ (was 87.8% — +15 ReferralCache tests in v2.3.0)
- `internal/utils`: 90.0% ✅ (was 87.5% — added format.go shared tests)
- `internal/logger`: 88.9% ✅
- `internal/backup`: 83.2% ✅
- `internal/subproxy`: 82.5% ✅
- `internal/scheduler`: 81.2% ✅
- `internal/database`: 77.8% 🟡
- `cmd/bot`: 5.4% 🟡 (main is integration — acceptable)
- `internal/testutil`: 0.0% 🔴 (mock helpers, no direct tests needed)

### Test Statistics
- **Total test functions:** 1,100+
- **Test files:** 54
- **E2E test files:** 12
- **Race-safe:** ✅
- **Golden files:** ✅ (subproxy)
- **Property-based tests:** ✅ (uuid)

### Areas to Improve
1. 🟡 `internal/database` - improve coverage (77.8%)
2. 🟡 `cmd/bot` - main is integration (5.4% is acceptable)

### v2.3.0 Test Additions
- Service layer: 30 new tests (Create, Delete, DeleteByID, GetWithTraffic, CreateTrial, CalcTrialTraffic)
- ReferralCache: 15 new tests (Get, GetAll, Increment, Decrement, Save, Sync, concurrent safety, admin rate limit)
- Shared format: 3 tests moved from service/bot to utils/format_test.go

---

**Обновлено:** 2026-04-12
