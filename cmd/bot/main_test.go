package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	_, _ = logger.Init("", "error")
	os.Exit(m.Run())
}

func TestGetVersion(t *testing.T) {
	t.Parallel()

	t.Run("returns non-empty string with correct prefix", func(t *testing.T) {
		v := getVersion()
		assert.NotEmpty(t, v)
		assert.True(t, strings.HasPrefix(v, "rs8kvn_bot@"))
		assert.Contains(t, v, "rs8kvn_bot@")
	})

	t.Run("commit variable accessible", func(t *testing.T) {
		if commit == "" {
			t.Log("commit is empty (expected in test environment)")
		}
	})

	t.Run("buildTime variable accessible", func(t *testing.T) {
		if buildTime == "" {
			t.Log("buildTime is empty (expected in test environment)")
		}
	})
}

func TestHandleUpdateSafely(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
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

// TestHandleUpdate_NilMessage тестирует обработку update с nil Message
func TestHandleUpdate_NilMessage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
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

// TestHandleUpdate_UnknownCommands тестирует обработку неизвестных команд
func TestHandleUpdate_UnknownCommands(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
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

// Skipped: requires proper test isolation for config.Load()
// func TestConfigLoad_ValidEnvVars(t *testing.T) { ... }
// func TestConfigLoad_InvalidNumericValues(t *testing.T) { ... }
// func TestConfigLoad_InvalidURL(t *testing.T) { ... }
// func TestConfigLoad_InvalidPort(t *testing.T) { ... }

func TestConfigLoad_MissingRequiredFields(t *testing.T) {
	// Not parallel: uses os.Unsetenv which modifies global process state

	require.NoError(t, os.Unsetenv("TELEGRAM_BOT_TOKEN"))
	require.NoError(t, os.Unsetenv("XUI_HOST"))
	require.NoError(t, os.Unsetenv("DATABASE_PATH"))

	_, err := config.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TELEGRAM_BOT_TOKEN")
}

func TestConfigLoad_InvalidNumericValues(t *testing.T) {
	// Not parallel: t.Setenv cannot be used with t.Parallel

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "not_a_number")
	t.Setenv("XUI_HOST", "http://localhost:2053")
	t.Setenv("XUI_API_TOKEN", "some-token")
	t.Setenv("XUI_INBOUND_ID", "invalid")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("HEARTBEAT_INTERVAL", "negative")
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
	// Not parallel: t.Setenv cannot be used with t.Parallel

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456789")
	t.Setenv("XUI_HOST", "not-a-valid-url")
	t.Setenv("XUI_API_TOKEN", "some-token")
	t.Setenv("XUI_INBOUND_ID", "1")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("HEARTBEAT_INTERVAL", "50")
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
	// Not parallel: t.Setenv cannot be used with t.Parallel

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456789")
	t.Setenv("XUI_HOST", "http://localhost:2053")
	t.Setenv("XUI_API_TOKEN", "some-token")
	t.Setenv("XUI_INBOUND_ID", "1")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("HEARTBEAT_INTERVAL", "50")
	t.Setenv("HEALTH_CHECK_PORT", "999999")
	t.Setenv("SITE_URL", "https://example.com")
	t.Setenv("TRIAL_DURATION_HOURS", "24")
	t.Setenv("TRIAL_RATE_LIMIT", "10")
	t.Setenv("CONTACT_USERNAME", "admin")
	t.Setenv("XUI_SUB_PATH", "xui")

	_, err := config.Load()
	assert.Error(t, err)
}
