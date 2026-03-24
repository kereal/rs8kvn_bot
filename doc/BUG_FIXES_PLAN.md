# Детальный план исправлений — Безопасность, Надежность, Память

**Дата:** 2026-03-24
**Покрытие:** 51.3%

---

## 🔴 P0 - Критические баги

### 1. Path Traversal в backup
- **Файл:** `internal/backup/backup.go:17-42`
- **Проблема:** `validatePath()` не проверяет absolute paths, `foo/./bar`
- **Исправление:** Использовать `filepath.Abs()` и проверять base directory

### 2. Buffer pool race condition
- **Файл:** `internal/xui/client.go:178`
- **Проблема:** Буфер возвращается в пул до завершения HTTP запроса
- **Исправление:** Копировать буфер или использовать `bytes.NewReader`

### 3. Goroutine leak на shutdown
- **Файл:** `cmd/bot/main.go:196`
- **Проблема:** Обработчики не отслеживаются WaitGroup
- **Исправление:** Добавить tracking goroutine через WaitGroup

### 4. Nil pointer в callbacks
- **Файл:** `internal/bot/callbacks.go:36-61`
- **Проблема:** `CallbackQuery.Message` может быть nil
- **Исправление:** Добавить проверку перед доступом

### 5. Unbounded goroutines
- **Файл:** `cmd/bot/main.go:196`
- **Проблема:** Нет лимита на обработчики
- **Исправление:** Worker pool с buffered channel

---

## 🟠 P1 - Безопасность

### 6. Default credentials
- **Файл:** `internal/config/config.go:62-63`
- **Проблема:** admin/admin по умолчанию
- **Исправление:** Пустые значения или требовать явной настройки

### 7. Markdown injection в username
- **Файл:** `internal/bot/admin.go:55, 342`
- **Проблема:** Username вставляется в Markdown без экранирования
- **Исправление:** Sanitize спецсимволов (`*`, `_`, `[`, `]`, etc.)

### 8. uint overflow в /del
- **Файл:** `internal/bot/admin.go:92`
- **Проблема:** Отрицательные числа вызывают overflow
- **Исправление:** Проверка `id > 0` после парсинга

### 9. Error disclosure пользователю
- **Файл:** `internal/bot/admin.go:111`
- **Проблема:** Внутренние ошибки показываются пользователю
- **Исправление:** Общие сообщения, логирование деталей

---

## 🟡 P2 - Надежность

### 10. TOCTOU race в xui.Client
- **Файл:** `internal/xui/client.go:140-162`
- **Проблема:** Проверка сессии и использование разделены
- **Исправление:** Проверять и использовать под одной блокировкой

### 11. Context not checked в broadcast
- **Файл:** `internal/bot/admin.go:183-199`
- **Проблема:** Цикл broadcast не проверяет cancellation
- **Исправление:** `select` с ctx.Done()

### 12. Orphan 3x-ui clients
- **Файл:** `internal/bot/subscription.go:26`
- **Проблема:** Старые клиенты не удаляются
- **Исправление:** DeleteClient перед созданием нового

### 13. Error wrapping inconsistency
- **Файл:** Множество
- **Проблема:** Mix of `%w` и `%v`
- **Исправление:** Всегда использовать `%w` для ошибок

---

## 🟢 P3 - Потребление памяти

### 14. GetAllSubscriptions в память
- **Файл:** `internal/bot/admin.go:296`
- **Проблема:** Все подписки загружаются для подсчёта
- **Исправление:** Использовать CountActiveSubscriptions

### 15. GetAllTelegramIDs без пагинации
- **Файл:** `internal/bot/admin.go:183`
- **Проблема:** Все ID в память при broadcast
- **Исправление:** Batch processing или streaming

### 16. HTTP response bodies не закрываются
- **Файл:** `internal/xui/client.go:193, 301, 361, 401`
- **Проблема:** `_ = resp.Body.Close()` игнорирует ошибки
- **Исправление:** Defer close с проверкой

---

## 🔵 P4 - Качество кода

### 17. Gosec G104 включить
- **Файл:** `.golangci.yml`
- **Проблема:** Unhandled errors выключены
- **Исправление:** Включить и исправить ошибки

### 18. MockDatabaseService thread-safe
- **Файл:** `internal/testutil/testutil.go:80-224`
- **Проблема:** Map без мьютекса
- **Исправление:** Добавить sync.RWMutex

### 19. Retry jitter
- **Файл:** `internal/xui/client.go:455-482`
- **Проблема:** Нет jitter в exponential backoff
- **Исправление:** Добавить случайную задержку

---

## Доп. улучшения (по желанию)

### 20. TLS конфигурация
- Опция `XUI_SKIP_TLS_VERIFY` для self-signed

### 21. Request ID для логирования
- Trace ID для корреляции

### 22. Metrics endpoint
- Prometheus /metrics

### 23. Health check
- Детальный health check (DB, xui)

---

*Обновлено: 2026-03-24*
