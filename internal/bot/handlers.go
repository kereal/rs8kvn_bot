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

	// Check for deep link parameter (e.g., /start donate)
	if update.Message.CommandArguments() == "donate" {
		donateText := `☕ *Поддержка проекта*

Пока у меня есть только сбор в т-банке
[https://tbank.ru/cf/9J6agHgWdNg](https://tbank.ru/cf/9J6agHgWdNg)

Если нужен другой способ — [напишите мне](https://t.me/kereal)`

		msg := tgbotapi.NewMessage(chatID, donateText)
		msg.ParseMode = "Markdown"
		h.send(ctx, msg)
		return
	}

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

	// Format the message as a table with 3 columns
	var sb strings.Builder
	sb.WriteString("📋 *Последние регистрации:*\n\n")

	for _, sub := range subs {
		// Column 1: ID, Column 2: Username (clickable link), Column 3: Date and time
		username := sub.Username
		if username == "" {
			username = "unknown"
		}
		dateStr := sub.CreatedAt.Format("02.01.2006 15:04:05")
		sb.WriteString(fmt.Sprintf("%d │ [@%s](https://t.me/%s) │ %s\n", sub.ID, username, username, dateStr))
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}

// HandleDel handles the /del command for admins.
// Deletes a subscription by database ID from both 3x-ui panel and database.
// Usage: /del <id>
func (h *Handler) HandleDel(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleDel called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if chatID != h.cfg.TelegramAdminID {
		logger.Warn("Non-admin user attempted to access /del", zap.Int64("chat_id", chatID))
		return
	}

	// Parse the command arguments
	args := update.Message.CommandArguments()
	if args == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /del <id>\n\nПример: /del 5")
		return
	}

	// Parse the ID
	var id uint
	if _, err := fmt.Sscanf(args, "%d", &id); err != nil {
		h.SendMessage(ctx, chatID, "❌ Неверный формат ID. Использование: /del <id>\n\nПример: /del 5")
		return
	}

	// Get the subscription
	sub, err := database.GetSubscriptionByID(id)
	if err != nil {
		logger.Error("Failed to get subscription", zap.Error(err), zap.Uint("id", id))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Подписка с ID %d не найдена", id))
		return
	}

	// Delete from 3x-ui panel first
	if err := h.xui.DeleteClient(ctx, sub.InboundID, sub.ClientID); err != nil {
		logger.Error("Failed to delete client from 3x-ui",
			zap.Error(err),
			zap.String("client_id", sub.ClientID),
			zap.Int("inbound_id", sub.InboundID))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка удаления клиента из панели 3x-ui: %v", err))
		return
	}

	// Delete from database
	_, err = database.DeleteSubscriptionByID(id)
	if err != nil {
		logger.Error("Failed to delete subscription from database",
			zap.Error(err),
			zap.Uint("id", id))
		// Client already deleted from 3x-ui, but database delete failed
		// This is a warning, not a critical error since the client is gone from the panel
		h.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Клиент удален из панели, но ошибка удаления из базы: %v\n\nОбратитесь к администратору.", err))
		return
	}

	// Success
	logger.Info("Subscription deleted",
		zap.Uint("id", id),
		zap.String("username", sub.Username),
		zap.Int64("telegram_id", sub.TelegramID),
		zap.String("client_id", sub.ClientID))

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Подписка успешно удалена!\n\n"+
			"🆔 ID: %d\n"+
			"👤 Пользователь: @%s\n"+
			"🆔 Telegram ID: %d\n"+
			"🔗 Client ID: %s",
		id,
		sub.Username,
		sub.TelegramID,
		sub.ClientID,
	))
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

	// Get traffic usage from 3x-ui panel
	trafficUsedGB := float64(0)

	traffic, err := h.xui.GetClientTraffic(ctx, sub.Username)
	if err != nil {
		logger.Warn("Failed to get client traffic from panel",
			zap.String("username", sub.Username),
			zap.Error(err))
	} else {
		// Calculate used traffic in GB (up + down) / 1024^3
		trafficUsedGB = float64(traffic.Up+traffic.Down) / 1024 / 1024 / 1024
	}

	trafficInfo := fmt.Sprintf("%.2f / %d ГБ", trafficUsedGB, h.cfg.TrafficLimitGB)
	expiryDate := sub.ExpiryTime.Format("02.01.2006")

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"📋 Информация о вашей подписке:\n\n📊 Трафик: %s\n📅 Сброс: %s\n\n🔗 Ссылка:\n`%s`",
		trafficInfo,
		expiryDate,
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
	expiryTime := getFirstSecondOfNextMonth(now)
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
		"🚀 *Ваша подписка готова!*\n\nТрафик: %dГб на месяц.\n\n📲 *1. Установите приложение Happ:*\n· [Скачать для iOS](https://apps.apple.com/ru/app/happ-proxy-utility-plus/id6746188973)\n· [Скачать для Android](https://play.google.com/store/apps/details?id=com.happproxy)\n\n📥 *2. Импортируйте подписку*\n\nНажмите, чтобы скопировать: `%s`\n\nВ приложении Happ нажмите *«+»* в правом верхнем углу и выберите *«Вставить из буфера»*.\n\n▶️ *3. Запустите VPN*\nДождитесь загрузки и нажмите на большую круглую кнопку в центре экрана.\n\n🛡️ *Важно знать*\nВ приложении Happ настроена автоматическая маршрутизация. Зарубежные сайты работают через VPN, а российские сервисы — напрямую. VPN можно не выключать.\n⚠️ _Если вы используете другое приложение или свою конфигурацию — не заходите через этот VPN на российские ресурсы, иначе сервер заблокируют._\n\n🤝 *Правила использования*\n· Не передавайте свою подписку другим. Делитесь ссылкой на этого бота `@rs8kvn_bot`.\n· Не публикуйте ссылку на бота в интернете, передавайте только из рук в руки (приветствуется).\n· Пользуйтесь ответственно, не занимайтесь незаконной деятельностью.\n\n☕ *Поддержка проекта:*\nЭтот VPN бесплатный и существует благодаря вашим пожертвованиям и усилиям Кирилла. [Поддержите проект](https://t.me/rs8kvn_bot?start=donate) — важна каждая сотня.",
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

	msg := tgbotapi.NewMessage(h.cfg.TelegramAdminID,
		fmt.Sprintf("🔔 Новая подписка создана!\n\n👤 Пользователь: @%s\n🆔 ID: %d\n📅 Истекает: %s\n🔗 Подписка: `%s`",
			username,
			chatID,
			expiryTime.Format("02.01.2006 15:04:05"),
			subscriptionURL,
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
func getFirstSecondOfNextMonth(t time.Time) time.Time {
	year, month, _ := t.Date()
	return time.Date(year, month+1, 1, 0, 0, 0, 0, t.Location())
}
