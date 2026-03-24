# План улучшений и рефакторинга — rs8kvn_bot

**Дата:** 2026-03-24
**Версия проекта:** v1.9.3+
**Go:** 1.25+

---

## Краткое резюме

Проект хорошо структурирован, имеет ~60% покрытие тестами, следует Go-идиомам. Основные проблемы:
- **Нет интерфейсов** для тестируемости
- **Глобальное состояние** (DB, Log)
- **Крупные файлы** (handlers_test.go: 1078 строк, database_test.go: 2628 строк)
- **Дублирование** (isAdmin, getFirstSecondOfNextMonth)
- **Низкое покрытие** bot-пакета (6.9%)

---

## Приоритет 1: Рефакторинг архитектуры

### 1.1 Интерфейсы для компонентов (🔴 Критично)

**Проблема:** Нет интерфейсов — сложно тестировать bot handlers без реальных зависимостей.

**Решение:** Определить интерфейсы в отдельном пакете.

```
internal/interfaces/
├── database.go    // DatabaseService interface
├── xui.go         // XUIClient interface  
├── notifier.go    // Notifier interface (Telegram)
└── metrics.go     // MetricsCollector interface
```

**Шаги:**
1. Создать пакет `internal/interfaces/`
2. Вынести интерфейсы из testutil в interfaces
3. Обновить Handler для приёма интерфейсов
4. Обновить testutil.MockDatabaseService и testutil.MockXUIClient

**Время:** 2-3 дня
**Приоритет:** 🔴 P0

---

### 1.2 Убрать глобальное состояние (🔴 Критично)

**Проблема:** `var DB *gorm.DB` и `var Log *zap.Logger` — deprecated, но всё ещё используются.

**Текущее состояние:**
- `database/database.go:51` — `var DB *gorm.DB`
- `logger/logger.go:25` — `var Log *zap.Logger`
- `main.go` инициализирует глобальные переменные, но компоненты используют их напрямую

**Решение:** Dependency injection через структуры.

**Шаги:**

1. **Logger:**
   - `NewService(dbPath, logLevel) (*Service, error)` вместо `Init()`
   - `Service` содержит `*zap.Logger`
   - Убрать глобальный `Log`

2. **Database:**
   - `NewService(dbPath) (*Service, error)` вместо `Init()`
   - `Service` содержит `*gorm.DB`
   - Убрать глобальный `DB`
   - Перенести методы `GetByTelegramID`, `CreateSubscription` и т.д. в `Service`

3. **Main.go:**
   - Создать `App` struct с внедрёнными зависимостями
   - Передать зависимости в Handler

**Время:** 2 дня
**Приоритет:** 🔴 P0

---

### 1.3 Разделить крупные файлы (🟡 Важно)

**Проблема:** 
- `internal/bot/handlers_test.go` — 1078 строк
- `internal/database/database_test.go` — 2628 строк

**Решение:**

```
internal/bot/
├── handlers_test.go           # → удалить
├── commands_test.go           # тесты команд
├── callbacks_test.go          # тесты callback-ов
├── subscription_test.go       # тесты подписки
└── admin_test.go              # тесты админ-команд

internal/database/
├── database_test.go           # → основные тесты
├── subscription_test.go        # тесты модели Subscription
├── service_test.go            # тесты Service
└── query_test.go              # тесты отдельных запросов
```

**Примечание:** Это организационное изменение, не менять логику тестов.

**Время:** 1 день
**Приоритет:** 🟡 P1

---

## Приоритет 2: Качество кода

### 2.1 Устранить дублирование (🟡 Важно)

**Найдено дублирование:**

| Код | Места |
|-----|-------|
| `isAdmin` проверка | `commands.go:37`, `admin.go:22,82,161,228,295`, `menu.go:19` |
| `getFirstSecondOfNextMonth` | `handler.go:72`, `subscription.go:161` |
| Admin ID константа | Повторяется в нескольких файлах |

**Решение:**

1. Вынести `isAdmin` в `handler.go` как метод `Handler.isAdmin(chatID int64) bool`
2. Создать `internal/utils/time.go` с `FirstSecondOfNextMonth`
3. Использовать `h.cfg.TelegramAdminID` вместо константы

**Время:** 2 часа
**Приоритет:** 🟡 P1

---

### 2.2 x-ui client: улучшить обработку ошибок (🟡 Важно)

**Проблема:** `xui/client.go:310-318` — fallback на основе ключевых слов в сообщении ненадёжен.

```go
// Текущий код (хрупкий)
if !simpleResp.Success && simpleResp.Msg != "" {
    if containsSuccessKeywords(simpleResp.Msg) {
        logger.Info("3x-ui returned success=false but operation appears successful")
    }
}
```

**Решение:** 
1. Логировать подозрительные ответы как warnings
2. Добавить метрику на такие случаи
3. Документировать известные edge cases

**Время:** 2 часа
**Приоритет:** 🟡 P1

---

### 2.3 Panic recovery централизация (🟢 Желательно)

**Проблема:** `main.go:221-230` и scheduler-ы содержат дублирующий panic recovery.

**Решение:** Создать утилиту:

```go
// internal/utils/recovery.go
func RecoverWithSentry(logger *zap.Logger) {
    if r := recover(); r != nil {
        sentry.CurrentHub().Recover(r)
        sentry.Flush(config.SentryPanicFlushTimeout)
        logger.Error("Panic recovered", zap.Any("panic", r), zap.String("stack", string(debug.Stack())))
    }
}
```

**Время:** 1 час
**Приоритет:** 🟢 P2

---

## Приоритет 3: Тестирование

### 3.1 Повысить покрытие bot-пакета (🔴 Критично)

**Текущее:** 6.9%
**Цель:** 60%+

**План:**

1. **Написать моки интерфейсов** (после 1.1)
2. **Тесты команд:**
   - `HandleStart` — с deep link и без
   - `HandleHelp` — базовый тест
   - `HandleDel` — валидация, несуществующий ID
   - `HandleBroadcast` — empty message, too long
   - `HandleSend` — валидация ID

3. **Тесты callback handlers:**
   - `handleGetSubscription` — новая подписка
   - `handleMySubscription` — active, expired, no subscription
   - `handleAdminStats` — не админ, админ
   - `handleBackToStart` — навигация

4. **Тесты keyboard:**
   - `getMainMenuKeyboard` — проверка кнопок
   - `getBackKeyboard` — проверка кнопки
   - `getAdminKeyboard` — проверка админских кнопок

**Время:** 3-4 дня
**Приоритет:** 🔴 P0

---

### 3.2 Integration тесты (🟡 Важно)

**Текущее:** только unit-тесты

**Нужно:**
1. Поднять test database (SQLite in-memory)
2. Mock x-ui server (httptest)
3. Подготовить test fixtures

```go
// internal/bot/integration_test.go
func TestSubscriptionFlow(t *testing.T) {
    // 1. Setup mock x-ui server
    // 2. Create handler with mock dependencies
    // 3. Simulate /start → callback → subscription creation
    // 4. Verify database state
    // 5. Verify x-ui API calls
}
```

**Время:** 2 дня
**Приоритет:** 🟡 P1

---

### 3.3 Тесты concurrent access (🟢 Желательно)

**Нет тестов для:**
- Rate limiter при параллельных запросах
- x-ui client session management
- Graceful shutdown

**Время:** 1 день
**Приоритет:** 🟢 P2

---

## Приоритет 4: Новые функциональности

### 4.1 Health check endpoint (🟡 Важно)

**Реализация:**

```go
// internal/health/health.go
type HealthChecker struct {
    db    interfaces.DatabaseService
    xui   interfaces.XUIClient
}

func (h *HealthChecker) Check(ctx context.Context) HealthStatus {
    return HealthStatus{
        Status:   "ok",
        Database: h.checkDB(),
        XUI:      h.checkXUI(),
    }
}
```

**Endpoint:** `GET /health` (опционально, запускать на отдельном порту)

**Время:** 2 часа
**Приоритет:** 🟡 P1

---

### 4.2 Prometheus metrics (🟡 Важно)

**Метрики:**

| Метрика | Тип | Описание |
|---------|-----|----------|
| `tgvpn_active_subscriptions` | Gauge | Активных подписок |
| `tgvpn_total_users` | Gauge | Всего пользователей |
| `tgvpn_xui_requests_total` | Counter | Запросов к x-ui |
| `tgvpn_xui_request_duration_seconds` | Histogram | Latency x-ui |
| `tgvpn_bot_messages_total` | Counter | Сообщений от бота |
| `tgvpn_subscription_created_total` | Counter | Созданных подписок |
| `tgvpn_subscription_expired_total` | Counter | Истёкших подписок |

**Endpoint:** `GET /metrics`

**Время:** 4 часа
**Приоритет:** 🟡 P1

---

### 4.3 Circuit breaker для x-ui (🟡 Важно)

**Проблема:** Бот зависает при недоступности x-ui панели.

**Решение:** Добавить circuit breaker.

```go
// internal/xui/breaker.go
import "github.com/rubyist/circuitbreaker"

cb := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
    Name:    "xui",
    MaxFail: 5,
    Timeout: 30 * time.Second,
})

result := cb.Call(func() (interface{}, error) {
    return client.Login(ctx)
})
```

**Альтернатива:** Использовать `sony/gobreaker` или написать простой встроенный.

**Время:** 4 часа
**Приоритет:** 🟡 P1

---

### 4.4 Уведомления об истечении подписки (🟡 Важно)

**Функция:**
1. Scheduler проверяет подписки каждые N часов
2. Отправляет напоминания за 7/3/1 дней
3. Отправляет уведомление в день истечения

**Структура:**

```go
// internal/notification/scheduler.go
func StartExpiryNotifier(ctx context.Context, db interfaces.DatabaseService, notifier interfaces.Notifier) {
    // Каждые 6 часов:
    // 1. SELECT * FROM subscriptions WHERE status='active' AND expiry_time BETWEEN NOW() AND NOW() + 7 days
    // 2. Для каждого отправить уведомление
}
```

**Сообщения:**
- За 7 дней: "Подписка истекает через неделю"
- За 3 дня: "Подписка истекает через 3 дня"
- За 1 день: "Подписка истекает завтра!"
- В день: "Подписка истекла 😢"

**Время:** 1 день
**Приоритет:** 🟡 P1

---

### 4.5 Мульти-серверность (🔴 Критично, уже в IMPROVEMENTS.md)

См. `doc/IMPROVEMENTS.md` раздел 4 — подробный план уже есть.

**Кратко:**
- YAML конфиг для серверов
- Один UUID на все серверы
- HTTP endpoint `/sub/{subID}` для генерации конфига
- Graceful degradation при недоступности сервера

**Время:** 3 дня
**Приоритет:** 🔴 P0

---

## Приоритет 5: CI/CD

### 5.1 golangci-lint в CI (🟢 Желательно)

**Добавить в GitHub Actions:**

```yaml
- name: Run golangci-lint
  uses: golangci-lint-action@v6
  with:
    version: latest
```

**Время:** 1 час
**Приоритет:** 🟢 P2

---

### 5.2 Security scan (gosec) (🟢 Желательно)

```yaml
- name: Run gosec
  uses: securego/gosec@master
  with:
    args: '-no-fail ./...'
```

**Время:** 1 час
**Приоритет:** 🟢 P2

---

## Итоговый план по приоритетам

### Фаза 1: Архитектура (1 неделя)

| Задача | Время | Приоритет |
|--------|-------|----------|
| 1.1 Интерфейсы | 3 дня | 🔴 P0 |
| 1.2 Убрать глобальное состояние | 2 дня | 🔴 P0 |
| 1.3 Разделить тестовые файлы | 1 день | 🟡 P1 |

### Фаза 2: Качество кода (2 дня)

| Задача | Время | Приоритет |
|--------|-------|----------|
| 2.1 Устранить дублирование | 2 часа | 🟡 P1 |
| 2.2 Улучшить x-ui error handling | 2 часа | 🟡 P1 |
| 2.3 Panic recovery централизация | 1 час | 🟢 P2 |

### Фаза 3: Тестирование (1 неделя)

| Задача | Время | Приоритет |
|--------|-------|----------|
| 3.1 Покрытие bot-пакета (6.9% → 60%) | 4 дня | 🔴 P0 |
| 3.2 Integration тесты | 2 дня | 🟡 P1 |
| 3.3 Concurrent тесты | 1 день | 🟢 P2 |

### Фаза 4: Новые фичи (2-3 недели)

| Задача | Время | Приоритет |
|--------|-------|----------|
| 4.1 Health check | 2 часа | 🟡 P1 |
| 4.2 Prometheus metrics | 4 часа | 🟡 P1 |
| 4.3 Circuit breaker | 4 часа | 🟡 P1 |
| 4.4 Уведомления об истечении | 1 день | 🟡 P1 |
| 4.5 Мульти-серверность | 3 дня | 🔴 P0 |

### Фаза 5: CI/CD (полдня)

| Задача | Время | Приоритет |
|--------|-------|-----------|
| 5.1 golangci-lint в CI | 1 час | 🟢 P2 |
| 5.2 gosec | 1 час | 🟢 P2 |

---

## Метрики успеха

| Метрика | До | После |
|---------|-----|-------|
| Покрытие тестами | ~60% | 80%+ |
| Покрытие bot-пакета | 6.9% | 60%+ |
| Интерфейсы | 0 | 4+ |
| Глобальные переменные | 2 | 0 |
| Крупные файлы (>500 строк) | 2 | 0 |
| Critical lint errors | ? | 0 |

---

## Быстрые победы (за 1 день)

1. **Централизация panic recovery** — 1 час
2. **Устранение дублирования isAdmin** — 30 минут
3. **golanci-lint в CI** — 1 час
4. **Health check endpoint** — 2 часа
5. **Circuit breaker** — 4 часа

---

## Рекомендуемый порядок действий

1. **Сначала:** Интерфейсы (1.1) + глобальное состояние (1.2)
   → Это foundation для всего остального

2. **Параллельно:** Улучшение тестов (3.1) + дублирование (2.1)
   → Без интерфейсов сложно тестировать

3. **После:** Новые фичи (4.x) + CI/CD (5.x)
   → На стабильной базе

4. **В любое время:** Быстрые победы
   → Мотивируют команду

---

*Создано: 2026-03-24*
