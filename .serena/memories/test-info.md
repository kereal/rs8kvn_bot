## Test Coverage (April 2026)

**Overall Coverage:** 70.7%

### By Module
- `internal/flag`: 97.7% ✅
- `internal/ratelimiter`: 97.4% ✅
- `internal/heartbeat`: 96.2% ✅
- `internal/config`: 91.5% ✅
- `internal/web`: 90.7% ✅
- `internal/xui`: 90.4% ✅
- `internal/bot`: 87.8% ✅
- `internal/utils`: 87.5% ✅
- `internal/logger`: 88.9% ✅
- `internal/subproxy`: 82.5% ✅
- `internal/backup`: 76.5% ✅
- `internal/database`: 59.5% 🟡
- `internal/service`: 24.8% 🟡
- `cmd/bot`: 6.1% 🟡
- `internal/scheduler`: 0.0% 🔴
- `internal/testutil`: 0.0% 🔴

### Test Statistics
- **Total test functions:** 1,058
- **Test files:** 52
- **E2E test files:** 12
- **Race-safe:** ✅

### Areas to Improve
1. 🔴 `internal/scheduler` - needs tests (0% coverage)
2. 🔴 `internal/testutil` - needs tests (0% coverage)
3. 🟡 `internal/service` - improve coverage (24.8%)
4. 🟡 `cmd/bot` - main is integration (6.1% is acceptable)
