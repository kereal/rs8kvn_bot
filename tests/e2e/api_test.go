package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/web"
	"rs8kvn_bot/internal/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_APISubscriptions_Success tests GET /api/v1/subscriptions with valid auth
// and returns active subscriptions from the database.
func TestE2E_APISubscriptions_Success(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	// Create a subscription in the database
	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "550e8400-e29b-41d4-a716-446655440000",
		SubscriptionID: "sub-token-abc123",
		InboundID:      1,
		TrafficLimit:   30 * 1024 * 1024 * 1024,
		Status:         "active",
	}
	err := env.db.CreateSubscription(context.Background(), sub)
	require.NoError(t, err)

	env.cfg.APIToken = "test-api-token-12345"
	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	// Make authenticated request
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/subscriptions", addr), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-api-token-12345")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 1)

	subMap := subs[0].(map[string]interface{})
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", subMap["id"])
	assert.Equal(t, "testuser", subMap["email"])
	assert.Equal(t, true, subMap["enabled"])
	assert.Equal(t, "sub-token-abc123", subMap["subscription_token"])
}

// TestE2E_APISubscriptions_EmptyList tests GET /api/v1/subscriptions with no subscriptions.
func TestE2E_APISubscriptions_EmptyList(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	env.cfg.APIToken = "test-api-token"
	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/subscriptions", addr), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-api-token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 0)
}

// TestE2E_APISubscriptions_Unauthorized tests GET /api/v1/subscriptions without auth.
func TestE2E_APISubscriptions_Unauthorized(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	env.cfg.APIToken = "secret-token"
	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	tests := []struct {
		name   string
		token  string
	}{
		{"no auth header", ""},
		{"wrong token", "Bearer wrong-token"},
		{"empty bearer", "Bearer "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/subscriptions", addr), nil)
			require.NoError(t, err)
			if tt.token != "" {
				req.Header.Set("Authorization", tt.token)
			}

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}
}

// TestE2E_APISubscriptions_InvalidToken tests GET /api/v1/subscriptions with wrong token.
func TestE2E_APISubscriptions_InvalidToken(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	env.cfg.APIToken = "correct-token"
	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/subscriptions", addr), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer wrong-token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestE2E_APISubscriptions_MethodNotAllowed tests POST to /api/v1/subscriptions.
func TestE2E_APISubscriptions_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	env.cfg.APIToken = "test-api-token"
	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req, err := http.NewRequest(method, fmt.Sprintf("http://%s/api/v1/subscriptions", addr), nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer test-api-token")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		})
	}
}

// TestE2E_APISubscriptions_FiltersInactive tests that inactive subscriptions are not returned.
func TestE2E_APISubscriptions_FiltersInactive(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	// Create an active subscription
	activeSub := &database.Subscription{
		TelegramID:     111222,
		Username:       "active_user",
		ClientID:       "active-client-uuid",
		SubscriptionID: "active-token",
		InboundID:      1,
		TrafficLimit:   30 * 1024 * 1024 * 1024,
		Status:         "active",
	}
	err := env.db.CreateSubscription(context.Background(), activeSub)
	require.NoError(t, err)

	// Create a revoked subscription
	revokedSub := &database.Subscription{
		TelegramID:     333444,
		Username:       "revoked_user",
		ClientID:       "revoked-client-uuid",
		SubscriptionID: "revoked-token",
		InboundID:      1,
		TrafficLimit:   30 * 1024 * 1024 * 1024,
		Status:         "revoked",
	}
	err = env.db.CreateSubscription(context.Background(), revokedSub)
	require.NoError(t, err)

	env.cfg.APIToken = "test-api-token"
	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/subscriptions", addr), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-api-token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 1, "only active subscription should be returned")

	subMap := subs[0].(map[string]interface{})
	assert.Equal(t, "active-client-uuid", subMap["id"])
	assert.Equal(t, "active_user", subMap["email"])
	assert.Equal(t, true, subMap["enabled"])
}

// TestE2E_WebhookSender_DeliverySuccess tests that webhook is delivered to a real HTTP server.
func TestE2E_WebhookSender_DeliverySuccess(t *testing.T) {
	var received atomic.Value
	var mu sync.Mutex
	receivedEvent := webhook.Event{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer webhook-secret-123", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		mu.Lock()
		defer mu.Unlock()

		if err := json.Unmarshal(body, &receivedEvent); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		received.Store(true)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	sender := webhook.NewSender(server.URL, "webhook-secret-123")
	

	event := webhook.Event{
		EventID:           "evt-e2e-test-001",
		Event:             webhook.EventSubscriptionActivated,
		UserID:            "user-uuid-123",
		Email:             "e2e@example.com",
		SubscriptionToken: "sub-token-xyz",
	}

	sender.SendAsync(event)

	assert.Eventually(t, func() bool {
		return received.Load() != nil && received.Load().(bool)
	}, 3*time.Second, 50*time.Millisecond, "webhook should be delivered")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "evt-e2e-test-001", receivedEvent.EventID)
	assert.Equal(t, webhook.EventSubscriptionActivated, receivedEvent.Event)
	assert.Equal(t, "user-uuid-123", receivedEvent.UserID)
	assert.Equal(t, "e2e@example.com", receivedEvent.Email)
	assert.Equal(t, "sub-token-xyz", receivedEvent.SubscriptionToken)
}

// TestE2E_WebhookSender_RetryOnFailure tests that webhook sender retries on server error.
func TestE2E_WebhookSender_RetryOnFailure(t *testing.T) {
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt64(&requestCount, 1)
		// Fail first attempt, succeed on second
		if count == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	sender := webhook.NewSender(server.URL, "test-secret")
	

	event := webhook.Event{
		EventID: "evt-retry-e2e",
		Event:   webhook.EventSubscriptionExpired,
		UserID:  "user-retry",
		Email:   "retry@example.com",
	}

	sender.SendAsync(event)

	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&requestCount) >= 2
	}, 10*time.Second, 100*time.Millisecond, "webhook should be retried after failure")

	assert.Equal(t, int64(2), atomic.LoadInt64(&requestCount))
}

// TestE2E_WebhookSender_EmptyURL tests that no webhook is sent when URL is empty.
func TestE2E_WebhookSender_EmptyURL(t *testing.T) {
	sender := webhook.NewSender("", "")

	// Should not panic and should return immediately
	sender.SendAsync(webhook.Event{
		EventID: "evt-noop",
		Event:   webhook.EventSubscriptionActivated,
	})

	// Give a small window to ensure no goroutine was started
	time.Sleep(20 * time.Millisecond)
}

// TestE2E_WebhookSender_ConcurrentEvents tests sending multiple webhooks concurrently.
func TestE2E_WebhookSender_ConcurrentEvents(t *testing.T) {
	var mu sync.Mutex
	receivedEventIDs := make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var event webhook.Event
		if err := json.Unmarshal(body, &event); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		mu.Lock()
		receivedEventIDs[event.EventID]++
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	sender := webhook.NewSender(server.URL, "concurrent-secret")
	

	eventCount := 5
	for i := 0; i < eventCount; i++ {
		sender.SendAsync(webhook.Event{
			EventID:           fmt.Sprintf("evt-concurrent-%d", i),
			Event:             webhook.EventSubscriptionActivated,
			UserID:            fmt.Sprintf("user-%d", i),
			Email:             fmt.Sprintf("user%d@example.com", i),
			SubscriptionToken: fmt.Sprintf("token-%d", i),
		})
	}

	assert.Eventually(t, func() bool {
		mu.Lock()
		count := len(receivedEventIDs)
		mu.Unlock()
		return count == eventCount
	}, 5*time.Second, 50*time.Millisecond, "all webhooks should be delivered")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, eventCount, len(receivedEventIDs))
	for eventID, count := range receivedEventIDs {
		assert.Equal(t, 1, count, "event %s should be received exactly once", eventID)
	}
}

// TestE2E_APISubscriptions_MultipleActive tests with multiple active subscriptions.
func TestE2E_APISubscriptions_MultipleActive(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	// Create 3 active subscriptions
	for i := 1; i <= 3; i++ {
		sub := &database.Subscription{
			TelegramID:     int64(100000 + i),
			Username:       fmt.Sprintf("user%d", i),
			ClientID:       fmt.Sprintf("client-uuid-%d", i),
			SubscriptionID: fmt.Sprintf("token-%d", i),
			InboundID:      1,
			TrafficLimit:   30 * 1024 * 1024 * 1024,
			Status:         "active",
		}
		err := env.db.CreateSubscription(context.Background(), sub)
		require.NoError(t, err)
	}

	env.cfg.APIToken = "test-api-token"
	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/subscriptions", addr), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-api-token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	require.NoError(t, err)

	subs, ok := response["subscriptions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, subs, 3)

	// Verify each subscription has correct fields
	for i, sub := range subs {
		subMap := sub.(map[string]interface{})
		assert.Contains(t, subMap, "id")
		assert.Contains(t, subMap, "email")
		assert.Contains(t, subMap, "enabled")
		assert.Contains(t, subMap, "subscription_token")
		assert.Equal(t, true, subMap["enabled"], "subscription %d should have enabled=true", i)
	}
}

// TestE2E_APISubscriptions_ResponseHeaders tests that response headers are set correctly.
func TestE2E_APISubscriptions_ResponseHeaders(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	env.cfg.APIToken = "test-api-token"
	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/subscriptions", addr), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-api-token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-store, private", resp.Header.Get("Cache-Control"))
}
