package bot

import (
	"context"
	"errors"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// handleBackToStart handles the "back_to_start" callback
// Edits message to show main menu (instant for text messages)
func (h *Handler) handleBackToStart(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User returning to start", zap.String("username", username), zap.Int64("chat_id", chatID))

	// Check if user has an active subscription
	sub, err := h.getSubscriptionWithCache(ctx, chatID)

	var hasSubscription bool

	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			hasSubscription = false
		} else {
			logger.Error("Failed to get subscription", zap.Error(err))

			hasSubscription = false
		}
	} else {
		hasSubscription = sub != nil && sub.Status == "active"
	}

	text, keyboard := h.getMainMenuContent(ctx, username, hasSubscription, chatID, sub)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)

	return nil
}

// handleMenuDonate handles the "menu_donate" callback - shows donate message with back button
func (h *Handler) handleMenuDonate(_ context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing donate", zap.String("username", username), zap.Int64("chat_id", chatID))
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
	logger.Info("User viewing help", zap.String("username", username), zap.Int64("chat_id", chatID))

	sub, err := h.getSubscriptionWithCache(ctx, chatID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
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

	if sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)

		return nil
	}

	trafficLimit := 0
	if h.subscriptionService != nil {
		trafficLimit = h.subscriptionService.PlanTrafficLimitGB(ctx, sub.TelegramID)
	}

	text := h.getHelpText(trafficLimit, h.cfg.SubURL(sub.SubscriptionID))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)

	return nil
}

// handleMenuDocuments handles the "menu_documents" callback - shows documents menu
func (h *Handler) handleMenuDocuments(_ context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing documents", zap.String("username", username), zap.Int64("chat_id", chatID))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "📑 *Документы*")
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.keyboards.Documents()

	editMsg.ReplyMarkup = &keyboard

	_, err := h.bot.Send(editMsg)
	if err != nil {
		return fmt.Errorf("send documents menu: %w", err)
	}

	return nil
}

// handleBackToDocuments closes the separate legal-content message.
func (h *Handler) handleBackToDocuments(_ context.Context, chatID int64, _ string, messageID int) error {
	logger.Info("User closing documents screen", zap.Int64("chat_id", chatID))

	h.documentMessagesMu.Lock()
	precedingIDs := append([]int(nil), h.documentMessages[messageID]...)
	delete(h.documentMessages, messageID)
	h.documentMessagesMu.Unlock()

	messageIDs := append(precedingIDs, messageID)
	var deleteErr error

	for _, id := range messageIDs {
		if id <= 0 {
			continue
		}

		deleteMsg := tgbotapi.NewDeleteMessage(chatID, id)
		if _, err := h.bot.Request(deleteMsg); err != nil {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("message %d: %w", id, err))
		}
	}

	if deleteErr != nil {
		return fmt.Errorf("delete documents screen: %w", deleteErr)
	}

	return nil
}

func (h *Handler) sendLegalText(ctx context.Context, chatID int64, messageID int, text string) error {
	chunks := splitMessage(text, 4096)
	if len(chunks) == 0 {
		return nil
	}

	precedingIDs := make([]int, 0, len(chunks)-1)
	finalMessageID := 0

	for i, chunk := range chunks {
		var keyboard *tgbotapi.InlineKeyboardMarkup

		if i == len(chunks)-1 {
			key := h.keyboards.BackToDocuments()
			keyboard = &key
		}

		msg := tgbotapi.NewMessage(chatID, chunk)
		msg.ParseMode = "Markdown"
		msg.DisableWebPagePreview = true

		msg.ReplyMarkup = keyboard
		sent, err := h.bot.Send(msg)
		if err != nil {
			logger.Error("Failed to send legal message", zap.Error(err))
			return fmt.Errorf("legal chunk send failed: %w", err)
		}

		if i < len(chunks)-1 && sent.MessageID > 0 {
			precedingIDs = append(precedingIDs, sent.MessageID)
		}

		if i == len(chunks)-1 {
			finalMessageID = sent.MessageID
		}
	}

	if len(precedingIDs) > 0 && finalMessageID > 0 {
		h.documentMessagesMu.Lock()
		if h.documentMessages == nil {
			h.documentMessages = make(map[int][]int)
		}
		h.documentMessages[finalMessageID] = precedingIDs
		h.documentMessagesMu.Unlock()
	}

	return nil
}

// handleMenuPrivacy handles the "menu_privacy" callback - shows privacy text with back button
func (h *Handler) handleMenuPrivacy(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing privacy", zap.String("username", username), zap.Int64("chat_id", chatID))
	return h.sendLegalText(ctx, chatID, messageID, h.keyboards.PrivacyText())
}

// handleMenuTerms handles the "menu_terms" callback - shows terms text with back button
func (h *Handler) handleMenuTerms(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing terms", zap.String("username", username), zap.Int64("chat_id", chatID))
	return h.sendLegalText(ctx, chatID, messageID, h.keyboards.TermsText())
}

// handleMenuSupport handles the "menu_support" callback - shows support text with back button
func (h *Handler) handleMenuSupport(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User viewing support", zap.String("username", username), zap.Int64("chat_id", chatID))
	return h.sendLegalText(ctx, chatID, messageID, h.keyboards.SupportText())
}
