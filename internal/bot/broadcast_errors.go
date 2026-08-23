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
