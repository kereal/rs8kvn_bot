## XUI Authentication Mechanism

### Architecture
- **Configurable session lifetime**: `XUI_SESSION_MAX_AGE_MINUTES` env var (default 720, production .env = 1440)
- **Session verification**: `verifySession()` makes real GET `/panel/api/server/status` call instead of relying on timers
- **Auto-relogin**: `doRequestWithAuthRetry()` detects HTTP 401/302/307, clears connection pool, re-logins, and retries request once
- **Singleflight**: `loginGroup.Do("login")` deduplicates concurrent login attempts
- **Circuit breaker**: 5 failures → 30s open, half-open allows 3 attempts

### Key Methods
- `ensureLoggedIn(ctx, force)` — fast path (timer check) → slow path (verifySession → login with retry)
- `verifySession(ctx) bool` — real API call to check session validity
- `doLogin(ctx)` — POST `/login`, clears idle connections first, updates `lastLogin` and `loginCount`
- `doHTTPRequest(ctx, method, url, bodyFn)` — shared HTTP helper with auth retry; bodyFn recreates body on each retry
- `doRequestWithAuthRetry(ctx, fn)` — checks statusCode BEFORE err (closure returns both for non-200)

### Thread Safety
- `lastLogin` protected by `c.mu` (RWMutex)
- Mutex released BEFORE entering singleflight to avoid blocking all API calls during retry backoff
- Mutex held during `doLogin` call in auto-relogin path (prevents race on `lastLogin` write)
- Counters (`loginCount`, `sessionFailCount`) use `atomic.AddInt64`

### Retry Behavior
- `isRetryable(err)` — `net.DNSError` → false (fast-fail), timeout → true, "no such host" string → false
- `RetryWithBackoff` — 3 retries, 2s initial delay, exponential backoff with jitter
- DNS errors fail immediately without retry
- Intermediate retries logged at DEBUG, final failure at WARN

### Startup
- 5 attempts with 5s + jitter delay, 5s timeout per attempt
- Fatal only after all attempts exhausted

### Files
- `internal/xui/client.go` — main client
- `internal/xui/breaker.go` — circuit breaker
- `internal/config/constants.go` — timeouts, defaults
- `internal/config/config.go` — `XUISessionMaxAgeMinutes` field
- `cmd/bot/main.go` — startup retry logic
- `internal/bot/errors.go` — error classification (ErrXUIAuth, ErrXUICircuitOpen, etc.)
- `internal/service/subscription.go` — NO explicit Login() calls (relies on ensureLoggedIn)

### Testing
- Unit tests: `internal/xui/client_test.go` — verifySession, auto-relogin, isRetryable, counters, concurrent dedup
- E2E tests: `tests/e2e/subscription_flow_test.go` — `TestE2E_RealClient_*` with real httptest.Server
- `TestForceSessionExpiry()` helper for forcing session expiration in tests