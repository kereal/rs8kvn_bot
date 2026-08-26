package bot

import (
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// broadcastFilterKeyboard builds the filter controls used before confirmation.
// The selected values are reflected in button labels, while the filter itself
// is kept in the admin's in-memory draft until the campaign is created.
func broadcastFilterKeyboard(f database.BroadcastFilter) *tgbotapi.InlineKeyboardMarkup {
	planAll := "👥 Все"
	planPaid := "💰 Платные"
	planFree := "🆓 Бесплатные"
	switch f.PlanType {
	case "":
		planAll = "👥 Все ✓"
	case "paid":
		planPaid = "💰 Платные ✓"
	case "free":
		planFree = "🆓 Бесплатные ✓"
	}

	statusAll := "📋 Все"
	statusActive := "✅ Активные"
	statusRevoked := "🚫 Отозванные"
	switch f.SubscriptionStatus {
	case "active", "":
		statusActive = "✅ Активные ✓"
	case "all":
		statusAll = "📋 Все ✓"
	case "revoked":
		statusRevoked = "🚫 Отозванные ✓"
	}

	date3m := "📅 Рег. за 3 мес"
	date6m := "📅 Рег. за 6 мес"
	date1y := "📅 Рег. за год"
	dateAll := "📅 Рег. все"
	if f.RegisteredAfter != nil {
		days := int(time.Since(*f.RegisteredAfter).Hours() / 24)
		switch {
		case days <= 95:
			date3m = "📅 Рег. за 3 мес ✓"
		case days <= 185:
			date6m = "📅 Рег. за 6 мес ✓"
		case days <= 370:
			date1y = "📅 Рег. за год ✓"
		default:
			dateAll = "📅 Рег. все ✓"
		}
	} else {
		dateAll = "📅 Рег. все ✓"
	}

	inactiveNever := "🚫 Не обращались"
	inactive1m := "⏰ > 1 мес"
	inactive3m := "⏰ > 3 мес"
	inactiveNone := "👤 Без фильтра"
	if f.InactiveDays != nil {
		switch *f.InactiveDays {
		case 0:
			inactiveNever = "🚫 Не обращались ✓"
		case 30:
			inactive1m = "⏰ > 1 мес ✓"
		case 90:
			inactive3m = "⏰ > 3 мес ✓"
		}
	} else {
		inactiveNone = "👤 Без фильтра ✓"
	}

	payYes := "💳 Платили"
	payNo := "🆓 Не платили"
	payNone := "👤 Без фильтра"
	if f.EverPaid != nil {
		if *f.EverPaid {
			payYes = "💳 Платили ✓"
		} else {
			payNo = "🆓 Не платили ✓"
		}
	} else {
		payNone = "👤 Без фильтра ✓"
	}

	return &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(planAll, "bfilter_plan_"),
			tgbotapi.NewInlineKeyboardButtonData(planPaid, "bfilter_plan_paid"),
			tgbotapi.NewInlineKeyboardButtonData(planFree, "bfilter_plan_free"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(statusAll, "bfilter_status_all"),
			tgbotapi.NewInlineKeyboardButtonData(statusActive, "bfilter_status_active"),
			tgbotapi.NewInlineKeyboardButtonData(statusRevoked, "bfilter_status_revoked"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(date3m, "bfilter_date_90"),
			tgbotapi.NewInlineKeyboardButtonData(date6m, "bfilter_date_180"),
			tgbotapi.NewInlineKeyboardButtonData(date1y, "bfilter_date_365"),
			tgbotapi.NewInlineKeyboardButtonData(dateAll, "bfilter_date_"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(inactiveNever, "bfilter_inactive_0"),
			tgbotapi.NewInlineKeyboardButtonData(inactive1m, "bfilter_inactive_30"),
			tgbotapi.NewInlineKeyboardButtonData(inactive3m, "bfilter_inactive_90"),
			tgbotapi.NewInlineKeyboardButtonData(inactiveNone, "bfilter_inactive_"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(payYes, "bfilter_ever_paid_true"),
			tgbotapi.NewInlineKeyboardButtonData(payNo, "bfilter_ever_paid_false"),
			tgbotapi.NewInlineKeyboardButtonData(payNone, "bfilter_ever_paid_"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Отправить", "broadcast_confirm"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "broadcast_cancel"),
		),
	}}
}

// broadcastFilterPreview describes the current draft before the admin confirms it.
func broadcastFilterPreview(name string, f database.BroadcastFilter) string {
	return fmt.Sprintf("✅ Превью готово. Рассылка «%s».\n\n👥 Фильтр: %s\n\n📤 Отправить это сообщение всем?", name, f.String())
}

// broadcastScheduleDayLabel возвращает человекочитаемую подпись дня по offset.
func broadcastScheduleDayLabel(offset int) string {
	if offset == 0 {
		return "Сегодня"
	}
	if offset == 1 {
		return "Завтра"
	}
	day := time.Now().In(broadcastScheduleTZ).AddDate(0, 0, offset)
	return fmt.Sprintf("%s %s", ruWeekdays[day.Weekday()], day.In(broadcastScheduleTZ).Format("02.01"))
}

// broadcastScheduleDayKeyboard строит клавиатуру выбора дня отправки.
func broadcastScheduleDayKeyboard() *tgbotapi.InlineKeyboardMarkup {
	var row []tgbotapi.InlineKeyboardButton
	for _, offset := range scheduleDayOptions {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("📅 "+broadcastScheduleDayLabel(offset), fmt.Sprintf("bsched_day_%d", offset)))
	}
	return &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{
		row,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "bsched_back"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "broadcast_cancel"),
		),
	}}
}

// broadcastScheduleHourKeyboard строит клавиатуру выбора часа (0-23, МСК).
func broadcastScheduleHourKeyboard() *tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, 5)
	for h := 0; h < 24; h += 6 {
		row := make([]tgbotapi.InlineKeyboardButton, 0, 6)
		for i := range 6 {
			hour := h + i
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%02d:00", hour), fmt.Sprintf("bsched_hour_%d", hour)))
		}
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "bsched_back"),
		tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "broadcast_cancel"),
	))
	return &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// broadcastSchedulePreviewKeyboard — подтверждение запланированной рассылки.
func broadcastSchedulePreviewKeyboard() *tgbotapi.InlineKeyboardMarkup {
	return &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✅ Подтвердить", "broadcast_final_confirm")),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Изменить время", "bsched_back"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "broadcast_cancel"),
		),
	}}
}
