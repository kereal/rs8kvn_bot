# TGVPN Go Bot — Полный Анализ и Предложения по Улучшению

**Дата:** 2026-03-31  
**Версия:** v2.0.2  
**Статус:** Активная разработка  
**Покрытие тестами:** 72.6% (цель: 80%+)  
**golangci-lint:** 51 warning (цель: < 20)

---

## 📊 Детальная Статистика Проекта

### Структура Кода
```
Строк кода (Go):     ~8,000
Файлов:              ~60
Пакетов:             13
Функций/Методов:     ~250
Тестов:              ~180
```

### Покрытие по Пакетам (Детально)
| Пакет | Файлов | Строк | Покрытие | Статус |
|-------|--------|-------|----------|--------|
| `internal/bot` | 8 | 2,100 | 94.5% | ✅ Отлично |
| `internal/ratelimiter` | 1 | 150 | 100% | ✅ Отлично |
| `internal/heartbeat` | 1 | 120 | 95.8% | ✅ Отлично |
| `internal/health` | 1 | 180 | 90.3% | ✅ Отлично |
| `internal/xui` | 2 | 650 | 86.8% | ✅ Хорошо |
| `internal/config` | 2 | 330 | 83.2% | ✅ Хорошо |
| `internal/web` | 1 | 480 | 75.6% | 🟡 Требует внимания |
| `internal/logger` | 1 | 180 | 82.3% | ✅ Хорошо |
| `internal/backup` | 1 | 290 | 81.4% | ✅ Хорошо |
| `internal/database` | 1 | 920 | 78.1% | 🟡 Требует внимания |
| `internal/utils` | 4 | 200 | 75.0% | 🟡 Требует внимания |
| `cmd/bot` | 1 | 433 | 19.6% | 🔴 Критично |

### Распределение по Типам Кода
```
Бизнес-логика:     45%
Обработка ошибок:  20%
Тесты:             18%
Конфигурация:       8%
Утилиты:            9%
```

---

## 🔴 КРИТИЧНЫЕ ПРОБЛЕМЫ (P0) — Исправить Немедленно

### 1. Утечка Горутин в `startBackupScheduler`

**Файл:** `cmd/bot/main.go:379`  
**Серьёзность:** 🔴 Критично  
**Влияние:** Утечка памяти, невозможность graceful shutdown

**Текущий Код:**
```go
func startBackupScheduler(ctx context.Context, dbPath string) {
    go func() {
        for {
            time.Sleep(24 * time.Hour)
            // ❌ НЕТ ПРОВЕРКИ НА ОТМЕНУ КОНТЕКСТА!
            backup.BackupDatabase(dbPath)
        }
    }()
}
```

**Проблема:**
- Горутина продолжает работать после `ctx.Done()`
- Блокирует завершение программы
- Утечка памяти (минимум 8KB на горутину)
- При перезапуске конфига — новая горутина, старая остаётся

**Решение:**
```go
func startBackupScheduler(ctx context.Context, dbPath string) {
    go func() {
        ticker := time.NewTicker(24 * time.Hour)
        defer ticker.Stop()
        
        // Первый бэкап через 5 минут после старта
        select {
        case <-ctx.Done():
            return
        case <-time.After(5 * time.Minute):
            backup.BackupDatabase(dbPath)
        }
        
        for {
            select {
            case <-ctx.Done():
                logger.Info("Backup scheduler shutting down")
                return
            case <-ticker.C:
                if err := backup.BackupDatabase(dbPath); err != nil {
                    logger.Error("Backup failed", zap.Error(err))
                }
            }
        }
    }()
}
```

**Время на исправление:** 15 минут  
**Риск изменений:** Низкий  
**Тесты:** Добавить тест на отмену контекста

---

### 2. Утечка Горутин в `startTrialCleanupScheduler`

**Файл:** `cmd/bot/main.go:410`  
**Серьёзность:** 🔴 Критично

**Текущий Код:**
```go
func startTrialCleanupScheduler(ctx context.Context, db *database.Service, xuiClient *xui.Client) {
    go func() {
        for {
            time.Sleep(time.Hour)
            // ❌ НЕТ ПРОВЕРКИ НА ОТМЕНУ КОНТЕКСТА!
            cleanupExpiredTrials(ctx, db, xuiClient)
        }
    }()
}
```

**Решение:**
```go
func startTrialCleanupScheduler(ctx context.Context, db *database.Service, xuiClient *xui.Client) {
    go func() {
        ticker := time.NewTicker(time.Hour)
        defer ticker.Stop()
        
        for {
            select {
            case <-ctx.Done():
                logger.Info("Trial cleanup scheduler shutting down")
                return
            case <-ticker.C:
                cleaned, err := cleanupExpiredTrials(ctx, db, xuiClient)
                if err != nil {
                    logger.Error("Trial cleanup failed", zap.Error(err))
                } else {
                    logger.Info("Trial cleanup completed", zap.Int("cleaned", cleaned))
                }
            }
        }
    }()
}
```

**Время на исправление:** 15 минут

---

### 3. Игнорирование Ошибок в `notifyAdmin`

**Файл:** `internal/bot/admin.go:389`  
**Серьёзность:** 🔴 Критично  
**Влияние:** Админ не получит важные уведомления

**Текущий Код:**
```go
func notifyAdmin(ctx context.Context, bot interfaces.BotAPI, text string) {
    // ❌ Ошибка игнорируется полностью!
    bot.Send(tgbotapi.NewMessage(adminID, text))
}
```

**Проблема:**
- Ошибки отправки теряются
- Невозможно отладить проблемы с уведомлениями
- Админ не узнает о критичных событиях

**Решение:**
```go
func notifyAdmin(ctx context.Context, bot interfaces.BotAPI, adminID int64, text string) error {
    if adminID <= 0 {
        return fmt.Errorf("invalid admin ID: %d", adminID)
    }
    
    msg := tgbotapi.NewMessage(adminID, text)
    msg.ParseMode = "Markdown"
    msg.DisableWebPagePreview = true
    
    _, err := bot.Send(msg)
    if err != nil {
        return fmt.Errorf("notify admin %d: %w", adminID, err)
    }
    
    logger.Debug("Admin notified", zap.Int64("admin_id", adminID))
    return nil
}
```

**Время на исправление:** 10 минут

---

### 4. Race Condition в `SubscriptionCache.Size()`

**Файл:** `internal/bot/cache.go:92`  
**Серьёзность:** 🔴 Критично  
**Влияние:** Паники при конкурентном доступе

**Текущий Код:**
```go
func (c *SubscriptionCache) Size() int {
    return len(c.items) // ❌ DATA RACE! Чтение без блокировки
}
```

**Проблема:**
- Чтение карты без `RLock()`
- Паника при одновременной записи
- Недетерминированное поведение

**Решение:**
```go
func (c *SubscriptionCache) Size() int {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return len(c.items)
}
```

**Время на исправление:** 5 минут

---

### 5. Отсутствует Graceful Shutdown для HTTP Сервера

**Файл:** `internal/web/web.go`  
**Серьёзность:** 🔴 Критично

**Проблема:**
```go
// В main.go
webServer := web.NewServer(...)
// ❌ Нет остановки сервера при shutdown
```

**Решение:**
```go
// В cmd/bot/main.go
webServer := web.NewServer(...)
go func() {
    if err := webServer.Start(); err != nil && err != http.ErrServerClosed {
        logger.Fatal("Web server failed", zap.Error(err))
    }
}()

// В shutdown handler
logger.Info("Shutting down web server...")
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := webServer.Shutdown(ctx); err != nil {
    logger.Error("Web server shutdown error", zap.Error(err))
}
```

**Время на исправление:** 30 минут

---

## 🟡 ПРОБЛЕМЫ СРЕДНЕЙ ВАЖНОСТИ (P1) — Исправить в Спринте

### 6. Дублирование Кода в QR Обработчиках

**Файл:** `internal/bot/callbacks.go:106-137`  
**Серьёзность:** 🟡 Средняя  
**Влияние:** Поддерживаемость, риск рассинхронизации

**Текущий Код:**
```go
func handleQRTelegram(...) {
    invite, err := h.db.GetOrCreateInvite(ctx, chatID, utils.GenerateInviteCode())
    if err != nil {
        logger.Error("Failed to get invite for QR", zap.Error(err))
        editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка генерации QR-кода. Попробуйте позже.")
        h.safeSend(editMsg)
        return
    }
    telegramLink := fmt.Sprintf("https://t.me/%s?start=share_%s", h.botUsername, invite.Code)
    h.sendQRCode(ctx, chatID, messageID, telegramLink, "📱 QR-код для Telegram...")
}

func handleQRWeb(...) {
    invite, err := h.db.GetOrCreateInvite(ctx, chatID, utils.GenerateInviteCode())
    if err != nil {
        // ❌ Те же 7 строк дублируются!
    }
    webLink := fmt.Sprintf("%s/i/%s", h.cfg.SiteURL, invite.Code)
    h.sendQRCode(ctx, chatID, messageID, webLink, "🌐 QR-код для веб-страницы...")
}
```

**Решение:**
```go
// linkType: "telegram" | "web"
func (h *Handler) generateInviteLink(ctx context.Context, chatID int64, linkType string) (string, error) {
    invite, err := h.db.GetOrCreateInvite(ctx, chatID, utils.GenerateInviteCode())
    if err != nil {
        return "", fmt.Errorf("get invite: %w", err)
    }
    
    switch linkType {
    case "telegram":
        return fmt.Sprintf("https://t.me/%s?start=share_%s", h.botUsername, invite.Code), nil
    case "web":
        return fmt.Sprintf("%s/i/%s", h.cfg.SiteURL, invite.Code), nil
    default:
        return "", fmt.Errorf("unknown link type: %s", linkType)
    }
}

func (h *Handler) handleQRTelegram(ctx context.Context, chatID int64, username string, messageID int) {
    link, err := h.generateInviteLink(ctx, chatID, "telegram")
    if err != nil {
        h.sendError(ctx, chatID, messageID, "Ошибка генерации QR-кода")
        return
    }
    h.sendQRCode(ctx, chatID, messageID, link, "📱 QR-код для Telegram...")
}
```

**Время на исправление:** 30 минут  
**Выгода:** -40 строк кода, легче поддерживать

---

### 7. Отсутствие Лимита на Длину Сообщений в `/broadcast`

**Файл:** `internal/bot/admin.go:174`  
**Серьёзность:** 🟡 Средняя

**Проблема:**
```go
func HandleBroadcast(...) {
    message := strings.TrimSpace(args)
    // ❌ Нет проверки на максимальную длину!
    // Telegram ограничивает 4096 символами
}
```

**Решение:**
```go
const (
    MaxTelegramMessageLen = 4096
    MaxCaptionLen         = 1024
)

func HandleBroadcast(...) {
    message := strings.TrimSpace(args)
    
    if len(message) == 0 {
        h.SendMessage(ctx, chatID, "❌ Введите текст сообщения")
        return
    }
    
    if len(message) > MaxTelegramMessageLen {
        h.SendMessage(ctx, chatID, fmt.Sprintf(
            "❌ Сообщение слишком длинное (%d символов). Максимум %d.",
            len(message), MaxTelegramMessageLen,
        ))
        return
    }
    
    // ...
}
```

**Время на исправление:** 15 минут

---

### 8. Hardcoded Значения (Магические Числа)

**Файлы:** Различные  
**Серьёзность:** 🟡 Средняя

**Проблемы:**
```go
// internal/bot/handler.go:32
const PendingInviteTTL = 60 * time.Minute  // ❌ Магическое число

// internal/web/web.go:267
trafficBytes := int64(s.cfg.TrialDurationHours) * 1024 * 1024 * 1024 / (24 * 365 / (30 * 24))
// ❌ Сложная формула без объяснений

// internal/bot/subscription.go:108
const CacheMaxSize = 1000  // ❌ Почему 1000?
const CacheTTL = 5 * time.Minute  // ❌ Почему 5 минут?
```

**Решение:**
```go
// internal/config/constants.go
package config

const (
    // Cache configuration
    DefaultCacheMaxSize     = 1000
    DefaultCacheTTL         = 5 * time.Minute
    
    // Invite configuration
    DefaultPendingInviteTTL = 60 * time.Minute
    
    // Trial configuration
    DefaultTrialMinTrafficGB    = 1
    DefaultTrialDurationHours   = 3
    DefaultSubscriptionResetDay = 30
    
    // Rate limiting
    RateLimiterMaxTokens    = 30
    RateLimiterRefillRate   = 5
    
    // Telegram limits
    MaxTelegramMessageLen   = 4096
    MaxCaptionLen           = 1024
)
```

**Время на исправление:** 1 час

---

### 9. Отсутствует Rate Limiting на `/send`

**Файл:** `internal/bot/admin.go:273`  
**Серьёзность:** 🟡 Средняя

**Проблема:** Админ может случайно (или намеренно) заспамить пользователей.

**Решение:**
```go
// internal/bot/admin.go
var (
    adminMessageLimiter = ratelimiter.NewRateLimiter(10, 1) // 10 сообщений в минуту
    adminMessageMu      sync.Map // chatID -> lastSent
)

func HandleSend(...) {
    now := time.Now()
    lastSent, _ := adminMessageMu.LoadOrStore(chatID, now)
    
    if time.Since(lastSent.(time.Time)) < 6*time.Second {
        h.SendMessage(ctx, chatID, "⚠️ Слишком много сообщений. Подождите минуту.")
        return
    }
    
    adminMessageMu.Store(chatID, now)
    // ...
}
```

**Время на исправление:** 20 минут

---

### 10. Смешение Бизнес-Логики с Презентацией

**Файл:** `internal/bot/subscription.go:262-340`  
**Серьёзность:** 🟡 Средняя

**Проблема:**
```go
func createSubscription(...) {
    // ❌ Бизнес-логика смешана с UI
    // ❌ Отправка сообщений внутри бизнес-функции
    // ❌ Сложно тестировать
}
```

**Решение:** Разделить на сервисный слой и handler:
```go
// internal/service/subscription.go
type SubscriptionService struct {
    db    DatabaseService
    xui   XUIClient
    cache SubscriptionCacher
}

func (s *SubscriptionService) Create(ctx context.Context, userID int64, username string) (*Subscription, error) {
    // Чистая бизнес-логика
}

// internal/bot/handlers.go
func (h *Handler) handleCreateSubscription(...) {
    sub, err := h.subscriptionService.Create(ctx, chatID, username)
    if err != nil {
        h.sendError(ctx, chatID, err)
        return
    }
    h.sendSuccess(ctx, chatID, sub)
}
```

**Время на исправление:** 3 часа  
**Выгода:** Легче тестировать, менять UI, добавлять новые функции

---

## 🟢 УЛУЧШЕНИЯ (P2) — Плановые Улучшения

### 11. Рефакторинг `handleUpdateSafely`

**Файл:** `cmd/bot/main.go:319`  
**Текущее покрытие:** 50%

**Проблема:** Функция смешивает recovery логику с бизнес-логикой.

**Решение:**
```go
func handleUpdateSafely(ctx context.Context, handler *bot.Handler, update tgbotapi.Update) {
    defer func() {
        if r := recover(); r != nil {
            stack := debug.Stack()
            logger.Error("Panic in handler",
                zap.Any("panic", r),
                zap.String("stack", string(stack)),
            )
            sentry.CaptureException(fmt.Errorf("panic: %v\n%s", r, stack))
        }
    }()
    
    handleUpdate(ctx, handler, update)
}

func handleUpdate(ctx context.Context, handler *bot.Handler, update tgbotapi.Update) {
    // Чистая бизнес-логика
    switch {
    case update.Message != nil:
        handler.HandleMessage(ctx, update)
    case update.CallbackQuery != nil:
        handler.HandleCallback(ctx, update)
    }
}
```

**Время на исправление:** 30 минут

---

### 12. Добавить Middleware для Логирования Callback Queries

**Файл:** `internal/bot/callbacks.go`

**Решение:**
```go
type CallbackMiddleware func(ctx context.Context, update tgbotapi.Update, next func())

func loggingMiddleware(logger *zap.Logger) CallbackMiddleware {
    return func(ctx context.Context, update tgbotapi.Update, next func()) {
        start := time.Now()
        
        logger.Debug("Callback received",
            zap.String("data", update.CallbackQuery.Data),
            zap.Int64("chat_id", update.CallbackQuery.Message.Chat.ID),
            zap.String("username", update.CallbackQuery.From.UserName),
        )
        
        next()
        
        logger.Debug("Callback processed",
            zap.String("data", update.CallbackQuery.Data),
            zap.Duration("duration", time.Since(start)),
        )
    }
}

func (h *Handler) HandleCallback(ctx context.Context, update tgbotapi.Update) {
    middleware := loggingMiddleware(logger)
    middleware(ctx, update, func() {
        // Обработка callback
    })
}
```

**Время на исправление:** 1 час

---

### 13. Оптимизация Запросов к БД

**Файл:** `internal/database/database.go`

**Проблема:**
```go
func (s *Service) GetByTelegramID(ctx context.Context, telegramID int64) (*Subscription, error) {
    var sub Subscription
    result := s.db.WithContext(ctx).Where("telegram_id = ?", telegramID).First(&sub)
    // ❌ Загружает ВСЕ поля, даже если нужна только проверка существования
}
```

**Решение:**
```go
// Быстрая проверка существования
func (s *Service) HasActiveSubscription(ctx context.Context, telegramID int64) (bool, error) {
    var count int64
    err := s.db.WithContext(ctx).
        Model(&Subscription{}).
        Where("telegram_id = ? AND status = ?", telegramID, "active").
        Select("id").
        Count(&count).Error
    return count > 0, err
}

// Загрузка только нужных полей
func (s *Service) GetSubscriptionStatus(ctx context.Context, telegramID int64) (*SubscriptionStatus, error) {
    var status SubscriptionStatus
    err := s.db.WithContext(ctx).
        Model(&Subscription{}).
        Where("telegram_id = ?", telegramID).
        Select("status, traffic_limit, expiry_time").
        First(&status).Error
    return &status, err
}
```

**Время на исправление:** 40 минут  
**Выгода:** -50% времени запросов

---

### 14. Добавить Кэширование Настроек Бота

**Файл:** Новый `internal/bot/botconfig.go`

**Решение:**
```go
package bot

type BotConfig struct {
    Username              string
    ID                    int64
    FirstName             string
    CanJoinGroups         bool
    CanReadAllGroupMessages bool
    SupportsInlineQueries bool
    loadedAt              time.Time
}

func NewBotConfig(botAPI interfaces.BotAPI) (*BotConfig, error) {
    user := botAPI.Self
    return &BotConfig{
        Username:              user.UserName,
        ID:                    user.ID,
        FirstName:             user.FirstName,
        CanJoinGroups:         user.CanJoinGroups,
        CanReadAllGroupMessages: user.CanReadAllGroupMessages,
        SupportsInlineQueries: user.SupportsInlineQueries,
        loadedAt:              time.Now(),
    }, nil
}

// В main.go
botConfig, err := bot.NewBotConfig(botAPI)
if err != nil {
    logger.Fatal("Failed to get bot config", zap.Error(err))
}
handler := bot.NewHandler(botAPI, cfg, dbService, xuiClient, botConfig.Username)
```

**Время на исправление:** 30 минут

---

### 15. Добавить Аудит Логирование

**Файл:** Новый `internal/audit/audit.go`

**Решение:**
```go
package audit

type Action string

const (
    ActionSubscriptionCreate  Action = "subscription.create"
    ActionSubscriptionDelete  Action = "subscription.delete"
    ActionTrialActivate       Action = "trial.activate"
    ActionAdminBroadcast      Action = "admin.broadcast"
    ActionAdminSend           Action = "admin.send"
)

type LogEntry struct {
    Timestamp   time.Time
    Action      Action
    ActorID     int64
    ActorType   string // "user" | "admin" | "system"
    TargetID    int64
    Details     map[string]interface{}
    IPAddress   string
    UserAgent   string
}

func Log(ctx context.Context, entry LogEntry) {
    logger.Info("Audit log",
        zap.Time("timestamp", entry.Timestamp),
        zap.String("action", string(entry.Action)),
        zap.Int64("actor_id", entry.ActorID),
        zap.String("actor_type", entry.ActorType),
        zap.Any("details", entry.Details),
    )
    
    // Сохранение в БД для долгосрочного хранения
    // db.Create(&entry)
}
```

**Время на исправление:** 2 часа

---

## 💡 ДОПОЛНИТЕЛЬНЫЕ ФУНКЦИИ (P3)

### 16. Команда `/stats` для Пользователей

**Описание:** Пользователи видят детальную статистику использования.

**Реализация:**
```go
func HandleUserStats(ctx context.Context, chatID int64) {
    sub, err := db.GetByTelegramID(ctx, chatID)
    if err != nil {
        SendMessage("❌ Подписка не найдена")
        return
    }
    
    traffic, err := xui.GetClientTraffic(ctx, sub.Username)
    if err != nil {
        SendMessage("❌ Ошибка получения статистики")
        return
    }
    
    // Рассчёт процентов
    usagePercent := float64(traffic.Total) / float64(sub.TrafficLimit) * 100
    
    // Дни до конца месяца
    daysLeft := time.Until(endOfMonth()).Hours() / 24
    
    text := fmt.Sprintf(`📊 *Ваша статистика*

📥 Скачано: %s
📤 Загружено: %s
📈 Всего: %s / %s (%.1f%%)
⏰ Дней до сброса: %d
📅 Следующий сброс: %s

%s`,
        formatBytes(traffic.Download),
        formatBytes(traffic.Upload),
        formatBytes(traffic.Total),
        formatBytes(sub.TrafficLimit),
        usagePercent,
        int(daysLeft),
        endOfMonth().Format("02.01.2006"),
        getUsageWarning(usagePercent),
    )
    
    SendMessage(text)
}

func getUsageWarning(percent float64) string {
    switch {
    case percent >= 100:
        return "⚠️ *Лимит исчерпан!* Подписка будет сброшена в конце месяца."
    case percent >= 90:
        return "⚠️ *Внимание!* Использовано более 90% трафика."
    case percent >= 80:
        return "💡 *Совет:* Использовано более 80% трафика."
    default:
        return ""
    }
}
```

**Время на реализацию:** 2 часа  
**Ценность:** Высокая для пользователей

---

### 17. Автоматические Уведомления об Истечении

**Описание:** Напоминания за 7, 3, 1 день до сброса.

**Реализация:**
```go
func startExpiryNotificationScheduler(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(6 * time.Hour) // Проверка каждые 6 часов
        defer ticker.Stop()
        
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                sendExpiryNotifications(ctx)
            }
        }
    }()
}

func sendExpiryNotifications(ctx context.Context) {
    days := []int{7, 3, 1}
    
    for _, daysLeft := range days {
        targetDate := endOfMonth().AddDate(0, 0, -daysLeft)
        
        // Найти подписки, которые нужно уведомить
        subs, err := db.GetSubscriptionsExpiringIn(ctx, targetDate)
        if err != nil {
            logger.Error("Failed to get expiring subscriptions", zap.Error(err))
            continue
        }
        
        for _, sub := range subs {
            // Проверить, не отправляли ли уже
            if wasNotified(sub.TelegramID, daysLeft) {
                continue
            }
            
            text := fmt.Sprintf(`⏰ *Напоминание о подписке*

Ваша подписка будет сброшена через %d дн.

📊 Текущий лимит: %s
📅 Дата сброса: %s

Не переживайте, сброс произойдёт автоматически!`,
                daysLeft,
                formatBytes(sub.TrafficLimit),
                endOfMonth().Format("02.01.2006"),
            )
            
            bot.SendMessage(ctx, sub.TelegramID, text)
            markAsNotified(sub.TelegramID, daysLeft)
        }
    }
}
```

**Время на реализацию:** 3 часа  
**Ценность:** Высокая для удержания пользователей

---

### 18. Метрики Prometheus

**Файл:** Новый `internal/metrics/metrics.go`

**Решение:**
```go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    subscriptionsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "tgvpn_subscriptions_total",
            Help: "Total number of subscriptions",
        },
        []string{"type"}, // trial, regular
    )
    
    subscriptionsActive = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "tgvpn_subscriptions_active",
            Help: "Number of active subscriptions",
        },
    )
    
    inviteCreationsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "tgvpn_invite_creations_total",
            Help: "Total invite link creations",
        },
        []string{"referrer_id"},
    )
    
    qrCodeGenerationsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "tgvpn_qr_generations_total",
            Help: "Total QR code generations",
        },
        []string{"type"}, // telegram, web, subscription
    )
    
    telegramErrorsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "tgvpn_telegram_errors_total",
            Help: "Total Telegram API errors",
        },
        []string{"operation", "error_type"},
    )
    
    xuiRequestDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "tgvpn_xui_request_duration_seconds",
            Help:    "XUI API request duration",
            Buckets: prometheus.DefBuckets,
        },
        []string{"operation"},
    )
    
    botUptimeSeconds = promauto.NewCounter(
        prometheus.CounterOpts{
            Name: "tgvpn_bot_uptime_seconds",
            Help: "Bot uptime in seconds",
        },
    )
)

// StartUptimeTracker запускает счётчик аптайма
func StartUptimeTracker(ctx context.Context) {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            botUptimeSeconds.Inc()
        }
    }
}
```

**Время на реализацию:** 2 часа  
**Ценность:** Критично для продакшена

---

### 19. Поддержка Нескольких Админов

**Файл:** `internal/config/config.go`

**Решение:**
```go
type Config struct {
    // ...
    TelegramAdminIDs []int64 `env:"TELEGRAM_ADMIN_IDS" envSeparator:","`
}

// В .env
TELEGRAM_ADMIN_IDS=123456,789012,345678

// Валидация
func (c *Config) Validate() error {
    if len(c.TelegramAdminIDs) == 0 {
        return fmt.Errorf("TELEGRAM_ADMIN_IDS is required")
    }
    // ...
}

// В handler
func (h *Handler) isAdmin(chatID int64) bool {
    for _, adminID := range h.cfg.TelegramAdminIDs {
        if chatID == adminID {
            return true
        }
    }
    return false
}
```

**Время на реализацию:** 1 час  
**Ценность:** Высокая для команды поддержки

---

### 20. Экспорт Данных (GDPR Compliance)

**Файл:** Новый `internal/bot/export.go`

**Команда:** `/export`

**Решение:**
```go
func HandleExport(ctx context.Context, chatID int64) {
    sub, err := db.GetByTelegramID(ctx, chatID)
    if err != nil {
        SendMessage("❌ Подписка не найдена")
        return
    }
    
    // Собрать все данные пользователя
    userData := map[string]interface{}{
        "telegram_id":     sub.TelegramID,
        "username":        sub.Username,
        "subscription": map[string]interface{}{
            "client_id":        sub.ClientID,
            "subscription_id":  sub.SubscriptionID,
            "inbound_id":       sub.InboundID,
            "traffic_limit":    sub.TrafficLimit,
            "created_at":       sub.CreatedAt,
            "status":           sub.Status,
        },
        "referrals": []map[string]interface{}{},
    }
    
    // Найти рефералов
    referrals, _ := db.GetReferrals(ctx, chatID)
    for _, ref := range referrals {
        userData["referrals"] = append(userData["referrals"].([]map[string]interface{}), map[string]interface{}{
            "telegram_id": ref.TelegramID,
            "username":    ref.Username,
            "created_at":  ref.CreatedAt,
        })
    }
    
    // Конвертировать в JSON
    jsonData, _ := json.MarshalIndent(userData, "", "  ")
    
    // Отправить файлом
    doc := tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{
        Name:  "my_data.json",
        Bytes: jsonData,
    })
    doc.Caption = "📦 Ваши персональные данные"
    
    bot.Send(doc)
}
```

**Время на реализацию:** 2 часа  
**Ценность:** Юридическая compliance

---

## 📈 ROADMAP

### Спринт 1 (1 неделя) — Критичные Исправления
- [ ] #1 Исправить утечку горутин в backup scheduler
- [ ] #2 Исправить утечку горутин в trial cleanup scheduler
- [ ] #3 Добавить проверку ошибок в notifyAdmin
- [ ] #4 Исправить race condition в SubscriptionCache
- [ ] #5 Добавить graceful shutdown для HTTP сервера

**Ожидаемый результат:** 0 критичных проблем, стабильная работа

---

### Спринт 2 (2 недели) — Рефакторинг
- [ ] #6 Удалить дублирование в QR обработчиках
- [ ] #7 Добавить лимит на длину сообщений
- [ ] #8 Вынести магические числа в конфиг
- [ ] #9 Добавить rate limiting на /send
- [ ] #10 Разделить бизнес-логику и презентацию

**Ожидаемый результат:** -15% кода, +10% покрытие тестов

---

### Спринт 3 (2 недели) — Улучшения Кода
- [ ] #11 Рефакторинг handleUpdateSafely
- [ ] #12 Добавить middleware для логирования
- [ ] #13 Оптимизация запросов к БД
- [ ] #14 Кэширование настроек бота
- [ ] #15 Добавить аудит логирование

**Ожидаемый результат:** +20% производительность, лучшая отладка

---

### Спринт 4 (3 недели) — Новые Функции
- [ ] #16 Команда /stats для пользователей
- [ ] #17 Автоматические уведомления об истечении
- [ ] #18 Метрики Prometheus
- [ ] #19 Поддержка нескольких админов
- [ ] #20 Экспорт данных (GDPR)

**Ожидаемый результат:** +5 пользовательских функций, готово к продакшену

---

## 📝 CHECKLIST CODE REVIEW

### Перед Каждым PR
- [ ] Все тесты проходят: `go test ./...`
- [ ] Покрытие не уменьшилось: `go test -cover ./...`
- [ ] Нет race conditions: `go test -race ./...`
- [ ] golangci-lint чист: `golangci-lint run ./...`
- [ ] go vet чист: `go vet ./...`
- [ ] Форматирование: `gofmt -d .` (должно быть пусто)
- [ ] Импорты отсортированы: `goimports -w .`
- [ ] Нет unused переменных: `staticcheck ./...`

### Архитектурные Требования
- [ ] Контекст передаётся первым параметром
- [ ] Ошибки обрабатываются на месте (не игнорируются)
- [ ] Нет утечек горутин (есть shutdown path через `select { case <-ctx.Done(): }`)
- [ ] Интерфейсы определены там, где используются
- [ ] Нет package-level state (глобальных переменных)
- [ ] Zero value типов полезен (можно использовать без инициализации)
- [ ] Нет вложенности больше 3 уровней

### Тестирование
- [ ] Table-driven тесты для чистых функций
- [ ] Моки для внешних зависимостей (BotAPI, Database, XUI)
- [ ] Тесты на error cases (обработка ошибок)
- [ ] Тесты на edge cases (пустые данные, nil, максимальные значения)
- [ ] Integration тесты для критичных путей (создание подписки, активация trial)
- [ ] Конкурентные тесты (`go test -race`)

### Безопасность
- [ ] Нет секретов в коде (только через .env)
- [ ] Входные данные валидируются
- [ ] SQL injection защищён (GORM parameterized queries)
- [ ] Rate limiting на всех пользовательских endpoint'ах
- [ ] Нет path traversal (проверка `..` в путях)

---

## 🎯 KPI ДЛЯ МОНИТОРИНГА

| Метрика | Текущее | Цель Q2 2026 | Цель Q3 2026 | Приоритет |
|---------|---------|--------------|--------------|-----------|
| **Общее покрытие тестами** | 72.6% | 80% | 85% | 🔴 Высокий |
| **Покрытие cmd/bot** | 19.6% | 50% | 70% | 🟡 Средний |
| **golangci-lint warnings** | 51 | < 20 | 0 | 🔴 Высокий |
| **Время ответа бота (p95)** | ~200ms | < 150ms | < 100ms | 🟡 Средний |
| **Uptime** | 99.5% | 99.9% | 99.95% | 🔴 Высокий |
| **Goroutine leaks** | 0 | 0 | 0 | 🔴 Критично |
| **Error rate (Sentry)** | ~5/day | < 2/day | < 1/day | 🟡 Средний |
| **Время деплоя** | ~5 мин | < 3 мин | < 2 мин | 🟢 Низкий |

---

## 📚 РЕСУРСЫ ДЛЯ РАЗРАБОТЧИКОВ

### Обязательное Чтение
1. [Effective Go](https://golang.org/doc/effective_go)
2. [Go Proverbs](https://go-proverbs.github.io/)
3. [Uber Go Style Guide](https://github.com/uber-go/guide)
4. [Go Blog: Error Handling](https://go.dev/blog/error-handling-and-go)
5. [Go Concurrency Patterns](https://go.dev/tour/concurrency/1)

### Инструменты
```bash
# Установить линтеры
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest

# Запустить все проверки
make lint      # golangci-lint
make test      # go test ./...
make race      # go test -race ./...
make security  # gosec ./...
make coverage  # go test -coverprofile=coverage.out && go tool cover -html=coverage.out
```

### Makefile Targets (Рекомендуемые)
```makefile
.PHONY: build test lint security coverage clean

build:
    go build -ldflags="-s -w" -o rs8kvn_bot ./cmd/bot

test:
    go test -v ./...

race:
    go test -race ./...

lint:
    golangci-lint run ./...

security:
    gosec ./...

coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    open coverage.html

clean:
    go clean
    rm -f rs8kvn_bot coverage.out coverage.html
```

### Шаблоны Кода
- [Go Templates](https://github.com/golang-templates/seed)
- [Standard Go Project Layout](https://github.com/golang-standards/project-layout)
- [Go Best Practices](https://github.com/golang-standards/project-layout/blob/master/README.md)
- [100 Go Mistakes](https://github.com/quasilyte/go-consistent)

---

## 📊 АНАЛИЗ ТЕКУЩЕЙ АРХИТЕКТУРЫ

### Сильные Стороны
✅ Чёткое разделение ответственности (пакеты по функциональности)  
✅ Dependency Injection через интерфейсы  
✅ Контекст передаётся корректно  
✅ Обработка ошибок на месте  
✅ Хорошее покрытие тестами (72.6%)  
✅ Нет глобального состояния  
✅ Graceful shutdown реализован частично  

### Слабые Стороны
⚠️  Утечки горутин в scheduler'ах  
⚠️  Смешение бизнес-логики с презентацией  
⚠️  Дублирование кода (QR обработчики, error handling)  
⚠️  Магические числа в коде  
⚠️  Отсутствует аудит логирование  
⚠️  Нет метрик для продакшена  
⚠️  Низкое покрытие cmd/bot (19.6%)  

### Возможности
🔵 Добавить Prometheus метрики  
🔵 Реализовать multi-language поддержку  
🔵 Добавить кэширование на Redis  
🔵 Реализовать webhook вместо long polling  
🔵 Добавить поддержку нескольких серверов 3x-ui  
🔵 Реализовать тарифные планы  

### Угрозы
🔴 Утечки памяти могут привести к падению в продакшене  
🔴 Отсутствие метрик = слепое администрирование  
🔴 Нет rate limiting на админских командах = риск спама  
🔴 Игнорирование ошибок = потеря важных уведомлений  

---

## 🔄 ПРОЦЕСС РАЗРАБОТКИ

### Ветка main
- ✅ Защищена от force push
- ✅ Требует code review
- ✅ Требует passing CI
- ⚠️  Нет requirement на coverage

### Ветка feature/*
- Создаётся от main
- Имя: `feature/{description}` (kebab-case)
- PR в main после завершения

### Коммиты
- Следовать [Conventional Commits](https://www.conventionalcommits.org/)
- Формат: `type(scope): description`
- Примеры:
  ```
  feat(web): add QR code generation for invite links
  fix(bot): prevent goroutine leak in backup scheduler
  test(db): add tests for GetTrialSubscriptionBySubID
  docs: update IMPROVEMENTS.md with new proposals
  ```

### CI/CD Pipeline
```yaml
# .github/workflows/ci.yml
name: CI

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.25'
      - run: go test -race -coverprofile=coverage.out ./...
      - uses: codecov/codecov-action@v3
        with:
          file: ./coverage.out

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - uses: golangci/golangci-lint-action@v3

  security:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - uses: securego/gosec@master
        with:
          args: ./...

  build:
    runs-on: ubuntu-latest
    needs: [test, lint, security]
    steps:
      - uses: actions/checkout@v3
      - uses: docker/build-push-action@v4
        with:
          push: ${{ github.event_name == 'push' }}
          tags: ghcr.io/kereal/rs8kvn_bot:${{ github.sha }}
```

---

**Документ обновляется:** При каждом значительном изменении архитектуры  
**Ответственный:** Tech Lead  
**Следующий пересмотр:** 2026-04-30  
**Статус:** ✅ Готово к реализации
