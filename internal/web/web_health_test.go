package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kereal/rs8kvn_bot/internal/config"
)

// === Health endpoint tests ===

func TestHandleHealthz(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleHealthz() status")
	assert.Contains(t, rec.Body.String(), "ok", "handleHealthz() body should contain 'ok'")
}

// TestHandleHealthz_DownCheckerReturns503 pins the gap fix: a DOWN dependency
// must be reported as 503 on /healthz too, not only on /readyz — otherwise a
// dead node keeps the load balancer green forever.
func TestHandleHealthz_DownCheckerReturns503(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)
	srv.RegisterChecker("db", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "connection refused"}
	})

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "healthz must report DOWN as 503")
	assert.Contains(t, rec.Body.String(), `"status":"down"`)
}

func TestHandleHealthz_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)

	// Test POST method
	req := httptest.NewRequest("POST", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleHealthz() should reject POST")
	assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"), "handleHealthz() should set Allow header")

	// Test PUT method
	req = httptest.NewRequest("PUT", "/healthz", nil)
	rec = httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleHealthz() should reject PUT")

	// Test DELETE method
	req = httptest.NewRequest("DELETE", "/healthz", nil)
	rec = httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleHealthz() should reject DELETE")
}

func TestHandleHealthz_HeadMethod(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)

	req := httptest.NewRequest("HEAD", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleHealthz() should accept HEAD")
}

func TestHandleReadyz_NotReadyEvenWhenHealthy(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "NOT READY", rec.Body.String())
}

func TestHandleReadyz_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)

	// Test POST method
	req := httptest.NewRequest("POST", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleReadyz() should reject POST")
	assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"), "handleReadyz() should set Allow header")

	// Test PUT method
	req = httptest.NewRequest("PUT", "/readyz", nil)
	rec = httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleReadyz() should reject PUT")

	// Test DELETE method
	req = httptest.NewRequest("DELETE", "/readyz", nil)
	rec = httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "handleReadyz() should reject DELETE")
}

func TestHandleReadyz_HeadMethod(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)
	srv.SetReady(true)

	req := httptest.NewRequest("HEAD", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleReadyz() should accept HEAD")
}

func TestHandleReadyz_Ready(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)
	srv.SetReady(true)
	// No checkers registered means health status will be "ok"

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleReadyz() status when ready")
	assert.Equal(t, "OK", rec.Body.String(), "handleReadyz() body should be 'OK'")
}

func TestHandleReadyz_WithFailingChecker(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)
	srv.SetReady(true)

	// Register a health checker that returns degraded
	srv.RegisterChecker("failing-component", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDegraded, Message: "something is wrong"}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	// Degraded status means NOT READY with 503
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "handleReadyz() status with degraded checker")
	assert.Equal(t, "NOT READY", rec.Body.String(), "handleReadyz() body should be 'NOT READY'")
}

func TestHandleReadyz_WithDownChecker(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)
	srv.SetReady(true)

	// Register a health checker that returns down
	srv.RegisterChecker("down-component", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "component is down"}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "handleReadyz() status with down checker")
	assert.Equal(t, "NOT READY", rec.Body.String(), "handleReadyz() body should be 'NOT READY'")
}

func TestCheckHealth_NoCheckers(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)

	health := srv.checkHealth(context.Background())

	assert.Equal(t, "ok", health.Status, "checkHealth() status with no checkers")
	assert.Empty(t, health.Components, "checkHealth() should have no components")
}

func TestCheckHealth_WithCheckers(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)

	srv.RegisterChecker("comp1", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK, Message: "ok"}
	})
	srv.RegisterChecker("comp2", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDegraded, Message: "degraded"}
	})

	health := srv.checkHealth(context.Background())

	assert.Equal(t, "degraded", health.Status, "checkHealth() should return degraded when one component is degraded")
	assert.Len(t, health.Components, 2, "checkHealth() should have 2 components")
}

func TestCheckHealth_AllDown(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)

	srv.RegisterChecker("comp1", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "down1"}
	})
	srv.RegisterChecker("comp2", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "down2"}
	})

	health := srv.checkHealth(context.Background())

	assert.Equal(t, "down", health.Status, "checkHealth() should return down when all components are down")
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, "testbot", nil, nil)

	rec := httptest.NewRecorder()

	resp := HealthResponse{
		Status:    string(StatusOK),
		Timestamp: time.Now(),
	}
	srv.writeJSON(rec, resp)

	assert.Equal(t, http.StatusOK, rec.Code, "writeJSON() status")
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"), "writeJSON() content-type")
	assert.Contains(t, rec.Body.String(), "status", "writeJSON() body should contain status")
}
