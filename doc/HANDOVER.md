# Handover Summary - rs8kvn_bot

## Архитектура

```
rs8kvn_bot/
├── cmd/bot/main.go           # Entry point, graceful shutdown
├── internal/
│   ├── bot/                  # Telegram handlers (split into 8 files)
│   │   ├── admin.go         # /lastreg, /del, /broadcast, /send
│   │   ├── callbacks.go     # Callback query handlers
│   │   ├── commands.go      # /start, /help commands
│   │   ├── handler.go      # Main handler setup
│   │   ├── handlers.go      # Legacy (backwards compat)
│   │   ├── menu.go         # Inline keyboard menus
│   │   ├── message.go     # Message formatting
│   │   └── subscription.go # Subscription creation logic
│   ├── config/              # Configuration
│   │   ├── config.go       # Env loader with validation
│   │   └── constants.go    # App constants (traffic limits, etc.)
│   ├── database/            # SQLite + GORM
│   │   ├── database.go     # CRUD operations
│   │   ├── database_test.go
│   │   └── migrations.go   # DB migrations
│   ├── xui/client.go       # 3x-ui API client
│   ├── backup/backup.go    # Daily backups
│   ├── heartbeat/          # Health monitoring
│   ├── logger/             # Zap + Sentry
│   ├── ratelimiter/        # Token bucket
│   ├── utils/uuid.go       # UUID v4, SubID generators
│   └── testutil/           # Test utilities
```

## Стек

- **Go**: 1.25+
- **Telegram Bot API**: `github.com/go-telegram-bot-api/telegram-bot-api/v5`
- **ORM**: `gorm.io/gorm` + `gorm.io/driver/sqlite`
- **Logging**: `go.uber.org/zap` + `lumberjack`
- **Errors**: `github.com/getsentry/sentry-go`
- **Database**: SQLite (`./data/tgvpn.db`)
- **Linting**: golangci-lint v2

## Текущее состояние

✅ Работающие функции:
- Создание VLESS+Reality+Vision подписки
- Просмотр статуса подписки (трафик, срок)
- Inline keyboard UI (меню под сообщениями)
- Admin команды: `/lastreg`, `/del`, `/broadcast`, `/send`
- Автообновление подписки (31 день)
- Ежедневные бэкапы БД
- Rate limiting
- Heartbeat мониторинг
- Логирование с ротацией
- Sentry интеграция
- Unit тесты (~60% coverage)

## Последние изменения

- Исправлены версии дат в документации (HANDOVER.md, IMPROVEMENTS.md, BYPASS_METHODS.md)
- Исправлен typo "ОборудованиеDeep" → "Оборудование Deep"
- Обновлён README.md:
  - Go 1.24+ → Go 1.25+
  - Добавлены тесты и golangci-lint в features
  - Обновлена структура проекта
- Добавлен golangci-lint v2 конфиг

## Текущая задача

Документация проекта обновлена и согласована с кодовой базой. Проект готов к использованию.

## Критичные нюансы

1. **3x-ui API**: `GET /panel/api/inbounds/getClientTraffics/{email}` — возвращает один объект, не массив
2. **Session validity**: 15 минут, auto re-login с exponential backoff
3. **Traffic limit**: Хранится в байтах, дефолт 100GB
4. **Expiry**: Первая секуница следующего месяца
5. **VLESS ссылка**: includes `flow=xtls-rprx-vision`, `security=reality`
6. **Test constants**: В `internal/config/constants.go` константы сделаны как `var` для возможности переопределения в тестах (init() с 10ms задержками вместо 2 секунд)
7. **golangci-lint v2**: Требует `version: 2` в конфиге, linters.settings вместо linters-settings
