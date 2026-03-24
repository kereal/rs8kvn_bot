package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"rs8kvn_bot/internal/logger"
)

func init() {
	// Initialize logger for tests
	_, _ = logger.Init("", "error")
}

func TestNewServer(t *testing.T) {
	server := NewServer(9999)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	if server.port != 9999 {
		t.Errorf("port = %d, want 9999", server.port)
	}
}

func TestHealthEndpoint(t *testing.T) {
	server := NewServer(19090)

	// Register a healthy checker
	server.RegisterChecker("test", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19090/healthz")
	if err != nil {
		t.Fatalf("Failed to get healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if health.Status != StatusOK {
		t.Errorf("status = %s, want ok", health.Status)
	}

	if health.Components["test"].Status != StatusOK {
		t.Errorf("test component status = %s, want ok", health.Components["test"].Status)
	}
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

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19091/healthz")
	if err != nil {
		t.Fatalf("Failed to get healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if health.Status != StatusDown {
		t.Errorf("status = %s, want down", health.Status)
	}
}

func TestReadyzNotReady(t *testing.T) {
	server := NewServer(19092)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19092/readyz")
	if err != nil {
		t.Fatalf("Failed to get readyz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestReadyzReady(t *testing.T) {
	server := NewServer(19093)
	server.SetReady(true)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19093/readyz")
	if err != nil {
		t.Fatalf("Failed to get readyz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestIndexEndpoint(t *testing.T) {
	server := NewServer(19094)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:19094/")
	if err != nil {
		t.Fatalf("Failed to get root: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDatabaseChecker(t *testing.T) {
	// Test healthy database
	checker := DatabaseChecker(func(ctx context.Context) error {
		return nil
	})
	health := checker(context.Background())
	if health.Status != StatusOK {
		t.Errorf("status = %s, want ok", health.Status)
	}

	// Test unhealthy database
	checker = DatabaseChecker(func(ctx context.Context) error {
		return fmt.Errorf("connection refused")
	})
	health = checker(context.Background())
	if health.Status != StatusDown {
		t.Errorf("status = %s, want down", health.Status)
	}
}

func TestXUIChecker(t *testing.T) {
	// Test healthy x-ui
	checker := XUIChecker(func(ctx context.Context) error {
		return nil
	})
	health := checker(context.Background())
	if health.Status != StatusOK {
		t.Errorf("status = %s, want ok", health.Status)
	}

	// Test unhealthy x-ui
	checker = XUIChecker(func(ctx context.Context) error {
		return fmt.Errorf("timeout")
	})
	health = checker(context.Background())
	if health.Status != StatusDegraded {
		t.Errorf("status = %s, want degraded", health.Status)
	}
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
			if status != tt.expected {
				t.Errorf("status = %s, want %s", status, tt.expected)
			}
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

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// The endpoint should still return a valid response even if encoding fails internally
	resp, err := http.Get("http://localhost:19191/healthz")
	if err != nil {
		t.Fatalf("Failed to get healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestHandleReadyz_JSONEncodingError(t *testing.T) {
	server := NewServer(19292)
	server.SetReady(true)

	// Register a checker
	server.RegisterChecker("test", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// The endpoint should still return a valid response
	resp, err := http.Get("http://localhost:19292/readyz")
	if err != nil {
		t.Fatalf("Failed to get readyz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestHandleIndex_JSONEncodingError(t *testing.T) {
	server := NewServer(19393)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// The index endpoint should return valid JSON
	resp, err := http.Get("http://localhost:19393/")
	if err != nil {
		t.Fatalf("Failed to get index: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Errorf("Failed to decode JSON response: %v", err)
	}

	if result["service"] != "rs8kvn_bot" {
		t.Errorf("service = %s, want rs8kvn_bot", result["service"])
	}
}
