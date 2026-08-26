package bot

import "strings"

// isUserBlockedError recognizes only Telegram's explicit blocked-by-user error.
// Deactivated accounts and missing chats are tracked as unreachable instead.
func isUserBlockedError(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(strings.ToLower(err.Error()), "bot was blocked by the user")
}

// isUserBlockedOrGoneError reports whether the Telegram error means the user
// can no longer receive messages (blocked the bot, deactivated, or chat gone).
// These are expected during delivery and reported separately from real failures.
// The general message sender treats them as a warning; broadcast delivery keeps
// blocked and unreachable separate for its report.
func isUserBlockedOrGoneError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "bot was blocked by the user") ||
		strings.Contains(msg, "user is deactivated") ||
		strings.Contains(msg, "chat not found")
}

// isUserUnreachableError identifies permanent delivery failures that mean the
// chat is unavailable, but do not prove that the user blocked the bot.
func isUserUnreachableError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "user is deactivated") ||
		strings.Contains(message, "chat not found") ||
		strings.Contains(message, "kicked from the group") ||
		strings.Contains(message, "have no rights to send a message")
}
