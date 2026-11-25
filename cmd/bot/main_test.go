package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// stubTelegramHTTP — заглушка HTTP-клиента Telegram: считает запросы и
// отвечает canned-ответом (getMe — валидный бот, остальное — ok).
type stubTelegramHTTP struct {
	mu       sync.Mutex
	requests []string
}

func (s *stubTelegramHTTP) Do(req *http.Request) (*http.Response, error) {
	body := `{"ok":true,"result":true}`
	if strings.Contains(req.URL.Path, "getMe") {
		body = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"t","username":"tbot"}}`
	}

	s.mu.Lock()
	s.requests = append(s.requests, req.URL.Path)
	s.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func (s *stubTelegramHTTP) countPath(fragment string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, path := range s.requests {
		if strings.Contains(path, fragment) {
			count++
		}
	}

	return count
}

// TestNewPreCheckoutAnswerer_AnswersEveryQuery — ответ на pre_checkout_query
// обязателен: переполнение очереди ответов не должно приводить к пропуску
// ответа (иначе запрос висит до истечения 10-секундного окна Telegram).
func TestNewPreCheckoutAnswerer_AnswersEveryQuery(t *testing.T) {
	t.Parallel()

	stub := &stubTelegramHTTP{}
	api, err := tgbotapi.NewBotAPIWithClient("test-token", tgbotapi.APIEndpoint, stub)
	require.NoError(t, err)

	answer := newPreCheckoutAnswerer(func() (interfaces.BotAPI, error) { return api, nil })

	const queries = preCheckoutAnswerMaxConcurrent * 3
	for i := range queries {
		answer(fmt.Sprintf("query-%d", i))
	}

	require.Eventually(t, func() bool {
		return stub.countPath("answerPreCheckoutQuery") == queries
	}, 3*time.Second, 10*time.Millisecond, "every pre_checkout query must get an answer")
}

// TestNewPreCheckoutAnswerer_UsesCurrentClientAfterRecovery — fallback обязан
// резолвить payment-клиент при каждом ответе: если инстанс поднялся после старта
// (фоновый ретрай через handler.SetPaymentBot), pre_checkout_query всё равно
// получает ответ; до восстановления — fail-closed без сетевого обращения.
func TestNewPreCheckoutAnswerer_UsesCurrentClientAfterRecovery(t *testing.T) {
	t.Parallel()

	stub := &stubTelegramHTTP{}
	api, err := tgbotapi.NewBotAPIWithClient("test-token", tgbotapi.APIEndpoint, stub)
	require.NoError(t, err)

	var (
		mu      sync.Mutex
		current interfaces.BotAPI
	)

	answer := newPreCheckoutAnswerer(func() (interfaces.BotAPI, error) {
		mu.Lock()
		defer mu.Unlock()

		if current == nil {
			return nil, errors.New("bot: payment API is not configured")
		}

		return current, nil
	})

	answer("query-before-recovery")

	require.Never(t, func() bool {
		return stub.countPath("answerPreCheckoutQuery") > 0
	}, 200*time.Millisecond, 10*time.Millisecond, "degraded payment API must stay fail-closed")

	mu.Lock()
	current = api
	mu.Unlock()

	answer("query-after-recovery")

	require.Eventually(t, func() bool {
		return stub.countPath("answerPreCheckoutQuery") == 1
	}, 3*time.Second, 10*time.Millisecond, "recovered client must receive the answer")
}

// TestPreCheckoutAnswerBudgetFitsTelegramWindow — инвариант: ответ на
// pre-checkout_query обязан уложиться в 10-секундное окно Telegram.
func TestPreCheckoutAnswerBudgetFitsTelegramWindow(t *testing.T) {
	t.Parallel()

	total := preCheckoutSlotWaitTimeout + bot.PreCheckoutTimeout + paymentClientTimeout
	require.LessOrEqual(t, total, 10*time.Second,
		"the pre-checkout answer must fit the Telegram 10s window")
}

// Тест поведения: платёжный Telegram-инстанс (Stars) поднимается только когда
// Stars реально нужен — активный провайдер или старые Stars-заказы (возвраты
// `/refund` после переключения на Platega). Иначе второй инстанс не создаётся.
func TestStarsClientNeeded(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		hasOrders bool
		ordersErr error
		want      bool
	}{
		{name: "stars provider without orders", provider: database.PaymentProviderTelegramStars, want: true},
		{name: "platega with stars orders", provider: database.PaymentProviderPlatega, hasOrders: true, want: true},
		{name: "platega without stars orders", provider: database.PaymentProviderPlatega, want: false},
		{name: "db error keeps stars client", provider: database.PaymentProviderPlatega, ordersErr: errors.New("db down"), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &testutil.ServiceStoreFake{
				HasOrdersByProviderFunc: func(context.Context, string) (bool, error) {
					return tt.hasOrders, tt.ordersErr
				},
			}

			assert.Equal(t, tt.want, starsClientNeeded(&config.Config{PaymentProvider: tt.provider}, db))
		})
	}
}

// TestNewPaymentBotAPI_RetriesTransientFailure — транзиентная ошибка Telegram
// при создании payment-инстанса не должна ронять старт бота: основной платёж
// Platega работает через webhook и от Telegram-инстанса не зависит.
func TestNewPaymentBotAPI_RetriesTransientFailure(t *testing.T) {
	original := newPaymentBotAPIFactory
	t.Cleanup(func() { newPaymentBotAPIFactory = original })

	var attempts int

	newPaymentBotAPIFactory = func(token string, client tgbotapi.HTTPClient) (*tgbotapi.BotAPI, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("temporary telegram failure")
		}

		return &tgbotapi.BotAPI{Token: token, Client: client}, nil
	}

	api, err := newPaymentBotAPIWithDelay("test-token", 0)
	require.NoError(t, err)
	require.NotNil(t, api)
	assert.Equal(t, 3, attempts, "payment client creation must be retried")
}

// TestNewPaymentBotAPI_UsesPaymentTimeout — после аутентификации инстанс
// работает с коротким бюджетом платёжных вызовов (окно Telegram 10 c).
func TestNewPaymentBotAPI_UsesPaymentTimeout(t *testing.T) {
	original := newPaymentBotAPIFactory
	t.Cleanup(func() { newPaymentBotAPIFactory = original })

	var authTimeout time.Duration

	newPaymentBotAPIFactory = func(token string, client tgbotapi.HTTPClient) (*tgbotapi.BotAPI, error) {
		if httpClient, ok := client.(*http.Client); ok {
			authTimeout = httpClient.Timeout
		}

		return &tgbotapi.BotAPI{Token: token, Client: client}, nil
	}

	api, err := newPaymentBotAPIWithDelay("test-token", 0)
	require.NoError(t, err)
	require.NotNil(t, api)

	assert.Equal(t, paymentAuthTimeout, authTimeout,
		"getMe must not be bound by the 4s payment budget")

	httpClient, ok := api.Client.(*http.Client)
	require.True(t, ok, "payment client must use an *http.Client")
	assert.Equal(t, paymentClientTimeout, httpClient.Timeout)
}
