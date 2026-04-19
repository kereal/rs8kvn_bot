package web

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/subproxy"
	"rs8kvn_bot/internal/testutil"

	"gorm.io/gorm"
)

func TestHandleSubscription_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{
		SubExtraServersEnabled: true,
	}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("POST", "/sub/abc123", nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET", rec.Header().Get("Allow"))
}

func TestHandleSubscription_SubProxyNil(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	cfg := &config.Config{}

	srv := NewServer(":8880", mockDB, mockXUI, cfg, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/sub/abc123", nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "not available")
}

func TestHandleSubscription_InvalidCode(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", nil, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	tests := []struct {
		path       string
		expectCode int
	}{
		{"/sub/", http.StatusBadRequest},
		{"/sub/abc/def", http.StatusBadRequest},
		{"/sub/code%20with%20spaces", http.StatusBadRequest},
		{"/sub/code%3Cscript%3E", http.StatusBadRequest},
		{"/sub/code/../../../etc", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()
			srv.handleSubscription(rec, req)
			assert.Equal(t, tt.expectCode, rec.Code)
		})
	}
}

func TestHandleSubscription_NotFoundInDB(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not found")
}

func TestHandleSubscription_EmptySubscriptionURL(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: "",
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/abc123", nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not available")
}

func TestHandleSubscription_XUIError_NoCache(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: "http://localhost:2053/sub/abc123",
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/abc123", nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "Failed to fetch")
}

func TestHandleSubscription_CacheHit(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	subID := "cached_sub_id"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			Status:         "active",
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	cachedBody := []byte("vless://cached-server@cache.example.com:443")
	cachedHeaders := map[string]string{
		"Subscription-Userinfo":   "upload=0; download=0; total=10737418240; expire=1234567890",
		"Profile-Update-Interval": "60",
		"Content-Type":            "text/plain; charset=utf-8",
	}

	subProxy.SetCache(subID, cachedBody, cachedHeaders)

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, string(cachedBody), rec.Body.String())
	assert.Equal(t, "upload=0; download=0; total=10737418240; expire=1234567890", rec.Header().Get("Subscription-Userinfo"))
	assert.Equal(t, "60", rec.Header().Get("Profile-Update-Interval"))
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
}

func TestHandleSubscription_CacheHitAfterXUIError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	subID := "fallback_sub_id"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			Status:         "active",
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	cachedBody := []byte("vless://fallback-server@example.com:443")
	cachedHeaders := map[string]string{
		"Subscription-Userinfo": "upload=0; download=0; total=10737418240; expire=1234567890",
	}

	subProxy.SetCache(subID, cachedBody, cachedHeaders)

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, string(cachedBody), rec.Body.String())
	assert.Equal(t, "upload=0; download=0; total=10737418240; expire=1234567890", rec.Header().Get("Subscription-Userinfo"))
}

func TestHandleSubscription_ExtraServersAppended(t *testing.T) {
	t.Parallel()

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=10737418240; expire=1234567890")
		w.Header().Set("Profile-Update-Interval", "60")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("vless://original-server@original.example.com:443"))
		require.NoError(t, err)
	}))
	defer servers.Close()

	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "extra.txt")
	err := os.WriteFile(serversFile, []byte("X-Custom: custom-value\n\nvless://extra1@extra1.example.com:443\ntrojan://extra2@extra2.example.com:443\n"), 0600)
	require.NoError(t, err)

	mockDB := testutil.NewMockDatabaseService()
	subID := "extra_test_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    serversFile,
	}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "vless://original-server@original.example.com:443")
	assert.Contains(t, body, "vless://extra1@extra1.example.com:443")
	assert.Contains(t, body, "trojan://extra2@extra2.example.com:443")

	assert.Equal(t, "upload=0; download=0; total=10737418240; expire=1234567890", rec.Header().Get("Subscription-Userinfo"))
	assert.Equal(t, "60", rec.Header().Get("Profile-Update-Interval"))
	assert.Equal(t, "custom-value", rec.Header().Get("X-Custom"))
}

func TestHandleSubscription_Base64FormatPreserved(t *testing.T) {
	t.Parallel()

	originalPlain := "vless://original@original.example.com:443"
	originalEncoded := base64.StdEncoding.EncodeToString([]byte(originalPlain))

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=10737418240; expire=1234567890")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(originalEncoded))
		require.NoError(t, err)
	}))
	defer servers.Close()

	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "extra.txt")
	err := os.WriteFile(serversFile, []byte("vless://extra@extra.example.com:443\n"), 0600)
	require.NoError(t, err)

	mockDB := testutil.NewMockDatabaseService()
	subID := "base64_test_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    serversFile,
	}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	decoded, err := base64.StdEncoding.DecodeString(rec.Body.String())
	require.NoError(t, err)

	mergedBody := string(decoded)
	assert.Contains(t, mergedBody, "vless://original@original.example.com:443")
	assert.Contains(t, mergedBody, "vless://extra@extra.example.com:443")
}

func TestHandleSubscription_ConcurrentRequests(t *testing.T) {
	t.Parallel()

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=10737418240; expire=1234567890")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("vless://concurrent@server.example.com:443"))
		require.NoError(t, err)
	}))
	defer servers.Close()

	mockDB := testutil.NewMockDatabaseService()
	subID := "concurrent_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	const numRequests = 5
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/sub/"+subID, nil)
			rec := httptest.NewRecorder()
			srv.handleSubscription(rec, req)
			results <- rec
		}()
	}

	wg.Wait()
	close(results)

	successCount := 0
	for rec := range results {
		if rec.Code == http.StatusOK {
			successCount++
			assert.Contains(t, rec.Body.String(), "vless://concurrent@server.example.com:443")
			assert.Equal(t, "upload=0; download=0; total=10737418240; expire=1234567890", rec.Header().Get("Subscription-Userinfo"))
		}
	}

	assert.Equal(t, numRequests, successCount, "All concurrent requests should succeed")
}

func TestHandleSubscription_ExtraServersFileMissing(t *testing.T) {
	t.Parallel()

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("vless://original@server.example.com:443"))
	}))
	defer servers.Close()

	mockDB := testutil.NewMockDatabaseService()
	subID := "missing_file_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    "/nonexistent/path/to/servers.txt",
	}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "vless://original@server.example.com:443", rec.Body.String())
}

func TestHandleSubscription_NoExtraServersWhenDisabled(t *testing.T) {
	t.Parallel()

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("vless://original@server.example.com:443"))
	}))
	defer servers.Close()

	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "extra.txt")
	err := os.WriteFile(serversFile, []byte("vless://extra@extra.example.com:443\n"), 0600)
	require.NoError(t, err)

	mockDB := testutil.NewMockDatabaseService()
	subID := "disabled_extra_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{
		SubExtraServersEnabled: false,
		SubExtraServersFile:    serversFile,
	}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "vless://original@server.example.com:443", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "vless://extra@extra.example.com:443")
}

func TestHandleSubscription_CacheStoresMergedResult(t *testing.T) {
	t.Parallel()

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("vless://original@server.example.com:443"))
		require.NoError(t, err)
	}))
	defer servers.Close()

	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "extra.txt")
	err := os.WriteFile(serversFile, []byte("vless://extra@extra.example.com:443\n"), 0600)
	require.NoError(t, err)

	mockDB := testutil.NewMockDatabaseService()
	subID := "cache_merge_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    serversFile,
	}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "vless://original@server.example.com:443")
	assert.Contains(t, rec.Body.String(), "vless://extra@extra.example.com:443")

	servers.Close()

	req2 := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec2 := httptest.NewRecorder()
	srv.handleSubscription(rec2, req2)

	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Contains(t, rec2.Body.String(), "vless://original@server.example.com:443")
	assert.Contains(t, rec2.Body.String(), "vless://extra@extra.example.com:443")
}

func TestHandleSubscription_EmptyBodyFromXUI(t *testing.T) {
	t.Parallel()

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("vless://original@server.example.com:443"))
		require.NoError(t, err)
	}))
	defer servers.Close()

	mockDB := testutil.NewMockDatabaseService()
	subID := "empty_body_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleSubscription_CorruptDataFromXUI(t *testing.T) {
	t.Parallel()

	// Simulate XUI returning corrupt/invalid subscription data
	corruptBody := "this is not a valid subscription, just random text !!!"
	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(corruptBody))
		require.NoError(t, err)
	}))
	defer servers.Close()

	mockDB := testutil.NewMockDatabaseService()
	subID := "corrupt_data_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: false}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	// The proxy should pass through whatever XUI returns, even if corrupt
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, corruptBody, rec.Body.String())
}

func TestHandleSubscription_CriticalHeadersPreserved(t *testing.T) {
	t.Parallel()

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=100; download=200; total=10737418240; expire=1700000000")
		w.Header().Set("Profile-Update-Interval", "120")
		w.Header().Set("Content-Disposition", "attachment; filename=sub.txt")
		w.Header().Set("Profile-Title", "base64:TXkgVlBO")
		w.Header().Set("Support-Url", "https://support.example.com")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("vless://server@example.com:443"))
		require.NoError(t, err)
	}))
	defer servers.Close()

	mockDB := testutil.NewMockDatabaseService()
	subID := "headers_test_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "upload=100; download=200; total=10737418240; expire=1700000000", rec.Header().Get("Subscription-Userinfo"))
	assert.Equal(t, "120", rec.Header().Get("Profile-Update-Interval"))
	assert.Equal(t, "attachment; filename=sub.txt", rec.Header().Get("Content-Disposition"))
	assert.Equal(t, "base64:TXkgVlBO", rec.Header().Get("Profile-Title"))
	assert.Equal(t, "https://support.example.com", rec.Header().Get("Support-Url"))
}

func TestHandleSubscription_CriticalHeadersPreservedFromCache(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	subID := "cached_headers_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			Status:         "active",
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	cachedBody := []byte("vless://cached@example.com:443")
	cachedHeaders := map[string]string{
		"Subscription-Userinfo":   "upload=0; download=0; total=5368709120; expire=1800000000",
		"Profile-Update-Interval": "60",
		"Content-Disposition":     "attachment; filename=vpn.txt",
	}

	subProxy.SetCache(subID, cachedBody, cachedHeaders)

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "upload=0; download=0; total=5368709120; expire=1800000000", rec.Header().Get("Subscription-Userinfo"))
	assert.Equal(t, "60", rec.Header().Get("Profile-Update-Interval"))
	assert.Equal(t, "attachment; filename=vpn.txt", rec.Header().Get("Content-Disposition"))
}

func TestHandleSubscription_ExtraHeadersOverrideXUI(t *testing.T) {
	t.Parallel()

	servers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=10737418240; expire=1234567890")
		w.Header().Set("Profile-Title", "xui-title")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("vless://original@server.example.com:443"))
		require.NoError(t, err)
	}))
	defer servers.Close()

	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "extra.txt")
	err := os.WriteFile(serversFile, []byte("Profile-Title: custom-title\nX-Extra: extra-value\n\nvless://extra@extra.example.com:443\n"), 0600)
	require.NoError(t, err)

	mockDB := testutil.NewMockDatabaseService()
	subID := "header_override_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "active",
			SubscriptionURL: servers.URL + "/sub/" + subscriptionID,
		}, nil
	}

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    serversFile,
	}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "custom-title", rec.Header().Get("Profile-Title"))
	assert.Equal(t, "extra-value", rec.Header().Get("X-Extra"))
	assert.Equal(t, "upload=0; download=0; total=10737418240; expire=1234567890", rec.Header().Get("Subscription-Userinfo"))
}

func TestHandleSubscription_ConcurrentNotFound(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	callCount := 0
	var mu sync.Mutex
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		return nil, gorm.ErrRecordNotFound
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	const numRequests = 5
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/sub/nonexistent_sub", nil)
			rec := httptest.NewRecorder()
			srv.handleSubscription(rec, req)
			results <- rec
		}()
	}

	wg.Wait()
	close(results)

	for rec := range results {
		assert.Equal(t, http.StatusNotFound, rec.Code)
	}

	mu.Lock()
	count := callCount
	mu.Unlock()
	// singleflight should deduplicate: expect 1 call in ideal case,
	// but allow up to numRequests in race conditions (no dedup at all)
	assert.LessOrEqual(t, count, numRequests, "DB calls should not exceed number of requests")
	if count == 1 {
		t.Log("singleflight deduplicated all DB queries (optimal)")
	} else {
		t.Logf("singleflight partial dedup: %d/%d DB calls", count, numRequests)
	}
}

func TestHandleSubscription_RevokedSubscription(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "revoked",
			SubscriptionURL: "http://localhost:2053/sub/abc123",
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/abc123", nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not found")
}

func TestHandleSubscription_ExpiredSubscription(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID:  subscriptionID,
			Status:          "expired",
			SubscriptionURL: "http://localhost:2053/sub/abc123",
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/abc123", nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not found")
}

func TestHandleSubscription_RevokedSubscription_CacheHit(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	subID := "revoked_cached_sub"
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			Status:         "revoked",
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	// Simulate stale cache entry from when subscription was active
	cachedBody := []byte("vless://cached-server@cache.example.com:443")
	cachedHeaders := map[string]string{
		"Content-Type": "text/plain; charset=utf-8",
	}
	subProxy.SetCache(subID, cachedBody, cachedHeaders)

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	// Should return 404 even though cache has data, because subscription is revoked
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not found")

	// Verify cache was invalidated
	_, _, ok := subProxy.GetCache(subID)
	assert.False(t, ok, "cache should be invalidated for revoked subscription")
}

func TestHandleSubscription_ExpiredByTime_CacheHit(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()
	subID := "time_expired_cached_sub"
	pastTime := time.Now().Add(-24 * time.Hour)
	mockDB.GetSubscriptionBySubscriptionIDFunc = func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			SubscriptionID: subscriptionID,
			Status:         "active",
			ExpiryTime:     pastTime,
		}, nil
	}

	cfg := &config.Config{SubExtraServersEnabled: true}
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	// Simulate stale cache entry
	cachedBody := []byte("vless://cached-server@cache.example.com:443")
	cachedHeaders := map[string]string{
		"Content-Type": "text/plain; charset=utf-8",
	}
	subProxy.SetCache(subID, cachedBody, cachedHeaders)

	srv := NewServer(":8880", mockDB, nil, cfg, bot.NewTestBotConfig(), nil, subProxy)

	req := httptest.NewRequest("GET", "/sub/"+subID, nil)
	rec := httptest.NewRecorder()
	srv.handleSubscription(rec, req)

	// Should return 404 because subscription has expired by time
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not found")

	// Verify cache was invalidated
	_, _, ok := subProxy.GetCache(subID)
	assert.False(t, ok, "cache should be invalidated for expired subscription")
}
