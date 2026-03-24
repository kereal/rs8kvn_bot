package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// Status represents the health status of a component.
type Status string

const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

// ComponentHealth represents the health of a single component.
type ComponentHealth struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
	Latency string `json:"latency,omitempty"`
}

// HealthResponse represents the health check response.
type HealthResponse struct {
	Status     Status                     `json:"status"`
	Timestamp  string                     `json:"timestamp"`
	Components map[string]ComponentHealth `json:"components"`
	Uptime     string                     `json:"uptime"`
}

// Checker is a function that checks the health of a component.
type Checker func(ctx context.Context) ComponentHealth

// Server is the health check HTTP server.
type Server struct {
	mu        sync.RWMutex
	port      int
	checkers  map[string]Checker
	server    *http.Server
	startTime time.Time
	ready     bool
}

// NewServer creates a new health check server.
func NewServer(port int) *Server {
	return &Server{
		port:      port,
		checkers:  make(map[string]Checker),
		startTime: time.Now(),
		ready:     false,
	}
}

// RegisterChecker registers a health check function for a component.
func (s *Server) RegisterChecker(name string, checker Checker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkers[name] = checker
}

// SetReady sets the ready state of the server.
func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
}

// Start starts the health check server.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/", s.handleIndex)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		logger.Info("Health check server starting", zap.Int("port", s.port))
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Health check server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop gracefully stops the health check server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	logger.Info("Stopping health check server")
	return s.server.Shutdown(ctx)
}

// handleHealthz handles the /healthz endpoint.
// Returns 200 if the process is alive, regardless of component health.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	checkers := make(map[string]Checker)
	for k, v := range s.checkers {
		checkers[k] = v
	}
	s.mu.RUnlock()

	components := s.runChecks(r.Context(), checkers)
	status := s.aggregateStatus(components)

	response := HealthResponse{
		Status:     status,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Components: components,
		Uptime:     time.Since(s.startTime).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	if status == StatusDown {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode healthz response", zap.Error(err))
	}
}

// handleReadyz handles the /readyz endpoint.
// Returns 200 only if the server is ready to accept requests.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ready := s.ready
	checkers := make(map[string]Checker)
	for k, v := range s.checkers {
		checkers[k] = v
	}
	s.mu.RUnlock()

	if !ready {
		response := HealthResponse{
			Status:    StatusDegraded,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Components: map[string]ComponentHealth{
				"ready": {Status: StatusDown, Message: "Server not ready"},
			},
			Uptime: time.Since(s.startTime).String(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode readyz response", zap.Error(err))
		}
		return
	}

	components := s.runChecks(r.Context(), checkers)
	status := s.aggregateStatus(components)

	response := HealthResponse{
		Status:     status,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Components: components,
		Uptime:     time.Since(s.startTime).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	if status == StatusDown {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode readyz response", zap.Error(err))
	}
}

// handleIndex handles the root endpoint.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"service":   "rs8kvn_bot",
		"endpoints": "/healthz, /readyz",
	}); err != nil {
		logger.Error("Failed to encode index response", zap.Error(err))
	}
}

// runChecks runs all registered health checkers.
func (s *Server) runChecks(ctx context.Context, checkers map[string]Checker) map[string]ComponentHealth {
	components := make(map[string]ComponentHealth)

	// Create a context with timeout for all checks
	checkTimeout := 5 * time.Second
	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	for name, checker := range checkers {
		start := time.Now()
		health := checker(checkCtx)
		health.Latency = time.Since(start).String()
		components[name] = health
	}

	return components
}

// aggregateStatus determines the overall status from component statuses.
func (s *Server) aggregateStatus(components map[string]ComponentHealth) Status {
	hasDown := false
	hasDegraded := false

	for _, health := range components {
		switch health.Status {
		case StatusDown:
			hasDown = true
		case StatusDegraded:
			hasDegraded = true
		}
	}

	if hasDown {
		return StatusDown
	}
	if hasDegraded {
		return StatusDegraded
	}
	return StatusOK
}

// DatabaseChecker creates a health checker for the database.
func DatabaseChecker(ping func(ctx context.Context) error) Checker {
	return func(ctx context.Context) ComponentHealth {
		if err := ping(ctx); err != nil {
			return ComponentHealth{
				Status:  StatusDown,
				Message: fmt.Sprintf("Database error: %v", err),
			}
		}
		return ComponentHealth{
			Status: StatusOK,
		}
	}
}

// XUIChecker creates a health checker for the 3x-ui panel.
func XUIChecker(check func(ctx context.Context) error) Checker {
	return func(ctx context.Context) ComponentHealth {
		if err := check(ctx); err != nil {
			return ComponentHealth{
				Status:  StatusDegraded,
				Message: fmt.Sprintf("3x-ui unavailable: %v", err),
			}
		}
		return ComponentHealth{
			Status: StatusOK,
		}
	}
}
