package bot

import (
	"context"
	"errors"

	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// handleBackToStart handles the "back_to_start" callback
// Edits message to show main menu (instant for text messages)
func (h *Handler) handleBackToStart(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User returning to start", zap.String("username", username))

	// Check if user has an active subscription
	sub, err := h.getSubscriptionWithCache(ctx, chatID)
	var hasSubscription bool
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasSubscription = false
		} else {
			logger.Error("Failed to get subscription", zap.Error(err))
			hasSubscription = false
		}
	} else {
		hasSubscription = sub != nil && sub.Status == "active"
	}

	text, keyboard := h.getMainMenuContent(username, hasSubscription, chatID)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
	return nil
}

// handleMenuDonate handles the "menu_donate" callback - shows donate message with back button
func (h *Handler) handleMenuDonate(_ context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing donate", zap.String("username", username))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, h.getDonateText())
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
	return nil
}

// handleMenuHelp handles the "menu_help" callback - shows help message with back button
func (h *Handler) handleMenuHelp(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing help", zap.String("username", username))

	sub, err := h.getSubscriptionWithCache(ctx, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.")
			editMsg.DisableWebPagePreview = true
			keyboard := h.getBackKeyboard()
			editMsg.ReplyMarkup = &keyboard
			h.safeSend(editMsg)
			return nil
		}
		logger.Error("Failed to get subscription", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Временная ошибка. Попробуйте позже.")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
		return nil
	}

	text := h.getHelpText(h.cfg.TrafficLimitGB, sub.SubscriptionURL)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
	return nil
}
