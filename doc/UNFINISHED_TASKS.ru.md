# Невыполненные задачи после аудита rs8kvn_bot

**Дата:** 2026-04-19  
**Статус:** P0/P1-1, P1-2, P1-3 выполнены; P1-4 — P2-2 требуют реализации

---

## 🔄 Оставшиеся High/Medium приоритеты

### P1-1: Декомпозиция Handler God Object
**Серьёзность:** Высокая  
**Файл:** `internal/bot/handler.go` (был 331 строка)

**Прогресс:** ✅ Выполнено

**Сделано:**
- ✅ Создан `CommandHandler` (`internal/bot/command.go`) — `/start`, `/help`, `/invite`, deep link
- ✅ Создан `CallbackHandler` (`internal/bot/callback.go`) — все callback-обработчики меню/QR/админ
- ✅ Создан `SubscriptionHandler` (`internal/bot/subscription_handler.go`) — бизнес-логика подписок, кэш, коды QR
- ✅ Handler переписан как фасад: содержит `cmdHandler`, `cbHandler`, `subHandler`, `referral` + делегирующие методы
- ✅ Админ методы (`HandleDel`, `HandleBroadcast`, `HandleSend`, `HandleRefstats`, `HandleVersion`, `handleAdminStats`, `handleAdminLastReg`, `notifyAdmin`, `notifyAdminError`, `escapeMarkdown`) остались в `admin.go`
- ✅ Удалён `internal/bot/commands.go` (дублировал логику)
- ✅ Удалён `internal/bot/message.go` (методы перемещены в `handler.go`)
- ✅ Удалён `internal/bot/subscription.go` (методы перемещены в `subscription_handler.go`)
- ✅ Удалён дублирующий `internal/bot/admin_handler.go` (методы уже в `admin.go`)
- ✅ Исправлена data race в `HandleBroadcast` (использует `atomic` подсчёт)
- ✅ Реализован `StartCacheCleanup` (раньше был пустым)
- ✅ Удалён неиспользуемый `loadReferralCacheIfNeeded`
- ✅ Все тесты проходят (`go test -race ./...`), сборка зелёная

**Зачем:** Улучшение тестируемости, single responsibility, легче добавлять новые фичи.

---

### P1-2: Централизованная инвалидация кэша
**Серьёзность:** Высокая

**Прогресс:** ✅ Выполнено

**Сделано:**
- ✅ Добавлен `SubscriptionService.InvalidateSubscription(ctx, telegramID int64) error`
- ✅ Добавлен `SubscriptionService.SetInvalidateFunc(fn func(telegramID int64))` для DI коллбэка
- ✅ В `Handler.NewHandler` автоматически привязывается `h.cache.Invalidate` к сервису
- ✅ Все прямые вызовы `h.cache.Invalidate`/`invalidateCache` заменены на вызов сервиса:
  - `handleBindTrial` (command.go)
  - `getSubscriptionWithCache` (subscription_handler.go → теперь через `h.invalidateCache` → сервис)
  - `HandleDel` (admin.go)
- ✅ Удалён метод `(*SubscriptionHandler).invalidateCache` (больше не нужен)
- ✅ Обеспечена обратная совместимость: `Handler.invalidateCache` остаётся (используется тестами), но теперь делегирует в сервис
- ✅ Все тесты патачены: где вручную создавался `subscriptionService`, добавлен вызов `SetInvalidateFunc(handler.cache.Invalidate)`
- ✅ Проблемы с гонкой в `HandleBroadcast` (admin.go) устранены: заменён подсчёт `int` на `int64` с `atomic`
- ✅ Реализован `StartCacheCleanup` (раньше был пустым)
- ✅ Удалён неиспользуемый `loadReferralCacheIfNeeded`
- ✅ Удалён мёртвый файл `internal/bot/admin_handler.go` и поле `admin` из `Handler`
- ✅ Все тесты проходят (`go test -race ./...`), сборка зелёная

---

### P1-3: Orphaned XUI клиентов при rollback (background reconciler)
**Серьёзность:** Высокая  
**Файлы:** `internal/service/subscription.go`, `cmd/bot/main.go`

**Прогресс:** ✅ Выполнено

**Сделано:**
- ✅ Добавлен `SubscriptionService.ReconcileOrphanedClients(ctx) (int, error)`
  - Сканирует все активные подписки (`GetAllSubscriptions` + фильтр `status == "active"`).
  - Для каждой проверяет наличие в XUI через `GetClientTraffic` (лёгкий запрос).
  - Если клиент не найден (`"client not found"`), удаляет запись из DB.
  - Логирует количество удалённых записей.
- ✅ Запускается в `main.go` как фоновая горутина:
  - Первый запуск через 30 секунд после старта (для стабилизации XUI).
  - Повтор каждые 6 часов.
- ✅ Использует централизованную инвалидацию кэша при удалении подписки.
- ✅ Обрабатывает отмену контекста, не блокирует graceful shutdown.
- ✅ Безопасен при наличии большого количества подписок (пагинация не требуется, фоновый контекст).

**Зачем:** Автоматическая очистка "сирот" — предотвращает накопление мёртвых записей в БД, когда клиент удалён из XUI, но запись в БД остаётся.

---

### P1-4: Context-aware singleflight
**Серьёзность:** Высокая  
**Файлы:** `internal/web/singleflight.go`, `internal/web/web.go:576`

**Проблема:** `singleflight.Group.Do()` без контекста — при shutdown запросы накапливаются, горутины не корректно завершаются.

**Решение:**
- Создан `singleflight.SingleFlight` с методом `Do(ctx context.Context, key string, fn func(ctx context.Context) (interface{}, error)) (interface{}, error)`
- Поддерживает контекст: если контекст отменён до завершения `fn`, немедленно возвращает `ctx.Err()` и не ждёт
- Внутренняя карта `calls` защищена мьютексом, каждый ключ имеет свой `done` канал
- Используется в `handleSubscriptionProxy` вместо `golang.org/x/sync/singleflight.Group`

**Статус:** ✅ Выполнено

**Зачем:** Graceful shutdown без висячих горутин и утечек памяти.

---

### P2-1: Prometheus metrics
**Серьёзность:** Средняя  
**Эффект:** 2-3 часа

**Что добавить:**
- Request rates (HTTP handlers, bot updates)
- Error rates (XUI failures, DB errors, cache misses)
- Latency histograms (p50, p95, p99)
- Circuit breaker state (closed/open/half-open)
- Cache hit ratios (bot cache, subproxy cache)
- Active subscriptions, trial conversions

**Библиотека:** `prometheus/client_golang` (уже в go.mod)

---

### P2-2: Подготовка к миграции на PostgreSQL
**Серьёзность:** Средняя  
**Эффект:** 4-6 часов

**Текущее:** Жёсткая привязка к SQLite через GORM.

**Решение:**
1. Создать абстракцию над `database/sql` через интерфейс `DatabaseService`
2. Реализовать `sqlite3Service` и `postgresService`
3. Конфиг: `DATABASE_DRIVER=sqlite|postgres`
4. Миграции: оставить golang-migrate (работает и с PG)

**Зачем:** Масштабируемость (>10к пользователей), конкурентные записи.

---

## 📋 Статус P0 (всё сделано)

| Задача | Исходник | Статус |
|--------|----------|--------|
| Handler duplicate field | `handler.go:47,52` | ✅ Исправлено |
| Missing delegate methods | handlers_test.go | ✅ Добавлены |
| Test field name mismatch | 6 тестовых файлов | ✅ Переписано |
| Failing corrupt data test | subproxy_test.go:567-599 | ✅ Исправлен |
| XUI sentinel errors | Отсутствовали | ✅ Добавлены |
| Circuit breaker race | breaker.go:78-83 | ✅ Уже защищено |

---

**Следующие шаги:** Начать с **P1-4** (context-aware singleflight) — высокий приоритет, требуется для graceful shutdown.
