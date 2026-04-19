package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SubscriptionHandler groups subscription-related business logic.
type SubscriptionHandler struct {
	h *Handler // parent
}

// NewSubscriptionHandler creates a new SubscriptionHandler.
func NewSubscriptionHandler(parent *Handler) *SubscriptionHandler {
	return &SubscriptionHandler{h: parent}
}

// getSubscriptionWithCache retrieves a subscription using cache first, then DB.
func (sh *SubscriptionHandler) getSubscriptionWithCache(ctx context.Context, chatID int64) (*database.Subscription, error) {
	// Try cache first
	if sub := sh.h.cache.Get(chatID); sub != nil {
		if sub.Status == "active" {
			return sub, nil
		}
		// Stale cache entry (non-active) — invalidate and fall through
		sh.h.invalidateCache(chatID)
	}

	// Cache miss, query database
	sub, err := sh.h.db.GetByTelegramID(ctx, chatID)
	if err != nil {
		return nil, err
	}

	// Store in cache only if active
	if sub != nil && sub.Status == "active" {
		sh.h.cache.Set(chatID, sub)
	}

	return sub, nil
}

// handleCreateSubscription handles the "create_subscription" callback or deep link flow.
func (sh *SubscriptionHandler) handleCreateSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting subscription", zap.String("username", username))

	// Prevent duplicate creation
	if _, loaded := sh.h.inProgressSyncMap.LoadOrStore(chatID, true); loaded {
		logger.Info("Subscription creation already in progress", zap.Int64("chat_id", chatID))
		return nil
	}
	defer sh.h.inProgressSyncMap.Delete(chatID)

	sub, err := sh.getSubscriptionWithCache(ctx, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Info("No existing subscription, creating new one", zap.String("username", username))
		} else {
			logger.Error("Failed to check subscription", zap.Error(err))
			errMsg := msg(MsgSubTempError)
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
			sh.h.safeSend(editMsg)
			return fmt.Errorf("check subscription: %w", err)
		}
	} else if sub != nil {
		// Existing active subscription
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubCreatedSuccess, sh.h.cfg.TrafficLimitGB, sub.SubscriptionURL))
		editMsg.ParseMode = "Markdown"
		editMsg.DisableWebPagePreview = true
		kb := sh.h.getQRKeyboard()
		editMsg.ReplyMarkup = &kb
		sh.h.safeSend(editMsg)
		return nil
	}

	// No existing, create new
	return sh.createSubscription(ctx, chatID, username, messageID)
}

// handleMySubscription displays user's subscription details.
func (sh *SubscriptionHandler) handleMySubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User checking subscription status", zap.String("username", username))

	messageID = sh.h.showLoadingMessage(chatID, messageID)
	if messageID == 0 {
		return nil
	}

	sub, traffic, err := sh.h.subscriptionService.GetWithTraffic(ctx, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
			sh.h.safeSend(editMsg)
			return nil
		}
		logger.Error("Failed to get subscription", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubTempError))
		sh.h.safeSend(editMsg)
		return fmt.Errorf("get subscription: %w", err)
	}

	if sub.Status != "active" {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
		sh.h.safeSend(editMsg)
		return nil
	}

	trafficInfo := fmt.Sprintf("%.2f из %d Гб (%.0f%%)", traffic.UsedGB, traffic.LimitGB, traffic.Percentage)

	messageText := fmt.Sprintf(
		"📋 *Ваша подписка*\n\n📊 Трафик: %s\n%s\n\n📅 Создана: %s\n%s\n\n🔗 Ссылка\n`%s`",
		trafficInfo,
		traffic.ProgressBar,
		traffic.CreatedAtFormatted,
		traffic.ResetInfo,
		sub.SubscriptionURL,
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, messageText)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	kb := sh.h.getQRKeyboard()
	editMsg.ReplyMarkup = &kb
	sh.h.safeSend(editMsg)
	return nil
}

// handleQRCode generates and sends QR code for subscription.
func (sh *SubscriptionHandler) handleQRCode(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting QR code", zap.String("username", username))

	sub, err := sh.h.db.GetByTelegramID(ctx, chatID)
	if err != nil || sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
		sh.h.safeSend(editMsg)
		return nil
	}

	pngBytes, err := utils.GenerateQRCodePNG(sub.SubscriptionURL)
	if err != nil {
		logger.Error("Failed to generate QR code", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgQRCodeFailed))
		sh.h.safeSend(editMsg)
		return fmt.Errorf("generate QR code: %w", err)
	}

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

	if _, err := sh.h.bot.Send(photo); err != nil {
		logger.Error("Failed to send QR photo", zap.Error(err))
		return fmt.Errorf("send QR photo: %w", err)
	}
	return nil
}

// handleBackToSubscription deletes the QR photo message.
func (sh *SubscriptionHandler) handleBackToSubscription(_ context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User closing QR code", zap.String("username", username))
	deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
	if _, err := sh.h.bot.Request(deleteMsg); err != nil {
		logger.Warn("Failed to delete QR message", zap.Error(err))
	}
	return nil
}

// sendQRCode generates and sends a QR code for the given URL.
func (sh *SubscriptionHandler) sendQRCode(ctx context.Context, chatID int64, messageID int, url string, caption string) error {
	pngBytes, err := utils.GenerateQRCodePNG(url)
	if err != nil {
		logger.Error("Failed to generate QR code", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgQRCodeFailed))
		sh.h.safeSend(editMsg)
		return fmt.Errorf("generate QR code: %w", err)
	}

	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  "qr.png",
		Bytes: pngBytes,
	})
	photo.Caption = caption
	photo.ParseMode = "Markdown"

	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ "+msg(MsgQRCodeBack), "back_to_invite"),
		),
	)
	photo.ReplyMarkup = &backKeyboard

	if _, err := sh.h.bot.Send(photo); err != nil {
		logger.Error("Failed to send QR photo", zap.Error(err))
		return fmt.Errorf("send QR photo: %w", err)
	}
	return nil
}

// handleBackToInvite deletes the QR photo and returns to invite.
func (sh *SubscriptionHandler) handleBackToInvite(_ context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User closing QR code", zap.String("username", username))
	deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
	if _, err := sh.h.bot.Request(deleteMsg); err != nil {
		logger.Warn("Failed to delete QR message", zap.Error(err))
	}
	return nil
}

// createSubscription creates a new subscription (atomic with rollback).
func (sh *SubscriptionHandler) createSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	messageID = sh.h.showLoadingMessage(chatID, messageID)
	if messageID == 0 {
		// Could not show loading; treat as error?
		return fmt.Errorf("failed to show loading message")
	}

	logger.Info("Creating subscription",
		zap.String("username", username),
		zap.Int("traffic_gb", sh.h.cfg.TrafficLimitGB))

	result, err := sh.h.subscriptionService.Create(ctx, chatID, username)
	if err != nil {
		sh.handleCreateError(ctx, chatID, messageID, username, err)
		return fmt.Errorf("create subscription: %w", err)
	}

	sh.h.pendingMu.Lock()
	if pending, ok := sh.h.pendingInvites[chatID]; ok {
		if time.Now().Before(pending.expiresAt) {
			invite, _ := sh.h.db.GetInviteByCode(ctx, pending.code)
			if invite != nil && invite.ReferrerTGID > 0 {
				result.Subscription.ReferredBy = invite.ReferrerTGID
				sh.h.IncrementReferralCount(invite.ReferrerTGID)
			}
		}
		delete(sh.h.pendingInvites, chatID)
	}
	sh.h.pendingMu.Unlock()

	sh.h.cache.Set(chatID, result.Subscription)
	if err := sh.h.notifyAdmin(ctx, username, chatID, result.SubscriptionURL, time.Time{}); err != nil {
		logger.Warn("Failed to notify admin of new subscription", zap.Error(err))
	}

	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, sh.h.getHelpText(sh.h.cfg.TrafficLimitGB, result.SubscriptionURL))
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &backKeyboard
	sh.h.safeSend(editMsg)

	logger.Info("Subscription created successfully",
		zap.String("username", username),
		zap.Int64("chat_id", chatID))
	return nil
}

// handleCreateError handles errors from createSubscription.
func (sh *SubscriptionHandler) handleCreateError(ctx context.Context, chatID int64, messageID int, username string, err error) error {
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
		sh.h.notifyAdminError(ctx, fmt.Sprintf("⚠️ ORPHAN CLIENT WARNING: %v", err))
	}

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
	editMsg.DisableWebPagePreview = true
	sh.h.safeSend(editMsg)
	return err
}
