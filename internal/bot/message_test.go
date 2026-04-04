package bot

import (
	"context"
	"errors"
	"testing"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSend_Success tests the send function with successful message
func TestSend_Success(t *testing.T) {
	ctx := context.Background()

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 123}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	msg := tgbotapi.NewMessage(12345, "test message")
	handler.send(ctx, msg)

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
	assert.NotNil(t, mockBot.LastChattableSafe(), "Message should be captured")
}

// TestSend_RateLimitContext tests the send function with context cancellation
func TestSend_RateLimitContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 123}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	msg := tgbotapi.NewMessage(12345, "test message")
	handler.send(ctx, msg)

	// With cancelled context, the rate limiter should return false and not send
	assert.Equal(t, 0, mockBot.SendCountSafe(), "Send should not be called with cancelled context")
}

// TestSend_SendError tests the send function when bot.Send fails
func TestSend_SendError(t *testing.T) {
	ctx := context.Background()

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{}, errors.New("send failed")
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	msg := tgbotapi.NewMessage(12345, "test message")

	// Should not panic on send error
	handler.send(ctx, msg)

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
}

// TestSend_DisablesWebPagePreview tests that send disables web page preview
func TestSend_DisablesWebPagePreview(t *testing.T) {
	ctx := context.Background()

	var capturedMsg tgbotapi.MessageConfig
	mockBot := &testutil.MockBotAPI{
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
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	msg := tgbotapi.NewMessage(12345, "test message with link https://example.com")
	handler.send(ctx, msg)

	assert.True(t, capturedMsg.DisableWebPagePreview, "Web page preview should be disabled")
}

// TestSafeSend_Success tests the safeSend function with successful message
func TestSafeSend_Success(t *testing.T) {
	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 456}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	msg := tgbotapi.NewMessage(12345, "safe message")
	handler.safeSend(msg)

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
}

// TestSafeSend_SendError tests safeSend when bot.Send fails
func TestSafeSend_SendError(t *testing.T) {
	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{}, errors.New("safe send failed")
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	msg := tgbotapi.NewMessage(12345, "safe message")

	// Should not panic on send error
	handler.safeSend(msg)

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
}

// TestSafeSend_WithEditMessage tests safeSend with EditMessageText
func TestSafeSend_WithEditMessage(t *testing.T) {
	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 789}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	editMsg := tgbotapi.NewEditMessageText(12345, 100, "edited message")
	handler.safeSend(editMsg)

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
	_, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	assert.True(t, ok, "Should be EditMessageTextConfig")
}

// TestSendMessage_Success tests the SendMessage function
func TestSendMessage_Success(t *testing.T) {
	ctx := context.Background()

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

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
	ctx := context.Background()

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	handler.SendMessage(ctx, 12345, "")

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called even with empty message")
}

// TestSendMessage_ContextCancellation tests SendMessage with context cancellation
func TestSendMessage_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	handler.SendMessage(ctx, 12345, "test message")

	// With cancelled context, the rate limiter should return false and not send
	assert.Equal(t, 0, mockBot.SendCountSafe(), "Send should not be called with cancelled context")
}

// TestSendMessage_SpecialCharacters tests SendMessage with special characters
func TestSendMessage_SpecialCharacters(t *testing.T) {
	ctx := context.Background()

	var capturedMsg tgbotapi.MessageConfig
	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			if msg, ok := c.(tgbotapi.MessageConfig); ok {
				capturedMsg = msg
			}
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	specialText := "Test with *markdown* and _formatting_ and `code`"
	handler.SendMessage(ctx, 12345, specialText)

	assert.Equal(t, specialText, capturedMsg.Text, "Message text should preserve special characters")
}

// TestSendMessage_Unicode tests SendMessage with unicode characters
func TestSendMessage_Unicode(t *testing.T) {
	ctx := context.Background()

	var capturedMsg tgbotapi.MessageConfig
	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			if msg, ok := c.(tgbotapi.MessageConfig); ok {
				capturedMsg = msg
			}
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	unicodeText := "Привет мир! 世界你好! 🎉"
	handler.SendMessage(ctx, 12345, unicodeText)

	assert.Equal(t, unicodeText, capturedMsg.Text, "Message text should preserve unicode")
}

// TestSendMessage_LongMessage tests SendMessage with a long message
func TestSendMessage_LongMessage(t *testing.T) {
	ctx := context.Background()

	var capturedMsg tgbotapi.MessageConfig
	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			if msg, ok := c.(tgbotapi.MessageConfig); ok {
				capturedMsg = msg
			}
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	// Create a long message
	longText := ""
	for i := 0; i < 1000; i++ {
		longText += "This is a test message. "
	}

	handler.SendMessage(ctx, 12345, longText)

	assert.Equal(t, longText, capturedMsg.Text, "Message text should preserve long text")
}

// TestSendMessage_MultipleMessages tests sending multiple messages
func TestSendMessage_MultipleMessages(t *testing.T) {
	ctx := context.Background()

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	// Send multiple messages
	for i := 0; i < 5; i++ {
		handler.SendMessage(ctx, 12345, "test message")
	}

	assert.Equal(t, 5, mockBot.SendCountSafe(), "Send should be called 5 times")
}

// TestSendMessage_DifferentChatIDs tests SendMessage with different chat IDs
func TestSendMessage_DifferentChatIDs(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name   string
		chatID int64
	}{
		{"positive chat ID", 12345},
		{"large chat ID", 999999999},
		{"negative chat ID", -1001234567890},
		{"zero chat ID", 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedMsg tgbotapi.MessageConfig
			mockBot := &testutil.MockBotAPI{
				SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
					if msg, ok := c.(tgbotapi.MessageConfig); ok {
						capturedMsg = msg
					}
					return tgbotapi.Message{MessageID: 999}, nil
				},
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
				TrafficLimitGB:   30,
			}

			handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

			handler.SendMessage(ctx, tc.chatID, "test message")

			assert.Equal(t, tc.chatID, capturedMsg.ChatID, "ChatID should match")
		})
	}
}

// TestSend_WithContextTimeout tests send with a context that times out
func TestSend_WithContextTimeout(t *testing.T) {
	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 123}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	// Create a context that's already timed out
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	msg := tgbotapi.NewMessage(12345, "test message")
	handler.send(ctx, msg)

	// With timed out context, the rate limiter should return false and not send
	assert.Equal(t, 0, mockBot.SendCountSafe(), "Send should not be called with timed out context")
}

// TestSafeSend_WithVariousChattables tests safeSend with different Chattable types
func TestSafeSend_WithVariousChattables(t *testing.T) {
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
			mockBot := &testutil.MockBotAPI{
				SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
					return tgbotapi.Message{MessageID: 999}, nil
				},
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
				TrafficLimitGB:   30,
			}

			handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

			handler.safeSend(tc.chattable)

			assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
			tc.checkFunc(t, mockBot.LastChattableSafe())
		})
	}
}

// TestSend_MultipleConcurrentSends tests concurrent send operations
func TestSend_MultipleConcurrentSends(t *testing.T) {
	ctx := context.Background()

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 123}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	// Send multiple messages concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			msg := tgbotapi.NewMessage(12345, "concurrent message")
			handler.send(ctx, msg)
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	assert.Equal(t, 10, mockBot.SendCountSafe(), "Send should be called 10 times")
}

// TestSendMessage_NilHandler tests that SendMessage doesn't panic with proper initialization
func TestSendMessage_NilHandler(t *testing.T) {
	ctx := context.Background()

	mockBot := &testutil.MockBotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			return tgbotapi.Message{MessageID: 999}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	// Should not panic
	handler.SendMessage(ctx, 12345, "test message")

	assert.Equal(t, 1, mockBot.SendCountSafe(), "Send should be called once")
}

// TestSend_WithMarkdownText tests send with markdown formatted text
func TestSend_WithMarkdownText(t *testing.T) {
	ctx := context.Background()

	var capturedMsg tgbotapi.MessageConfig
	mockBot := &testutil.MockBotAPI{
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
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, nil, nil, NewTestBotConfig(), nil)

	msg := tgbotapi.NewMessage(12345, "*bold* _italic_ `code`")
	msg.ParseMode = "Markdown"
	handler.send(ctx, msg)

	assert.Equal(t, "Markdown", capturedMsg.ParseMode, "ParseMode should be Markdown")
	assert.True(t, capturedMsg.DisableWebPagePreview, "Web page preview should be disabled")
}
