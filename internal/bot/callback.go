package bot

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"rs8kvn_bot/internal/logger"
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
		zap.String("data", data),
		zap.String("username", username),
		zap.Int64("chat_id", chatID))

	callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "")
	if _, err := c.h.bot.Request(callback); err != nil {
		logger.Error("Failed to answer callback", zap.Error(err))
	}

	switch data {
	case "create_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleCreateSubscription(ctx, chatID, username, messageID)
	case "qr_code":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleQRCode(ctx, chatID, username, messageID)
	case "admin_stats":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleAdminStats(ctx, chatID, username, messageID)
	case "admin_lastreg":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleAdminLastReg(ctx, chatID, username, messageID)
	case "back_to_start":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleBackToStart(ctx, chatID, username, messageID)
	case "menu_donate":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleMenuDonate(ctx, chatID, username, messageID)
	case "menu_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleMySubscription(ctx, chatID, username, messageID)
	case "back_to_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleBackToSubscription(ctx, chatID, username, messageID)
	case "menu_help":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleMenuHelp(ctx, chatID, username, messageID)
	case "share_invite":
		messageID := update.CallbackQuery.Message.MessageID
		return c.handleShareInvite(ctx, chatID, username, messageID)
	case "qr_telegram":
		messageID := update.CallbackQuery.Message.MessageID
		return c.handleQRTelegram(ctx, chatID, username, messageID)
	case "qr_web":
		messageID := update.CallbackQuery.Message.MessageID
		return c.handleQRWeb(ctx, chatID, username, messageID)
	case "back_to_invite":
		messageID := update.CallbackQuery.Message.MessageID
		return c.h.handleBackToInvite(ctx, chatID, username, messageID)
	default:
		logger.Warn("Unknown callback data", zap.String("data", data))
		return nil
	}
}

// handleShareInvite generates and sends an invite link.
func (c *CallbackHandler) handleShareInvite(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting share invite", zap.String("username", username))
	return c.h.referral.sendInviteLink(ctx, chatID, messageID)
}

// handleQRTelegram generates QR for Telegram invite link.
func (c *CallbackHandler) handleQRTelegram(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting QR for Telegram invite", zap.String("username", username))

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
	logger.Info("User requesting QR for web invite", zap.String("username", username))

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

// handleMenuHelp displays help message with back button.
func (c *CallbackHandler) handleMenuHelp(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing help", zap.String("username", username))

	sub, err := c.h.getSubscriptionWithCache(ctx, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.")
			editMsg.DisableWebPagePreview = true
			keyboard := c.h.getBackKeyboard()
			editMsg.ReplyMarkup = &keyboard
			c.h.safeSend(editMsg)
			return nil
		}
		logger.Error("Failed to get subscription", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Временная ошибка. Попробуйте позже.")
		editMsg.DisableWebPagePreview = true
		keyboard := c.h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		c.h.safeSend(editMsg)
		return fmt.Errorf("get subscription: %w", err)
	}

	text := c.h.getHelpText(c.h.cfg.TrafficLimitGB, sub.SubscriptionURL)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := c.h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	c.h.safeSend(editMsg)
	return nil
}

// handleMenuDonate shows donation info with back button.
func (c *CallbackHandler) handleMenuDonate(_ context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing donate", zap.String("username", username))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, c.h.getDonateText())
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := c.h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	c.h.safeSend(editMsg)
	return nil
}

// handleBackToStart returns to main menu.
func (c *CallbackHandler) handleBackToStart(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User returning to start", zap.String("username", username))

	sub, err := c.h.getSubscriptionWithCache(ctx, chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	text, keyboard := c.h.getMainMenuContent(username, hasSubscription, chatID)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &keyboard
	c.h.safeSend(editMsg)
	return nil
}

// handleMySubscription shows user's subscription details.
func (c *CallbackHandler) handleMySubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing subscription", zap.String("username", username))
	return c.h.handleMySubscription(ctx, chatID, username, messageID)
}

// handleBackToSubscription returns from QR view to subscription info.
func (c *CallbackHandler) handleBackToSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User back to subscription", zap.String("username", username))
	return c.h.handleBackToSubscription(ctx, chatID, username, messageID)
}

// handleQRCode shows QR code for subscription.
func (c *CallbackHandler) handleQRCode(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting QR code", zap.String("username", username))
	return c.h.handleQRCode(ctx, chatID, username, messageID)
}

// handleAdminStats shows admin statistics.
func (c *CallbackHandler) handleAdminStats(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("Admin viewing stats", zap.String("username", username))
	return c.h.handleAdminStats(ctx, chatID, username, messageID)
}

// handleAdminLastReg shows last registrations to admin.
func (c *CallbackHandler) handleAdminLastReg(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("Admin viewing last registrations", zap.String("username", username))
	return c.h.handleAdminLastReg(ctx, chatID, username, messageID)
}

// handleBackToInvite returns to invite menu.
func (c *CallbackHandler) handleBackToInvite(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User back to invite", zap.String("username", username))
	return c.h.handleBackToInvite(ctx, chatID, username, messageID)
}
