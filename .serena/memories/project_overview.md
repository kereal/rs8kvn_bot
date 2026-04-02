# rs8kvn_bot - Telegram Bot for 3x-ui VLESS Subscription Distribution

## Project Purpose
This is a Telegram bot for distributing VLESS+Reality+Vision proxy subscriptions from 3x-ui panel.

## Features
- Get subscription on demand
- View current subscription status  
- QR code for easy subscription import
- Invite/trial landing page with one-click setup
- Referral system with in-memory cache + periodic sync
- Configurable traffic limit (default 30GB/month)
- Auto-renewal on the last day of each month
- Admin notifications on new subscriptions
- Heartbeat monitoring support
- Health check endpoint (/healthz, /readyz)
- File logging with rotation (zap)
- Daily database backups with rotation
- Database migrations system
- Sentry error tracking
- Rate limiting per user
- Graceful shutdown with goroutine tracking
- Circuit breaker for 3x-ui panel
- Donate message with card number in config (constants.go)
- Friendly and inviting donation message tone

## Tech Stack
- **Language**: Go 1.25.0
- **Bot Framework**: telegram-bot-api/v5
- **Database**: SQLite with GORM
- **Logging**: Zap
- **Testing**: testify
- **Migration**: golang-migrate/migrate/v4
- **QR Codes**: piglig/go-qr
- **Error Tracking**: getsentry/sentry-go

## Code Structure
- `cmd/bot/` - Main application entry point
- `internal/bot/` - Bot logic (handlers, commands, callbacks, menus)
- `internal/database/` - Database operations and migrations
- `internal/xui/` - 3x-ui panel client with circuit breaker
- `internal/utils/` - Utility functions (time, UUID, QR codes)
- `internal/config/` - Configuration management
- `internal/logger/` - Logging setup
- `internal/health/` - Health check endpoints
- `internal/heartbeat/` - Heartbeat monitoring
- `internal/backup/` - Database backup functionality
- `internal/ratelimiter/` - Rate limiting logic
- `internal/web/` - Web endpoints (invite/trial pages)
- `internal/interfaces/` - Interface definitions
- `internal/testutil/` - Test utilities and mocks

## Development Workflow

### Terminal Tool Usage
**Important**: When using the `terminal` tool, use the basename of the root directory as `cd` parameter:
- ✅ Correct: `cd: "tgvpn_go"` 
- ❌ Wrong: `cd: "/home/kereal/tgvpn_go"` (causes worktree error)

### Git Workflow
**Simplified workflow without Pull Requests:**
- Feature branches: `feature/description`
- Workflow: `feature/* → merge → dev → merge → main`
- Direct merge to dev (no PRs)
- Merge dev to main for releases
- Tag releases: `v2.1.0`, etc.
- Sync dev with main after release

**Commit conventions:**
- Conventional Commits (`feat:`, `fix:`, `docs:`, etc.)
- Branch naming: `feature/description`

Project includes `.agents/skills/git-workflow-skill/` with best practices.

### Available Tools
- `git` - version control
- `gh` CLI (v2.46.0) - GitHub operations
- `golangci-lint` - linting
- `go` (v1.25.0) - Go toolchain

## Последние изменения (v2.1.0)
 
### Сессия 2026-04-02 — Документация и планирование

#### Очистка IMPROVEMENTS.md
- **Действие**: Удалены все выполненные и отменённые задачи
- **Удалено**: P0 критические исправления (#1-5), отменённые (#11, #14), выполненные (#10, #16, #27, #33)
- **Удалены Quick Wins**: Q1, Q2, Q4, Q6, Q8-Q10 (все выполнены)
- **Обновлено**: План спринтов с новыми приоритетами
- **Коммит**: `9a07fdf`

#### Обновление документации
- **Файлы**: `HANDOVER.md`, `README.md`, `.serena/memories/project_overview.md`
- **Добавлено**: Документация команды `/refstats`
- **Обновлено**: Описание реферальной системы с деталями кеша
- **Коммит**: `e30b635`

#### Создан документ идей
- **Файл**: `doc/ideas.md` (40 идей всего)
- **Батч 1-10**: Основные фичи (алерты трафика, награды, платежи, веб-панель, ML)
- **Батч 11-20**: Расширенный функционал (выбор протокола, бэкапы, тикеты, промо)
- **Батч 21-30**: Практичные фичи для малого бизнеса (10-100 клиентов)
- **Батч 31-40**: Фичи для роста (семейные тарифы, мульти-сервер, персонализация)

#### Обсуждение архитектуры: Remnawave vs 3x-ui
- **Контекст**: Пользователь рассматривает миграцию на Remnawave
- **Ключевая проблема**: Ограничение архитектуры 3x-ui — 1 установка = 1 сервер
- **Потребность**: Поддержка нескольких серверов в одной подписке
- **Решение**: Миграция отложена, сначала реализовать кастомный генератор подписок
- **Альтернатива**: Создать генератор для многосерверных VLESS конфигов (1-2 дня работы)

#### Улучшения тестов
- **Коммит**: `7b099b2` — рефакторинг: консолидация тестовой настройки, удаление ненадёжных time.Sleep
- **Коммит**: `0f48d1d` — тесты: улучшены проверки в админ-тестах

### Система кеша рефералов
- **Implementation**: Full referral cache with database methods and in-memory sync
- **Admin command**: `/refstats` shows referral count per user
- **Files**: `database/database.go`, `bot/handler.go`, `bot/admin.go`, `cmd/bot/main.go`
- **Commit**: `66f5d86`

### Trial Atomic Rollback
- **Problem**: Trial creation could leave orphaned client in 3x-ui if cleanup failed
- **Solution**: `RetryWithBackoff` for rollback with up to 3 retries
- **Commit**: `d2d8c16`

### Subscription Locking
- **Problem**: Manual lock/unlock via map could deadlock on panic
- **Solution**: Replaced with `sync.Map` using `LoadOrStore`
- **Commit**: `113f2bf`

### Singleflight for XUI Login
- **Problem**: Concurrent requests after session expiry triggered multiple logins
- **Solution**: `singleflight.Group` to deduplicate concurrent login attempts
- **Commit**: `1857ae8`

### SQLite Connection Pool
- **Configuration**: Set `MaxOpenConns(1)`, `MaxIdleConns(2)`, `ConnMaxLifetime(1h)`
- **Purpose**: Prevents `database is locked` errors in SQLite

### Donate Improvements
- **Card number added to config**: `DonateCardNumber = "2200702156780864"` (T-Bank)
- **Donate constants**: `DonateURL`, `DonateContactUsername` in `internal/config/constants.go`
- **Improved donate message text**:
  - Friendly and inviting tone (no pressure)
  - Emojis: 😊 (call to action), ❤️ (gratitude)
  - Card number in code blocks for easy copying
  - Better formatting with line breaks

### Traffic Limit
- **Default traffic limit**: Changed from 100GB to 30GB (`DefaultTrafficLimitGB = 30`)

### Release Management
- **Tag**: v2.1.0 created on commit a69c7f6
- **Workflow**: Clean git history, removed empty merge commits
- **Branches**: main and dev synchronized on same commit

---

## Архитектурные ограничения и решения

### Ограничение 3x-ui: один сервер
**Проблема**: Архитектура 3x-ui не поддерживает многосерверные подписки нативно.
- 1 установка = 1 сервер = 1 inbound
- Клиент может быть привязан только к ОДНОМУ inbound за раз
- Нет встроенной балансировки нагрузки или failover между серверами

**Требование пользователя**: Одна подписка с несколькими серверами:
```
Подписка пользователя:
├─ Сервер 1: Москва (VLESS+Reality)
├─ Сервер 2: Германия (VLESS+Reality)
└─ Сервер 3: Нидерланды (VMess+WS)
```

**Варианты решения**:
1. **Кастомный генератор подписок** (РЕКОМЕНДУЕТСЯ) — Создать сервис, который:
  - Собирает данные с нескольких серверов 3x-ui
  - Генерирует VLESS конфиг с массивом серверов
  - VLESS клиенты (Happ, V2RayNG) поддерживают многосерверные конфиги нативно
  - **Время**: 1-2 дня

2. **Миграция на Remnawave** — Если поддерживает кластеризацию (нужно исследовать)
  - **Риск**: Полная переработка бота (2-4 дня)
  - **Риск**: Меньшее комьюнити, меньше проверен
  - **Решение**: Отложено пока текущая архитектура не станет ограничением

3. **HAProxy/Load Balancer** — Прокси-слой перед серверами
  - **Минус**: Единая точка отказа, нет гео-роутинга

**Текущее решение**: Реализовать кастомный генератор подписок, остаться на 3x-ui пока.

---

## Масштаб проекта

### Текущее состояние (2026-04-02)
- **Активные пользователи**: ~10 клиентов
- **Покрытие тестами**: ~75%
- **Документация**: Полная (IMPROVEMENTS.md, HANDOVER.md, README.md, ideas.md)
- **Приоритет**: Монетизация и рост (интеграция платежей, промо, привлечение пользователей)

### Цели роста
- **Краткосрочные (1-2 месяца)**: 50-100 платящих клиентов
- **Фокусные области**:
 1. Интеграция платежей (ЮKassa/Т-Банк)
 2. Система промокодов
 3. Уведомления о лимите трафика
 4. Генератор многосерверных подписок
- **Долгосрочные (3-6 месяцев)**: Оценить миграцию на Remnawave при необходимости
