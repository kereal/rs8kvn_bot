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

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

func (h *Handler) HandleVersion(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleVersion called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID
	if !h.isAdmin(chatID) {
		return
	}

	logger.Info("Admin requesting version", zap.Int64("chat_id", chatID))
	h.SendMessage(ctx, chatID, h.version)
}

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
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

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
	var err error
	if parsedID, err = strconv.ParseInt(strings.TrimSpace(args), 10, 64); err != nil {
		h.SendMessage(ctx, chatID, "❌ Неверный формат ID. Использование: /del <id>\n\nПример: /del 5")
		return
	}

	// Validate ID is positive
	if parsedID <= 0 {
		h.SendMessage(ctx, chatID, "❌ ID должен быть положительным числом")
		return
	}

	id := uint(parsedID)

	// Delete subscription via service (includes webhook notification).
	// DeleteByID returns the deleted record so we can use it for
	// referral/cache updates only after a successful deletion.
	deleted, err := h.subscriptionService.DeleteByID(ctx, id)
	if err != nil {
		logger.Error("Failed to delete subscription",
			zap.Error(err),
			zap.Uint("id", id))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка удаления подписки: %v", err))
		return
	}

	// Decrement referral cache only after successful deletion
	if deleted.ReferredBy > 0 {
		h.DecrementReferralCount(deleted.ReferredBy)
	}

	// Invalidate cache only after successful deletion
	if deleted.TelegramID != 0 {
		h.invalidateCache(deleted.TelegramID)
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
			"👤 Пользователь: @%s\n"+
			"🆔 Telegram ID: %d",
		id,
		deleted.Username,
		deleted.TelegramID,
	))
}

// escapeMarkdown escapes special characters in Markdown V2 to prevent injection.
// Backslash MUST be first — escaping it before other chars prevents double-escaping
// escapeMarkdown returns text with Telegram Markdown V2 special characters escaped by prefixing each with a backslash.
// It escapes the backslash character first to prevent incorrect double-escaping (for example, input "\*" becomes "\\\*").
// The characters escaped include: \ _ * [ ] ( ) ~ ` > # + - = | { } . !
func escapeMarkdown(text string) string {
	// Characters that need to be escaped in Markdown V2: \ _ * [ ] ( ) ~ ` > # + - = | { } . !
	specialChars := []string{"\\", "_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	result := text
	for _, char := range specialChars {
		result = strings.ReplaceAll(result, char, "\\"+char)
	}
	return result
}

// HandleBroadcast handles the /broadcast command for admins to send messages to all users.
func (h *Handler) HandleBroadcast(ctx context.Context, update tgbotapi.Update) {
	const broadcastTimeout = 5 * time.Minute
	ctx, cancel := context.WithTimeout(ctx, broadcastTimeout)
	defer cancel()

	if update.Message == nil {
		logger.Error("HandleBroadcast called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /broadcast", zap.Int64("chat_id", chatID))
		return
	}

	message := update.Message.CommandArguments()
	if message == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /broadcast <сообщение>\n\nПример: /broadcast Привет всем!")
		return
	}
	if len(message) > config.MaxTelegramMessageLen {
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Сообщение слишком длинное (%d символов).\n\nМаксимум: %d символов.", len(message), config.MaxTelegramMessageLen))
		return
	}

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

	const (
		batchSize            = 100
		broadcastConcurrency = 10 // max concurrent sends per batch
	)

	var (
		successCount int64 = 0
		failCount    int64 = 0
		batchErr     error
		cancelled    bool
	)
	offset := 0
forLoop:
	for offset < int(totalCount) {
		// Check cancellation before fetching next batch
		select {
		case <-ctx.Done():
			cancelled = true
			break forLoop
		default:
		}

		ids, err := h.db.GetTelegramIDsBatch(ctx, offset, batchSize)
		if err != nil {
			logger.Error("Failed to get telegram IDs batch", zap.Error(err))
			batchErr = err
			break forLoop
		}

		// Process batch with bounded concurrency
		var wg sync.WaitGroup
		sem := make(chan struct{}, broadcastConcurrency)

		for _, telegramID := range ids {
			// Attempt to acquire semaphore with cancellation support
			select {
			case sem <- struct{}{}: // acquired
				wg.Add(1)
				go func(tg int64) {
					defer wg.Done()
					defer func() { <-sem }() // release

					// Check context inside goroutine before sending
					select {
					case <-ctx.Done():
						return
					default:
					}

					escapedMessage := escapeMarkdown(message)
					msg := tgbotapi.NewMessage(tg, escapedMessage)
					msg.ParseMode = "MarkdownV2"
					msg.DisableWebPagePreview = true
					if err := h.sendWithError(ctx, msg); err != nil {
						atomic.AddInt64(&failCount, 1)
					} else {
						atomic.AddInt64(&successCount, 1)
					}
					time.Sleep(50 * time.Millisecond)
				}(telegramID)
			case <-ctx.Done():
				// Cancelled while waiting for semaphore; stop launching new tasks
				cancelled = true
				break forLoop
			}
		}

		wg.Wait() // wait for batch to complete
		offset += batchSize
	}

	// If we exited the loop due to cancellation or batch error, handle accordingly
	if cancelled {
		h.SendMessage(ctx, chatID, fmt.Sprintf(`⚠️ Рассылка прервана!

📤 Отправлено: %d
❌ Ошибок: %d
👥 Осталось: %d`,
			atomic.LoadInt64(&successCount),
			atomic.LoadInt64(&failCount),
			int(totalCount)-int(atomic.LoadInt64(&successCount)+atomic.LoadInt64(&failCount))))
		return
	}
	if batchErr != nil {
		h.SendMessage(ctx, chatID, fmt.Sprintf(`❌ Рассылка прервана из-за ошибки!

📤 Отправлено: %d
❌ Ошибок отправки: %d
👥 Всего пользователей: %d

Ошибка: %v`,
			atomic.LoadInt64(&successCount),
			atomic.LoadInt64(&failCount),
			totalCount,
			batchErr,
		))
		logger.Error("Broadcast failed due to batch retrieval error",
			zap.Error(batchErr),
			zap.Int64("success", atomic.LoadInt64(&successCount)),
			zap.Int64("failed", atomic.LoadInt64(&failCount)),
			zap.Int64("total", totalCount))
		return
	}

	if batchErr != nil {
		// Batch retrieval failed — report partial/failure with error details
		h.SendMessage(ctx, chatID, fmt.Sprintf(`❌ Рассылка прервана из-за ошибки!

📤 Отправлено: %d
❌ Ошибок отправки: %d
👥 Всего пользователей: %d

Ошибка: %v`,
			atomic.LoadInt64(&successCount),
			atomic.LoadInt64(&failCount),
			totalCount,
			batchErr,
		))
		logger.Error("Broadcast failed due to batch retrieval error",
			zap.Error(batchErr),
			zap.Int64("success", atomic.LoadInt64(&successCount)),
			zap.Int64("failed", atomic.LoadInt64(&failCount)),
			zap.Int64("total", totalCount))
		return
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf(`✅ Рассылка завершена!

📤 Отправлено: %d
❌ Ошибок: %d
👥 Всего пользователей: %d`,
		atomic.LoadInt64(&successCount),
		atomic.LoadInt64(&failCount),
		totalCount,
	))
	logger.Info("Broadcast completed",
		zap.Int64("success", atomic.LoadInt64(&successCount)),
		zap.Int64("failed", atomic.LoadInt64(&failCount)),
		zap.Int64("total", totalCount))
}

// HandleSend handles the /send command for admins to send a message to a specific user.
func (h *Handler) HandleSend(ctx context.Context, update tgbotapi.Update) {
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

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

	// Rate limiting check
	if !h.checkAdminSendRateLimit(chatID) {
		h.SendMessage(ctx, chatID, "⚠️ Слишком много сообщений. Подождите минуту.")
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
	// Escape markdown to prevent injection
	escapedMessage := escapeMarkdown(message)
	msg := tgbotapi.NewMessage(telegramID, escapedMessage)
	msg.ParseMode = "MarkdownV2"
	msg.DisableWebPagePreview = true
	sentMsg, err := h.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send admin message",
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
	totalCount, err := h.db.CountAllSubscriptions(ctx)
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
}

// notifyAdmin sends a notification to the admin about a new subscription.
func (h *Handler) notifyAdmin(ctx context.Context, username string, chatID int64, subscriptionURL string, expiryTime time.Time) error {
	if h.cfg.TelegramAdminID == 0 {
		return nil
	}

	msg := tgbotapi.NewMessage(h.cfg.TelegramAdminID,
		fmt.Sprintf("🔔 Новая подписка создана!\n\n👤 Пользователь: @%s\n🆔 ID: %d\n🔗 Подписка: `%s`\n⏰ Истекает: %s",
			username,
			chatID,
			subscriptionURL,
			expiryTime.Format("02.01.2006 15:04:05"),
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
func (h *Handler) HandleRefstats(ctx context.Context, update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	username := "unknown"
	if update.Message.From != nil {
		if update.Message.From.UserName != "" {
			username = update.Message.From.UserName
		}
	}

	if !h.isAdmin(chatID) {
		h.SendMessage(ctx, chatID, "❌ Эта команда доступна только администратору")
		return
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
}
