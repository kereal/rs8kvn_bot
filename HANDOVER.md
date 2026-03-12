# 🔄 Handover Summary - TGVPN Go Bot

## 📁 Архитектура

```
tgvpn_go/
├── cmd/bot/main.go              # Entry point, graceful shutdown, backup scheduler
├── internal/
│   ├── config/config.go         # Env config loader (godotenv)
│   ├── database/database.go     # GORM models, migrations, CRUD
│   ├── bot/handlers.go          # Telegram bot commands & callbacks
│   ├── xui/client.go            # 3x-ui HTTP API client
│   ├── logger/logger.go         # Zap structured logging
│   ├── ratelimiter/ratelimiter.go # Token bucket for API rate limiting
│   ├── backup/backup.go         # Daily DB backup scheduler
│   ├── heartbeat/heartbeat.go   # Health check monitoring
│   └── utils/uuid.go            # UUID & SubID generators
├── data/                        # Runtime: SQLite DB, logs
└── Dockerfile                   # Multi-stage build
```

**Поток данных:**
1. Telegram → `bot/handlers.go` → обрабатывает команды/callbacks
2. Создание подписки → `xui/client.go` добавляет клиента в 3x-ui
3. Сохранение → `database/database.go` (SQLite + GORM)
4. Логирование → Zap (console + file rotation)
5. Мониторинг → Sentry (ошибки) + Heartbeat (health check)

## 🛠 Стек

**Go 1.24.0**

**Основные зависимости:**
- `telegram-bot-api/v5` - Telegram Bot API
- `gorm.io/gorm` + `gorm.io/driver/sqlite` - ORM, SQLite
- `go.uber.org/zap` - Структурированное логирование
- `gopkg.in/natefinch/lumberjack.v2` - Ротация логов
- `github.com/joho/godotenv` - Загрузка .env
- `github.com/getsentry/sentry-go` - Error tracking

**База данных:** SQLite (файл `./data/tgvpn.db`)

## ✅ Текущее состояние

**Реализовано и работает:**

1. **Telegram Bot эндпоинты:**
   - `/start` - Приветствие + inline-кнопки (получить подписку, моя подписка, статистика для админа)
   - `/help` - Справка по командам
   - Callback handlers: `get_subscription`, `my_subscription`, `admin_stats`

2. **Подписки:**
   - ✅ Создание новой подписки (VLESS+Reality+Vision)
   - ✅ Получение существующей подписки
   - ✅ Проверка статуса подписки
   - ✅ Автоматическое обновление в конце месяца (reset=31)
   - ✅ Traffic limit: 100GB/мес (configurable)

3. **Интеграция с 3x-ui:**
   - ✅ Логин сессией (15 мин validity)
   - ✅ Auto re-login с exponential backoff
   - ✅ AddClient API (создаёт клиента в панели)
   - ✅ Генерация subscription URL

4. **Админ-функции:**
   - ✅ Уведомления о новых подписках
   - ✅ Статистика бота (активные/истекшие подписки)

5. **Инфраструктура:**
   - ✅ Graceful shutdown (SIGINT/SIGTERM)
   - ✅ Daily backup scheduler (03:00, keeps 7 days)
   - ✅ Rate limiting (30 burst, 5/sec)
   - ✅ Sentry error tracking
   - ✅ Heartbeat monitoring
   - ✅ Docker multi-arch (amd64/arm64)
   - ✅ CI/CD GitHub Actions

## 📝 Последние изменения

**Нет контекста о последних изменениях** (это начало сессии).

**Из кода видно:**
- Добавлен Sentry integration (версия 1.5.1)
- Оптимизирован HTTP transport для low-memory footprint
- Disabled prepared statement cache в GORM (экономия памяти)
- Добавлен heartbeat мониторинг
- Уменьшены connection pool таймауты (5min lifetime, 2min idle)

## ❓ Текущая проблема/задача

**Нет информации о текущей задаче** - это новая сессия без предыдущего контекста.

**Потенциальные направления:**
1. Добавление тестов (существует `uuid_test.go`, но нет integration tests)
2. Расширение функционала (например, управление подписками)
3. Оптимизация или bug fixes
4. Новые фичи

## ⚠️ Критичные нюансы

### Бизнес-логика

1. **Atomic операция создания подписки:**
   - Сначала генерируются `clientID` и `subID`
   - Затем клиент добавляется в 3x-ui панель
   - Только потом сохраняется в БД
   - Если БД сохранение fails → клиент остаётся в панели (орфан)

2. **Subscription lifecycle:**
   - При создании новой подписки старая ревокается (status="revoked")
   - Только одна активная подписка на пользователя
   - Автообновление: reset=31 (ежемесячно)

3. **3x-ui API особенности:**
   - Сессия живёт 15 минут, auto re-login
   - API может вернуть `success=false` но с `msg="successfully"` (parsed manually)
   - Endpoint: `/panel/api/inbounds/addClient`

### Конфигурация

**Обязательные env vars:**
```env
TELEGRAM_BOT_TOKEN=...
XUI_HOST=http://panel:2053
XUI_USERNAME=admin
XUI_PASSWORD=...
XUI_INBOUND_ID=1
```

**Важные defaults:**
- `TRAFFIC_LIMIT_GB=100` (1-1000)
- `DATABASE_PATH=./data/tgvpn.db`
- `LOG_LEVEL=info`
- `HEARTBEAT_INTERVAL=300` (5 минут)

### Database schema

**Subscriptions table:**
- `telegram_id` + `status` - composite unique (только одна активная)
- `status`: active | revoked | expired
- Soft deletes enabled (`deleted_at`)

### Memory optimization

- Single DB connection (SQLite)
- HTTP transport: MaxIdleConns=1
- GORM PrepareStmt disabled
- Connection pool: lifetime 5min, idle 2min

### Sentry integration

- Версия: `rs8kvn_bot@1.5.1`
- TracesSampleRate: 0.1 (10%)
- Panic recovery в goroutines (backup scheduler, heartbeat)

### Rate limiting

- Token bucket: 30 burst, 5 tokens/sec refill
- Apply к Telegram API calls
- Context-aware (cancellation support)
