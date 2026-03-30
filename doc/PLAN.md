# План развития rs8kvn_bot

**Дата:** 2026-03-30  
**Версия:** v1.9.6  
**Покрытие тестами:** ~51%

---

## 🔴 HIGH - Критические

*Все критические баги исправлены*

---

## 🟡 MEDIUM - Важные

| # | Проблема | Файл |
|---|----------|------|
| 8 | Half-open circuit breaker пропускает безлимитные запросы | `internal/xui/breaker.go` |
| 9 | Circuit breaker игнорирует отмену контекста | `internal/xui/breaker.go` |
| 10 | Невалидные env vars молча используют значения по умолчанию | `internal/config/config.go` |
| 11 | Команда `/del` — Sscanf парсит частичный ввод (`/del 5abc` → ID=5) | `internal/bot/admin.go` |
| 12 | Markdown инъекция в `/broadcast` — без санитизации | `internal/bot/admin.go` |
| 13 | Канал обновлений не дренируется при shutdown | `cmd/bot/main.go` |
| 14 | Утечка idle соединений HTTP transport при shutdown | `internal/xui/client.go` |
| 15 | containsSuccessKeywords ложные срабатывания ("not added" матчит "added") | `internal/xui/client.go` |
| 16 | Отсутствует индекс на username | `internal/database/database.go` |
| 17 | Ping/GetPoolStats не в интерфейсе DatabaseService | `internal/interfaces/interfaces.go` |

---

## 🔵 LOW - Рефакторинг

| # | Проблема | Файл |
|---|----------|------|
| 18 | Дублирование паттерна "edit message with back button" (6+ мест) | `internal/bot/admin.go`, `internal/bot/menu.go` |
| 19 | Дублирование создания QR keyboard | `internal/bot/subscription.go` |
| 20 | Бизнес-логика смешана с презентацией в createSubscription | `internal/bot/subscription.go` |
| 21 | handleBackToStart и handleMenuHelp обходят кэш | `internal/bot/menu.go` |
| 22 | Хрупкая классификация ошибок через strings.Contains | `internal/bot/subscription.go` |
| 23 | XUI_SUB_PATH не валидируется на .. | `internal/config/config.go` |

---

## 📋 Улучшения

| # | Задача | Файл |
|---|--------|------|
| 1 | TLS конфигурация (XUI_SKIP_TLS_VERIFY) | `internal/config/config.go`, `internal/xui/client.go` |
| 2 | Request ID для логирования | `internal/logger` |
| 3 | Prometheus Metrics endpoint | `internal/health/health.go` |
| 4 | Multi-admin поддержка (TELEGRAM_ADMIN_IDS) | `internal/config/config.go` |

---

## 🎯 Новые функции

### Приоритет 1: Уведомления
- Expiry notifications — за 7, 3, 1 день до истечения
- Traffic warnings — при 80%, 90%, 100% трафика

### Приоритет 2: Админ функции
- `/dashboard` — метрики активных пользователей
- `/extend <id> <days>` — продлить подписку
- `/settraffic <id> <gb>` — изменить лимит
- `/export` — экспорт подписок/статистики

### Приоритет 3: Пользовательские функции
- Multi-language (ru, en, zh, es)
- `/share` — генерация временной ссылки на подписку

### Приоритет 4: Тарифные планы
- Базовый: 50 GB / Стандарт: 150 GB / Премиум: 500 GB

### Приоритет 5: Промокоды

---

## 🌐 Мульти-серверность

**Концепция:** Один клиент на ВСЕХ серверах, ОДНА подписка со ВСЕМИ серверами

---

## 📝 План реализации

### Фаза 1: Стабильность (1 неделя)
- [ ] HIGH #1-7 (все выполнены)

### Фаза 2: Observability (2 недели)
- [ ] MEDIUM #8-17
- [ ] Prometheus metrics
- [ ] Request ID logging

### Фаза 3: Конфигурация (1 неделя)
- [ ] TLS configuration
- [ ] Multi-admin support

### Фаза 4: Пользовательские функции (3-4 недели)
- [ ] Expiry notifications
- [ ] Admin dashboard
- [ ] Multi-language

### Фаза 5: Мульти-серверность (4-6 недель)

---

**Обновлено:** 2026-03-30  
**Версия:** v1.9.6
