package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
)

// === Health endpoint tests ===

func TestHandleHealthz(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleHealthz() status")
	assert.Contains(t, rec.Body.String(), "ok", "handleHealthz() body should contain 'ok'")
}

func TestHandleHealthz_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

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

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	req := httptest.NewRequest("HEAD", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleHealthz() should accept HEAD")
}

func TestHandleReadyz_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

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

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)
	srv.SetReady(true)

	req := httptest.NewRequest("HEAD", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleReadyz() should accept HEAD")
}

func TestHandleReadyz_NotReady(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)
	// Register a failing checker to make health status not "ok"
	srv.RegisterChecker("failing", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusDown, Message: "service down"}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "handleReadyz() status when not ready")
	assert.Contains(t, rec.Body.String(), "NOT READY", "handleReadyz() body should contain 'NOT READY'")
}

func TestHandleReadyz_Ready(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)
	// No checkers registered means health status will be "ok"

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handleReadyz() status when ready")
	assert.Equal(t, "OK", rec.Body.String(), "handleReadyz() body should be 'OK'")
}

func TestHandleReadyz_WithChecker(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	// Register a health checker that returns OK
	srv.RegisterChecker("test-component", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK, Message: "all good"}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	// handleReadyz returns simple "OK" or "NOT READY", not JSON
	assert.Equal(t, http.StatusOK, rec.Code, "handleReadyz() status with healthy checker")
	assert.Equal(t, "OK", rec.Body.String(), "handleReadyz() body should be 'OK'")
}

func TestHandleReadyz_WithFailingChecker(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

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

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

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

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	health := srv.checkHealth(context.Background())

	assert.Equal(t, "ok", health.Status, "checkHealth() status with no checkers")
	assert.Empty(t, health.Components, "checkHealth() should have no components")
}

func TestCheckHealth_WithCheckers(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

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

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

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

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

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

func TestSetReady(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	// Initially not ready
	srv.mu.Lock()
	ready := srv.ready
	srv.mu.Unlock()
	assert.False(t, ready, "Server should not be ready initially")

	// Set ready
	srv.SetReady(true)
	srv.mu.Lock()
	ready = srv.ready
	srv.mu.Unlock()
	assert.True(t, ready, "Server should be ready after SetReady(true)")

	// Set not ready
	srv.SetReady(false)
	srv.mu.Lock()
	ready = srv.ready
	srv.mu.Unlock()
	assert.False(t, ready, "Server should not be ready after SetReady(false)")
}

func TestRegisterChecker(t *testing.T) {
	t.Parallel()

	srv := NewServer(":8880", nil, &config.Config{}, bot.NewTestBotConfig(), nil, nil)

	// Register multiple checkers
	srv.RegisterChecker("checker1", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})
	srv.RegisterChecker("checker2", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusOK}
	})

	srv.mu.Lock()
	count := len(srv.checkers)
	srv.mu.Unlock()

	assert.Equal(t, 2, count, "Server should have 2 checkers registered")
}

// === Status constant tests ===

func TestStatusConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, Status("ok"), StatusOK, "StatusOK constant")
	assert.Equal(t, Status("degraded"), StatusDegraded, "StatusDegraded constant")
	assert.Equal(t, Status("down"), StatusDown, "StatusDown constant")
}

// === ComponentHealth tests ===

func TestComponentHealth_Fields(t *testing.T) {
	t.Parallel()

	health := ComponentHealth{
		Status:  StatusOK,
		Message: "test message",
	}

	assert.Equal(t, StatusOK, health.Status, "ComponentHealth.Status")
	assert.Equal(t, "test message", health.Message, "ComponentHealth.Message")
}

// === HealthResponse tests ===

func TestHealthResponse_Fields(t *testing.T) {
	t.Parallel()

	resp := HealthResponse{
		Status:    string(StatusOK),
		Timestamp: time.Now(),
		Components: map[string]ComponentHealth{
			"comp1": {Status: StatusOK, Message: "component1"},
		},
	}

	assert.Equal(t, string(StatusOK), resp.Status, "HealthResponse.Status")
	assert.Len(t, resp.Components, 1, "HealthResponse.Components length")
}
