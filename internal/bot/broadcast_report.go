package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// sendBroadcastReport sends one compact completion message. Full recipient IDs
// remain in the persisted delivery_report and are available from Details.
func (h *Handler) sendBroadcastReport(ctx context.Context, chatID int64, text string, broadcastID uint) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableWebPagePreview = true
	if broadcastID > 0 {
		msg.ReplyMarkup = broadcastDetailsKeyboard(broadcastID)
	}
	h.send(ctx, msg)
}

// broadcastDetailsKeyboard returns the callback used to open the persisted report.
func broadcastDetailsKeyboard(broadcastID uint) *tgbotapi.InlineKeyboardMarkup {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Детали рассылки", fmt.Sprintf("broadcast_details_%d", broadcastID)),
		),
	)
	return &kb
}

// broadcastControlKeyboard exposes only actions valid for the current campaign state.
func broadcastControlKeyboard(broadcast *database.Broadcast) *tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("📋 Детали рассылки", fmt.Sprintf("broadcast_details_%d", broadcast.ID))),
	}
	switch broadcast.Status {
	case string(database.BroadcastStatusScheduled), string(database.BroadcastStatusRunning):
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⏹ Отменить", fmt.Sprintf("broadcast_cancel_%d", broadcast.ID))))
	case string(database.BroadcastStatusCompleted), string(database.BroadcastStatusFailed), string(database.BroadcastStatusCanceled):
		if broadcast.FailedCount > 0 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔁 Повторить ошибки", fmt.Sprintf("broadcast_retry_%d", broadcast.ID))))
		}
	}
	return &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// sendBroadcastDetails renders the compact report; complete lists stay in the database.
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
		report = emptyBroadcastDeliveryReport()
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
	fmt.Fprintf(&sb, "⚠️ Недоступны: %d\n", c.UnreachableCount)
	fmt.Fprintf(&sb, "❌ Ошибок: %d\n", c.FailedCount)
	if c.LastError != "" {
		fmt.Fprintf(&sb, "⚠️ Последняя ошибка worker-а: %s\n", c.LastError)
	}
	if c.Filters != "" && c.Filters != "{}" {
		fmt.Fprintf(&sb, "\n🔎 Фильтры: `%s`\n", c.Filters)
	}
	if len(report.Delivered) > 0 {
		fmt.Fprintf(&sb, "\n✅ Доставлено (ID): %s\n", formatIDList(report.Delivered, 15))
	}
	if len(report.Blocked) > 0 {
		fmt.Fprintf(&sb, "🚫 Заблокировали (ID): %s\n", formatIDList(report.Blocked, 15))
	}
	if len(report.Unreachable) > 0 {
		fmt.Fprintf(&sb, "⚠️ Недоступны (ID): %s\n", formatIDList(report.Unreachable, 15))
	}
	if len(report.Errors) > 0 {
		fmt.Fprintf(&sb, "❌ Ошибки (ID): %s\n", formatErrorList(report.Errors, 15))
	}
	if len(report.NotProcessed) > 0 {
		fmt.Fprintf(&sb, "⏳ Не обработано (ID): %s\n", formatIDList(report.NotProcessed, 15))
	}
	fmt.Fprintf(&sb, "\n💬 *Текст:*\n%s", truncateRunes(c.MessageText, broadcastTextPreviewMaxRunes))

	msg := tgbotapi.NewMessage(chatID, utils.EscapeMarkdownV2(sb.String()))
	msg.ParseMode = "MarkdownV2"
	msg.ReplyMarkup = broadcastControlKeyboard(c)
	h.send(ctx, msg)
}

// truncateRunes limits preview text without cutting a UTF-8 rune.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func emptyBroadcastDeliveryReport() *database.BroadcastDeliveryReport {
	return &database.BroadcastDeliveryReport{
		Delivered: []int64{}, Blocked: []int64{}, Unreachable: []int64{},
		Errors: []database.BroadcastSendError{}, NotProcessed: []int64{},
	}
}

// broadcastStatusEmoji returns the compact status marker used in campaign lists.
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

// broadcastStatusLabel returns the administrator-facing status label.
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

// formatIDList formats IDs and limits only the visible preview, not persisted data.
func formatIDList(ids []int64, maxItems int) string {
	if len(ids) == 0 {
		return "—"
	}
	capacity := len(ids)
	if maxItems > 0 && capacity > maxItems {
		capacity = maxItems
	}
	parts := make([]string, 0, capacity)
	for i, id := range ids {
		if maxItems > 0 && i >= maxItems {
			break
		}
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	result := strings.Join(parts, ", ")
	if maxItems > 0 && len(ids) > maxItems {
		result += fmt.Sprintf(" … и ещё %d", len(ids)-maxItems)
	}
	return result
}

// formatErrorList formats failed recipient IDs and their persisted error text.
func formatErrorList(errs []database.BroadcastSendError, maxItems int) string {
	if len(errs) == 0 {
		return "—"
	}
	capacity := len(errs)
	if maxItems > 0 && capacity > maxItems {
		capacity = maxItems
	}
	parts := make([]string, 0, capacity)
	for i, e := range errs {
		if maxItems > 0 && i >= maxItems {
			break
		}
		parts = append(parts, fmt.Sprintf("%d (%s)", e.TelegramID, e.Error))
	}
	result := strings.Join(parts, "; ")
	if maxItems > 0 && len(errs) > maxItems {
		result += fmt.Sprintf(" … и ещё %d", len(errs)-maxItems)
	}
	return result
}
