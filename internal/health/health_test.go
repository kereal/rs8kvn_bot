package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/logger"
)

func init() {
	// Initialize logger for tests
	_, _ = logger.Init("", "error")
}

func TestNewServer(t *testing.T) {
	server := NewServer(9999)
	require.NotNil(t, server, "NewServer returned nil")
	assert.Equal(t, 9999, server.port, "port")
}

func TestHealthEndpoint(t *testing.T) {
	server := NewServer(19090)

	// Register a healthy checker
	server.RegisterChecker("test", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19090/healthz")
	require.NoError(t, err, "Failed to get healthz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "status code")

	var health HealthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health), "Failed to decode response")

	assert.Equal(t, StatusOK, health.Status, "status")
	assert.Equal(t, StatusOK, health.Components["test"].Status, "test component status")
}

func TestHealthEndpointWithFailure(t *testing.T) {
	server := NewServer(19091)

	// Register a failing checker
	server.RegisterChecker("failing", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{
			Status:  StatusDown,
			Message: "connection refused",
		}
	})

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19091/healthz")
	require.NoError(t, err, "Failed to get healthz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "status code")

	var health HealthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health), "Failed to decode response")

	assert.Equal(t, StatusDown, health.Status, "status")
}

func TestReadyzNotReady(t *testing.T) {
	server := NewServer(19092)

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19092/readyz")
	require.NoError(t, err, "Failed to get readyz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "status code")
}

func TestReadyzReady(t *testing.T) {
	server := NewServer(19093)
	server.SetReady(true)

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19093/readyz")
	require.NoError(t, err, "Failed to get readyz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "status code")
}

func TestIndexEndpoint(t *testing.T) {
	server := NewServer(19094)

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19094/")
	require.NoError(t, err, "Failed to get root")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "status code")
}

func TestDatabaseChecker(t *testing.T) {
	// Test healthy database
	checker := DatabaseChecker(func(ctx context.Context) error {
		return nil
	})
	health := checker(context.Background())
	assert.Equal(t, StatusOK, health.Status, "status for healthy database")

	// Test unhealthy database
	checker = DatabaseChecker(func(ctx context.Context) error {
		return fmt.Errorf("connection refused")
	})
	health = checker(context.Background())
	assert.Equal(t, StatusDown, health.Status, "status for unhealthy database")
}

func TestXUIChecker(t *testing.T) {
	// Test healthy x-ui
	checker := XUIChecker(func(ctx context.Context) error {
		return nil
	})
	health := checker(context.Background())
	assert.Equal(t, StatusOK, health.Status, "status for healthy x-ui")

	// Test unhealthy x-ui
	checker = XUIChecker(func(ctx context.Context) error {
		return fmt.Errorf("timeout")
	})
	health = checker(context.Background())
	assert.Equal(t, StatusDegraded, health.Status, "status for unhealthy x-ui")
}

func TestAggregateStatus(t *testing.T) {
	server := NewServer(0)

	tests := []struct {
		name       string
		components map[string]ComponentHealth
		expected   Status
	}{
		{
			name: "all ok",
			components: map[string]ComponentHealth{
				"a": {Status: StatusOK},
				"b": {Status: StatusOK},
			},
			expected: StatusOK,
		},
		{
			name: "one degraded",
			components: map[string]ComponentHealth{
				"a": {Status: StatusOK},
				"b": {Status: StatusDegraded},
			},
			expected: StatusDegraded,
		},
		{
			name: "one down",
			components: map[string]ComponentHealth{
				"a": {Status: StatusOK},
				"b": {Status: StatusDown},
			},
			expected: StatusDown,
		},
		{
			name: "degraded and down",
			components: map[string]ComponentHealth{
				"a": {Status: StatusDegraded},
				"b": {Status: StatusDown},
			},
			expected: StatusDown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := server.aggregateStatus(tt.components)
			assert.Equal(t, tt.expected, status, "status")
		})
	}
}

// ==================== JSON Encoding Error Tests ====================

func TestHandleHealthz_JSONEncodingError(t *testing.T) {
	server := NewServer(19191)

	// Register a checker that returns a very large response to potentially cause encoding issues
	server.RegisterChecker("test", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// The endpoint should still return a valid response even if encoding fails internally
	resp, err := http.Get("http://localhost:19191/healthz")
	require.NoError(t, err, "Failed to get healthz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "StatusCode")
}

func TestHandleReadyz_JSONEncodingError(t *testing.T) {
	server := NewServer(19292)
	server.SetReady(true)

	// Register a checker
	server.RegisterChecker("test", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// The endpoint should still return a valid response
	resp, err := http.Get("http://localhost:19292/readyz")
	require.NoError(t, err, "Failed to get readyz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "StatusCode")
}

func TestHandleIndex_JSONEncodingError(t *testing.T) {
	server := NewServer(19393)

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19393/")
	require.NoError(t, err, "Failed to get index")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "StatusCode")

	var result map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result), "Failed to decode JSON response")

	assert.Equal(t, "rs8kvn_bot", result["service"], "service")
}

func TestHandleIndex_NotFound(t *testing.T) {
	server := NewServer(19394)

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19394/nonexistent")
	require.NoError(t, err, "Failed to get nonexistent path")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "StatusCode")
}

func TestStop_NilServer(t *testing.T) {
	server := NewServer(19395)

	err := server.Stop(context.Background())
	assert.NoError(t, err, "Stop() with nil server should not error")
}

func TestHandleHealthz_AllComponentsDown(t *testing.T) {
	server := NewServer(19396)

	server.RegisterChecker("db", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "db down"}
	})
	server.RegisterChecker("xui", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "xui down"}
	})

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19396/healthz")
	require.NoError(t, err, "Failed to get healthz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "StatusCode")

	var health HealthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health), "Failed to decode response")

	assert.Equal(t, StatusDown, health.Status, "status")
	assert.Equal(t, StatusDown, health.Components["db"].Status, "db component")
	assert.Equal(t, StatusDown, health.Components["xui"].Status, "xui component")
}

func TestHandleReadyz_NotReadyWithComponents(t *testing.T) {
	server := NewServer(19397)

	server.RegisterChecker("db", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19397/readyz")
	require.NoError(t, err, "Failed to get readyz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "StatusCode")

	var health HealthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health), "Failed to decode response")

	assert.Equal(t, StatusDegraded, health.Status, "status")
	assert.Equal(t, StatusDown, health.Components["ready"].Status, "ready component")
}

func TestRegisterChecker(t *testing.T) {
	server := NewServer(0)

	checker := func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	}

	server.RegisterChecker("test", checker)

	assert.Contains(t, server.checkers, "test", "Checker should be registered")
}

func TestHandleHealthz_DegradedComponents(t *testing.T) {
	server := NewServer(19398)

	server.RegisterChecker("degraded", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDegraded, Message: "slow"}
	})

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19398/healthz")
	require.NoError(t, err, "Failed to get healthz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "StatusCode - degraded should still be 200")

	var health HealthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health), "Failed to decode response")

	assert.Equal(t, StatusDegraded, health.Status, "status")
}

// ==================== Additional Health Tests ====================

func TestHandleIndex_Success(t *testing.T) {
	server := NewServer(19400)

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19400/")
	require.NoError(t, err, "Failed to get index")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Index should return 200")
}

func TestHandleReadyz_AllComponentsUp(t *testing.T) {
	server := NewServer(19401)
	server.SetReady(true)

	server.RegisterChecker("up", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19401/readyz")
	require.NoError(t, err, "Failed to get readyz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "readyz should return 200 when all components up")
}

func TestHandleReadyz_NoCheckers(t *testing.T) {
	server := NewServer(19402)
	server.SetReady(true)

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19402/readyz")
	require.NoError(t, err, "Failed to get readyz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "readyz should return 200 with no checkers")
}

func TestHandleHealthz_NoCheckers(t *testing.T) {
	server := NewServer(19403)

	require.NoError(t, server.Start(), "Failed to start server")
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19403/healthz")
	require.NoError(t, err, "Failed to get healthz")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "healthz should return 200 with no checkers")
}
