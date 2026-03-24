# Единый план развития rs8kvn_bot

**Дата создания:** 2026-03-24
**Версия:** v1.9.6
**Текущее покрытие:** ~51%

---

## 📊 Текущее состояние

### ✅ Реализовано
- Telegram бот для VLESS+Reality подписок
- Интеграция с 3x-ui панелью
- SQLite БД с миграциями
- Логирование (zap) с ротацией
- Ежедневные бэкапы
- Sentry интеграция
- Docker + CI/CD (golangci-lint, gosec)
- Health check endpoint (/healthz, /readyz)
- Circuit breaker для 3x-ui
- Rate limiting
- Graceful shutdown с отслеживанием goroutines

### 📈 Метрики
- Memory: ~17 MB RSS | Binary: ~10 MB | CPU: Minimal

---

## 🐛 Исправления багов

### ✅ P0 - Критические (Завершены)

| # | Проблема | Файл | Статус |
|---|----------|------|--------|
| 1 | **Path Traversal** — validatePath() проверяла `..` ПОСЛЕ filepath.Clean() | `internal/backup/backup.go` | ✅ |
| 2 | **Buffer pool race** — sync.Pool возвращал буфер до завершения HTTP запроса | `internal/xui/client.go` | ✅ |
| 3 | **Goroutine leak** — обработчики апдейтов не отслеживались WaitGroup | `cmd/bot/main.go` | ✅ |
| 4 | **Nil pointer** — CallbackQuery.Message не проверялся на nil | `internal/bot/callbacks.go` | ✅ |
| 5 | **Unbounded goroutines** — неограниченное создание goroutines при спаме | `cmd/bot/main.go` | ✅ |

### ✅ P1 - Безопасность (Завершены)

| # | Проблема | Файл | Статус |
|---|----------|------|--------|
| 6 | **Default credentials** — XUI_USERNAME/PASSWORD имели "admin" по умолчанию | `internal/config/config.go` | ✅ |
| 7 | **Markdown injection** — username вставлялся без экранирования | `internal/bot/handler.go` | ✅ |
| 8 | **uint overflow** — /del парсил отрицательные числа в огромный uint | `internal/bot/admin.go` | ✅ |
| 9 | **Error disclosure** — внутренние ошибки показывались пользователю | `internal/bot/admin.go` | ✅ |

### 🟡 P2 - Надежность

| # | Проблема | Файл | Статус |
|---|----------|------|--------|
| 10 | **TOCTOU race** — проверка сессии и использование под разными блокировками | `internal/xui/client.go:140-162` | ✅ Уже исправлено |
| 11 | **Context not checked** — broadcast loop не проверял ctx.Done() | `internal/bot/admin.go:183-199` | ✅ |
| 12 | **Orphan 3x-ui clients** — старый клиент не удалялся при пересоздании | `internal/bot/subscription.go:26` | ⬜ |
| 13 | **Error wrapping** — смешанное использование %w и %v | Везде | ✅ Уже корректно |

### 🟢 P3 - Потребление памяти

| # | Проблема | Файл | Статус |
|---|----------|------|--------|
| 14 | **GetAllSubscriptions для подсчёта** — загрузка всех подписок только для COUNT | `internal/bot/admin.go:296` | ✅ |
| 15 | **GetAllTelegramIDs без пагинации** — все ID загружаются в память | `internal/bot/admin.go:183` | ✅ Уже исправлено через batch processing |
| 16 | **HTTP response bodies** — ошибки закрытия игнорировались | `internal/xui/client.go` | ✅ |

### 🔵 P4 - Качество кода

| # | Проблема | Файл | Статус |
|---|----------|------|--------|
| 17 | **Gosec G104** — исключение unhandled errors | `.golangci.yml` | ⬜ |
| 18 | **MockDatabaseService thread-safe** — map без мьютекса | `internal/testutil/testutil.go` | ✅ |
| 19 | **Retry jitter** — thundering herd при одновременных сбоях | `internal/xui/client.go:455-482` | ⬜ |

### 📋 Доп. улучшения

| # | Задача | Статус |
|---|--------|--------|
| 20 | TLS конфигурация (XUI_SKIP_TLS_VERIFY) | ⬜ |
| 21 | Request ID для логирования | ⬜ |
| 22 | Metrics endpoint (Prometheus) | ⬜ |
| 23 | Health check | ✅ |

---

## 🔧 Технические улучшения

### Приоритет 1: Производительность

#### 1.1 Кэширование подписок
- **Проблема:** Каждый запрос делает SQL запрос к БД
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

### Приоритет 2: Надёжность

#### 2.1 Health check endpoint ✅
- HTTP endpoint на порту 8080 с проверкой БД и x-ui
- **Эндпоинты:**
  - `GET /healthz` — базовая проверка
  - `GET /readyz` — готовность принимать запросы

#### 2.2 Graceful degradation
- **Проблема:** Сбой x-ui блокирует весь бот
- **Решение:** Кэширование последнего успешного состояния
- **Эффект:** Бот работает в read-only режиме при сбое x-ui

#### 2.3 Retry with jitter
- **Проблема:** Thundering herd при одновременных сбоях
- **Решение:** `delay * (1 + rand.Float64())`

### Приоритет 3: Конфигурация

#### 3.1 YAML конфиг
- **Проблема:** Env vars неудобны для сложных настроек
- **Решение:** Поддержка config.yaml с env vars переопределением

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
- **Формат:** `TELEGRAM_ADMIN_IDS=123,456,789`

---

## 🎯 Новые функции

### Приоритет 1: Уведомления

#### 1.1 Expiry notifications
- Уведомления за 7, 3, 1 день до истечения
- **Файл:** `internal/bot/notifications.go` (новый)
- **Фоновая задача:** Ежедневная проверка в 09:00

#### 1.2 Traffic warnings
- Предупреждение при 80%, 90%, 100% трафика

#### 1.3 Payment reminders
- Напоминание об оплате для истекающих подписок

### Приоритет 2: Админ функции

#### 2.1 Admin dashboard
- **Команда:** `/dashboard`
- **Метрики:** Активные пользователи, новые подписки, трафик, ошибки x-ui

#### 2.2 Subscription management
- `/extend <id> <days>` — продлить
- `/settraffic <id> <gb>` — изменить лимит
- `/ban <id>` — заблокировать

#### 2.3 Export functionality
- **Команда:** `/export [subscriptions|stats]`

### Приоритет 3: Пользовательские функции

#### 3.1 Multi-language support
- **Поддерживаемые:** ru, en, zh, es
- **Команда:** `/lang <code>`

#### 3.2 Subscription sharing
- **Команда:** `/share`
- Генерация временной ссылки (24ч)

#### 3.3 Connection instructions
- **Команда:** `/instructions <client>`
- **Клиенты:** v2rayNG, Streisand, Hiddify, Clash

#### 3.4 QR code improvements
- QR с логотипом и branding

### Приоритет 4: Пробный период
- **Команда:** `/trial`
- Лимит: 1GB трафика на 3 дня
- 1 trial на telegram_id

### Приоритет 5: Тарифные планы

| Тариф | Трафик | Срок | Цена |
|-------|--------|------|------|
| Базовый | 50 GB | 30 дней | 100₽ |
| Стандарт | 150 GB | 30 дней | 250₽ |
| Премиум | 500 GB | 30 дней | 500₽ |

### Приоритет 6: Промокоды
- **Типы:** скидка, бесплатное продление, бонусный трафик
- **Команды:** `/promo <code>`, `/addpromo` (админ)

---

## 🌐 Мульти-серверность (Ключевая фича)

### Концепция
Один клиент на ВСЕХ серверах, ОДНА подписка со ВСЕМИ серверами

### Как это работает
1. Бот генерирует UUID, email, subId ОДИН РАЗ
2. Для каждого сервера создаёт клиента с ТЕМИ ЖЕ данными
3. Бот генерирует VLESS конфиг со всеми серверами
4. Пользователь получает ОДНУ подписку для Happ

### HTTP Endpoint
- Бот запускает HTTP сервер на порту 8080
- Endpoint: `GET /sub/{subID}`
- Возвращает base64-закодированный конфиг xray/v2ray

### Оценка времени
| Подзадача | Время |
|-----------|-------|
| Конфигурация серверов | 0.5 дня |
| Создание клиента на всех серверах | 1 день |
| HTTP endpoint для подписок | 1 день |
| Обработка ошибок | 0.5 дня |
| **Итого** | **3 дня** |

---

## 🔒 Безопасность

### Аутентификация

#### Admin API key
- API ключ для админских операций
- **Файл:** `internal/auth/auth.go` (новый)

#### Rate limiting improvements
- Обычные пользователи: 10/мин
- Админы: 60/мин

### Аудит

#### Audit logging
- Логирование всех админских действий
- **Файл:** `internal/logger/audit.go` (новый)

#### Access logging
- Логирование всех запросов бота (JSON формат)
- **Файл:** `internal/logger/access.go` (новый)

---

## 📊 Мониторинг

### Метрики

#### Prometheus metrics
- `bot_requests_total` — общее количество запросов
- `bot_requests_duration_seconds` — время обработки
- `bot_subscriptions_active` — активные подписки
- `bot_xui_errors_total` — ошибки x-ui
- `bot_database_errors_total` — ошибки БД

#### Structured logging
- JSON логи для ELK/Graylog

### Алерты

#### Health alerts
- Уведомления админу о проблемах:
  - x-ui недоступен > 5 минут
  - БД ошибка
  - Memory usage > 80%

---

## 🐳 DevOps

### Docker

#### Docker Compose improvements
- Healthcheck с HTTP проверкой
- Log rotation
- Volume backups

### CI/CD

#### Automated releases
- Автоматический changelog и release notes

#### E2E tests
- End-to-end тесты с тестовым ботом

#### Security scanning
- Trivy для сканирования зависимостей

---

## 📝 Документация

### API docs
- Формат: OpenAPI/Swagger
- Файл: `docs/api.yaml` (новый)

### Architecture docs
- Data flow diagrams
- Component interaction
- Deployment guide

### Runbook
- Troubleshooting
- Backup/restore
- Scaling

---

## 📋 План реализации

### Фаза 1: Stability (1-2 недели)
- [ ] Expiry notifications
- [ ] Retry with jitter  
- [ ] Audit logging
- [ ] Orphan 3x-ui clients fix

### Фаза 2: Features (2-4 недели)
- [ ] Admin dashboard
- [ ] Multi-language support
- [ ] YAML config
- [ ] Prometheus metrics

### Фаза 3: Scale (4-6 недель)
- [ ] Subscription caching
- [ ] Batch operations
- [ ] Multi-admin support
- [ ] Connection instructions

### Фаза 4: Multi-server (3-6 недель)
- [ ] Server configuration
- [ ] Multi-server subscription creation
- [ ] HTTP endpoint for subscriptions
- [ ] Error handling

### Фаза 5: Polish (6-8 недель)
- [ ] E2E tests
- [ ] API documentation
- [ ] Security scanning

---

## 📊 Метрики успеха

### Надежность
- [ ] Uptime > 99.9%
- [ ] MTTR < 5 минут
- [ ] 0 panic в production

### Производительность
- [ ] Response time < 100ms (95%)
- [ ] Memory < 50MB
- [ ] 1000+ одновременных пользователей

### Качество кода
- [ ] Coverage > 80%
- [ ] 0 критических lint ошибок

---

## 📊 Статус тестов

| Модуль | Покрытие |
|--------|----------|
| cmd/bot | 4.5% (пропущен) |
| internal/bot | 9.9% (пропущен) |
| internal/config | 88.6% ✅ |
| internal/database | 84.3% ✅ |
| internal/health | 100% ✅ |
| internal/interfaces | 100% ✅ |
| internal/xui | 88.3% ✅ |
| internal/logger | 85.6% ✅ |
| internal/utils | 80.0% ✅ |
| internal/heartbeat | 95.8% ✅ |
| internal/ratelimiter | 100% ✅ |
| **Итого** | **~51%** |

---

## 📌 Статус задач

**Легенда:**
- ✅ Завершено
- 🔄 В работе
- ⬜ Запланировано
- ❌ Отменено

---

*Обновлено: 2026-03-24*
*Версия бота: v1.9.6*
