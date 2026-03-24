# План улучшений — rs8kvn_bot

**Дата:** 2026-03-24
**Статус:** В работе
**Тестовое покрытие:** 51.3%

---

## 🔴 P0 - Критические баги (исправить немедленно)

> См. `doc/BUG_FIXES_PLAN.md` для деталей

| # | Проблема | Файл | Исправление |
|---|----------|------|-------------|
| 1 | **Goroutine leak** | `main.go:196` | WaitGroup для обработчиков |
| 2 | **Buffer pool race** | `client.go:178` | Не возвращать буфер в пул |
| 3 | **Nil pointer** | `callbacks.go:36-61` | Проверка Message != nil |
| 4 | **Unbounded goroutines** | `main.go:196` | Worker pool с semaphore |
| 5 | **uint overflow** | `admin.go:92` | Проверка id > 0 |
| 6 | **Orphan clients** | `subscription.go:26` | DeleteClient перед созданием |
| 7 | **Global DB state** | `database.go` | Удалить глобальную DB |

---

## 🟡 P1 - Завершённые задачи

- ✅ **1.1 Interfaces** — созданы `internal/interfaces/`
- ✅ **1.2 Global state** — logger.Init() возвращает Service
- ✅ **2.1 Duplication** — isAdmin(), utils/time.go
- ✅ **3.2 Integration tests** — `integration_test.go`
- ✅ **4.3 Circuit breaker** — `breaker.go`
- ✅ **5.1 golangci-lint** — уже в docker.yml
- ✅ **5.2 gosec** — уже в docker.yml
- ✅ **6.1 Unit tests** — qr_test.go, uuid_test.go, time_test.go

---

## 🟢 P2 - Новые идеи для улучшения

### Приоритет 2: Безопасность

**2.1 Admin ACL**
- Список разрешённых admin Telegram ID
- Защита от неавторизованного доступа

**2.2 Валидация input**
- Проверка Telegram ID (числовой)
- Sanitize username (Markdown escaping)

**2.3 Default credentials**
- Убрать admin/admin по умолчанию

---

### Приоритет 3: UX улучшения

**3.1 Улучшенные клавиатуры**
- Persistent keyboard для навигации

**3.2 Превью подписки**
- Показать данные до подтверждения

**3.3 /help команда**
- Разделение user vs admin команды

---

### Приоритет 4: Уведомления

**4.1 Expiry notifications**
- За 7/3/1 день до истечения

**4.2 Admin notifications**
- Уведомления о новых подписках

---

### Приоритет 5: Производительность

**5.1 Connection pool**
- Настройка MaxOpenConns/MaxIdleConns

**5.2 Кэш подписок**
- LRU кэш, TTL: 5 минут

**5.3 TLS конфигурация**
- Опция для self-signed сертификатов

---

### Приоритет 6: Качество кода

**6.1 Gosec включить G104**
- Исправить unhandled errors

**6.2 Logger interface**
- Использовать для DI или удалить

**6.3 MockDatabaseService**
- Сделать thread-safe

---

### Приоритет 7: Разное

**7.1 Webhook режим**
- Вместо long-polling

**7.2 Multi-bot поддержка**
- Несколько ботов с одной БД

**7.3 Метрики**
- uptime, users, subscriptions, errors

---

## Статус тестов

| Модуль | Покрытие | Статус |
|--------|----------|--------|
| cmd/bot | 5.1% | Пропущен (user request) |
| internal/bot | 9.7% | Пропущен (user request) |
| internal/config | 88.6% | ✅ |
| internal/database | 84.3% | ✅ |
| internal/xui | 89.1% | ✅ |
| internal/logger | 85.6% | ✅ |
| internal/utils | 80.0% | ✅ |
| internal/heartbeat | 95.8% | ✅ |
| internal/ratelimiter | 100% | ✅ |
| **Итого** | **51.3%** | |

---

*Обновлено: 2026-03-24*
