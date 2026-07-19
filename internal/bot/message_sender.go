package bot

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/ratelimiter"

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

func (ms *MessageSender) SetBot(bot interfaces.BotAPI) {
	ms.bot = bot
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

	start := time.Now()
	_, err := ms.bot.Send(msg)
	duration := time.Since(start).Seconds()

	if err != nil {
		metrics.TelegramAPICallsTotal.WithLabelValues("send", "error").Inc()
		metrics.TelegramAPIDuration.WithLabelValues("send").Observe(duration)
		if isUserBlockedError(err) {
			logger.Warn("Failed to send message", zap.Error(err))
		} else {
			logger.Error("Failed to send message", zap.Error(err))
		}
		return err
	}

	metrics.TelegramAPICallsTotal.WithLabelValues("send", "success").Inc()
	metrics.TelegramAPIDuration.WithLabelValues("send").Observe(duration)
	return nil
}

// SafeSend sends a message without rate limiting and logs errors.
func (ms *MessageSender) SafeSend(chattable tgbotapi.Chattable) {
	start := time.Now()
	_, err := ms.bot.Send(chattable)
	duration := time.Since(start).Seconds()

	if err != nil {
		metrics.TelegramAPICallsTotal.WithLabelValues("send", "error").Inc()
		metrics.TelegramAPIDuration.WithLabelValues("send").Observe(duration)
		if isUserBlockedError(err) {
			logger.Warn("Failed to send message", zap.Error(err))
		} else {
			logger.Error("Failed to send message", zap.Error(err))
		}
	} else {
		metrics.TelegramAPICallsTotal.WithLabelValues("send", "success").Inc()
		metrics.TelegramAPIDuration.WithLabelValues("send").Observe(duration)
	}
}

// SendMessage sends a plain text message to a chat.
func (ms *MessageSender) SendMessage(ctx context.Context, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	ms.Send(ctx, msg)
}
