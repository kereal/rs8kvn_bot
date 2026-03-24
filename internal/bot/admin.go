package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

func (h *Handler) handleAdminLastReg(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("Admin requesting last registrations", zap.String("username", username))

	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access last registrations", zap.Int64("chat_id", chatID))
		return
	}

	subs, err := h.db.GetLatestSubscriptions(ctx, 10)
	if err != nil {
		logger.Error("Failed to get latest subscriptions", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка получения списка подписок")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
		return
	}

	if len(subs) == 0 {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "📭 Нет активных подписок")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
		return
	}

	// Format the message as a table with 3 columns
	var sb strings.Builder
	sb.WriteString("📋 *Последние регистрации*\n\n")

	for _, sub := range subs {
		// Column 1: ID, Column 2: Username (clickable link), Column 3: Date and time
		username := sub.Username
		if username == "" {
			username = "unknown"
		}
		dateStr := sub.CreatedAt.Format("02.01.2006 15:04:05")
		fmt.Fprintf(&sb, "%d │ [@%s](https://t.me/%s) │ %s\n", sub.ID, username, username, dateStr)
	}

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, sb.String())
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
}

// HandleDel handles the /del command for admins.
// Deletes a subscription by database ID from both 3x-ui panel and database.
// Usage: /del <id>
func (h *Handler) HandleDel(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleDel called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /del", zap.Int64("chat_id", chatID))
		return
	}

	// Parse the command arguments
	args := update.Message.CommandArguments()
	if args == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /del <id>\n\nПример: /del 5")
		return
	}

	// Parse the ID - use int64 to properly detect negative numbers
	var parsedID int64
	if _, err := fmt.Sscanf(args, "%d", &parsedID); err != nil {
		h.SendMessage(ctx, chatID, "❌ Неверный формат ID. Использование: /del <id>\n\nПример: /del 5")
		return
	}

	// Validate ID is positive
	if parsedID <= 0 {
		h.SendMessage(ctx, chatID, "❌ ID должен быть положительным числом")
		return
	}

	id := uint(parsedID)

	// Get the subscription
	sub, err := h.db.GetByID(ctx, id)
	if err != nil {
		logger.Error("Failed to get subscription", zap.Error(err), zap.Uint("id", id))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Подписка с ID %d не найдена", id))
		return
	}

	// Delete from 3x-ui panel first
	if err := h.xui.DeleteClient(ctx, sub.InboundID, sub.ClientID); err != nil {
		logger.Error("Failed to delete client from 3x-ui",
			zap.Error(err),
			zap.String("client_id", sub.ClientID),
			zap.Int("inbound_id", sub.InboundID))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка удаления клиента из панели 3x-ui: %v", err))
		return
	}

	// Delete from database
	_, err = h.db.DeleteSubscriptionByID(ctx, id)
	if err != nil {
		logger.Error("Failed to delete subscription from database",
			zap.Error(err),
			zap.Uint("id", id))
		// Client already deleted from 3x-ui, but database delete failed
		// This is a warning, not a critical error since the client is gone from the panel
		h.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Клиент удален из панели, но ошибка удаления из базы: %v\n\nОбратитесь к администратору.", err))
		return
	}

	// Invalidate cache
	h.invalidateCache(sub.TelegramID)

	// Success
	logger.Info("Subscription deleted",
		zap.Uint("id", id),
		zap.String("username", sub.Username),
		zap.Int64("telegram_id", sub.TelegramID),
		zap.String("client_id", sub.ClientID))

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Подписка успешно удалена!\n\n"+
			"🆔 ID: %d\n"+
			"👤 Пользователь: @%s\n"+
			"🆔 Telegram ID: %d\n"+
			"🔗 Client ID: %s",
		id,
		sub.Username,
		sub.TelegramID,
		sub.ClientID,
	))
}

// Broadcast batch size for pagination
const broadcastBatchSize = 100

// HandleBroadcast handles the /broadcast command for admins to send messages to all users.
func (h *Handler) HandleBroadcast(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleBroadcast called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /broadcast", zap.Int64("chat_id", chatID))
		return
	}

	// Get the message to broadcast
	message := update.Message.CommandArguments()
	if message == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /broadcast <сообщение>\n\nПример: /broadcast Привет всем!")
		return
	}

	// Get total count for progress reporting
	totalCount, err := h.db.GetTotalTelegramIDCount(ctx)
	if err != nil {
		logger.Error("Failed to count telegram IDs", zap.Error(err))
		h.SendMessage(ctx, chatID, "❌ Ошибка получения списка пользователей")
		return
	}

	if totalCount == 0 {
		h.SendMessage(ctx, chatID, "❌ Нет пользователей для рассылки")
		return
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf("📤 Начинаю рассылку для %d пользователей...", totalCount))

	// Process in batches to avoid loading all IDs into memory
	const batchSize = 100
	successCount := 0
	failCount := 0
	offset := 0

	for offset < int(totalCount) {
		// Get batch of IDs
		ids, err := h.db.GetTelegramIDsBatch(ctx, offset, batchSize)
		if err != nil {
			logger.Error("Failed to get telegram IDs batch", zap.Error(err), zap.Int("offset", offset))
			break
		}

		for _, telegramID := range ids {
			// Check context cancellation to allow graceful shutdown
			select {
			case <-ctx.Done():
				logger.Warn("Broadcast cancelled due to shutdown")
				h.SendMessage(ctx, chatID, fmt.Sprintf(
					"⚠️ Рассылка прервана!\n\n📤 Отправлено: %d\n❌ Ошибок: %d\n👥 Осталось: %d",
					successCount,
					failCount,
					int(totalCount)-successCount-failCount,
				))
				return
			default:
			}

			msg := tgbotapi.NewMessage(telegramID, message)
			msg.ParseMode = "Markdown"
			msg.DisableWebPagePreview = true
			if _, err := h.bot.Send(msg); err != nil {
				logger.Warn("Failed to send broadcast message",
					zap.Int64("telegram_id", telegramID),
					zap.Error(err))
				failCount++
			} else {
				successCount++
			}
			// Small delay to avoid rate limiting
			time.Sleep(50 * time.Millisecond)
		}

		offset += batchSize
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Рассылка завершена!\n\n📤 Отправлено: %d\n❌ Ошибок: %d\n👥 Всего пользователей: %d",
		successCount,
		failCount,
		totalCount,
	))

	logger.Info("Broadcast completed",
		zap.Int("success", successCount),
		zap.Int("failed", failCount),
		zap.Int64("total", totalCount))
}

// HandleSend handles the /send command for admins to send a message to a specific user.
func (h *Handler) HandleSend(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleSend called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /send", zap.Int64("chat_id", chatID))
		return
	}

	// Parse the command arguments
	args := update.Message.CommandArguments()
	if args == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /send <telegram_id|username> <сообщение>\n\nПримеры:\n/send 123456789 Привет!\n/send @username Привет!")
		return
	}

	// Split args into target and message
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 2 {
		h.SendMessage(ctx, chatID, "❌ Использование: /send <telegram_id|username> <сообщение>\n\nПримеры:\n/send 123456789 Привет!\n/send @username Привет!")
		return
	}

	target := strings.TrimPrefix(parts[0], "@")
	message := parts[1]

	// Try to parse as Telegram ID first, then as username
	var telegramID int64
	var err error

	// Check if target is a number (Telegram ID)
	if id, parseErr := strconv.ParseInt(target, 10, 64); parseErr == nil {
		telegramID = id
	} else {
		// Try to find by username
		telegramID, err = h.db.GetTelegramIDByUsername(ctx, target)
		if err != nil {
			h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Пользователь @%s не найден в базе", target))
			return
		}
	}

	// Send the message
	msg := tgbotapi.NewMessage(telegramID, message)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true
	sentMsg, err := h.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send message",
			zap.Int64("telegram_id", telegramID),
			zap.Error(err))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка отправки сообщения: %v", err))
		return
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Сообщение отправлено!\n\n👤 Получатель: %d\n💬 ID сообщения: %d",
		telegramID,
		sentMsg.MessageID,
	))

	logger.Info("Message sent via /send command",
		zap.Int64("telegram_id", telegramID),
		zap.Int64("admin_id", chatID))
}

// handleAdminStats handles the "admin stats" callback.
func (h *Handler) handleAdminStats(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("Admin requesting stats", zap.String("username", username))

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access admin stats", zap.Int64("chat_id", chatID))
		return
	}

	// Get counts efficiently using SQL COUNT queries
	totalCount, err := h.db.CountActiveSubscriptions(ctx)
	if err != nil {
		logger.Error("Failed to count subscriptions for stats", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка получения статистики")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
		return
	}

	activeCount, err := h.db.CountActiveSubscriptions(ctx)
	if err != nil {
		logger.Error("Failed to count active subscriptions", zap.Error(err))
		activeCount = 0
	}

	expiredCount, err := h.db.CountExpiredSubscriptions(ctx)
	if err != nil {
		logger.Error("Failed to count expired subscriptions", zap.Error(err))
		expiredCount = 0
	}

	text := fmt.Sprintf(
		"📊 *Статистика бота*\n\n👥 Всего пользователей: %d\n✅ Активные подписки: %d\n⏰ Истекшие: %d",
		totalCount,
		activeCount,
		expiredCount,
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
}

// notifyAdmin sends a notification to the admin about a new subscription.
func (h *Handler) notifyAdmin(ctx context.Context, username string, chatID int64, subscriptionURL string, expiryTime time.Time) {
	if h.cfg.TelegramAdminID == 0 {
		return
	}

	msg := tgbotapi.NewMessage(h.cfg.TelegramAdminID,
		fmt.Sprintf("🔔 Новая подписка создана!\n\n👤 Пользователь: @%s\n🆔 ID: %d\n📅 Истекает: %s\n🔗 Подписка: `%s`",
			username,
			chatID,
			expiryTime.Format("02.01.2006 15:04:05"),
			subscriptionURL,
		))
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)

	logger.Info("Admin notified about new subscription", zap.String("username", username))
}

// notifyAdminError sends an error notification to the admin.
func (h *Handler) notifyAdminError(ctx context.Context, message string) {
	if h.cfg.TelegramAdminID == 0 {
		return
	}

	msg := tgbotapi.NewMessage(h.cfg.TelegramAdminID, message)
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}
