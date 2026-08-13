package utils

import (
	"fmt"
	"strings"
	"time"
)

// IsRealUsername checks if the given identifier is a real Telegram username
// (not a fallback like "user_<id>") that can be used in t.me links and @ mentions.
func IsRealUsername(username string) bool {
	if username == "" {
		return false
	}

	for _, r := range username {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') { //nolint:staticcheck // QF1001 suppressed: negation of combined range check is clearer than expanded De Morgan form
			return false
		}
	}

	return true
}

// IsNumericUsername checks if the username is purely numeric (e.g. "11", "123").
// Telegram does not support purely numeric usernames -- t.me/11 will not resolve.
// Use tg://user?id=<id> deep link instead to ensure the profile opens correctly.
func IsNumericUsername(username string) bool {
	if username == "" {
		return false
	}

	for _, r := range username {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// FormatUserLink returns a Markdown-formatted clickable user link for Telegram.
// For alphabetic usernames, links to https://t.me/username.
// For purely numeric usernames (e.g. "11"), uses tg://user?id=ID deep link,
// because Telegram does not resolve t.me/123 as a profile.
// For empty/unsupported usernames, falls back to tg://user?id=TelegramID deep link
// with "unknown" display text.
func FormatUserLink(username string, telegramID int64) string {
	if IsNumericUsername(username) && telegramID != 0 {
		return fmt.Sprintf("[%s](tg://user?id=%d)", username, telegramID)
	}

	if IsRealUsername(username) {
		return fmt.Sprintf("[@%s](https://t.me/%s)", username, username)
	}

	if telegramID != 0 {
		return fmt.Sprintf("[unknown](tg://user?id=%d)", telegramID)
	}

	return "[unknown](#)"
}

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
	filled := min(int(percentage/10), 10)

	var bar strings.Builder

	for i := range 10 {
		if i < filled {
			bar.WriteString("🟩")
		} else {
			bar.WriteString("⬜")
		}
	}

	return bar.String()
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
	days := max(int(duration.Hours()/24), 0)

	return days
}

// FormatDateRu formats a date in Russian locale (e.g., "15 января 2025").
// Returns "--" for zero time.
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

// TruncateString truncates s to at most maxLen runes, appending "..." if truncation occurs.
// Rune-safe: correctly handles multi-byte UTF-8 (Cyrillic, CJK, emoji) without splitting characters.
func TruncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}

	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}

	return string(r[:maxLen]) + "..."
}
