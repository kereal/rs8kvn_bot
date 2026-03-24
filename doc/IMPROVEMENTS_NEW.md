# План улучшений и новых функций — rs8kvn_bot

**Дата:** 2026-03-24
**Версия:** v1.9.6
**Статус:** Предложения

---

## 🔧 Технические улучшения

### Приоритет 1: Производительность

#### 1.1 Кэширование подписок
- **Проблема:** Каждый запрос к боту делает SQL запрос к БД
- **Решение:** LRU кэш в памяти с TTL 5 минут
- **Файл:** `internal/bot/cache.go` (новый)
- **Эффект:** Снижение нагрузки на БД на 80%

#### 1.2 Batch операции
- **Проблема:** Broadcast загружает все ID в память
- **Решение:** Cursor-based pagination с батчами по 100
- **Файл:** `internal/bot/admin.go`
- **Эффект:** Снижение памяти для 10k+ пользователей

#### 1.3 Connection pool мониторинг
- **Проблема:** Нет видимости использования соединений
- **Решение:** Логирование stats каждые 5 минут
- **Файл:** `internal/database/database.go`
- **Эффект:** Видимость для отладки

### Приоритет 2: Надёжность

#### 2.1 Health check endpoint
- **Проблема:** Docker healthcheck только проверяет процесс
- **Решение:** HTTP endpoint на порту 8080 с проверкой БД и x-ui
- **Файл:** `internal/health/health.go` (новый)
- **Эндпоинты:**
  - `GET /healthz` — базовая проверка
  - `GET /readyz` — готовность принимать запросы
  - `GET /metrics` — Prometheus метрики

#### 2.2 Graceful degradation
- **Проблема:** Сбой x-ui блокирует весь бот
- **Решение:** Кэширование последнего успешного состояния
- **Файл:** `internal/xui/client.go`
- **Эффект:** Бот работает в read-only режиме при сбое x-ui

#### 2.3 Retry with jitter
- **Проблема:** Thundering herd при одновременных сбоях
- **Решение:** Добавить random jitter к exponential backoff
- **Файл:** `internal/xui/client.go`
- **Формула:** `delay * (1 + rand.Float64())`

### Приоритет 3: Конфигурация

#### 3.1 YAML конфиг
- **Проблема:** Env vars неудобны для сложных настроек
- **Решение:** Поддержка config.yaml с env vars переопределением
- **Файл:** `internal/config/config.go`
- **Структура:**
```yaml
telegram:
  bot_token: "${TELEGRAM_BOT_TOKEN}"
  admin_id: 85939687000

xui:
  host: "https://panel.example.com"
  username: "${XUI_USERNAME}"
  password: "${XUI_PASSWORD}"
  inbound_id: 1
  sub_path: "sub"

database:
  path: "./data/tgvpn.db"
  max_open_conns: 10

subscription:
  traffic_limit_gb: 100
  expiry_notify_days: [7, 3, 1]
```

#### 3.2 Multi-admin поддержка
- **Проблема:** Только один админ
- **Решение:** Список admin IDs
- **Файл:** `internal/config/config.go`
- **Формат:** `TELEGRAM_ADMIN_IDS=123,456,789`

---

## 🎯 Новые функции

### Приоритет 1: Уведомления

#### 1.1 Expiry notifications
- **Описание:** Уведомления об истечении подписки
- **Время:** За 7, 3, 1 день до истечения
- **Файл:** `internal/bot/notifications.go` (новый)
- **Фоновая задача:** Ежедневная проверка в 09:00

#### 1.2 Traffic warnings
- **Описание:** Предупреждение при использовании 80%, 90%, 100% трафика
- **Файл:** `internal/bot/notifications.go`
- **Интеграция:** Проверка при каждом `/my` запросе

#### 1.2 Payment reminders
- **Описание:** Напоминание об оплате для истекающих подписок
- **Файл:** `internal/bot/notifications.go`

### Приоритет 2: Админ функции

#### 2.1 Admin dashboard
- **Описание:** Расширенная статистика в реальном времени
- **Команда:** `/dashboard`
- **Метрики:**
  - Активные пользователи за 24ч
  - Новые подписки за период
  - Использование трафика
  - Ошибки x-ui
- **Файл:** `internal/bot/dashboard.go` (новый)

#### 2.2 Subscription management
- **Описание:** Продление/изменение подписок
- **Команды:**
  - `/extend <id> <days>` — продлить
  - `/settraffic <id> <gb>` — изменить лимит
  - `/ban <id>` — заблокировать
- **Файл:** `internal/bot/admin.go`

#### 2.3 Export functionality
- **Описание:** Экспорт данных в CSV
- **Команда:** `/export [subscriptions|stats]`
- **Файл:** `internal/bot/export.go` (новый)

### Приоритет 3: Пользовательские функции

#### 3.1 Multi-language support
- **Описание:** Поддержка нескольких языков
- **Реализация:** i18n с JSON файлами
- **Поддерживаемые:** ru, en, zh, es
- **Файл:** `internal/i18n/` (новый директория)
- **Команда:** `/lang <code>`

#### 3.2 Subscription sharing
- **Описание:** Возможность поделиться подпиской
- **Команда:** `/share`
- **Реализация:** Генерация временной ссылки (24ч)
- **Файл:** `internal/bot/share.go` (новый)

#### 3.3 Connection instructions
- **Описание:** Подробные инструкции для разных клиентов
- **Команда:** `/instructions <client>`
- **Клиенты:** v2rayNG, Streisand, Hiddify, Clash
- **Файл:** `internal/bot/instructions.go` (новый)

#### 3.4 QR code improvements
- **Описание:** QR с логотипом и branding
- **Файл:** `internal/utils/qr.go`

---

## 🔒 Безопасность

### Приоритет 1: Аутентификация

#### 1.1 Admin API key
- **Описание:** API ключ для админских операций
- **Использование:** Веб-интерфейс, внешние интеграции
- **Файл:** `internal/auth/auth.go` (новый)

#### 1.2 Rate limiting improvements
- **Описание:** Per-user rate limiting
- **Лимиты:**
  - Обычные пользователи: 10/мин
  - Админы: 60/мин
- **Файл:** `internal/ratelimiter/ratelimiter.go`

### Приоритет 2: Аудит

#### 2.1 Audit logging
- **Описание:** Логирование всех админских действий
- **Действия:** delete, send, broadcast, extend
- **Файл:** `internal/logger/audit.go` (новый)

#### 2.2 Access logging
- **Описание:** Логирование всех запросов бота
- **Формат:** JSON с user_id, action, timestamp
- **Файл:** `internal/logger/access.go` (новый)

---

## 📊 Мониторинг

### Приоритет 1: Метрики

#### 1.1 Prometheus metrics
- **Описание:** Стандартные метрики для Prometheus
- **Метрики:**
  - `bot_requests_total` — общее количество запросов
  - `bot_requests_duration_seconds` — время обработки
  - `bot_subscriptions_active` — активные подписки
  - `bot_xui_errors_total` — ошибки x-ui
  - `bot_database_errors_total` — ошибки БД
- **Файл:** `internal/metrics/metrics.go` (новый)

#### 1.2 Structured logging
- **Описание:** JSON логи для ELK/Graylog
- **Поля:** timestamp, level, user_id, action, duration, error
- **Файл:** `internal/logger/logger.go`

### Приоритет 2: Алерты

#### 2.1 Health alerts
- **Описание:** Уведомления админу о проблемах
- **События:**
  - x-ui недоступен > 5 минут
  - БД ошибка
  - Memory usage > 80%
- **Файл:** `internal/health/alerts.go` (новый)

---

## 🐳 DevOps

### Приоритет 1: Docker

#### 1.1 Multi-stage improvements
- **Описание:** Оптимизация размера образа
- **Текущий:** ~30MB с UPX
- **Цель:** ~20MB без UPX
- **Файл:** `Dockerfile`

#### 1.2 Docker Compose improvements
- **Описание:** Production-ready compose
- **Добавить:**
  - healthcheck с реальной проверкой
  - log rotation
  - volume backups
- **Файл:** `docker-compose.yml`

### Приоритет 2: CI/CD

#### 2.1 Automated releases
- **Описание:** Автоматический changelog и release notes
- **Файл:** `.github/workflows/release.yml` (новый)

#### 2.2 E2E tests
- **Описание:** End-to-end тесты с тестовым ботом
- **Файл:** `.github/workflows/e2e.yml` (новый)

#### 2.3 Security scanning
- **Описание:** Trivy для сканирования зависимостей
- **Файл:** `.github/workflows/security.yml` (новый)

---

## 📝 Документация

### Приоритет 1: API docs
- **Описание:** Документация для интеграций
- **Формат:** OpenAPI/Swagger
- **Файл:** `docs/api.yaml` (новый)

### Приоритет 2: Architecture docs
- **Описание:** Документация архитектуры
- **Темы:**
  - Data flow diagrams
  - Component interaction
  - Deployment guide
- **Файл:** `docs/architecture.md` (новый)

### Приоритет 3: Runbook
- **Описание:** Инструкции для операций
- **Темы:**
  - Troubleshooting
  - Backup/restore
  - Scaling
- **Файл:** `docs/runbook.md` (новый)

---

## 📋 План реализации

### Фаза 1: Stability (1-2 недели)
- [ ] 1.1 Health check endpoint
- [ ] 1.2 Expiry notifications
- [ ] 1.3 Retry with jitter
- [ ] 1.4 Audit logging

### Фаза 2: Features (2-4 недели)
- [ ] 2.1 Admin dashboard
- [ ] 2.2 Multi-language support
- [ ] 2.3 YAML config
- [ ] 2.4 Prometheus metrics

### Фаза 3: Scale (4-6 недель)
- [ ] 3.1 Subscription caching
- [ ] 3.2 Batch operations
- [ ] 3.3 Multi-admin support
- [ ] 3.4 Connection instructions

### Фаза 4: Polish (6-8 недель)
- [ ] 4.1 E2E tests
- [ ] 4.2 API documentation
- [ ] 4.3 Security scanning
- [ ] 4.4 Performance optimization

---

## 💡 Будущие идеи

### Веб-интерфейс
- React/Vue dashboard для админов
- REST API для управления
- WebSocket для real-time обновлений

### Мобильное приложение
- iOS/Android приложение для пользователей
- Push-уведомления
- QR-код сканер

### Multi-tenant
- Поддержка нескольких панелей 3x-ui
- Изоляция данных
- Раздельная биллинг

### Payment integration
- Stripe/PayPal для автоматической оплаты
- Автоматическое продление
- Invoice generation

---

*Обновлено: 2026-03-24*
