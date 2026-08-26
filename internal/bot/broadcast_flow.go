package bot

// Broadcast admin flow: session state machine from /broadcast to launch,
// recipient filters, confirmation, scheduling and campaign history.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
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
	recipientCount int64      // количество получателей (для preview перед подтверждением)
	plannedAt      *time.Time // выбранное время запланированной отправки (nil = отправить сейчас)
	scheduleDay    int        // выбранный день в планировании: 0 = сегодня, 1 = завтра, ...; -1 = не выбран
}

// editBroadcastMessage обновляет текст и клавиатуру inline-сообщения (MarkdownV2).
func (h *Handler) editBroadcastMessage(chatID int64, messageID int, text string, kb *tgbotapi.InlineKeyboardMarkup, warn string) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, utils.EscapeMarkdownV2(text))
	edit.ParseMode = "MarkdownV2"
	edit.ReplyMarkup = kb
	if _, err := h.bot.Send(edit); err != nil {
		logger.Warn(warn, zap.Error(err))
	}
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
	runeCount := utf8.RuneCountInString(text)
	if runeCount > maxBroadcastLen {
		h.clearBroadcastSession(chatID)
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Сообщение слишком длинное (%d символов). Максимум — %d символов; рассылка автоматически разбивается на части по %d символов.", runeCount, maxBroadcastLen, config.MaxTelegramMessageLen))

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
	h.editBroadcastMessage(chatID, messageID,
		broadcastFilterPreview(s.name, f), broadcastFilterKeyboard(f),
		"Failed to update broadcast filter preview")

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

	h.editBroadcastMessage(chatID, messageID,
		fmt.Sprintf("🗓 Рассылка *«%s»* — выберите день отправки:", s.name),
		broadcastScheduleDayKeyboard(), "Failed to open broadcast schedule picker")
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

	h.editBroadcastMessage(chatID, messageID,
		fmt.Sprintf("🕐 Выбран день: *%s*. В какое время (МСК)?", broadcastScheduleDayLabel(offset)),
		broadcastScheduleHourKeyboard(), "Failed to open broadcast schedule hour picker")
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
	if err != nil {
		return fmt.Errorf("parse schedule hour: %w", err)
	}
	if hour < 0 || hour > 23 {
		return fmt.Errorf("unsupported schedule hour: %d", hour)
	}
	now := time.Now().In(broadcastScheduleTZ)
	day := now.AddDate(0, 0, s.scheduleDay)
	plannedAt := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, broadcastScheduleTZ)

	// Время в прошлом (например, «Сегодня» + уже прошедший час) не принимаем.
	if !plannedAt.After(time.Now()) {
		h.editBroadcastMessage(chatID, messageID,
			"⏰ Выбранное время уже прошло. Выберите другое:",
			broadcastScheduleHourKeyboard(), "Failed to reject past schedule time")
		return nil
	}

	h.broadcastMu.Lock()
	s.plannedAt = &plannedAt
	h.broadcastSessions[chatID] = s
	h.broadcastMu.Unlock()

	h.editBroadcastMessage(chatID, messageID,
		fmt.Sprintf("🗓 Рассылка *«%s»* запланирована на *%s*.\n\n👥 Получателей: *%d*\n\nПодтверждаете?",
			s.name, plannedAt.Format("02.01.2006 15:04"), s.recipientCount),
		broadcastSchedulePreviewKeyboard(), "Failed to render broadcast schedule preview")
	return nil
}

// handleBroadcastScheduleBack — обратная навигация по этапу планирования:
// из превью/часа — к выбору дня, из выбора дня — обратно к подтверждению.
func (h *Handler) handleBroadcastScheduleBack(ctx context.Context, chatID int64, messageID int) error {
	s := h.getBroadcastSession(chatID)
	if s == nil || s.stage != broadcastStageScheduling {
		return nil
	}

	if s.plannedAt != nil || s.scheduleDay >= 0 {
		// Из превью или выбора часа — обратно к выбору дня.
		h.broadcastMu.Lock()
		s.plannedAt = nil
		s.scheduleDay = -1
		h.broadcastSessions[chatID] = s
		h.broadcastMu.Unlock()

		h.editBroadcastMessage(chatID, messageID,
			"🗓 Выберите *день* отправки:",
			broadcastScheduleDayKeyboard(), "Failed to reopen broadcast schedule picker")
		return nil
	}

	// Из выбора дня — обратно к сообщению подтверждения.
	h.broadcastMu.Lock()
	s.stage = broadcastStageConfirming
	h.broadcastSessions[chatID] = s
	h.broadcastMu.Unlock()

	confirmText, kb := broadcastConfirmContent(s)
	h.editBroadcastMessage(chatID, messageID, confirmText, kb, "Failed to return to broadcast confirmation")
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
	h.editBroadcastMessage(chatID, messageID,
		broadcastFilterPreview(s.name, s.filter), broadcastFilterKeyboard(s.filter),
		"Failed to go back to filters")

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
		Status:    string(database.BroadcastStatusScheduled),
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
