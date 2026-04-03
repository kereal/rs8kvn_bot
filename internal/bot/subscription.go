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
	if _, loaded := h.inProgressSyncMap.LoadOrStore(chatID, true); loaded {
		logger.Info("Subscription creation already in progress", zap.Int64("chat_id", chatID))
		return
	}
	defer h.inProgressSyncMap.Delete(chatID)

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
		// Return existing active subscription - edit the message
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, fmt.Sprintf(
			"✅ Ваша подписка\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка\n`%s`",
			h.cfg.TrafficLimitGB,
			sub.SubscriptionURL,
		))
		editMsg.ParseMode = "Markdown"
		editMsg.DisableWebPagePreview = true
		kb := h.getQRKeyboard()
		editMsg.ReplyMarkup = &kb
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

	// Calculate percentage for progress bar
	trafficLimitGB := float64(h.cfg.TrafficLimitGB)
	percentage := 0.0
	if trafficLimitGB > 0 {
		percentage = (trafficUsedGB / trafficLimitGB) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	// Format traffic info
	trafficInfo := fmt.Sprintf("%.2f из %d Гб (%.0f%%)", trafficUsedGB, h.cfg.TrafficLimitGB, percentage)
	progressBar := generateProgressBar(trafficUsedGB, trafficLimitGB)

	// Format dates
	createdAt := formatDateRu(sub.CreatedAt)

	// Calculate traffic reset: if ExpiryTime is set, use it; otherwise use CreatedAt + reset days
	var resetTime time.Time
	if sub.ExpiryTime.IsZero() {
		resetTime = sub.CreatedAt.AddDate(0, 0, config.SubscriptionResetDay)
	} else {
		resetTime = sub.ExpiryTime
	}
	daysUntilTrafficReset := daysUntilReset(time.Now(), resetTime)

	// Build reset info string
	var resetInfo string
	if daysUntilTrafficReset < 0 {
		resetInfo = "🔄 Сброс: отключен"
	} else if daysUntilTrafficReset == 0 {
		resetInfo = "🔄 Сброс: сегодня"
	} else {
		resetInfo = fmt.Sprintf("🔄 Сброс: через %d дн.", daysUntilTrafficReset)
	}

	// Build message
	messageText := fmt.Sprintf(
		"📋 *Ваша подписка*\n\n📊 Трафик: %s\n%s\n\n📅 Создана: %s\n%s\n\n🔗 Ссылка\n`%s`",
		trafficInfo,
		progressBar,
		createdAt,
		resetInfo,
		sub.SubscriptionURL,
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, messageText)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	kb := h.getQRKeyboard()
	editMsg.ReplyMarkup = &kb
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

// sendQRCode generates and sends a QR code for the given URL.
// Used for both subscription and invite link QR codes.
func (h *Handler) sendQRCode(ctx context.Context, chatID int64, messageID int, url string, caption string) {
	// Generate QR code
	pngBytes, err := utils.GenerateQRCodePNG(url)
	if err != nil {
		logger.Error("Failed to generate QR code", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка генерации QR-кода. Попробуйте позже.")
		h.safeSend(editMsg)
		return
	}

	// Send QR code as photo
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  "qr.png",
		Bytes: pngBytes,
	})
	photo.Caption = caption
	photo.ParseMode = "Markdown"

	// Keyboard with back button
	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_invite"),
		),
	)
	photo.ReplyMarkup = &backKeyboard

	if _, err := h.bot.Send(photo); err != nil {
		logger.Error("Failed to send QR photo", zap.Error(err))
	}
}

// handleBackToInvite handles the "back_to_invite" callback.
// Deletes the QR photo message and returns to invite link message.
func (h *Handler) handleBackToInvite(_ context.Context, chatID int64, username string, messageID int) {
	logger.Info("User closing QR code", zap.String("username", username))

	// Delete the QR photo message
	deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
	if _, err := h.bot.Request(deleteMsg); err != nil {
		logger.Warn("Failed to delete QR message", zap.Error(err))
	}
	// Invite message is already visible above, no need to send anything
}

// createSubscription creates a new subscription for the user.
// This operation is atomic with rollback: if database save fails,
// the client is removed from the 3x-ui panel to prevent orphan records.
func (h *Handler) createSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	messageID = h.showLoadingMessage(chatID, messageID)
	if messageID == 0 {
		return
	}

	logger.Info("Creating subscription",
		zap.String("username", username),
		zap.Int("traffic_gb", h.cfg.TrafficLimitGB))

	result, err := h.subscriptionService.Create(ctx, chatID, username)
	if err != nil {
		h.handleCreateError(ctx, chatID, messageID, username, err)
		return
	}

	h.pendingMu.Lock()
	if pending, ok := h.pendingInvites[chatID]; ok {
		if time.Now().Before(pending.expiresAt) {
			invite, _ := h.db.GetInviteByCode(ctx, pending.code)
			if invite != nil && invite.ReferrerTGID > 0 {
				result.Subscription.ReferredBy = invite.ReferrerTGID
				// Increment referral cache
				h.IncrementReferralCount(invite.ReferrerTGID)
			}
		}
		delete(h.pendingInvites, chatID)
	}
	h.pendingMu.Unlock()

	h.cache.Set(chatID, result.Subscription)
	if err := h.notifyAdmin(ctx, username, chatID, result.SubscriptionURL, time.Time{}); err != nil {
		logger.Warn("Failed to notify admin of new subscription", zap.Error(err))
	}

	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, h.getHelpText(h.cfg.TrafficLimitGB, result.SubscriptionURL))
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &backKeyboard
	h.safeSend(editMsg)

	logger.Info("Subscription created successfully",
		zap.String("username", username),
		zap.Int64("chat_id", chatID))
}

func (h *Handler) handleCreateError(ctx context.Context, chatID int64, messageID int, username string, err error) {
	logger.Error("Failed to create subscription", zap.Error(err))

	errMsg := "❌ Ошибка при создании подписки."
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "timeout"):
		errMsg = "❌ Не удается подключиться к серверу. Попробуйте позже."
	case strings.Contains(errStr, "authentication") || strings.Contains(errStr, "unauthorized"):
		errMsg = "❌ Ошибка авторизации на сервере. Свяжитесь с администратором."
	case strings.Contains(errStr, "context canceled"):
		errMsg = "❌ Запрос был прерван. Попробуйте снова."
	case strings.Contains(errStr, "no such host") || strings.Contains(errStr, "dial tcp"):
		errMsg = "❌ Ошибка подключения к серверу. Проверьте настройки DNS."
	case strings.Contains(errStr, "certificate") || strings.Contains(errStr, "TLS"):
		errMsg = "❌ Ошибка SSL/TLS сертификата. Свяжитесь с администратором."
	case strings.Contains(errStr, "inbound") || strings.Contains(errStr, "client"):
		errMsg = "❌ Ошибка сервера при создании подписки. Попробуйте позже."
	case strings.Contains(errStr, "rollback failed"):
		errMsg = "❌ Подписка создана в панели, но не сохранена в базе. Обратитесь к администратору."
		h.notifyAdminError(ctx, fmt.Sprintf("⚠️ ORPHAN CLIENT WARNING: %v", err))
	}

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
	editMsg.DisableWebPagePreview = true
	h.safeSend(editMsg)
}

// generateProgressBar creates a visual progress bar using Unicode emojis.
// Returns a string like "🟩🟩🟩🟩🟩⬜⬜⬜⬜⬜" representing used/total ratio.
func generateProgressBar(usedGB, limitGB float64) string {
	if limitGB <= 0 {
		return "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜"
	}

	percentage := (usedGB / limitGB) * 100
	if percentage > 100 {
		percentage = 100
	}

	// 10 blocks total
	filled := int(percentage / 10)
	if filled > 10 {
		filled = 10
	}

	bar := ""
	for i := 0; i < 10; i++ {
		if i < filled {
			bar += "🟩"
		} else {
			bar += "⬜"
		}
	}

	return bar
}

// daysUntilReset calculates the number of days until the next traffic reset.
// Returns -1 if auto-reset is not configured (expiryTime is zero).
// Returns 0 if already expired (reset should happen now).
// Returns positive number of days until reset otherwise.
func daysUntilReset(now time.Time, expiryTime time.Time) int {
	if expiryTime.IsZero() {
		return -1 // Auto-reset not configured
	}

	if now.After(expiryTime) || now.Equal(expiryTime) {
		return 0 // Already expired, reset should happen now
	}

	duration := expiryTime.Sub(now)
	days := int(duration.Hours() / 24)

	if days < 0 {
		days = 0
	}

	return days
}

// formatDateRu formats a date in Russian locale (e.g., "15 января 2025").
func formatDateRu(t time.Time) string {
	months := []string{
		"января", "февраля", "марта", "апреля", "мая", "июня",
		"июля", "августа", "сентября", "октября", "ноября", "декабря",
	}

	day := t.Day()
	month := months[t.Month()-1]
	year := t.Year()

	return fmt.Sprintf("%d %s %d", day, month, year)
}
