package web

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
	"rs8kvn_bot/internal/subserver"

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
	cfg             *config.Config
	botConfig       *bot.BotConfig
	subService      *service.SubscriptionService
	subServer       *subserver.Service
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

	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", zap.Error(err))
		} else if err == http.ErrServerClosed {
			logger.Info("HTTP server stopped gracefully")
		}
	}()

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
	if sub.TelegramID != 0 {
		return nil, fmt.Errorf("not a valid trial")
	}

	plan, planErr := s.db.GetPlanByID(ctx, sub.PlanID)
	if planErr != nil || plan.Name != database.TrialPlanName {
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

// sourceHost returns the host part of a URL for logging (no path / subID).
func sourceHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Host
}

// subIDRegex validates subscription IDs: alphanumeric, underscore, hyphen.
var subIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// maxIPEntries limits the number of tracked IP addresses per subscription.
const maxIPEntries = 100

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
	// Only GET is allowed.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Subscription server must be initialized.
	if s.subServer == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Subscription server is not available"))
		return
	}

	// Extract and validate the subscription ID from the path.
	path := r.URL.Path
	if !strings.HasPrefix(path, "/sub/") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Subscription not found"))
		return
	}

	subID := path[5:]
	if subID == "" || strings.Contains(subID, "/") || !subIDRegex.MatchString(subID) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Subscription not found"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// From this point on, log a debug trace of the request lifecycle.
	start := time.Now()
	clientIP := getClientIP(r)
	logDebug := func(msg string, fields ...zap.Field) {
		logger.Debug(msg, append([]zap.Field{zap.String("sub_id", subID), zap.String("client_ip", clientIP)}, fields...)...)
	}
	logger.Info("subscription request received",
		zap.String("sub_id", subID),
		zap.String("client_ip", clientIP),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)

	// Cache check: if we have a fresh cached response for this subID,
	// verify the subscription is still active in the database and
	// serve the cached body with cached headers directly. Since v2.3.0
	// we use a cheap status+expiry lookup instead of the full JOIN.
	if cachedBody, cachedHeaders, ok := s.subServer.GetCache(subID); ok {
		status, expiryTime, err := s.db.GetSubscriptionStatus(ctx, subID)
		if err != nil || status != "active" || (expiryTime.IsZero() == false && time.Now().After(expiryTime)) {
			// Subscription no longer valid — purge cache and return 404.
			s.subServer.InvalidateCache(subID)
			logDebug("cache invalidated: subscription no longer active",
				zap.String("status", status),
				zap.Time("expiry_time", expiryTime),
				zap.Error(err),
			)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Subscription not found"))
			return
		}
		logDebug("cache hit, serving cached response",
			zap.Int("body_size", len(cachedBody)),
			zap.Int("cached_headers", len(cachedHeaders)),
		)
		for k, v := range cachedHeaders {
			w.Header().Set(k, v)
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(http.StatusOK)
		w.Write(cachedBody)
		logDebug("response served from cache",
			zap.Int("status", http.StatusOK),
			zap.Int("body_size", len(cachedBody)),
			zap.Duration("elapsed", time.Since(start)),
		)
		return
	}

	logDebug("cache miss, fetching subscription")

	// Fetch the full subscription record (plan + active sources).
	subFull, err := s.db.GetSubscriptionWithPlanAndSources(ctx, subID)
	if err != nil {
		logDebug("subscription lookup failed", zap.Error(err))
		logger.Warn("Failed to get subscription", zap.String("sub_id", subID), zap.Error(err))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if errors.Is(err, gorm.ErrRecordNotFound) {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		w.Write([]byte("Subscription not found"))
		return
	}

	// Track device info and IP address for this request.
	logDebug("subscription loaded from database",
		zap.Uint("sub_pk", subFull.ID),
		zap.String("status", subFull.Subscription.Status),
		zap.Time("expiry_time", subFull.ExpiryTime),
		zap.Int64("plan_traffic_limit", subFull.Plan.TrafficLimit),
		zap.Int("sources_count", len(subFull.Sources)),
	)

	requestHeaders := filterHeaders(r.Header)
	s.updateDevices(ctx, subFull, requestHeaders)
	s.updateIPs(ctx, subFull, clientIP)

	// Aggregate results across all active sources.
	var allItems []string
	var allJSONConfigs []json.RawMessage
	var firstExpire string
	var totalUpload, totalDownload int64
	allJSON := true
	var firstSourceHeaders map[string]string

	for _, src := range subFull.Sources {
		// Skip sources without a subscription URL.
		if src.SubURL == "" {
			logDebug("skipping source without sub_url", zap.String("source", src.Name))
			continue
		}

		sourceURL := src.SubURL + subID
		logDebug("fetching from source",
			zap.String("source", src.Name),
			zap.String("url_host", sourceHost(sourceURL)),
		)

		body, xuiHeaders, err := s.fetchSource(sourceURL)
		if err != nil {
			logger.Warn("Failed to fetch from source", zap.String("source", src.Name), zap.Error(err))
			logDebug("source fetch failed",
				zap.String("source", src.Name),
				zap.Error(err),
			)
			continue
		}

		format := subserver.DetectFormat(body)
		logDebug("source response received",
			zap.String("source", src.Name),
			zap.String("format", format.String()),
			zap.Int("body_size", len(body)),
			zap.Int("headers_count", len(xuiHeaders)),
			zap.Int64("upload", parseUserInfoValue(xuiHeaders, "upload")),
			zap.Int64("download", parseUserInfoValue(xuiHeaders, "download")),
		)

		// Aggregate subscription-userinfo: pick the earliest expire.
		if xuiHeaders != nil {
			if firstSourceHeaders == nil {
				firstSourceHeaders = xuiHeaders
			}
			if expireVal, ok := xuiHeaders["subscription-userinfo"]; ok {
				expire := parseExpireFromUserInfo(expireVal)
				if expire != "" && (firstExpire == "" || expire < firstExpire) {
					firstExpire = expire
				}
			}
		}

		// Sum upload/download across all sources.
		totalUpload += parseUserInfoValue(xuiHeaders, "upload")
		totalDownload += parseUserInfoValue(xuiHeaders, "download")

		switch format {
		case subserver.FormatJSON:
			// JSON configs are kept as raw messages for pure-JSON output
			// or converted to share links in mixed mode.
			configs, parseErr := subserver.ExtractJSONConfigs(body)
			if parseErr != nil {
				logger.Warn("Failed to parse JSON configs",
					zap.String("source", src.Name),
					zap.Error(parseErr))
				allJSON = false
				continue
			}
			allJSONConfigs = append(allJSONConfigs, configs...)
		case subserver.FormatBase64:
			allJSON = false
			decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
			if decErr != nil {
				allItems = append(allItems, strings.TrimSpace(string(body)))
			} else {
				allItems = append(allItems, strings.TrimSpace(string(decoded)))
			}
		case subserver.FormatPlain:
			allJSON = false
			lines := strings.Split(string(body), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					allItems = append(allItems, line)
				}
			}
		}
	}

	// Build the aggregated Subscription-UserInfo header.
	userInfo := buildUserInfoHeader(totalUpload, totalDownload, subFull.Plan.TrafficLimit, firstExpire)

	logDebug("sources aggregated",
		zap.Int("sources_with_suburl", len(subFull.Sources)),
		zap.Int("json_configs", len(allJSONConfigs)),
		zap.Int("plain_items", len(allItems)),
		zap.Bool("pure_json", allJSON),
		zap.Int64("total_upload", totalUpload),
		zap.Int64("total_download", totalDownload),
		zap.String("first_expire", firstExpire),
	)

	// If we are in mixed mode (some sources returned non-JSON),
	// convert any collected JSON configs to share links and merge into allItems.
	if !allJSON && len(allJSONConfigs) > 0 {
		for _, rawConfig := range allJSONConfigs {
			link, convErr := subserver.ConvertSingleJSONToLink(rawConfig)
			if convErr != nil {
				logger.Debug("Failed to convert JSON config to share link", zap.Error(convErr))
				continue
			}
			allItems = append(allItems, link)
		}
	}

	// Pure-JSON output: marshal all raw configs into a JSON array response.
	if allJSON && len(allJSONConfigs) > 0 {
		responseBody, _ := json.Marshal(allJSONConfigs)
		cacheHeaders := responseHeaders(firstSourceHeaders, "application/json; charset=utf-8", userInfo)
		s.subServer.SetCache(subID, responseBody, cacheHeaders)
		applySourceHeaders(w.Header(), firstSourceHeaders)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Subscription-UserInfo", userInfo)
		w.WriteHeader(http.StatusOK)
		w.Write(responseBody)
		logDebug("response served (pure JSON)",
			zap.String("mode", "json"),
			zap.Int("status", http.StatusOK),
			zap.Int("body_size", len(responseBody)),
			zap.Int("cached_headers", len(cacheHeaders)),
			zap.Duration("elapsed", time.Since(start)),
		)
		return
	}

	// No servers collected from any source.
	if len(allItems) == 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Subscription not found"))
		logDebug("response served (no items)",
			zap.String("mode", "empty"),
			zap.Int("status", http.StatusNotFound),
			zap.Duration("elapsed", time.Since(start)),
		)
		return
	}

	// Mixed or plain-text output: join all share links and encode to base64.
	var responseBody []byte

	combined := strings.Join(allItems, "\n")
	responseBody = []byte(base64.StdEncoding.EncodeToString([]byte(combined)))

	ct := "text/plain; charset=utf-8; profile=base64"
	cacheHeaders := responseHeaders(firstSourceHeaders, ct, userInfo)
	s.subServer.SetCache(subID, responseBody, cacheHeaders)

	s.writeSubscriptionResponse(w, responseBody, userInfo, firstSourceHeaders)
	logDebug("response served (base64)",
		zap.String("mode", "base64"),
		zap.Int("status", http.StatusOK),
		zap.Int("body_size", len(responseBody)),
		zap.Int("raw_items", len(allItems)),
		zap.Int("cached_headers", len(cacheHeaders)),
		zap.Duration("elapsed", time.Since(start)),
	)
}

// fetchSource retrieves a subscription response from a source URL via HTTP GET.
func (s *Server) fetchSource(sourceURL string) ([]byte, map[string]string, error) {
	xuiResp, err := subserver.FetchFromXUI(sourceURL)
	if err != nil {
		return nil, nil, err
	}

	body := xuiResp.Body
	headers := xuiResp.Headers

	if headers == nil {
		headers = make(map[string]string)
	}

	return body, headers, nil
}

// filterHeaders extracts request headers into a lowercased map, excluding
// X-Forwarded-Proto, X-Forwarded-For, and X-Real-Ip. Values are also lowercased.
func filterHeaders(h http.Header) map[string]string {
	result := make(map[string]string)
	excluded := map[string]bool{
		"x-forwarded-proto": true,
		"x-forwarded-for":   true,
		"x-real-ip":         true,
	}

	for key, values := range h {
		lowerKey := strings.ToLower(key)
		if excluded[lowerKey] {
			continue
		}
		if len(values) > 0 {
			result[lowerKey] = strings.ToLower(values[0])
		}
	}
	return result
}

// updateDevices records the current request headers as a device entry in the
// subscription's Devices JSON field. Each entry includes a "timestamp" key
// (UTC RFC3339) marking when the device was last seen. If an existing entry
// has the same x-hwid value it is replaced (rotated to the end). The updated
// list is persisted to DB.
func (s *Server) updateDevices(ctx context.Context, subFull *database.SubscriptionFull, headers map[string]string) {
	devices, err := subFull.GetDevices()
	if err != nil {
		logger.Warn("Failed to parse devices JSON", zap.Error(err))
		devices = []map[string]string{}
	}

	currentHWID := headers["x-hwid"]
	nowStr := time.Now().UTC().Format(time.RFC3339)

	for i, dev := range devices {
		if dev["x-hwid"] == currentHWID {
			devices = append(devices[:i], devices[i+1:]...)
			break
		}
	}

	entry := make(map[string]string, len(headers)+1)
	for k, v := range headers {
		entry[k] = v
	}
	entry["timestamp"] = nowStr
	devices = append(devices, entry)

	if err := subFull.SetDevices(devices); err != nil {
		logger.Warn("Failed to set devices", zap.Error(err))
		return
	}

	if err := s.db.UpdateSubscriptionDevices(ctx, subFull.ID, subFull.Devices); err != nil {
		logger.Warn("Failed to save devices", zap.Error(err))
	}
}

// updateIPs records the current client IP with a UTC timestamp in the
// subscription's Ips JSON field. Duplicate IPs are rotated to the end.
// The list is capped at maxIPEntries (oldest entries are dropped).
func (s *Server) updateIPs(ctx context.Context, subFull *database.SubscriptionFull, ip string) {
	ips, err := subFull.GetIPs()
	if err != nil {
		logger.Warn("Failed to parse ips JSON", zap.Error(err))
		ips = []map[string]string{}
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)

	for i, entry := range ips {
		if _, exists := entry[ip]; exists {
			ips = append(ips[:i], ips[i+1:]...)
			break
		}
	}

	newEntry := map[string]string{ip: nowStr}
	ips = append(ips, newEntry)

	if len(ips) > maxIPEntries {
		ips = ips[len(ips)-maxIPEntries:]
	}

	if err := subFull.SetIPs(ips); err != nil {
		logger.Warn("Failed to set IPs", zap.Error(err))
		return
	}

	if err := s.db.UpdateSubscriptionIPs(ctx, subFull.ID, subFull.Ips); err != nil {
		logger.Warn("Failed to save IPs", zap.Error(err))
	}
}

// parseUserInfoValue extracts a numeric value (upload/download/total) from a
// subscription-userinfo header string (format: "key=N; key2=N2").
func parseUserInfoValue(headers map[string]string, key string) int64 {
	if headers == nil {
		return 0
	}
	userInfo, ok := headers["subscription-userinfo"]
	if !ok {
		return 0
	}
	prefix := key + "="
	parts := strings.Split(userInfo, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, prefix) {
			val := strings.TrimPrefix(part, prefix)
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// parseExpireFromUserInfo extracts the "expire=" value from a subscription-userinfo header string.
func parseExpireFromUserInfo(userInfo string) string {
	parts := strings.Split(userInfo, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "expire=") {
			return strings.TrimPrefix(part, "expire=")
		}
	}
	return ""
}

// buildUserInfoHeader constructs a subscription-userinfo header value from
// aggregated upload/download/total bytes and an optional expire timestamp.
func buildUserInfoHeader(upload, download, total int64, expire string) string {
	parts := []string{
		"upload=" + strconv.FormatInt(upload, 10),
		"download=" + strconv.FormatInt(download, 10),
		"total=" + strconv.FormatInt(total, 10),
	}
	if expire != "" {
		parts = append(parts, "expire="+expire)
	}
	return strings.Join(parts, "; ")
}

// skipTransportHeader returns true for headers that should NOT be forwarded
// from the upstream (3x-ui) response to the subscription client.
func skipTransportHeader(key string) bool {
	switch strings.ToLower(key) {
	case "content-length", "content-type", "content-encoding",
		"transfer-encoding", "connection", "date", "server",
		"alt-svc", "trailer", "subscription-userinfo":
		return true
	default:
		return false
	}
}

// applySourceHeaders copies non-transport headers from the first source's
// response into the target http.Header. Our Content-Type and Subscription-UserInfo
// are set separately afterwards to overwrite any upstream values.
func applySourceHeaders(target http.Header, source map[string]string) {
	if source == nil {
		return
	}
	for k, v := range source {
		if !skipTransportHeader(k) {
			target.Set(k, v)
		}
	}
}

// responseHeaders builds the full set of response headers to cache alongside the body.
// It collects forwarded source headers (profile-title, routing-*, etc.) via
// applySourceHeaders and adds the Content-Type and Subscription-UserInfo headers
// that must be present on every cached response.
func responseHeaders(sourceHeaders map[string]string, contentType, userInfo string) map[string]string {
	h := http.Header{}
	applySourceHeaders(h, sourceHeaders)
	out := make(map[string]string, len(h)+2)
	for k, v := range h {
		out[k] = v[0]
	}
	out["content-type"] = contentType
	out["subscription-userinfo"] = userInfo
	return out
}

// writeSubscriptionResponse writes the final subscription response.
// It sets Content-Type to text/plain with base64 profile, Subscription-UserInfo
// header, and removes Content-Length since body size may vary after aggregation.
// Go's http.ResponseWriter will use chunked encoding automatically.
// Source headers from the first source (profile-title, profile-update-interval, etc.)
// are copied over, while our Content-Type and Subscription-UserInfo overwrite them.
func (s *Server) writeSubscriptionResponse(w http.ResponseWriter, body []byte, userInfo string, sourceHeaders map[string]string) {
	applySourceHeaders(w.Header(), sourceHeaders)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8; profile=base64")
	w.Header().Set("Subscription-UserInfo", userInfo)
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}
