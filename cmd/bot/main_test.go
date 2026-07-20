package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/kereal/rs8kvn_bot/internal/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubVPNClient struct{}

func (s *stubVPNClient) CreateSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	return nil
}
func (s *stubVPNClient) UpdateSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	return nil
}
func (s *stubVPNClient) DeleteSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	return nil
}
func (s *stubVPNClient) Close() error { return nil }

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

func TestBuildRuntimeNodeClients_FiltersInactiveAndInitializes3xUIOnly(t *testing.T) {
	xuiCalls := make([]string, 0)
	vpnCalls := make([]vpn.Config, 0)
	opts := &runOptions{
		xuiClientFn: func(host, apiToken string) (interfaces.XUIClient, error) {
			xuiCalls = append(xuiCalls, host+"|"+apiToken)
			return &xui.Client{}, nil
		},
		vpnClientFn: func(cfg vpn.Config) (vpn.Client, error) {
			vpnCalls = append(vpnCalls, cfg)
			return &stubVPNClient{}, nil
		},
	}

	nodes := []database.Node{
		{ID: 1, Type: database.NodeType3xUI, IsActive: true, Host: "http://active-xui", APIToken: "token-a", InboundIDs: `[1]`},
		{ID: 2, Type: database.NodeTypeProxman, IsActive: false, Host: "http://inactive-prox", APIToken: "token-b", InboundIDs: `[2]`},
	}

	runtimeNodes, xuiClients, vpnClients, err := buildRuntimeNodeClients(nodes, opts)

	require.NoError(t, err)
	require.Len(t, runtimeNodes, 1)
	assert.Equal(t, uint(1), runtimeNodes[0].ID)
	assert.Len(t, xuiCalls, 1)
	assert.Len(t, vpnCalls, 1)
	assert.Len(t, xuiClients, 1)
	assert.Len(t, vpnClients, 1)
	assert.Equal(t, database.NodeType3xUI, vpnCalls[0].Type)
	assert.NotNil(t, vpnCalls[0].XUIClient)
}

func TestBuildRuntimeNodeClients_SkipsUnsupportedActiveNode(t *testing.T) {
	opts := &runOptions{
		xuiClientFn: func(host, apiToken string) (interfaces.XUIClient, error) {
			return &xui.Client{}, nil
		},
		vpnClientFn: func(cfg vpn.Config) (vpn.Client, error) {
			return &stubVPNClient{}, nil
		},
	}

	nodes := []database.Node{{ID: 7, Type: database.NodeTypeProxman, IsActive: true, Host: "http://prox", APIToken: "token", InboundIDs: `[1]`}}

	runtimeNodes, xuiClients, vpnClients, err := buildRuntimeNodeClients(nodes, opts)

	require.NoError(t, err)
	assert.Len(t, runtimeNodes, 1)
	assert.Equal(t, uint(7), runtimeNodes[0].ID)
	assert.Empty(t, xuiClients)
	assert.Len(t, vpnClients, 1)
	assert.NotNil(t, vpnClients[7])
}

func TestHandleUpdateSafely(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
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
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{}

	assert.NotPanics(t, func() {
		handler.HandleUpdate(ctx, update)
	})

	assert.False(t, mockBot.SendCalledSafe())
	assert.False(t, mockBot.RequestCalledSafe())
}

func TestConfigLoad_MissingRequiredFields(t *testing.T) {
	// Not parallel: uses os.Unsetenv which modifies global process state

	require.NoError(t, os.Unsetenv("TELEGRAM_BOT_TOKEN"))
	require.NoError(t, os.Unsetenv("DATABASE_PATH"))

	_, err := config.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TELEGRAM_BOT_TOKEN")
}

func TestConfigLoad_InvalidNumericValues(t *testing.T) {
	// Not parallel: t.Setenv cannot be used with t.Parallel

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "not_a_number")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("HEARTBEAT_INTERVAL", "negative")
	t.Setenv("WEB_SERVER_PORT", "not_a_port")
	t.Setenv("SITE_URL", "https://example.com")
	t.Setenv("TRIAL_DURATION_HOURS", "24")
	t.Setenv("TRIAL_RATE_LIMIT", "10")
	t.Setenv("CONTACT_USERNAME", "admin")
	_, err := config.Load()
	assert.Error(t, err)
}

func TestConfigLoad_InvalidURL(t *testing.T) {
	// Not parallel: t.Setenv cannot be used with t.Parallel

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456789")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("HEARTBEAT_INTERVAL", "50")
	t.Setenv("WEB_SERVER_PORT", "8080")
	t.Setenv("SITE_URL", "invalid-url")
	t.Setenv("TRIAL_DURATION_HOURS", "24")
	t.Setenv("TRIAL_RATE_LIMIT", "10")
	t.Setenv("CONTACT_USERNAME", "admin")

	_, err := config.Load()
	assert.Error(t, err)
}

func TestConfigLoad_InvalidPort(t *testing.T) {
	// Not parallel: t.Setenv cannot be used with t.Parallel

	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456789")
	t.Setenv("DATABASE_PATH", ":memory:")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("HEARTBEAT_INTERVAL", "50")
	t.Setenv("WEB_SERVER_PORT", "999999")
	t.Setenv("SITE_URL", "https://example.com")
	t.Setenv("TRIAL_DURATION_HOURS", "24")
	t.Setenv("TRIAL_RATE_LIMIT", "10")
	t.Setenv("CONTACT_USERNAME", "admin")

	_, err := config.Load()
	assert.Error(t, err)
}
