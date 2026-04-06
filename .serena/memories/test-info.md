## Test Coverage (April 2026)

**Overall Coverage:** ~80%

### By Module
- `internal/flag`: 97.7% ✅
- `internal/ratelimiter`: 97.4% ✅
- `internal/heartbeat`: 96.2% ✅
- `internal/config`: 91.5% ✅
- `internal/web`: 90.7% ✅
- `internal/xui`: 90.4% ✅
- `internal/bot`: 89.0% ✅
- `internal/utils`: 87.5% ✅
- `internal/logger`: 88.9% ✅
- `internal/scheduler`: 81.2% ✅
- `internal/subproxy`: 81.9% ✅
- `internal/backup`: 76.5% ✅
- `internal/database`: 77.8% ✅
- `internal/service`: 24.8% 🟡
- `cmd/bot`: 6.1% 🟡

### Test Statistics
- **Total test functions:** 1,058+
- **Test files:** 52
- **E2E test files:** 12
- **Race-safe:** ✅
- **Golden files:** ✅ (subproxy)
- **Property-based tests:** ✅ (uuid)

### Areas to Improve
1. 🟡 `internal/service` - improve coverage (24.8%)
2. 🟡 `cmd/bot` - main is integration (6.1% is acceptable)

### Recent Test Improvements
- Golden files added for subproxy (`internal/testdata/subproxy/`)
  - `vless_single.txt`
  - `vmess_multi.txt`
  - `base64_encoded.txt`
- Property-based tests for utils (1000+ iterations)
  - `TestProperties_UUID`
  - `TestProperties_SubID`
  - `TestProperties_InviteCode`

---

**Обновлено:** 2026-04-05