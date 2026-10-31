package service

import (
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"go.uber.org/zap"
)

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
// midnight, today included. Months shorter than the day are skipped (Feb has
// no 31st — the loop advances to the first month that does), matching
// "ближайшая будущая дата с числом D". Month arithmetic is done on
// (year, month) pairs: now.AddDate(0, k, 0) would normalize day-overflow
// (Oct 31 + 1 month → Dec 1) and silently skip months.
func nextMonthlyReset(now time.Time, day int) time.Time {
	today := truncateDay(now)
	year, month, _ := now.Date()

	for range 13 {
		candidate := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		if candidate.Day() == day && !candidate.Before(today) {
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
// midnight, today included. weekday is 1=Monday..7=Sunday (panel numbering).
func nextWeeklyReset(now time.Time, weekday int) time.Time {
	// time.Weekday is 0=Sunday..6=Saturday; panel 7=Sunday maps to 0.
	target := time.Weekday(weekday % 7)
	delta := (int(target) - int(now.Weekday()) + 7) % 7

	return truncateDay(now).AddDate(0, 0, delta)
}

// fullDays counts whole calendar days between two moments, truncating both to
// local midnight.
func fullDays(from, to time.Time) int {
	return int(truncateDay(to).Sub(truncateDay(from)).Hours() / 24)
}

// truncateDay floors a moment to local midnight.
func truncateDay(t time.Time) time.Time {
	year, month, day := t.Date()

	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}
