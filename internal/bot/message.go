package bot

import (
	"context"
	"time"

	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// send sends a message with rate limiting and saves the message ID for future editing.
func (h *Handler) send(ctx context.Context, msg tgbotapi.MessageConfig) {
	// Disable link previews for all messages
	msg.DisableWebPagePreview = true

	if !h.rateLimiter.Wait(ctx) {
		logger.Warn("Message send cancelled due to context")
		return
	}

	_, err := h.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send message", zap.Error(err))
		return
	}

}

// safeSend sends a message and logs any errors.
// Use this for non-critical messages where you don't need to handle the error.
func (h *Handler) safeSend(chattable tgbotapi.Chattable) {
	_, err := h.bot.Send(chattable)
	if err != nil {
		logger.Error("Failed to send message", zap.Error(err))
	}
}

// sendWithRetry sends a message with rate limiting and retry logic.
// Saves the message ID for future editing on success.
func (h *Handler) sendWithRetry(ctx context.Context, msg tgbotapi.MessageConfig, maxRetries int) {
	// Disable link previews for all messages
	msg.DisableWebPagePreview = true

	delay := time.Second

	for i := 0; i < maxRetries; i++ {
		if !h.rateLimiter.Wait(ctx) {
			logger.Warn("Message send cancelled due to context")
			return
		}

		_, err := h.bot.Send(msg)
		if err == nil {
			return
		}

		if i < maxRetries-1 {
			logger.Warn("Message send failed, retrying",
				zap.Duration("delay", delay),
				zap.Error(err))

			select {
			case <-time.After(delay):
				delay *= 2 // Exponential backoff
			case <-ctx.Done():
				logger.Warn("Message retry cancelled due to context")
				return
			}
		}
	}

	logger.Error("Failed to send message after retries", zap.Int("max_retries", maxRetries))
}

// SendMessage sends a plain text message to a chat.
func (h *Handler) SendMessage(ctx context.Context, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	h.send(ctx, msg)
}
