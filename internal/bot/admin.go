package bot

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

func (h *Handler) HandleVersion(ctx context.Context, update tgbotapi.Update) error {
	if update.Message == nil {
		logger.Error("HandleVersion called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID
	if !h.isAdmin(chatID) {
		return nil
	}

	logger.Info("Admin requesting version", zap.Int64("chat_id", chatID))
	h.SendMessage(ctx, chatID, h.version)
	return nil
}

func (h *Handler) handleAdminLastReg(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("Admin requesting last registrations", zap.String("username", username))

	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access last registrations", zap.Int64("chat_id", chatID))
		return nil
	}

	subs, err := h.db.GetLatestSubscriptions(ctx, 10)
	if err != nil {
		logger.Error("Failed to get latest subscriptions", zap.Error(err))
		h.sendLastRegText(ctx, chatID, messageID, "❌ Ошибка получения списка подписок", true)
		return fmt.Errorf("get latest subscriptions: %w", err)
	}

	if len(subs) == 0 {
		h.sendLastRegText(ctx, chatID, messageID, "📭 Нет активных подписок", false)
		return nil
	}

	var sb strings.Builder
	sb.WriteString("📋 *Последние регистрации*\n\n")

	for _, sub := range subs {
		username := formatUserLink(sub.Username, sub.TelegramID)
		dateStr := sub.CreatedAt.Format("02.01.06")
		fmt.Fprintf(&sb, "%d │ %s │ %s\n", sub.ID, username, dateStr)
	}

	h.sendLastRegText(ctx, chatID, messageID, sb.String(), true)
	return nil
}

// sendLastRegText sends or edits the lastreg result message.
// A zero messageID means there's no inline keyboard to update (slash command case),
// so a new message is sent; otherwise the button message is edited.
func (h *Handler) sendLastRegText(ctx context.Context, chatID int64, messageID int, text string, isMarkdown bool) {
	if messageID == 0 {
		h.sendLastRegNewMessage(ctx, chatID, text, isMarkdown)
		return
	}
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	if isMarkdown {
		editMsg.ParseMode = "Markdown"
	}
	h.safeSend(editMsg)
}

func (h *Handler) sendLastRegNewMessage(ctx context.Context, chatID int64, text string, isMarkdown bool) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	msg.ReplyMarkup = &keyboard
	if isMarkdown {
		msg.ParseMode = "Markdown"
	}
	h.send(ctx, msg)
}

// HandleDel handles the /del command for admins.
// Deletes a subscription by database ID from both 3x-ui panel and database.
// Usage: /del <id>
func (h *Handler) HandleDel(ctx context.Context, update tgbotapi.Update) error {
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

	if update.Message == nil {
		logger.Error("HandleDel called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /del", zap.Int64("chat_id", chatID))
		return nil
	}

	// Parse the command arguments
	args := update.Message.CommandArguments()
	if args == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /del <id>\n\nПример: /del 5")
		return nil
	}

	// Parse the ID - use int64 to properly detect negative numbers
	var parsedID int64
	var err error
	if parsedID, err = strconv.ParseInt(strings.TrimSpace(args), 10, 64); err != nil {
		h.SendMessage(ctx, chatID, "❌ Неверный формат ID. Использование: /del <id>\n\nПример: /del 5")
		return nil
	}

	// Validate ID is positive
	if parsedID <= 0 {
		h.SendMessage(ctx, chatID, "❌ ID должен быть положительным числом")
		return nil
	}

	id := uint(parsedID)

	// Delete subscription via service.
	// DeleteByID returns the deleted record so we can use it for
	// referral/cache updates only after a successful deletion.
	deleted, err := h.subscriptionService.DeleteByID(ctx, id)
	if err != nil {
		logger.Error("Failed to delete subscription",
			zap.Error(err),
			zap.Uint("id", id))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка удаления подписки: %v", err))
		return fmt.Errorf("delete subscription: %w", err)
	}

	// Decrement referral cache only after successful deletion
	if deleted.ReferredBy != nil && *deleted.ReferredBy > 0 {
		h.DecrementReferralCount(*deleted.ReferredBy)
	}

	// Invalidate cache only after successful deletion
	if deleted.TelegramID > 0 {
		h.invalidateCache(ctx, deleted.TelegramID)
	}

	// Success
	logger.Info("Subscription deleted",
		zap.Uint("id", id),
		zap.String("username", deleted.Username),
		zap.Int64("telegram_id", deleted.TelegramID),
		zap.String("client_id", deleted.ClientID))

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Подписка успешно удалена!\n\n"+
			"🆔 ID: %d\n"+
			"👤 Пользователь: %s\n"+
			"🆔 Telegram ID: %d",
		id,
		formatUserDisplay(deleted.Username),
		deleted.TelegramID,
	))
	return nil
}

// HandleBroadcast handles the /broadcast command for admins to send messages to all users.
func (h *Handler) HandleBroadcast(ctx context.Context, update tgbotapi.Update) error {
	const broadcastTimeout = 5 * time.Minute
	ctx, cancel := context.WithTimeout(ctx, broadcastTimeout)
	defer cancel()

	if update.Message == nil {
		logger.Error("HandleBroadcast called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID

	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /broadcast", zap.Int64("chat_id", chatID))
		return nil
	}

	message := update.Message.CommandArguments()
	if message == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /broadcast <сообщение>\n\nПример: /broadcast Привет всем!")
		return nil
	}
	if len(message) > config.MaxTelegramMessageLen {
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Сообщение слишком длинное (%d символов).\n\nМаксимум: %d символов.", len(message), config.MaxTelegramMessageLen))
		return nil
	}

	const (
		batchSize            = 100
		broadcastConcurrency = 10 // max concurrent sends per batch
	)

	var (
		successCount       int64
		failCount          int64
		totalProcessed     int64
		batchErr           error
		broadcastCancelled bool
	)
	offset := 0
	for {
		select {
		case <-ctx.Done():
			broadcastCancelled = true
		default:
		}
		if broadcastCancelled {
			break
		}

		ids, err := h.db.GetTelegramIDsBatch(ctx, offset, batchSize)
		if err != nil {
			logger.Error("Failed to get telegram IDs batch", zap.Error(err))
			batchErr = err
			break
		}
		if len(ids) == 0 {
			break
		}

		var wg sync.WaitGroup
		sem := make(chan struct{}, broadcastConcurrency)

		for _, telegramID := range ids {
			if broadcastCancelled {
				break
			}
			select {
			case sem <- struct{}{}:
				wg.Add(1)
				go func(tg int64) {
					defer logger.Recover("Broadcast worker")
					defer wg.Done()
					defer func() {
						time.Sleep(50 * time.Millisecond)
						<-sem
					}()

					select {
					case <-ctx.Done():
						return
					default:
					}

					escapedMessage := utils.EscapeMarkdown(message)
					msg := tgbotapi.NewMessage(tg, escapedMessage)
					msg.ParseMode = "MarkdownV2"
					msg.DisableWebPagePreview = true
					if err := h.sendWithError(ctx, msg); err != nil {
						if ctx.Err() == nil {
							atomic.AddInt64(&failCount, 1)
						}
					} else {
						atomic.AddInt64(&successCount, 1)
					}
				}(telegramID)
			case <-ctx.Done():
				broadcastCancelled = true
			}
		}

		wg.Wait()
		if broadcastCancelled {
			break
		}
		offset += len(ids)
		atomic.AddInt64(&totalProcessed, int64(len(ids)))
	}

	sent := atomic.LoadInt64(&successCount)
	failed := atomic.LoadInt64(&failCount)
	remaining := int(totalProcessed) - int(sent+failed)

	if broadcastCancelled {
		h.SendMessage(context.WithoutCancel(ctx), chatID, fmt.Sprintf(`⚠️ Рассылка прервана!

📤 Отправлено: %d
❌ Ошибок: %d
👥 Осталось: %d`,
			sent, failed, remaining))
		return fmt.Errorf("broadcast cancelled")
	}
	if batchErr != nil {
		h.SendMessage(context.WithoutCancel(ctx), chatID, fmt.Sprintf(`❌ Рассылка прервана из-за ошибки!

📤 Отправлено: %d
❌ Ошибок: %d
👥 Не обработано: %d

Ошибка: %v`,
			sent, failed, remaining, batchErr,
		))
		logger.Error("Broadcast failed due to batch retrieval error",
			zap.Error(batchErr),
			zap.Int64("success", sent),
			zap.Int64("failed", failed),
			zap.Int("remaining", remaining))
		return fmt.Errorf("broadcast batch error: %w", batchErr)
	}

	h.SendMessage(context.WithoutCancel(ctx), chatID, fmt.Sprintf(`✅ Рассылка завершена!

📤 Отправлено: %d
❌ Ошибок: %d
👥 Всего: %d`,
		sent, failed, totalProcessed,
	))
	logger.Info("Broadcast completed",
		zap.Int64("success", sent),
		zap.Int64("failed", failed),
		zap.Int64("total", totalProcessed))
	return nil
}

// HandleSend handles the /send command for admins to send a message to a specific user.
func (h *Handler) HandleSend(ctx context.Context, update tgbotapi.Update) error {
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

	if update.Message == nil {
		logger.Error("HandleSend called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /send", zap.Int64("chat_id", chatID))
		return nil
	}

	// Rate limiting check
	if !h.checkAdminSendRateLimit(chatID) {
		h.SendMessage(ctx, chatID, "⚠️ Слишком много сообщений. Подождите минуту.")
		return nil
	}

	// Parse the command arguments
	args := update.Message.CommandArguments()
	if args == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /send <telegram_id|username> <сообщение>\n\nПримеры:\n/send 123456789 Привет!\n/send @username Привет!")
		return nil
	}

	// Split args into target and message
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 2 {
		h.SendMessage(ctx, chatID, "❌ Использование: /send <telegram_id|username> <сообщение>\n\nПримеры:\n/send 123456789 Привет!\n/send @username Привет!")
		return nil
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
			return fmt.Errorf("get telegram id by username: %w", err)
		}
	}

	// Send the message
	escapedMessage := utils.EscapeMarkdown(message)
	msg := tgbotapi.NewMessage(telegramID, escapedMessage)
	msg.ParseMode = "MarkdownV2"
	msg.DisableWebPagePreview = true
	sentMsg, err := h.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send admin message",
			zap.Int64("telegram_id", telegramID),
			zap.Error(err))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка отправки сообщения: %v", err))
		return fmt.Errorf("send admin message: %w", err)
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Сообщение отправлено!\n\n👤 Получатель: %d\n💬 ID сообщения: %d",
		telegramID,
		sentMsg.MessageID,
	))

	logger.Info("Message sent via /send command",
		zap.Int64("telegram_id", telegramID),
		zap.Int64("admin_id", chatID))
	return nil
}

// handleAdminStats handles the "admin stats" callback.
func (h *Handler) handleAdminStats(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("Admin requesting stats", zap.String("username", username))

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access admin stats", zap.Int64("chat_id", chatID))
		return nil
	}

	// Get counts efficiently using SQL COUNT queries
	totalCount, err := h.db.CountAllSubscriptions(ctx)
	if err != nil {
		logger.Error("Failed to count subscriptions for stats", zap.Error(err))
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка получения статистики")
		editMsg.DisableWebPagePreview = true
		keyboard := h.getBackKeyboard()
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
		return fmt.Errorf("count all subscriptions: %w", err)
	}

	activeCount, err := h.db.CountActiveSubscriptions(ctx)
	if err != nil {
		logger.Error("Failed to count active subscriptions", zap.Error(err))
		activeCount = 0
		// Continue with partial stats; not a fatal error
	}

	text := fmt.Sprintf(
		"📊 *Статистика бота*\n\n👥 Всего пользователей: %d\n✅ Активные подписки: %d",
		totalCount,
		activeCount,
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	keyboard := h.getBackKeyboard()
	editMsg.ReplyMarkup = &keyboard
	h.safeSend(editMsg)
	return nil
}

// notifyAdmin sends a notification to the admin about a new subscription.
func (h *Handler) notifyAdmin(ctx context.Context, username string, chatID int64, subscriptionURL string) error {
	if h.cfg.TelegramAdminID == 0 {
		return nil
	}

	msg := tgbotapi.NewMessage(h.cfg.TelegramAdminID,
		fmt.Sprintf("🔔 Новая подписка создана!\n\n👤 Пользователь: %s\n🆔 ID: %d\n🔗 Подписка: `%s`",
			formatUserLink(username, chatID),
			chatID,
			subscriptionURL,
		))
	msg.ParseMode = "Markdown"

	err := h.sendWithError(ctx, msg)
	if err != nil {
		logger.Warn("Failed to notify admin", zap.String("username", username), zap.Error(err))
		return fmt.Errorf("notify admin: %w", err)
	}

	logger.Info("Admin notified about new subscription", zap.String("username", username))
	return nil
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

// HandleRefstats handles the /refstats command to show referral statistics.
func (h *Handler) HandleRefstats(ctx context.Context, update tgbotapi.Update) error {
	if update.Message == nil {
		logger.Error("HandleRefstats called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID
	username := "unknown"
	if update.Message.From != nil && update.Message.From.UserName != "" {
		username = update.Message.From.UserName
	}

	if !h.isAdmin(chatID) {
		h.SendMessage(ctx, chatID, "❌ Эта команда доступна только администратору")
		return nil
	}

	logger.Info("Admin requesting referral stats", zap.String("username", username))

	allCounts := h.referralCache.GetAll()
	type referrer struct {
		chatID int64
		count  int64
	}
	referrals := make([]referrer, 0, len(allCounts))

	for chatID, count := range allCounts {
		referrals = append(referrals, referrer{chatID: chatID, count: count})
	}

	// Sort by count (descending)
	sort.Slice(referrals, func(i, j int) bool {
		return referrals[i].count > referrals[j].count
	})

	// Calculate totals
	var totalReferrals int64
	for _, r := range referrals {
		totalReferrals += r.count
	}

	// Format message
	var sb strings.Builder
	sb.WriteString("📊 *Статистика рефералов*\n\n")
	sb.WriteString(fmt.Sprintf("👥 Всего рефералов: %d\n", totalReferrals))
	sb.WriteString(fmt.Sprintf("👤 Уникальных рефереров: %d\n\n", len(referrals)))

	if len(referrals) > 0 {
		sb.WriteString("🏆 *Топ-10 рефереров:*\n")
		limit := 10
		if len(referrals) < limit {
			limit = len(referrals)
		}
		for i := 0; i < limit; i++ {
			r := referrals[i]
			sb.WriteString(fmt.Sprintf("%d\\. ID %d: %d рефералов\n", i+1, r.chatID, r.count))
		}
	} else {
		sb.WriteString("📭 Нет данных о рефералах")
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "MarkdownV2"
	h.send(ctx, msg)
	return nil
}
