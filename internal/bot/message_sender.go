package bot

import (
	"context"

	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/ratelimiter"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// MessageSender handles rate-limited Telegram message sending.
type MessageSender struct {
	bot         interfaces.BotAPI
	rateLimiter *ratelimiter.PerUserRateLimiter
}

// NewMessageSender creates a new MessageSender.
func NewMessageSender(bot interfaces.BotAPI, rl *ratelimiter.PerUserRateLimiter) *MessageSender {
	return &MessageSender{bot: bot, rateLimiter: rl}
}

// Send sends a message with rate limiting, swallowing errors.
func (ms *MessageSender) Send(ctx context.Context, msg tgbotapi.MessageConfig) {
	_ = ms.SendWithError(ctx, msg)
}

// SendWithError sends a message with rate limiting and returns any error.
func (ms *MessageSender) SendWithError(ctx context.Context, msg tgbotapi.MessageConfig) error {
	msg.DisableWebPagePreview = true

	if !ms.rateLimiter.Wait(ctx, msg.ChatID) {
		return ctx.Err()
	}

	_, err := ms.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send message", zap.Error(err))
		return err
	}

	return nil
}

// SafeSend sends a message without rate limiting and logs errors.
func (ms *MessageSender) SafeSend(chattable tgbotapi.Chattable) {
	_, err := ms.bot.Send(chattable)
	if err != nil {
		logger.Error("Failed to send message", zap.Error(err))
	}
}

// SendMessage sends a plain text message to a chat.
func (ms *MessageSender) SendMessage(ctx context.Context, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	ms.Send(ctx, msg)
}
