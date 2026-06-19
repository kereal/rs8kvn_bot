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

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"gorm.io/gorm"
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
	ExpiresAt  time.Time
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

const subserverAccessLogCloseTimeout = 5 * time.Second

type Server struct {
	addr            string
	db              interfaces.DatabaseService
	cfg             *config.Config
	botConfig       *bot.BotConfig
	subService      *service.SubscriptionService
	subServer       *subserver.Service
	subserverLogger *subserver.AccessLogger
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

func NewServer(addr string, db interfaces.DatabaseService, cfg *config.Config, botConfig *bot.BotConfig, subService *service.SubscriptionService, subServer *subserver.Service) *Server {
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
		cfg:             cfg,
		botConfig:       botConfig,
		subService:      subService,
		subServer:       subServer,
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

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/v1/subscriptions", s.GetSubscriptions)
	mux.Handle("/api/v1/subscriptions", BearerAuthMiddleware(s.cfg.APIToken)(apiMux))

	mux.Handle("/metrics", promhttp.Handler())

	instrumentedHandler := metrics.InstrumentHTTP(mux)

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           instrumentedHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to bind %s: %w", s.addr, err)
	}
	s.listenerAddr = listener.Addr().String()
	s.initSubserverAccessLogger()

	go func() {
		defer logger.Recover("HTTP server")
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", zap.Error(err))
		} else if err == http.ErrServerClosed {
			logger.Info("HTTP server stopped gracefully")
		}
	}()

	return nil
}

func (s *Server) initSubserverAccessLogger() {
	if s.cfg == nil || s.cfg.SubServerAccessLogPath == "" {
		return
	}

	accessLogger, err := subserver.NewAccessLogger(s.cfg.SubServerAccessLogPath)
	if err != nil {
		logger.Error("Subserver access logging disabled",
			zap.String("path", s.cfg.SubServerAccessLogPath),
			zap.Error(err))
		return
	}

	s.subserverLogger = accessLogger
	if accessLogger.Enabled() {
		logger.Info("Subserver access logging is enabled and working", zap.String("path", s.cfg.SubServerAccessLogPath))
	}
}

func (s *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var err error
	if s.server != nil {
		err = s.server.Shutdown(ctx)
	}
	if s.subserverLogger != nil {
		closeCtx, cancel := context.WithTimeout(ctx, subserverAccessLogCloseTimeout)
		defer cancel()

		if closeErr := s.subserverLogger.CloseWithContext(closeCtx); err == nil {
			err = closeErr
		}
	}
	return err
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

	switch resp.Status {
	case string(StatusDown):
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		w.WriteHeader(http.StatusOK)
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("Failed to encode JSON response", zap.Error(err))
	}
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	s.HandleInvite(w, r)
}

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
		logger.Error("Invite not found",
			zap.String("code", code),
			zap.String("client_ip", getClientIP(r)),
			zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		s.renderErrorPage(w, "Приглашение не найдено")
		return
	}

	// Проверяем куку на существующий trial
	existingSub, err := s.getExistingTrialFromCookie(r, ctx, code)
	if err != nil {
		logger.Error("Failed to check existing trial from cookie",
			zap.String("code", code),
			zap.String("client_ip", getClientIP(r)),
			zap.Error(err))
	} else if existingSub != nil {
		// Trial уже создан — показываем существующий
		logger.Info("Existing trial found via cookie", zap.String("sub_id", existingSub.SubscriptionID))
		telegramLink := "https://t.me/" + s.botConfig.Username + "?start=trial_" + existingSub.SubscriptionID
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		subURL := s.cfg.GlobalSubURL + existingSub.SubscriptionID
		s.renderTrialPage(w, existingSub.SubscriptionID, subURL, telegramLink, s.cfg.TrialDurationHours)
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

	// Проверяем, что это trial и не активирован
	if sub.TelegramID > 0 {
		return nil, fmt.Errorf("not a valid trial")
	}

	plan, planErr := s.db.GetPlanByID(ctx, sub.PlanID)
	if planErr != nil || plan.Name != database.TrialPlanName {
		return nil, fmt.Errorf("not a valid trial")
	}

	// Проверяем, что не истёк
	if time.Now().After(sub.ExpiresAt) {
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
		HappLink:     template.URL(happLink),
		SubURL:       subURL,
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

var (
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrSubscriptionNoItems  = errors.New("no subscription items found")
)

func getClientIP(r *http.Request) string {
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
		return r.RemoteAddr
	}
	return host
}

func isLocalAddress(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// handleSubscription is the HTTP handler for GET /sub/{subID}.
// It first checks the per-subID response cache (added in v2.3.0) and, on
// hit, verifies the subscription is still active via a cheap status lookup
// before replaying the cached body and headers. On miss it fetches the
// subscription together with its plan and active sources from the database,
// tracks the request device and IP, fetches each source URL, detects the
// response format (JSON / Base64 / plain), converts JSON server configs to
// share links, aggregates subscription-userinfo headers across sources,
// caches the result, and writes the final response.
func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	clientIP := getClientIP(r)

	var rec *statusRecorder
	var response http.ResponseWriter = w
	if s.subserverLogger != nil && s.subserverLogger.Enabled() {
		rec = &statusRecorder{ResponseWriter: w}
		response = rec
		defer s.logSubscriptionAccess(rec, r, clientIP)
	}

	if r.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeSubscriptionText(response, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}

	if s.subServer == nil {
		writeSubscriptionText(response, http.StatusServiceUnavailable, "Subscription server is not available")
		return
	}

	path := r.URL.Path
	if !strings.HasPrefix(path, "/sub/") {
		writeSubscriptionText(response, http.StatusNotFound, "Subscription not found")
		return
	}

	subID := path[5:]
	if subID == "" || strings.Contains(subID, "/") || !subserver.SubIDRegex().MatchString(subID) {
		writeSubscriptionText(response, http.StatusNotFound, "Subscription not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	logger.Info("subscription request received",
		zap.String("sub_id", subID),
		zap.String("client_ip", clientIP),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)

	requestHeaders := subserver.FilterHeaders(r.Header)
	result, err := subserver.HandleSubscription(ctx, s.db, s.subServer, subID, clientIP, requestHeaders)
	if err != nil {
		logger.Error("Failed to process subscription",
			zap.String("sub_id", subID),
			zap.String("client_ip", clientIP),
			zap.Error(err))
		if errors.Is(err, gorm.ErrRecordNotFound) ||
			errors.Is(err, subserver.ErrSubscriptionNotFound) ||
			errors.Is(err, subserver.ErrNoSubscriptionItems) {
			writeSubscriptionText(response, http.StatusNotFound, "Subscription not found")
			return
		}
		writeSubscriptionText(response, http.StatusInternalServerError, "Subscription not found")
		return
	}

	if result == nil {
		logger.Error("Empty subscription result",
			zap.String("sub_id", subID),
			zap.String("client_ip", clientIP))
		writeSubscriptionText(response, http.StatusInternalServerError, "Subscription not found")
		return
	}

	for k, v := range result.Headers {
		response.Header().Set(k, v)
	}

	if result.StatusCode != 0 {
		response.WriteHeader(result.StatusCode)
	} else {
		response.WriteHeader(http.StatusOK)
	}
	response.Write(result.Body)
}

func (s *Server) logSubscriptionAccess(rec *statusRecorder, r *http.Request, clientIP string) {
	if s == nil || s.subserverLogger == nil || rec == nil {
		return
	}
	s.subserverLogger.Log(r, rec.StatusCode(), clientIP)
}

func writeSubscriptionText(w http.ResponseWriter, statusCode int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)
	w.Write([]byte(body))
}

type statusRecorder struct {
	http.ResponseWriter

	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	if r.statusCode == 0 {
		r.statusCode = statusCode
	}
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) StatusCode() int {
	if r.statusCode == 0 {
		return http.StatusOK
	}
	return r.statusCode
}
