package web

import (
	"context"
	"embed"
	"encoding/json"
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
	"rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

//go:embed templates/*.html
var templateFS embed.FS

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
	server          *http.Server
	mu              sync.RWMutex
	ready           bool
	checkers        map[string]func(context.Context) ComponentHealth
	inviteCodeRegex *regexp.Regexp
}

func NewServer(addr string, db interfaces.DatabaseService, xuiClient interfaces.XUIClient, cfg *config.Config, botConfig *bot.BotConfig) *Server {
	return &Server{
		addr:            addr,
		db:              db,
		xuiClient:       xuiClient,
		cfg:             cfg,
		botConfig:       botConfig,
		checkers:        make(map[string]func(context.Context) ComponentHealth),
		inviteCodeRegex: regexp.MustCompile(`^[a-zA-Z0-9_-]+$`),
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

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/i/", s.handleInvite)

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

type HealthResponse struct {
	Status     string                     `json:"status"`
	Components map[string]ComponentHealth `json:"components"`
	Timestamp  time.Time                  `json:"timestamp"`
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
	w.WriteHeader(http.StatusOK)

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
		w.Write([]byte(s.renderErrorPage("Страница не найдена")))
		return
	}

	code := path[3:]
	if code == "" || strings.Contains(code, "/") || !s.inviteCodeRegex.MatchString(code) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(s.renderErrorPage("Приглашение не найдено")))
		return
	}

	invite, err := s.db.GetInviteByCode(ctx, code)
	if err != nil {
		logger.Warn("Invite not found", zap.String("code", code), zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(s.renderErrorPage("Приглашение не найдено")))
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
		w.Write([]byte(s.renderTrialPage(existingSub.SubscriptionID, existingSub.SubscriptionURL, telegramLink, s.cfg.TrialDurationHours)))
		return
	}

	ip := getClientIP(r)

	count, err := s.db.CountTrialRequestsByIPLastHour(ctx, ip)
	if err != nil {
		logger.Error("Failed to check rate limit", zap.Error(err), zap.String("ip", ip))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(s.renderErrorPage("Ошибка сервера. Попробуйте позже.")))
		return
	}
	if count >= s.cfg.TrialRateLimit {
		logger.Warn("Rate limit exceeded", zap.String("ip", ip), zap.Int("count", count))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(s.renderErrorPage("Слишком много запросов. Попробуйте позже.")))
		return
	}

	if err := s.db.CreateTrialRequest(ctx, ip); err != nil {
		logger.Error("Failed to create trial request", zap.Error(err))
	}

	result, err := s.createTrialSubscription(ctx, code)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(s.renderErrorPage("Ошибка сервера. Попробуйте позже.")))
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
	w.Write([]byte(s.renderTrialPage(result.SubID, result.SubURL, telegramLink, s.cfg.TrialDurationHours)))
}

// createTrialSubscription creates a new trial subscription with full rollback on failure.
// It handles XUI login, client creation, subscription URL generation, and DB persistence.
func (s *Server) createTrialSubscription(ctx context.Context, inviteCode string) (*TrialCreationResult, error) {
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации идентификатора подписки")
	}
	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации идентификатора клиента")
	}
	trafficBytes := int64(s.cfg.TrafficLimitGB) * 1024 * 1024 * 1024
	expiryTime := time.Now().Add(time.Duration(s.cfg.TrialDurationHours) * time.Hour)

	if err := s.xuiClient.Login(ctx); err != nil {
		return nil, fmt.Errorf("ошибка авторизации на сервере")
	}

	_, err = s.xuiClient.AddClientWithID(
		ctx,
		s.cfg.XUIInboundID,
		subID,
		clientID,
		subID,
		trafficBytes,
		expiryTime,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания клиента на сервере")
	}

	subURL := s.xuiClient.GetSubscriptionLink(s.cfg.XUIHost, subID, s.cfg.XUISubPath)

	sub, err := s.db.CreateTrialSubscription(
		ctx,
		inviteCode,
		subID,
		clientID,
		s.cfg.XUIInboundID,
		trafficBytes,
		expiryTime,
		subURL,
	)
	if err != nil {
		logger.Error("Failed to create trial subscription in DB, rolling back",
			zap.Error(err),
			zap.String("sub_id", subID),
			zap.String("client_id", clientID))

		if delErr := s.xuiClient.DeleteClient(ctx, s.cfg.XUIInboundID, clientID); delErr != nil {
			logger.Error("Failed to rollback XUI client",
				zap.Error(delErr),
				zap.String("client_id", clientID))
		}

		return nil, fmt.Errorf("ошибка создания подписки")
	}

	return &TrialCreationResult{
		SubID:      sub.SubscriptionID,
		ClientID:   sub.ClientID,
		SubURL:     sub.SubscriptionURL,
		InviteCode: inviteCode,
		ExpiryTime: sub.ExpiryTime,
	}, nil
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
	SubURL       template.URL
	HappLink     template.URL
	TelegramLink template.URL
	TrialHours   int
}

func (s *Server) renderTrialPage(subID, subURL, telegramLink string, trialHours int) string {
	tmpl, err := template.New("trial.html").ParseFS(templateFS, "templates/trial.html")
	if err != nil {
		logger.Error("Failed to parse trial template", zap.Error(err))
		return "<h1>Error</h1>"
	}

	data := trialPageData{
		SubURL:       template.URL(subURL),
		HappLink:     template.URL("happ://add/" + subURL),
		TelegramLink: template.URL(telegramLink),
		TrialHours:   trialHours,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		logger.Error("Failed to execute trial template", zap.Error(err))
		return "<h1>Error</h1>"
	}
	return buf.String()
}

type errorPageData struct {
	Message string
}

func (s *Server) renderErrorPage(message string) string {
	tmpl, err := template.New("error.html").ParseFS(templateFS, "templates/error.html")
	if err != nil {
		logger.Error("Failed to parse error template", zap.Error(err))
		return "<h1>Error</h1>"
	}

	data := errorPageData{
		Message: message,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		logger.Error("Failed to execute error template", zap.Error(err))
		return "<h1>Error</h1>"
	}
	return buf.String()
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

	// Fall back to the real remote address
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLocalAddress(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}
