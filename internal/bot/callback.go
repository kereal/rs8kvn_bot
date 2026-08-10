// Package bot contains Telegram handlers, callbacks, keyboards, and user flows.
package bot

import (
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"github.com/kereal/rs8kvn_bot/internal/logger"
)

// CallbackHandler processes callback queries from inline keyboards.
type CallbackHandler struct {
	h *Handler
}

// NewCallbackHandler constructs CallbackHandler with parent reference.
func NewCallbackHandler(parent *Handler) *CallbackHandler {
	return &CallbackHandler{h: parent}
}

// HandleCallback routes callback data to appropriate handlers.
func (c *CallbackHandler) HandleCallback(ctx context.Context, update tgbotapi.Update) error {
	if update.CallbackQuery == nil {
		logger.Error("HandleCallback called with nil CallbackQuery")
		return fmt.Errorf("nil callback query")
	}
	if update.CallbackQuery.From == nil {
		logger.Error("HandleCallback: CallbackQuery.From is nil",
			zap.String("data", update.CallbackQuery.Data))
		return fmt.Errorf("nil from")
	}
	if update.CallbackQuery.Message == nil {
		logger.Warn("CallbackQuery has nil Message, skipping",
			zap.String("data", update.CallbackQuery.Data),
			zap.Int64("from_id", update.CallbackQuery.From.ID))
		callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "Сообщение не найдено")
		if _, err := c.h.bot.Request(callback); err != nil {
			logger.Error("Failed to answer callback", zap.Error(err))
		}
		return nil
	}

	data := update.CallbackQuery.Data
	chatID := update.CallbackQuery.Message.Chat.ID
	username := c.h.getUsername(update.CallbackQuery.From)

	logger.Debug("Callback received",
		append(userFields(update.CallbackQuery.From, chatID), zap.String("data", data))...)

	switch data {
	case "create_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleCreateSubscription(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle create_subscription: %w", err)
		}
	case "qr_code":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleQRCode(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle qr_code: %w", err)
		}
	case "admin_stats":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleAdminStats(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle admin_stats: %w", err)
		}
	case "admin_lastreg":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleAdminLastReg(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle admin_lastreg: %w", err)
		}
	case "menu_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleMySubscription(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle menu_subscription: %w", err)
		}
	case "back_to_start":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleBackToStart(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle back_to_start: %w", err)
		}
	case "menu_donate":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleMenuDonate(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle menu_donate: %w", err)
		}
	case "buy_premium_230":
		answer := tgbotapi.CallbackConfig{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Скоро в продаже",
		}
		if _, err := c.h.bot.Request(answer); err != nil {
			logger.Error("Failed to answer callback", zap.Error(err))
		}
	case "upgrade_premium":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleUpgradePremium(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle upgrade_premium: %w", err)
		}
	case "confirm_upgrade_premium":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleConfirmUpgradePremium(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle confirm_upgrade_premium: %w", err)
		}
	case "back_to_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleBackToSubscription(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle back_to_subscription: %w", err)
		}
	case "menu_help":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleMenuHelp(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle menu_help: %w", err)
		}
	case "back_to_documents":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleBackToDocuments(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle back_to_documents: %w", err)
		}
	case "menu_documents":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleMenuDocuments(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle menu_documents: %w", err)
		}
	case "menu_privacy":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleMenuPrivacy(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle menu_privacy: %w", err)
		}
	case "menu_terms":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleMenuTerms(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle menu_terms: %w", err)
		}
	case "menu_support":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleMenuSupport(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle menu_support: %w", err)
		}
	case "share_invite":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.handleShareInvite(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle share_invite: %w", err)
		}
	case "qr_telegram":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.handleQRTelegram(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle qr_telegram: %w", err)
		}
	case "qr_web":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.handleQRWeb(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle qr_web: %w", err)
		}
	case "broadcast_confirm":
		if err := c.h.handleBroadcastConfirm(ctx, chatID); err != nil {
			return fmt.Errorf("handle broadcast_confirm: %w", err)
		}
	case "broadcast_cancel":
		if err := c.h.handleBroadcastCancel(ctx, chatID); err != nil {
			return fmt.Errorf("handle broadcast_cancel: %w", err)
		}
	case "back_to_invite":
		messageID := update.CallbackQuery.Message.MessageID
		if err := c.h.handleBackToInvite(ctx, chatID, username, messageID); err != nil {
			return fmt.Errorf("handle back_to_invite: %w", err)
		}
	default:
		logger.Warn("Unknown callback data", zap.String("data", data))
	}
	callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "")
	if _, err := c.h.bot.Request(callback); err != nil {
		logger.Error("Failed to answer callback", zap.Error(err))
	}
	return nil
}

// handleShareInvite generates and sends an invite link.
func (c *CallbackHandler) handleShareInvite(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting share invite", zap.String("username", username), zap.Int64("chat_id", chatID))
	return c.h.referral.sendInviteLink(ctx, chatID, messageID)
}

// handleQRTelegram generates QR for Telegram invite link.
func (c *CallbackHandler) handleQRTelegram(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting QR for Telegram invite", zap.String("username", username), zap.Int64("chat_id", chatID))

	link, err := c.h.referral.generateInviteLink(ctx, chatID, linkTypeTelegram)
	if err != nil {
		logger.Error("Failed to get invite for QR", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка генерации QR-кода. Попробуйте позже.")
		c.h.safeSend(editMsg)
		return fmt.Errorf("generate invite link (telegram): %w", err)
	}

	if err := c.h.sendQRCode(ctx, chatID, messageID, link, "📱 QR-код для Telegram\n\nПокажите этот QR-код для быстрого добавления в Telegram"); err != nil {
		return fmt.Errorf("send qr code: %w", err)
	}
	return nil
}

// handleQRWeb generates QR for web invite page.
func (c *CallbackHandler) handleQRWeb(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting QR for web invite", zap.String("username", username), zap.Int64("chat_id", chatID))

	link, err := c.h.referral.generateInviteLink(ctx, chatID, linkTypeWeb)
	if err != nil {
		logger.Error("Failed to get invite for QR", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка генерации QR-кода. Попробуйте позже.")
		c.h.safeSend(editMsg)
		return fmt.Errorf("generate invite link (web): %w", err)
	}

	if err := c.h.sendQRCode(ctx, chatID, messageID, link, "🌐 QR-код для веб-страницы\n\nПокажите этот QR-код для открытия страницы с подпиской"); err != nil {
		return fmt.Errorf("send qr code: %w", err)
	}
	return nil
}
