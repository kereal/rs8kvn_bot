# 3x-ui Authentication & HTTP

## Auth
- **Bearer token** через `Authorization` header, **нет** сессии/логина/CSRF/cookie jar.
- Token: `XUI_API_TOKEN` env → `Config.XUIAPIToken`.
- Каждый запрос: `Authorization: Bearer <token>` через `doHTTPRequest()`.
- **Нет session expiry tracking** — клиент готов сразу после конструкции.

## Singleflight
- **НЕ для логина** (логина нет).
- Используется в `internal/web/singleflight.go` для дедупликации одновременных DB queries (subserver cache misses).

## Circuit Breaker — УДАЛЁН
- Circuit breaker снесён как мёртвый код (коммит `406995b`, 2026-11); файлов breaker.go/breaker_test.go в репозитории нет.
- Метрики `CircuitBreakerState` больше нет.
- Устойчивость держится на `RetryWithBackoff` + `classify.go` (классификация ответов панели, не-200 не ретраится).

## Retry
- `RetryWithBackoff(ctx, maxRetries, initialDelay, fn)` — exponential + jitter.
- `utils.IsRetryable(err)`:
  - `net.DNSError` → **false** (fast-fail)
  - `utils.ErrNon200Response` → **false**
  - отмена/дедлайн контекста (`utils.IsCtxErr`) → **false**
  - timeout → **true**
  - "no such host" в строке ошибки → **false**
- HTTP 5xx → **не ретраится**: `doHTTPRequest` любой non-200 (включая 5xx) map-ит в `utils.ErrNon200Response`, а `utils.IsRetryable` для него → **false**. Ретраятся только сетевые сбои (timeout/connection); проверка закреплена в `client_test.go` («non-200 panel response must not be retried»).

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
- `internal/xui/classify.go` — единый классификатор ответов панели `{success,msg,obj}` → sentinel-ошибки
- `internal/config/constants.go` — `XUIRequestTimeout` и др. defaults
- `internal/config/config.go` — `XUIAPIToken` field
- `internal/interfaces/interfaces.go` — `XUIClient` interface (без `Login(ctx)`)
- `internal/web/singleflight.go` — singleflight (НЕ в xui/)

## Tests
- `internal/xui/client_test.go` — doHTTPRequest, CRUD, RetryWithBackoff, isRetryable (non-200 не ретраится), flow detection, 401/403/500 errors.
- E2E: см. `tests/e2e/`.

## См. также
- CRUD: `xui/client-crud.md`
- Reset: `xui/reset-mechanism.md`
