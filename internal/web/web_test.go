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

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
}

func TestRenderTrialPage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		subURL       string
		telegramLink string
		mockDB       bool
		trialHours   int
		check        func(t *testing.T, html string)
	}{
		{
			name:         "basic elements",
			subURL:       "https://vpn.site/sub/sub123",
			telegramLink: "https://t.me/testbot?start=trial_sub123",
			mockDB:       false,
			trialHours:   3,
			check: func(t *testing.T, html string) {
				for _, expected := range []string{
					"<!DOCTYPE html>", "RS8 KVN", "Добавить в Happ",
					"happ://add/", "Активировать",
					"https://t.me/testbot?start=trial_sub123",
					"3 часа", "Срок действия", "copyToClipboard",
					"play.google.com", "apps.apple.com",
				} {
					assert.Contains(t, html, expected)
				}
			},
		},
		{
			name:         "happ link",
			subURL:       "https://vpn.site/sub/abc123",
			telegramLink: "https://t.me/testbot?start=trial_abc123",
			mockDB:       false,
			trialHours:   3,
			check: func(t *testing.T, html string) {
				assert.Contains(t, html, "happ://add/https://vpn.site/sub/abc123")
			},
		},
		{
			name:         "xss protection - script tag",
			subURL:       "test<script>alert('xss')</script>",
			telegramLink: "https://t.me/testbot?start=123",
			mockDB:       true,
			trialHours:   3,
			check: func(t *testing.T, html string) {
				assert.NotContains(t, html, "<script>alert('xss')</script>")
				assert.Contains(t, html, `\u003cscript\u003ealert(\u0027xss\u0027)\u003c\/script\u003e`)
			},
		},
		{
			name:         "xss protection - javascript href",
			subURL:       "javascript:alert('xss')",
			telegramLink: "https://t.me/testbot?start=123",
			mockDB:       true,
			trialHours:   3,
			check: func(t *testing.T, html string) {
				assert.NotContains(t, html, `<a href="javascript:alert`)
				assert.Contains(t, html, `javascript:alert(\u0027xss\u0027)`)
			},
		},
		{
			name:         "xss protection - onclick",
			subURL:       `test" onclick="alert('xss')"`,
			telegramLink: "https://t.me/testbot?start=123",
			mockDB:       true,
			trialHours:   3,
			check: func(t *testing.T, html string) {
				assert.NotContains(t, html, `onclick="alert`)
			},
		},
		{
			name:         "logo reference",
			subURL:       "https://example.com/sub",
			telegramLink: "https://t.me/testbot?start=trial_sub1",
			mockDB:       false,
			trialHours:   3,
			check: func(t *testing.T, html string) {
				assert.Contains(t, html, `/static/logo.png`)
			},
		},
		{
			name:         "golden - 24h",
			subURL:       "https://example.com/sub/abc123",
			telegramLink: "https://t.me/testbot?start=trial_abc123",
			mockDB:       true,
			trialHours:   24,
			check: func(t *testing.T, html string) {
				assert.Contains(t, html, "<!DOCTYPE html>")
				assert.Contains(t, html, "RS8 KVN")
				assert.Contains(t, html, "Добавить в Happ")
				assert.Contains(t, html, "Активировать")
				assert.Contains(t, html, "24 часа")
				assert.Contains(t, html, "Скопировать ссылку")
			},
		},
		{
			name:         "structure",
			subURL:       "https://example.com/sub/abc123",
			telegramLink: "https://t.me/testbot?start=trial_abc123",
			mockDB:       true,
			trialHours:   24,
			check: func(t *testing.T, html string) {
				assert.Contains(t, html, "<!DOCTYPE html>")
				assert.Contains(t, html, "RS8 KVN")
				assert.Contains(t, html, "Добавить в Happ")
				assert.Contains(t, html, "Активировать")
				assert.Contains(t, html, "24 часа")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var srv *Server
			if tt.mockDB {
				mockDB := testutil.NewDatabaseService()
				cfg := &config.Config{SiteURL: "https://example.com", TrialDurationHours: 24}
				mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
					return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824}, nil
				}
				srv = NewServer(":8880", mockDB, cfg, "testbot", nil, nil)
				defer mockDB.Close()
			} else {
				cfg := &config.Config{SiteURL: "https://vpn.site", TrialDurationHours: 3}
				srv = NewServer(":8880", nil, cfg, "testbot", nil, nil)
			}
			w := httptest.NewRecorder()
			srv.renderTrialPage(w, "sub1", tt.subURL, tt.telegramLink, tt.trialHours)
			tt.check(t, w.Body.String())
		})
	}
}

// === Server Start/Stop tests ===

func TestServer_StartAndStop(t *testing.T) {
	t.Parallel()

	srv := NewServer(":0", nil, &config.Config{}, "testbot", nil, nil) // :0 for random port

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
	srv := NewServer(":0", nil, cfg, "testbot", nil, nil)

	ctx := context.Background()
	err := srv.Start(ctx)
	require.NoError(t, err, "Start() should not fail when optional subserver access log cannot be opened")

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Stop(stopCtx)
	assert.NoError(t, err, "Stop() should not return error")
}

func TestServer_StopWithoutStart(t *testing.T) {
	t.Parallel()

	srv := NewServer(":0", nil, &config.Config{}, "testbot", nil, nil)

	// Stop without start should not panic
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Stop(stopCtx)
	assert.NoError(t, err, "Stop() without Start() should not return error")
}

// TestServer_Stop_AlwaysShutdownsHTTPServer verifies the audit #8 fix:
// even when the access-log CloseWithContext fails (here: via an already-canceled
// context), HTTP server Shutdown must still be invoked and release the listener.
// Before the fix, a CloseWithContext error short-circuited Shutdown, leaving the
// HTTP server accepting connections.
func TestServer_Stop_AlwaysShutdownsHTTPServer(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "subserver.log")
	srv := NewServer(":0", nil, &config.Config{
		SubServerAccessLogPath: logPath,
	}, "testbot", nil, nil)

	require.NoError(t, srv.Start(context.Background()))

	// Capture the bound address before stopping.
	addr := srv.listenerAddr
	require.NotEmpty(t, addr)

	// Use an already-canceled context so CloseWithContext fails with context.Canceled.
	// Shutdown receives the same ctx and will also fail, but it MUST still be called
	// (the listener must be released) — that is the regression we guard against.
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := srv.Stop(stopCtx)
	// We expect an aggregate error (log close + shutdown both failed), not nil.
	require.Error(t, err, "Stop should report errors from both log-close and shutdown")

	// The key assertion: the listener must be released even though CloseWithContext failed.
	// Attempt a new bind on the same address; if Shutdown was skipped, this would block/fail.
	conn, dialErr := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if conn != nil {
		conn.Close()
	}
	assert.Error(t, dialErr, "listener must be released after Stop even if access-log close failed")
}

func TestIsLocalAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"127.0.0.1", "127.0.0.1", true},
		{"localhost IPv4", "127.0.0.2", true},
		{"localhost IPv6", "::1", true},
		{"10.x.x.x", "10.0.0.1", true},
		{"172.16.x.x", "172.16.0.1", true},
		{"172.19.x.x docker gw", "172.19.0.1", true},
		{"192.168.x.x", "192.168.1.1", true},
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

func TestGetClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		remote   string
		forward  string
		realIP   string
		expected string
	}{
		{"direct", "192.168.1.100:12345", "", "", "192.168.1.100"},
		{"x-forwarded-for single", "127.0.0.1:12345", "203.0.113.50, 198.51.100.1", "", "198.51.100.1"},
		{"x-forwarded-for multiple", "127.0.0.1:12345", "203.0.113.50, 198.51.100.1, 192.0.2.1", "", "192.0.2.1"},
		{"no port", "192.168.1.100", "", "", "192.168.1.100"},
		{"localhost IPv4", "127.0.0.1:12345", "8.8.8.8", "", "8.8.8.8"},
		{"localhost IPv6", "[::1]:12345", "", "", "::1"},
		{"private network trusted (docker gw)", "172.19.0.1:12345", "10.0.0.5", "", "10.0.0.5"},
		{"public IP not trusted", "8.8.8.8:12345", "1.2.3.4", "", "8.8.8.8"},
		{"empty X-Forwarded-For fallback", "127.0.0.1:12345", "", "", "127.0.0.1"},
		{"whitespace in X-Forwarded-For", "127.0.0.1:12345", "  192.0.2.1  ,  198.51.100.1  ", "", "198.51.100.1"},
		{"non-local remote addr", "8.8.8.8:12345", "", "", "8.8.8.8"},
		{"invalid remote addr", "invalid", "", "", "invalid"},
		{"IPv6 single", "[::1]:12345", "2001:db8::1", "", "2001:db8::1"},
		{"IPv6 with brackets port", "[::1]:12345", "[2001:db8::1]:8080", "", "[2001:db8::1]:8080"},
		{"IPv6 mixed with IPv4", "[::1]:12345", "2001:db8::1, 192.168.1.1", "", "192.168.1.1"},
		{"IPv6 loopback", "[::1]:12345", "::1", "", "::1"},
		{"x-real-ip preferred", "172.19.0.1:12345", "10.0.0.5", "203.0.113.9", "203.0.113.9"},
		{"x-real-ip docker gw", "172.19.0.1:12345", "", "203.0.113.9", "203.0.113.9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/i/test", nil)
			req.RemoteAddr = tt.remote
			if tt.forward != "" {
				req.Header.Set("X-Forwarded-For", tt.forward)
			}
			if tt.realIP != "" {
				req.Header.Set("X-Real-IP", tt.realIP)
			}
			ip := getClientIP(req)
			assert.Equal(t, tt.expected, ip)
		})
	}
}

func TestHandleLogo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		method       string
		expectedCode int
		checks       func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name:         "success",
			method:       http.MethodGet,
			expectedCode: http.StatusOK,
			checks: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
				assert.Equal(t, "public, max-age=86400", w.Header().Get("Cache-Control"))
				assert.NotEmpty(t, w.Body.Bytes())
				assert.Equal(t, byte(0x89), w.Body.Bytes()[0])
				assert.Equal(t, byte('P'), w.Body.Bytes()[1])
				assert.Equal(t, byte('N'), w.Body.Bytes()[2])
				assert.Equal(t, byte('G'), w.Body.Bytes()[3])
			},
		},
		{
			name:         "cache headers",
			method:       http.MethodGet,
			expectedCode: http.StatusOK,
			checks: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
				assert.Equal(t, "public, max-age=86400", w.Header().Get("Cache-Control"))
			},
		},
		{
			name:         "HEAD no body",
			method:       http.MethodHead,
			expectedCode: http.StatusOK,
			checks: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
				assert.Empty(t, w.Body.Bytes())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)
			req := httptest.NewRequest(tt.method, "/static/logo.png", nil)
			w := httptest.NewRecorder()
			srv.handleLogo(w, req)
			assert.Equal(t, tt.expectedCode, w.Code)
			tt.checks(t, w)
		})
	}
}

func TestServer_StartPortInUse(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "Failed to create listener")
	defer listener.Close()

	addr := listener.Addr().String()
	port := strings.Split(addr, ":")[1]

	t.Run("start_binds_fails", func(t *testing.T) {
		srv := NewServer(":"+port, nil, &config.Config{}, "testbot", nil, nil)
		ctx := context.Background()
		err := srv.Start(ctx)
		require.Error(t, err, "Start() should return error when port is already in use")
		assert.Contains(t, err.Error(), "failed to bind")
	})

	t.Run("port_inuse_does_not_create_subserver_log", func(t *testing.T) {
		listener2, err2 := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err2)
		defer listener2.Close()

		logPath := filepath.Join(t.TempDir(), "subserver.log")
		srv := NewServer(listener2.Addr().String(), nil, &config.Config{
			SubServerAccessLogPath: logPath,
		}, "testbot", nil, nil)

		err := srv.Start(context.Background())
		require.Error(t, err, "Start() should return error when port is already in use")
		assert.Contains(t, err.Error(), "failed to bind")

		_, statErr := os.Stat(logPath)
		assert.True(t, os.IsNotExist(statErr), "access log should not be created before successful bind")
	})
}

func TestRenderErrorPage(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, nil, "testbot", nil, nil)

	w := httptest.NewRecorder()
	srv.renderErrorPage(w, "Тестовая ошибка")
	html := w.Body.String()

	assert.Contains(t, html, "Тестовая ошибка", "renderErrorPage() should contain error message")
	assert.Contains(t, html, "<!DOCTYPE html>", "renderErrorPage() should be valid HTML")
}
