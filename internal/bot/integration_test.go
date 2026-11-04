package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/kereal/rs8kvn_bot/internal/xui"
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

	client, err := xui.NewClient(server.URL, "test-api-token")
	if err != nil {
		t.Fatalf("Failed to create XUI client: %v", err)
	}

	expectedToken := "Bearer test-api-token"
	requireAuth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != expectedToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"msg":"unauthorized"}`))

			return false
		}

		return true
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

	mux.HandleFunc("/panel/api/clients/add", func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth(w, r) {
			return
		}

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

	mux.HandleFunc("/panel/api/clients/traffic/", func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth(w, r) {
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"obj":     mock.TrafficResp,
		})
	})

	mux.HandleFunc("/panel/api/clients/del/", func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth(w, r) {
			return
		}

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
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
		LogFilePath:      "/dev/null",
		LogLevel:         "error",
		DatabasePath:     ":memory:",
	}

	handler := NewHandler(testutil.NewBotAPI(), cfg, dbService, NewTestBotConfig(), nil, "")
	clients := service.NodeClients{1: {Reader: mockXUI.Client}}
	nodes := []database.Node{{ID: 1, Name: "main", IsActive: true, Host: mockXUI.Server.URL, APIToken: "test-api-token", InboundIDs: "[1]"}}
	subService := service.NewSubscriptionService(dbService, clients, nodes, cfg)
	handler.subscriptionService = subService
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

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

func CreateTestSubscriptionInDB(t *testing.T, db *database.Service, chatID int64, username string, status string, expiry *time.Time) *database.Subscription {
	t.Helper()

	clientID, err := utils.GenerateUUID()
	if err != nil {
		t.Fatalf("Failed to generate client ID: %v", err)
	}

	subscriptionID, err := utils.GenerateUUID()
	if err != nil {
		t.Fatalf("Failed to generate subscription ID: %v", err)
	}

	// The test DSN enables SQLite foreign-key enforcement: reference the seeded
	// free plan so the insert is valid.
	plan, planErr := db.GetPlanByName(context.Background(), database.FreePlanName)
	if planErr != nil {
		t.Fatalf("Failed to resolve free plan: %v", planErr)
	}

	sub := &database.Subscription{
		TelegramID:     chatID,
		Username:       username,
		ClientID:       clientID,
		SubscriptionID: subscriptionID,
		ExpiresAt:      expiry,
		Status:         status,
		PlanID:         plan.ID,
	}

	err = db.CreateSubscription(context.Background(), sub, "")
	if err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	return sub
}

// TestSubscriptionFlow_RevokeOldSubscription verifies the UNIQUE constraint on
// subscriptions.telegram_id: creating a second subscription row for the same
// Telegram user must fail, leaving exactly one active subscription.
func TestSubscriptionFlow_RevokeOldSubscription(t *testing.T) {
	t.Parallel()

	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()

	CreateTestSubscriptionInDB(t, f.DB, f.UserChatID, "testuser1", "active", testutil.PtrTime(time.Now().Add(30*24*time.Hour)))

	clientID, err := utils.GenerateUUID()
	if err != nil {
		t.Fatalf("Failed to generate client ID: %v", err)
	}

	plan, planErr := f.DB.GetPlanByName(ctx, database.FreePlanName)
	if planErr != nil {
		t.Fatalf("Failed to resolve free plan: %v", planErr)
	}

	// Creating another subscription with the same telegram_id should fail
	// due to UNIQUE constraint
	err = f.DB.CreateSubscription(ctx, &database.Subscription{
		TelegramID:     f.UserChatID,
		Username:       "testuser2",
		ClientID:       clientID,
		SubscriptionID: "testuser2",
		ExpiresAt:      testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)),
		Status:         "active",
		PlanID:         plan.ID,
	}, "")
	if err == nil {
		t.Fatal("Expected error due to UNIQUE constraint on telegram_id")
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

func TestDatabaseService_GetTelegramIDsBatch(t *testing.T) {
	t.Parallel()

	f := NewTestFixture(t)
	defer f.Close()

	ctx := context.Background()

	CreateTestSubscriptionInDB(t, f.DB, 111, "user1", "active", testutil.PtrTime(time.Now().Add(30*24*time.Hour)))
	CreateTestSubscriptionInDB(t, f.DB, 222, "user2", "active", testutil.PtrTime(time.Now().Add(30*24*time.Hour)))
	CreateTestSubscriptionInDB(t, f.DB, 333, "user3", "revoked", testutil.PtrTime(time.Now().Add(-24*time.Hour)))

	ids, err := f.DB.GetTelegramIDsBatch(ctx, 0, 100)
	if err != nil {
		t.Fatalf("Failed to get telegram IDs batch: %v", err)
	}

	if len(ids) != 2 {
		t.Errorf("Expected 2 active telegram IDs, got %d", len(ids))
	}
}
