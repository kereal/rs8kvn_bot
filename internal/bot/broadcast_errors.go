package bot

import (
	"errors"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

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

// isFloodError reports whether Telegram rate-limited the bot (HTTP 429,
// "Too Many Requests"). Flood errors are transient: delivery waits out the
// retry_after hint instead of recording the recipient as permanently failed.
func isFloodError(err error) bool {
	var apiErr *tgbotapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == 429 || strings.Contains(strings.ToLower(apiErr.Message), "too many requests")
	}

	return err != nil && strings.Contains(strings.ToLower(err.Error()), "too many requests")
}

// floodRetryDelay extracts the wait hinted by Telegram's retry_after parameter
// (seconds). Falls back to a default when the hint is missing and caps the
// result so one large hint cannot consume the whole campaign time slice.
func floodRetryDelay(err error) time.Duration {
	delay := broadcastFloodDefaultDelay

	var apiErr *tgbotapi.Error
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		if hinted := time.Duration(apiErr.RetryAfter) * time.Second; hinted < broadcastFloodMaxDelay {
			delay = hinted
		} else {
			delay = broadcastFloodMaxDelay
		}
	}

	return delay
}
