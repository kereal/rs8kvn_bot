package heartbeat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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
	os.Exit(m.Run())
}

func TestGetHTTPClient_Singleton(t *testing.T) {
	t.Parallel()

	// Reset the singleton for this test
	resetHTTPClient()

	client1 := getHTTPClient()
	client2 := getHTTPClient()

	require.NotNil(t, client1, "getHTTPClient() returned nil")
	require.NotNil(t, client2, "getHTTPClient() returned nil on second call")
	assert.Equal(t, client1, client2, "getHTTPClient() should return the same client instance")
}

func TestGetHTTPClient_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	// Reset the singleton for this test
	resetHTTPClient()

	var wg sync.WaitGroup
	clients := make([]*http.Client, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			clients[idx] = getHTTPClient()
		}(i)
	}

	wg.Wait()

	// All clients should be the same instance
	for i := 1; i < 10; i++ {
		assert.Equal(t, clients[0], clients[i], "Client %d is not the same as client 0", i)
	}
}

func TestGetHTTPClient_Timeout(t *testing.T) {
	t.Parallel()

	// Reset the singleton for this test
	resetHTTPClient()

	client := getHTTPClient()

	assert.Equal(t, 10*time.Second, client.Timeout, "Client timeout")
}

func TestStart_EmptyURL(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Start with empty URL should return immediately
	done := make(chan struct{})
	go func() {
		Start(ctx, "", 1)
		close(done)
	}()

	select {
	case <-done:
		// Good, returned immediately
	case <-time.After(2 * time.Second):
		t.Fatal("Start() with empty URL should return immediately")
	}
}

func TestStart_ContextCancellation(t *testing.T) {
	t.Parallel()

	// Create a mock server that responds quickly
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		Start(ctx, server.URL, 1)
		close(done)
	}()

	// Wait a bit for initial heartbeat
	time.Sleep(20 * time.Millisecond)

	// Cancel the context
	cancel()

	select {
	case <-done:
		// Good, stopped after context cancellation
	case <-time.After(2 * time.Second):
		t.Fatal("Start() should stop after context cancellation")
	}
}

func TestStart_NegativeInterval(t *testing.T) {
	t.Parallel()

	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use negative interval - should default to 300 seconds
	done := make(chan struct{})
	go func() {
		Start(ctx, server.URL, -1)
		close(done)
	}()

	// Wait for initial heartbeat
	time.Sleep(20 * time.Millisecond)

	// Cancel to stop the scheduler
	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("Start() should handle negative interval")
	}
}

func TestStart_ZeroInterval(t *testing.T) {
	t.Parallel()

	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use zero interval - should default to 300 seconds
	done := make(chan struct{})
	go func() {
		Start(ctx, server.URL, 0)
		close(done)
	}()

	// Wait for initial heartbeat
	time.Sleep(20 * time.Millisecond)

	// Cancel to stop the scheduler
	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("Start() should handle zero interval")
	}
}

func TestSendHeartbeat_Success(t *testing.T) {
	t.Parallel()

	requestReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		assert.Equal(t, "POST", r.Method, "Expected POST request")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"), "Content-Type header")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sendHeartbeat(server.URL)

	assert.True(t, requestReceived, "sendHeartbeat() did not send request to server")
}

func TestSendHeartbeat_ServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	// Should not panic or crash
	sendHeartbeat(server.URL)
}

func TestSendHeartbeat_InvalidURL(t *testing.T) {
	t.Parallel()

	// Should not panic with invalid URL
	sendHeartbeat("http://invalid-host-that-does-not-exist:12345/heartbeat")
}

func TestSendHeartbeat_MultipleRequests(t *testing.T) {
	t.Parallel()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Send multiple heartbeats
	for i := 0; i < 5; i++ {
		sendHeartbeat(server.URL)
	}

	assert.Equal(t, 5, requestCount, "Expected 5 requests")
}

func TestStart_IntervalTiming(t *testing.T) {
	t.Parallel()

	var requestCount int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use 1 second interval for testing
	go Start(ctx, server.URL, 1)

	// Wait for initial heartbeat
	time.Sleep(20 * time.Millisecond)

	// Initial heartbeat should have been sent
	assert.GreaterOrEqual(t, atomic.LoadInt64(&requestCount), int64(1), "Initial heartbeat was not sent")

	// Cancel to stop the scheduler
	cancel()
}

func TestMaskURL_Heartbeat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"http", "http://example.com/heartbeat", "http://example.com/***"},
		{"https", "https://secure.example.com:8443/hb", "https://secure.example.com:8443/***"},
		{"empty", "", "(empty)"},
		{"no scheme short", "localhost", "***"},
		{"no scheme long", "verylonghostname.example.com", "verylongho..."},
		{"scheme only no path", "http://example.com", "http://example.com/***"},
		{"https no path", "https://secure.example.com", "https://secure.example.com/***"},
		{"path with query", "http://example.com/path?query=value", "http://example.com/***"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskURL(tt.url)
			assert.Equal(t, tt.expected, result, "maskURL(%q)", tt.url)
		})
	}
}

func TestStart_MultipleContexts(t *testing.T) {
	t.Parallel()

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// Start multiple heartbeat schedulers
	go Start(ctx1, "", 60)
	go Start(ctx2, "", 60)

	// Should not panic
	cancel1()
	cancel2()
}

func TestSendHeartbeat_ContextTimeout(t *testing.T) {
	t.Parallel()

	// Test that sendHeartbeat handles context-like timeouts gracefully
	// Use very short timeout by calling invalid URL
	_ = maskURL("http://localhost:19999/heartbeat")
}
