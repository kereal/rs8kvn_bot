// Package admin serves the local admin panel: a JSON API over the bot's SQLite
// data and the embedded single-page frontend.
//
// The panel is deliberately small and separate from the bot's public web server
// (internal/web). It binds to a loopback address by default, requires a bearer
// token, and exposes only operations that cannot corrupt domain state machines:
// plans are editable, subscriptions/orders/sync statuses are not.
package admin

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// distFiles embeds the Vite build output. Rebuild it with `npm run build` in
// this directory before `go build`; the resulting assets are part of the
// binary, so production needs no Node toolchain.
//
//go:embed all:dist
var distFiles embed.FS

// PlanStore is the narrow persistence contract the plans screen needs. The
// concrete *database.Service satisfies it.
type PlanStore interface {
	ListPlanOverviews(ctx context.Context) ([]database.PlanOverview, error)
	CreatePlan(ctx context.Context, plan *database.Plan) error
	UpdatePlan(ctx context.Context, plan *database.Plan) error
	DeletePlan(ctx context.Context, id uint) error
}

// Server is the admin HTTP server. Create it with NewServer and run Start/Stop.
//
// The server holds the concrete *database.Service and narrows it per screen:
// every screen declares the few methods it needs (PlanStore, and one interface
// per upcoming screen) instead of widening a single god interface.
type Server struct {
	addr  string
	token string
	plans PlanStore
	// screen: orders
	orders OrderStore
	dist   fs.FS

	mu           sync.Mutex
	server       *http.Server
	listenerAddr string
}

// NewServer builds an admin server bound to addr. The token is required: an
// empty token makes Start fail instead of exposing an unauthenticated panel.
func NewServer(addr, token string, db *database.Service) *Server {
	dist, err := fs.Sub(distFiles, "dist")
	if err != nil {
		// The embed directive guarantees this directory exists; a failure here
		// means the build is broken, not that the runtime can recover.
		logger.Error("admin: embedded frontend missing", zap.Error(err))
		dist = nil
	}

	return &Server{
		addr:   addr,
		token:  token,
		plans:  db, // screen: plans (existing)
		orders: db, // screen: orders
		dist:   dist,
	}
}

// Handler returns the fully wired handler. It is exported so tests can drive
// the API without binding a port.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /admin/api/session", s.withAuth(s.handleSession))
	mux.HandleFunc("GET /admin/api/plans", s.withAuth(s.handleListPlans))
	mux.HandleFunc("POST /admin/api/plans", s.withAuth(s.handleCreatePlan))
	mux.HandleFunc("PUT /admin/api/plans/{id}", s.withAuth(s.handleUpdatePlan))
	mux.HandleFunc("DELETE /admin/api/plans/{id}", s.withAuth(s.handleDeletePlan))
	// --- orders ---
	mux.HandleFunc("GET /admin/api/orders", s.withAuth(s.handleListOrders))
	mux.HandleFunc("GET /admin/api/orders/{id}", s.withAuth(s.handleGetOrder))
	mux.HandleFunc("/admin/", s.handleSPA)

	return securityHeaders(mux)
}

// Start binds the listener and serves requests in a background goroutine.
func (s *Server) Start(ctx context.Context) error {
	if strings.TrimSpace(s.token) == "" {
		return fmt.Errorf("admin: ADMIN_TOKEN must be set to start the panel")
	}

	if s.dist == nil {
		return fmt.Errorf("admin: embedded frontend is not available")
	}

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("admin: failed to bind %s: %w", s.addr, err)
	}

	if !isLoopback(listener.Addr()) {
		logger.Warn("Admin panel is bound to a non-loopback address",
			zap.String("addr", listener.Addr().String()))
	}

	s.mu.Lock()
	s.listenerAddr = listener.Addr().String()
	s.server = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	server := s.server
	s.mu.Unlock()

	logger.Info("Admin panel started", zap.String("addr", s.listenerAddr))

	go func() {
		defer logger.Recover("admin HTTP server")

		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Admin panel server error", zap.Error(err))
		}
	}()

	_ = ctx

	return nil
}

// Addr reports the bound address (useful when addr used port 0).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.listenerAddr
}

// Stop shuts the server down, waiting for in-flight requests until ctx expires.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	server := s.server
	s.server = nil
	s.mu.Unlock()

	if server == nil {
		return nil
	}

	err := server.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("admin: shutdown: %w", err)
	}

	return nil
}

// withAuth enforces a bearer token. Comparison is constant-time and an empty
// configured token never authenticates anything.
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected := s.token
		if strings.TrimSpace(expected) == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "admin token is not configured")

			return
		}

		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid admin token")

			return
		}

		next(w, r)
	}
}

// handleSession exists so the SPA can verify a token before showing the panel.
func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSPA serves the built frontend and falls back to index.html so that
// deep links and reloads work (the app routes on the URL hash).
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	if s.dist == nil {
		http.Error(w, "admin frontend is not built", http.StatusServiceUnavailable)

		return
	}

	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/admin"), "/")
	if name == "" {
		name = "index.html"
	}

	// fs.ValidPath rejects "", ".", ".." and any leading slash, so a crafted
	// request cannot address a file outside the embedded dist tree.
	if !fs.ValidPath(name) {
		name = "index.html"
	}

	info, err := fs.Stat(s.dist, name)
	if err != nil || info.IsDir() {
		name = "index.html"
	}

	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}

	// #nosec G703 -- name passed fs.ValidPath and is resolved inside the embedded
	// filesystem, which cannot escape its root.
	http.ServeFileFS(w, r, s.dist, name)
}

// securityHeaders mirrors internal/web: the panel is same-origin and must never
// be framed or MIME-sniffed.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func isLoopback(addr net.Addr) bool {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}

	return tcpAddr.IP.IsLoopback()
}
