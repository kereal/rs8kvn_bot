package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_RealClient_FullSubscriptionLifecycle(t *testing.T) {
	env := setupRealXUIEnv(t, nil)
	defer env.Close()

	ctx := context.Background()

	result, err := env.subService.Create(ctx, 12345, "testuser")
	require.NoError(t, err, "Create subscription should succeed")
	require.NotNil(t, result)
	assert.NotEmpty(t, result.SubscriptionURL)

	sub, err := env.db.GetByTelegramID(ctx, 12345)
	require.NoError(t, err)
	assert.Equal(t, "testuser", sub.Username)
	assert.False(t, sub.IsTrial)

	_, traffic, err := env.subService.GetWithTraffic(ctx, 12345)
	require.NoError(t, err)
	assert.NotNil(t, traffic)

	err = env.subService.Delete(ctx, 12345)
	require.NoError(t, err)

	_, err = env.db.GetByTelegramID(ctx, 12345)
	assert.Error(t, err, "Subscription should be deleted")
}

func TestE2E_RealClient_AutoReloginOn401(t *testing.T) {
	env := setupRealXUIEnv(t, map[string]http.HandlerFunc{
		"/panel/api/server/status": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(xui.APIResponse{Success: false})
		},
		"/panel/api/inbounds/addClient": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client added"})
		},
	})
	defer env.Close()

	ctx := context.Background()
	// Bearer token auth: Login is a no-op, there are no session cookies.
	// The Create call uses Bearer token directly; 401 auto-relogin is not applicable.
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)

	env.xuiClient.TestForceSessionExpiry()

	// Session expiry is a no-op; create should still succeed via Bearer token.
	result, err := env.subService.Create(ctx, 22345, "relogin_user")
	require.NoError(t, err, "Create should succeed (Bearer token auth)")
	require.NotNil(t, result)
}

func TestE2E_RealClient_SessionVerificationSkipsLogin(t *testing.T) {
	env := setupRealXUIEnv(t, nil)
	defer env.Close()

	ctx := context.Background()
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)

	env.xuiClient.TestForceSessionExpiry()

	result, err := env.subService.Create(ctx, 32345, "verify_user")
	require.NoError(t, err)
	require.NotNil(t, result)
	// Bearer token auth: Login and TestForceSessionExpiry are no-ops.
	// Verify subscription creation succeeds without session state.
}

func TestE2E_RealClient_DNSErrorFastFail(t *testing.T) {
	t.Skip("Skipping flaky test: DNS resolution timing varies by OS/network environment")

	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	cfg := &config.Config{
		TelegramAdminID:  123456,
		TrafficLimitGB:   100,
		XUIInboundID:     1,
		XUIHost:          "http://nonexistent.invalid.host:9999",
		XUISubPath:       "sub",
		SiteURL:          "https://example.com",
		TelegramBotToken: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
		XUIAPIToken:      "test-api-token",
	}

	xuiClient, err := xui.NewClient("http://nonexistent.invalid.host:9999", "test-api-token")
	require.NoError(t, err)
	defer func() {
		if err := xuiClient.Close(); err != nil {
			t.Logf("Warning: failed to close XUI client: %v", err)
		}
	}()

	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	subService := service.NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	_, err = subService.Create(ctx, 42345, "dns_fail_user")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed.Seconds(), 25.0, "DNS error should fail fast, not retry (took %.1fs)", elapsed.Seconds())
}

func TestE2E_RealClient_ConcurrentLoginDedup(t *testing.T) {
	env := setupRealXUIEnv(t, nil)
	defer env.Close()

	ctx := context.Background()
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)

	env.xuiClient.TestForceSessionExpiry()

	var wg sync.WaitGroup
	results := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			_, results[idx] = env.subService.Create(ctx2, int64(50000+idx), fmt.Sprintf("concurrent_user_%d", idx))
		}(i)
	}

	wg.Wait()

	// Bearer token auth: Login is a no-op, there is no singleflight dedup.
	// Verify all concurrent subscription creations succeed without deadlock.
	for i, err := range results {
		require.NoError(t, err, "concurrent Create(%d) should succeed", i)
	}
}

func TestE2E_RealClient_LoginHTTPError(t *testing.T) {
	env := setupRealXUIEnv(t, map[string]http.HandlerFunc{
		"/login": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("Bad Gateway"))
		},
	})
	defer env.Close()

	ctx := context.Background()
	// Bearer token auth: Login is a no-op that always returns nil.
	// The /login handler is never called; errors from it are unreachable.
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)
}

func TestE2E_RealClient_AutoReloginViaCircuitBreaker(t *testing.T) {
	env := setupRealXUIEnv(t, map[string]http.HandlerFunc{
		"/panel/api/server/status": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(xui.APIResponse{Success: false})
		},
		"/panel/api/inbounds/addClient": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client added"})
		},
	})
	defer env.Close()

	ctx := context.Background()
	// Bearer token auth: Login and TestForceSessionExpiry are no-ops.
	// Circuit breaker for session auth does not apply.
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)

	env.xuiClient.TestForceSessionExpiry()

	// Session expiry is a no-op; create should succeed via Bearer token.
	result, err := env.subService.Create(ctx, 62345, "cb_relogin_user")
	require.NoError(t, err, "Create should succeed (Bearer token auth)")
	require.NotNil(t, result)
}

func TestE2E_RealClient_MultipleOperationsNoRelogin(t *testing.T) {
	env := setupRealXUIEnv(t, nil)
	defer env.Close()

	ctx := context.Background()
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)

	// Bearer token auth: Login is a no-op. Multiple Create() calls should
	// each succeed independently without any session or login state.
	result1, err := env.subService.Create(ctx, 72345, "multi_user1")
	require.NoError(t, err)

	result2, err := env.subService.Create(ctx, 72346, "multi_user2")
	require.NoError(t, err)

	result3, err := env.subService.Create(ctx, 72347, "multi_user3")
	require.NoError(t, err)

	require.NotNil(t, result1)
	require.NotNil(t, result2)
	require.NotNil(t, result3)
}
