# План развития rs8kvn_bot

**Дата:** 2026-03-30  
**Версия:** v1.9.6  
**Покрытие тестами:** ~51%

---

## 🔴 HIGH - Критические

| # | Проблема | Файл |
|---|----------|------|
| 1 | Дублирование тестовых функций causing build failures | `internal/utils/time_test.go`, `internal/utils/time_extended_test.go` и другие |
| 2 | Half-open circuit breaker пропускает безлимитные запросы | `internal/xui/breaker.go` |
| 3 | Circuit breaker игнорирует отмену контекста | `internal/xui/breaker.go` |
| 4 | Невалидные env vars молча используют значения по умолчанию | `internal/config/config.go` |
| 5 | Команда `/del` — Sscanf парсит частичный ввод (`/del 5abc` → ID=5) | `internal/bot/admin.go` |
| 6 | Markdown инъекция в `/broadcast` — без санитизации | `internal/bot/admin.go` |
| 7 | Канал обновлений не дренируется при shutdown | `cmd/bot/main.go` |
| 8 | Утечка idle соединений HTTP transport при shutdown | `internal/xui/client.go` |
| 9 | containsSuccessKeywords ложные срабатывания ("not added" матчит "added") | `internal/xui/client.go` |
| 10 | Отсутствует индекс на username | `internal/database/database.go` |
| 11 | Ping/GetPoolStats не в интерфейсе DatabaseService | `internal/interfaces/interfaces.go` |

---

## 🟡 MEDIUM - Важные

| # | Проблема | Файл |
|---|----------|------|
| 12 | Дублирование паттерна "edit message with back button" (6+ мест) | `internal/bot/admin.go`, `internal/bot/menu.go` |
| 13 | Дублирование создания QR keyboard | `internal/bot/subscription.go` |
| 14 | Бизнес-логика смешана с презентацией в createSubscription | `internal/bot/subscription.go` |
| 15 | handleBackToStart и handleMenuHelp обходят кэш | `internal/bot/menu.go` |
| 16 | Хрупкая классификация ошибок через strings.Contains | `internal/bot/subscription.go` |
| 17 | XUI_SUB_PATH не валидируется на .. | `internal/config/config.go` |

---

## 🔵 LOW - Рефакторинг

| # | Проблема | Файл |
|---|----------|------|
| 18 | Отсутствие Request ID для логирования | `internal/logger` |
| 19 | Отсутствие Prometheus Metrics endpoint | `internal/health/health.go` |
| 20 | Отсутствие multi-admin поддержки | `internal/config/config.go` |

---

## 📋 Улучшения

| # | Задача | Файл |
|---|--------|------|
| 1 | TLS конфигурация (XUI_SKIP_TLS_VERIFY) | `internal/config/config.go`, `internal/xui/client.go` |
| 2 | Expiry notifications — за 7, 3, 1 день до истечения | `internal/bot/subscription.go`, `internal/bot/handlers.go` |
| 3 | Traffic warnings — при 80%, 90%, 100% трафика | `internal/bot/subscription.go`, `internal/bot/handlers.go` |

---

## 🎯 Новые функции

### Приоритет 1: Админ функции
- `/dashboard` — метрики активных пользователей
- `/extend <id> <days>` — продлить подписку
- `/settraffic <id> <gb>` — изменить лимит
- `/export` — экспорт подписок/статистики

### Приоритет 2: Пользовательские функции
- Multi-language (ru, en, zh, es)
- `/share` — генерация временной ссылки на подписку

### Приоритет 3: Тарифные планы
- Базовый: 50 GB / Стандарт: 150 GB / Премиум: 500 GB

### Приоритет 4: Промокоды

---

## 🌐 Мульти-серверность

**Концепция:** Один клиент на ВСЕХ серверах, ОДНА подписка со ВСЕМИ серверами

---

## 📝 План реализации

### Фаза 1: Стабильность (2 недели)
- [ ] Исправить дублирование тестовых функций (HIGH #1)
- [ ] Исправить все HIGH priority проблемы (#2-11)

### Фаза 2: Refactoring и улучшения кода (3 недели)
- [ ] Устранить дублирование кода (MEDIUM #12-16)
- [ ] Добавить Request ID для логирования (LOW #18)
- [ ] Добавить Prometheus Metrics endpoint (LOW #19)
- [ ] Добавить multi-admin поддержку (LOW #20)

### Фаза 3: Конфигурация и улучшения функционала (3 недели)
- [ ] TLS конфигурация (XUI_SKIP_TLS_VERIFY) (Улучшения #1)
- [ ] Expiry notifications — за 7, 3, 1 день до истечения (Улучшения #2)
- [ ] Traffic warnings — при 80%, 90%, 100% трафика (Улучшения #3)

### Фаза 4: Новые функции (4 недели)
- [ ] Админ функции: `/dashboard`, `/extend`, `/settraffic`, `/export`
- [ ] Пользовательские функции: Multi-language, `/share`
- [ ] Тарифные планы
- [ ] Промокоды

### Фаза 5: Мульти-серверность (5-6 недель)

---

**Обновлено:** 2026-03-30  
**Версия:** v1.9.6
