package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/xui"

	"gorm.io/gorm"
)

func init() {
	// Initialize logger for tests
	_, _ = logger.Init("", "error")
}

func TestHandleInvite_InvalidCode(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, "testbot")

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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     2,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, "testbot")

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

	srv := NewServer(":8880", mockDB, mockXUI, cfg, "testbot")

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

	// Mock: XUI login
	mockXUI.LoginFunc = func(ctx context.Context) error {
		return nil
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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, "testbot")

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

	// Mock: XUI login fails
	mockXUI.LoginFunc = func(ctx context.Context) error {
		return fmt.Errorf("unauthorized")
	}

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "handleInvite() status")

	body := rec.Body.String()
	assert.Contains(t, body, "Ошибка сервера", "handleInvite() body should contain error message")
}

func TestRenderTrialPage(t *testing.T) {
	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
	}

	srv := NewServer(":8880", nil, nil, cfg, "testbot")

	html := srv.renderTrialPage("sub123", "https://vpn.site/sub/sub123", "https://t.me/testbot?start=trial_sub123", 3)

	// Check that HTML contains expected elements
	expectedElements := []string{
		"<!DOCTYPE html>",
		"RS8 KVN",
		"Добавить в Happ",
		"happ://add/",
		"Активировать",
		"https://t.me/testbot?start=trial_sub123",
		"3 часа",
		"Срок действия",
		"copyToClipboard",
		"play.google.com",
		"apps.apple.com",
	}

	for _, expected := range expectedElements {
		assert.Contains(t, html, expected, "renderTrialPage() should contain expected element")
	}
}

func TestRenderErrorPage(t *testing.T) {
	srv := NewServer(":8880", nil, nil, nil, "")

	html := srv.renderErrorPage("Тестовая ошибка")

	// Check that HTML contains error message
	assert.Contains(t, html, "Тестовая ошибка", "renderErrorPage() should contain error message")

	// Check HTML structure
	assert.Contains(t, html, "<!DOCTYPE html>", "renderErrorPage() should be valid HTML")
}

func TestGetClientIP_Direct(t *testing.T) {
	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	ip := getClientIP(req)

	assert.Equal(t, "192.168.1.100", ip, "getClientIP()")
}

func TestGetClientIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 198.51.100.1")

	ip := getClientIP(req)

	// Should use first IP from X-Forwarded-For
	assert.Equal(t, "203.0.113.50", ip, "getClientIP() should use first IP from X-Forwarded-For")
}

func TestGenerateSubID(t *testing.T) {
	id1 := utils.GenerateSubID()
	id2 := utils.GenerateSubID()

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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, "testbot")

	req := httptest.NewRequest("GET", "/i/", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "handleInvite() status")
}

func TestHandleInvite_DatabaseError(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, "testbot")

	// Mock: database error
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return nil, gorm.ErrInvalidDB
	}

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "handleInvite() status")
}

func TestRenderTrialPage_HappLink(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, "testbot")

	subURL := "https://vpn.site/sub/abc123"
	html := srv.renderTrialPage("abc123", subURL, "https://t.me/testbot?start=trial_abc123", 3)

	// Check happ:// link is generated correctly
	expectedHappLink := "happ://add/" + subURL
	assert.Contains(t, html, expectedHappLink, "renderTrialPage() should contain happ link")
}
