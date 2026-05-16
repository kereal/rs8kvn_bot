## XUI Authentication Mechanism

### Architecture
- **API Token auth**: Bearer token via `Authorization` header, no session/login/CSRF
- **Token config**: `XUI_API_TOKEN` env var → `Config.XUIAPIToken`
- **Every request** includes `Authorization: Bearer <token>` header via `doHTTPRequest()`
- **No session state**: No login, no cookie jar, no session expiry tracking, no singleflight dedup
- **No circuit breaker**: Removed; all retry logic is in `RetryWithBackoff()`

### Key Methods
- `NewClient(host, apiToken)` — 2-param constructor (was 4-param with username/password/sessionMaxAge)
- `doHTTPRequest(ctx, method, url, bodyFn)` — shared HTTP helper, sets Bearer token header
- `RetryWithBackoff(ctx, maxRetries, initialDelay, fn)` — exponential backoff with jitter
- `isRetryable(err)` — `net.DNSError` → false (fast-fail), timeout → true, "no such host" string → false
- `Ping(ctx)` — GET `/panel/api/server/status` (replaces `verifySession`)

### Thread Safety
- No shared mutable state — all fields immutable after construction
- No mutexes, no atomics
- `http.Transport` is goroutine-safe (from `net/http` stdlib)

### Retry Behavior
- `isRetryable(err)` — DNS errors → false (fast-fail), network timeouts → true
- `RetryWithBackoff` — configurable retries (default 3), configurable initial delay (default 1s), exponential backoff with jitter
- Non-retryable errors fail immediately
- HTTP 5xx errors are retried (the response body is returned with error, and `isRetryable` treats non-DNS errors as retryable)

### Startup
- No background login goroutine — client is ready immediately after construction
- No startup retry loop needed

### Flow Detection
- `getRequiredFlow(ctx, inboundID)` — fetches inbound settings, detects transport, returns appropriate flow
- Transport `xhttp`/`h2`/`ws`/`grpc`/`grpcs` → flow empty (not needed)
- Transport `tcp` or unknown → flow `xtls-rprx-vision`

### Files
- `internal/xui/client.go` — main client
- `internal/config/constants.go` — timeouts, defaults, `XUIRequestTimeout`
- `internal/config/config.go` — `XUIAPIToken` field (replaces `XUIUsername`/`XUIPassword`/`XUISessionMaxAgeMinutes`)
- `internal/interfaces/interfaces.go` — `XUIClient` interface without `Login(ctx)` method

### Testing
- Unit tests: `internal/xui/client_test.go` — doHTTPRequest, CRUD methods, RetryWithBackoff, isRetryable, Inbound flow detection, HTTP error handling (401/403/500)
- E2E tests: `tests/e2e/real_client_test.go` — FullSubscriptionLifecycle, DNSErrorFastFail