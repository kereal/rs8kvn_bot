package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/xui"
)

type IntegrationTestFixture struct {
	DB          *database.Service
	XUIServer   *MockXUIServer
	XUIClient   *xui.Client
	Handler     *Handler
	Cfg         *config.Config
	Ctx         context.Context
	Cancel      context.CancelFunc
	AdminChatID int64
	UserChatID  int64
}

type MockXUIServer struct {
	Server       *httptest.Server
	Client       *xui.Client
	AddClientErr error
	DeleteErr    error
	TrafficResp  *xui.ClientTraffic
}

func NewMockXUIServer(t *testing.T) *MockXUIServer {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	client, err := xui.NewClient(server.URL, "admin", "password")
	if err != nil {
		t.Fatalf("Failed to create XUI client: %v", err)
	}

	mock := &MockXUIServer{
		Server: server,
		Client: client,
		TrafficResp: &xui.ClientTraffic{
			Up:   1024 * 1024 * 100,
			Down: 1024 * 1024 * 200,
		},
	}

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"success": true})
	})

	mux.HandleFunc("/panel/api/inbounds/addClient", func(w http.ResponseWriter, r *http.Request) {
		if mock.AddClientErr != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"msg":     mock.AddClientErr.Error(),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"obj":     map[string]any{"id": "test-client-id"},
		})
	})

	mux.HandleFunc("/panel/api/inbounds/getClientTraffics/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"obj":     mock.TrafficResp,
		})
	})

	mux.HandleFunc("/panel/api/inbounds/delClient/", func(w http.ResponseWriter, r *http.Request) {
		if mock.DeleteErr != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"msg":     mock.DeleteErr.Error(),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"success": true})
	})

	return mock
}

func (m *MockXUIServer) Close() {
	m.Server.Close()
}

func NewTestFixture(t *testing.T) *IntegrationTestFixture {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	dbService, err := database.NewService(":memory:")
	if err != nil {
		t.Fatalf("Failed to create in-memory database: %v", err)
	}

	mockXUI := NewMockXUIServer(t)

	cfg := &config.Config{
		TrafficLimitGB:   100,
		XUIHost:          mockXUI.Server.URL,
		XUIInboundID:     1,
		XUISubPath:       "sub",
		XUIUsername:      "admin",
		XUIPassword:      "password",
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
		LogFilePath:      "/dev/null",
		LogLevel:         "error",
		DatabasePath:     ":memory:",
	}

	handler := NewHandler(testutil.NewMockBotAPI(), cfg, dbService, mockXUI.Client, NewTestBotConfig())

	return &IntegrationTestFixture{
		DB:          dbService,
		XUIServer:   mockXUI,
		XUIClient:   mockXUI.Client,
		Handler:     handler,
		Cfg:         cfg,
		Ctx:         ctx,
		Cancel:      cancel,
		AdminChatID: 123456789,
		UserChatID:  987654321,
	}
}

func (f *IntegrationTestFixture) Close() {
	f.Cancel()
	if f.DB != nil {
		_ = f.DB.Close()
	}
	if f.XUIServer != nil {
		f.XUIServer.Close()
	}
}

func CreateTestSubscriptionInDB(t *testing.T, db *database.Service, chatID int64, username string, status string, expiry time.Time) *database.Subscription {
	t.Helper()

	sub := &database.Subscription{
		TelegramID:      chatID,
		Username:        username,
		ClientID:        utils.GenerateUUID(),
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      expiry,
		Status:          status,
		SubscriptionURL: "http://localhost/sub/" + username,
	}

	err := db.CreateSubscription(context.Background(), sub)
	if err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	return sub
}

func TestSubscriptionFlow_CreateAndGet(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()

	sub, err := f.DB.GetByTelegramID(ctx, f.UserChatID)
	if err == nil {
		t.Fatalf("Expected no subscription, got: %v", sub)
	}

	activeSub := CreateTestSubscriptionInDB(t, f.DB, f.UserChatID, "testuser", "active", time.Now().Add(30*24*time.Hour))

	retrieved, err := f.DB.GetByTelegramID(ctx, f.UserChatID)
	if err != nil {
		t.Fatalf("Failed to get subscription: %v", err)
	}

	if retrieved.TelegramID != activeSub.TelegramID {
		t.Errorf("TelegramID = %d, want %d", retrieved.TelegramID, activeSub.TelegramID)
	}

	if retrieved.Status != "active" {
		t.Errorf("Status = %s, want active", retrieved.Status)
	}
}

func TestSubscriptionFlow_ExpiredSubscription(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()

	CreateTestSubscriptionInDB(t, f.DB, f.UserChatID, "testuser", "active", time.Now().Add(-24*time.Hour))

	sub, err := f.DB.GetByTelegramID(ctx, f.UserChatID)
	if err != nil {
		t.Fatalf("Failed to get subscription: %v", err)
	}

	if !sub.IsExpired() {
		t.Error("Expected subscription to be expired")
	}

	if sub.IsActive() {
		t.Error("Expected subscription to not be active")
	}
}

func TestSubscriptionFlow_RevokeOldSubscription(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()

	CreateTestSubscriptionInDB(t, f.DB, f.UserChatID, "testuser1", "active", time.Now().Add(30*24*time.Hour))

	err := f.DB.CreateSubscription(ctx, &database.Subscription{
		TelegramID:      f.UserChatID,
		Username:        "testuser2",
		ClientID:        utils.GenerateUUID(),
		SubscriptionID:  "testuser2",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(30 * 24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/testuser2",
	})
	if err != nil {
		t.Fatalf("Failed to create new subscription: %v", err)
	}

	subs, err := f.DB.GetLatestSubscriptions(ctx, 10)
	if err != nil {
		t.Fatalf("Failed to get subscriptions: %v", err)
	}

	var activeCount int
	for _, s := range subs {
		if s.TelegramID == f.UserChatID && s.Status == "active" {
			activeCount++
		}
	}

	if activeCount != 1 {
		t.Errorf("Expected 1 active subscription, got %d", activeCount)
	}
}

func TestAdminStats(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()

	CreateTestSubscriptionInDB(t, f.DB, 111, "user1", "active", time.Now().Add(30*24*time.Hour))
	CreateTestSubscriptionInDB(t, f.DB, 222, "user2", "active", time.Now().Add(30*24*time.Hour))
	CreateTestSubscriptionInDB(t, f.DB, 333, "user3", "revoked", time.Now().Add(-24*time.Hour))

	allSubs, err := f.DB.GetAllSubscriptions(ctx)
	if err != nil {
		t.Fatalf("Failed to get all subscriptions: %v", err)
	}

	if len(allSubs) != 3 {
		t.Errorf("Expected 3 subscriptions, got %d", len(allSubs))
	}

	activeCount, err := f.DB.CountActiveSubscriptions(ctx)
	if err != nil {
		t.Fatalf("Failed to count active subscriptions: %v", err)
	}

	if activeCount != 2 {
		t.Errorf("Expected 2 active subscriptions, got %d", activeCount)
	}
}

func TestDatabaseService_GetAllTelegramIDs(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()

	CreateTestSubscriptionInDB(t, f.DB, 111, "user1", "active", time.Now().Add(30*24*time.Hour))
	CreateTestSubscriptionInDB(t, f.DB, 222, "user2", "active", time.Now().Add(30*24*time.Hour))
	CreateTestSubscriptionInDB(t, f.DB, 333, "user3", "revoked", time.Now().Add(-24*time.Hour))

	ids, err := f.DB.GetAllTelegramIDs(ctx)
	if err != nil {
		t.Fatalf("Failed to get all telegram IDs: %v", err)
	}

	if len(ids) != 3 {
		t.Errorf("Expected 3 telegram IDs, got %d", len(ids))
	}
}

func TestDatabaseService_GetByUsername(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()

	CreateTestSubscriptionInDB(t, f.DB, 111, "testuser", "active", time.Now().Add(30*24*time.Hour))

	id, err := f.DB.GetTelegramIDByUsername(ctx, "testuser")
	if err != nil {
		t.Fatalf("Failed to get telegram ID by username: %v", err)
	}

	if id != 111 {
		t.Errorf("Expected telegram ID 111, got %d", id)
	}

	_, err = f.DB.GetTelegramIDByUsername(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent username")
	}
}

func TestHandler_GetMainMenuContent_Admin(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	text, keyboard := f.Handler.getMainMenuContent("testuser", true, f.AdminChatID)

	assert.Contains(t, text, "testuser")
	assert.NotEmpty(t, keyboard.InlineKeyboard)
}

func TestHandler_GetMainMenuContent_User(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	text, keyboard := f.Handler.getMainMenuContent("testuser", false, f.UserChatID)

	assert.Contains(t, text, "testuser")
	assert.NotEmpty(t, keyboard.InlineKeyboard)
}

func TestHandler_GetDonateText(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	text := f.Handler.getDonateText()
	assert.NotEmpty(t, text)
}

func TestHandler_GetHelpText(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	text := f.Handler.getHelpText(100, "https://example.com/sub")
	assert.Contains(t, text, "100")
	assert.Contains(t, text, "Happ")
}

func TestHandler_GetUsername(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	tests := []struct {
		name string
		user *tgbotapi.User
		want string
	}{
		{"with username", &tgbotapi.User{UserName: "testuser"}, "testuser"},
		{"first name only", &tgbotapi.User{FirstName: "Test"}, "Test"},
		{"both username and first", &tgbotapi.User{UserName: "testuser", FirstName: "Test"}, "testuser"},
		{"empty user", &tgbotapi.User{ID: 0}, "user_0"},
		{"nil user", nil, "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := f.Handler.getUsername(tc.user)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMockXUIServer_Endpoints(t *testing.T) {
	mock := NewMockXUIServer(t)
	defer mock.Close()

	t.Run("login", func(t *testing.T) {
		resp, err := http.Get(mock.Server.URL + "/login")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		var result map[string]any
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.True(t, result["success"].(bool))
	})

	t.Run("addClient", func(t *testing.T) {
		resp, err := http.Post(mock.Server.URL+"/panel/api/inbounds/addClient", "application/json", nil)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		var result map[string]any
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.True(t, result["success"].(bool))
	})

	t.Run("getClientTraffics", func(t *testing.T) {
		resp, err := http.Get(mock.Server.URL + "/panel/api/inbounds/getClientTraffics/testuser")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		var result map[string]any
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.True(t, result["success"].(bool))

		obj := result["obj"].(map[string]any)
		assert.Equal(t, float64(1024*1024*100), obj["up"])
		assert.Equal(t, float64(1024*1024*200), obj["down"])
	})

	t.Run("delClient", func(t *testing.T) {
		resp, err := http.Post(mock.Server.URL+"/panel/api/inbounds/delClient/test-id", "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		var result map[string]any
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.True(t, result["success"].(bool))
	})
}

func TestMockXUIServer_ErrorResponses(t *testing.T) {
	mock := NewMockXUIServer(t)
	defer mock.Close()

	t.Run("addClient error", func(t *testing.T) {
		mock.AddClientErr = assert.AnError

		resp, err := http.Post(mock.Server.URL+"/panel/api/inbounds/addClient", "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		var result map[string]any
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.False(t, result["success"].(bool))
		assert.Equal(t, assert.AnError.Error(), result["msg"])
	})

	t.Run("delClient error", func(t *testing.T) {
		mock.DeleteErr = assert.AnError

		resp, err := http.Post(mock.Server.URL+"/panel/api/inbounds/delClient/test-id", "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		var result map[string]any
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.False(t, result["success"].(bool))
		assert.Equal(t, assert.AnError.Error(), result["msg"])
	})
}

func resetMockBotAPI(m *testutil.MockBotAPI) {
	m.SendCalled = false
	m.RequestCalled = false
	m.LastSentText = ""
	m.LastChatID = 0
	m.SendCount = 0
	m.SendError = nil
	m.RequestError = nil
}

// ==================== Additional Integration Tests ====================

func TestIntegration_HandleStart_NoSubscription(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()
	resetMockBotAPI(f.Handler.bot.(*testutil.MockBotAPI))

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: f.UserChatID},
			From: &tgbotapi.User{
				ID:       f.UserChatID,
				UserName: "testuser",
			},
			Text:     "/start",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 6}},
		},
	}
	f.Handler.HandleStart(ctx, update)

	assert.True(t, f.Handler.bot.(*testutil.MockBotAPI).SendCalled)
}

func TestIntegration_HandleStart_WithSubscription(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()
	CreateTestSubscriptionInDB(t, f.DB, f.UserChatID, "testuser", "active", time.Now().Add(30*24*time.Hour))
	resetMockBotAPI(f.Handler.bot.(*testutil.MockBotAPI))

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: f.UserChatID},
			From: &tgbotapi.User{
				ID:       f.UserChatID,
				UserName: "testuser",
			},
			Text:     "/start",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 6}},
		},
	}
	f.Handler.HandleStart(ctx, update)

	assert.True(t, f.Handler.bot.(*testutil.MockBotAPI).SendCalled)
}

func TestIntegration_HandleHelp(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()
	resetMockBotAPI(f.Handler.bot.(*testutil.MockBotAPI))

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: f.UserChatID},
			From: &tgbotapi.User{
				ID:       f.UserChatID,
				UserName: "testuser",
			},
			Text:     "/help",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	f.Handler.HandleHelp(ctx, update)

	assert.True(t, f.Handler.bot.(*testutil.MockBotAPI).SendCalled)
}

func TestIntegration_HandleInvite(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()
	resetMockBotAPI(f.Handler.bot.(*testutil.MockBotAPI))

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: f.UserChatID},
			From: &tgbotapi.User{
				ID:       f.UserChatID,
				UserName: "testuser",
			},
			Text:     "/invite",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 7}},
		},
	}
	f.Handler.HandleInvite(ctx, update)

	assert.True(t, f.Handler.bot.(*testutil.MockBotAPI).SendCalled)
}

func TestIntegration_Callback_CreateSubscription(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()
	resetMockBotAPI(f.Handler.bot.(*testutil.MockBotAPI))

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       f.UserChatID,
				UserName: "testuser",
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: f.UserChatID},
				MessageID: 100,
			},
		},
	}
	f.Handler.HandleCallback(ctx, update)

	assert.True(t, f.Handler.bot.(*testutil.MockBotAPI).SendCalled)
}

func TestIntegration_Callback_MenuSubscription(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	CreateTestSubscriptionInDB(t, f.DB, f.UserChatID, "testuser", "active", time.Now().Add(30*24*time.Hour))
	resetMockBotAPI(f.Handler.bot.(*testutil.MockBotAPI))

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       f.UserChatID,
				UserName: "testuser",
			},
			Data: "menu_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: f.UserChatID},
				MessageID: 100,
			},
		},
	}
	f.Handler.HandleCallback(ctx, update)

	assert.True(t, f.Handler.bot.(*testutil.MockBotAPI).SendCalled)
}

func TestIntegration_Callback_QRCode(t *testing.T) {
	f := NewTestFixture(t)
	defer f.Close()

	CreateTestSubscriptionInDB(t, f.DB, f.UserChatID, "testuser", "active", time.Now().Add(30*24*time.Hour))
	resetMockBotAPI(f.Handler.bot.(*testutil.MockBotAPI))

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       f.UserChatID,
				UserName: "testuser",
			},
			Data: "qr_code",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: f.UserChatID},
				MessageID: 100,
			},
		},
	}
	f.Handler.HandleCallback(ctx, update)

	assert.True(t, f.Handler.bot.(*testutil.MockBotAPI).SendCalled)
}
