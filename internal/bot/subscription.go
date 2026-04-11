package bot

import (
	"context"
	"errors"
	"fmt"
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
		if sub.Status == "active" {
			return sub, nil
		}
		// Stale cache entry (non-active subscription) — invalidate and fall through to DB
		h.invalidateCache(chatID)
	}

	// Cache miss, query database
	sub, err := h.db.GetByTelegramID(ctx, chatID)
	if err != nil {
		return nil, err
	}

	// Store in cache only if active
	if sub != nil && sub.Status == "active" {
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
			errMsg := msg(MsgSubTempError)
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
			h.safeSend(editMsg)
			return
		}
	} else if sub != nil {
		// Return existing active subscription - edit the message
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubCreatedSuccess, h.cfg.TrafficLimitGB, sub.SubscriptionURL))
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
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
			h.safeSend(editMsg)
			return
		} else {
			logger.Error("Failed to get subscription", zap.Error(err))
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubTempError))
			h.safeSend(editMsg)
			return
		}
	}

	if sub.Status != "active" {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
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
	progressBar := utils.GenerateProgressBar(trafficUsedGB, trafficLimitGB)

	// Format dates
	createdAt := utils.FormatDateRu(sub.CreatedAt)

	// Calculate traffic reset: if ExpiryTime is set, use it; otherwise use CreatedAt + reset days
	var resetTime time.Time
	if sub.ExpiryTime.IsZero() {
		resetTime = sub.CreatedAt.AddDate(0, 0, config.SubscriptionResetDay)
	} else {
		resetTime = sub.ExpiryTime
	}
	daysUntilTrafficReset := utils.DaysUntilReset(time.Now(), resetTime)

	// Build reset info string
	var resetInfo string
	switch {
	case daysUntilTrafficReset < 0:
		resetInfo = "🔄 Сброс: отключен"
	case daysUntilTrafficReset == 0:
		resetInfo = "🔄 Сброс: сегодня"
	default:
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
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
		h.safeSend(editMsg)
		return
	}

	// Generate QR code
	pngBytes, err := utils.GenerateQRCodePNG(sub.SubscriptionURL)
	if err != nil {
		logger.Error("Failed to generate QR code", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgQRCodeFailed))
		h.safeSend(editMsg)
		return
	}

	// Send QR code as photo (instant, no delete)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  "qr.png",
		Bytes: pngBytes,
	})
	photo.Caption = msg(MsgQRCodeCaption)
	photo.ParseMode = "Markdown"

	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ "+msg(MsgQRCodeBack), "back_to_subscription"),
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
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgQRCodeFailed))
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
			tgbotapi.NewInlineKeyboardButtonData("⬅️ "+msg(MsgQRCodeBack), "back_to_invite"),
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

	classified := classifyXUIError(err)

	errMsg := msg(MsgErrGeneric)
	switch {
	case errors.Is(classified, ErrXUIConnection):
		errMsg = msg(MsgErrConnection)
	case errors.Is(classified, ErrXUIAuth):
		errMsg = msg(MsgErrAuth)
	case errors.Is(classified, ErrXUIContextCanceled):
		errMsg = msg(MsgErrRequestCanceled)
	case errors.Is(classified, ErrXUIDNS):
		errMsg = msg(MsgErrDialTCP)
	case errors.Is(classified, ErrXUITLS):
		errMsg = msg(MsgErrTLS)
	case errors.Is(classified, ErrXUIServer):
		errMsg = msg(MsgErrInboundNotFound)
	case errors.Is(classified, ErrXUIRollbackFailed):
		errMsg = msg(MsgErrPartialSave)
		h.notifyAdminError(ctx, fmt.Sprintf("⚠️ ORPHAN CLIENT WARNING: %v", err))
	}

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
	editMsg.DisableWebPagePreview = true
	h.safeSend(editMsg)
}
