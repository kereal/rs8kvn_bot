package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/metrics"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/subproxy"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

//go:embed templates/*.html templates/logo.png
var allFiles embed.FS

var staticFiles = allFiles

// TrialCreationResult holds the outcome of a successful trial creation.
type TrialCreationResult struct {
	SubID      string
	ClientID   string
	SubURL     string
	InviteCode string
	ExpiryTime time.Time
}

type Status string

const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

type ComponentHealth struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

type Server struct {
	addr            string
	db              interfaces.DatabaseService
	xuiClient       interfaces.XUIClient
	cfg             *config.Config
	botConfig       *bot.BotConfig
	subService      *service.SubscriptionService
	subProxy        *subproxy.Service
	subFetchGroup   *SingleFlight
	server          *http.Server
	listenerAddr    string
	mu              sync.RWMutex
	ready           bool
	checkers        map[string]func(context.Context) ComponentHealth
	inviteCodeRegex *regexp.Regexp
	startTime       time.Time
	trialTemplate   *template.Template
	errorTemplate   *template.Template
}

func NewServer(addr string, db interfaces.DatabaseService, xuiClient interfaces.XUIClient, cfg *config.Config, botConfig *bot.BotConfig, subService *service.SubscriptionService, subProxy *subproxy.Service) *Server {
	trialTmpl := template.Must(template.New("trial.html").Funcs(template.FuncMap{
		"escape": func(s string) string {
			var buf strings.Builder
			template.HTMLEscape(&buf, []byte(s))
			return buf.String()
		},
	}).ParseFS(allFiles, "templates/trial.html"))

	errorTmpl := template.Must(template.New("error.html").ParseFS(allFiles, "templates/error.html"))

	return &Server{
		addr:            addr,
		db:              db,
		xuiClient:       xuiClient,
		cfg:             cfg,
		botConfig:       botConfig,
		subService:      subService,
		subProxy:        subProxy,
		subFetchGroup:   NewSingleFlight(),
		checkers:        make(map[string]func(context.Context) ComponentHealth),
		inviteCodeRegex: regexp.MustCompile(`^[a-zA-Z0-9_-]+$`),
		startTime:       time.Now(),
		trialTemplate:   trialTmpl,
		errorTemplate:   errorTmpl,
	}
}

func (s *Server) RegisterChecker(name string, checker func(context.Context) ComponentHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkers[name] = checker
}

func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
}

// Addr returns the server's actual listening address. Only valid after Start
// has been called. When the server is configured with port :0, this returns
// the OS-assigned port.
func (s *Server) Addr() string {
	if s.listenerAddr != "" {
		return s.listenerAddr
	}
	return s.addr
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/i/", s.handleInvite)
	mux.HandleFunc("/sub/", s.handleSubscription)
	mux.HandleFunc("/static/logo.png", s.handleLogo)

	// API routes with Bearer token auth
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/v1/subscriptions", s.GetSubscriptions)
	mux.Handle("/api/v1/subscriptions", BearerAuthMiddleware(s.cfg.APIToken)(apiMux))

	// Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Wrap with metrics middleware
	instrumentedHandler := metrics.InstrumentHTTP(mux)

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           instrumentedHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Bind the port before starting the goroutine.
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to bind %s: %w", s.addr, err)
	}
	s.listenerAddr = listener.Addr().String()

	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", zap.Error(err))
		} else if err == http.ErrServerClosed {
			logger.Info("HTTP server stopped gracefully")
		}
	}()

	logger.Info("Web server started", zap.String("addr", s.addr))
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	health := s.checkHealth(ctx)
	s.writeJSON(w, health)
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	health := s.checkHealth(ctx)

	if health.Status == "ok" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("NOT READY"))
	}
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	data, err := staticFiles.ReadFile("templates/logo.png")
	if err != nil {
		http.Error(w, "logo not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if r.Method == http.MethodHead {
		return
	}
	w.Write(data)
}

type HealthResponse struct {
	Status     string                     `json:"status"`
	Components map[string]ComponentHealth `json:"components"`
	Timestamp  time.Time                  `json:"timestamp"`
	Uptime     string                     `json:"uptime"`
}

func (s *Server) checkHealth(ctx context.Context) HealthResponse {
	s.mu.RLock()
	checkers := make(map[string]func(context.Context) ComponentHealth, len(s.checkers))
	for k, v := range s.checkers {
		checkers[k] = v
	}
	s.mu.RUnlock()

	response := HealthResponse{
		Status:     string(StatusOK),
		Components: make(map[string]ComponentHealth),
		Timestamp:  time.Now(),
		Uptime:     time.Since(s.startTime).Round(time.Second).String(),
	}

	for name, checker := range checkers {
		component := checker(ctx)
		response.Components[name] = component

		if component.Status == StatusDown {
			response.Status = string(StatusDown)
		} else if component.Status == StatusDegraded && response.Status == string(StatusOK) {
			response.Status = string(StatusDegraded)
		}
	}

	return response
}

func (s *Server) writeJSON(w http.ResponseWriter, resp HealthResponse) {
	w.Header().Set("Content-Type", "application/json")

	// Map health status to HTTP status code so that Kubernetes liveness
	// probes correctly detect when the service is down.
	switch resp.Status {
	case string(StatusDown):
		w.WriteHeader(http.StatusServiceUnavailable) // 503
	default:
		w.WriteHeader(http.StatusOK) // 200 for OK and Degraded
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("Failed to encode JSON response", zap.Error(err))
	}
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	s.HandleInvite(w, r)
}

// HandleInvite is the exported version of handleInvite for E2E testing.
func (s *Server) HandleInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	path := r.URL.Path
	if !strings.HasPrefix(path, "/i/") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		s.renderErrorPage(w, "Страница не найдена")
		return
	}

	code := path[3:]
	if code == "" || strings.Contains(code, "/") || !s.inviteCodeRegex.MatchString(code) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		s.renderErrorPage(w, "Приглашение не найдено")
		return
	}

	invite, err := s.db.GetInviteByCode(ctx, code)
	if err != nil {
		logger.Warn("Invite not found", zap.String("code", code), zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		s.renderErrorPage(w, "Приглашение не найдено")
		return
	}

	// Проверяем куку на существующий trial
	existingSub, err := s.getExistingTrialFromCookie(r, ctx, code)
	if err == nil && existingSub != nil {
		// Trial уже создан — показываем существующий
		logger.Info("Existing trial found via cookie", zap.String("sub_id", existingSub.SubscriptionID))
		telegramLink := "https://t.me/" + s.botConfig.Username + "?start=trial_" + existingSub.SubscriptionID
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		s.renderTrialPage(w, existingSub.SubscriptionID, existingSub.SubscriptionURL, telegramLink, s.cfg.TrialDurationHours)
		return
	}

	ip := getClientIP(r)

	count, err := s.db.CountTrialRequestsByIPLastHour(ctx, ip)
	if err != nil {
		logger.Error("Failed to check rate limit", zap.Error(err), zap.String("ip", ip))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		s.renderErrorPage(w, "Ошибка сервера. Попробуйте позже.")
		return
	}
	if count >= s.cfg.TrialRateLimit {
		logger.Warn("Rate limit exceeded", zap.String("ip", ip), zap.Int("count", count))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		s.renderErrorPage(w, "Слишком много запросов. Попробуйте позже.")
		return
	}

	if err := s.db.CreateTrialRequest(ctx, ip); err != nil {
		logger.Error("Failed to create trial request", zap.Error(err))
	}

	if s.subService == nil {
		logger.Error("Subscription service not initialized")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		s.renderErrorPage(w, "Ошибка сервера. Попробуйте позже.")
		return
	}

	result, err := s.subService.CreateTrial(ctx, code)
	if err != nil {
		logger.Error("Failed to create trial subscription", zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		s.renderErrorPage(w, "Ошибка сервера. Попробуйте позже.")
		return
	}

	logger.Info("Trial subscription created",
		zap.String("code", code),
		zap.String("subscription_id", result.SubID),
		zap.String("ip", ip),
		zap.Int64("referrer_tg_id", invite.ReferrerTGID))

	http.SetCookie(w, &http.Cookie{
		Name:     "rs8kvn_trial_" + code,
		Value:    result.SubID,
		Path:     "/i/" + code,
		Expires:  time.Now().Add(time.Duration(s.cfg.TrialDurationHours) * time.Hour),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	telegramLink := "https://t.me/" + s.botConfig.Username + "?start=trial_" + result.SubID
	s.renderTrialPage(w, result.SubID, result.SubscriptionURL, telegramLink, s.cfg.TrialDurationHours)
}

// getExistingTrialFromCookie проверяет куку и возвращает существующий trial
func (s *Server) getExistingTrialFromCookie(r *http.Request, ctx context.Context, code string) (*database.Subscription, error) {
	cookie, err := r.Cookie("rs8kvn_trial_" + code)
	if err != nil {
		return nil, err
	}

	subID := cookie.Value
	if subID == "" {
		return nil, fmt.Errorf("empty cookie value")
	}

	sub, err := s.db.GetTrialSubscriptionBySubID(ctx, subID)
	if err != nil {
		return nil, err
	}

	// Проверяем, что это всё ещё trial и не активирован
	if !sub.IsTrial || sub.TelegramID != 0 {
		return nil, fmt.Errorf("not a valid trial")
	}

	// Проверяем, что не истёк
	if time.Now().After(sub.ExpiryTime) {
		return nil, fmt.Errorf("trial expired")
	}

	return sub, nil
}

type trialPageData struct {
	HappLink     template.URL
	SubURL       string
	TelegramLink template.URL
	TrialHours   int
}

func (s *Server) renderTrialPage(w http.ResponseWriter, subID, subURL, telegramLink string, trialHours int) {
	happLink := "happ://add/" + subURL
	data := trialPageData{
		//nolint:gosec // G203: happLink is constructed from internal subscription URL, safe
		HappLink: template.URL(happLink),
		SubURL:   subURL,
		//nolint:gosec // G203: telegramLink is validated invite link from internal system
		TelegramLink: template.URL(telegramLink),
		TrialHours:   trialHours,
	}
	if err := s.trialTemplate.Execute(w, data); err != nil {
		logger.Error("Failed to render trial page", zap.Error(err))
	}
}

type errorPageData struct {
	Message string
}

func (s *Server) renderErrorPage(w http.ResponseWriter, message string) {
	data := errorPageData{Message: message}
	if err := s.errorTemplate.Execute(w, data); err != nil {
		logger.Error("Failed to render error page", zap.Error(err))
	}
}

func getClientIP(r *http.Request) string {
	// Only trust X-Forwarded-For if the connection comes from a local address
	// (i.e., behind a reverse proxy like nginx/caddy). Direct connections cannot
	// be trusted to set this header correctly.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && isLocalAddress(host) {
		forwarded := r.Header.Get("X-Forwarded-For")
		if forwarded != "" {
			ips := strings.Split(forwarded, ",")
			if len(ips) > 0 {
				ip := strings.TrimSpace(ips[0])
				if ip != "" {
					return ip
				}
			}
		}
	}

	// Fall back to the real remote address (host part only).
	// If SplitHostPort failed on r.RemoteAddr, try once more as a fallback
	// to strip the port — otherwise the IP with port (e.g., "1.2.3.4:54321")
	// would bypass rate limiting since it looks unique each time.
	if err != nil {
		fallbackHost, _, splitErr := net.SplitHostPort(r.RemoteAddr)
		if splitErr == nil {
			return fallbackHost
		}
		return r.RemoteAddr // last resort — may include port
	}
	return host
}

func isLocalAddress(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	// Only trust loopback addresses (reverse proxy on the same host).
	// Do NOT trust all private IPs — in cloud environments (AWS, GCP),
	// other VMs on the same VPC could spoof X-Forwarded-For and bypass
	// IP-based rate limiting for trial subscriptions.
	return ip.IsLoopback()
}

var subIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type subFetchResult struct {
	body    []byte
	headers map[string]string
}

type subFetchError struct {
	err      error
	notFound bool
}

func (e *subFetchError) Error() string {
	return e.err.Error()
}

func (e *subFetchError) Unwrap() error {
	return e.err
}

func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.subProxy == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Subscription proxy is not available"))
		return
	}

	path := r.URL.Path
	if !strings.HasPrefix(path, "/sub/") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not found"))
		return
	}

	subID := path[5:]
	if subID == "" || strings.Contains(subID, "/") || !subIDRegex.MatchString(subID) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid subscription code"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if cachedBody, cachedHeaders, ok := s.subProxy.GetCache(subID); ok {
		// Verify subscription is still active even on cache hit
		sub, err := s.db.GetSubscriptionBySubscriptionID(ctx, subID)
		if err != nil || !sub.IsActive() {
			s.subProxy.InvalidateCache(subID)
			if err != nil {
				logger.Warn("Subscription not found in DB on cache hit", zap.String("sub_id", subID), zap.Error(err))
			} else {
				logger.Info("Cached subscription is no longer active", zap.String("sub_id", subID), zap.String("status", sub.Status))
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Subscription not found"))
			return
		}
		logger.Debug("Subscription proxy cache hit", zap.String("sub_id", subID))
		s.writeSubscriptionResponse(w, cachedBody, cachedHeaders)
		return
	}

	result, err := s.subFetchGroup.Do(ctx, subID, func(ctx context.Context) (interface{}, error) {
		if cachedBody, cachedHeaders, ok := s.subProxy.GetCache(subID); ok {
			return &subFetchResult{body: cachedBody, headers: cachedHeaders}, nil
		}

		sub, err := s.db.GetSubscriptionBySubscriptionID(ctx, subID)
		if err != nil {
			logger.Warn("Subscription not found in DB", zap.String("sub_id", subID), zap.Error(err))
			return nil, &subFetchError{err: err, notFound: true}
		}

		if sub.SubscriptionURL == "" {
			logger.Warn("Subscription URL is empty", zap.String("sub_id", subID))
			return nil, nil
		}

		if !sub.IsActive() {
			logger.Info("Subscription is not active", zap.String("sub_id", subID), zap.String("status", sub.Status))
			return nil, &subFetchError{err: nil, notFound: true}
		}

		xuiResp, fetchErr := subproxy.FetchFromXUI(sub.SubscriptionURL)
		if fetchErr != nil {
			logger.Warn("Failed to fetch from 3x-ui", zap.String("sub_id", subID), zap.Error(fetchErr))
			if cachedBody, cachedHeaders, ok := s.subProxy.GetCache(subID); ok {
				return &subFetchResult{body: cachedBody, headers: cachedHeaders}, nil
			}
			return nil, fetchErr
		}

		extraServers := s.subProxy.GetExtraServers()
		extraHeaders := s.subProxy.GetExtraHeaders()
		format := subproxy.DetectFormat(xuiResp.Body)
		mergedBody := subproxy.MergeSubscriptions(xuiResp.Body, extraServers, format)

		mergedHeaders := make(map[string]string, len(xuiResp.Headers)+len(extraHeaders))
		for k, v := range xuiResp.Headers {
			mergedHeaders[k] = v
		}
		for k, v := range extraHeaders {
			mergedHeaders[k] = v
		}

		s.subProxy.SetCache(subID, mergedBody, mergedHeaders)

		return &subFetchResult{body: mergedBody, headers: mergedHeaders}, nil
	})

	if err != nil {
		var subErr *subFetchError
		if errors.As(err, &subErr) && subErr.notFound {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Subscription not found"))
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusGatewayTimeout)
			w.Write([]byte("Request timeout"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("Failed to fetch subscription"))
		return
	}

	if result == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Subscription URL not available"))
		return
	}

	res := result.(*subFetchResult)
	logger.Info("Subscription proxy served",
		zap.String("sub_id", subID),
		zap.Int("extra_servers", len(s.subProxy.GetExtraServers())),
		zap.Int("body_size", len(res.body)))

	s.writeSubscriptionResponse(w, res.body, res.headers)
}

func (s *Server) writeSubscriptionResponse(w http.ResponseWriter, body []byte, headers map[string]string) {
	for key, value := range headers {
		w.Header().Set(key, value)
	}
	// Remove Content-Length since body size changed after merge.
	// Go's http.ResponseWriter will use chunked encoding automatically.
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}
