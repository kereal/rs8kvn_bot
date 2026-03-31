package bot

import (
	"context"
	"fmt"

	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// HandleCallback handles callback queries from inline keyboards.
func (h *Handler) HandleCallback(ctx context.Context, update tgbotapi.Update) {
	if update.CallbackQuery == nil {
		logger.Error("HandleCallback called with nil CallbackQuery")
		return
	}

	if update.CallbackQuery.From == nil {
		logger.Error("HandleCallback: CallbackQuery.From is nil",
			zap.String("data", update.CallbackQuery.Data))
		return
	}

	// Check if Message is nil (can happen with inline mode callbacks)
	if update.CallbackQuery.Message == nil {
		logger.Warn("CallbackQuery has nil Message, skipping",
			zap.String("data", update.CallbackQuery.Data),
			zap.Int64("from_id", update.CallbackQuery.From.ID))
		// Still answer the callback to remove loading state
		callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "Сообщение не найдено")
		if _, err := h.bot.Request(callback); err != nil {
			logger.Error("Failed to answer callback", zap.Error(err))
		}
		return
	}

	data := update.CallbackQuery.Data
	chatID := update.CallbackQuery.Message.Chat.ID
	username := h.getUsername(update.CallbackQuery.From)

	logger.Debug("Callback received",
		zap.String("data", data),
		zap.String("username", username),
		zap.Int64("chat_id", chatID))

	// Answer the callback to remove the loading state
	callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "")
	if _, err := h.bot.Request(callback); err != nil {
		logger.Error("Failed to answer callback", zap.Error(err))
	}

	switch data {
	case "create_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleCreateSubscription(ctx, chatID, username, messageID)
	case "qr_code":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleQRCode(ctx, chatID, username, messageID)
	case "admin_stats":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleAdminStats(ctx, chatID, username, messageID)
	case "admin_lastreg":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleAdminLastReg(ctx, chatID, username, messageID)
	case "back_to_start":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleBackToStart(ctx, chatID, username, messageID)
	case "menu_donate":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleMenuDonate(ctx, chatID, username, messageID)
	case "menu_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleMySubscription(ctx, chatID, username, messageID)
	case "back_to_subscription":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleBackToSubscription(ctx, chatID, username, messageID)
	case "menu_help":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleMenuHelp(ctx, chatID, username, messageID)
	case "share_invite":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleShareInvite(ctx, chatID, username, messageID)
	case "qr_telegram":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleQRTelegram(ctx, chatID, username, messageID)
	case "qr_web":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleQRWeb(ctx, chatID, username, messageID)
	case "back_to_invite":
		messageID := update.CallbackQuery.Message.MessageID
		h.handleBackToInvite(ctx, chatID, username, messageID)
	default:
		logger.Warn("Unknown callback data", zap.String("data", data))
	}
}

func (h *Handler) handleShareInvite(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User requesting share invite", zap.String("username", username))
	h.sendInviteLink(ctx, chatID, messageID)
}

type linkType string

const (
	linkTypeTelegram linkType = "telegram"
	linkTypeWeb      linkType = "web"
)

func (h *Handler) generateInviteLink(ctx context.Context, chatID int64, lt linkType) (string, error) {
	invite, err := h.db.GetOrCreateInvite(ctx, chatID, utils.GenerateInviteCode())
	if err != nil {
		return "", fmt.Errorf("get invite: %w", err)
	}

	switch lt {
	case linkTypeTelegram:
		return fmt.Sprintf("https://t.me/%s?start=share_%s", h.botConfig.Username, invite.Code), nil
	case linkTypeWeb:
		return fmt.Sprintf("%s/i/%s", h.cfg.SiteURL, invite.Code), nil
	default:
		return "", fmt.Errorf("unknown link type: %s", lt)
	}
}

func (h *Handler) handleQRTelegram(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User requesting QR for Telegram invite", zap.String("username", username))

	link, err := h.generateInviteLink(ctx, chatID, linkTypeTelegram)
	if err != nil {
		logger.Error("Failed to get invite for QR", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка генерации QR-кода. Попробуйте позже.")
		h.safeSend(editMsg)
		return
	}

	h.sendQRCode(ctx, chatID, messageID, link, "📱 QR-код для Telegram\n\nПокажите этот QR-код для быстрого добавления в Telegram")
}

func (h *Handler) handleQRWeb(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User requesting QR for web invite", zap.String("username", username))

	link, err := h.generateInviteLink(ctx, chatID, linkTypeWeb)
	if err != nil {
		logger.Error("Failed to get invite for QR", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка генерации QR-кода. Попробуйте позже.")
		h.safeSend(editMsg)
		return
	}

	h.sendQRCode(ctx, chatID, messageID, link, "🌐 QR-код для веб-страницы\n\nПокажите этот QR-код для открытия страницы с подпиской")
}
