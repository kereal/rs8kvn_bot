package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/ratelimiter"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Handler handles Telegram bot updates and manages subscription operations.
type Handler struct {
	bot         *tgbotapi.BotAPI
	cfg         *config.Config
	xui         *xui.Client
	rateLimiter *ratelimiter.RateLimiter
}

// NewHandler creates a new bot handler.
func NewHandler(bot *tgbotapi.BotAPI, cfg *config.Config, xuiClient *xui.Client) *Handler {
	return &Handler{
		bot:         bot,
		cfg:         cfg,
		xui:         xuiClient,
		rateLimiter: ratelimiter.NewRateLimiter(config.RateLimiterMaxTokens, config.RateLimiterRefillRate),
	}
}

// HandleStart handles the /start command.
func (h *Handler) HandleStart(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleStart called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	username := h.getUsername(update.Message.From)
	logger.Info("User started the bot",
		zap.String("username", username),
		zap.Int64("chat_id", chatID))

	isAdmin := chatID == h.cfg.TelegramAdminID

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nВыберите действие:",
		username,
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "get_subscription"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Моя подписка", "my_subscription"),
		),
	)

	if isAdmin {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📊 Статистика", "admin_stats"),
			),
		)
	}

	msg.ReplyMarkup = keyboard
	h.send(ctx, msg)
}

// HandleHelp handles the /help command.
func (h *Handler) HandleHelp(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleHelp called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	helpText := `📖 *Справка по командам бота*

*Доступные команды:*
/start - Начать работу с ботом
/help - Показать эту справку

*Функции бота:*
📥 *Получить подписку* - Создать новую подписку или получить существующую
📋 *Моя подписка* - Посмотреть информацию о текущей подписке

*Параметры подписки:*
📊 Трафик: ` + fmt.Sprintf("%d", h.cfg.TrafficLimitGB) + ` ГБ в месяц

*Технические детали:*
🔐 Протокол: VLESS+Reality+Vision
📱 Совместимость: V2Ray, Xray, и другие клиенты

*Поддержка:*
При возникновении проблем обратитесь к администратору: [@kereal](https://t.me/kereal)

*Дополнительная информация:*
- Подписка автоматически обновляется в конце месяца
- Не передавайте ссылку на подписку третьим лицам
- При истечении трафика подписка перестанет работать до следующего месяца`

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}

// HandleLastReg handles the /lastreg command for admins.
// Returns a list of the latest users who subscribed.
func (h *Handler) HandleLastReg(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleLastReg called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if chatID != h.cfg.TelegramAdminID {
		logger.Warn("Non-admin user attempted to access /lastreg", zap.Int64("chat_id", chatID))
		return
	}

	// Get latest 10 subscriptions
	subs, err := database.GetLatestSubscriptions(10)
	if err != nil {
		logger.Error("Failed to get latest subscriptions", zap.Error(err))
		h.SendMessage(ctx, chatID, "❌ Ошибка получения списка подписок")
		return
	}

	if len(subs) == 0 {
		h.SendMessage(ctx, chatID, "📭 Нет активных подписок")
		return
	}

	// Format the message as a table with 2 columns
	var sb strings.Builder
	sb.WriteString("📋 *Последние регистрации:*\n\n")

	for _, sub := range subs {
		// Column 1: Username (clickable link), Column 2: Date and time
		username := sub.Username
		if username == "" {
			username = "unknown"
		}
		dateStr := sub.CreatedAt.Format("02.01.2006 15:04:05")
		sb.WriteString(fmt.Sprintf("[@%s](https://t.me/%s) │ %s\n", username, username, dateStr))
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}

// HandleCallback handles callback queries from inline keyboards.
func (h *Handler) HandleCallback(ctx context.Context, update tgbotapi.Update) {
	if update.CallbackQuery == nil {
		logger.Error("HandleCallback called with nil CallbackQuery")
		return
	}

	data := update.CallbackQuery.Data
	chatID := update.CallbackQuery.Message.Chat.ID
	username := h.getUsername(update.CallbackQuery.From)

	logger.Info("Callback received",
		zap.String("data", data),
		zap.String("username", username),
		zap.Int64("chat_id", chatID))

	// Answer the callback to remove the loading state
	callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "")
	if _, err := h.bot.Request(callback); err != nil {
		logger.Error("Failed to answer callback", zap.Error(err))
	}

	switch data {
	case "get_subscription":
		h.handleGetSubscription(ctx, chatID, username)
	case "my_subscription":
		h.handleMySubscription(ctx, chatID, username)
	case "admin_stats":
		h.handleAdminStats(ctx, chatID, username)
	default:
		logger.Warn("Unknown callback data", zap.String("data", data))
	}
}

// handleGetSubscription handles the "get subscription" callback.
func (h *Handler) handleGetSubscription(ctx context.Context, chatID int64, username string) {
	logger.Info("User requesting subscription", zap.String("username", username))

	sub, err := database.GetByTelegramID(chatID)
	if err == nil && sub != nil {
		// Check if subscription is expired
		if sub.IsExpired() {
			logger.Info("Subscription expired, creating new one", zap.String("username", username))
			h.createSubscription(ctx, chatID, username)
			return
		}

		// Return existing active subscription
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"✅ Ваша активная подписка:\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка на подписку:\n`%s`",
			h.cfg.TrafficLimitGB,
			sub.SubscriptionURL,
		))
		msg.ParseMode = "Markdown"
		h.send(ctx, msg)
		return
	}

	// No existing subscription, create new one
	h.createSubscription(ctx, chatID, username)
}

// handleMySubscription handles the "my subscription" callback.
func (h *Handler) handleMySubscription(ctx context.Context, chatID int64, username string) {
	logger.Info("User checking subscription status", zap.String("username", username))

	sub, err := database.GetByTelegramID(chatID)
	if err != nil || sub == nil {
		msg := tgbotapi.NewMessage(chatID, "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.")
		h.send(ctx, msg)
		return
	}

	if sub.IsExpired() {
		msg := tgbotapi.NewMessage(chatID, "⚠️ Ваша подписка истекла.\n\nНажмите «Получить подписку» для создания новой.")
		h.send(ctx, msg)
		return
	}

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"📋 Информация о вашей подписке:\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка:\n`%s`",
		h.cfg.TrafficLimitGB,
		sub.SubscriptionURL,
	))
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}

// handleAdminStats handles the "admin stats" callback.
func (h *Handler) handleAdminStats(ctx context.Context, chatID int64, username string) {
	logger.Info("Admin requesting stats", zap.String("username", username))

	// Verify admin access
	if chatID != h.cfg.TelegramAdminID {
		logger.Warn("Non-admin user attempted to access admin stats", zap.Int64("chat_id", chatID))
		return
	}

	var allSubs []database.Subscription
	if err := database.DB.Find(&allSubs).Error; err != nil {
		logger.Error("Failed to fetch subscriptions for stats", zap.Error(err))
		h.SendMessage(ctx, chatID, "❌ Ошибка получения статистики")
		return
	}

	activeCount := 0
	expiredCount := 0
	for _, sub := range allSubs {
		if sub.Status == "active" {
			if sub.IsExpired() {
				expiredCount++
			} else {
				activeCount++
			}
		}
	}

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"📊 **Статистика бота**:\n\n👥 Всего пользователей: %d\n✅ Активные подписки: %d\n⏰ Истекшие: %d\n📁 Записей в БД: %d",
		len(allSubs),
		activeCount,
		expiredCount,
		len(allSubs),
	))
	msg.ParseMode = "Markdown"
	h.sendWithRetry(ctx, msg, 3)
}

// createSubscription creates a new subscription for the user.
// This operation is atomic with rollback: if database save fails,
// the client is removed from the 3x-ui panel to prevent orphan records.
func (h *Handler) createSubscription(ctx context.Context, chatID int64, username string) {
	now := time.Now()
	expiryTime := getLastSecondOfMonth(now)
	trafficBytes := int64(h.cfg.TrafficLimitGB) * 1024 * 1024 * 1024

	logger.Info("Creating subscription",
		zap.String("username", username),
		zap.Int("traffic_gb", h.cfg.TrafficLimitGB),
		zap.String("expiry", expiryTime.Format("02.01.2006 15:04:05")))

	// Step 1: Generate IDs
	clientID := utils.GenerateUUID()
	subID := utils.GenerateSubID()

	// Step 2: Add client to 3x-ui panel
	client, err := h.xui.AddClientWithID(ctx, h.cfg.XUIInboundID, username, clientID, subID, trafficBytes, expiryTime)
	if err != nil {
		logger.Error("Failed to add client to 3x-ui", zap.Error(err))
		h.SendMessage(ctx, chatID, "❌ Произошла ошибка при создании подписки. Попробуйте позже.")
		return
	}

	// Step 3: Save to database (with rollback on failure)
	subscriptionURL := h.xui.GetSubscriptionLink(xui.GetExternalURL(h.cfg.XUIHost), client.SubID, h.cfg.XUISubPath)

	sub := &database.Subscription{
		TelegramID:      chatID,
		Username:        username,
		ClientID:        client.ID,
		XUIHost:         h.cfg.XUIHost,
		InboundID:       h.cfg.XUIInboundID,
		TrafficLimit:    trafficBytes,
		ExpiryTime:      expiryTime,
		Status:          "active",
		SubscriptionURL: subscriptionURL,
	}

	if err := database.CreateSubscription(sub); err != nil {
		logger.Error("Failed to save subscription to database", zap.Error(err))

		// CRITICAL: Rollback - remove client from 3x-ui panel to prevent orphan record
		logger.Info("Attempting rollback: removing client from 3x-ui panel", zap.String("client_id", client.ID))
		if rollbackErr := h.xui.DeleteClient(ctx, h.cfg.XUIInboundID, client.ID); rollbackErr != nil {
			logger.Error("CRITICAL: Failed to rollback client deletion from 3x-ui", zap.Error(rollbackErr))
			// This is a critical error - we have an orphan client in the panel
			// Admin should be notified
			h.notifyAdminError(ctx, fmt.Sprintf(
				"⚠️ ORPHAN CLIENT WARNING\n\nClient ID: %s\nUsername: %s\nInbound: %d\n\nClient was created in 3x-ui but database save failed and rollback also failed!",
				client.ID, username, h.cfg.XUIInboundID,
			))
		} else {
			logger.Info("Rollback successful: client removed from 3x-ui panel", zap.String("client_id", client.ID))
		}

		h.SendMessage(ctx, chatID, "❌ Подписка создана в панели, но не сохранена в базе. Обратитесь к администратору.")
		return
	}

	// Success - send subscription to user
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"✅ Подписка успешно создана!\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка на подписку:\n`%s`\n\n📱 Используйте эту ссылку в любом клиенте поддерживающем VLESS+Reality+Vision",
		h.cfg.TrafficLimitGB,
		subscriptionURL,
	))
	msg.ParseMode = "Markdown"
	h.sendWithRetry(ctx, msg, 3)

	// Notify admin about new subscription
	h.notifyAdmin(ctx, username, chatID, subscriptionURL, expiryTime)
	logger.Info("Subscription created successfully",
		zap.String("username", username),
		zap.Int64("chat_id", chatID))
}

// notifyAdmin sends a notification to the admin about a new subscription.
func (h *Handler) notifyAdmin(ctx context.Context, username string, chatID int64, subscriptionURL string, expiryTime time.Time) {
	if h.cfg.TelegramAdminID == 0 {
		return
	}

	// Mask the subscription URL for security (show only last 8 chars)
	maskedURL := maskSubscriptionURL(subscriptionURL)

	msg := tgbotapi.NewMessage(h.cfg.TelegramAdminID,
		fmt.Sprintf("🔔 Новая подписка создана!\n\n👤 Пользователь: @%s\n🆔 ID: %d\n📅 Истекает: %s\n🔗 Подписка: `%s`",
			username,
			chatID,
			expiryTime.Format("02.01.2006 15:04:05"),
			maskedURL,
		))
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)

	logger.Info("Admin notified about new subscription", zap.String("username", username))
}

// notifyAdminError sends an error notification to the admin.
func (h *Handler) notifyAdminError(ctx context.Context, message string) {
	if h.cfg.TelegramAdminID == 0 {
		return
	}

	msg := tgbotapi.NewMessage(h.cfg.TelegramAdminID, message)
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}

// send sends a message with rate limiting.
func (h *Handler) send(ctx context.Context, msg tgbotapi.MessageConfig) {
	if !h.rateLimiter.Wait(ctx) {
		logger.Warn("Message send cancelled due to context")
		return
	}

	_, err := h.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send message", zap.Error(err))
	}
}

// sendWithRetry sends a message with rate limiting and retry logic.
func (h *Handler) sendWithRetry(ctx context.Context, msg tgbotapi.MessageConfig, maxRetries int) {
	delay := time.Second

	for i := 0; i < maxRetries; i++ {
		if !h.rateLimiter.Wait(ctx) {
			logger.Warn("Message send cancelled due to context")
			return
		}

		_, err := h.bot.Send(msg)
		if err == nil {
			return
		}

		if i < maxRetries-1 {
			logger.Warn("Message send failed, retrying",
				zap.Duration("delay", delay),
				zap.Error(err))

			select {
			case <-time.After(delay):
				delay *= 2 // Exponential backoff
			case <-ctx.Done():
				logger.Warn("Message retry cancelled due to context")
				return
			}
		}
	}

	logger.Error("Failed to send message after retries", zap.Int("max_retries", maxRetries))
}

// SendMessage sends a plain text message to a chat.
func (h *Handler) SendMessage(ctx context.Context, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	h.send(ctx, msg)
}

// getUsername extracts a username from a Telegram user.
func (h *Handler) getUsername(user *tgbotapi.User) string {
	if user == nil {
		return "unknown"
	}

	if user.UserName != "" {
		return user.UserName
	}

	if user.FirstName != "" {
		return user.FirstName
	}

	return fmt.Sprintf("user_%d", user.ID)
}

// getLastSecondOfMonth returns the last second of the current month.
func getLastSecondOfMonth(t time.Time) time.Time {
	year, month, _ := t.Date()
	firstDayNextMonth := time.Date(year, month+1, 1, 0, 0, 0, 0, t.Location())
	return firstDayNextMonth.Add(-1 * time.Second)
}

// maskSubscriptionURL masks a subscription URL for logging/notification purposes.
// Shows only the last 8 characters of the subscription ID.
func maskSubscriptionURL(url string) string {
	if len(url) < 12 {
		return "***"
	}
	// Find the last segment (subscription ID)
	lastSlash := -1
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			lastSlash = i
			break
		}
	}

	if lastSlash == -1 || lastSlash == len(url)-1 {
		return url[:8] + "..."
	}

	subID := url[lastSlash+1:]
	if len(subID) <= 8 {
		return url[:lastSlash+1] + "***"
	}

	return url[:lastSlash+1+len(subID)-8] + "..." + subID[len(subID)-8:]
}
