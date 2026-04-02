# План улучшения тестов

Дата: 2026-04-02
Текущее состояние: 31 тестовый файл, ~300+ тестов, 30 пакетов, есть fuzz-тесты, `-race` проходит

---

## P0 — Критические пробелы

### 1. Тесты `Handle()` / маршрутизация обновлений
- **Затронутые файлы:** `internal/bot/handlers_test.go` (новые тесты)
- **Зачем:** Центральный метод диспетчеризации маршрутизирует сообщения/callbacks/inline-запросы. Не покрыт тестами.
- **Что добавить:**
  - `TestHandle_MessageUpdate` — текстовое сообщение уходит в HandleStart/HandleHelp и т.д.
  - `TestHandle_CallbackUpdate` — callback уходит в HandleCallback
  - `TestHandle_NilUpdate` — nil update не вызывает панику
  - `TestHandle_UnknownUpdateType` — channel post, edited message и т.п. игнорируются

### 2. Тесты `CleanupExpiredTrials`
- **Затронутые файлы:** `internal/database/database_test.go` (новые тесты)
- **Зачем:** Критичная для бизнеса функция — удаляет осиротевшие триал-подписки и XUI-клиенты. Нулевое покрытие.
- **Что добавить:**
  - `TestService_CleanupExpiredTrials_RemovesExpired` — просроченные триалы удаляются
  - `TestService_CleanupExpiredTrials_KeepsActive` — активные триалы сохраняются
  - `TestService_CleanupExpiredTrials_XUIDeleteError` — продолжает работу при ошибке XUI
  - `TestService_CleanupExpiredTrials_EmptyDatabase` — no-op на пустой БД
  - `TestService_CleanupExpiredTrials_CalledFromScheduler` — интеграция с планировщиком

### 3. E2E тесты — полный жизненный цикл
- **Затронутые файлы:** `tests/e2e/subscription_flow_test.go` (расширить)
- **Зачем:** Текущие E2E тесты не покрывают полный путь пользователя.
- **Что добавить:**
  - `TestE2E_FullLifecycle` — /start → создание подписки → /invite → share → новый пользователь /start share_XXX → триал → bind
  - `TestE2E_AdminWorkflow` — /del, /broadcast, /send, admin_stats, admin_lastreg
  - `TestE2E_TrialExpiryAndCleanup` — создание триала, ожидание истечения, проверка очистки

---

## P1 — Значительные улучшения

### 4. Property-based / fuzz-тесты для бизнес-логики
- **Затронутые файлы:** `internal/bot/fuzz_test.go`, `internal/utils/fuzz_test.go` (новый)
- **Зачем:** Сейчас только 3 fuzz-цели (escapeMarkdown, truncateString, inviteCodeRegex).
- **Что добавить:**
  - `FuzzSubscriptionURL` — конструирование URL с произвольными входными данными
  - `FuzzTrafficCalculation` — байты ↔ ГБ, пограничные случаи
  - `FuzzInviteCode` — проверка инвариантов формата кода
  - `FuzzTelegramMessageText` — отсутствие ошибок парсинга MarkdownV2 на произвольном тексте

### 5. Строгие проверки формата сообщений
- **Затронутые файлы:** `internal/bot/*_test.go` (обновить существующие тесты)
- **Зачем:** Большинство тестов используют `assert.Contains(text, "подписк")` — слишком слабо.
- **Изменения:**
  - Добавить хелпер `assertMessageFormat()` — валидация синтаксиса MarkdownV2
  - Тесты на двойное экранирование (регрессия `@sveta\_pupka`)
  - Проверка наличия/отсутствия эмодзи в конкретных сообщениях
  - Проверка, что чувствительные данные (client ID, UUID) НЕ утекают в пользовательские сообщения

### 6. Тесты конкурентного создания подписок
- **Затронутые файлы:** `internal/bot/subscription_test.go` (новые тесты)
- **Зачем:** Риск race condition при одновременном создании подписок двумя пользователями.
- **Что добавить:**
  - `TestCreateSubscription_Concurrent_SameUser` — защита от двойного клика через inProgress map
  - `TestCreateSubscription_Concurrent_DifferentUsers` — отсутствие взаимного влияния
  - `TestHandleCreateSubscription_RaceOnInProgress` — стресс-тест mutex + map

### 7. Исправить потокобезопасность MockBotAPI
- **Затронутые файлы:** `internal/testutil/mock_bot.go` (исправить), все `*_test.go` (обновить)
- **Зачем:** Некоторые тесты всё ещё обращаются к `mockBot.SendCalled` напрямую вместо `SendCalledSafe()`.
- **Изменения:**
  - Сделать прямые поля приватными, форсировать использование аксессоров
  - Аудит всех тестовых файлов на прямой доступ к полям
  - Прогнать весь набор с `-race` после исправления

---

## P2 — Полезные дополнения

### 8. Тесты graceful shutdown
- **Затронутые файлы:** `internal/bot/bot_test.go` (новый), `internal/web/web_test.go` (расширить)
- **Что добавить:**
  - `TestBot_GracefulShutdown` — остановка polling, очистка каналов, закрытие БД
  - `TestServer_GracefulShutdown` — активные запросы завершаются, новые не принимаются
  - `TestHeartbeat_StopOnContextCancel` — частично покрыто, расширить
  - `TestGoroutineLeak` — проверка отсутствия утечек горутин после shutdown (`runtime.NumGoroutine`)

### 9. Тесты миграций БД
- **Затронутые файлы:** `internal/database/database_test.go` (новые тесты)
- **Что добавить:**
  - `TestMigration_OldSchemaToNew` — симуляция старого файла БД, проверка AutoMigrate
  - `TestMigration_AddColumn` — добавление поля в модель, сохранение существующих данных
  - `TestMigration_ConcurrentAccess` — миграция при параллельном чтении/записи

### 10. Бенчмарки
- **Затронутые файлы:** `internal/bot/*_test.go`, `internal/utils/*_test.go` (новые бенчмарки)
- **Что добавить:**
  - `BenchmarkGetMainMenuContent`
  - `BenchmarkEscapeMarkdown`
  - `BenchmarkGetSubscriptionWithCache`
  - `BenchmarkCache_SetGet`
  - `BenchmarkPerUserRateLimiter_Allow`
  - `BenchmarkCircuitBreaker_Execute`

### 11. Тесты валидации конфигурации
- **Затронутые файлы:** `internal/config/config_test.go` (расширить)
- **Что добавить:**
  - Пустой TelegramBotToken
  - Отрицательный TelegramAdminID
  - Невалидный XUIHost URL
  - Отсутствующий DatabasePath
  - Нулевой TrafficLimitGB
  - Невалидное значение LogLevel
  - Приоритет переопределения через env var

### 12. Тесты восстановления после ошибок
- **Затронутые файлы:** `internal/xui/client_test.go`, `internal/bot/handlers_test.go` (расширить)
- **Что добавить:**
  - `TestXUIClient_RetryOn429` — rate limit от XUI панели
  - `TestXUIClient_RetryOn500` — восстановление после серверной ошибки
  - `TestXUIClient_CircuitBreakerRecovery` — цикл open → half-open → closed под нагрузкой
  - `TestBot_PollingRecovery` — отключение и переподключение к Telegram API
  - `TestBroadcast_ResumeAfterSendFailure` — частичная рассылка, затем повтор

---

## P3 — Желательно, но не обязательно

### 13. Golden file тесты для HTML
- **Затронутые файлы:** `internal/web/web_test.go` (новые тесты)
- **Что добавить:**
  - `TestRenderTrialPage_Golden` — сравнение с `testdata/trial_page.html`
  - `TestRenderErrorPage_Golden` — сравнение с `testdata/error_page.html`

### 14. Тесты логирования
- **Затронутые файлы:** `internal/logger/logger_test.go` (расширить)
- **Что добавить:**
  - Проверка отсутствия токенов/паролей в логах
  - Проверка маскировки/усечения client ID
  - Проверка наличия контекста в ошибках (файл, строка, операция)
  - Тест структурированных полей лога (JSON output)

### 15. Snapshot-тест callback-данных
- **Затронутые файлы:** `internal/bot/callbacks_test.go` (расширить)
- **Что добавить:**
  - Единый тест-источник истины со ВСЕМИ валидными callback-строками
  - Проверка: каждый callback в клавиатуре имеет обработчик
  - Обнаружение осиротевших callback (определён, но не используется) и отсутствующих (используется, но не обработан)

---

## Порядок выполнения

1. P0 — первыми, ловят реальные баги (паника nil-логгера была бы поймана пунктом 1)
2. P1 — улучшение качества, предотвращение регрессий
3. P2 — заполнение оставшихся пробелов
4. P3 — полировка

## Оценка трудозатрат

| Приоритет | Пунктов | Тестов ~ | Время |
|-----------|---------|----------|-------|
| P0 | 3 | ~20 | 2-3 часа |
| P1 | 4 | ~35 | 4-5 часов |
| P2 | 5 | ~30 | 4-6 часов |
| P3 | 3 | ~15 | 2-3 часа |
| **Итого** | **15** | **~100** | **12-17 часов** |
