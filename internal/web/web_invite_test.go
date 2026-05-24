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
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	"gorm.io/gorm"
)

func TestHandleInvite_InvalidCode(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	// Mock: invite not found
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return nil, gorm.ErrRecordNotFound
	}

	req := httptest.NewRequest("GET", "/i/invalidcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "handleInvite() status")

	body := rec.Body.String()
	assert.Contains(t, body, "Приглашение не найдено", "handleInvite() body should contain error message")
}

func TestHandleInvite_RateLimitExceeded(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     2,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	// Mock: invite exists
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}

	// Mock: rate limit exceeded
	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 3, nil // More than limit
	}

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "handleInvite() status")

	body := rec.Body.String()
	assert.Contains(t, body, "Слишком много запросов", "handleInvite() body should contain rate limit message")
}

func TestHandleInvite_Success(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	// Mock: invite exists
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}

	// Mock: rate limit OK
	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 0, nil
	}

	// Mock: create trial request
	mockDB.CreateTrialRequestFunc = func(ctx context.Context, ip string) error {
		return nil
	}

	// Mock: create trial subscription
	mockDB.CreateTrialSubscriptionFunc = func(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			ClientID:        clientID,
			InviteCode:      inviteCode,
			SubscriptionURL: subURL,
		}, nil
	}

	// Mock: XUI add client
	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
	}

	// Mock: XUI get subscription link
	mockXUI.GetSubscriptionLinkFunc = func(host, subID, subPath string) string {
		return "http://localhost:2053/sub/" + subID
	}

	// Mock: XUI get external URL
	mockXUI.GetExternalURLFunc = func(host string) string {
		return host
	}

	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg, &webhook.NoopSender{})
	srv.subService = subService

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleInvite() status")

	body := rec.Body.String()

	// Check HTML contains expected elements
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
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
	}

	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg, &webhook.NoopSender{})
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), subService, nil)

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

	// Should be 10 characters (5 random bytes hex-encoded)
	assert.Equal(t, 10, len(id1), "GenerateSubID() length")

	// Should be different each time
	assert.NotEqual(t, id1, id2, "GenerateSubID() should generate different IDs")

	// Should only contain hex characters
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
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/i/", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "handleInvite() status")
}

func TestHandleInvite_DatabaseError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	// Mock: database error
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
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/i/test", nil)

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.Error(t, err, "getExistingTrialFromCookie() should return error when no cookie")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil when no cookie")
}

func TestGetExistingTrialFromCookie_InvalidSubID(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

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
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	// Mock: subscription exists but is not trial
	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			IsTrial:        false,
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
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	// Mock: trial subscription but already activated (telegram_id != 0)
	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			IsTrial:        true,
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
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	// Mock: expired trial subscription
	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			IsTrial:        true,
			TelegramID:     0,
			ExpiryTime:     time.Now().Add(-1 * time.Hour), // Expired
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
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	// Mock: valid unactivated trial subscription
	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			IsTrial:         true,
			TelegramID:      0,
			ExpiryTime:      time.Now().Add(2 * time.Hour),
			SubscriptionURL: "https://vpn.site/sub/test",
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

	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

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
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

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

	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg, &webhook.NoopSender{})
	srv.subService = subService

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
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

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
	mockDB.CreateTrialSubscriptionFunc = func(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error) {
		return nil, fmt.Errorf("DB error")
	}

	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg, &webhook.NoopSender{})
	srv.subService = subService

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
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}
	mockDB.GetTrialSubscriptionBySubIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  "existing-sub-id",
			SubscriptionURL: "https://vpn.site/sub/existing",
			IsTrial:         true,
			TelegramID:      0,
			ExpiryTime:      time.Now().Add(2 * time.Hour),
		}, nil
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
	assert.Contains(t, body, "https://vpn.site/sub/existing")
}

func TestHandleInvite_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("POST", "/i/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET", rec.Header().Get("Allow"))
}

func TestHandleInvite_InvalidPath(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/not-invite/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Страница не найдена")
}

func TestHandleInvite_InvalidCodeChars(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/i/invalid@code!", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Приглашение не найдено")
}

func TestHandleInvite_RateLimitCheckError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

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
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg, &webhook.NoopSender{})
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), subService, nil)

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
	mockDB.CreateTrialSubscriptionFunc = func(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			SubscriptionURL: subURL,
			IsTrial:         true,
			InviteCode:      inviteCode,
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
