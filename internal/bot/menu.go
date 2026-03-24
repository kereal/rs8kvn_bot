package bot

import (
	"context"
	"fmt"

	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// handleBackToStart handles the "back_to_start" callback
// Edits message to show main menu with InlineKeyboard
func (h *Handler) handleBackToStart(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User returning to start", zap.String("username", username))

	// Check if user has an active subscription
	sub, err := h.db.GetByTelegramID(ctx, chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	if hasSubscription {
		// User has subscription - edit message with inline menu keyboard
		text := fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nИспользуйте кнопки ниже для взаимодействия с ботом.",
			username,
		)

		keyboard := h.getMainMenuKeyboard()
		// Add admin buttons if user is admin
		if h.isAdmin(chatID) {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
	} else {
		// User has no subscription - edit message with inline button to get subscription
		text := fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nНажмите кнопку ниже, чтобы получить подписку:",
			username,
		)

		inlineKeyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "get_subscription"),
			),
		)

		// Add admin buttons if user is admin
		if h.isAdmin(chatID) {
			inlineKeyboard.InlineKeyboard = append(inlineKeyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &inlineKeyboard
		h.safeSend(editMsg)
	}
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

// handleMenuSubscription handles the "menu_subscription" callback - shows subscription info with back button
func (h *Handler) handleMenuSubscription(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User viewing subscription", zap.String("username", username))

	sub, err := h.db.GetByTelegramID(ctx, chatID)
	if err != nil || sub == nil {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ У вас нет активной подписки.\n\nНажмите «🏠 В начало» для получения подписки.")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
		return
	}

	if sub.IsExpired() {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "⚠️ Ваша подписка истекла.\n\nНажмите «🏠 В начало» для создания новой.")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
		return
	}

	// Get traffic usage from 3x-ui panel
	trafficUsedGB := float64(0)
	traffic, err := h.xui.GetClientTraffic(ctx, sub.Username)
	if err != nil {
		logger.Warn("Failed to get client traffic from panel", zap.String("username", sub.Username), zap.Error(err))
	} else {
		trafficUsedGB = float64(traffic.Up+traffic.Down) / 1024 / 1024 / 1024
	}

	trafficInfo := fmt.Sprintf("%.2f / %d ГБ", trafficUsedGB, h.cfg.TrafficLimitGB)
	expiryDate := sub.ExpiryTime.Format("02.01.2006")

	text := fmt.Sprintf(
		"📋 *Информация о вашей подписке*\n\n📊 Трафик: %s\n📅 Сброс: %s\n\n🔗 Ссылка:\n`%s`",
		trafficInfo,
		expiryDate,
		sub.SubscriptionURL,
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
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
