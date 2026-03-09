package bot

import (
	"context"
	"fmt"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/ratelimiter"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Handler struct {
	bot         *tgbotapi.BotAPI
	config      *config.Config
	xui         *xui.Client
	rateLimiter *ratelimiter.TokenBucket
}

func NewHandler(bot *tgbotapi.BotAPI, cfg *config.Config, xuiClient *xui.Client) *Handler {
	return &Handler{
		bot:         bot,
		config:      cfg,
		xui:         xuiClient,
		rateLimiter: ratelimiter.NewTokenBucket(30, 5),
	}
}

func (h *Handler) HandleStart(update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleStart called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	username := ""
	if update.Message.From != nil {
		username = update.Message.From.UserName
		if username == "" {
			username = update.Message.From.FirstName
		}
	}

	logger.Infof("User %s (%d) started the bot", username, chatID)

	isAdmin := chatID == h.config.TelegramAdminID

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nВыберите действие:", username))

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

	h.send(context.Background(), msg)
}

func (h *Handler) HandleCallback(update tgbotapi.Update) {
	if update.CallbackQuery == nil {
		logger.Error("HandleCallback called with nil CallbackQuery")
		return
	}

	data := update.CallbackQuery.Data
	chatID := update.CallbackQuery.Message.Chat.ID

	username := ""
	if update.CallbackQuery.From != nil {
		username = update.CallbackQuery.From.UserName
	}

	logger.Infof("Callback received: %s from user %s (%d)", data, username, chatID)

	callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "")
	if _, err := h.bot.Request(callback); err != nil {
		logger.Errorf("Failed to answer callback: %v", err)
		return
	}

	ctx := context.Background()
	switch data {
	case "get_subscription":
		h.handleGetSubscription(ctx, chatID, username)
	case "my_subscription":
		h.handleMySubscription(ctx, chatID, username)
	case "admin_stats":
		h.handleAdminStats(ctx, chatID, username)
	}
}

func (h *Handler) handleGetSubscription(ctx context.Context, chatID int64, username string) {
	logger.Infof("User %s requesting subscription", username)

	sub, err := database.GetByTelegramID(chatID)
	if err == nil && sub != nil {
		if time.Now().After(sub.ExpiryTime) {
			logger.Infof("Subscription expired for user %s, creating new one", username)
			h.createSubscription(ctx, chatID, username)
			return
		}

		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Ваша активная подписка:\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка на подписку:\n`%s`",
			h.config.TrafficLimitGB,
			sub.SubscriptionURL,
		))
		msg.ParseMode = "Markdown"
		h.send(ctx, msg)
		return
	}

	h.createSubscription(ctx, chatID, username)
}

func (h *Handler) handleMySubscription(ctx context.Context, chatID int64, username string) {
	logger.Infof("User %s checking subscription status", username)

	sub, err := database.GetByTelegramID(chatID)
	if err != nil || sub == nil {
		msg := tgbotapi.NewMessage(chatID, "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.")
		h.send(ctx, msg)
		return
	}

	if time.Now().After(sub.ExpiryTime) {
		msg := tgbotapi.NewMessage(chatID, "⚠️ Ваша подписка истекла.\n\nНажмите «Получить подписку» для создания новой.")
		h.send(ctx, msg)
		return
	}

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📋 Информация о вашей подписке:\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка:\n`%s`",
		h.config.TrafficLimitGB,
		sub.SubscriptionURL,
	))
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}

func (h *Handler) handleAdminStats(ctx context.Context, chatID int64, username string) {
	logger.Infof("Admin %s requesting stats", username)

	var allSubs []database.Subscription
	if err := database.DB.Find(&allSubs).Error; err != nil {
		logger.Errorf("Failed to fetch subscriptions for stats: %v", err)
		h.SendMessage(ctx, chatID, "❌ Ошибка получения статистики")
		return
	}

	activeCount := 0
	expiredCount := 0
	for _, sub := range allSubs {
		if sub.Status == "active" {
			if time.Now().After(sub.ExpiryTime) {
				expiredCount++
			} else {
				activeCount++
			}
		}
	}

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📊 **Статистика бота**:\n\n👥 Всего пользователей: %d\n✅ Активные подписки: %d\n⏰ Истекшие: %d\n📁 Записей в БД: %d",
		len(allSubs),
		activeCount,
		expiredCount,
		len(allSubs),
	))
	msg.ParseMode = "Markdown"
	h.sendWithRetry(ctx, msg, 3)
}

func (h *Handler) createSubscription(ctx context.Context, chatID int64, username string) {
	now := time.Now()
	expiryTime := getLastSecondOfMonth(now)
	trafficBytes := int64(h.config.TrafficLimitGB) * 1024 * 1024 * 1024

	logger.Infof("Creating subscription for %s: traffic=%d GB, expiry=%s",
		username, h.config.TrafficLimitGB, expiryTime.Format("02.01.2006, 15:04:05"))

	// Шаг 1: Генерируем ID
	clientID := generateUUID()
	subID := generateSubID()

	// Шаг 2: Сначала добавляем клиента в 3x-ui панель
	client, err := h.xui.AddClientWithID(ctx, h.config.XUIInboundID, username, clientID, subID, trafficBytes, expiryTime)
	if err != nil {
		logger.Errorf("Failed to add client to 3x-ui: %v", err)
		h.SendMessage(ctx, chatID, "❌ Произошла ошибка при создании подписки. Попробуйте позже.")
		return
	}

	// Шаг 3: Сохраняем в базу данных
	subscriptionURL := h.xui.GetSubscriptionLink(xui.GetExternalURL(h.config.XUIHost), client.SubID, h.config.XUISubPath)

	sub := &database.Subscription{
		TelegramID:      chatID,
		Username:        username,
		ClientID:        client.ID,
		XUIHost:         h.config.XUIHost,
		InboundID:       h.config.XUIInboundID,
		TrafficLimit:    trafficBytes,
		ExpiryTime:      expiryTime,
		Status:          "active",
		SubscriptionURL: subscriptionURL,
	}

	if err := database.CreateSubscription(sub); err != nil {
		logger.Errorf("Failed to save subscription: %v", err)
		h.SendMessage(ctx, chatID, "❌ Подписка создана в панели, но не сохранена в базе. Обратитесь к администратору.")
		return
	}

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Подписка успешно создана!\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка на подписку:\n`%s`\n\n📱 Используйте эту ссылку в любом клиенте поддерживающем VLESS+Reality+Vision",
		h.config.TrafficLimitGB,
		subscriptionURL,
	))
	msg.ParseMode = "Markdown"
	h.sendWithRetry(ctx, msg, 3)

	h.notifyAdmin(ctx, username, chatID, subscriptionURL, expiryTime)
	logger.Infof("Subscription created for user %s (%d)", username, chatID)
}

func (h *Handler) notifyAdmin(ctx context.Context, username string, chatID int64, subscriptionURL string, expiryTime time.Time) {
	if h.config.TelegramAdminID == 0 {
		return
	}

	msg := tgbotapi.NewMessage(h.config.TelegramAdminID,
		fmt.Sprintf("🔔 Новая подписка создана!\n\n👤 Пользователь: @%s\n🆔 ID: %d\n📅 Истекает: %s\n🔗 Подписка:\n`%s`",
			username,
			chatID,
			expiryTime.Format("02.01.2006, 15:04:05"),
			subscriptionURL,
		))
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)

	logger.Infof("Admin notified about new subscription for %s", username)
}

func (h *Handler) send(ctx context.Context, msg tgbotapi.MessageConfig) {
	if !h.rateLimiter.Wait(ctx) {
		logger.Warn("Message send cancelled due to context")
		return
	}

	_, err := h.bot.Send(msg)
	if err != nil {
		logger.Errorf("Failed to send message: %v", err)
	}
}

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
			logger.Warnf("Message send failed, retrying in %v: %v", delay, err)

			select {
			case <-time.After(delay):
				delay *= 2
			case <-ctx.Done():
				logger.Warn("Message retry cancelled due to context")
				return
			}
		}
	}
	logger.Errorf("Failed to send message after %d retries", maxRetries)
}

func (h *Handler) SendMessage(ctx context.Context, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	h.send(ctx, msg)
}

func generateUUID() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		time.Now().Unix(),
		time.Now().UnixNano()&0xFFFF,
		(time.Now().UnixNano()>>16)&0xFFFF,
		(time.Now().UnixNano()>>32)&0xFFFF,
		time.Now().UnixNano()&0xFFFFFFFFFFFF,
	)
}

func generateSubID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFFFFFFFFFF)
}

func getLastSecondOfMonth(t time.Time) time.Time {
	nextMonth := t.AddDate(0, 1, 0)
	firstDayNextMonth := time.Date(nextMonth.Year(), nextMonth.Month(), 1, 0, 0, 0, 0, t.Location())
	return firstDayNextMonth.Add(-1 * time.Second)
}
