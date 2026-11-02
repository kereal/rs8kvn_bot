package heartbeat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
}

// newHeartbeatServer запускает сервер, считающий входящие heartbeats.
func newHeartbeatServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var count atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	return server, &count
}

func TestGetHTTPClient_Singleton(t *testing.T) {
	// Reset the singleton for this test
	resetHTTPClient()

	client1 := getHTTPClient()
	client2 := getHTTPClient()

	require.NotNil(t, client1, "getHTTPClient() returned nil")
	require.NotNil(t, client2, "getHTTPClient() returned nil on second call")
	// Use pointer comparison to avoid reflect.DeepEqual on *http.Client
	// which races with Transport's lazy HTTP/2 initialization
	assert.Same(t, client1, client2, "getHTTPClient() should return the same client instance")
}

func TestGetHTTPClient_ConcurrentAccess(t *testing.T) {
	// Reset the singleton for this test
	resetHTTPClient()

	var wg sync.WaitGroup

	clients := make([]*http.Client, 10)

	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			clients[idx] = getHTTPClient()
		}(i)
	}

	wg.Wait()

	// All clients should be the same pointer instance.
	// Use pointer comparison (assert.Same) instead of assert.Equal
	// because reflect.DeepEqual on *http.Client reads Transport fields
	// concurrently with HTTP/2 lazy initialization, causing DATA RACE.
	for i := 1; i < 10; i++ {
		assert.Same(t, clients[0], clients[i], "Client %d should be the same pointer as client 0", i)
	}
}

func TestGetHTTPClient_Timeout(t *testing.T) {
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
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Start() with empty URL should return immediately")
	}
}

func TestStart_ContextCancellation(t *testing.T) {
	t.Parallel()

	server, requestCount := newHeartbeatServer(t)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		Start(ctx, server.URL, 1)
		close(done)
	}()

	// Wait for the initial heartbeat to be sent
	require.Eventually(t, func() bool { return requestCount.Load() >= 1 },
		200*time.Millisecond, 5*time.Millisecond, "initial heartbeat should be sent")

	// Cancel the context
	cancel()

	select {
	case <-done:
		// Good, stopped after context cancellation
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Start() should stop after context cancellation")
	}
}

// TestStart_InvalidIntervalDefault проверяет, что интервал ниже минимума
// (0 и отрицательный) заменяется на минимальный и шедулер работает.
func TestStart_InvalidIntervalDefault(t *testing.T) {
	t.Parallel()

	for _, interval := range []int{0, -1} {
		t.Run(fmt.Sprintf("interval_%d", interval), func(t *testing.T) {
			t.Parallel()

			server, requestCount := newHeartbeatServer(t)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})

			go func() {
				Start(ctx, server.URL, interval)
				close(done)
			}()

			// Wait for the initial heartbeat to be sent
			require.Eventually(t, func() bool { return requestCount.Load() >= 1 },
				200*time.Millisecond, 5*time.Millisecond, "initial heartbeat should be sent")

			// Cancel to stop the scheduler
			cancel()

			select {
			case <-done:
				// Good
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Start() should stop after context cancellation")
			}
		})
	}
}

func TestSendHeartbeat_Success(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		assert.Equal(t, "POST", r.Method, "Expected POST request")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"), "Content-Type header")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Single request
	sendHeartbeat(server.URL)

	assert.Equal(t, int64(1), requestCount.Load(), "sendHeartbeat() did not send request to server")

	// Multiple requests: each call sends exactly one request
	for range 5 {
		sendHeartbeat(server.URL)
	}

	assert.Equal(t, int64(6), requestCount.Load(), "Expected 6 requests in total")
}

func TestSendHeartbeat_InvalidURL(t *testing.T) {
	t.Parallel()

	// Dialing a closed local port is refused immediately (no 10s client timeout),
	// so this stays fast.
	sendHeartbeat("http://127.0.0.1:1/heartbeat")
}
