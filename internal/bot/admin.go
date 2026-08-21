package bot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// broadcastStage represents the state of an admin broadcast session.
type broadcastStage int

const (
	broadcastStageIdle broadcastStage = iota
	broadcastStageAwaitingName
	broadcastStageAwaitingDraft
	broadcastStagePreview
)

const (
	broadcastSessionTTL = 15 * time.Minute
	// broadcastNameMaxLen — максимальная длина названия рассылки.
	broadcastNameMaxLen = 100
	// broadcastRetries — число повторов доставки при временных (не blocked) ошибках.
	broadcastRetries = 2
	// broadcastRetryBaseDelay — базовая пауза между повторами (растёт линейно).
	broadcastRetryBaseDelay = 300 * time.Millisecond
	// broadcastErrorTextMaxLen — максимальная длина текста ошибки в отчёте.
	broadcastErrorTextMaxLen = 500
	// broadcastTextPreviewMaxRunes — максимальная длина текста в карточке рассылки.
	broadcastTextPreviewMaxRunes = 500
)

// broadcastSession holds the in-progress broadcast draft for an admin.
type broadcastSession struct {
	createdAt time.Time
	stage     broadcastStage
	name      string
	text      string
}

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
	logger.Info("Admin requesting last registrations", zap.String("username", username), zap.Int64("chat_id", chatID))

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
		username := utils.FormatUserLink(sub.Username, sub.TelegramID)
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
	var (
		parsedID int64
		err      error
	)

	parsedID, err = strconv.ParseInt(strings.TrimSpace(args), 10, 64)
	if err != nil {
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

// HandleSetPlan handles the /setplan command for admins.
// Changes the subscription plan through the service layer: subscription row is
// updated (plan, status, expiry), node bindings are reconciled
// (pending_add/pending_update/pending_remove), and VPN panels are synced
// best-effort. Usage: /setplan <subscription_id> <plan_id> [days]
func (h *Handler) HandleSetPlan(ctx context.Context, update tgbotapi.Update) error {
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

	if update.Message == nil {
		logger.Error("HandleSetPlan called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /setplan", zap.Int64("chat_id", chatID))
		return nil
	}

	// Parse the command arguments
	fields := strings.Fields(update.Message.CommandArguments())
	if len(fields) < 2 {
		h.SendMessage(ctx, chatID, "❌ Использование: /setplan <id_подписки> <id_тарифа> [дней]\n\nПример: /setplan 5 3 30")
		return nil
	}

	subID, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || subID == 0 {
		h.SendMessage(ctx, chatID, "❌ Неверный ID подписки. Использование: /setplan <id_подписки> <id_тарифа> [дней]")
		return nil
	}

	planID, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || planID == 0 {
		h.SendMessage(ctx, chatID, "❌ Неверный ID тарифа. Использование: /setplan <id_подписки> <id_тарифа> [дней]")
		return nil
	}

	var days int
	if len(fields) >= 3 {
		days, err = strconv.Atoi(fields[2])
		if err != nil || days <= 0 {
			h.SendMessage(ctx, chatID, "❌ Количество дней должно быть положительным числом")
			return nil
		}

		if days > service.AdminSetPlanMaxDays {
			h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Количество дней не может превышать %d", service.AdminSetPlanMaxDays))
			return nil
		}
	}

	updated, err := h.subscriptionService.AdminSetPlan(ctx, uint(subID), uint(planID), days)
	if err != nil {
		logger.Error("Failed to set subscription plan",
			zap.Error(err),
			zap.Uint("subscription_id", uint(subID)),
			zap.Uint("plan_id", uint(planID)))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка смены тарифа: %v", err))

		return fmt.Errorf("set plan: %w", err)
	}

	logger.Info("Subscription plan changed",
		zap.Uint("subscription_id", updated.ID),
		zap.Uint("plan_id", updated.PlanID),
		zap.String("username", updated.Username),
		zap.Int64("telegram_id", updated.TelegramID))

	expiry := "без срока (free)"
	if updated.ExpiresAt != nil {
		expiry = updated.ExpiresAt.Format("02.01.2006")
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ Тариф подписки изменён!\n\n"+
			"🆔 ID: %d\n"+
			"👤 Пользователь: %s\n"+
			"🆔 Telegram ID: %d\n"+
			"💡 Тариф: %d\n"+
			"⏰ Истекает: %s",
		updated.ID,
		formatUserDisplay(updated.Username),
		updated.TelegramID,
		updated.PlanID,
		expiry,
	))

	return nil
}

// isUserBlockedError reports whether the Telegram error means the user can no
// longer receive messages (blocked the bot, deactivated, or chat gone). These
// are expected during a broadcast and reported separately from real failures.
func isUserBlockedError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "bot was blocked by the user") ||
		strings.Contains(msg, "user is deactivated") ||
		strings.Contains(msg, "chat not found")
}

// HandleBroadcast handles the /broadcast command for admins.
// It starts the broadcast flow: the admin first sends a broadcast NAME, then a
// multi-line MarkdownV2 message which is previewed, and confirmed via inline
// buttons before sending. The confirmed broadcast is recorded in the broadcasts
// table with delivery statistics.
func (h *Handler) HandleBroadcast(ctx context.Context, update tgbotapi.Update) error {
	if update.Message == nil {
		logger.Error("HandleBroadcast called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID

	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /broadcast", zap.Int64("chat_id", chatID))
		return nil
	}

	h.startBroadcastSession(chatID)

	h.SendMessage(ctx, chatID, "📦 Отправьте *название* рассылки (для статистики, до 100 символов).\n\n"+
		"Нажмите /cancel для отмены.")

	return nil
}

// HandleBroadcastDraft consumes the admin's text messages: first the broadcast
// name, then the broadcast draft (previewed with MarkdownV2 and confirmed via
// inline buttons).
func (h *Handler) HandleBroadcastDraft(ctx context.Context, update tgbotapi.Update) error {
	if update.Message == nil {
		logger.Error("HandleBroadcastDraft called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID

	if !h.isAdmin(chatID) {
		h.clearBroadcastSession(chatID)
		return nil
	}

	text := update.Message.Text
	if text == "" {
		h.SendMessage(ctx, chatID, "❌ Поддерживаются только текстовые сообщения. /cancel для отмены.")
		return nil
	}

	if text == "/cancel" {
		h.clearBroadcastSession(chatID)
		h.SendMessage(ctx, chatID, "❌ Рассылка отменена.")

		return nil
	}

	s := h.getBroadcastSession(chatID)
	if s == nil {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки. Начните с /broadcast")
		return nil
	}

	switch s.stage {
	case broadcastStageAwaitingName:
		return h.handleBroadcastName(ctx, chatID, text)
	case broadcastStageAwaitingDraft:
		return h.handleBroadcastDraftText(ctx, chatID, text)
	default:
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки. Начните с /broadcast")
		return nil
	}
}

// handleBroadcastName consumes the broadcast name and moves the session to the
// draft stage.
func (h *Handler) handleBroadcastName(ctx context.Context, chatID int64, raw string) error {
	name := strings.TrimSpace(raw)
	if name == "" {
		h.SendMessage(ctx, chatID, "❌ Название не может быть пустым. /cancel для отмены.")
		return nil
	}

	if len([]rune(name)) > broadcastNameMaxLen {
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Название слишком длинное (до %d символов). /cancel для отмены.", broadcastNameMaxLen))
		return nil
	}

	h.broadcastMu.Lock()
	h.broadcastSessions[chatID] = &broadcastSession{createdAt: time.Now(), stage: broadcastStageAwaitingDraft, name: name}
	h.broadcastMu.Unlock()

	h.SendMessage(ctx, chatID, "✅ Название принято.\n\n✍️ Теперь отправьте текст сообщения (MarkdownV2, до 4096 символов на часть, с форматированием).\n\n"+
		"Многострочный текст поддерживается. После отправки бот покажет превью и кнопки подтверждения.\n\n"+
		"Нажмите /cancel для отмены.")

	return nil
}

// handleBroadcastDraftText consumes the broadcast text, previews it (validating
// MarkdownV2), and offers confirm/cancel buttons.
func (h *Handler) handleBroadcastDraftText(ctx context.Context, chatID int64, text string) error {
	const maxBroadcastLen = config.MaxTelegramMessageLen * 20
	if len(text) > maxBroadcastLen {
		h.clearBroadcastSession(chatID)
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Сообщение слишком длинное (%d символов). Максимум — %d символов; рассылка автоматически разбивается на части по %d символов.", len(text), maxBroadcastLen, config.MaxTelegramMessageLen))

		return nil
	}

	name := ""
	if s := h.getBroadcastSession(chatID); s != nil {
		name = s.name
	}

	// D3: preview with MarkdownV2. The draft may exceed one Telegram message,
	// so show the first chunk and note how many parts the broadcast will use.
	chunks := splitMessage(text, config.MaxTelegramMessageLen)

	previewText := chunks[0]
	if len(chunks) > 1 {
		previewText += fmt.Sprintf("\n\n… (и ещё %d частей по %d символов)", len(chunks)-1, config.MaxTelegramMessageLen)
	}

	preview := tgbotapi.NewMessage(chatID, utils.EscapeMarkdownV2(previewText))
	preview.ParseMode = "MarkdownV2"

	preview.DisableWebPagePreview = true

	_, err := h.bot.Send(preview)
	if err != nil {
		logger.Warn("Broadcast preview failed", zap.Error(err))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Не удалось отправить превью:\n\n%v\n\n"+
			"/cancel для отмены.", err))

		return nil
	}

	h.broadcastMu.Lock()
	h.broadcastSessions[chatID] = &broadcastSession{createdAt: time.Now(), stage: broadcastStagePreview, name: name, text: text}
	h.broadcastMu.Unlock()

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Отправить всем", "broadcast_confirm"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "broadcast_cancel"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, utils.EscapeMarkdownV2(fmt.Sprintf("✅ Превью готово. Рассылка «%s».\n\nОтправить это сообщение всем пользователям?", name)))
	msg.ParseMode = "MarkdownV2"
	msg.ReplyMarkup = kb
	h.send(ctx, msg)

	return nil
}

// handleBroadcastConfirm creates the broadcast record (DB-setup phase: a failure
// aborts the broadcast — the broadcast must be recorded before any message is
// sent) and runs the broadcast.
func (h *Handler) handleBroadcastConfirm(ctx context.Context, chatID int64) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || s.stage != broadcastStagePreview {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки для подтверждения.")
		return nil
	}

	name, text := s.name, s.text

	h.clearBroadcastSession(chatID)

	now := time.Now().UTC()
	broadcast := &database.Broadcast{
		Name:        name,
		Filters:     "{}",
		MessageText: text,
		Status:      string(database.BroadcastStatusRunning),
		PlannedAt:   &now,
		StartedAt:   &now,
	}
	err := h.db.CreateBroadcast(ctx, broadcast)
	if err != nil {
		logger.Error("Failed to create broadcast", zap.Error(err))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Не удалось сохранить рассылку:\n\n%v", err))

		return fmt.Errorf("create broadcast: %w", err)
	}

	return h.runBroadcast(ctx, chatID, name, text, broadcast.ID)
}

// handleBroadcastCancel discards the in-progress broadcast draft.
func (h *Handler) handleBroadcastCancel(ctx context.Context, chatID int64) error {
	h.clearBroadcastSession(chatID)
	h.SendMessage(ctx, chatID, "❌ Рассылка отменена.")

	return nil
}

// runBroadcast sends text (MarkdownV2, as-is) to all users in batches, retrying
// transient (non-blocked) failures, and records the outcome in the broadcast row
// (counters + delivery_report JSON).
func (h *Handler) runBroadcast(ctx context.Context, adminChatID int64, name, text string, broadcastID uint) error {
	const broadcastTimeout = 5 * time.Minute

	ctx, cancel := context.WithTimeout(ctx, broadcastTimeout)
	defer cancel()

	const (
		batchSize            = 100
		broadcastConcurrency = 10 // max concurrent sends per batch
	)

	var (
		successCount       atomic.Int64
		failCount          atomic.Int64
		blockedCount       atomic.Int64
		totalProcessed     int64
		batchErr           error
		broadcastCancelled bool
		report             = database.BroadcastDeliveryReport{Delivered: []int64{}, Blocked: []int64{}, Errors: []database.BroadcastSendError{}}
		reportMu           sync.Mutex
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

					chunks := splitMessage(text, config.MaxTelegramMessageLen)
					userBlocked, userFailed := false, false
					var lastErr error

					for _, chunk := range chunks {
						msg := tgbotapi.NewMessage(tg, utils.EscapeMarkdownV2(chunk))
						msg.ParseMode = "MarkdownV2"

						msg.DisableWebPagePreview = true

						// Retry transient failures up to broadcastRetries times with
						// linear backoff. Blocked-type errors are permanent and never
						// retried; ctx cancellation aborts immediately.
						for attempt := 0; ; attempt++ {
							err := h.sendWithError(ctx, msg)
							if err == nil {
								break
							}
							if ctx.Err() != nil {
								return
							}
							if isUserBlockedError(err) {
								userBlocked = true
								break
							}
							lastErr = err
							if attempt < broadcastRetries {
								select {
								case <-time.After(broadcastRetryBaseDelay * time.Duration(attempt+1)):
								case <-ctx.Done():
									return
								}
								continue
							}
							userFailed = true
							break
						}

					if userBlocked || userFailed || ctx.Err() != nil {
						break
					}
					}

					if ctx.Err() != nil {
						return
					}

					reportMu.Lock()
					switch {
					case userBlocked:
						blockedCount.Add(1)
						report.Blocked = append(report.Blocked, tg)
					case userFailed:
						failCount.Add(1)
						report.Errors = append(report.Errors, database.BroadcastSendError{TelegramID: tg, Error: truncateRunes(lastErr.Error(), broadcastErrorTextMaxLen)})
					default:
						successCount.Add(1)
						report.Delivered = append(report.Delivered, tg)
					}
					reportMu.Unlock()
				}(telegramID)
			case <-ctx.Done():
				broadcastCancelled = true
			}
		}

		wg.Wait()

		offset += len(ids)
		atomic.AddInt64(&totalProcessed, int64(len(ids)))

		if broadcastCancelled {
			break
		}
	}

	sent := successCount.Load()
	failed := failCount.Load()
	blocked := blockedCount.Load()
	remaining := int(totalProcessed) - int(sent+failed+blocked)
	finishedAt := time.Now().UTC()

	// Используем context.WithoutCancel, чтобы финализация (обновление БД и
	// отправка отчёта админу) работала даже при отменённом/таймаутном контексте.
	bgCtx := context.WithoutCancel(ctx)

	if broadcastCancelled {
		h.finalizeBroadcast(bgCtx, broadcastID, string(database.BroadcastStatusCanceled), &report, finishedAt, totalProcessed, sent, blocked, failed)
		h.sendBroadcastReport(bgCtx, adminChatID, fmt.Sprintf(`⚠️ Рассылка прервана!

📦 Рассылка #%d: %s

📤 Отправлено: %d
🚫 Заблокировали бота: %d
❌ Ошибок: %d
👥 Осталось: %d`,
			broadcastID, name, sent, blocked, failed, remaining), broadcastID)

		return fmt.Errorf("broadcast cancelled: %w", ctx.Err())
	}

	if batchErr != nil {
		h.finalizeBroadcast(bgCtx, broadcastID, string(database.BroadcastStatusFailed), &report, finishedAt, totalProcessed, sent, blocked, failed)
		h.sendBroadcastReport(bgCtx, adminChatID, fmt.Sprintf(`❌ Рассылка прервана из-за ошибки!

📦 Рассылка #%d: %s

📤 Отправлено: %d
🚫 Заблокировали бота: %d
❌ Ошибок: %d
👥 Не обработано: %d

Ошибка: %v`,
			broadcastID, name, sent, blocked, failed, remaining, batchErr), broadcastID)
		logger.Error("Broadcast failed due to batch retrieval error",
			zap.Error(batchErr),
			zap.Uint("broadcast_id", broadcastID),
			zap.Int64("success", sent),
			zap.Int64("blocked", blocked),
			zap.Int64("failed", failed),
			zap.Int("remaining", remaining))

		return fmt.Errorf("broadcast batch error: %w", batchErr)
	}

	h.finalizeBroadcast(bgCtx, broadcastID, string(database.BroadcastStatusCompleted), &report, finishedAt, totalProcessed, sent, blocked, failed)
	h.sendBroadcastReport(bgCtx, adminChatID, fmt.Sprintf(`✅ Рассылка завершена!

📦 Рассылка #%d: %s

📤 Отправлено: %d
🚫 Заблокировали бота: %d
❌ Ошибок: %d
👥 Всего: %d`,
		broadcastID, name, sent, blocked, failed, totalProcessed), broadcastID)
	logger.Info("Broadcast completed",
		zap.Uint("broadcast_id", broadcastID),
		zap.Int64("success", sent),
		zap.Int64("blocked", blocked),
		zap.Int64("failed", failed),
		zap.Int64("total", totalProcessed))

	return nil
}

// finalizeBroadcast сохраняет итоги рассылки в строку рассылки: статус, время
// завершения, счётчики и JSON-отчёт. Ошибка обновления логируется — отчёт
// администратору отправляется в любом случае.
func (h *Handler) finalizeBroadcast(ctx context.Context, broadcastID uint, status string, report *database.BroadcastDeliveryReport, finishedAt time.Time, totalProcessed, sent, blocked, failed int64) {
	broadcast := &database.Broadcast{
		ID:              broadcastID,
		Status:          status,
		FinishedAt:      &finishedAt,
		RecipientsTotal: totalProcessed,
		SentCount:       sent,
		BlockedCount:    blocked,
		FailedCount:     failed,
	}
	err := broadcast.SetDeliveryReport(report)
	if err != nil {
		logger.Error("Failed to marshal broadcast delivery report", zap.Uint("broadcast_id", broadcastID), zap.Error(err))
	}
	err = h.db.UpdateBroadcast(ctx, broadcast)
	if err != nil {
		logger.Error("Failed to update broadcast", zap.Uint("broadcast_id", broadcastID), zap.Error(err))
	}
}

// sendBroadcastReport отправляет финальный отчёт администратору с кнопкой
// «Детали рассылки» (ссылка на карточку).
func (h *Handler) sendBroadcastReport(ctx context.Context, chatID int64, text string, broadcastID uint) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableWebPagePreview = true
	if broadcastID > 0 {
		msg.ReplyMarkup = broadcastDetailsKeyboard(broadcastID)
	}
	h.send(ctx, msg)
}

// broadcastDetailsKeyboard возвращает кнопку перехода к карточке рассылки.
func broadcastDetailsKeyboard(broadcastID uint) *tgbotapi.InlineKeyboardMarkup {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Детали рассылки", fmt.Sprintf("broadcast_details_%d", broadcastID)),
		),
	)

	return &kb
}

// startBroadcastSession begins (or restarts) the draft flow for an admin.
func (h *Handler) startBroadcastSession(chatID int64) {
	h.broadcastMu.Lock()
	defer h.broadcastMu.Unlock()

	h.broadcastSessions[chatID] = &broadcastSession{createdAt: time.Now(), stage: broadcastStageAwaitingName}
}

// getBroadcastSession returns the active broadcast session for an admin, or nil.
func (h *Handler) getBroadcastSession(chatID int64) *broadcastSession {
	h.broadcastMu.Lock()
	defer h.broadcastMu.Unlock()

	s, ok := h.broadcastSessions[chatID]
	if !ok || time.Since(s.createdAt) > broadcastSessionTTL {
		delete(h.broadcastSessions, chatID)
		return nil
	}

	return s
}

// clearBroadcastSession removes the broadcast session for an admin.
func (h *Handler) clearBroadcastSession(chatID int64) {
	h.broadcastMu.Lock()
	defer h.broadcastMu.Unlock()

	delete(h.broadcastSessions, chatID)
}

// splitMessage splits text into chunks of at most maxLen bytes. It prefers to
// break at spaces and newlines, but never breaks an open MarkdownV2 entity: a
// word that would exceed maxLen while an entity is still open is kept whole
// (the chunk may then exceed maxLen, but the entity stays valid). A single
// token longer than maxLen that is NOT inside an entity is hard-split by
// UTF-8 byte length at valid rune boundaries so multi-byte characters are
// never split and every returned chunk is at most maxLen bytes — this may
// break the entity (an accepted trade-off for pathological, whitespace-free
// input).
func splitMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		return []string{text}
	}

	if len(text) <= maxLen {
		return []string{text}
	}

	var (
		chunks []string
		cur    strings.Builder
		open   []string
	)

	lastNewline := false
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()

			open = nil
		}

		lastNewline = false
	}

	// addWord appends a word. While an inline entity is open the chunk must not
	// be split (that would invalidate the entity), so the word is kept whole
	// even if it pushes the chunk past maxLen. Otherwise we break the chunk
	// first when the word would not fit.
	addWord := func(word string) {
		sep := 0
		if cur.Len() > 0 && !lastNewline {
			sep = 1
		}

		if len(open) == 0 && cur.Len() > 0 && cur.Len()+sep+len(word) > maxLen {
			flush()
		}

		if cur.Len() > 0 && !lastNewline {
			cur.WriteByte(' ')
		}

		cur.WriteString(word)

		lastNewline = false

		updateEntities(&open, word)
	}

	for li, line := range strings.Split(text, "\n") {
		if li > 0 && cur.Len() > 0 {
			// Newlines are legal inside MarkdownV2 entities, so keep an open
			// entity intact across the break. Break the chunk only when no
			// entity is open and there is no room for the newline.
			if len(open) == 0 && cur.Len()+1 > maxLen {
				flush()
			} else {
				cur.WriteByte('\n')

				lastNewline = true
			}
		}

		for word := range strings.FieldsSeq(line) {
			if len(word) > maxLen {
				// The token is over-long. If an entity is open, or the token
				// itself contains an entity delimiter, keep it whole: a
				// hard-split would break the entity and trigger a Telegram
				// parse error. We tolerate the chunk exceeding maxLen; such a
				// token is inherently unparseable in MarkdownV2 anyway.
				if len(open) > 0 || containsEntityChar(word) {
					addWord(word)
					continue
				}

				flush()

				for _, piece := range hardSplitToken(word, maxLen) {
					if cur.Len() > 0 {
						flush()
					}

					cur.WriteString(piece)
				}

				continue
			}

			addWord(word)
		}
	}

	flush()

	return chunks
}

// updateEntities maintains the stack of currently-open MarkdownV2 inline
// entities as text is appended. Delimiters handled: * _ ` ~ (open and close),
// [ ] (link text open / close).
func updateEntities(open *[]string, seg string) {
	for _, r := range seg {
		switch r {
		case '[':
			*open = append(*open, "[")
		case ']':
			if len(*open) > 0 && (*open)[len(*open)-1] == "[" {
				*open = (*open)[:len(*open)-1]
			} else {
				*open = append(*open, "]")
			}
		case '*', '_', '`', '~':
			if len(*open) > 0 && (*open)[len(*open)-1] == string(r) {
				*open = (*open)[:len(*open)-1]
			} else {
				*open = append(*open, string(r))
			}
		}
	}
}

// hardSplitToken splits a single over-long token into chunks of at most maxLen
// bytes, cutting only at rune boundaries so multi-byte characters are never
// split and every returned chunk is at most maxLen bytes.
func hardSplitToken(word string, maxLen int) []string {
	// Split by runes first to preserve UTF-8, then re-encode each chunk and
	// verify its byte length stays within maxLen.
	runes := []rune(word)

	var out []string

	for len(runes) > 0 {
		// Grow the chunk by runes until adding the next rune would exceed
		// maxLen bytes (or we run out of runes).
		take := 0
		for take < len(runes) {
			next := string(runes[:take+1])
			if len(next) > maxLen {
				break
			}

			take++
		}

		if take == 0 {
			// A single rune is wider than maxLen; emit it alone to make
			// progress without producing invalid UTF-8.
			take = 1
		}

		out = append(out, string(runes[:take]))
		runes = runes[take:]
	}

	return out
}

// containsEntityChar reports whether the token contains a MarkdownV2 inline
// entity delimiter, meaning a hard-split would break an entity.
func containsEntityChar(s string) bool {
	for _, r := range s {
		switch r {
		case '*', '_', '`', '~', '[', ']':
			return true
		}
	}

	return false
}

// broadcastSessionActive reports whether an admin has an in-progress broadcast.
func (h *Handler) broadcastSessionActive(chatID int64) bool {
	s := h.getBroadcastSession(chatID)
	return s != nil && (s.stage == broadcastStageAwaitingName || s.stage == broadcastStageAwaitingDraft || s.stage == broadcastStagePreview)
}

// sendBroadcastDetails отправляет карточку рассылки. Используется кнопкой
// «Детали рассылки» из финального отчёта рассылки.
func (h *Handler) sendBroadcastDetails(ctx context.Context, chatID int64, broadcastID uint) {
	c, err := h.db.GetBroadcast(ctx, broadcastID)
	if err != nil {
		if errors.Is(err, database.ErrBroadcastNotFound) {
			h.SendMessage(ctx, chatID, "❌ Рассылка не найдена.")
			return
		}

		logger.Error("Failed to get broadcast", zap.Uint("broadcast_id", broadcastID), zap.Error(err))
		h.SendMessage(ctx, chatID, "❌ Ошибка получения рассылки.")

		return
	}

	report, err := c.ParseDeliveryReport()
	if err != nil {
		logger.Warn("Failed to parse delivery report", zap.Uint("broadcast_id", c.ID), zap.Error(err))
		report = &database.BroadcastDeliveryReport{Delivered: []int64{}, Blocked: []int64{}, Errors: []database.BroadcastSendError{}}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "📦 *Рассылка #%d: %s*\n\n", c.ID, c.Name)
	fmt.Fprintf(&sb, "🗂 Статус: %s %s\n", broadcastStatusEmoji(c.Status), broadcastStatusLabel(c.Status))

	if c.PlannedAt != nil {
		fmt.Fprintf(&sb, "🗓 Запланирована: %s\n", c.PlannedAt.Format("02.01.06 15:04"))
	}
	if c.StartedAt != nil {
		fmt.Fprintf(&sb, "▶️ Начало: %s\n", c.StartedAt.Format("02.01.06 15:04"))
	}
	if c.FinishedAt != nil {
		fmt.Fprintf(&sb, "⏹ Конец: %s\n", c.FinishedAt.Format("02.01.06 15:04"))
	}

	fmt.Fprintf(&sb, "\n👥 Получателей: %d\n", c.RecipientsTotal)
	fmt.Fprintf(&sb, "📤 Отправлено: %d\n", c.SentCount)
	fmt.Fprintf(&sb, "🚫 Заблокировали бота: %d\n", c.BlockedCount)
	fmt.Fprintf(&sb, "❌ Ошибок: %d\n", c.FailedCount)

	if c.Filters != "" && c.Filters != "{}" {
		fmt.Fprintf(&sb, "\n🔎 Фильтры: `%s`\n", c.Filters)
	}

	if len(report.Delivered) > 0 {
		fmt.Fprintf(&sb, "\n✅ Доставлено (ID): %s\n", formatIDList(report.Delivered, 15))
	}
	if len(report.Blocked) > 0 {
		fmt.Fprintf(&sb, "🚫 Заблокировали (ID): %s\n", formatIDList(report.Blocked, 15))
	}
	if len(report.Errors) > 0 {
		fmt.Fprintf(&sb, "❌ Ошибки (ID): %s\n", formatErrorList(report.Errors, 15))
	}

	fmt.Fprintf(&sb, "\n💬 *Текст:*\n%s", truncateRunes(c.MessageText, broadcastTextPreviewMaxRunes))

	msg := tgbotapi.NewMessage(chatID, utils.EscapeMarkdownV2(sb.String()))
	msg.ParseMode = "MarkdownV2"
	h.send(ctx, msg)
}

// broadcastStatusEmoji возвращает эмодзи статуса рассылки для списков.
func broadcastStatusEmoji(status string) string {
	switch status {
	case string(database.BroadcastStatusScheduled):
		return "⏳"
	case string(database.BroadcastStatusRunning):
		return "▶️"
	case string(database.BroadcastStatusCompleted):
		return "✅"
	case string(database.BroadcastStatusFailed):
		return "❌"
	case string(database.BroadcastStatusCanceled):
		return "⚠️"
	default:
		return "❔"
	}
}

// broadcastStatusLabel возвращает человекочитаемую подпись статуса рассылки.
func broadcastStatusLabel(status string) string {
	switch status {
	case string(database.BroadcastStatusScheduled):
		return "запланирована"
	case string(database.BroadcastStatusRunning):
		return "идёт отправка"
	case string(database.BroadcastStatusCompleted):
		return "завершена"
	case string(database.BroadcastStatusFailed):
		return "прервана ошибкой"
	case string(database.BroadcastStatusCanceled):
		return "прервана"
	default:
		return status
	}
}

// formatIDList форматирует список telegram_id, усекая до maxItems.
func formatIDList(ids []int64, maxItems int) string {
	if len(ids) == 0 {
		return "—"
	}

	parts := make([]string, 0, min(len(ids), maxItems))
	for i, id := range ids {
		if i >= maxItems {
			break
		}
		parts = append(parts, strconv.FormatInt(id, 10))
	}

	s := strings.Join(parts, ", ")
	if len(ids) > maxItems {
		s += fmt.Sprintf(" … и ещё %d", len(ids)-maxItems)
	}

	return s
}

// formatErrorList форматирует список ошибок доставки, усекая до maxItems.
func formatErrorList(errs []database.BroadcastSendError, maxItems int) string {
	if len(errs) == 0 {
		return "—"
	}

	parts := make([]string, 0, min(len(errs), maxItems))
	for i, e := range errs {
		if i >= maxItems {
			break
		}
		parts = append(parts, fmt.Sprintf("%d (%s)", e.TelegramID, e.Error))
	}

	s := strings.Join(parts, "; ")
	if len(errs) > maxItems {
		s += fmt.Sprintf(" … и ещё %d", len(errs)-maxItems)
	}

	return s
}

// truncateRunes обрезает строку до maxRunes рун (без разрыва UTF-8) с эллипсисом.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}

	return string(r[:maxRunes]) + "…"
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
	var (
		telegramID int64
		err        error
	)

	// Check if target is a number (Telegram ID)
	id, parseErr := strconv.ParseInt(target, 10, 64)
	if parseErr == nil {
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
	logger.Info("Admin requesting stats", zap.String("username", username), zap.Int64("chat_id", chatID))

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
			utils.FormatUserLink(username, chatID),
			chatID,
			subscriptionURL,
		))
	msg.ParseMode = "Markdown"

	err := h.sendWithError(ctx, msg)
	if err != nil {
		logger.Warn("Failed to notify admin", zap.String("username", username), zap.Error(err))
		return fmt.Errorf("notify admin: %w", err)
	}

	logger.Info("Admin notified about new subscription", zap.String("username", username), zap.Int64("chat_id", chatID))

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

	logger.Info("Admin requesting referral stats", zap.String("username", username), zap.Int64("chat_id", chatID))

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
	fmt.Fprintf(&sb, "👥 Всего рефералов: %d\n", totalReferrals)
	fmt.Fprintf(&sb, "👤 Уникальных рефереров: %d\n\n", len(referrals))

	if len(referrals) > 0 {
		sb.WriteString("🏆 *Топ-10 рефереров:*\n")

		limit := min(len(referrals), 10)

		for i := range limit {
			r := referrals[i]
			fmt.Fprintf(&sb, "%d\\. ID %d: %d рефералов\n", i+1, r.chatID, r.count)
		}
	} else {
		sb.WriteString("📭 Нет данных о рефералах")
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "MarkdownV2"
	h.send(ctx, msg)

	return nil
}
