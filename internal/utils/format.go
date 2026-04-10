package utils

import (
	"fmt"
	"time"
)

// GenerateProgressBar creates a 10-block emoji progress bar representing
// traffic usage. Returns empty blocks when limitGB is zero or negative.
func GenerateProgressBar(usedGB, limitGB float64) string {
	if limitGB <= 0 {
		return "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜"
	}

	percentage := (usedGB / limitGB) * 100
	if percentage > 100 {
		percentage = 100
	}

	// 10 blocks total
	filled := int(percentage / 10)
	if filled > 10 {
		filled = 10
	}

	bar := ""
	for i := 0; i < 10; i++ {
		if i < filled {
			bar += "🟩"
		} else {
			bar += "⬜"
		}
	}

	return bar
}

// DaysUntilReset calculates the number of days until the next traffic reset.
// Returns -1 if auto-reset is not configured (expiryTime is zero).
// Returns 0 if already expired (reset should happen now).
// Returns positive number of days until reset otherwise.
func DaysUntilReset(now, expiryTime time.Time) int {
	if expiryTime.IsZero() {
		return -1 // Auto-reset not configured
	}

	if now.After(expiryTime) || now.Equal(expiryTime) {
		return 0 // Already expired, reset should happen now
	}

	duration := expiryTime.Sub(now)
	days := int(duration.Hours() / 24)

	if days < 0 {
		days = 0
	}

	return days
}

// FormatDateRu formats a date in Russian locale (e.g., "15 января 2025").
// Returns "—" for zero time.
func FormatDateRu(t time.Time) string {
	if t.IsZero() {
		return "—"
	}

	months := []string{
		"января", "февраля", "марта", "апреля", "мая", "июня",
		"июля", "августа", "сентября", "октября", "ноября", "декабря",
	}

	day := t.Day()
	month := months[t.Month()-1]
	year := t.Year()

	return fmt.Sprintf("%d %s %d", day, month, year)
}
