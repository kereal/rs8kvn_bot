package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/webhook"
)

// newTestAPIServer creates a Server for API tests with sensible defaults.
func newTestAPIServer(t *testing.T, cfg *config.Config, mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) *Server {
	t.Helper()

	if cfg.APIToken == "" {
		cfg.APIToken = "test-api-token"
	}
	if cfg.XUIHost == "" {
		cfg.XUIHost = "http://localhost:2053"
	}
	if cfg.XUIInboundID == 0 {
		cfg.XUIInboundID = 1
	}

	botConfig := &bot.BotConfig{Username: "testbot"}
	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg, &webhook.NoopSender{})

	return NewServer(":0", mockDB, mockXUI, cfg, botConfig, subService, nil)
}

func TestGetSubscriptions_Success(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	now := time.Now().Truncate(time.Second)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{
			{
				ID:             1,
				TelegramID:     123456,
				Username:       "user1",
				ClientID:       "client-uuid-1",
				SubscriptionID: "sub-token-1",
				Status:         "active",
				CreatedAt:      now,
			},
			{
				ID:             2,
				TelegramID:     789012,
				Username:       "user2",
				ClientID:       "client-uuid-2",
				SubscriptionID: "sub-token-2",
				Status:         "active",
				CreatedAt:      now,
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store, private", rec.Header().Get("Cache-Control"))

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 2)

	sub1 := subs[0].(map[string]interface{})
	assert.Equal(t, "client-uuid-1", sub1["id"])
	assert.Equal(t, "user1", sub1["email"])
	assert.Equal(t, true, sub1["enabled"])
	assert.Equal(t, "sub-token-1", sub1["subscription_token"])

	sub2 := subs[1].(map[string]interface{})
	assert.Equal(t, "client-uuid-2", sub2["id"])
	assert.Equal(t, "user2", sub2["email"])
	assert.Equal(t, true, sub2["enabled"])
	assert.Equal(t, "sub-token-2", sub2["subscription_token"])
}

func TestGetSubscriptions_EmptyList(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 0)
}

func TestGetSubscriptions_FiltersInactiveSubscriptions(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	now := time.Now().Truncate(time.Second)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{
			{
				ID:             1,
				TelegramID:     123456,
				Username:       "active_user",
				ClientID:       "client-active",
				SubscriptionID: "token-active",
				Status:         "active",
				CreatedAt:      now,
			},
			{
				ID:             2,
				TelegramID:     789012,
				Username:       "revoked_user",
				ClientID:       "client-revoked",
				SubscriptionID: "token-revoked",
				Status:         "revoked",
				CreatedAt:      now,
			},
			{
				ID:             3,
				TelegramID:     345678,
				Username:       "expired_user",
				ClientID:       "client-expired",
				SubscriptionID: "token-expired",
				Status:         "expired",
				CreatedAt:      now,
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 1, "only active subscription should be returned")

	sub := subs[0].(map[string]interface{})
	assert.Equal(t, "client-active", sub["id"])
	assert.Equal(t, "active_user", sub["email"])
}

func TestGetSubscriptions_FiltersSoftDeleted(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	now := time.Now().Truncate(time.Second)
	deletedAt := now.Add(-1 * time.Hour)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{
			{
				ID:             1,
				TelegramID:     123456,
				Username:       "active_user",
				ClientID:       "client-active",
				SubscriptionID: "token-active",
				Status:         "active",
				CreatedAt:      now,
			},
			{
				ID:             2,
				TelegramID:     789012,
				Username:       "deleted_user",
				ClientID:       "client-deleted",
				SubscriptionID: "token-deleted",
				Status:         "active",
				CreatedAt:      now,
				DeletedAt:      gorm.DeletedAt{Time: deletedAt, Valid: true},
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 1, "soft-deleted subscription should be filtered out")

	sub := subs[0].(map[string]interface{})
	assert.Equal(t, "client-active", sub["id"])
}

func TestGetSubscriptions_DatabaseError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return nil, assert.AnError
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "internal server error")
}

func TestGetSubscriptions_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/subscriptions", nil)
			rec := httptest.NewRecorder()

			s.GetSubscriptions(rec, req)

			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
			assert.Contains(t, rec.Body.String(), "method not allowed")
			assert.Equal(t, http.MethodGet, rec.Header().Get("Allow"))
		})
	}
}

func TestGetSubscriptions_WithBearerAuth(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{
		APIToken: "my-secret-api-token",
	}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{}, nil
	}

	tests := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{"valid token", "my-secret-api-token", http.StatusOK},
		{"invalid token", "wrong-token", http.StatusUnauthorized},
		{"empty token", "", http.StatusUnauthorized},
		{"missing header", "-", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
			if tt.token != "-" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			rec := httptest.NewRecorder()

			// Use the handler with middleware like in production
			apiMux := http.NewServeMux()
			apiMux.HandleFunc("/api/v1/subscriptions", s.GetSubscriptions)
			handler := BearerAuthMiddleware(cfg.APIToken)(apiMux)

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestGetSubscriptions_ResponseFormat(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	now := time.Now().Truncate(time.Second)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{
			{
				ID:             1,
				TelegramID:     123456,
				Username:       "user@example.com",
				ClientID:       "550e8400-e29b-41d4-a716-446655440000",
				SubscriptionID: "abc123def456",
				Status:         "active",
				CreatedAt:      now,
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	require.Len(t, subs, 1)

	sub := subs[0].(map[string]interface{})

	// Verify exact field names match the spec from task-bot-integration.md
	assert.Contains(t, sub, "id")
	assert.Contains(t, sub, "email")
	assert.Contains(t, sub, "enabled")
	assert.Contains(t, sub, "subscription_token")

	// Verify field values match the spec mapping
	// id = Subscription.ClientID (UUID)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", sub["id"])
	// email = Subscription.Username
	assert.Equal(t, "user@example.com", sub["email"])
	// enabled = IsActive()
	assert.Equal(t, true, sub["enabled"])
	// subscription_token = Subscription.SubscriptionID
	assert.Equal(t, "abc123def456", sub["subscription_token"])

	// Verify no extra fields
	assert.Len(t, sub, 4)
}

func TestGetSubscriptions_MixedStatuses(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	now := time.Now().Truncate(time.Second)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{
			{ID: 1, ClientID: "active-1", Username: "user1", SubscriptionID: "token1", Status: "active", CreatedAt: now},
			{ID: 2, ClientID: "active-2", Username: "user2", SubscriptionID: "token2", Status: "active", CreatedAt: now},
			{ID: 3, ClientID: "revoked-1", Username: "user3", SubscriptionID: "token3", Status: "revoked", CreatedAt: now},
			{ID: 4, ClientID: "expired-1", Username: "user4", SubscriptionID: "token4", Status: "expired", CreatedAt: now},
			{ID: 5, ClientID: "active-3", Username: "user5", SubscriptionID: "token5", Status: "active", CreatedAt: now},
			{ID: 6, ClientID: "pending-1", Username: "user6", SubscriptionID: "token6", Status: "pending", CreatedAt: now},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 3, "only active subscriptions should be returned")

	for i, sub := range subs {
		subMap := sub.(map[string]interface{})
		assert.Equal(t, true, subMap["enabled"], "subscription %d should have enabled=true", i)
	}
}

func TestGetSubscriptions_ActiveButSoftDeleted(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	now := time.Now().Truncate(time.Second)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{
			{
				ID:             1,
				ClientID:       "active-not-deleted",
				Username:       "user1",
				SubscriptionID: "token1",
				Status:         "active",
				CreatedAt:      now,
			},
			{
				ID:             2,
				ClientID:       "active-but-deleted",
				Username:       "user2",
				SubscriptionID: "token2",
				Status:         "active",
				CreatedAt:      now,
				DeletedAt:      gorm.DeletedAt{Time: now, Valid: true},
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 1)

	sub := subs[0].(map[string]interface{})
	assert.Equal(t, "active-not-deleted", sub["id"])
}

func TestGetSubscriptions_LargeDataset(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	now := time.Now().Truncate(time.Second)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		subs := make([]database.Subscription, 100)
		for i := 0; i < 100; i++ {
			id := i + 1
			// Safe: i is always 0-99, but explicit check for 32-bit systems
			if id < 0 {
				return nil, fmt.Errorf("integer overflow in test data")
			}
			subs[i] = database.Subscription{
				ID:             uint(id),
				TelegramID:     int64(100000 + i),
				Username:       "user" + string(rune('A'+i%26)),
				ClientID:       "client-uuid-" + string(rune('A'+i%26)),
				SubscriptionID: "token-" + string(rune('A'+i%26)),
				Status:         "active",
				CreatedAt:      now,
			}
		}
		return subs, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer test-api-token")
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 100)
}

func TestGetSubscriptions_HeadMethod(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	s := newTestAPIServer(t, cfg, mockDB, mockXUI)

	mockDB.GetAllSubscriptionsFunc = func(ctx context.Context) ([]database.Subscription, error) {
		return []database.Subscription{}, nil
	}

	req := httptest.NewRequest(http.MethodHead, "/api/v1/subscriptions", nil)
	rec := httptest.NewRecorder()

	s.GetSubscriptions(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
