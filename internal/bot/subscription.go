package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Create subscription
func (h *Handler) handleCreateSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User requesting subscription", zap.String("username", username))

	sub, err := h.db.GetByTelegramID(ctx, chatID)
	if err == nil && sub != nil {
		// Check if subscription is expired
		if sub.IsExpired() {
			logger.Info("Subscription expired, creating new one", zap.String("username", username))
			h.createSubscription(ctx, chatID, username, messageID)
			return
		}

		// Return existing active subscription - edit the message
		qrKeyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📱 QR-код", "qr_code"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
			),
		)
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, fmt.Sprintf(
			"✅ Ваша подписка\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка\n`%s`",
			h.cfg.TrafficLimitGB,
			sub.SubscriptionURL,
		))
		editMsg.ParseMode = "Markdown"
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &qrKeyboard
		h.safeSend(editMsg)
		return
	}

	// No existing subscription, create new one
	h.createSubscription(ctx, chatID, username, messageID)
}

// handleMySubscription handles the "my subscription" callback.
func (h *Handler) handleMySubscription(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User checking subscription status", zap.String("username", username))

	// Show loading message
	messageID = h.showLoadingMessage(chatID, messageID)
	if messageID == 0 {
		return
	}

	sub, err := h.db.GetByTelegramID(ctx, chatID)
	if err != nil || sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.")
		h.safeSend(editMsg)
		return
	}

	if sub.IsExpired() {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "⚠️ Ваша подписка истекла.\n\nНажмите «Получить подписку» для создания новой.")
		h.safeSend(editMsg)
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
		trafficUsedGB = float64(traffic.Up+traffic.Down) / 1024 / 1024 / 1024
	}

	trafficInfo := fmt.Sprintf("%.2f / %d ГБ", trafficUsedGB, h.cfg.TrafficLimitGB)
	expiryDate := sub.ExpiryTime.Format("02.01.2006")

	qrKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 QR-код", "qr_code"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, fmt.Sprintf(
		"📋 *Ваша подписка*\n\n📊 Трафик: %s\n📅 Сброс: %s\n\n🔗 Ссылка\n`%s`",
		trafficInfo,
		expiryDate,
		sub.SubscriptionURL,
	))
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &qrKeyboard
	h.safeSend(editMsg)
}

// handleQRCode handles the "qr_code" callback - generates and sends QR code image.
func (h *Handler) handleQRCode(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User requesting QR code", zap.String("username", username))

	// Get subscription
	sub, err := h.db.GetByTelegramID(ctx, chatID)
	if err != nil || sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.")
		h.safeSend(editMsg)
		return
	}

	// Generate QR code
	pngBytes, err := utils.GenerateQRCodePNG(sub.SubscriptionURL)
	if err != nil {
		logger.Error("Failed to generate QR code", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка генерации QR-кода. Попробуйте позже.")
		h.safeSend(editMsg)
		return
	}

	// Send QR code as photo (instant, no delete)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  "qr.png",
		Bytes: pngBytes,
	})
	photo.Caption = "📱 QR-код с подпиской\n\nНаведите камеру телефона на код, чтобы импортировать подписку"
	photo.ParseMode = "Markdown"

	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_subscription"),
		),
	)
	photo.ReplyMarkup = &backKeyboard

	if _, err := h.bot.Send(photo); err != nil {
		logger.Error("Failed to send QR photo", zap.Error(err))
	}
}

// handleBackToSubscription handles the "back_to_subscription" callback.
// Deletes the QR photo message - the subscription message remains visible above.
func (h *Handler) handleBackToSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User closing QR code", zap.String("username", username))

	// Delete the QR photo message
	deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
	if _, err := h.bot.Request(deleteMsg); err != nil {
		logger.Warn("Failed to delete QR message", zap.Error(err))
	}
	// Subscription message is already visible above, no need to send anything
}

// createSubscription creates a new subscription for the user.
// This operation is atomic with rollback: if database save fails,
// the client is removed from the 3x-ui panel to prevent orphan records.
func (h *Handler) createSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	// Show loading message
	messageID = h.showLoadingMessage(chatID, messageID)
	if messageID == 0 {
		return
	}

	now := time.Now()
	expiryTime := utils.FirstSecondOfNextMonth(now)
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

		// Provide more specific error message based on error type
		errMsg := "❌ Ошибка при создании подписки."
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "timeout") {
			errMsg = "❌ Не удается подключиться к серверу. Попробуйте позже."
		} else if strings.Contains(err.Error(), "authentication") {
			errMsg = "❌ Ошибка авторизации на сервере. Свяжитесь с администратором."
		} else if strings.Contains(err.Error(), "context canceled") {
			errMsg = "❌ Запрос был прерван. Попробуйте снова."
		}

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
		editMsg.DisableWebPagePreview = true
		h.safeSend(editMsg)
		return
	}

	// Step 3: Save to database (with rollback on failure)
	subscriptionURL := h.xui.GetSubscriptionLink(h.xui.GetExternalURL(h.cfg.XUIHost), client.SubID, h.cfg.XUISubPath)

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

	if err := h.db.CreateSubscription(ctx, sub); err != nil {
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
		h.safeSend(editMsg)
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
	h.safeSend(editMsg)

	// Notify admin about new subscription
	h.notifyAdmin(ctx, username, chatID, subscriptionURL, expiryTime)
	logger.Info("Subscription created successfully",
		zap.String("username", username),
		zap.Int64("chat_id", chatID))
}
