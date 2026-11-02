package service

import (
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/kereal/rs8kvn_bot/internal/xui"
	"go.uber.org/zap"
)

// PremiumBenefitsText is the concise user-facing summary shared by purchase
// and payment-success messages.
const PremiumBenefitsText = "♾️ Безлимитный трафик\n🌍 Больше серверов и вариантов подключения\n🧪 Дополнительные и экспериментальные функции\n💬 Приоритетная поддержка"

// FormatSubscriptionMessage renders the canonical subscription presentation.
// Both the bot's "My subscription" screen and payment success notification use
// this helper so tariff, traffic, dates, reset information and URL stay aligned.
func FormatSubscriptionMessage(heading, status string, traffic *TrafficInfo, subURL string) string {
	if traffic == nil {
		traffic = &TrafficInfo{}
	}

	trafficInfo := "неограничен"
	progress := ""

	if traffic.LimitGB > 0 {
		// Available=false: трафик не измерен (нет читателя на активных узлах
		// или все чтения упали) — показываем честное «измерение недоступно»,
		// а не вводящие в заблуждение 0 ГБ.
		if !traffic.Available {
			trafficInfo = "измерение недоступно"
		} else {
			trafficInfo = fmt.Sprintf("%.2f из %d Гб (%.0f%%)", traffic.UsedGB, traffic.LimitGB, traffic.Percentage)
			progress = "\n" + traffic.ProgressBar
		}
	}

	resetInfo := traffic.ResetInfo
	if resetInfo == "" {
		resetInfo = "нет"
	}

	if status == "" {
		status = "активна"
	}

	return fmt.Sprintf(
		"%s\n\n✌️ Статус: *%s*\n💡 Тариф: *%s*\n📊 Трафик: %s%s\n\n📅 Создана: %s\n⏰ Истекает: %s\n🔄 Сброс: %s\n\n🔗 Ссылка (для Happ)\n`%s`",
		heading,
		status,
		utils.EscapeLegacyMarkdown(traffic.PlanName),
		trafficInfo,
		progress,
		traffic.CreatedAtFormatted,
		traffic.ExpiresAtFormatted,
		resetInfo,
		subURL,
	)
}

// SubscriptionURL is kept as a tiny presentation seam for callers that already
// carry Config and should not duplicate URL construction.
func SubscriptionURL(cfg *config.Config, subID string) string {
	if cfg == nil {
		return ""
	}

	return cfg.SubURL(subID)
}

// resetDisplay is the rendered «🔄 Сброс» line of the subscription card.
type resetDisplay struct {
	// Text is the user-facing string: "через N дн.", "сегодня",
	// "не сбрасывается" or "неизвестно".
	Text string
	// Days is the whole-day distance to the next reset (0 = today). Meaningful
	// only when Text is a countdown.
	Days int
}

// resetUnknownText is shown when the reset schedule exists but cannot be
// computed honestly (unknown period, weekly without a weekday, profile error).
const resetUnknownText = "неизвестно"

// resetNeverText is shown when the panel profile has no traffic reset cycle.
const resetNeverText = "не сбрасывается"

// describeTrafficReset derives the next traffic-reset date from the panel's
// per-client profile (GET /panel/api/clients/get/{email}): TrafficReset is the
// period, TrafficResetDay the day of month for monthly resets, ResetWeekday
// the weekday for weekly resets. Panel semantics (3x-ui client_crud.go and
// OpenAPI): day 0 normalizes to 1 for monthly; weekday is 1=Monday..7=Sunday,
// 0 disables weekly; resets fire at panel-local midnight.
func describeTrafficReset(now time.Time, profile xui.ClientConfig) resetDisplay {
	switch profile.TrafficReset {
	case "", "never":
		return resetDisplay{Text: resetNeverText}
	case "daily":
		return countdown(now, now.AddDate(0, 0, 1))
	case "monthly":
		day := profile.TrafficResetDay
		if day == 0 {
			// Panel normalizeTrafficResetDay stores 0 as 1; the bot never
			// writes 0, but hand-edited panels may still hold it.
			day = 1
		}
		if day < 0 || day > 31 {
			logger.Debug("traffic reset: monthly day out of range",
				zap.Int("traffic_reset_day", profile.TrafficResetDay))

			return resetDisplay{Text: resetUnknownText}
		}

		return countdown(now, nextMonthlyReset(now, day))
	case "weekly":
		// resetWeekday: 1=Monday..7=Sunday, 0 disables the weekly cycle.
		if profile.ResetWeekday < 1 || profile.ResetWeekday > 7 {
			logger.Debug("traffic reset: weekly period without a weekday",
				zap.Int("reset_weekday", profile.ResetWeekday))

			return resetDisplay{Text: resetUnknownText}
		}

		return countdown(now, nextWeeklyReset(now, profile.ResetWeekday))
	default:
		// hourly and any future period the bot does not model yet: do not
		// guess a date.
		logger.Debug("traffic reset: unsupported period",
			zap.String("traffic_reset", profile.TrafficReset))

		return resetDisplay{Text: resetUnknownText}
	}
}

// countdown renders a future reset date as "сегодня" / "через N дн.".
func countdown(now, next time.Time) resetDisplay {
	days := fullDays(now, next)
	if days == 0 {
		return resetDisplay{Text: "сегодня"}
	}

	return resetDisplay{
		Text: fmt.Sprintf("через %d дн.", days),
		Days: days,
	}
}

// nextMonthlyReset returns the next occurrence of day-of-month day at local
// midnight strictly after today: the reset fires at panel-local midnight, so
// when the day matches today it has already passed. Months shorter than the
// day are skipped (Feb has no 31st — the loop advances to the first month
// that does), matching "ближайшая будущая дата с числом D". Month arithmetic
// is done on (year, month) pairs: now.AddDate(0, k, 0) would normalize
// day-overflow (Oct 31 + 1 month → Dec 1) and silently skip months.
func nextMonthlyReset(now time.Time, day int) time.Time {
	today := truncateDay(now)
	year, month, _ := now.Date()

	for range 13 {
		candidate := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		if candidate.Day() == day && candidate.After(today) {
			return candidate
		}

		month++
		if month > 12 {
			month = 1
			year++
		}
	}

	// Unreachable: every day of month 1..31 occurs within the next 13 months.
	return today.AddDate(1, 0, 0)
}

// nextWeeklyReset returns the next occurrence of the panel weekday at local
// midnight strictly after today: the reset fires at midnight, so when the
// weekday is today it has already fired and the cycle rolls to next week
// (delta == 0 → 7). weekday is 1=Monday..7=Sunday (panel numbering).
func nextWeeklyReset(now time.Time, weekday int) time.Time {
	// time.Weekday is 0=Sunday..6=Saturday; panel 7=Sunday maps to 0.
	target := time.Weekday(weekday % 7)
	delta := (int(target) - int(now.Weekday()) + 7) % 7
	if delta == 0 {
		delta = 7
	}

	return truncateDay(now).AddDate(0, 0, delta)
}

// fullDays counts whole calendar days between two moments. The distance is
// measured between UTC midnights of the dates' calendar days (not local
// midnights): across a DST transition two local days can span 23 or 25 hours,
// and integer division of that duration would round to the wrong day count.
func fullDays(from, to time.Time) int {
	f := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	t := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)

	return int(t.Sub(f).Hours() / 24)
}

// truncateDay floors a moment to local midnight.
func truncateDay(t time.Time) time.Time {
	year, month, day := t.Date()

	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}
