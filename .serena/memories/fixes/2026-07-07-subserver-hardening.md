# subserver — усиление надёжности и параллелизм (2026-07-07)

Комплекс правок пакета `internal/subserver` (endpoint `/sub/{id}`): исправление
критических багов, рефакторинг access-log, параллельная выборка апстрима,
сужение `FilterHeaders`. Все изменения проверены: `go build ./...`,
`go vet ./internal/subserver/... ./internal/web/...`, `go test ./internal/subserver/... ./internal/web/...` — зелёные.

## Что сделано

### fetch.go
- Переименование: `FetchFromSource` → `FetchFromNode`, `SourceResponse` → `NodeResponse`.
- Добавлена проверка HTTP-статуса: `resp.StatusCode >= 400` возвращает ошибку
  (раньше 4xx/5xx агрегировались как валидный контент — критический баг).
- Добавлен guard `resp == nil || resp.Body == nil` (защита от паники).
- `fetchHTTPClient.Timeout`: 15s → 10s (синхронизировано с `web.go` request timeout).

### subscription_handler_split.go
- `serveFromCache` теперь принимает `(clientIP, requestHeaders)`.
- Revoked/expired подписка → возврат `ErrSubscriptionNotFound` (web.go отдаёт 404,
  соблюдая контракт `/sub/:id`; раньше `fmt.Errorf("subscription not active")` → 500).
  Кэш при этом НЕ инвалидируется (запись оставляется целой).
- Транзиентная ошибка `GetSubscriptionStatus` (БД недоступна и т.п.) → отдача
  stale-кэша best-effort (метрика + warn + `trackDeviceAndIP`), вместо жёсткого
  падения и уничтожения валидного кэша.
- Сравнение `expire` теперь числовое (`int64`) через `updateMinExpire` /
  `parseExpireToInt` (раньше строковое сравнение — ненадёжно при mixed unix-sec/ms).
- Переименование `firstSourceHeaders` → `srcHeaders` (была внутри цикла, вводило в заблуждение).
- **Параллельная выборка апстрима**: `fetchAndAggregateSources` использует
  `sync.WaitGroup` + буферизованный семафор (`maxSourceConcurrency = 8`),
  `results[]` индексируется по ноде; merge последовательный (детерминированный
  выбор first-source header/expire). Новый хелпер `fetchSource` возвращает
  `sourceResult{}` (nil body) при ошибке. Время выборки — max, а не сумма таймаутов.

### subscription_handler.go
- `trackDeviceAndIP(ctx, db, subID, clientIP, requestHeaders)`: грузит подписку
  через `db.GetSubscription`, затем `UpdateDevices`/`UpdateIPs` на
  `&database.SubscriptionFull{Subscription: *sub}` (обёртка value→full).
  Вызывается на обоих путях (cache hit И cache miss) — аналитика устройств/IP
  теперь обновляется между истечениями кэша, а не только на miss.
  Сигнатуры `UpdateDevices`/`UpdateIPs` оставлены `*database.SubscriptionFull`,
  чтобы не ломать тесты.

### access_log.go
- Переписан без `zapcore`: `Log()` пишет **TSV-строку** напрямую через
  `strings.Builder` (поля: timestamp UTC, level=INFO, method, request_uri,
  status_code, client_ip, X-Hwid, X-Device-Os, X-Ver-Os, X-Device-Model, User-Agent).
  Удалены `accessLogEncoder`/`accessLogFieldEncoder`/буфер-пул. Пустые значения
  пишутся как пустые поля (без `-` и без кавычек). Асинхронная запись
  (`asyncAccessLogWriter`, queue 1024, drop-счётчик) сохранена.

### subscription_helpers.go — FilterHeaders
- Добавлены в исключения `accept`, `authorization`, `cookie` (поверх
  `x-forwarded-proto/-for`, `x-real-ip`). Значения по-прежнему lowercas-ятся.
  Частичное закрытие бага #5 (утечка/шум заголовков в колонке `devices`).

### web.go
- `web.go:608` request timeout: 15s → 10s.
- Удалён мёртвый блок `var (ErrSubscriptionNotFound / ErrSubscriptionNoItems)`
  в пакете `web` — он нигде не использовался; пакет использует квалифицированные
  `database.ErrSubscriptionNotFound` и `subserver.ErrSubscriptionNotFound`.

## Тесты / доки
- `internal/subserver/subscription_handler_test.go`: revoked/expired →
  `errors.Is(err, ErrSubscriptionNotFound)`; transient status error →
  `NoError` + отдаётся stale + кэш цел.
- `internal/web/subserver_test.go` (`TestHandleSubscription_AccessLogVariants`):
  ожидания приведены к TSV-формату. **ВАЖНО**: тест делает
  `strings.TrimSpace(content)` — он срезает завершающие табы пустых полей
  заголовков, поэтому для запроса без device-заголовков видимая строка содержит
  ровно 6 полей (timestamp..client_ip). Не писать ожидания с завершающими `\t`.
- Доки обновлены: `doc/operations.md` (§4.3), `doc/installation.md`,
  `doc/handover.md` — формат access-log теперь TSV; `doc/api.md` — поведение
  cache-hit (404 на revoked/expired, stale на транзиентной ошибке БД).

## Известные НЕ закрытые пункты (вне объёма)
- **#5 (header leak, полный)**: `UpdateDevices` по-прежнему пишет ВСЕ отфильтрованные
  заголовки (вкл. `user-agent`) вместо allowlist device-идентификаторов; значения
  lowercas-ятся (портит канонический `User-Agent`). Нет дедупа по HWID для запросов
  без `x-hwid` (curl добавляет новую запись каждый раз, capped `MaxDeviceEntries`).
- **#7**: per-request ревалидация `GetSubscriptionStatus` на каждом cache-hit
  сохранена (транзиентная ошибка теперь не фатальна, но запрос к БД остаётся).
- **#8**: singleflight на cache stampede не добавлен (есть в `internal/web/singleflight.go`
  для логина, но путь cache-hit субсервера его не использует).
