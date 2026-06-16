package web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
}

func TestRenderTrialPage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
	}

	srv := NewServer(":8880", nil, cfg, bot.NewTestBotConfig(), nil, nil)

	w := httptest.NewRecorder()
	srv.renderTrialPage(w, "sub123", "https://vpn.site/sub/sub123", "https://t.me/testbot?start=trial_sub123", 3)
	html := w.Body.String()

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
	t.Parallel()

	srv := NewServer(":8880", nil, nil, bot.NewTestBotConfig(), nil, nil)

	w := httptest.NewRecorder()
	srv.renderErrorPage(w, "Тестовая ошибка")
	html := w.Body.String()

	// Check that HTML contains error message
	assert.Contains(t, html, "Тестовая ошибка", "renderErrorPage() should contain error message")

	// Check HTML structure
	assert.Contains(t, html, "<!DOCTYPE html>", "renderErrorPage() should be valid HTML")
}

func TestGetClientIP_Direct(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	ip := getClientIP(req)

	assert.Equal(t, "192.168.1.100", ip, "getClientIP()")
}

func TestGetClientIP_XForwardedFor(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 198.51.100.1")

	ip := getClientIP(req)

	// Should use first IP from X-Forwarded-For
	assert.Equal(t, "203.0.113.50", ip, "getClientIP() should use first IP from X-Forwarded-For")
}

func TestRenderTrialPage_HappLink(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	subURL := "https://vpn.site/sub/abc123"
	w := httptest.NewRecorder()
	srv.renderTrialPage(w, "abc123", subURL, "https://t.me/testbot?start=trial_abc123", 3)
	html := w.Body.String()

	// Check happ:// link is generated correctly
	expectedHappLink := "happ://add/" + subURL
	assert.Contains(t, html, expectedHappLink, "renderTrialPage() should contain happ link")
}

// === GetClientIP edge cases ===

func TestGetClientIP_XForwardedForMultiple(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 198.51.100.1, 192.0.2.1")

	ip := getClientIP(req)

	// Should use first IP from X-Forwarded-For
	assert.Equal(t, "203.0.113.50", ip, "getClientIP() should use first IP from X-Forwarded-For")
}

// Note: X-Real-IP is not checked by getClientIP - it only checks X-Forwarded-For

func TestGetClientIP_NoPort(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "192.168.1.100" // No port

	ip := getClientIP(req)

	assert.Equal(t, "192.168.1.100", ip, "getClientIP() should handle address without port")
}

func TestGetClientIP_Localhost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		remote   string
		forward  string
		expected string
	}{
		{"localhost IPv4", "127.0.0.1:12345", "8.8.8.8", "8.8.8.8"},
		{"localhost IPv6", "[::1]:12345", "", "::1"},
		{"private network NOT trusted", "192.168.1.1:12345", "10.0.0.5", "192.168.1.1"},
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
	t.Parallel()

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "") // Empty header

	ip := getClientIP(req)

	assert.Equal(t, "127.0.0.1", ip, "Should fall back to RemoteAddr when X-Forwarded-For is empty")
}

func TestGetClientIP_WhitespaceInXForwardedFor(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "  192.0.2.1  ,  198.51.100.1  ") // Extra whitespace

	ip := getClientIP(req)

	assert.Equal(t, "192.0.2.1", ip, "Should trim whitespace from IP")
}

// === Server Start/Stop tests ===

func TestServer_StartAndStop(t *testing.T) {
	t.Parallel()

	srv := NewServer(":0", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil) // :0 for random port

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

func TestServer_StartWithInvalidSubserverAccessLog(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		SubServerAccessLogPath: t.TempDir(),
	}
	srv := NewServer(":0", nil, cfg, bot.NewTestBotConfig(), nil, nil)

	ctx := context.Background()
	err := srv.Start(ctx)
	require.NoError(t, err, "Start() should not fail when optional subserver access log cannot be opened")

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Stop(stopCtx)
	assert.NoError(t, err, "Stop() should not return error")
}

func TestServer_StartPortInUseDoesNotCreateSubserverAccessLog(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "Failed to create listener")
	defer listener.Close()

	logPath := filepath.Join(t.TempDir(), "subserver.log")
	srv := NewServer(listener.Addr().String(), nil, &config.Config{
		SubServerAccessLogPath: logPath,
	}, bot.NewTestBotConfig(), nil, nil)

	err = srv.Start(context.Background())
	require.Error(t, err, "Start() should return error when port is already in use")
	assert.Contains(t, err.Error(), "failed to bind")

	_, err = os.Stat(logPath)
	assert.True(t, os.IsNotExist(err), "access log should not be created before successful bind")
}

func TestServer_StopWithoutStart(t *testing.T) {
	t.Parallel()

	srv := NewServer(":0", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	// Stop without start should not panic
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Stop(stopCtx)
	assert.NoError(t, err, "Stop() without Start() should not return error")
}

func TestIsLocalAddress_Loopback(t *testing.T) {
	t.Parallel()

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

func TestIsLocalAddress_NonLoopback(t *testing.T) {
	t.Parallel()

	// Only loopback addresses are trusted as proxy nodes.
	// Private IPs (10.x, 172.16.x, 192.168.x) are NOT trusted because
	// in cloud environments other VMs on the same VPC could spoof
	// X-Forwarded-For to bypass IP-based rate limiting.
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"10.x.x.x", "10.0.0.1", false},
		{"172.16.x.x", "172.16.0.1", false},
		{"192.168.x.x", "192.168.1.1", false},
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
	t.Parallel()

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "8.8.8.8:12345"

	ip := getClientIP(req)

	assert.Equal(t, "8.8.8.8", ip, "Should use remote addr when not local")
}

func TestGetClientIP_InvalidRemoteAddr(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/i/test", nil)
	req.RemoteAddr = "invalid"

	ip := getClientIP(req)

	assert.Equal(t, "invalid", ip, "Should fall back to raw remote addr")
}

func TestServer_Start_PortInUse(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "Failed to create listener")
	defer listener.Close()

	addr := listener.Addr().String()
	port := strings.Split(addr, ":")[1]

	srv := NewServer(":"+port, nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	ctx := context.Background()
	err = srv.Start(ctx)
	require.Error(t, err, "Start() should return error when port is already in use")
	assert.Contains(t, err.Error(), "failed to bind", "Error message should mention binding failure")

	listener.Close()
}

func TestGetClientIP_IPv6(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	cfg := &config.Config{
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	srv := NewServer(":8880", testutil.NewMockDatabaseService(), cfg, bot.NewTestBotConfig(), nil, nil)

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
				assert.Contains(t, html, `\u003cscript\u003ealert(\u0027xss\u0027)\u003c\/script\u003e`, "script should be JS-escaped in template context")
			},
		},
		{
			name:         "javascript in subURL",
			subURL:       "javascript:alert('xss')",
			telegramLink: "https://t.me/testbot?start=123",
			checkXSS: func(t *testing.T, html string) {
				assert.NotContains(t, html, `<a href="javascript:alert`, "raw javascript href should be escaped")
				assert.Contains(t, html, `javascript:alert(\u0027xss\u0027)`, "javascript should be JS-escaped")
			},
		},
		{
			name:         "onclick in subURL",
			subURL:       `test" onclick="alert('xss')"`,
			telegramLink: "https://t.me/testbot?start=123",
			checkXSS: func(t *testing.T, html string) {
				assert.NotContains(t, html, `onclick="alert`, "onclick should not appear unescaped")
			},
		},
		{
			name:         "script tag in telegramLink",
			subURL:       "test123",
			telegramLink: "https://t.me/testbot?start=<script>alert('xss')</script>",
			checkXSS: func(t *testing.T, html string) {
				assert.NotContains(t, html, `<script>alert('xss')</script>`, "raw script in telegramLink should not appear")
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
			w := httptest.NewRecorder()
			srv.renderTrialPage(w, "sub1", tt.subURL, tt.telegramLink, 3)
			tt.checkXSS(t, w.Body.String())
		})
	}
}

// ==================== HandleLogo Tests ====================

func TestHandleLogo_Success(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/static/logo.png", nil)
	w := httptest.NewRecorder()

	srv.handleLogo(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "public, max-age=86400", w.Header().Get("Cache-Control"))
	assert.NotEmpty(t, w.Body.Bytes(), "logo body should not be empty")
	assert.Equal(t, byte(0x89), w.Body.Bytes()[0], "PNG should start with 0x89")
	assert.Equal(t, byte('P'), w.Body.Bytes()[1], "PNG should have 'P' as second byte")
	assert.Equal(t, byte('N'), w.Body.Bytes()[2], "PNG should have 'N' as third byte")
	assert.Equal(t, byte('G'), w.Body.Bytes()[3], "PNG should have 'G' as fourth byte")
}

func TestHandleLogo_CacheHeaders(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/static/logo.png", nil)
	w := httptest.NewRecorder()

	srv.handleLogo(w, req)

	assert.Equal(t, "image/png", w.Header().Get("Content-Type"), "Content-Type should be image/png")
	assert.Equal(t, "public, max-age=86400", w.Header().Get("Cache-Control"), "Cache-Control should be set")
}

func TestHandleLogo_HEAD(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("HEAD", "/static/logo.png", nil)
	w := httptest.NewRecorder()

	srv.handleLogo(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Empty(t, w.Body.Bytes(), "HEAD should not return body")
}

func TestRenderTrialPage_TemplateRendersLogo(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	w := httptest.NewRecorder()
	srv.renderTrialPage(w, "sub1", "https://example.com/sub", "https://t.me/testbot?start=trial_sub1", 3)

	assert.Contains(t, w.Body.String(), `/static/logo.png`, "trial page should reference logo")
}

func TestRenderErrorPage_TemplateRendersLogo(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	w := httptest.NewRecorder()
	srv.renderErrorPage(w, "Test error")

	assert.Contains(t, w.Body.String(), `/static/logo.png`, "error page should reference logo")
}

func TestRenderTrialPage_Golden(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{
		SiteURL:            "https://example.com",
		TrialDurationHours: 24,
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)
	defer srv.db.Close()

	subID := "test_sub_123"
	subURL := "https://example.com/sub/abc123"
	telegramLink := "https://t.me/testbot?start=trial_abc123"
	trialHours := 24

	w := httptest.NewRecorder()
	srv.renderTrialPage(w, subID, subURL, telegramLink, trialHours)

	rendered := w.Body.String()

	assert.Contains(t, rendered, "<!DOCTYPE html>", "Should have HTML doctype")
	assert.Contains(t, rendered, "RS8 KVN", "Should have title")
	assert.Contains(t, rendered, "Добавить в Happ", "Should have Add to Happ button")
	assert.Contains(t, rendered, "Активировать", "Should have Activate button")
	assert.Contains(t, rendered, "24 часа", "Should have trial hours")
	assert.Contains(t, rendered, "Скопировать ссылку", "Should have copy link")
}

func TestRenderTrialPage_Structure(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	cfg := &config.Config{
		SiteURL:            "https://example.com",
		TrialDurationHours: 24,
	}

	srv := NewServer(":8880", mockDB, cfg, bot.NewTestBotConfig(), nil, nil)
	defer srv.db.Close()

	w := httptest.NewRecorder()
	srv.renderTrialPage(w, "test_sub_123", "https://example.com/sub/abc123", "https://t.me/testbot?start=trial_abc123", 24)

	rendered := w.Body.String()

	assert.Contains(t, rendered, "<!DOCTYPE html>", "Should have HTML doctype")
	assert.Contains(t, rendered, "RS8 KVN", "Should have title")
	assert.Contains(t, rendered, "Добавить в Happ", "Should have Add to Happ button")
	assert.Contains(t, rendered, "Активировать", "Should have Activate button")
	assert.Contains(t, rendered, "24 часа", "Should have trial hours")
}
