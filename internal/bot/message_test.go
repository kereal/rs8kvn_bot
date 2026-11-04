package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSend_Success tests the send function with successful message
func TestSend_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := &testutil.BotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 123}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, nil, NewTestBotConfig(), nil, "")

	msg := tgbotapi.NewMessage(12345, "test message")
	handler.send(ctx, msg)

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
	assert.NotNil(t, mockBot.LastChattableSafe(), "Message should be captured")
}

// TestSend_Error_IncrementsTelegramAPIMetric verifies that a failed send is
// counted in the TelegramAPICallsTotal{send,error} series (G4): the error path
// must be observable in metrics, since send() itself swallows the error.
func TestSend_Error_IncrementsTelegramAPIMetric(t *testing.T) {
	// Reads global counter deltas: must not run in parallel with tests
	// bumping the same series.
	ctx := context.Background()

	mockBot := &testutil.BotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{}, errors.New("send failed")
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, nil, NewTestBotConfig(), nil, "")

	beforeErr := promtestutil.ToFloat64(metrics.TelegramAPICallsTotal.WithLabelValues("send", "error"))
	beforeOK := promtestutil.ToFloat64(metrics.TelegramAPICallsTotal.WithLabelValues("send", "success"))

	msg := tgbotapi.NewMessage(12345, "test message")
	handler.send(ctx, msg)

	errDelta := promtestutil.ToFloat64(metrics.TelegramAPICallsTotal.WithLabelValues("send", "error")) - beforeErr
	okDelta := promtestutil.ToFloat64(metrics.TelegramAPICallsTotal.WithLabelValues("send", "success")) - beforeOK

	assert.Equal(t, float64(1), errDelta, "failed send must increment the {send,error} series")
	assert.Zero(t, okDelta, "failed send must not touch the {send,success} series")
}

// TestSend_DisablesWebPagePreview tests that send disables web page preview
// and passes the caller's ParseMode through untouched.
func TestSend_DisablesWebPagePreview(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var capturedMsg tgbotapi.MessageConfig

	mockBot := &testutil.BotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			if msg, ok := c.(tgbotapi.MessageConfig); ok {
				capturedMsg = msg
			}

			return tgbotapi.Message{MessageID: 123}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, nil, NewTestBotConfig(), nil, "")

	msg := tgbotapi.NewMessage(12345, "test message with link https://example.com")
	handler.send(ctx, msg)

	assert.True(t, capturedMsg.DisableWebPagePreview, "Web page preview should be disabled")

	markdownMsg := tgbotapi.NewMessage(12345, "*bold* _italic_ `code`")
	markdownMsg.ParseMode = "Markdown"
	handler.send(ctx, markdownMsg)

	assert.Equal(t, "Markdown", capturedMsg.ParseMode, "ParseMode should be passed through")
	assert.True(t, capturedMsg.DisableWebPagePreview, "Web page preview should be disabled")
}

// TestSendMessage_Success tests the SendMessage function
func TestSendMessage_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := &testutil.BotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, nil, NewTestBotConfig(), nil, "")

	handler.SendMessage(ctx, 12345, "Hello, World!")

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")

	msg, ok := mockBot.LastChattableSafe().(tgbotapi.MessageConfig)
	require.True(t, ok, "Should be MessageConfig")
	assert.Equal(t, int64(12345), msg.ChatID, "ChatID")
	assert.Equal(t, "Hello, World!", msg.Text, "Message text")
	assert.True(t, msg.DisableWebPagePreview, "Web page preview should be disabled")
}

// TestSendMessage_EmptyMessage tests SendMessage with empty message
func TestSendMessage_EmptyMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := &testutil.BotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, nil, NewTestBotConfig(), nil, "")

	handler.SendMessage(ctx, 12345, "")

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called even with empty message")
}

// TestSendMessage_ContextCancellation tests SendMessage with context cancellation
func TestSendMessage_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockBot := &testutil.BotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, nil, NewTestBotConfig(), nil, "")

	handler.SendMessage(ctx, 12345, "test message")

	// With cancelled context, the rate limiter should return false and not send
	assert.Equal(t, 0, mockBot.SendCountSafe(), "Send should not be called with cancelled context")
}

// TestSafeSend_WithVariousChattables covers safeSend passthrough for every
// Chattable kind the bot sends (message, edit, delete): exactly one Send per
// call with the value forwarded unmodified.
func TestSafeSend_WithVariousChattables(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		chattable tgbotapi.Chattable
		checkFunc func(t *testing.T, c tgbotapi.Chattable)
	}{
		{
			name:      "MessageConfig",
			chattable: tgbotapi.NewMessage(12345, "test"),
			checkFunc: func(t *testing.T, c tgbotapi.Chattable) {
				_, ok := c.(tgbotapi.MessageConfig)
				assert.True(t, ok, "Should be MessageConfig")
			},
		},
		{
			name:      "EditMessageText",
			chattable: tgbotapi.NewEditMessageText(12345, 100, "edited"),
			checkFunc: func(t *testing.T, c tgbotapi.Chattable) {
				_, ok := c.(tgbotapi.EditMessageTextConfig)
				assert.True(t, ok, "Should be EditMessageTextConfig")
			},
		},
		{
			name:      "DeleteMessage",
			chattable: tgbotapi.NewDeleteMessage(12345, 100),
			checkFunc: func(t *testing.T, c tgbotapi.Chattable) {
				_, ok := c.(tgbotapi.DeleteMessageConfig)
				assert.True(t, ok, "Should be DeleteMessageConfig")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockBot := &testutil.BotAPI{
				SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
					return tgbotapi.Message{MessageID: 999}, nil
				},
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
			}

			handler := NewHandler(mockBot, cfg, nil, NewTestBotConfig(), nil, "")

			handler.safeSend(tc.chattable)

			assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
			tc.checkFunc(t, mockBot.LastChattableSafe())
		})
	}
}

// TestSend_MultipleConcurrentSends tests concurrent send operations
func TestSend_MultipleConcurrentSends(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := &testutil.BotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 123}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, nil, NewTestBotConfig(), nil, "")

	// Send multiple messages concurrently
	done := make(chan bool, 10)

	for range 10 {
		go func() {
			msg := tgbotapi.NewMessage(12345, "concurrent message")
			handler.send(ctx, msg)

			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for range 10 {
		<-done
	}

	assert.Equal(t, 10, mockBot.SendCountSafe(), "Send should be called 10 times")
}
