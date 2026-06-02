# 3x-ui Authentication & HTTP

## Auth
- **Bearer token** через `Authorization` header, **нет** сессии/логина/CSRF/cookie jar.
- Token: `XUI_API_TOKEN` env → `Config.XUIAPIToken`.
- Каждый запрос: `Authorization: Bearer <token>` через `doHTTPRequest()`.
- **Нет session expiry tracking** — клиент готов сразу после конструкции.

## Singleflight
- **НЕ для логина** (логина нет).
- Используется в `internal/web/singleflight.go` для дедупликации одновременных DB queries (subproxy cache misses).

## Circuit Breaker ✅ ЕСТЬ
- `internal/xui/breaker.go` — НЕ удалён.
- 5 failures → 30s Open → Half-Open → Closed.
- `metrics.CircuitBreakerState` (Prometheus-метрика).
- **Не** в xui/auth-mechanism ранее было написано "No circuit breaker: Removed" — это была ошибка, исправлено.

## Retry
- `RetryWithBackoff(ctx, maxRetries, initialDelay, fn)` — exponential + jitter.
- `isRetryable(err)`:
  - `net.DNSError` → **false** (fast-fail)
  - timeout → **true**
  - "no such host" в строке ошибки → **false**
- HTTP 5xx → retried (response body возвращается с error).

## Thread safety
- Нет shared mutable state — все поля immutable после конструкции.
- Нет mutex/atomic.
- `http.Transport` goroutine-safe (stdlib).

## Ключевые методы
- `NewClient(host, apiToken)` — 2-param конструктор (был 4-param с username/password/sessionMaxAge).
- `doHTTPRequest(ctx, method, url, bodyFn)` — общий HTTP helper, ставит Bearer header.
- `Ping(ctx)` — `GET /panel/api/server/status` (liveness check).
- `getRequiredFlow(ctx, inboundID)` — детектит transport (`xhttp`/`h2`/`ws`/`grpc`/`grpcs` → flow empty; `tcp`/unknown → `xtls-rprx-vision`).

## Flow detection
- Transport `xhttp`/`h2`/`ws`/`grpc`/`grpcs` → flow пустой (не нужен).
- Transport `tcp` или unknown → flow `xtls-rprx-vision`.

## Files
- `internal/xui/client.go` — main client + retry
- `internal/xui/breaker.go` — circuit breaker
- `internal/config/constants.go` — `XUIRequestTimeout` и др. defaults
- `internal/config/config.go` — `XUIAPIToken` field
- `internal/interfaces/interfaces.go` — `XUIClient` interface (без `Login(ctx)`)
- `internal/web/singleflight.go` — singleflight (НЕ в xui/)

## Tests
- `internal/xui/client_test.go` — doHTTPRequest, CRUD, RetryWithBackoff, isRetryable, flow detection, 401/403/500 errors.
- `internal/xui/breaker_test.go` — circuit breaker state machine.
- E2E: см. `tests/e2e/`.

## См. также
- CRUD: `xui/client-crud.md`
- Reset: `xui/reset-mechanism.md`
