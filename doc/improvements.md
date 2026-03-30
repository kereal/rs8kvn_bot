# Улучшения кода

## HIGH приоритет

### 1. Двойной клик при создании подписки — orphan XUI клиенты
**Файл:** `internal/bot/subscription.go:44-78`

Если пользователь быстро нажмёт "создать подписку" дважды, могут быть созданы два XUI клиента, но в БД сохранится только одна запись.

**Исправление:** Добавить мьютекс на пользователя или distributed lock.

### 2. Ошибка БД = "нет подписки"
**Файлы:** `internal/bot/subscription.go:47`, `internal/bot/menu.go:18`

Код не различает `gorm.ErrRecordNotFound` от временных ошибок БД. Ошибка подключения тихо становится "нет подписки".

**Исправление:** Логировать ошибки БД отдельно от "не найдено".

### 3. Код приглашения не валидируется
**Файл:** `internal/web/web.go:210`

Код приглашения принимает любые символы из URL пути.

**Исправление:** Добавить валидацию regex: `^[a-zA-Z0-9_-]+$`

### 4. Email в URL без кодирования
**Файл:** `internal/xui/client.go:468`

`GetClientTraffic` использует email напрямую в URL без `url.PathEscape`.

**Исправление:** Использовать `url.PathEscape(email)`.

### 5. Валидация URL принимает произвольные схемы
**Файл:** `internal/config/config.go:196`

`validateURL` принимает `javascript:`, `file:` и т.д.

**Исправление:** Ограничить до `http` и `https`.

### 6. DeleteSubscriptionByID TOCTOU
**Файл:** `internal/database/database.go:630-643`

Чтение-удаление без транзакции.

**Исправление:** Обернуть в транзакцию.

### 7. Гонка в CleanupExpiredTrials
**Файл:** `internal/database/database.go:870-907`

Delete запрос делает повторный SELECT вместо использования ID из первого SELECT — новые записи могут быть удалены без очистки в xui.

**Исправление:** Использовать `WHERE id IN (...)` с ID из SELECT.

---

## MEDIUM приоритет

### 8. Half-open circuit breaker пропускает безлимитные запросы
**Файл:** `internal/xui/breaker.go:71`

В half-open состоянии ВСЕ запросы проходят (нет лимита).

**Исправление:** Ограничить до `halfOpenMax` параллельных запросов.

### 9. Circuit breaker игнорирует отмену контекста
**Файл:** `internal/xui/breaker.go:41`

Параметр `ctx` получается, но никогда не проверяется.

**Исправление:** Добавить проверку `ctx.Err()` в начале.

### 10. Невалидные int переменные окружения молча используют значения по умолчанию
**Файл:** `internal/config/config.go:226-238`

`XUI_INBOUND_ID=abc` молча использует значение 1.

**Исправление:** Логировать предупреждение при невалидном значении.

### 11. Команда `/del` - Sscanf парсит частичный ввод
**Файл:** `internal/bot/admin.go:90-95`

`/del 5abc` удаляет подписку с ID 5 (игнорирует `abc`).

**Исправление:** Использовать `strconv.ParseUint`.

### 12. Markdown инъекция в `/broadcast`
**Файл:** `internal/bot/admin.go:226-228`

Сообщение админа отправляется с ParseMode=Markdown без санитизации.

**Исправление:** Санитизировать или использовать MarkdownV2.

### 13. Канал обновлений не дренируется при shutdown
**Файл:** `cmd/bot/main.go:269-271`

Буферизированные обновления бросаются при shutdown.

**Исправление:** Дренировать канал перед выходом.

### 14. Утечка idle соединений HTTP transport
**Файл:** `internal/xui/client.go`

Нет `CloseIdleConnections()` при shutdown.

**Исправление:** Добавить метод `Close()`.

### 15. Health handlers принимают все HTTP методы
**Файл:** `internal/web/web.go`

Следует ограничить до GET/HEAD.

### 16. Non-admin молча игнорируется на admin callbacks
**Файл:** `internal/bot/callbacks.go:60-65`

### 17. containsSuccessKeywords ложные срабатывания
**Файл:** `internal/xui/client.go:530-535`

`"not added"` матчит `"added"`.

### 18. Ошибки QueryRow.Scan игнорируются в миграциях
**Файл:** `internal/database/database.go:209-214, 255-258`

### 19. Отсутствует индекс на username
**Файл:** `internal/database/database.go`

### 20. GetAllSubscriptions загружает всю таблицу
**Файл:** `internal/database/database.go:646-653`

---

## LOW приоритет (Рефакторинг)

### 21. Дублирование логики поиска пути миграций
**Файл:** `internal/database/database.go:150-166, 181-197`

### 22. Дублирование паттерна "edit message with back button" (6+ мест)
**Файлы:** `internal/bot/admin.go`, `internal/bot/menu.go`, `internal/bot/subscription.go`

### 23. Дублирование создания QR keyboard
**Файл:** `internal/bot/subscription.go:57-64, 119-126`

### 24. Boilerplate nil-check для update.Message
**Файлы:** `internal/bot/commands.go`, `internal/bot/admin.go`

### 25. Бизнес-логика смешана с презентацией в createSubscription
**Файл:** `internal/bot/subscription.go:196-301`

### 26. handleBackToStart и handleMenuHelp обходят кэш
**Файл:** `internal/bot/menu.go:18, 47`

### 27. Хрупкая классификация ошибок через strings.Contains
**Файл:** `internal/bot/subscription.go:222-235`

### 28. safeSend не хватает документации по rate limiting
**Файл:** `internal/bot/message.go`

### 29. Ping/GetPoolStats не в интерфейсе
**Файл:** `internal/interfaces/interfaces.go`

### 30. XUI_SUB_PATH не валидируется на ..
**Файл:** `internal/config/config.go:133`

### 31. UpdateClient хардкодит reset:30
**Файл:** `internal/xui/client.go:407`
