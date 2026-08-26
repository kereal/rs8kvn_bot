package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
	broadcastStageFiltering
	broadcastStageConfirming
	broadcastStageScheduling
)

// broadcastSession holds the in-progress broadcast draft for an admin.
type broadcastSession struct {
	createdAt      time.Time
	stage          broadcastStage
	name           string
	text           string
	filter         database.BroadcastFilter
	recipientCount int64  // количество получателей (для preview перед подтверждением)
	plannedAt      *time.Time // выбранное время запланированной отправки (nil = отправить сейчас)
	scheduleDay    int        // выбранный день в планировании: 0 = сегодня, 1 = завтра, ...; -1 = не выбран
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

	h.SendMessageMarkdown(ctx, chatID, fmt.Sprintf(
		"✅ Подписка успешно удалена!\n\n"+
			"🆔 ID: %d\n"+
			"👤 Пользователь: %s\n"+
			"🆔 Telegram ID: `%d`",
		id,
		utils.FormatUserLink(deleted.Username, deleted.TelegramID),
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

	h.SendMessageMarkdown(ctx, chatID, fmt.Sprintf(
		"✅ Тариф подписки изменён!\n\n"+
			"🆔 ID: %d\n"+
			"👤 Пользователь: %s\n"+
			"🆔 Telegram ID: `%d`\n"+
			"💡 Тариф: %d\n"+
			"⏰ Истекает: %s",
		updated.ID,
		utils.FormatUserLink(updated.Username, updated.TelegramID),
		updated.TelegramID,
		updated.PlanID,
		expiry,
	))

	return nil
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
		logger.Info("Broadcast draft canceled", zap.Int64("chat_id", chatID))
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

	var emptyFilter database.BroadcastFilter

	h.broadcastMu.Lock()
	h.broadcastSessions[chatID] = &broadcastSession{createdAt: time.Now(), stage: broadcastStageFiltering, name: name, text: text}
	h.broadcastMu.Unlock()

	kb := broadcastFilterKeyboard(emptyFilter)
	msg := tgbotapi.NewMessage(chatID, utils.EscapeMarkdownV2(broadcastFilterPreview(name, emptyFilter)))
	msg.ParseMode = "MarkdownV2"
	msg.ReplyMarkup = kb
	h.send(ctx, msg)

	return nil
}

// handleBroadcastFilter обрабатывает нажатия кнопок фильтров в broadcastSession.
// callbackData формат: bfilter_<type>_<value>
//   - bfilter_plan_ / bfilter_plan_paid / bfilter_plan_free
//   - bfilter_date_<days> / bfilter_date_ (сброс)
//   - bfilter_inactive_<days> / bfilter_inactive_ (сброс)
func (h *Handler) handleBroadcastFilter(ctx context.Context, chatID int64, messageID int, callbackData string) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || s.stage != broadcastStageFiltering {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки для настройки фильтров.")
		return nil
	}

	f := s.filter

	switch {
	case strings.HasPrefix(callbackData, "bfilter_plan_"):
		val := strings.TrimPrefix(callbackData, "bfilter_plan_")
		if val == f.PlanType {
			f.PlanType = "" // toggle off
		} else {
			f.PlanType = val
		}

	case strings.HasPrefix(callbackData, "bfilter_status_"):
		val := strings.TrimPrefix(callbackData, "bfilter_status_")
		if val != "active" && val != "all" && val != "revoked" {
			return fmt.Errorf("unsupported broadcast status filter: %s", val)
		}
		if val == "active" && f.SubscriptionStatus == "" {
			// default active is already selected
		} else if val == f.SubscriptionStatus {
			f.SubscriptionStatus = ""
		} else {
			f.SubscriptionStatus = val
		}

	case strings.HasPrefix(callbackData, "bfilter_date_"):
		val := strings.TrimPrefix(callbackData, "bfilter_date_")
		if val == "" {
			f.RegisteredAfter = nil
		} else {
			days, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("parse date filter: %w", err)
			}
			daysAgo := time.Now().AddDate(0, 0, -days)
			f.RegisteredAfter = &daysAgo
		}
		f.RegisteredBefore = nil // сброс верхней границы при выборе "За N мес"

	case strings.HasPrefix(callbackData, "bfilter_inactive_"):
		val := strings.TrimPrefix(callbackData, "bfilter_inactive_")
		if val == "" {
			f.InactiveDays = nil
		} else {
			days, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("parse inactive filter: %w", err)
			}
			f.InactiveDays = &days
		}

	case strings.HasPrefix(callbackData, "bfilter_ever_paid_"):
		val := strings.TrimPrefix(callbackData, "bfilter_ever_paid_")
		if val == "" {
			f.EverPaid = nil
		} else {
			everPaid := val == "true"
			f.EverPaid = &everPaid
		}

	default:
		return fmt.Errorf("unknown filter callback: %s", callbackData)
	}

	h.broadcastMu.Lock()
	s.filter = f
	h.broadcastSessions[chatID] = s
	h.broadcastMu.Unlock()

	// Обновляем сообщение с новым превью и клавиатурой.
	preview := broadcastFilterPreview(s.name, f)
	kb := broadcastFilterKeyboard(f)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2(preview))
	editMsg.ParseMode = "MarkdownV2"
	editMsg.ReplyMarkup = kb
	_, err := h.bot.Send(editMsg)
	if err != nil {
		logger.Warn("Failed to update broadcast filter preview", zap.Error(err))
	}

	return nil
}

// handleBroadcastConfirm обрабатывает подтверждение рассылки.
// Из broadcastStageFiltering → показывает количество получателей → broadcastStageConfirming.
// Из broadcastStageConfirming → запускает рассылку.
func (h *Handler) handleBroadcastConfirm(ctx context.Context, chatID int64) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || (s.stage != broadcastStageFiltering && s.stage != broadcastStageConfirming) {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки для подтверждения.")
		return nil
	}

	// Если мы на этапе фильтров — показываем количество получателей.
	if s.stage == broadcastStageFiltering {
		count, err := h.db.GetFilteredTelegramIDCount(ctx, s.filter)
		if err != nil {
			logger.Error("Failed to count broadcast recipients", zap.Error(err))
			h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Не удалось посчитать получателей:\n\n%v", err))
			return fmt.Errorf("count broadcast recipients: %w", err)
		}

		h.broadcastMu.Lock()
		s.recipientCount = count
		s.stage = broadcastStageConfirming
		h.broadcastSessions[chatID] = s
		h.broadcastMu.Unlock()

		confirmText, kb := broadcastConfirmContent(s)

		msg := tgbotapi.NewMessage(chatID, utils.EscapeMarkdownV2(confirmText))
		msg.ParseMode = "MarkdownV2"
		msg.ReplyMarkup = kb
		h.send(ctx, msg)

		return nil
	}

	// На этапе подтверждения — запускаем рассылку.
	return h.startBroadcast(ctx, chatID, s)
}

// broadcastConfirmContent возвращает текст и клавиатуру сообщения подтверждения:
// отправка сейчас либо переход к планированию времени.
func broadcastConfirmContent(s *broadcastSession) (string, *tgbotapi.InlineKeyboardMarkup) {
	confirmText := fmt.Sprintf(
		"📦 *Рассылка: %s*\n\n"+
			"👥 Получателей: *%d*\n"+
			"🔍 Фильтр: %s\n\n"+
			"⚡ Отправить %d пользователям?",
		s.name, s.recipientCount, s.filter.String(), s.recipientCount)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Отправить сейчас", "broadcast_final_confirm"),
			tgbotapi.NewInlineKeyboardButtonData("⏰ Запланировать", "broadcast_schedule"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Назад к фильтрам", "broadcast_back_to_filters"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "broadcast_cancel"),
		),
	)
	return confirmText, &kb
}

// handleBroadcastFinalConfirm запускает рассылку сразу или по расписанию после
// подтверждения количества получателей.
func (h *Handler) handleBroadcastFinalConfirm(ctx context.Context, chatID int64) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || (s.stage != broadcastStageConfirming && s.stage != broadcastStageScheduling) {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки для подтверждения.")
		return nil
	}

	return h.startBroadcast(ctx, chatID, s)
}

// handleBroadcastSchedule открывает выбор дня отправки из этапа подтверждения.
func (h *Handler) handleBroadcastSchedule(ctx context.Context, chatID int64, messageID int) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || s.stage != broadcastStageConfirming {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки для планирования.")
		return nil
	}

	h.broadcastMu.Lock()
	s.stage = broadcastStageScheduling
	s.plannedAt = nil
	s.scheduleDay = -1
	h.broadcastSessions[chatID] = s
	h.broadcastMu.Unlock()

	text := fmt.Sprintf("🗓 Рассылка *«%s»* — выберите день отправки:", s.name)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2(text))
	edit.ParseMode = "MarkdownV2"
	edit.ReplyMarkup = broadcastScheduleDayKeyboard()
	if _, err := h.bot.Send(edit); err != nil {
		logger.Warn("Failed to open broadcast schedule picker", zap.Error(err))
	}
	return nil
}

// handleBroadcastScheduleDay фиксирует выбранный день и показывает выбор часа.
// callbackData формат: bsched_day_<offset>, где offset — дней от сегодня.
func (h *Handler) handleBroadcastScheduleDay(ctx context.Context, chatID int64, messageID int, callbackData string) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || s.stage != broadcastStageScheduling {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки для планирования.")
		return nil
	}

	offset, err := strconv.Atoi(strings.TrimPrefix(callbackData, "bsched_day_"))
	if err != nil {
		return fmt.Errorf("parse schedule day: %w", err)
	}
	valid := false
	for _, o := range scheduleDayOptions {
		if o == offset {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("unsupported schedule day offset: %d", offset)
	}

	h.broadcastMu.Lock()
	s.scheduleDay = offset
	s.plannedAt = nil
	h.broadcastSessions[chatID] = s
	h.broadcastMu.Unlock()

	text := fmt.Sprintf("🕐 Выбран день: *%s*. В какое время (МСК)?", broadcastScheduleDayLabel(offset))
	edit := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2(text))
	edit.ParseMode = "MarkdownV2"
	edit.ReplyMarkup = broadcastScheduleHourKeyboard()
	if _, err := h.bot.Send(edit); err != nil {
		logger.Warn("Failed to open broadcast schedule hour picker", zap.Error(err))
	}
	return nil
}

// handleBroadcastScheduleHour фиксирует выбранный час и показывает превью
// запланированной рассылки. callbackData формат: bsched_hour_<час 0-23>.
func (h *Handler) handleBroadcastScheduleHour(ctx context.Context, chatID int64, messageID int, callbackData string) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || s.stage != broadcastStageScheduling || s.scheduleDay < 0 {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки для планирования.")
		return nil
	}

	hour, err := strconv.Atoi(strings.TrimPrefix(callbackData, "bsched_hour_"))
	if err != nil || hour < 0 || hour > 23 {
		return fmt.Errorf("parse schedule hour: %w", err)
	}
	day := time.Now().AddDate(0, 0, s.scheduleDay)
	plannedAt := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, time.Local)

	// Время в прошлом (например, «Сегодня» + уже прошедший час) не принимаем.
	if !plannedAt.After(time.Now()) {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2("⏰ Выбранное время уже прошло. Выберите другое:"))
		edit.ParseMode = "MarkdownV2"
		edit.ReplyMarkup = broadcastScheduleHourKeyboard()
		if _, err := h.bot.Send(edit); err != nil {
			logger.Warn("Failed to reject past schedule time", zap.Error(err))
		}
		return nil
	}

	h.broadcastMu.Lock()
	s.plannedAt = &plannedAt
	h.broadcastSessions[chatID] = s
	h.broadcastMu.Unlock()

	text := fmt.Sprintf("🗓 Рассылка *«%s»* запланирована на *%s*.\n\n👥 Получателей: *%d*\n\nПодтверждаете?",
		s.name, plannedAt.Format("02.01.2006 15:04"), s.recipientCount)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2(text))
	edit.ParseMode = "MarkdownV2"
	edit.ReplyMarkup = broadcastSchedulePreviewKeyboard()
	if _, err := h.bot.Send(edit); err != nil {
		logger.Warn("Failed to render broadcast schedule preview", zap.Error(err))
	}
	return nil
}

// handleBroadcastScheduleBack — обратная навигация по этапу планирования:
// из превью/часа — к выбору дня, из выбора дня — обратно к подтверждению.
func (h *Handler) handleBroadcastScheduleBack(ctx context.Context, chatID int64, messageID int) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || s.stage != broadcastStageScheduling {
		return nil
	}

	if s.plannedAt != nil {
		// Из превью — обратно к выбору дня.
		h.broadcastMu.Lock()
		s.plannedAt = nil
		s.scheduleDay = -1
		h.broadcastSessions[chatID] = s
		h.broadcastMu.Unlock()

		edit := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2("🗓 Выберите *день* отправки:"))
		edit.ParseMode = "MarkdownV2"
		edit.ReplyMarkup = broadcastScheduleDayKeyboard()
		if _, err := h.bot.Send(edit); err != nil {
			logger.Warn("Failed to reopen broadcast schedule picker", zap.Error(err))
		}
		return nil
	}

	if s.scheduleDay >= 0 {
		// Из выбора часа — обратно к выбору дня.
		h.broadcastMu.Lock()
		s.scheduleDay = -1
		h.broadcastSessions[chatID] = s
		h.broadcastMu.Unlock()

		edit := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2("🗓 Выберите *день* отправки:"))
		edit.ParseMode = "MarkdownV2"
		edit.ReplyMarkup = broadcastScheduleDayKeyboard()
		if _, err := h.bot.Send(edit); err != nil {
			logger.Warn("Failed to reopen broadcast schedule picker", zap.Error(err))
		}
		return nil
	}

	// Из выбора дня — обратно к сообщению подтверждения.
	h.broadcastMu.Lock()
	s.stage = broadcastStageConfirming
	h.broadcastSessions[chatID] = s
	h.broadcastMu.Unlock()

	confirmText, kb := broadcastConfirmContent(s)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2(confirmText))
	edit.ParseMode = "MarkdownV2"
	edit.ReplyMarkup = kb
	if _, err := h.bot.Send(edit); err != nil {
		logger.Warn("Failed to return to broadcast confirmation", zap.Error(err))
	}
	return nil
}

// handleBroadcastBackToFilters возвращает к выбору фильтров из этапа подтверждения.
func (h *Handler) handleBroadcastBackToFilters(ctx context.Context, chatID int64, messageID int) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || s.stage != broadcastStageConfirming {
		h.SendMessage(ctx, chatID, "❌ Нет активной рассылки.")
		return nil
	}

	h.broadcastMu.Lock()
	s.stage = broadcastStageFiltering
	h.broadcastSessions[chatID] = s
	h.broadcastMu.Unlock()

	// Показываем keyboard фильтров.
	preview := broadcastFilterPreview(s.name, s.filter)
	kb := broadcastFilterKeyboard(s.filter)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2(preview))
	editMsg.ParseMode = "MarkdownV2"
	editMsg.ReplyMarkup = kb
	_, err := h.bot.Send(editMsg)
	if err != nil {
		logger.Warn("Failed to go back to filters", zap.Error(err))
	}

	return nil
}

// startBroadcast запускает рассылку: создаёт запись в БД и отправляет сообщения.
func (h *Handler) startBroadcast(ctx context.Context, chatID int64, s *broadcastSession) error {
	// Claim the in-memory confirmation before any database work. This closes the
	// duplicate callback race even when two Telegram updates arrive together.
	h.broadcastMu.Lock()
	current, ok := h.broadcastSessions[chatID]
	if !ok || (current.stage != broadcastStageConfirming && current.stage != broadcastStageScheduling) {
		h.broadcastMu.Unlock()
		return nil
	}
	// Кнопка подтверждения в планировании видна только после выбора времени;
	// если планирование без времени (гоночный дубль) — игнорируем.
	if current.stage == broadcastStageScheduling && current.plannedAt == nil {
		h.broadcastMu.Unlock()
		return nil
	}
	name, text, filter := current.name, current.text, current.filter
	var plannedAt *time.Time
	if current.stage == broadcastStageScheduling {
		plannedAt = current.plannedAt
	}
	delete(h.broadcastSessions, chatID)
	h.broadcastMu.Unlock()

	filtersJSON := "{}"
	if !filter.IsEmpty() {
		data, err := json.Marshal(filter)
		if err != nil {
			return fmt.Errorf("marshal broadcast filter: %w", err)
		}
		filtersJSON = string(data)
	}

	broadcast := &database.Broadcast{
		Name: name, Filters: filtersJSON, MessageText: text,
		Status: string(database.BroadcastStatusScheduled),
		PlannedAt: plannedAt,
	}
	if err := h.db.CreateBroadcast(ctx, broadcast); err != nil {
		logger.Error("Failed to create broadcast", zap.Error(err))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Не удалось сохранить рассылку:\n\n%v", err))
		return fmt.Errorf("create broadcast: %w", err)
	}

	logger.Info("Broadcast created",
		zap.Uint("broadcast_id", broadcast.ID),
		zap.String("name", name),
		zap.Int64("recipient_count", current.recipientCount),
		zap.String("filters", filtersJSON))

	// Snapshot and sending happen in the background. The worker claims the
	// persistent row, so a second callback or a process restart cannot duplicate it.
	// A campaign scheduled for the future is left for the ticker worker, which
	// only claims it once planned_at is due.
	if plannedAt == nil || !plannedAt.After(time.Now()) {
		h.bgWg.Go(func() {
			workerCtx := h.broadcastContext()
			if err := h.broadcastWorker.processCampaign(workerCtx, broadcast); err != nil {
				switch {
				case errors.Is(err, context.Canceled):
					// Admin cancel or shutdown — nothing to log.
				case errors.Is(err, context.DeadlineExceeded), errors.Is(err, errBroadcastIncomplete):
					// Planned resume: a long campaign exceeded its time slice.
					logger.Info("Broadcast campaign will resume on next pass", zap.Uint("broadcast_id", broadcast.ID), zap.Error(err))
				default:
					logger.Warn("Broadcast launch failed", zap.Uint("broadcast_id", broadcast.ID), zap.Error(err))
				}
			}
		})
	}
	if plannedAt != nil {
		h.SendMessage(ctx, chatID, fmt.Sprintf("🗓 Рассылка #%d запланирована на %s. Отправка начнётся автоматически.", broadcast.ID, plannedAt.Format("02.01.2006 15:04")))
	} else {
		h.SendMessage(ctx, chatID, fmt.Sprintf("📤 Рассылка #%d поставлена в очередь. Отправка продолжается в фоне.", broadcast.ID))
	}
	return nil
}

// HandleBroadcastHistory shows recent campaign cards to an administrator.
func (h *Handler) HandleBroadcastHistory(ctx context.Context, update tgbotapi.Update) error {
	if update.Message == nil {
		return fmt.Errorf("nil message")
	}
	chatID := update.Message.Chat.ID
	if !h.isAdmin(chatID) {
		return nil
	}

	broadcasts, err := h.db.ListBroadcasts(ctx, 10)
	if err != nil {
		return fmt.Errorf("list broadcasts: %w", err)
	}
	if len(broadcasts) == 0 {
		h.SendMessage(ctx, chatID, "📭 История рассылок пуста.")
		return nil
	}

	var text strings.Builder
	text.WriteString("📋 *Последние рассылки*\n\n")
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(broadcasts))
	for _, broadcast := range broadcasts {
		fmt.Fprintf(&text, "%s #%d · %s · %d/%d\n", broadcastStatusEmoji(broadcast.Status), broadcast.ID, broadcast.Name, broadcast.SentCount, broadcast.RecipientsTotal)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("📋 #%d %s", broadcast.ID, truncateRunes(broadcast.Name, 25)),
			fmt.Sprintf("broadcast_details_%d", broadcast.ID),
		)))
	}

	msg := tgbotapi.NewMessage(chatID, utils.EscapeMarkdownV2(text.String()))
	msg.ParseMode = "MarkdownV2"
	msg.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
	h.send(ctx, msg)
	return nil
}

// handleBroadcastCancel discards the in-progress broadcast draft.
func (h *Handler) handleBroadcastCancel(ctx context.Context, chatID int64) error {
	h.clearBroadcastSession(chatID)
	logger.Info("Broadcast draft canceled", zap.Int64("chat_id", chatID))
	h.SendMessage(ctx, chatID, "❌ Рассылка отменена.")

	return nil
}

// startBroadcastSession begins (or restarts) the draft flow for an admin.
func (h *Handler) startBroadcastSession(chatID int64) {
	h.broadcastMu.Lock()
	defer h.broadcastMu.Unlock()

	h.broadcastSessions[chatID] = &broadcastSession{createdAt: time.Now(), stage: broadcastStageAwaitingName}
}

// getBroadcastSession returns the active broadcast session for an admin, or nil.
func (h *Handler) getBroadcastSession(chatID int64) *broadcastSession {
	h.broadcastMu.RLock()
	s, ok := h.broadcastSessions[chatID]
	expired := ok && time.Since(s.createdAt) > broadcastSessionTTL
	if ok && !expired {
		// Return a snapshot so concurrent Telegram callbacks cannot race on the
		// mutable session object while one callback updates the filter or stage.
		copy := *s
		h.broadcastMu.RUnlock()
		return &copy
	}
	h.broadcastMu.RUnlock()

	if expired {
		// Drop the stale entry under the exclusive lock: two concurrent readers
		// can both observe the expiry, and deleting under RLock would be a
		// concurrent map write (same pattern as SubscriptionCache.Get).
		h.broadcastMu.Lock()
		delete(h.broadcastSessions, chatID)
		h.broadcastMu.Unlock()
	}
	return nil
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
	return s != nil && (s.stage == broadcastStageAwaitingName || s.stage == broadcastStageAwaitingDraft || s.stage == broadcastStageFiltering || s.stage == broadcastStageConfirming || s.stage == broadcastStageScheduling)
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
