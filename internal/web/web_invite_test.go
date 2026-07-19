package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"gorm.io/gorm"
)


func setupInviteServer(t *testing.T, inviteCode string) *Server {
	t.Helper()
	mockDB := testutil.NewDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
	}
	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}
	return NewServer(":8880", mockDB, cfg, "testbot", nil, nil)
}

func TestHandleInvite_InvalidCodeVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		wantCode int
		wantBody string
	}{
		{"empty code", "/i/", http.StatusNotFound, ""},
		{"invalid path", "/not-invite/testcode", http.StatusNotFound, "Страница не найдена"},
		{"invalid chars", "/i/invalid@code!", http.StatusNotFound, "Приглашение не найдено"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := setupInviteServer(t, tt.path)
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()
			srv.handleInvite(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code, "handleInvite() status")
			if tt.wantBody != "" {
				assert.Contains(t, rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func makeTestSubService(mockDB *testutil.DatabaseService) (*config.Config, *service.SubscriptionService, *testutil.XUIClient) {
	mockXUI := testutil.NewXUIClient()
	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		GlobalSubURL:       "https://vpn.site/sub/",
	}

	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
	}

	xuiClients := map[uint]interfaces.XUIClient{1: mockXUI}
	nodes := []database.Node{{ID: 1, Name: "default", IsActive: true, Host: "http://localhost:2053", APIToken: "test-token", InboundIDs: "[1]", SubscriptionURL: cfg.GlobalSubURL}}
	subService := service.NewSubscriptionService(mockDB, xuiClients, nil, nodes, cfg)
	return cfg, subService, mockXUI
}

func TestHandleInvite_InvalidCode(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
	}
	cfg, subService, mockXUI := makeTestSubService(mockDB)

	// Override rate limit to 3
	cfg.TrialRateLimit = 3

	// Mocks
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}

	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 0, nil
	}

	mockDB.CreateTrialRequestFunc = func(ctx context.Context, ip string) error {
		return nil
	}

	mockDB.CreateTrialSubscriptionFunc = func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
		inviteVal := inviteCode
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			ClientID:       clientID,
			InviteCode:     &inviteVal,
		}, nil
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		assert.Equal(t, []int{1}, req.InboundIDs, "inboundIDs should resolve to expected value")
		return &xui.ClientConfig{ID: req.ClientID, SubID: req.SubID}, nil
	}

	mockXUI.GetSubscriptionLinkFunc = func(host, subID, subPath string) string {
		return "http://localhost:2053/sub/" + subID
	}

	mockXUI.GetExternalURLFunc = func(host string) string {
		return host
	}

	srv := NewServer(":8880", mockDB, cfg, "testbot", subService, nil)

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleInvite() status")

	body := rec.Body.String()

	expectedElements := []string{
		"RS8 KVN",
		"Добавить в Happ",
		"Активировать",
		"t.me/testbot",
		"trial_",
	}

	for _, expected := range expectedElements {
		assert.Contains(t, body, expected, "handleInvite() body should contain expected element")
	}
}

func TestHandleInvite_XUIError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	cfg, subService, mockXUI := makeTestSubService(mockDB)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}

	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 0, nil
	}

	mockDB.CreateTrialRequestFunc = func(ctx context.Context, ip string) error {
		return nil
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		return nil, fmt.Errorf("XUI API error")
	}

	srv := NewServer(":8880", mockDB, cfg, "testbot", subService, nil)

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "handleInvite() status")

	body := rec.Body.String()
	assert.Contains(t, body, "Ошибка сервера", "handleInvite() body should contain error message")
}

func TestHandleInvite_DatabaseError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return nil, gorm.ErrInvalidDB
	}

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "handleInvite() status")
}

// === getExistingTrialFromCookie tests ===

func TestGetExistingTrialFromCookie_NoCookie(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	req := httptest.NewRequest("GET", "/i/test", nil)

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.NoError(t, err, "getExistingTrialFromCookie() should not return error when no cookie (expected business state)")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil when no cookie")
}

func TestGetExistingTrialFromCookie_InvalidSubID(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.AddCookie(&http.Cookie{
		Name:  "rs8kvn_trial_test",
		Value: "invalid-sub-id",
	})

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.NoError(t, err, "getExistingTrialFromCookie() should not return error for invalid sub ID (expected business state)")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil for invalid sub ID")
}

func TestGetExistingTrialFromCookie_NotTrial(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			PlanID:         0,
			TelegramID:     123456,
		}, nil
	}

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.AddCookie(&http.Cookie{
		Name:  "rs8kvn_trial_test",
		Value: "valid-sub-id",
	})

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.NoError(t, err, "getExistingTrialFromCookie() should not return error for non-trial subscription (expected business state)")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil for non-trial subscription")
}

func TestGetExistingTrialFromCookie_AlreadyActivated(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			PlanID:         1,
			TelegramID:     123456, // Activated
		}, nil
	}

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.AddCookie(&http.Cookie{
		Name:  "rs8kvn_trial_test",
		Value: "valid-sub-id",
	})

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.NoError(t, err, "getExistingTrialFromCookie() should not return error for activated trial (expected business state)")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil for activated trial")
}

func TestGetExistingTrialFromCookie_Expired(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			PlanID:         1,
			TelegramID:     0,
			ExpiresAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour)), // Expired
		}, nil
	}
	mockDB.GetPlanByIDFunc = func(ctx context.Context, planID uint) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.TrialPlanName}, nil
	}

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.AddCookie(&http.Cookie{
		Name:  "rs8kvn_trial_test",
		Value: "valid-sub-id",
	})

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.NoError(t, err, "getExistingTrialFromCookie() should not return error for expired trial (expected business state)")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil for expired trial")
}

func TestGetExistingTrialFromCookie_Valid(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			PlanID:         1,
			TelegramID:     0,
			ExpiresAt:      testutil.PtrTime(time.Now().Add(2 * time.Hour)),
		}, nil
	}
	mockDB.GetPlanByIDFunc = func(ctx context.Context, planID uint) (*database.Plan, error) {
		return &database.Plan{
			ID:   planID,
			Name: database.TrialPlanName,
		}, nil
	}

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.AddCookie(&http.Cookie{
		Name:  "rs8kvn_trial_test",
		Value: "valid-sub-id",
	})

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.NoError(t, err, "getExistingTrialFromCookie() should return nil error for valid trial")
	assert.NotNil(t, sub, "getExistingTrialFromCookie() should return subscription for valid trial")
	assert.Equal(t, "valid-sub-id", sub.SubscriptionID, "getExistingTrialFromCookie() should return correct sub ID")
}

// === Additional handleInvite error path tests ===

func TestHandleInvite_XUIAddClientFails(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	cfg, subService, mockXUI := makeTestSubService(mockDB)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}
	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 0, nil
	}
	mockDB.CreateTrialRequestFunc = func(ctx context.Context, ip string) error {
		return nil
	}
	mockXUI.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		return nil, fmt.Errorf("XUI add client error")
	}

	srv := NewServer(":8880", mockDB, cfg, "testbot", subService, nil)

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера")
}

func TestHandleInvite_CreateTrialSubscriptionFails(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	cfg, subService, mockXUI := makeTestSubService(mockDB)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}
	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 0, nil
	}
	mockDB.CreateTrialRequestFunc = func(ctx context.Context, ip string) error {
		return nil
	}
	mockXUI.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		return &xui.ClientConfig{ID: req.ClientID, SubID: req.SubID}, nil
	}
	mockXUI.GetSubscriptionLinkFunc = func(host, subID, subPath string) string {
		return "http://localhost:2053/sub/" + subID
	}
	mockXUI.GetExternalURLFunc = func(host string) string {
		return host
	}
	mockDB.CreateTrialSubscriptionFunc = func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
		return nil, fmt.Errorf("DB error")
	}

	srv := NewServer(":8880", mockDB, cfg, "testbot", subService, nil)

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера")
}

func TestHandleInvite_ExistingTrialFromCookie(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}
	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: "existing-sub-id",
			PlanID:         1,
			TelegramID:     0,
			ExpiresAt:      testutil.PtrTime(time.Now().Add(2 * time.Hour)),
		}, nil
	}
	mockDB.GetPlanByIDFunc = func(ctx context.Context, planID uint) (*database.Plan, error) {
		return &database.Plan{ID: planID, Name: database.TrialPlanName}, nil
	}

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.AddCookie(&http.Cookie{
		Name:  "rs8kvn_trial_testcode",
		Value: "existing-sub-id",
	})
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "existing-sub-id")
	expectedSubURL := cfg.GlobalSubURL + "existing-sub-id"
	assert.Contains(t, body, expectedSubURL)
}

func TestHandleInvite_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	req := httptest.NewRequest("POST", "/i/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET", rec.Header().Get("Allow"))
}

func TestHandleInvite_RateLimitCheckError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, cfg, "testbot", nil, nil)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}
	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 0, fmt.Errorf("DB connection failed")
	}

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера")
}

func TestHandleInvite_ParallelRequests(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	cfg, subService, _ := makeTestSubService(mockDB)
	srv := NewServer(":8880", mockDB, cfg, "testbot", subService, nil)

	var (
		mu    sync.Mutex
		count int
	)
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}

	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		count++
		return count, nil
	}
	mockDB.CreateTrialRequestFunc = func(ctx context.Context, ip string) error {
		return nil
	}
	mockDB.CreateTrialSubscriptionFunc = func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
		inviteVal := inviteCode
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			PlanID:         1,
			InviteCode:     &inviteVal,
		}, nil
	}

	const numParallel = 10
	var wg sync.WaitGroup
	results := make(chan int, numParallel)

	for i := 0; i < numParallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/i/testcode", nil)
			req.RemoteAddr = "192.168.1.1:12345"
			rec := httptest.NewRecorder()
			srv.handleInvite(rec, req)
			results <- rec.Code
		}()
	}

	wg.Wait()
	close(results)

	successCount := 0
	rateLimitedCount := 0
	for code := range results {
		if code == http.StatusOK {
			successCount++
		} else if code == http.StatusTooManyRequests {
			rateLimitedCount++
		}
	}

	assert.Greater(t, rateLimitedCount, 0, "some parallel requests should be rate limited")
	assert.Equal(t, numParallel, successCount+rateLimitedCount, "all requests should return a response")
	assert.LessOrEqual(t, successCount, cfg.TrialRateLimit, "at most TrialRateLimit requests should succeed")
}
