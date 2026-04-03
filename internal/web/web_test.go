package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	"rs8kvn_bot/internal/xui"

	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
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

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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

	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg)
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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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

	srv := NewServer(":8880", nil, nil, cfg, bot.NewTestBotConfig(), nil)

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
	srv := NewServer(":8880", nil, nil, nil, bot.NewTestBotConfig(), nil)

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

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	subURL := "https://vpn.site/sub/abc123"
	html := srv.renderTrialPage("abc123", subURL, "https://t.me/testbot?start=trial_abc123", 3)

	// Check happ:// link is generated correctly
	expectedHappLink := "happ://add/" + subURL
	assert.Contains(t, html, expectedHappLink, "renderTrialPage() should contain happ link")
}

// === Health endpoint tests ===

func TestHandleHealthz(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleHealthz() status")
	assert.Contains(t, rec.Body.String(), "ok", "handleHealthz() body should contain 'ok'")
}

func TestHandleHealthz_MethodNotAllowed(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	// Test POST method
	req := httptest.NewRequest("POST", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleHealthz() should reject POST")
	assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"), "handleHealthz() should set Allow header")

	// Test PUT method
	req = httptest.NewRequest("PUT", "/healthz", nil)
	rec = httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleHealthz() should reject PUT")

	// Test DELETE method
	req = httptest.NewRequest("DELETE", "/healthz", nil)
	rec = httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleHealthz() should reject DELETE")
}

func TestHandleHealthz_HeadMethod(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	req := httptest.NewRequest("HEAD", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleHealthz() should accept HEAD")
}

func TestHandleReadyz_MethodNotAllowed(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	// Test POST method
	req := httptest.NewRequest("POST", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleReadyz() should reject POST")
	assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"), "handleReadyz() should set Allow header")

	// Test PUT method
	req = httptest.NewRequest("PUT", "/readyz", nil)
	rec = httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleReadyz() should reject PUT")

	// Test DELETE method
	req = httptest.NewRequest("DELETE", "/readyz", nil)
	rec = httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleReadyz() should reject DELETE")
}

func TestHandleReadyz_HeadMethod(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)
	srv.SetReady(true)

	req := httptest.NewRequest("HEAD", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleReadyz() should accept HEAD")
}

func TestHandleReadyz_NotReady(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)
	// Register a failing checker to make health status not "ok"
	srv.RegisterChecker("failing", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "service down"}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "handleReadyz() status when not ready")
	assert.Contains(t, rec.Body.String(), "NOT READY", "handleReadyz() body should contain 'NOT READY'")
}

func TestHandleReadyz_Ready(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)
	// No checkers registered means health status will be "ok"

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleReadyz() status when ready")
	assert.Equal(t, "OK", rec.Body.String(), "handleReadyz() body should be 'OK'")
}

func TestHandleReadyz_WithChecker(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	// Register a health checker that returns OK
	srv.RegisterChecker("test-component", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK, Message: "all good"}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	// handleReadyz returns simple "OK" or "NOT READY", not JSON
	assert.Equal(t, http.StatusOK, rec.Code, "handleReadyz() status with healthy checker")
	assert.Equal(t, "OK", rec.Body.String(), "handleReadyz() body should be 'OK'")
}

func TestHandleReadyz_WithFailingChecker(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	// Register a health checker that returns degraded
	srv.RegisterChecker("failing-component", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDegraded, Message: "something is wrong"}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	// Degraded status means NOT READY with 503
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "handleReadyz() status with degraded checker")
	assert.Equal(t, "NOT READY", rec.Body.String(), "handleReadyz() body should be 'NOT READY'")
}

func TestHandleReadyz_WithDownChecker(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	// Register a health checker that returns down
	srv.RegisterChecker("down-component", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "component is down"}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "handleReadyz() status with down checker")
	assert.Equal(t, "NOT READY", rec.Body.String(), "handleReadyz() body should be 'NOT READY'")
}

func TestCheckHealth_NoCheckers(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	health := srv.checkHealth(context.Background())

	assert.Equal(t, "ok", health.Status, "checkHealth() status with no checkers")
	assert.Empty(t, health.Components, "checkHealth() should have no components")
}

func TestCheckHealth_WithCheckers(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	srv.RegisterChecker("comp1", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK, Message: "ok"}
	})
	srv.RegisterChecker("comp2", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDegraded, Message: "degraded"}
	})

	health := srv.checkHealth(context.Background())

	assert.Equal(t, "degraded", health.Status, "checkHealth() should return degraded when one component is degraded")
	assert.Len(t, health.Components, 2, "checkHealth() should have 2 components")
}

func TestCheckHealth_AllDown(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	srv.RegisterChecker("comp1", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "down1"}
	})
	srv.RegisterChecker("comp2", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "down2"}
	})

	health := srv.checkHealth(context.Background())

	assert.Equal(t, "down", health.Status, "checkHealth() should return down when all components are down")
}

func TestWriteJSON(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	rec := httptest.NewRecorder()

	resp := HealthResponse{
		Status:    string(StatusOK),
		Timestamp: time.Now(),
	}
	srv.writeJSON(rec, resp)

	assert.Equal(t, http.StatusOK, rec.Code, "writeJSON() status")
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"), "writeJSON() content-type")
	assert.Contains(t, rec.Body.String(), "status", "writeJSON() body should contain status")
}

func TestSetReady(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	// Initially not ready
	srv.mu.Lock()
	ready := srv.ready
	srv.mu.Unlock()
	assert.False(t, ready, "Server should not be ready initially")

	// Set ready
	srv.SetReady(true)
	srv.mu.Lock()
	ready = srv.ready
	srv.mu.Unlock()
	assert.True(t, ready, "Server should be ready after SetReady(true)")

	// Set not ready
	srv.SetReady(false)
	srv.mu.Lock()
	ready = srv.ready
	srv.mu.Unlock()
	assert.False(t, ready, "Server should not be ready after SetReady(false)")
}

func TestRegisterChecker(t *testing.T) {
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	// Register multiple checkers
	srv.RegisterChecker("checker1", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})
	srv.RegisterChecker("checker2", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	srv.mu.Lock()
	count := len(srv.checkers)
	srv.mu.Unlock()

	assert.Equal(t, 2, count, "Server should have 2 checkers registered")
}

// === Status constant tests ===

func TestStatusConstants(t *testing.T) {
	assert.Equal(t, Status("ok"), StatusOK, "StatusOK constant")
	assert.Equal(t, Status("degraded"), StatusDegraded, "StatusDegraded constant")
	assert.Equal(t, Status("down"), StatusDown, "StatusDown constant")
}

// === ComponentHealth tests ===

func TestComponentHealth_Fields(t *testing.T) {
	health := ComponentHealth{
		Status:  StatusOK,
		Message: "test message",
	}

	assert.Equal(t, StatusOK, health.Status, "ComponentHealth.Status")
	assert.Equal(t, "test message", health.Message, "ComponentHealth.Message")
}

// === HealthResponse tests ===

func TestHealthResponse_Fields(t *testing.T) {
	resp := HealthResponse{
		Status:    string(StatusOK),
		Timestamp: time.Now(),
		Components: map[string]ComponentHealth{
			"comp1": {Status: StatusOK, Message: "component1"},
		},
	}

	assert.Equal(t, string(StatusOK), resp.Status, "HealthResponse.Status")
	assert.Len(t, resp.Components, 1, "HealthResponse.Components length")
}

// === GetClientIP edge cases ===

func TestGetClientIP_XForwardedForMultiple(t *testing.T) {
	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 198.51.100.1, 192.0.2.1")

	ip := getClientIP(req)

	// Should use first IP from X-Forwarded-For
	assert.Equal(t, "203.0.113.50", ip, "getClientIP() should use first IP from X-Forwarded-For")
}

// Note: X-Real-IP is not checked by getClientIP - it only checks X-Forwarded-For

func TestGetClientIP_NoPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "192.168.1.100" // No port

	ip := getClientIP(req)

	assert.Equal(t, "192.168.1.100", ip, "getClientIP() should handle address without port")
}

func TestGetClientIP_Localhost(t *testing.T) {
	tests := []struct {
		name     string
		remote   string
		forward  string
		expected string
	}{
		{"localhost IPv4", "127.0.0.1:12345", "8.8.8.8", "8.8.8.8"},
		{"localhost IPv6", "[::1]:12345", "", "::1"},
		{"private network trusted", "192.168.1.1:12345", "10.0.0.5", "10.0.0.5"},
		{"public IP not trusted", "8.8.8.8:12345", "1.2.3.4", "8.8.8.8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/i/test", nil)
			req.RemoteAddr = tt.remote
			if tt.forward != "" {
				req.Header.Set("X-Forwarded-For", tt.forward)
			}

			ip := getClientIP(req)
			assert.Equal(t, tt.expected, ip)
		})
	}
}

func TestGetClientIP_EmptyXForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "") // Empty header

	ip := getClientIP(req)

	assert.Equal(t, "10.0.0.1", ip, "Should fall back to RemoteAddr when X-Forwarded-For is empty")
}

func TestGetClientIP_WhitespaceInXForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "  192.0.2.1  ,  198.51.100.1  ") // Extra whitespace

	ip := getClientIP(req)

	assert.Equal(t, "192.0.2.1", ip, "Should trim whitespace from IP")
}

// === Server Start/Stop tests ===

func TestServer_StartAndStop(t *testing.T) {
	srv := NewServer(":0", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil) // :0 for random port

	ctx := context.Background()

	// Start server
	err := srv.Start(ctx)
	assert.NoError(t, err, "Start() should not return error")

	// Stop server
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Stop(stopCtx)
	assert.NoError(t, err, "Stop() should not return error")
}

func TestServer_StopWithoutStart(t *testing.T) {
	srv := NewServer(":0", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	// Stop without start should not panic
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Stop(stopCtx)
	assert.NoError(t, err, "Stop() without Start() should not return error")
}

// === getExistingTrialFromCookie tests ===

func TestGetExistingTrialFromCookie_NoCookie(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

	req := httptest.NewRequest("GET", "/i/test", nil)

	ctx := context.Background()
	sub, err := srv.getExistingTrialFromCookie(req, ctx, "test")

	assert.Error(t, err, "getExistingTrialFromCookie() should return error when no cookie")
	assert.Nil(t, sub, "getExistingTrialFromCookie() should return nil when no cookie")
}

func TestGetExistingTrialFromCookie_InvalidSubID(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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
	srv := NewServer(":8880", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}
	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 0, nil
	}
	mockDB.CreateTrialRequestFunc = func(ctx context.Context, ip string) error {
		return nil
	}
	mockXUI.LoginFunc = func(ctx context.Context) error {
		return fmt.Errorf("XUI error")
	}

	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg)
	srv.subService = subService

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера")
}

func TestHandleInvite_CreateTrialSubscriptionFails(t *testing.T) {
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

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "testcode", ReferrerTGID: 12345}, nil
	}
	mockDB.CountTrialRequestsByIPLastHourFunc = func(ctx context.Context, ip string) (int, error) {
		return 0, nil
	}
	mockDB.CreateTrialRequestFunc = func(ctx context.Context, ip string) error {
		return nil
	}
	mockXUI.LoginFunc = func(ctx context.Context) error {
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

	subService := service.NewSubscriptionService(mockDB, mockXUI, cfg)
	srv.subService = subService

	req := httptest.NewRequest("GET", "/i/testcode", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера")
}

func TestHandleInvite_ExistingTrialFromCookie(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

	req := httptest.NewRequest("POST", "/i/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET", rec.Header().Get("Allow"))
}

func TestHandleInvite_InvalidPath(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

	req := httptest.NewRequest("GET", "/not-invite/testcode", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Страница не найдена")
}

func TestHandleInvite_InvalidCodeChars(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}
	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

	req := httptest.NewRequest("GET", "/i/invalid@code!", nil)
	rec := httptest.NewRecorder()

	srv.handleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Приглашение не найдено")
}

func TestHandleInvite_RateLimitCheckError(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil)

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

func TestIsLocalAddress_Loopback(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"127.0.0.1", "127.0.0.1", true},
		{"localhost IPv4", "127.0.0.2", true},
		{"localhost IPv6", "::1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLocalAddress(tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsLocalAddress_Private(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"10.x.x.x", "10.0.0.1", true},
		{"10.x.x.x large", "10.255.255.255", true},
		{"172.16.x.x", "172.16.0.1", true},
		{"172.31.x.x", "172.31.255.255", true},
		{"192.168.x.x", "192.168.1.1", true},
		{"192.168.x.x max", "192.168.255.255", true},
		{"public IP", "8.8.8.8", false},
		{"public IP 2", "1.1.1.1", false},
		{"invalid", "invalid", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLocalAddress(tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetClientIP_NonLocalRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "8.8.8.8:12345"

	ip := getClientIP(req)

	assert.Equal(t, "8.8.8.8", ip, "Should use remote addr when not local")
}

func TestGetClientIP_InvalidRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "invalid"

	ip := getClientIP(req)

	assert.Equal(t, "invalid", ip, "Should fall back to raw remote addr")
}

func TestServer_Start_PortInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "Failed to create listener")

	addr := listener.Addr().String()
	port := strings.Split(addr, ":")[1]

	srv := NewServer(":"+port, nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)

	ctx := context.Background()
	err = srv.Start(ctx)
	require.NoError(t, err, "Start() should not return error (server runs in goroutine)")

	// Give the goroutine time to attempt binding and fail
	time.Sleep(100 * time.Millisecond)

	// Now close our listener
	listener.Close()

	// Give the OS time to release the port
	time.Sleep(50 * time.Millisecond)

	// The server should NOT be listening because it already failed to bind
	conn, dialErr := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		t.Fatal("Server should not be listening on a port that was already in use at start time")
	}
}

func TestGetClientIP_IPv6(t *testing.T) {
	tests := []struct {
		name     string
		forward  string
		remote   string
		expected string
	}{
		{
			name:     "single IPv6",
			forward:  "2001:db8::1",
			remote:   "[::1]:12345",
			expected: "2001:db8::1",
		},
		{
			name:     "IPv6 with port in brackets",
			forward:  "[2001:db8::1]:8080",
			remote:   "[::1]:12345",
			expected: "[2001:db8::1]:8080",
		},
		{
			name:     "IPv6 first of multiple",
			forward:  "2001:db8::1, 192.168.1.1",
			remote:   "[::1]:12345",
			expected: "2001:db8::1",
		},
		{
			name:     "IPv6 loopback",
			forward:  "::1",
			remote:   "[::1]:12345",
			expected: "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/i/test", nil)
			req.RemoteAddr = tt.remote
			req.Header.Set("X-Forwarded-For", tt.forward)

			ip := getClientIP(req)
			assert.Equal(t, tt.expected, ip)
		})
	}
}

func TestRenderTrialPage_XSSProtection(t *testing.T) {
	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		XUIInboundID:       1,
	}

	srv := NewServer(":8880", testutil.NewMockDatabaseService(), testutil.NewMockXUIClient(), cfg, bot.NewTestBotConfig(), nil)

	tests := []struct {
		name         string
		subURL       string
		telegramLink string
		checkXSS     func(t *testing.T, html string)
	}{
		{
			name:         "script tag in subURL",
			subURL:       "test<script>alert('xss')</script>",
			telegramLink: "https://t.me/testbot?start=123",
			checkXSS: func(t *testing.T, html string) {
				assert.NotContains(t, html, "<script>alert('xss')</script>", "script tag should be escaped")
				assert.Contains(t, html, "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;", "script should be HTML-escaped")
			},
		},
		{
			name:         "javascript in subURL",
			subURL:       "javascript:alert('xss')",
			telegramLink: "https://t.me/testbot?start=123",
			checkXSS: func(t *testing.T, html string) {
				assert.NotContains(t, html, `<a href="javascript:alert`, "raw javascript href should be escaped")
				assert.Contains(t, html, `javascript:alert(&#39;xss&#39;)`, "escaped javascript should be present")
			},
		},
		{
			name:         "onclick in subURL",
			subURL:       `test" onclick="alert('xss')"`,
			telegramLink: "https://t.me/testbot?start=123",
			checkXSS: func(t *testing.T, html string) {
				assert.NotContains(t, html, `onclick="alert`, "onclick should be escaped")
			},
		},
		{
			name:         "script tag in telegramLink",
			subURL:       "test123",
			telegramLink: "https://t.me/testbot?start=<script>alert('xss')</script>",
			checkXSS: func(t *testing.T, html string) {
				assert.NotContains(t, html, "<script>alert('xss')</script>", "script in telegramLink should be escaped")
			},
		},
		{
			name:         "normal input - no escaping needed",
			subURL:       "abc123def456",
			telegramLink: "https://t.me/testbot?start=abc123",
			checkXSS: func(t *testing.T, html string) {
				assert.Contains(t, html, "abc123def456", "normal subURL should be present")
				assert.Contains(t, html, "https://t.me/testbot?start=abc123", "normal telegramLink should be present")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := srv.renderTrialPage("sub1", tt.subURL, tt.telegramLink, 3)
			tt.checkXSS(t, html)
		})
	}
}
