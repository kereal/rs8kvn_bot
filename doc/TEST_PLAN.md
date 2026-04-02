# Testing Plan

## P0 — Critical (catch startup/regression bugs)

### 1. Smoke Test
- Запускает скомпилированный бинарник с тестовым конфигом
- Проверяет exit code и отсутствие паники за 5 секунд
- **Поймал бы**: nil pointer panic в `main()` до инициализации логгера

### 2. Graceful Shutdown Test
- Посылает SIGTERM запущенному бинарнику
- Проверяет graceful cleanup: goroutine tracker, Sentry flush, DB close
- Проверяет, что broadcast завершается через `context.WithoutCancel`

## P1 — Real-world scenarios

### 3. Web Server Integration Test
- Поднимает реальный HTTP сервер
- Дёргает `/sub/{id}` — проверяет subscription link
- Дёргает trial endpoint — проверяет создание trial подписки

### 4. XUI Retry/Backoff Test
- Поднимает тестовый HTTP сервер с последовательностью 500→500→200
- Проверяет retry с jitter и exponential backoff
- Проверяет `ensureLoggedIn(false)` для health check

### 5. Concurrent Subscription Stress Test
- 50 goroutines создают подписки одновременно
- Проверяет отсутствие дублей в реальной SQLite
- Проверяет atomic cleanup через `DELETE RETURNING`

## P2 — Edge cases

### 6. Config Validation
- Пустой токен, невалидный DSN, отрицательные таймауты
- Проверяет, что бот отказывается стартовать с невалидным конфигом

### 7. Cache Eviction Under Load
- Заполняет кэш > Capacity
- Проверяет LRU eviction по `lastAccess`, а не `expiresAt`

### 8. Database Migration Tests
- Fresh DB, existing DB, corrupted DB, rollback
- Проверяет idempotency миграций

### 9. Rate Limiter Burst
- 100 concurrent requests от одного chatID
- Проверяет блокировку и per-user isolation

### 10. Invite Chain
- A→B→C referral chain
- Проверяет referral tracking и invite code generation

## P3 — Infrastructure

### 11. Memory/Goroutine Leak Test
- Запускает цикл create→delete (100 итераций)
- Проверяет отсутствие утечек через `runtime.NumGoroutine()`

### 12. Broadcast with Failures
- 10% пользователей "offline"
- Проверяет продолжение рассылки и detach context
