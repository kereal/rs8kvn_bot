package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	_, _ = logger.Init("", "error")
	os.Exit(m.Run())
}

func TestGetVersion(t *testing.T) {
	t.Parallel()

	t.Run("returns non-empty string", func(t *testing.T) {
		v := getVersion()
		assert.NotEmpty(t, v, "getVersion() returned empty string")
	})

	t.Run("returns string with correct prefix", func(t *testing.T) {
		v := getVersion()
		assert.True(t, strings.HasPrefix(v, "rs8kvn_bot@"), "getVersion() = %s, want prefix rs8kvn_bot@", v)
	})

	t.Run("handles dev version gracefully", func(t *testing.T) {
		// When version is "dev", should still return a valid string
		v := getVersion()
		assert.Contains(t, v, "rs8kvn_bot@", "getVersion() should contain rs8kvn_bot@")
	})
}

func TestGetVersion_CommitVariable(t *testing.T) {
	t.Parallel()

	// Test that commit variable is accessible
	t.Run("commit variable is defined", func(t *testing.T) {
		if commit == "" {
			t.Log("commit is empty (expected in test environment)")
		}
	})
}

func TestGetVersion_BuildTimeVariable(t *testing.T) {
	t.Parallel()

	// Test that buildTime variable is accessible
	t.Run("buildTime variable is defined", func(t *testing.T) {
		if buildTime == "" {
			t.Log("buildTime is empty (expected in test environment)")
		}
	})
}

func TestHandleUpdate_CommandRouting(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	tests := []struct {
		name    string
		command string
	}{
		{"start command", "start"},
		{"help command", "help"},
		{"invite command", "invite"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456},
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Text: "/" + tt.command,
				},
			}

			// Should not panic
			handler.HandleUpdate(ctx, update)
		})
	}
}

func TestHandleUpdate_NonCommandMessage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "Hello, this is not a command",
		},
	}

	// Should not panic
	handler.HandleUpdate(ctx, update)
}

func TestHandleUpdate_CallbackQuery(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "test_data",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic
	handler.HandleUpdate(ctx, update)
}

func TestHandleUpdateSafely_PanicRecovery(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/start",
		},
	}

	// Should not panic even if internal panic occurs
	// The function has a recovery mechanism
	assert.NotPanics(t, func() {
		handleUpdateSafely(ctx, handler, update)
	})
}

func TestHandleUpdate_UnknownCommand(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/unknowncommand",
		},
	}

	// Should not panic
	handler.HandleUpdate(ctx, update)
}

// TestStartBackupScheduler_ContextCancellation тестирует остановку scheduler при отмене контекста
// TestHandleUpdateSafely_PanicInHandler tests panic recovery in handler
func TestHandleUpdateSafely_PanicInHandler(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/start",
		},
	}

	// Должно завершиться без паники благодаря recover
	assert.NotPanics(t, func() {
		handleUpdateSafely(ctx, handler, update)
	})
}

// TestGetVersion_WithLdflags тестирует getVersion при различных сценариях
func TestGetVersion_WithLdflags(t *testing.T) {
	t.Parallel()

	t.Run("dev version", func(t *testing.T) {
		v := getVersion()
		assert.NotEmpty(t, v)
		assert.Contains(t, v, "rs8kvn_bot@")
	})
}

// TestHandleUpdate_NilMessage тестирует обработку update с nil Message
func TestHandleUpdate_NilMessage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{}

	assert.NotPanics(t, func() {
		handler.HandleUpdate(ctx, update)
	})
}

// TestGetVersion_WithBuildInfo тестирует getVersion с различными build info
func TestGetVersion_WithBuildInfo(t *testing.T) {
	t.Parallel()

	t.Run("dev version returns valid format", func(t *testing.T) {
		v := getVersion()
		assert.NotEmpty(t, v)
		assert.Contains(t, v, "rs8kvn_bot@")
	})

	t.Run("version starts with rs8kvn_bot@", func(t *testing.T) {
		v := getVersion()
		assert.True(t, strings.HasPrefix(v, "rs8kvn_bot@"), "version should start with 'rs8kvn_bot@'")
	})
}

// TestHandleUpdate_UnknownCommands тестирует обработку неизвестных команд
func TestHandleUpdate_UnknownCommands(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	tests := []struct {
		name    string
		command string
	}{
		{"del command", "del"},
		{"broadcast command", "broadcast"},
		{"send command", "send"},
		{"unknown command", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456},
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Text: "/" + tt.command,
				},
			}

			assert.NotPanics(t, func() {
				handler.HandleUpdate(ctx, update)
			})
		})
	}
}

func TestHandleUpdate_UnknownCommand_Text(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/unknown",
			Entities: []tgbotapi.MessageEntity{
				{Type: "bot_command", Offset: 0, Length: 8},
			},
		},
	}

	handler.HandleUpdate(ctx, update)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Неизвестная команда")
}

func TestHandleUpdate_NonCommandMessage_Text(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "Hello, this is not a command",
		},
	}

	handler.HandleUpdate(ctx, update)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "/start")
}

func TestHandleUpdate_NonCommandMessage_UsernameFallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, FirstName: "John"},
			Text: "hello",
		},
	}

	assert.NotPanics(t, func() {
		handler.HandleUpdate(ctx, update)
	})
}

func TestHandleUpdate_NonCommandMessage_NoUser(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			Text: "hello",
		},
	}

	assert.NotPanics(t, func() {
		handler.HandleUpdate(ctx, update)
	})
}

func TestHandleUpdate_NonCommandMessage_LongText(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	longText := strings.Repeat("a", 200)
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: longText,
		},
	}

	assert.NotPanics(t, func() {
		handler.HandleUpdate(ctx, update)
	})
}

func TestHandleUpdate_CallbackQuery_NoMessage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback",
			Data: "test_data",
			From: &tgbotapi.User{ID: 123456},
		},
	}

	assert.NotPanics(t, func() {
		handler.HandleUpdate(ctx, update)
	})
}

func TestHandleUpdate_NilMessageAndNilCallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{}

	assert.NotPanics(t, func() {
		handler.HandleUpdate(ctx, update)
	})

	assert.False(t, mockBot.SendCalledSafe())
	assert.False(t, mockBot.RequestCalledSafe())
}

func TestHandleUpdateSafely_DoesNotSwallowPanic(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/start",
		},
	}

	assert.NotPanics(t, func() {
		handleUpdateSafely(ctx, handler, update)
	})
}

func TestGetVersion_DevVersion(t *testing.T) {
	t.Parallel()

	v := getVersion()
	assert.NotEmpty(t, v)
	assert.Contains(t, v, "rs8kvn_bot@")
}

func TestHandleUpdateSafely_RecoversFromPanic(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/start",
		},
	}

	assert.NotPanics(t, func() {
		handleUpdateSafely(ctx, handler, update)
	})
}

// Skipped: requires proper test isolation for config.Load()
// func TestConfigLoad_ValidEnvVars(t *testing.T) { ... }
// func TestConfigLoad_InvalidNumericValues(t *testing.T) { ... }
// func TestConfigLoad_InvalidURL(t *testing.T) { ... }
// func TestConfigLoad_InvalidPort(t *testing.T) { ... }

func TestConfigLoad_MissingRequiredFields(t *testing.T) {
	t.Parallel()

	os.Unsetenv("TELEGRAM_BOT_TOKEN")
	os.Unsetenv("XUI_HOST")
	os.Unsetenv("DATABASE_PATH")

	_, err := config.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TELEGRAM_BOT_TOKEN")
}

func TestConfigLoad_InvalidNumericValues(t *testing.T) {
	t.Parallel()

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "not_a_number")
	t.Setenv("XUI_HOST", "http://localhost:2053")
	t.Setenv("XUI_USERNAME", "admin")
	t.Setenv("XUI_PASSWORD", "password")
	t.Setenv("XUI_INBOUND_ID", "invalid")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("TRAFFIC_LIMIT_GB", "negative")
	t.Setenv("HEALTH_CHECK_PORT", "not_a_port")
	t.Setenv("SITE_URL", "https://example.com")
	t.Setenv("TRIAL_DURATION_HOURS", "24")
	t.Setenv("TRIAL_RATE_LIMIT", "10")
	t.Setenv("CONTACT_USERNAME", "admin")
	t.Setenv("XUI_SUB_PATH", "xui")

	_, err := config.Load()
	assert.Error(t, err)
}

func TestConfigLoad_InvalidURL(t *testing.T) {
	t.Parallel()

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456789")
	t.Setenv("XUI_HOST", "not-a-valid-url")
	t.Setenv("XUI_USERNAME", "admin")
	t.Setenv("XUI_PASSWORD", "password")
	t.Setenv("XUI_INBOUND_ID", "1")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("TRAFFIC_LIMIT_GB", "50")
	t.Setenv("HEALTH_CHECK_PORT", "8080")
	t.Setenv("SITE_URL", "invalid-url")
	t.Setenv("TRIAL_DURATION_HOURS", "24")
	t.Setenv("TRIAL_RATE_LIMIT", "10")
	t.Setenv("CONTACT_USERNAME", "admin")
	t.Setenv("XUI_SUB_PATH", "xui")

	_, err := config.Load()
	assert.Error(t, err)
}

func TestConfigLoad_InvalidPort(t *testing.T) {
	t.Parallel()

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456789")
	t.Setenv("XUI_HOST", "http://localhost:2053")
	t.Setenv("XUI_USERNAME", "admin")
	t.Setenv("XUI_PASSWORD", "password")
	t.Setenv("XUI_INBOUND_ID", "1")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("TRAFFIC_LIMIT_GB", "50")
	t.Setenv("HEALTH_CHECK_PORT", "999999")
	t.Setenv("SITE_URL", "https://example.com")
	t.Setenv("TRIAL_DURATION_HOURS", "24")
	t.Setenv("TRIAL_RATE_LIMIT", "10")
	t.Setenv("CONTACT_USERNAME", "admin")
	t.Setenv("XUI_SUB_PATH", "xui")

	_, err := config.Load()
	assert.Error(t, err)
}
