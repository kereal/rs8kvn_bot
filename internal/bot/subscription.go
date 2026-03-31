package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// getSubscriptionWithCache retrieves a subscription using cache first, then DB.
func (h *Handler) getSubscriptionWithCache(ctx context.Context, chatID int64) (*database.Subscription, error) {
	// Try cache first
	if sub := h.cache.Get(chatID); sub != nil {
		return sub, nil
	}

	// Cache miss, query database
	sub, err := h.db.GetByTelegramID(ctx, chatID)
	if err != nil {
		return nil, err
	}

	// Store in cache
	if sub != nil {
		h.cache.Set(chatID, sub)
	}

	return sub, nil
}

// invalidateCache removes a subscription from cache (call after updates/deletes).
func (h *Handler) invalidateCache(chatID int64) {
	h.cache.Invalidate(chatID)
}

// Create subscription
func (h *Handler) handleCreateSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User requesting subscription", zap.String("username", username))

	// Prevent duplicate subscription creation (double-click protection)
	h.subCreationMu.Lock()
	if _, inProgress := h.inProgress[chatID]; inProgress {
		h.subCreationMu.Unlock()
		logger.Info("Subscription creation already in progress", zap.Int64("chat_id", chatID))
		return
	}
	h.inProgress[chatID] = struct{}{}
	h.subCreationMu.Unlock()
	defer func() {
		h.subCreationMu.Lock()
		delete(h.inProgress, chatID)
		h.subCreationMu.Unlock()
	}()

	sub, err := h.getSubscriptionWithCache(ctx, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Info("No existing subscription, creating new one", zap.String("username", username))
		} else {
			logger.Error("Failed to check subscription", zap.Error(err))
			errMsg := "❌ Временная ошибка. Попробуйте позже."
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
			h.safeSend(editMsg)
			return
		}
	} else if sub != nil {
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

	sub, err := h.getSubscriptionWithCache(ctx, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.")
			h.safeSend(editMsg)
			return
		} else {
			logger.Error("Failed to get subscription", zap.Error(err))
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Временная ошибка. Попробуйте позже.")
			h.safeSend(editMsg)
			return
		}
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
func (h *Handler) handleBackToSubscription(_ context.Context, chatID int64, username string, messageID int) {
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

	trafficBytes := int64(h.cfg.TrafficLimitGB) * 1024 * 1024 * 1024

	logger.Info("Creating subscription",
		zap.String("username", username),
		zap.Int("traffic_gb", h.cfg.TrafficLimitGB))

	// Step 1: Generate IDs
	clientID := utils.GenerateUUID()
	subID := utils.GenerateSubID()

	// Step 2: Add client to 3x-ui panel
	// expiryTime: zero (no expiry), reset: 30 (reset on last day of month)
	client, err := h.xui.AddClientWithID(ctx, h.cfg.XUIInboundID, username, clientID, subID, trafficBytes, time.Time{}, config.SubscriptionResetDay)
	if err != nil {
		logger.Error("Failed to add client to 3x-ui", zap.Error(err))

		// Provide more specific error message based on error type
		errMsg := "❌ Ошибка при создании подписки."
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "timeout") {
			errMsg = "❌ Не удается подключиться к серверу. Попробуйте позже."
		} else if strings.Contains(err.Error(), "authentication") || strings.Contains(err.Error(), "unauthorized") {
			errMsg = "❌ Ошибка авторизации на сервере. Свяжитесь с администратором."
		} else if strings.Contains(err.Error(), "context canceled") {
			errMsg = "❌ Запрос был прерван. Попробуйте снова."
		} else if strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "dial tcp") {
			errMsg = "❌ Ошибка подключения к серверу. Проверьте настройки DNS."
		} else if strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "TLS") {
			errMsg = "❌ Ошибка SSL/TLS сертификата. Свяжитесь с администратором."
		} else if strings.Contains(err.Error(), "inbound") || strings.Contains(err.Error(), "client") {
			errMsg = "❌ Ошибка сервера при создании подписки. Попробуйте позже."
		}

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
		editMsg.DisableWebPagePreview = true
		h.safeSend(editMsg)
		return
	}

	// Step 3: Проверяем pending invite для записи referred_by
	var referredBy int64
	h.pendingMu.RLock()
	if pending, ok := h.pendingInvites[chatID]; ok {
		if time.Now().Before(pending.expiresAt) {
			// Кэш валиден, получаем referrer_tg_id из БД
			invite, err := h.db.GetInviteByCode(ctx, pending.code)
			if err == nil {
				referredBy = invite.ReferrerTGID
				logger.Info("Using pending invite for subscription",
					zap.Int64("chat_id", chatID),
					zap.String("invite_code", pending.code),
					zap.Int64("referred_by", referredBy))
			}
		}
		// Очищаем кэш в любом случае
		h.pendingMu.Lock()
		delete(h.pendingInvites, chatID)
		h.pendingMu.Unlock()
	}
	h.pendingMu.RUnlock()

	// Step 4: Save to database (with rollback on failure)
	subscriptionURL := h.xui.GetSubscriptionLink(h.xui.GetExternalURL(h.cfg.XUIHost), client.SubID, h.cfg.XUISubPath)

	sub := &database.Subscription{
		TelegramID:      chatID,
		Username:        username,
		ClientID:        client.ID,
		SubscriptionID:  client.SubID,
		InboundID:       h.cfg.XUIInboundID,
		TrafficLimit:    trafficBytes,
		ExpiryTime:      time.Time{}, // zero value — no expiry
		Status:          "active",
		SubscriptionURL: subscriptionURL,
		ReferredBy:      referredBy,
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

	// Update cache with new subscription
	h.cache.Set(chatID, sub)

	// Notify admin about new subscription
	h.notifyAdmin(ctx, username, chatID, subscriptionURL, time.Time{})
	logger.Info("Subscription created successfully",
		zap.String("username", username),
		zap.Int64("chat_id", chatID))
}
