# Детальный план исправлений — Безопасность, Надежность, Память

**Дата:** 2026-03-24
**Покрытие:** 49.6%
**Статус:** P0 и P1 завершены

---

## ✅ P0 - Критические баги (Завершены)

### 1. Path Traversal в backup
- **Файл:** `internal/backup/backup.go`
- **Проблема:** Функция `validatePath()` использовала `filepath.Clean()` ДО проверки на `..`, что позволяло обход защиты через нормализацию пути. Например, `../etc/passwd` после Clean становился `/home/user/etc/passwd` без `..`.
- **Риск:** Злоумышленник мог заставить бот делать бэкап произвольных файлов.
- **Исправление:** Проверка `strings.Contains(path, "..")` теперь выполняется ДО `filepath.Abs()` и `filepath.Clean()`. Добавлены проверки на пустой путь и системные директории (`/dev/`, `/var/run/`).

### 2. Buffer pool race condition
- **Файл:** `internal/xui/client.go`
- **Проблема:** `sync.Pool` использовался для буферов HTTP запросов. Буфер возвращался в пул через `defer putBuffer(buf)` до завершения HTTP запроса. HTTP клиент читает тело асинхронно, что приводило к race condition — буфер мог быть переиспользован и очищен во время чтения.
- **Риск:** Повреждение тела запроса, потеря данных, некорректные ответы от 3x-ui.
- **Исправление:** Заменён `sync.Pool` на `marshalJSON()`, которая возвращает `*bytes.Reader`. Каждый запрос получает свой неизменяемый reader, исключая race condition. Пул убран полностью.

### 3. Goroutine leak на shutdown
- **Файл:** `cmd/bot/main.go`
- **Проблема:** Каждый обработчик апдейтов запускался в отдельной goroutine (`go handleUpdateSafely()`), но эти goroutines не отслеживались `sync.WaitGroup`. При shutdown goroutines могли всё ещё выполняться после закрытия БД, что приводило к panic.
- **Риск:** Panic при graceful shutdown, потеря данных, некорректное завершение.
- **Исправление:**
  - Добавлен `updatesWg sync.WaitGroup` для отслеживания goroutines обработчиков
  - Каждый `go handleUpdateSafely()` теперь вызывает `wg.Add(1)` и `wg.Done()`
  - При shutdown ждём завершения всех обработчиков перед закрытием БД

### 4. Nil pointer dereference в callbacks
- **Файл:** `internal/bot/callbacks.go`
- **Проблема:** `update.CallbackQuery.Message` не проверялся на `nil`. При callback из inline-режима или удалённых сообщений `Message` может быть nil, что вызывало panic при доступе к `Message.Chat.ID`.
- **Риск:** Crash бота при получении определённых типов callback queries.
- **Исправление:** Добавлена проверка `if update.CallbackQuery.Message == nil` с логированием и ответом пользователю. Callback отвечается корректно даже при отсутствии сообщения.

### 5. Unbounded goroutine spawning
- **Файл:** `cmd/bot/main.go`
- **Проблема:** При быстром потоке сообщений (спам кнопками) создавалось неограниченное количество goroutines. Каждая goroutine потребляет ~2-8KB стека плюс ресурсы на обработку.
- **Рisk:** Memory exhaustion (OOM) при атаке или высокой нагрузке.
- **Исправление:**
  - Добавлен semaphore: `updateSem := make(chan struct{}, MaxConcurrentHandlers)`
  - Константа `MaxConcurrentHandlers = 10` в `internal/config/constants.go`
  - Перед запуском goroutine записываем в semaphore, блокируясь при исчерпании лимита

---

## ✅ P1 - Безопасность (Завершены)

### 6. Default credentials
- **Файл:** `internal/config/config.go`
- **Проблема:** XUIUsername и XUIPassword имели значения по умолчанию `"admin"` и `"admin"`. Если переменные окружения не были заданы (например, при отсутствии .env файла), бот пытался подключиться к 3x-ui с стандартными учётными данными.
- **Рisk:** Доступ к панели 3x-ui если она использует default credentials. Уязвимость "security by obscurity".
- **Исправление:** Значения по умолчанию заменены на пустые строки. Валидация (`validate()`) требует явного указания `XUI_USERNAME` и `XUI_PASSWORD`.

### 7. Markdown injection в username
- **Файл:** `internal/bot/handler.go`
- **Проблема:** Имя пользователя вставлялось напрямую в Markdown-форматированные сообщения. Telegram поддерживает Markdown разметку (`*жирный*`, `_курсив_`, `[ссылка](url)`). Злоумышленник мог задать username с Markdown-спецсимволами.
- **Рisk:** Фишинг через поддельные ссылки в сообщениях бота, искажение форматирования.
- **Исправление:** Добавлена функция `sanitizeForMarkdown()` которая заменяет спецсимволы (`*`, `_`, `` ` ``, `[`, `]`, `(`, `)`) на визуально похожие Unicode символы (fullwidth variants).

### 8. uint overflow в /del команде
- **Файл:** `internal/bot/admin.go`
- **Проблема:** `fmt.Sscanf(args, "%d", &id)` где `id uint`. При вводе отрицательного числа (например `/del -5`) `Sscanf` парсит число и оно переполняется в огромное uint значение.
- **Рisk:** Попытка удалить несуществующую подписку с огромным ID, потенциальные побочные эффекты.
- **Исправление:** Парсинг через `int64` с последующей проверкой `if parsedID <= 0` и явным приведением к `uint`.

### 9. Error disclosure (частично)
- **Файл:** `internal/bot/admin.go`
- **Проблема:** Внутренние ошибки (stack traces, DB errors, 3x-ui ошибки) показывались пользователю в Telegram.
- **Рisk:** Утечка информации о внутренней архитектуре, путей файлов, версий.
- **Исправление:** Частично — ошибки 3x-ui больше не показываются пользователю. Нужно продолжить работу: добавить обобщённые сообщения для всех ошибок.

---

## 🟡 P2 - Надежность (Требует исправления)

### 10. TOCTOU race в xui.Client
- **Файл:** `internal/xui/client.go:140-162`
- **Проблема:** `doEnsureLoggedIn()` проверяет валидность сессии под `RLock`, затем разблокирует, затем выполняет запрос. Между проверкой и использованием сессия может протухнуть или быть инвалидирована другой goroutine.
- **Рisk:** Некорректные запросы к 3x-ui, потеря сессии.
- **Исправление:** Проверку и использование сессии выполнять под одной блокировкой или использовать atomic флаг.

### 11. Context not checked в broadcast loop
- **Файл:** `internal/bot/admin.go:183-199`
- **Проблема:** Цикл отправки сообщений всем пользователям не проверяет `ctx.Done()`. При shutdown бота broadcast продолжает работать.
- **Рisk:** Broadcast продолжается после инициирования shutdown, возможна отправка в закрытый канал.
- **Исправление:** Добавить `select` с `ctx.Done()` внутрь цикла.

### 12. Orphan 3x-ui clients
- **Файл:** `internal/bot/subscription.go:26`
- **Проблема:** При пересоздании подписки старый клиент в 3x-ui не удаляется. Создаётся новый, но старый остаётся в панели.
- **Рisk:** Накопление orphan clients в 3x-ui, исчерпание лимитов.
- **Исправление:** Перед созданием нового клиента вызывать `xui.DeleteClient()` для старого.

### 13. Error wrapping inconsistency
- **Файл:** Множество файлов
- **Проблема:** Смешанное использование `%w` (unwrap для errors.Is/As) и `%v` (простое строковое представление).
- **Рisk:** Невозможность корректно проверить тип ошибки через `errors.Is()`.
- **Исправление:** Везде использовать `%w` для обёртки ошибок.

---

## 🟢 P3 - Потребление памяти (Требует исправления)

### 14. GetAllSubscriptions для подсчёта
- **Файл:** `internal/bot/admin.go:296`
- **Проблема:** `GetAllSubscriptions()` загружает ВСЕ подписки в память только чтобы посчитать количество. Для 1000+ подписок это ~1-10MB.
- **Рisk:** Memory spike при large userbase.
- **Исправление:** Использовать `CountActiveSubscriptions()` и `CountExpiredSubscriptions()` которые используют SQL `COUNT`.

### 15. GetAllTelegramIDs без пагинации
- **Файл:** `internal/bot/admin.go:183`
- **Проблема:** При broadcast все Telegram ID загружаются в память. Для 10000+ пользователей это ~80KB только для ID, плюс overhead.
- **Рisk:** Memory spike при large userbase.
- **Исправление:** Batch processing с cursor-based pagination или streaming.

### 16. HTTP response bodies
- **Файл:** `internal/xui/client.go:193, 301, 361, 401`
- **Проблема:** `defer func() { _ = resp.Body.Close() }()` игнорирует ошибки закрытия.
- **Рisk:** Утечка file descriptors при частых запросах.
- **Исправление:** Логировать ошибки закрытия (DEBUG level) или использовать helper функцию.

---

## 🔵 P4 - Качество кода

### ✅ 18. MockDatabaseService thread-safe
- **Файл:** `internal/testutil/testutil.go`
- **Статус:** ✅ Завершено
- **Исправление:** Добавлен `sync.RWMutex` с `RLock/RUnlock` для чтения и `Lock/Unlock` для записи.

### ⬜ 17. Gosec G104
- **Файл:** `.golangci.yml`
- **Проблема:** Исключение G104 (unhandled errors) ослабляет проверки безопасности.
- **Исправление:** Убрать G104 из excludes и исправить все unhandled errors.

### ⬜ 19. Retry jitter
- **Файл:** `internal/xui/client.go:455-482`
- **Проблема:** Exponential backoff без jitter. При одновременных сбоях все клиенты ретраятся в одном времени (thundering herd).
- **Исправление:** Добавить `time.Duration(rand.Intn(1000)) * time.Millisecond` к задержке.

---

## Доп. улучшения (Требует исправления)

### ⬜ 20. TLS конфигурация
- **Проблема:** Нет опции для self-signed сертификатов.
- **Исправление:** Добавить `XUI_SKIP_TLS_VERIFY` или `XUI_CA_CERT` в конфиг.

### ⬜ 21. Request ID для логирования
- **Проблема:** Трудно трассировать цепочку событий в логах.
- **Исправление:** Добавить trace ID генерируемый для каждого запроса.

### ⬜ 22. Metrics endpoint
- **Проблема:** Нет метрик для мониторинга.
- **Исправление:** Добавить `/metrics` endpoint с Prometheus форматом.

### ⬜ 23. Health check
- **Проблема:** Docker healthcheck только проверяет `pgrep`.
- **Исправление:** HTTP endpoint проверяющий БД и 3x-ui connectivity.

---

## Статус тестов

| Модуль | Покрытие | Комментарий |
|--------|----------|-------------|
| cmd/bot | 4.5% | Пропущен (user request) |
| internal/bot | 9.9% | Пропущен (user request) |
| internal/config | 88.6% | ✅ Отлично |
| internal/database | 84.3% | ✅ Хорошо |
| internal/xui | 88.3% | ✅ Хорошо |
| internal/logger | 85.6% | ✅ Хорошо |
| internal/utils | 80.0% | ✅ Хорошо |
| internal/heartbeat | 95.8% | ✅ Отлично |
| internal/ratelimiter | 100% | ✅ Полное |
| **Итого** | **49.6%** | |

---

## Рекомендации по приоритетам

**Критично (делать сейчас):**
1. P2: TOCTOU race (10)
2. P2: Context not checked (11)
3. P2: Orphan clients (12)

**Важно (делать скоро):**
4. P3: Memory optimization (14, 15)
5. P4: Gosec G104 (17)
6. P4: Retry jitter (19)

**Можно подождать:**
7. Доп: TLS, Metrics, Health check (20-23)

---

*Обновлено: 2026-03-24*
