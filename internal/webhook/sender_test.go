package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
}

func TestNewSender_WithURL(t *testing.T) {
	s := NewSender("https://example.com/webhook", "secret123")
	assert.Equal(t, "https://example.com/webhook", s.url)
	assert.Equal(t, "secret123", s.secret)
	assert.NotNil(t, s.client)
}

func TestNewSender_EmptyURL(t *testing.T) {
	s := NewSender("", "")
	assert.Equal(t, "", s.url)
	assert.Equal(t, "", s.secret)
	assert.NotNil(t, s.client)
}

func TestNoopSender(t *testing.T) {
	n := &NoopSender{}
	// Should not panic
	n.SendAsync(Event{
		EventID: "evt-test",
		Event:   EventSubscriptionActivated,
	})
}

func TestSender_SendAsync_Success(t *testing.T) {
	var received atomic.Value
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)

		// Verify headers
		assert.Equal(t, "Bearer secret123", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Read and verify body
		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			return
		}

		var event Event
		if !assert.NoError(t, json.Unmarshal(body, &event)) {
			return
		}

		received.Store(event)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	s := NewSender(server.URL, "secret123")
	// Use short client timeout for tests
	s.client = server.Client()

	event := Event{
		EventID:           "evt-550e8400-e29b-41d4-a716-446655440000",
		Event:             EventSubscriptionActivated,
		UserID:            "user-123",
		Email:             "test@example.com",
		SubscriptionToken: "token-abc",
	}

	s.SendAsync(event)

	// Wait for async delivery
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&requestCount) == 1
	}, 2*time.Second, 50*time.Millisecond, "webhook should be delivered once")

	// Verify received event
	receivedEvent, ok := received.Load().(Event)
	require.True(t, ok)
	assert.Equal(t, event.EventID, receivedEvent.EventID)
	assert.Equal(t, event.Event, receivedEvent.Event)
	assert.Equal(t, event.UserID, receivedEvent.UserID)
	assert.Equal(t, event.Email, receivedEvent.Email)
	assert.Equal(t, event.SubscriptionToken, receivedEvent.SubscriptionToken)

	// Should only be called once (no retries on success)
	assert.Equal(t, int64(1), atomic.LoadInt64(&requestCount))
}

func TestSender_SendAsync_RetryOnFailure(t *testing.T) {
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt64(&requestCount, 1)
		// Fail first 2 attempts, succeed on 3rd
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	s := NewSender(server.URL, "secret")
	s.client = server.Client()

	event := Event{
		EventID: "evt-retry-test",
		Event:   EventSubscriptionExpired,
		UserID:  "user-456",
		Email:   "retry@example.com",
	}

	s.SendAsync(event)

	// Wait for all retries
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&requestCount) >= 3
	}, 15*time.Second, 100*time.Millisecond, "webhook should be retried and succeed on 3rd attempt")

	// Should have been called 3 times (2 failures + 1 success)
	assert.Equal(t, int64(3), atomic.LoadInt64(&requestCount))
}

func TestSender_SendAsync_AllRetriesFail(t *testing.T) {
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	s := NewSender(server.URL, "secret")
	s.client = server.Client()

	event := Event{
		EventID: "evt-fail-test",
		Event:   EventSubscriptionActivated,
		UserID:  "user-789",
		Email:   "fail@example.com",
	}

	s.SendAsync(event)

	// Wait for all retries (4 attempts: 0 + 1s + 5s + 15s)
	// We use a shorter timeout since we just need to verify it tried
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&requestCount) >= 4
	}, 30*time.Second, 200*time.Millisecond, "webhook should be retried 4 times")

	// Should have been called 4 times (all failed)
	assert.Equal(t, int64(4), atomic.LoadInt64(&requestCount))
}

func TestSender_SendAsync_EmptyURL_NoOp(t *testing.T) {
	s := NewSender("", "")

	// Should not panic and should return immediately
	s.SendAsync(Event{
		EventID: "evt-noop",
		Event:   EventSubscriptionActivated,
	})

	// Give a small window to ensure no goroutine was started
	time.Sleep(100 * time.Millisecond)
}

func TestSender_SendAsync_PermanentError_NoRetry(t *testing.T) {
	// Permanent client errors (4xx except 429) should NOT be retried.
	permanentStatuses := []struct {
		name       string
		statusCode int
	}{
		{"400 Bad Request", http.StatusBadRequest},
		{"401 Unauthorized", http.StatusUnauthorized},
		{"403 Forbidden", http.StatusForbidden},
		{"404 Not Found", http.StatusNotFound},
	}

	for _, tt := range permanentStatuses {
		t.Run(tt.name, func(t *testing.T) {
			var requestCount int64

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&requestCount, 1)
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			s := NewSender(server.URL, "secret")
			s.client = server.Client()

			s.SendAsync(Event{
				EventID: "evt-perm-test",
				Event:   EventSubscriptionActivated,
				UserID:  "user-perm",
				Email:   "perm@example.com",
			})

			// Wait for the single request
			assert.Eventually(t, func() bool {
				return atomic.LoadInt64(&requestCount) >= 1
			}, 2*time.Second, 50*time.Millisecond)

			// Give time for any potential retries to occur
			time.Sleep(500 * time.Millisecond)

			// Should only have been called once — no retries for permanent errors
			assert.Equal(t, int64(1), atomic.LoadInt64(&requestCount),
				"permanent error should not trigger retries")
		})
	}
}

func TestSender_SendAsync_TooManyRequests_Retried(t *testing.T) {
	// 429 Too Many Requests is a transient error and should be retried.
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt64(&requestCount, 1)
		if count < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := NewSender(server.URL, "secret")
	s.client = server.Client()

	s.SendAsync(Event{
		EventID: "evt-429-test",
		Event:   EventSubscriptionActivated,
		UserID:  "user-429",
		Email:   "retry429@example.com",
	})

	// Should retry and eventually succeed on 3rd attempt
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&requestCount) >= 3
	}, 5*time.Second, 50*time.Millisecond, "429 should be retried until success")

	assert.Equal(t, int64(3), atomic.LoadInt64(&requestCount))
}

func TestSender_SendAsync_ConnectionError(t *testing.T) {
	// Create a server that immediately closes to simulate connection error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This handler won't be reached because we'll close the server
	}))
	server.Close() // Close immediately

	s := NewSender(server.URL, "secret")

	// Use fast client to speed up test
	s.client = &http.Client{Timeout: 1 * time.Second}

	// After failures, all retries will fail since the server is closed
	s.SendAsync(Event{
		EventID: "evt-conn-error",
		Event:   EventSubscriptionExpired,
	})

	// Just verify it doesn't panic
	time.Sleep(2 * time.Second)
}

func TestSender_SendAsync_Non2xxResponse(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		shouldRetry bool
	}{
		{"200 OK", http.StatusOK, false},
		{"201 Created", http.StatusCreated, false},
		{"204 No Content", http.StatusNoContent, false},
		{"400 Bad Request", http.StatusBadRequest, false},           // permanent, no retry
		{"401 Unauthorized", http.StatusUnauthorized, false},        // permanent, no retry
		{"429 Too Many Requests", http.StatusTooManyRequests, true}, // transient, retry
		{"500 Internal Server Error", http.StatusInternalServerError, true},
		{"502 Bad Gateway", http.StatusBadGateway, true},
		{"503 Service Unavailable", http.StatusServiceUnavailable, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestCount int64

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := atomic.AddInt64(&requestCount, 1)
				// Succeed on second attempt if it should retry
				if tt.shouldRetry && count == 2 {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			s := NewSender(server.URL, "secret")
			s.client = server.Client()

			s.SendAsync(Event{
				EventID: "evt-status-test",
				Event:   EventSubscriptionActivated,
				UserID:  "user-test",
				Email:   "status@example.com",
			})

			if tt.shouldRetry {
				// Transient errors (5xx, 429) should retry and eventually succeed
				assert.Eventually(t, func() bool {
					return atomic.LoadInt64(&requestCount) >= 2
				}, 5*time.Second, 50*time.Millisecond)
			} else {
				// Success (2xx) or permanent errors (4xx except 429) — only one request
				assert.Eventually(t, func() bool {
					return atomic.LoadInt64(&requestCount) >= 1
				}, 2*time.Second, 50*time.Millisecond)
				// Give a small window to ensure no retries happen for permanent errors
				time.Sleep(200 * time.Millisecond)
				assert.Equal(t, int64(1), atomic.LoadInt64(&requestCount), "should not retry")
			}
		})
	}
}

func TestSender_SendAsync_ConcurrentEvents(t *testing.T) {
	var mu sync.Mutex
	receivedEvents := make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var event Event
		if err := json.Unmarshal(body, &event); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		mu.Lock()
		receivedEvents[event.EventID]++
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	s := NewSender(server.URL, "secret")
	s.client = server.Client()

	// Send multiple events concurrently
	eventCount := 5
	for i := 0; i < eventCount; i++ {
		s.SendAsync(Event{
			EventID:           "evt-concurrent-" + string(rune('A'+i)),
			Event:             EventSubscriptionActivated,
			UserID:            "user-concurrent",
			Email:             "concurrent@example.com",
			SubscriptionToken: "token-concurrent",
		})
	}

	// Wait for all events to be delivered
	assert.Eventually(t, func() bool {
		mu.Lock()
		count := len(receivedEvents)
		mu.Unlock()
		return count == eventCount
	}, 5*time.Second, 50*time.Millisecond, "all events should be delivered")

	// Each event should be received exactly once
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, eventCount, len(receivedEvents))
	for eventID, count := range receivedEvents {
		assert.Equal(t, 1, count, "event %s should be received exactly once", eventID)
	}
}

func TestSender_SendAsync_EventDataIntegrity(t *testing.T) {
	var received atomic.Value

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			return
		}

		var event Event
		if !assert.NoError(t, json.Unmarshal(body, &event)) {
			return
		}

		received.Store(event)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := NewSender(server.URL, "my-secret-token")
	s.client = server.Client()

	event := Event{
		EventID:           "evt-550e8400-e29b-41d4-a716-446655440000",
		Event:             EventSubscriptionUpdated,
		UserID:            "550e8400-e29b-41d4-a716-446655440000",
		Email:             "user@example.com",
		SubscriptionToken: "abc123def456",
	}

	s.SendAsync(event)

	// Wait for delivery
	assert.Eventually(t, func() bool {
		return received.Load() != nil
	}, 2*time.Second, 50*time.Millisecond)

	// Verify all fields are preserved
	receivedEvent, ok := received.Load().(Event)
	require.True(t, ok)
	assert.Equal(t, event.EventID, receivedEvent.EventID)
	assert.Equal(t, event.Event, receivedEvent.Event)
	assert.Equal(t, event.UserID, receivedEvent.UserID)
	assert.Equal(t, event.Email, receivedEvent.Email)
	assert.Equal(t, event.SubscriptionToken, receivedEvent.SubscriptionToken)
}

func TestSender_SendAsync_AuthHeader(t *testing.T) {
	var authHeader atomic.Value

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tests := []struct {
		name   string
		secret string
		want   string
	}{
		{"simple token", "mytoken", "Bearer mytoken"},
		{"uuid token", "550e8400-e29b-41d4", "Bearer 550e8400-e29b-41d4"},
		{"empty token", "", "Bearer "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authHeader.Store("")

			s := NewSender(server.URL, tt.secret)
			s.client = server.Client()

			s.SendAsync(Event{
				EventID: "evt-auth-test",
				Event:   EventSubscriptionActivated,
			})

			assert.Eventually(t, func() bool {
				return authHeader.Load() != nil && authHeader.Load() != ""
			}, 2*time.Second, 50*time.Millisecond)

			assert.Equal(t, tt.want, authHeader.Load())
		})
	}
}

func TestSender_SendAsync_ContentTypeHeader(t *testing.T) {
	var contentType atomic.Value

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType.Store(r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := NewSender(server.URL, "secret")
	s.client = server.Client()

	s.SendAsync(Event{
		EventID: "evt-content-type",
		Event:   EventSubscriptionActivated,
	})

	assert.Eventually(t, func() bool {
		return contentType.Load() != nil && contentType.Load() != ""
	}, 2*time.Second, 50*time.Millisecond)

	assert.Equal(t, "application/json", contentType.Load())
}

func TestSender_SendAsync_DuplicateEvent(t *testing.T) {
	// Test that the same event can be sent twice (server handles dedup)
	var requestCount int64
	var mu sync.Mutex
	receivedEventIDs := []string{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)

		body, _ := io.ReadAll(r.Body)
		var event Event
		json.Unmarshal(body, &event)

		mu.Lock()
		receivedEventIDs = append(receivedEventIDs, event.EventID)
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	s := NewSender(server.URL, "secret")
	s.client = server.Client()

	event := Event{
		EventID: "evt-duplicate-test",
		Event:   EventSubscriptionActivated,
		UserID:  "user-dup",
	}

	// Send the same event twice
	s.SendAsync(event)
	s.SendAsync(event)

	// Both should be delivered (server is responsible for dedup)
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&requestCount) >= 2
	}, 2*time.Second, 50*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, len(receivedEventIDs))
	assert.Equal(t, "evt-duplicate-test", receivedEventIDs[0])
	assert.Equal(t, "evt-duplicate-test", receivedEventIDs[1])
}

func TestEventTypes(t *testing.T) {
	assert.Equal(t, "subscription.activated", EventSubscriptionActivated)
	assert.Equal(t, "subscription.expired", EventSubscriptionExpired)
	assert.Equal(t, "subscription.updated", EventSubscriptionUpdated)
}

func TestEvent_JSONMarshal(t *testing.T) {
	event := Event{
		EventID:           "evt-123",
		Event:             EventSubscriptionActivated,
		UserID:            "user-456",
		Email:             "test@example.com",
		SubscriptionToken: "token-789",
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	var unmarshaled Event
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, event.EventID, unmarshaled.EventID)
	assert.Equal(t, event.Event, unmarshaled.Event)
	assert.Equal(t, event.UserID, unmarshaled.UserID)
	assert.Equal(t, event.Email, unmarshaled.Email)
	assert.Equal(t, event.SubscriptionToken, unmarshaled.SubscriptionToken)
}

func TestEvent_JSONKeys(t *testing.T) {
	event := Event{
		EventID:           "evt-123",
		Event:             EventSubscriptionActivated,
		UserID:            "user-456",
		Email:             "test@example.com",
		SubscriptionToken: "token-789",
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	// Verify JSON keys match the spec
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	assert.Contains(t, raw, "event_id")
	assert.Contains(t, raw, "event")
	assert.Contains(t, raw, "user_id")
	assert.Contains(t, raw, "email")
	assert.Contains(t, raw, "subscription_token")
}
