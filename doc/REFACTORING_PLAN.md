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

### 1.1 Интерфейсы для компонентов ✅ (Выполнено)

**Сделано:**
- Создан пакет `internal/interfaces/` с интерфейсами `DatabaseService`, `XUIClient`, `Logger`
- Handler теперь принимает интерфейсы вместо конкретных типов
- Все методы БД принимают `context.Context`
- testutil обновлён с полными mock-реализациями

**Время:** 2-3 дня → выполнено
**Статус:** ✅ Готово

---

### 1.2 Убрать глобальное состояние ✅ (Выполнено)

**Сделано:**
- `logger.Init()` теперь возвращает `*Service` для dependency injection
- Global `Log` устанавливается из Service для обратной совместимости
- `main.go` использует Service pattern для БД и логгера
- Убран дублирующий вызов `database.Init()`

**Примечание:** `logger.Log` остаётся как singleton — стандартный Go паттерн для логгеров

**Время:** 2 дня → выполнено
**Статус:** ✅ Готово

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

### 2.1 Устранить дублирование ✅ (Выполнено)

**Сделано:**
- Добавлен метод `Handler.isAdmin(chatID int64)` в `handler.go`
- Создан `internal/utils/time.go` с `FirstSecondOfNextMonth`
- Все файлы обновлены: `commands.go`, `menu.go`, `admin.go`, `subscription.go`

**Время:** 2 часа → выполнено
**Статус:** ✅ Готово

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

### 3.2 Integration тесты ✅ (Выполнено)

**Сделано:**
- `internal/bot/integration_test.go` с `NewIntegrationTestFixture`
- In-memory SQLite база данных для изоляции
- `MockXUIServer` для мокирования x-ui API

**Добавлены тесты:**
- `TestSubscriptionFlow_CreateAndGet`
- `TestSubscriptionFlow_ExpiredSubscription`
- `TestSubscriptionFlow_RevokeOldSubscription`
- `TestAdminStats`
- `TestDatabaseService_GetAllTelegramIDs`
- `TestDatabaseService_GetByUsername`

**Также добавлено:**
- `internal/utils/time_test.go` — тесты для `FirstSecondOfNextMonth`
- `internal/interfaces/interfaces_test.go` — mock-тесты для интерфейсов

**Время:** 2 дня → выполнено
**Статус:** ✅ Готово

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

### 4.3 Circuit breaker для x-ui ✅ (Выполнено)

**Сделано:**
- Собственная реализация `CircuitBreaker` в `internal/xui/breaker.go`
- 9 тестов в `internal/xui/breaker_test.go`
- Константы в `config.CircuitBreakerMaxFailures` и `config.CircuitBreakerTimeout`

**Параметры:**
- Открывается после 5 неудачных запросов
- Через 30 сек переходит в half-open
- Закрывается после 3 успешных запросов
- Интегрирован в `ensureLoggedIn` → все операции защищены

**Время:** 4 часа → выполнено
**Статус:** ✅ Готово

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

### ✅ Выполнено

| Задача | Статус |
|--------|--------|
| 1.1 Интерфейсы | ✅ Готово |
| 1.2 Глобальное состояние | ✅ Готово |
| 2.1 Дублирование | ✅ Готово |
| 3.2 Integration тесты | ✅ Готово |
| 4.3 Circuit breaker | ✅ Готово |

### 🔄 Осталось

| Задача | Приоритет |
|--------|----------|
| 1.3 Разделить тестовые файлы | 🟡 P1 |
| 2.2 x-ui error handling | 🟡 P1 |
| 2.3 Panic recovery | 🟢 P2 |
| 3.1 Покрытие bot-пакета | 🔴 P0 |
| 3.3 Concurrent тесты | 🟢 P2 |
| 4.1 Health check | 🟡 P1 |
| 4.2 Prometheus metrics | 🟡 P1 |
| 4.4 Уведомления об истечении | 🟡 P1 |
| 4.5 Мульти-серверность | 🔴 P0 |
| 5.1 golangci-lint в CI | 🟢 P2 |
| 5.2 gosec | 🟢 P2 |

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
