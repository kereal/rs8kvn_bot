package bot

import (
	"context"
	"fmt"
	"strconv"
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

// getMainMenuKeyboard returns the inline keyboard with main menu buttons
func (h *Handler) getMainMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Подписка", "menu_subscription"),
			tgbotapi.NewInlineKeyboardButtonData("☕ Донат", "menu_donate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❓ Помощь", "menu_help"),
		),
	)
}

// getBackKeyboard returns the inline keyboard with back button
func (h *Handler) getBackKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
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
		msg := tgbotapi.NewMessage(chatID, h.getDonateText())
		msg.ParseMode = "Markdown"
		msg.ReplyMarkup = h.getBackKeyboard()
		h.send(ctx, msg)
		return
	}

	isAdmin := chatID == h.cfg.TelegramAdminID

	// Check if user has an active subscription
	sub, err := database.GetByTelegramID(chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	if hasSubscription {
		// User has subscription - show inline keyboard with menu buttons
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nИспользуйте кнопки ниже для взаимодействия с ботом.",
			username,
		))

		keyboard := h.getMainMenuKeyboard()
		// Add admin buttons if user is admin
		if isAdmin {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}

		msg.ReplyMarkup = keyboard
		h.send(ctx, msg)
	} else {
		// User has no subscription - show inline button to get subscription
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nНажмите кнопку ниже, чтобы получить подписку:",
			username,
		))

		inlineKeyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "get_subscription"),
			),
		)

		// Add admin buttons if user is admin
		if isAdmin {
			inlineKeyboard.InlineKeyboard = append(inlineKeyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}

		msg.ReplyMarkup = inlineKeyboard
		h.send(ctx, msg)
	}
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
📋 *Подписка* - Посмотреть информацию о текущей подписке

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
// handleAdminLastReg handles the "admin_lastreg" callback - shows last 10 registrations
func (h *Handler) handleAdminLastReg(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("Admin requesting last registrations", zap.String("username", username))

	// Verify admin access
	if chatID != h.cfg.TelegramAdminID {
		logger.Warn("Non-admin user attempted to access last registrations", zap.Int64("chat_id", chatID))
		return
	}

	// Get latest 10 subscriptions
	subs, err := database.GetLatestSubscriptions(10)
	if err != nil {
		logger.Error("Failed to get latest subscriptions", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка получения списка подписок")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.bot.Send(editMsg)
		return
	}

	if len(subs) == 0 {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "📭 Нет активных подписок")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.bot.Send(editMsg)
		return
	}

	// Format the message as a table with 3 columns
	var sb strings.Builder
	sb.WriteString("📋 *Последние регистрации*\n\n")

	for _, sub := range subs {
		// Column 1: ID, Column 2: Username (clickable link), Column 3: Date and time
		username := sub.Username
		if username == "" {
			username = "unknown"
		}
		dateStr := sub.CreatedAt.Format("02.01.2006 15:04:05")
		sb.WriteString(fmt.Sprintf("%d │ [@%s](https://t.me/%s) │ %s\n", sub.ID, username, username, dateStr))
	}

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, sb.String())
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.bot.Send(editMsg)
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

// HandleBroadcast handles the /broadcast command for admins to send messages to all users.
func (h *Handler) HandleBroadcast(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleBroadcast called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if chatID != h.cfg.TelegramAdminID {
		logger.Warn("Non-admin user attempted to access /broadcast", zap.Int64("chat_id", chatID))
		return
	}

	// Get the message to broadcast
	message := update.Message.CommandArguments()
	if message == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /broadcast <сообщение>\n\nПример: /broadcast Привет всем!")
		return
	}

	// Get all Telegram IDs
	ids, err := database.GetAllTelegramIDs()
	if err != nil {
		logger.Error("Failed to get telegram IDs", zap.Error(err))
		h.SendMessage(ctx, chatID, "❌ Ошибка получения списка пользователей")
		return
	}

	if len(ids) == 0 {
		h.SendMessage(ctx, chatID, "❌ Нет пользователей для рассылки")
		return
	}

	// Send to all users
	successCount := 0
	failCount := 0
	for _, telegramID := range ids {
		msg := tgbotapi.NewMessage(telegramID, message)
		msg.ParseMode = "Markdown"
		msg.DisableWebPagePreview = true
		if _, err := h.bot.Send(msg); err != nil {
			logger.Warn("Failed to send broadcast message",
				zap.Int64("telegram_id", telegramID),
				zap.Error(err))
			failCount++
		} else {
			successCount++
		}
		// Small delay to avoid rate limiting
		time.Sleep(50 * time.Millisecond)
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Рассылка завершена!\n\n📤 Отправлено: %d\n❌ Ошибок: %d\n👥 Всего пользователей: %d",
		successCount,
		failCount,
		len(ids),
	))

	logger.Info("Broadcast completed",
		zap.Int("success", successCount),
		zap.Int("failed", failCount),
		zap.Int("total", len(ids)))
}

// HandleSend handles the /send command for admins to send a message to a specific user.
func (h *Handler) HandleSend(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleSend called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if chatID != h.cfg.TelegramAdminID {
		logger.Warn("Non-admin user attempted to access /send", zap.Int64("chat_id", chatID))
		return
	}

	// Parse the command arguments
	args := update.Message.CommandArguments()
	if args == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /send <telegram_id|username> <сообщение>\n\nПримеры:\n/send 123456789 Привет!\n/send @username Привет!")
		return
	}

	// Split args into target and message
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 2 {
		h.SendMessage(ctx, chatID, "❌ Использование: /send <telegram_id|username> <сообщение>\n\nПримеры:\n/send 123456789 Привет!\n/send @username Привет!")
		return
	}

	target := strings.TrimPrefix(parts[0], "@")
	message := parts[1]

	// Try to parse as Telegram ID first, then as username
	var telegramID int64
	var err error

	// Check if target is a number (Telegram ID)
	if id, parseErr := strconv.ParseInt(target, 10, 64); parseErr == nil {
		telegramID = id
	} else {
		// Try to find by username
		telegramID, err = database.GetTelegramIDByUsername(target)
		if err != nil {
			h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Пользователь @%s не найден в базе", target))
			return
		}
	}

	// Send the message
	msg := tgbotapi.NewMessage(telegramID, message)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true
	sentMsg, err := h.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send message",
			zap.Int64("telegram_id", telegramID),
			zap.Error(err))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка отправки сообщения: %v", err))
		return
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Сообщение отправлено!\n\n👤 Получатель: %d\n💬 ID сообщения: %d",
		telegramID,
		sentMsg.MessageID,
	))

	logger.Info("Message sent via /send command",
		zap.Int64("telegram_id", telegramID),
		zap.Int64("admin_id", chatID))
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

	logger.Debug("Callback received",
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
		messageID := update.CallbackQuery.Message.MessageID
		h.handleGetSubscription(ctx, chatID, username, messageID)
	case "my_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleMySubscription(ctx, chatID, username, messageID)
	case "admin_stats":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleAdminStats(ctx, chatID, username, messageID)
	case "admin_lastreg":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleAdminLastReg(ctx, chatID, username, messageID)
	case "back_to_start":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleBackToStart(ctx, chatID, username, messageID)
	case "menu_donate":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleMenuDonate(ctx, chatID, username, messageID)
	case "menu_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleMenuSubscription(ctx, chatID, username, messageID)
	case "menu_help":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleMenuHelp(ctx, chatID, username, messageID)
	default:
		logger.Warn("Unknown callback data", zap.String("data", data))
	}
}

// handleGetSubscription handles the "get subscription" callback.
func (h *Handler) handleGetSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User requesting subscription", zap.String("username", username))

	sub, err := database.GetByTelegramID(chatID)
	if err == nil && sub != nil {
		// Check if subscription is expired
		if sub.IsExpired() {
			logger.Info("Subscription expired, creating new one", zap.String("username", username))
			h.createSubscription(ctx, chatID, username, messageID)
			return
		}

		// Return existing active subscription - edit the message
		backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
			),
		)
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, fmt.Sprintf(
			"✅ Ваша активная подписка:\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка на подписку:\n`%s`",
			h.cfg.TrafficLimitGB,
			sub.SubscriptionURL,
		))
		editMsg.ParseMode = "Markdown"
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &backKeyboard
		h.bot.Send(editMsg)
		return
	}

	// No existing subscription, create new one
	h.createSubscription(ctx, chatID, username, messageID)
}

// handleMySubscription handles the "my subscription" callback.
func (h *Handler) handleMySubscription(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User checking subscription status", zap.String("username", username))

	// Show loading message - try to edit existing, or send new if fails
	if messageID > 0 {
		// Try to edit existing message
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "⏳ Загрузка...")
		editMsg.DisableWebPagePreview = true
		if _, err := h.bot.Send(editMsg); err != nil {
			logger.Warn("Failed to edit message for loading, sending new one", zap.Error(err))
			messageID = 0 // Reset to send new message
		}
	}

	if messageID == 0 {
		// Send new message
		loadingMsg := tgbotapi.NewMessage(chatID, "⏳ Загрузка...")
		loadingMsg.DisableWebPagePreview = true
		sentMsg, err := h.bot.Send(loadingMsg)
		if err != nil {
			logger.Error("Failed to send loading message", zap.Error(err))
			return
		}
		messageID = sentMsg.MessageID
	}

	sub, err := database.GetByTelegramID(chatID)
	if err != nil || sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.")
		editMsg.DisableWebPagePreview = true
		h.bot.Send(editMsg)
		return
	}

	if sub.IsExpired() {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "⚠️ Ваша подписка истекла.\n\nНажмите «Получить подписку» для создания новой.")
		editMsg.DisableWebPagePreview = true
		h.bot.Send(editMsg)
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

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, fmt.Sprintf(
		"📋 *Информация о вашей подписке*\n\n📊 Трафик: %s\n📅 Сброс: %s\n\n🔗 Ссылка\n`%s`",
		trafficInfo,
		expiryDate,
		sub.SubscriptionURL,
	))
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	if _, err := h.bot.Send(editMsg); err != nil {
		logger.Warn("Failed to edit message, sending new one", zap.Error(err), zap.Int64("chat_id", chatID), zap.Int("message_id", messageID))
		// Delete old message if exists
		if messageID > 0 {
			deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
			h.bot.Request(deleteMsg)
		}
		// Send new message instead
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"📋 *Информация о вашей подписке*\n\n📊 Трафик: %s\n📅 Сброс: %s\n\n🔗 Ссылка\n`%s`",
			trafficInfo,
			expiryDate,
			sub.SubscriptionURL,
		))
		msg.ParseMode = "Markdown"
		h.send(ctx, msg)
	}
}

// handleAdminStats handles the "admin stats" callback.
func (h *Handler) handleAdminStats(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("Admin requesting stats", zap.String("username", username))

	// Verify admin access
	if chatID != h.cfg.TelegramAdminID {
		logger.Warn("Non-admin user attempted to access admin stats", zap.Int64("chat_id", chatID))
		return
	}

	var allSubs []database.Subscription
	if err := database.DB.Find(&allSubs).Error; err != nil {
		logger.Error("Failed to fetch subscriptions for stats", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка получения статистики")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.bot.Send(editMsg)
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

	text := fmt.Sprintf(
		"📊 *Статистика бота*\n\n👥 Всего пользователей: %d\n✅ Активные подписки: %d\n⏰ Истекшие: %d\n📁 Записей в БД: %d",
		len(allSubs),
		activeCount,
		expiredCount,
		len(allSubs),
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.bot.Send(editMsg)
}

// createSubscription creates a new subscription for the user.
// This operation is atomic with rollback: if database save fails,
// the client is removed from the 3x-ui panel to prevent orphan records.
func (h *Handler) createSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	// Show loading message - edit existing or send new
	if messageID == 0 {
		// No message to edit, send new one
		loadingMsg := tgbotapi.NewMessage(chatID, "⏳ Загрузка...")
		loadingMsg.DisableWebPagePreview = true
		sentMsg, err := h.bot.Send(loadingMsg)
		if err != nil {
			logger.Error("Failed to send loading message", zap.Error(err))
			return
		}
		messageID = sentMsg.MessageID
	} else {
		// Edit existing message to show loading
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "⏳ Загрузка...")
		editMsg.DisableWebPagePreview = true
		h.bot.Send(editMsg)
	}

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
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Произошла ошибка при создании подписки. Попробуйте позже.")
		editMsg.DisableWebPagePreview = true
		h.bot.Send(editMsg)
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

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Подписка создана в панели, но не сохранена в базе. Обратитесь к администратору.")
		editMsg.DisableWebPagePreview = true
		h.bot.Send(editMsg)
		return
	}

	// Success - send subscription info with "Back to start" button
	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, h.getHelpText(h.cfg.TrafficLimitGB, subscriptionURL))
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &backKeyboard
	h.bot.Send(editMsg)

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

// send sends a message with rate limiting and saves the message ID for future editing.
func (h *Handler) send(ctx context.Context, msg tgbotapi.MessageConfig) {
	// Disable link previews for all messages
	msg.DisableWebPagePreview = true

	if !h.rateLimiter.Wait(ctx) {
		logger.Warn("Message send cancelled due to context")
		return
	}

	_, err := h.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send message", zap.Error(err))
		return
	}

}

// sendWithRetry sends a message with rate limiting and retry logic.
// Saves the message ID for future editing on success.
func (h *Handler) sendWithRetry(ctx context.Context, msg tgbotapi.MessageConfig, maxRetries int) {
	// Disable link previews for all messages
	msg.DisableWebPagePreview = true

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

// getDonateText returns the donation message text.
func (h *Handler) getDonateText() string {
	return `☕ *Поддержка проекта*

Есть сбор в Т-Банке
[https://tbank.ru/cf/9J6agHgWdNg](https://tbank.ru/cf/9J6agHgWdNg)

Если нужен другой способ — [напишите мне](https://t.me/kereal)`
}

// getHelpText returns the help/instruction message text with subscription URL.
func (h *Handler) getHelpText(trafficLimitGB int, subscriptionURL string) string {
	return fmt.Sprintf(
		"🚀 *Ваша подписка готова!*\n\nТрафик: %dГб на месяц.\n\n📲 *1. Установите приложение Happ*\n· [Скачать для iOS](https://apps.apple.com/ru/app/happ-proxy-utility-plus/id6746188973)\n· [Скачать для Android](https://play.google.com/store/apps/details?id=com.happproxy)\n\n📥 *2. Импортируйте подписку*\n\nНажмите, чтобы скопировать: `%s`\n\nВ приложении Happ нажмите *«+»* в правом верхнем углу и выберите *«Вставить из буфера»*.\n\n▶️ *3. Запустите VPN*\nДождитесь загрузки и нажмите на большую круглую кнопку в центре экрана.\n\n🛡️ *Важно знать*\nВ приложении Happ настроена автоматическая маршрутизация. Зарубежные сайты работают через VPN, а российские сервисы — напрямую. VPN можно не выключать.\n⚠️ _Если вы используете другое приложение или свою конфигурацию — не заходите через этот VPN на российские ресурсы, иначе сервер заблокируют._\n\n🤝 *Правила использования*\n· Не передавайте свою подписку другим. Делитесь ссылкой на этого бота `@rs8kvn_bot`.\n· Не публикуйте ссылку на бота в интернете, передавайте только из рук в руки (приветствуется).\n· Пользуйтесь ответственно, не занимайтесь незаконной деятельностью.\n\n☕ *Поддержка проекта*\nЭтот VPN бесплатный и существует благодаря вашим пожертвованиям и усилиям Кирилла.\n[Поддержите проект](https://t.me/rs8kvn_bot?start=donate) — важна каждая сотня.\n\nПомощь, вопросы: [@kereal](https://t.me/kereal)",
		trafficLimitGB,
		subscriptionURL,
	)
}

// handleBackToStart handles the "back_to_start" callback
// Edits message to show main menu with InlineKeyboard
func (h *Handler) handleBackToStart(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User returning to start", zap.String("username", username))

	isAdmin := chatID == h.cfg.TelegramAdminID

	// Check if user has an active subscription
	sub, err := database.GetByTelegramID(chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	if hasSubscription {
		// User has subscription - edit message with inline menu keyboard
		text := fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nИспользуйте кнопки ниже для взаимодействия с ботом.",
			username,
		)

		keyboard := h.getMainMenuKeyboard()
		// Add admin buttons if user is admin
		if isAdmin {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &keyboard
		h.bot.Send(editMsg)
	} else {
		// User has no subscription - edit message with inline button to get subscription
		text := fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nНажмите кнопку ниже, чтобы получить подписку:",
			username,
		)

		inlineKeyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "get_subscription"),
			),
		)

		// Add admin buttons if user is admin
		if isAdmin {
			inlineKeyboard.InlineKeyboard = append(inlineKeyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &inlineKeyboard
		h.bot.Send(editMsg)
	}
}

// handleMenuDonate handles the "menu_donate" callback - shows donate message with back button
func (h *Handler) handleMenuDonate(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User viewing donate", zap.String("username", username))

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, h.getDonateText())
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.bot.Send(editMsg)
}

// handleMenuSubscription handles the "menu_subscription" callback - shows subscription info with back button
func (h *Handler) handleMenuSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User viewing subscription", zap.String("username", username))

	sub, err := database.GetByTelegramID(chatID)
	if err != nil || sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.\n\nНажмите «🏠 В начало» для получения подписки.")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.bot.Send(editMsg)
		return
	}

	if sub.IsExpired() {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "⚠️ Ваша подписка истекла.\n\nНажмите «🏠 В начало» для создания новой.")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.bot.Send(editMsg)
		return
	}

	// Get traffic usage from 3x-ui panel
	trafficUsedGB := float64(0)
	traffic, err := h.xui.GetClientTraffic(ctx, sub.Username)
	if err != nil {
		logger.Warn("Failed to get client traffic from panel", zap.String("username", sub.Username), zap.Error(err))
	} else {
		trafficUsedGB = float64(traffic.Up+traffic.Down) / 1024 / 1024 / 1024
	}

	trafficInfo := fmt.Sprintf("%.2f / %d ГБ", trafficUsedGB, h.cfg.TrafficLimitGB)
	expiryDate := sub.ExpiryTime.Format("02.01.2006")

	text := fmt.Sprintf(
		"📋 *Информация о вашей подписке*\n\n📊 Трафик: %s\n📅 Сброс: %s\n\n🔗 Ссылка:\n`%s`",
		trafficInfo,
		expiryDate,
		sub.SubscriptionURL,
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.bot.Send(editMsg)
}

// handleMenuHelp handles the "menu_help" callback - shows help message with back button
func (h *Handler) handleMenuHelp(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User viewing help", zap.String("username", username))

	sub, err := database.GetByTelegramID(chatID)
	if err != nil || sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка: подписка не найдена.")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.bot.Send(editMsg)
		return
	}

	text := h.getHelpText(h.cfg.TrafficLimitGB, sub.SubscriptionURL)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.bot.Send(editMsg)
}
