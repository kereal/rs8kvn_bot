# Унифицированный план тестирования

**Дата актуализации:** 2026-04-02
**Текущее состояние:**
- ~31 тестовый файл, ~300+ тестов, 30 пакетов
- Fuzz-тесты: `escapeMarkdown`, `truncateString`, `inviteCodeRegex`
- Флаг `-race` проходит без ошибок
- **Добавлено (v2.1.0):** 8 тестов для `daysUntilReset` (100% покрытие)

---

## P0 — Критические (блокируют релиз)

### 1. Smoke Test
- **Затронутые файлы:** `tests/smoke/smoke_test.go` (новый)
- **Что делает:**
  - Компилирует бинарник с тестовым конфигом
  - Запускает на 5 секунд, проверяет exit code и отсутствие паники
- **Поймал бы:** nil pointer panic в `main()` до инициализации логгера
- **Статус:** ⬜ Не реализовано

### 2. Тесты `Handle()` — маршрутизация обновлений
- **Затронутые файлы:** `internal/bot/handlers_test.go`
- **Зачем:** Центральный метод диспетчеризации не покрыт тестами
- **Что добавить:**
  - `TestHandle_MessageUpdate` — текстовое сообщение → HandleStart/HandleHelp
  - `TestHandle_CallbackUpdate` — callback → HandleCallback
  - `TestHandle_NilUpdate` — nil update не вызывает панику
  - `TestHandle_UnknownUpdateType` — channel post, edited message игнорируются
- **Статус:** ⬜ Не реализовано

### 3. Тесты `CleanupExpiredTrials`
- **Затронутые файлы:** `internal/database/database_test.go`
- **Зачем:** Критичная функция — удаляет осиротевшие триал-подписки и XUI-клиенты
- **Что добавить:**
  - `TestService_CleanupExpiredTrials_RemovesExpired`
  - `TestService_CleanupExpiredTrials_KeepsActive`
  - `TestService_CleanupExpiredTrials_XUIDeleteError` — продолжает при ошибке XUI
  - `TestService_CleanupExpiredTrials_EmptyDatabase`
  - `TestService_CleanupExpiredTrials_CalledFromScheduler`
- **Статус:** ⬜ Не реализовано

### 4. E2E тесты — полный жизненный цикл
- **Затронутые файлы:** `tests/e2e/subscription_flow_test.go`
- **Что добавить:**
  - `TestE2E_FullLifecycle` — /start → создание → /invite → share → новый пользователь → триал → bind
  - `TestE2E_AdminWorkflow` — /del, /broadcast, /send, admin_stats
  - `TestE2E_TrialExpiryAndCleanup` — создание триала, истечение, проверка очистки
  - `TestE2E_AutoResetTraffic` — проверка синхронизации ExpiryTime после авто-сброса
- **Статус:** ⬜ Частично реализовано (нуждается в расширении)

---

## P1 — Значительные улучшения

### 5. Graceful Shutdown Test
- **Затронутые файлы:** `internal/bot/bot_test.go`, `internal/web/web_test.go`
- **Что добавить:**
  - `TestBot_GracefulShutdown` — остановка polling, очистка каналов, закрытие БД
  - `TestServer_GracefulShutdown` — активные запросы завершаются, новые отклоняются
  - `TestHeartbeat_StopOnContextCancel`
  - `TestGoroutineLeak` — проверка через `runtime.NumGoroutine()`
- **Статус:** ⬜ Не реализовано

### 6. Web Server Integration Test
- **Затронутые файлы:** `internal/web/web_test.go`
- **Что тестирует:**
  - `/sub/{id}` — ссылка подписки
  - Trial endpoint — создание триал-подписки
  - Обработка невалидных ID
- **Статус:** ⬜ Частично реализовано

### 7. XUI Retry/Backoff Test
- **Затронутые файлы:** `internal/xui/client_test.go`
- **Что тестирует:**
  - Последовательность 500→500→200 — retry с exponential backoff
  - `ensureLoggedIn(false)` для health check
  - Rate limit 429 — корректная обработка
- **Статус:** ⬜ Частично реализовано (есть базовые retry тесты)

### 8. Concurrent Subscription Stress Test
- **Затронутые файлы:** `internal/bot/subscription_test.go`, `internal/database/database_test.go`
- **Что добавить:**
  - 50 goroutines создают подписки одновременно
  - `TestCreateSubscription_Concurrent_SameUser` — защита от двойного клика
  - `TestCreateSubscription_Concurrent_DifferentUsers` — изоляция пользователей
  - Проверка отсутствия дублей в SQLite
- **Статус:** ⬜ Не реализовано

### 9. Property-based / fuzz-тесты
- **Затронутые файлы:** `internal/bot/fuzz_test.go`, `internal/utils/fuzz_test.go`
- **Что добавить:**
  - `FuzzSubscriptionURL` — конструирование URL с произвольными входными данными
  - `FuzzTrafficCalculation` — байты ↔ ГБ, пограничные случаи
  - `FuzzInviteCode` — инварианты формата кода
  - `FuzzTelegramMessageText` — отсутствие ошибок парсинга MarkdownV2
- **Статус:** ⬜ 3 fuzz-цели реализованы (нужно расширение)

### 10. Строгие проверки формата сообщений
- **Затронутые файлы:** `internal/bot/*_test.go`
- **Зачем:** Текущие проверки `assert.Contains(text, "подписк")` слишком слабые
- **Изменения:**
  - Хелпер `assertMessageFormat()` — валидация синтаксиса MarkdownV2
  - Тесты на двойное экранирование (регрессия `@sveta\_pupka`)
  - Проверка отсутствия утечек client ID / UUID в пользовательские сообщения
- **Статус:** ⬜ Не реализовано

### 11. Исправить потокобезопасность MockBotAPI
- **Затронутые файлы:** `internal/testutil/mock_bot.go`, все `*_test.go`
- **Проблема:** Прямой доступ к `mockBot.SendCalled` вместо `SendCalledSafe()`
- **Изменения:**
  - Приватизировать прямые поля, форсировать использование аксессоров
  - Аудит всех тестовых файлов
  - Прогон с `-race` после исправления
- **Статус:** ⬜ Требуется аудит

---

## P2 — Полезные дополнения

### 12. Config Validation
- **Затронутые файлы:** `internal/config/config_test.go`
- **Что тестировать:**
  - Пустой TelegramBotToken
  - Отрицательный TelegramAdminID
  - Невалидный XUIHost URL
  - Отсутствующий DatabasePath
  - Нулевой TrafficLimitGB
  - Невалидный LogLevel
  - Приоритет env var над конфигом
- **Статус:** ⬜ Частично реализовано

### 13. Database Migration Tests
- **Затронутые файлы:** `internal/database/database_test.go`
- **Что добавить:**
  - `TestMigration_OldSchemaToNew` — симуляция старого файла БД
  - `TestMigration_AddColumn` — добавление поля, сохранение данных
  - `TestMigration_ConcurrentAccess` — миграция при параллельных операциях
  - Idempotency проверка
- **Статус:** ⬜ Не реализовано

### 14. Cache Eviction Under Load
- **Затронутые файлы:** `internal/cache/cache_test.go`
- **Что тестировать:**
  - Заполнение > Capacity
  - LRU eviction по `lastAccess`, а не `expiresAt`
  - Конкурентный доступ
- **Статус:** ⬜ Не реализовано

### 15. Rate Limiter Burst
- **Затронутые файлы:** `internal/bot/ratelimiter_test.go`
- **Что тестировать:**
  - 100 concurrent requests от одного chatID
  - Блокировка и per-user isolation
  - Burst boundary cases
- **Статус:** ⬜ Не реализовано

### 16. Invite Chain
- **Затронутые файлы:** `internal/database/database_test.go`, `internal/bot/subscription_test.go`
- **Сценарий:** A→B→C referral chain
- **Проверки:**
  - Referral tracking
  - Invite code generation
  - Связь приглашённого с пригласившим
- **Статус:** ⬜ Не реализовано

### 17. Бенчмарки
- **Затронутые файлы:** `internal/bot/*_test.go`, `internal/utils/*_test.go`
- **Что добавить:**
  - `BenchmarkGetMainMenuContent`
  - `BenchmarkEscapeMarkdown`
  - `BenchmarkGetSubscriptionWithCache`
  - `BenchmarkCache_SetGet`
  - `BenchmarkPerUserRateLimiter_Allow`
  - `BenchmarkCircuitBreaker_Execute`
- **Статус:** ⬜ Не реализовано

### 18. Тесты восстановления после ошибок
- **Затронутые файлы:** `internal/xui/client_test.go`, `internal/bot/handlers_test.go`
- **Что добавить:**
  - `TestXUIClient_RetryOn429`
  - `TestXUIClient_RetryOn500`
  - `TestXUIClient_CircuitBreakerRecovery` — open → half-open → closed
  - `TestBot_PollingRecovery`
  - `TestBroadcast_ResumeAfterSendFailure`
- **Статус:** ⬜ Частично реализовано

---

## P3 — Желательно, но не обязательно

### 19. Memory/Goroutine Leak Test
- **Затронутые файлы:** `tests/leak/leak_test.go` (новый)
- **Сценарий:** 100 итераций create→delete
- **Проверка:** `runtime.NumGoroutine()` до и после
- **Статус:** ⬜ Не реализовано

### 20. Broadcast with Failures
- **Затронутые файлы:** `internal/bot/broadcast_test.go`
- **Сценарий:** 10% пользователей "offline"
- **Проверки:**
  - Продолжение рассылки при ошибках
  - Detach context через `context.WithoutCancel`
- **Статус:** ⬜ Не реализовано

### 21. Golden file тесты для HTML
- **Затронутые файлы:** `internal/web/web_test.go`
- **Что добавить:**
  - `TestRenderTrialPage_Golden` — сравнение с `testdata/trial_page.html`
  - `TestRenderErrorPage_Golden`
- **Статус:** ⬜ Не реализовано

### 22. Тесты логирования
- **Затронутые файлы:** `internal/logger/logger_test.go`
- **Что проверять:**
  - Отсутствие токенов/паролей в логах
  - Маскировка client ID
  - Контекст в ошибках (файл, строка, операция)
  - Структурированные поля (JSON output)
- **Статус:** ⬜ Не реализовано

### 23. Snapshot-тест callback-данных
- **Затронутые файлы:** `internal/bot/callbacks_test.go`
- **Что добавить:**
  - Единый источник истины со всеми валидными callback-строками
  - Проверка: каждый callback имеет обработчик
  - Обнаружение осиротевших/отсутствующих callback
- **Статус:** ⬜ Не реализовано

---

## Покрыто в v2.1.0 (Авто-сброс трафика)

### ✅ `daysUntilReset` — 8 тестов
- `TestDaysUntilReset_ZeroExpiryTime` — возвращает -1 (авто-сброс отключён)
- `TestDaysUntilReset_Expired` — возвращает 0 (уже истёк)
- `TestDaysUntilReset_Equal` — граничный случай
- `TestDaysUntilReset_NormalCase` — нормальный сценарий
- `TestDaysUntilReset_OneDayLeft` — остался 1 день
- `TestDaysUntilReset_TwentyNineDaysLeft` — 29 дней
- `TestDaysUntilReset_AlmostExpired` — меньше 1 дня
- `TestDaysUntilReset_FutureExpiry` — 30-дневный интервал

**Покрытие:** 100% функции `daysUntilReset`

---

## Порядок выполнения

| Этап | Приоритет | Пунктов | Тестов ~ | Время |
|------|-----------|---------|----------|-------|
| 1 | P0 | 4 | ~25 | 3-4 часа |
| 2 | P1 | 7 | ~50 | 6-8 часов |
| 3 | P2 | 7 | ~45 | 5-7 часов |
| 4 | P3 | 5 | ~20 | 3-4 часа |
| **Итого** | | **23** | **~140** | **17-23 часа** |

---

## Рекомендации по выполнению

1. **Сначала P0** — ловят критические баги (паники, nil-указатели)
2. **Затем P1** — улучшают качество и предотвращают регрессии
3. **P2 по необходимости** — заполняют пробелы в покрытии
4. **P3 в конце** — полировка

### Быстрые победы (quick wins)
- Пункт 11 (MockBotAPI) — аудит и исправление, ~30 мин
- Пункт 12 (Config Validation) — расширение существующих тестов, ~1 час
- Пункт 7 (XUI Retry) — добавить 2-3 теста, ~1 час

### Зависимости
- Пункт 4 (E2E) требует п.2 (Handle) и п.3 (CleanupExpiredTrials)
- Пункт 8 (Concurrent) требует п.11 (MockBotAPI thread-safe)
- Пункт 19 (Leak) лучше делать после п.5 (Graceful Shutdown)