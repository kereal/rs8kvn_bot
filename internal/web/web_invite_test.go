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
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	"gorm.io/gorm"
)

func makeTestSubService(mockDB *testutil.MockDatabaseService) (*config.Config, *service.SubscriptionService, *testutil.MockXUIClient) {
	mockXUI := testutil.NewMockXUIClient()
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
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundID: 1}}, nil
	}

	xuiClients := map[uint]interfaces.XUIClient{1: mockXUI}
	sources := []database.Node{{ID: 1, Name: "default", IsActive: true, Host: "http://localhost:2053", APIToken: "test-token", InboundID: 1, SubscriptionURL: cfg.GlobalSubURL}}
	subService := service.NewSubscriptionService(mockDB, xuiClients, sources, cfg, cfg.GlobalSubURL, &webhook.NoopSender{})
	return cfg, subService, mockXUI
}

func TestHandleInvite_InvalidCode(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundID: 1}}, nil
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
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			ClientID:       clientID,
			InviteCode:     inviteCode,
		}, nil
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
	}

	mockXUI.GetSubscriptionLinkFunc = func(host, subID, subPath string) string {
		return "http://localhost:2053/sub/" + subID
	}

	mockXUI.GetExternalURLFunc = func(host string) string {
		return host
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), subService, nil)

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

	mockDB := testutil.NewMockDatabaseService()

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

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return nil, fmt.Errorf("XUI API error")
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), subService, nil)

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "handleInvite() status")

	body := rec.Body.String()
	assert.Contains(t, body, "Ошибка сервера", "handleInvite() body should contain error message")
}

func TestGenerateSubID(t *testing.T) {
	t.Parallel()

	id1, err := utils.GenerateSubID()
	require.NoError(t, err)
	id2, err := utils.GenerateSubID()
	require.NoError(t, err)

	assert.Equal(t, 10, len(id1), "GenerateSubID() length")
	assert.NotEqual(t, id1, id2, "GenerateSubID() should generate different IDs")
	for _, c := range id1 {
		assert.True(t, isHexDigit(c), "GenerateSubID() contains non-hex character: %c", c)
	}
}

func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

func TestHandleInvite_EmptyCode(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundID: 1}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/i/", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "handleInvite() status")
}

func TestHandleInvite_DatabaseError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundID: 1}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

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

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/i/test", nil)

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.Error(t, err, "getExistingTrialFromCookie() should return error when no cookie")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil when no cookie")
}

func TestGetExistingTrialFromCookie_InvalidSubID(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.AddCookie(&http.Cookie{
		Name:  "rs8kvn_trial_test",
		Value: "invalid-sub-id",
	})

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.Error(t, err, "getExistingTrialFromCookie() should return error for invalid sub ID")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil for invalid sub ID")
}

func TestGetExistingTrialFromCookie_NotTrial(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

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

	assert.Error(t, err, "getExistingTrialFromCookie() should return error for non-trial subscription")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil for non-trial subscription")
}

func TestGetExistingTrialFromCookie_AlreadyActivated(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

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

	assert.Error(t, err, "getExistingTrialFromCookie() should return error for activated trial")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil for activated trial")
}

func TestGetExistingTrialFromCookie_Expired(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			PlanID:         1,
			TelegramID:     0,
			ExpiresAt:      time.Now().Add(-1 * time.Hour), // Expired
		}, nil
	}

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.AddCookie(&http.Cookie{
		Name:  "rs8kvn_trial_test",
		Value: "valid-sub-id",
	})

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.Error(t, err, "getExistingTrialFromCookie() should return error for expired trial")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil for expired trial")
}

func TestGetExistingTrialFromCookie_Valid(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			PlanID:         1,
			TelegramID:     0,
			ExpiresAt:      time.Now().Add(2 * time.Hour),
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

func TestInviteCodeRegex(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	tests := []struct {
		name  string
		code  string
		valid bool
	}{
		{"alphanumeric", "abc123", true},
		{"with underscore", "abc_123", true},
		{"with hyphen", "abc-123", true},
		{"uppercase", "ABC123", true},
		{"mixed case", "AbC123", true},
		{"empty string", "", false},
		{"with slash", "abc/123", false},
		{"with dot", "abc.123", false},
		{"with space", "abc 123", false},
		{"with at sign", "abc@123", false},
		{"sql injection", "abc'; DROP TABLE--", false},
		{"path traversal", "../etc/passwd", false},
		{"only numbers", "123456", true},
		{"only letters", "abcdef", true},
		{"single char", "a", true},
		{"long code", "abcdefghij1234567890", true},
		{"cyrillic", "абв", false},
		{"unicode", "用户", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := srv.inviteCodeRegex.MatchString(tt.code)
			assert.Equal(t, tt.valid, result, "inviteCodeRegex.MatchString(%q)", tt.code)
		})
	}
}

// === Additional handleInvite error path tests ===

func TestHandleInvite_XUIAddClientFails(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()

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
	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return nil, fmt.Errorf("XUI add client error")
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), subService, nil)

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера")
}

func TestHandleInvite_CreateTrialSubscriptionFails(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()

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
	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
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

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), subService, nil)

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера")
}

func TestHandleInvite_ExistingTrialFromCookie(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundID: 1}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}
	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: "existing-sub-id",
			PlanID:         1,
			TelegramID:     0,
			ExpiresAt:      time.Now().Add(2 * time.Hour),
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

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("POST", "/i/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET", rec.Header().Get("Allow"))
}

func TestHandleInvite_InvalidPath(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/not-invite/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Страница не найдена")
}

func TestHandleInvite_InvalidCodeChars(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/i/invalid@code!", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Приглашение не найдено")
}

func TestHandleInvite_RateLimitCheckError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundID: 1}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)

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

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}
	mockDB.GetNodesByPlanNameFunc = func(ctx context.Context, planName string) ([]database.Node, error) {
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundID: 1}}, nil
	}

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	cfg, subService, _ := makeTestSubService(mockDB)
	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), subService, nil)

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
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			PlanID:         1,
			InviteCode:     inviteCode,
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
