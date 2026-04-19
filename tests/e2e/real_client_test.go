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

	err := env.xuiClient.Login(ctx)
	require.NoError(t, err, "Initial login should succeed")

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
	addClientAttempts := 0
	var mu sync.Mutex

	env := setupRealXUIEnv(t, map[string]http.HandlerFunc{
		"/panel/api/server/status": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(xui.APIResponse{Success: false})
		},
		"/panel/api/inbounds/addClient": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			addClientAttempts++
			attempt := addClientAttempts
			mu.Unlock()

			if attempt == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(xui.APIResponse{Success: false, Msg: "unauthorized"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client added"})
		},
	})
	defer env.Close()

	ctx := context.Background()
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)

	env.xuiClient.TestForceSessionExpiry()

	result, err := env.subService.Create(ctx, 22345, "relogin_user")
	require.NoError(t, err, "Create should succeed after auto-relogin")
	require.NotNil(t, result)
	assert.Equal(t, 2, addClientAttempts, "addClient should be called twice (401 then retry)")
}

func TestE2E_RealClient_SessionVerificationSkipsLogin(t *testing.T) {
	loginCalls := 0
	var mu sync.Mutex

	env := setupRealXUIEnv(t, map[string]http.HandlerFunc{
		"/panel/api/server/status": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true})
		},
		"/login": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			loginCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true})
		},
	})
	defer env.Close()

	ctx := context.Background()
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, loginCalls)

	env.xuiClient.TestForceSessionExpiry()

	_, err = env.subService.Create(ctx, 32345, "verify_user")
	require.NoError(t, err)

	assert.Equal(t, 1, loginCalls, "Login should not be called when verifySession succeeds")
}

func TestE2E_RealClient_DNSErrorFastFail(t *testing.T) {
	t.Skip("Skipping flaky test: DNS resolution timing varies by OS/network environment")

	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	cfg := &config.Config{
		TelegramAdminID:         123456,
		TrafficLimitGB:          100,
		XUIInboundID:            1,
		XUIHost:                 "http://nonexistent.invalid.host:9999",
		XUISubPath:              "sub",
		SiteURL:                 "https://example.com",
		TelegramBotToken:        "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
		XUISessionMaxAgeMinutes: 15,
	}

	xuiClient, err := xui.NewClient("http://nonexistent.invalid.host:9999", "admin", "password", 15*time.Minute)
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
	loginCalls := 0
	var mu sync.Mutex
	loginStarted := make(chan struct{}, 1)

	env := setupRealXUIEnv(t, map[string]http.HandlerFunc{
		"/panel/api/server/status": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(xui.APIResponse{Success: false})
		},
		"/login": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			loginCalls++
			mu.Unlock()

			select {
			case loginStarted <- struct{}{}:
			default:
			}
			time.Sleep(10 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true})
		},
	})
	defer env.Close()

	ctx := context.Background()
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)

	env.xuiClient.TestForceSessionExpiry()

	<-loginStarted
	time.Sleep(20 * time.Millisecond)

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

	assert.Equal(t, 2, loginCalls, "Singleflight should deduplicate concurrent logins (1 initial + 1 shared)")
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
	err := env.xuiClient.Login(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 502")
}

func TestE2E_RealClient_AutoReloginViaCircuitBreaker(t *testing.T) {
	loginAttempts := 0
	var mu sync.Mutex

	env := setupRealXUIEnv(t, map[string]http.HandlerFunc{
		"/panel/api/server/status": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(xui.APIResponse{Success: false})
		},
		"/login": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			loginAttempts++
			mu.Unlock()
			mu.Lock()
			count := loginAttempts
			mu.Unlock()
			if count <= 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(xui.APIResponse{Success: false, Msg: "unavailable"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Login successful"})
		},
		"/panel/api/inbounds/addClient": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client added"})
		},
	})
	defer env.Close()

	ctx := context.Background()
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)

	env.xuiClient.TestForceSessionExpiry()

	result, err := env.subService.Create(ctx, 62345, "cb_relogin_user")
	require.NoError(t, err, "Create should succeed after auto-relogin retry")
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, loginAttempts, 2, "Auto-relogin should have been attempted with retry")
}

func TestE2E_RealClient_MultipleOperationsNoRelogin(t *testing.T) {
	loginCalls := 0
	var mu sync.Mutex

	env := setupRealXUIEnv(t, map[string]http.HandlerFunc{
		"/panel/api/server/status": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true})
		},
		"/login": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			loginCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true})
		},
	})
	defer env.Close()

	ctx := context.Background()
	err := env.xuiClient.Login(ctx)
	require.NoError(t, err)
	initialLogins := loginCalls

	_, err = env.subService.Create(ctx, 72345, "multi_user1")
	require.NoError(t, err)

	_, err = env.subService.Create(ctx, 72346, "multi_user2")
	require.NoError(t, err)

	_, err = env.subService.Create(ctx, 72347, "multi_user3")
	require.NoError(t, err)

	assert.Equal(t, initialLogins, loginCalls, "No re-logins within valid session")
}
