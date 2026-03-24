package bot

import (
	"context"

	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// handleBackToStart handles the "back_to_start" callback
// Edits message to show main menu (instant for text messages)
func (h *Handler) handleBackToStart(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User returning to start", zap.String("username", username))

	// Check if user has an active subscription
	sub, err := h.db.GetByTelegramID(ctx, chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	// Get main menu content
	text, keyboard := h.getMainMenuContent(username, hasSubscription, chatID)

	// Edit message to show main menu (instant)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
}

// handleMenuDonate handles the "menu_donate" callback - shows donate message with back button
func (h *Handler) handleMenuDonate(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User viewing donate", zap.String("username", username))

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, h.getDonateText())
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
}

// handleMenuHelp handles the "menu_help" callback - shows help message with back button
func (h *Handler) handleMenuHelp(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User viewing help", zap.String("username", username))

	sub, err := h.db.GetByTelegramID(ctx, chatID)
	if err != nil || sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка: подписка не найдена.")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
		return
	}

	text := h.getHelpText(h.cfg.TrafficLimitGB, sub.SubscriptionURL)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
}
