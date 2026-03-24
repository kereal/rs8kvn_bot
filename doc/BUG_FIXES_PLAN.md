# План исправления багов и улучшений

**Дата:** 2026-03-24
**Статус:** В работе
**Тестовое покрытие:** 51.3%

---

## 🔴 P0 - Критические баги

### 1. Goroutine leak на shutdown
- **Файл:** `cmd/bot/main.go:196`
- **Проблема:** Обработчики апдейтов не отслеживаются WaitGroup
- **Исправление:**
  - Добавить `sync.WaitGroup` для goroutine обработки апдейтов
  - При shutdown дождаться завершения всех обработчиков
  - Добавить context cancellation для graceful shutdown

### 2. Buffer pool race condition
- **Файл:** `internal/xui/client.go:178`
- **Проблема:** Буфер HTTP запроса возвращается в пул до завершения запроса
- **Исправление:**
  - Использовать `io.NopCloser(bytes.NewReader(body))` вместо пула
  - Или копировать буфер перед использованием в запросе

### 3. Nil pointer dereference в callbacks
- **Файл:** `internal/bot/callbacks.go:36-61`
- **Проблема:** `CallbackQuery.Message` не проверяется на nil
- **Исправление:**
  - Добавить проверку `if update.CallbackQuery.Message == nil`
  - Вернуть ошибку или пропустить обработку

### 4. Unbounded goroutine spawning
- **Файл:** `cmd/bot/main.go:196`
- **Проблема:** Нет лимита на количество goroutine обработки
- **Исправление:**
  - Использовать worker pool с buffered channel как semaphore
  - Лимит: 10-20 параллельных обработчиков

---

## 🟡 P1 - Логические баги

### 5. uint parsing позволяет отрицательные числа
- **Файл:** `internal/bot/admin.go:92`
- **Проблема:** `/del -5` вызывает overflow uint
- **Исправление:**
  - Добавить проверку `if id <= 0` после парсинга
  - Возвращать ошибку для невалидных ID

### 6. Orphan 3x-ui clients
- **Файл:** `internal/bot/subscription.go:26`
- **Проблема:** При пересоздании подписки старый клиент не удаляется
- **Исправление:**
  - Вызывать `DeleteClient` перед созданием нового
  - Добавить cleanup job для orphan clients

### 7. Dual database patterns
- **Файл:** `internal/database/database.go`
- **Проблема:** Глобальная переменная DB и Service pattern дублируются
- **Исправление:**
  - Удалить глобальную переменную DB
  - Использовать только Service pattern
  - Обновить migrations.go

---

## 🟢 P2 - Улучшения

### 8. Default credentials
- **Файл:** `internal/config/config.go:63-64`
- **Проблема:** admin/admin по умолчанию
- **Исправление:**
  - Установить пустые значения по умолчанию
  - Требовать явной настройки

### 9. Logger interface не используется
- **Файл:** `internal/interfaces/interfaces.go`
- **Проблема:** Интерфейс определён, но не используется для DI
- **Исправление:**
  - Удалить неиспользуемый интерфейс
  - Или внедрить в Handler

### 10. Gosec exclusions
- **Файл:** `.golangci.yml`
- **Проблема:** G104 (Unhandled errors) выключен
- **Исправление:**
  - Включить G104
  - Исправить все unhandled errors

---

## Дополнительные улучшения

### 11. TLS конфигурация
- Добавить опцию `XUI_SKIP_TLS_VERIFY` для self-signed сертификатов

### 12. Connection pooling
- Настроить `MaxOpenConns` и `MaxIdleConns` для SQLite

### 13. Кэширование подписок
- LRU кэш для статуса подписок
- TTL: 5 минут

### 14. Input sanitization
- Sanitize username перед использованием в сообщениях
- Экранировать Markdown символы

---

## Порядок выполнения

1. P0 баги (1-4) - критичные для стабильности
2. P1 баги (5-7) - логические ошибки
3. P2 улучшения (8-10) - безопасность и качество
4. Доп. улучшения (11-14) - по желанию

---

*Обновлено: 2026-03-24*
