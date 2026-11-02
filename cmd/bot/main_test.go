package main

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
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
func (s *stubVPNClient) ResetTraffic(_ context.Context, _ vpn.SubscriptionProvision) error {
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
		{ID: 1, Name: "primary-eu", Type: database.NodeType3xUI, IsActive: true, Host: "http://active-xui", APIToken: "token-a", InboundIDs: `[1]`},
		{ID: 2, Type: database.NodeTypeProxman, IsActive: false, Host: "http://inactive-prox", APIToken: "token-b", InboundIDs: `[2]`},
	}

	runtimeNodes, clients, err := buildRuntimeNodeClients(nodes, opts)

	require.NoError(t, err)
	require.Len(t, runtimeNodes, 1)
	assert.Equal(t, uint(1), runtimeNodes[0].ID)
	assert.Len(t, xuiCalls, 1)
	assert.Len(t, vpnCalls, 1)
	assert.Len(t, clients, 1)
	assert.NotNil(t, clients[1].Provision)
	assert.NotNil(t, clients[1].Reader)
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

	runtimeNodes, clients, err := buildRuntimeNodeClients(nodes, opts)

	require.NoError(t, err)
	assert.Len(t, runtimeNodes, 1)
	assert.Equal(t, uint(7), runtimeNodes[0].ID)
	assert.Len(t, clients, 1)
	assert.NotNil(t, clients[7].Provision)
	assert.Nil(t, clients[7].Reader)
}

// TestHandleUpdateSafely: паника в deps (бот-клиенте) не должна пробивать
// handleUpdateSafely — recoverAndReport глотает её, а обработчик остаётся
// жив и обрабатывает следующий апдейт. По образцу
// TestRunSupervised_RestartsAfterPanic (cmd/bot/supervisor_test.go).
func TestHandleUpdateSafely(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	// Бот, паникующий при первой отправке: паника из deps внутри
	// HandleUpdate должна быть поглощена recoverAndReport (она же пишет
	// лог через logger.Error; отдельного счётчика у обработчика нет —
	// метрика WorkerRestartsTotal ведётся только supervisor'ом).
	panickingBot := testutil.NewBotAPI()
	var sendCalled atomic.Bool
	panickingBot.SendFunc = func(tgbotapi.Chattable) (tgbotapi.Message, error) {
		sendCalled.Store(true)
		panic("boom in bot client")
	}

	healthyBot := testutil.NewBotAPI()

	handler := bot.NewHandler(panickingBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")

	commandUpdate := func(text string, cmdLen int) tgbotapi.Update {
		return tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: 123456},
				From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
				Text: text,
				Entities: []tgbotapi.MessageEntity{
					{Type: "bot_command", Offset: 0, Length: cmdLen},
				},
			},
		}
	}

	// 1. Паника deps не пробивает наружу.
	assert.NotPanics(t, func() {
		handleUpdateSafely(context.Background(), handler, commandUpdate("/unknown", 8))
	})
	assert.True(t, sendCalled.Load(), "паникующий deps должен был вызваться")

	// 2. Выживание: после паники обработчик продолжает работать.
	handler.SetBot(healthyBot)
	assert.NotPanics(t, func() {
		handleUpdateSafely(context.Background(), handler, commandUpdate("/start", 6))
	})
	assert.True(t, healthyBot.SendCalledSafe(), "после паники обработчик должен отвечать на команды")
}

// TestHandleUpdate_UnknownCommand / _UnknownCommands / _UnknownCommand_Text
// удалены: покрытие в internal/bot TestHandleUpdate_CommandRouting (кейс
// "unknown command" ассертит отправку «Неизвестная команда»).

// TestHandleUpdate_NilMessage удалён: слит с TestHandleUpdate_NilMessageAndNilCallback,
// который покрывает тот же апдейт (nil Message) плюс отсутствие побочных вызовов.

func TestHandleUpdate_NonCommandMessage_UsernameFallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()

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
	mockDB := testutil.NewBotStoreFake()

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

// TestHandleUpdate_NonCommandMessage_LongText удалён: единственный assert —
// NotPanics, уникального покрытия нет (текст не отличается поведенчески;
// ответ на non-command покрывает internal/bot TestHandleUpdate_NonCommandMessage).

func TestHandleUpdate_CallbackQuery_NoMessage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()

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
	mockDB := testutil.NewBotStoreFake()

	handler := bot.NewHandler(mockBot, cfg, mockDB, bot.NewTestBotConfig(), nil, "")
	ctx := context.Background()

	update := tgbotapi.Update{}

	assert.NotPanics(t, func() {
		handler.HandleUpdate(ctx, update)
	})

	assert.False(t, mockBot.SendCalledSafe())
	assert.False(t, mockBot.RequestCalledSafe())
}

// TestConfigLoad_MissingRequiredFields / _InvalidNumericValues / _InvalidURL /
// _InvalidPort удалены: дубли покрытия internal/config (config_test.go:
// TestConfig_Validate_*, TestValidateURL) — та же конфигурация и те же
// ветки validate(), только через os.Setenv.
