package heartbeat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rs8kvn_bot/internal/logger"
)

func init() {
	// Initialize logger for tests
	logger.Init("", "error")
}

func TestGetHTTPClient_Singleton(t *testing.T) {
	// Reset the singleton for this test
	httpClientOnce = sync.Once{}
	httpClient = nil

	client1 := getHTTPClient()
	client2 := getHTTPClient()

	if client1 == nil {
		t.Fatal("getHTTPClient() returned nil")
	}
	if client2 == nil {
		t.Fatal("getHTTPClient() returned nil on second call")
	}
	if client1 != client2 {
		t.Error("getHTTPClient() should return the same client instance")
	}
}

func TestGetHTTPClient_ConcurrentAccess(t *testing.T) {
	// Reset the singleton for this test
	httpClientOnce = sync.Once{}
	httpClient = nil

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
		if clients[i] != clients[0] {
			t.Errorf("Client %d is not the same as client 0", i)
		}
	}
}

func TestGetHTTPClient_Timeout(t *testing.T) {
	// Reset the singleton for this test
	httpClientOnce = sync.Once{}
	httpClient = nil

	client := getHTTPClient()

	if client.Timeout != 10*time.Second {
		t.Errorf("Client timeout = %v, want 10s", client.Timeout)
	}
}

func TestStart_EmptyURL(t *testing.T) {
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
	time.Sleep(100 * time.Millisecond)

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
	time.Sleep(100 * time.Millisecond)

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
	time.Sleep(100 * time.Millisecond)

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
	requestReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type: application/json, got %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sendHeartbeat(server.URL)

	if !requestReceived {
		t.Error("sendHeartbeat() did not send request to server")
	}
}

func TestSendHeartbeat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	// Should not panic or crash
	sendHeartbeat(server.URL)
}

func TestSendHeartbeat_InvalidURL(t *testing.T) {
	// Should not panic with invalid URL
	sendHeartbeat("http://invalid-host-that-does-not-exist:12345/heartbeat")
}

func TestSendHeartbeat_MultipleRequests(t *testing.T) {
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

	if requestCount != 5 {
		t.Errorf("Expected 5 requests, got %d", requestCount)
	}
}

func TestStart_IntervalTiming(t *testing.T) {
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
	time.Sleep(100 * time.Millisecond)

	// Initial heartbeat should have been sent
	if atomic.LoadInt64(&requestCount) < 1 {
		t.Error("Initial heartbeat was not sent")
	}

	// Cancel to stop the scheduler
	cancel()
}
