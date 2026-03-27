package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/xui"
)

type TestFixture struct {
	DB          *database.Service
	XUIServer   *httptest.Server
	XUIClient   *xui.Client
	Handler     *Handler
	Cfg         *config.Config
	Ctx         context.Context
	Cancel      context.CancelFunc
	AdminChatID int64
	UserChatID  int64
}

func NewTestFixture(t *testing.T) *TestFixture {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	dbService, err := database.NewService(":memory:")
	if err != nil {
		t.Fatalf("Failed to create in-memory database: %v", err)
	}

	cfg := &config.Config{
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
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

	xuiClient, err := xui.NewClient(cfg.XUIHost, cfg.XUIUsername, cfg.XUIPassword)
	if err != nil {
		t.Fatalf("Failed to create XUI client: %v", err)
	}

	handler := NewHandler(nil, cfg, dbService, xuiClient)

	return &TestFixture{
		DB:          dbService,
		XUIClient:   xuiClient,
		Handler:     handler,
		Cfg:         cfg,
		Ctx:         ctx,
		Cancel:      cancel,
		AdminChatID: 123456789,
		UserChatID:  987654321,
	}
}

func (f *TestFixture) Close() {
	f.Cancel()
	if f.DB != nil {
		_ = f.DB.Close()
	}
	if f.XUIServer != nil {
		f.XUIServer.Close()
	}
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
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})

	mux.HandleFunc("/panel/api/inbounds/addClient", func(w http.ResponseWriter, r *http.Request) {
		if mock.AddClientErr != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"msg":     mock.AddClientErr.Error(),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"msg":     "ok",
		})
	})

	mux.HandleFunc("/panel/api/inbounds/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if path == "/panel/api/inbounds/getClientTraffics/testuser" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"obj":     mock.TrafficResp,
			})
			return
		}

		if r.Method == "POST" && contains(path, "delClient") {
			if mock.DeleteErr != nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"msg":     mock.DeleteErr.Error(),
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
		}
	})

	return mock
}

func (m *MockXUIServer) Close() {
	m.Server.Close()
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (len(s) == 0 ||
		(len(s) > 0 && (s[:len(substr)] == substr || contains(s[1:], substr))))
}

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

func NewIntegrationTestFixture(t *testing.T) *IntegrationTestFixture {
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

	handler := NewHandler(nil, cfg, dbService, mockXUI.Client)

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
	f := NewIntegrationTestFixture(t)
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
	f := NewIntegrationTestFixture(t)
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
	f := NewIntegrationTestFixture(t)
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
	f := NewIntegrationTestFixture(t)
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
	f := NewIntegrationTestFixture(t)
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
	f := NewIntegrationTestFixture(t)
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
