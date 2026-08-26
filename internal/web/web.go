// Package web exposes the HTTP endpoints used by the bot, subscription server,
// health checks, invite landing pages, and payment callbacks.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"maps"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// allFiles contains the HTML templates and static assets bundled into the binary.
// Keeping these files embedded makes the web server self-contained in production.
//
//go:embed templates/*.html templates/logo.png
var allFiles embed.FS

// staticFiles is the filesystem used by template parsing and the logo handler.
var staticFiles = allFiles

// TrialCreationResult holds the outcome of a successful trial creation.
type TrialCreationResult struct {
	SubID      string
	ClientID   string
	SubURL     string
	InviteCode string
	ExpiresAt  time.Time
}

// Status is the aggregate health state returned by the health endpoints.
type Status string

const (
	// StatusOK means all registered health checks are healthy.
	StatusOK Status = "ok"
	// StatusDegraded means at least one dependency is degraded, but none is down.
	StatusDegraded Status = "degraded"
	// StatusDown means at least one dependency is unavailable.
	StatusDown Status = "down"
)

// ComponentHealth describes the health state of one registered dependency.
type ComponentHealth struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// subserverAccessLogCloseTimeout bounds shutdown time spent flushing access logs.
const subserverAccessLogCloseTimeout = 5 * time.Second

// PaymentConfig holds the resolved, ready-to-use payment settings for the
// callback endpoint. It is set via SetPaymentConfig; when nil, the endpoint
// returns 503 to signal that payments are not available.
type PaymentConfig struct {
	Enabled    bool
	MerchantID string
	Secret     string
}

// Server owns the HTTP listener, endpoint dependencies, health checkers, and
// lifecycle state for the public web service.
type Server struct {
	addr            string
	db              interfaces.WebRepository
	cfg             *config.Config
	botUsername     string
	bot             interfaces.BotAPI
	subService      *service.SubscriptionService
	orderService    *service.OrderService
	paymentConfig   *PaymentConfig
	subServer       *subserver.Service
	subserverLogger *subserver.AccessLogger
	server          *http.Server
	listenerAddr    string
	mu              sync.RWMutex
	trialRateMu     sync.Mutex
	ready           bool
	paymentReady    bool
	checkers        map[string]func(context.Context) ComponentHealth
	inviteCodeRegex *regexp.Regexp
	startTime       time.Time
	trialTemplate   *template.Template
	errorTemplate   *template.Template
}

// NewServer constructs a web server and parses the embedded templates. It does
// not open a listener; call Start to begin serving requests.
func NewServer(addr string, db interfaces.WebRepository, cfg *config.Config, botUsername string, subService *service.SubscriptionService, subServer *subserver.Service) *Server {
	trialTmpl := template.Must(template.New("trial.html").Funcs(template.FuncMap{"formatTime": func(t time.Time) string { return t.Format("02.01.2006 15:04") }}).ParseFS(staticFiles, "templates/trial.html"))
	errorTmpl := template.Must(template.New("error.html").ParseFS(staticFiles, "templates/error.html"))

	return &Server{addr: addr, db: db, cfg: cfg, botUsername: botUsername, subService: subService, subServer: subServer, checkers: make(map[string]func(context.Context) ComponentHealth), inviteCodeRegex: regexp.MustCompile(`^[a-zA-Z0-9_-]+$`), startTime: time.Now(), trialTemplate: trialTmpl, errorTemplate: errorTmpl}
}

// SetBot wires Telegram delivery for payment notifications and administrator alerts.
// The server may already be serving when this is called (payment callbacks run
// concurrently), so the field is written under the lock.
func (s *Server) SetBot(bot interfaces.BotAPI) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.bot = bot
}

// SetOrderService wires payment confirmation and cancellation into the callback endpoint.
func (s *Server) SetOrderService(orderService *service.OrderService) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.orderService = orderService
}

// SetPaymentConfig configures runtime payment settings used by the callback
// endpoint. Set before exposing /payment/callback; nil disables it.
func (s *Server) SetPaymentConfig(c *PaymentConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.paymentConfig = c
}

// RegisterChecker adds a dependency health check used by /healthz and /readyz.
// A checker should honor the context deadline and return a stable component name.
func (s *Server) RegisterChecker(name string, checker func(context.Context) ComponentHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.checkers[name] = checker
}

// SetReady updates the application readiness flag used by readiness checks.
func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ready = ready
}

// SetPaymentReady enables webhook processing only after the real bot and the
// post-commit synchronization service have been wired.
func (s *Server) SetPaymentReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.paymentReady = ready
}

// SetBotUsername updates the runtime Telegram username used in invite links.
func (s *Server) SetBotUsername(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.botUsername = username
}

// effectiveBotUsername returns the bot username for share/invite links.
// It is the runtime-injected username from initBot (set via SetBotUsername);
// the bot username comes from Telegram getMe, not from configuration.
func (s *Server) effectiveBotUsername() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.botUsername
}

// Addr returns the bound listener address, or the configured address before Start.
func (s *Server) Addr() string {
	s.mu.RLock()
	listenerAddr := s.listenerAddr
	s.mu.RUnlock()

	if listenerAddr != "" {
		return listenerAddr
	}

	return s.addr
}

// Start binds the listener, registers all HTTP routes, and serves requests in a
// background goroutine. The context is reserved for lifecycle coordination by
// callers; use Stop to shut the server down gracefully.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/payment/callback", s.handlePaymentCallback)
	mux.HandleFunc("/i/", s.handleInvite)
	mux.HandleFunc("/sub/", s.handleSubscription)
	mux.HandleFunc("/static/logo.png", s.handleLogo)

	mux.Handle("/metrics", promhttp.Handler())

	instrumentedHandler := metrics.InstrumentHTTP(SecurityHeadersMiddleware(mux))

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

	s.mu.Lock()
	s.listenerAddr = listener.Addr().String()
	s.mu.Unlock()

	logger.Info("Web server started", zap.String("addr", s.listenerAddr))
	s.initSubserverAccessLogger()

	go func() {
		defer logger.Recover("HTTP server")

		err := s.server.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", zap.Error(err))
		} else if err == http.ErrServerClosed {
			logger.Info("HTTP server stopped gracefully")
		}
	}()

	return nil
}

// initSubserverAccessLogger creates the optional subscription access logger
// configured by SubServerAccessLogPath. Logging remains disabled when no path is set.
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

// Stop flushes the optional access logger and gracefully shuts down the HTTP
// server. It returns all shutdown errors joined together.
func (s *Server) Stop(ctx context.Context) error {
	var errs []error

	if s.subserverLogger != nil {
		closeCtx, cancel := context.WithTimeout(ctx, subserverAccessLogCloseTimeout)
		defer cancel()

		cerr := s.subserverLogger.CloseWithContext(closeCtx)
		if cerr != nil {
			errs = append(errs, cerr)
		}
	}
	// HTTP server must shut down even if access-log close failed.
	if s.server != nil {
		serr := s.server.Shutdown(ctx)
		if serr != nil {
			errs = append(errs, serr)
		}
	}

	return errors.Join(errs...)
}

// handleHealthz serves the dependency health report. GET and HEAD are accepted;
// a dependency failure is represented in the JSON response and HTTP status.
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

// handleReadyz reports whether the service is ready to receive traffic. Unlike
// healthz, degraded or down dependencies produce a 503 response.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	s.mu.RLock()
	ready := s.ready
	s.mu.RUnlock()

	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)

		_, err := w.Write([]byte("NOT READY"))
		if err != nil {
			logger.Debug("failed to write readiness response", zap.Error(err))
		}

		return
	}

	health := s.checkHealth(ctx)

	if health.Status == "ok" {
		w.WriteHeader(http.StatusOK)

		_, err := w.Write([]byte("OK"))
		if err != nil {
			logger.Debug("failed to write readiness response", zap.Error(err))
		}
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)

		_, err := w.Write([]byte("NOT READY"))
		if err != nil {
			logger.Debug("failed to write readiness response", zap.Error(err))
		}
	}
}

// handlePaymentCallback validates and dispatches Platega webhook events. It
// authenticates the request, enforces a bounded single-JSON body, applies the
// payment state transition, and returns a JSON acknowledgement for accepted
// provider statuses.
func (s *Server) handlePaymentCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)

		return
	}

	// Snapshot the runtime-wired dependencies under the same lock so a concurrent
	// SetBot/SetOrderService cannot be observed as a torn state.
	s.mu.RLock()
	pc := s.paymentConfig
	paymentReady := s.paymentReady
	bot := s.bot
	orderService := s.orderService
	s.mu.RUnlock()

	if !paymentReady || pc == nil || !pc.Enabled || orderService == nil || bot == nil {
		http.Error(w, "payments not available", http.StatusServiceUnavailable)
		return
	}

	if !platega.VerifyHeaders(pc.MerchantID, pc.Secret, r.Header) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Payment state transitions and post-commit user/admin notifications must
	// complete even if the provider closes the connection mid-request. Detach
	// the request context for all follow-up work so an aborted webhook cannot
	// drop a confirmed payment or its alert.
	notifyCtx := context.WithoutCancel(r.Context())
	defer func() { _ = r.Body.Close() }()
	// Read the authenticated callback once so DEBUG can preserve every field
	// sent by the provider, including fields unknown to CallbackPayload.
	limitedBody := http.MaxBytesReader(w, r.Body, 256<<10)

	rawBody, readErr := io.ReadAll(limitedBody)
	if readErr != nil {
		logger.Info("Payment callback rejected",
			zap.String("provider", "platega"),
			zap.String("reason", "body_read_failed"),
			zap.Int("body_bytes", len(rawBody)),
			zap.Error(readErr))
		logger.Debug("Payment callback raw payload",
			zap.ByteString("body", rawBody))
		s.notifyPaymentCallbackIssue(r.Context(), platega.CallbackPayload{}, "malformed_callback", readErr.Error(), "send a corrected callback and verify the provider payload")
		http.Error(w, "invalid callback", http.StatusBadRequest)

		return
	}

	logger.Debug("Payment callback raw payload",
		zap.ByteString("body", rawBody))

	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.UseNumber()
	// Ignore provider fields that are not needed by this integration. Platega
	// may add documented callback fields without requiring a bot deployment.
	var payload platega.CallbackPayload

	err := decoder.Decode(&payload)
	if err != nil {
		logger.Info("Payment callback rejected",
			zap.String("provider", "platega"),
			zap.String("reason", "invalid_json"),
			zap.Int("body_bytes", len(rawBody)),
			zap.Error(err))
		s.notifyPaymentCallbackIssue(r.Context(), payload, "malformed_callback", err.Error(), "send a corrected callback and verify the provider payload")
		http.Error(w, "invalid callback", http.StatusBadRequest)

		return
	}

	logger.Info("Payment callback received",
		zap.String("provider", "platega"),
		zap.String("payment_id", strings.TrimSpace(payload.ID)),
		zap.String("status", strings.ToUpper(strings.TrimSpace(payload.Status))),
		zap.String("currency", strings.TrimSpace(payload.Currency)),
		zap.String("amount", payload.Amount.String()),
		zap.Int("body_bytes", len(rawBody)))

	paymentID, err := platega.ParseTransactionID(payload.ID)
	if err != nil {
		s.notifyPaymentCallbackIssue(r.Context(), payload, "invalid_provider_id", err.Error(), "verify the provider transaction ID and callback schema")
		http.Error(w, "invalid callback", http.StatusBadRequest)

		return
	}

	err = payload.Validate()
	if err != nil {
		s.notifyPaymentCallbackIssue(r.Context(), payload, "invalid_callback", err.Error(), "send a corrected callback and verify the provider schema")
		http.Error(w, "invalid callback", http.StatusBadRequest)

		return
	}

	var trailing json.RawMessage

	err = decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		reason := "callback contains trailing JSON data"
		if err != nil {
			reason = err.Error()
		}

		logger.Info("Payment callback rejected",
			zap.String("provider", "platega"),
			zap.String("payment_id", strings.TrimSpace(payload.ID)),
			zap.String("status", strings.ToUpper(strings.TrimSpace(payload.Status))),
			zap.String("reason", "trailing_callback_data"),
			zap.Int("body_bytes", len(rawBody)),
			zap.Error(err))
		s.notifyPaymentCallbackIssue(r.Context(), payload, "trailing_callback_data", reason, "send exactly one JSON callback document")
		http.Error(w, "invalid callback", http.StatusBadRequest)

		return
	}

	status := strings.ToUpper(strings.TrimSpace(payload.Status))
	switch status {
	case "CONFIRMED":
		confirmation, err := orderService.ConfirmPayment(notifyCtx, paymentID, payload.Amount, payload.Currency)
		if err != nil {
			if errors.Is(err, service.ErrAmountMismatch) || errors.Is(err, service.ErrCurrencyMismatch) || errors.Is(err, service.ErrInvalidPaymentTransition) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				http.Error(w, "processing failed", http.StatusInternalServerError)
			}

			return
		}

		if confirmation.Activated {
			chatID, text, err := orderService.BuildPaidUserNotification(notifyCtx, confirmation.Order)
			if err != nil {
				logger.Warn("failed to build paid notification", zap.Error(err))
				orderService.NotifyPaymentIssue(notifyCtx, service.PaymentIssue{Event: "paid_notification_build_failed", Reason: err.Error(), Action: "send the confirmed payment details to the user manually", OrderID: confirmation.Order.ID, SubscriptionID: confirmation.Order.SubscriptionID, ProductID: confirmation.Order.ProductID, PlanID: 0, AmountCents: confirmation.Order.AmountCents, Currency: confirmation.Order.Currency, ProviderID: payload.ID, CallbackStatus: payload.Status, Payload: payload.Payload, PaymentMethod: payload.PaymentMethod})
			} else if chatID > 0 && bot != nil {
				userMessage := tgbotapi.NewMessage(chatID, text)
				userMessage.ParseMode = tgbotapi.ModeMarkdown
				_, err := bot.Send(userMessage)
				if err != nil {
					logger.Warn("failed to send paid notification", zap.Int64("chat_id", chatID), zap.Error(err))
					orderService.NotifyPaymentIssue(notifyCtx, service.PaymentIssue{Event: "paid_notification_send_failed", Reason: err.Error(), Action: "send the confirmed payment details to the user manually", OrderID: confirmation.Order.ID, TelegramID: chatID, SubscriptionID: confirmation.Order.SubscriptionID, ProductID: confirmation.Order.ProductID, AmountCents: confirmation.Order.AmountCents, Currency: confirmation.Order.Currency, ProviderID: payload.ID, CallbackStatus: payload.Status, Payload: payload.Payload, PaymentMethod: payload.PaymentMethod})
				}
			}
		}
	case "CANCELED", "CHARGEBACKED":
		// Record the cancellation/chargeback transition. A chargeback on a
		// previously-paid order automatically downgrades the subscription to the
		// free plan inside CancelPaymentByProvider (unless another paid order
		// exists); a plain CANCELED only cancels the pending order.
		_, _, err = orderService.CancelPaymentByProvider(notifyCtx, paymentID, status, payload.Amount, payload.Currency)
		if err != nil {
			if errors.Is(err, service.ErrAmountMismatch) || errors.Is(err, service.ErrCurrencyMismatch) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				http.Error(w, "processing failed", http.StatusInternalServerError)
			}

			return
		}

		if status == "CHARGEBACKED" {
			logger.Warn("chargeback callback recorded; manual review required", zap.String("payment_id", payload.ID), zap.String("payload", payload.Payload))
		}
	case "PENDING":
		logger.Warn("ignored pending payment callback", zap.String("payment_id", payload.ID))
	default:
		logger.Warn("ignored unsupported payment callback status", zap.String("status", payload.Status), zap.String("payment_id", payload.ID))
		s.notifyPaymentCallbackIssue(r.Context(), payload, "unsupported_callback_status", fmt.Sprintf("unsupported callback status %q", payload.Status), "verify the provider status and update the integration only after documentation confirms it")
	}

	w.Header().Set("Content-Type", "application/json")

	err = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	if err != nil {
		logger.Warn("failed to write payment callback response", zap.Error(err))
	}
}

// notifyPaymentCallbackIssue sends malformed or operational callback details
// to the payment service, which logs the event and alerts the administrator.
// The context is detached from the request so a disconnected client cannot
// suppress the operational alert.
func (s *Server) notifyPaymentCallbackIssue(ctx context.Context, payload platega.CallbackPayload, event, reason, action string) {
	s.mu.RLock()
	orderService := s.orderService
	s.mu.RUnlock()

	if orderService == nil {
		return
	}

	providerID := strings.TrimSpace(payload.ID)
	callbackPayload := fmt.Sprintf("amount=%s; payload=%s", payload.Amount.String(), payload.Payload)
	orderService.NotifyPaymentIssue(context.WithoutCancel(ctx), service.PaymentIssue{
		Event:          event,
		Reason:         reason,
		Action:         action,
		ProviderID:     providerID,
		Currency:       payload.Currency,
		CallbackStatus: payload.Status,
		Payload:        callbackPayload,
		PaymentMethod:  payload.PaymentMethod,
	})
}

// handleLogo serves the embedded mobile-optimized logo with a long cache lifetime.
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

	_, err = w.Write(data)
	if err != nil {
		logger.Debug("failed to write logo response", zap.Error(err))
	}
}

// HealthResponse is the JSON document returned by the health endpoint.
type HealthResponse struct {
	Status     string                     `json:"status"`
	Components map[string]ComponentHealth `json:"components"`
	Timestamp  time.Time                  `json:"timestamp"`
	Uptime     string                     `json:"uptime"`
}

// checkHealth runs a snapshot of all registered dependency checkers and folds
// their results into one aggregate status.
func (s *Server) checkHealth(ctx context.Context) HealthResponse {
	s.mu.RLock()

	checkers := make(map[string]func(context.Context) ComponentHealth, len(s.checkers))
	maps.Copy(checkers, s.checkers)

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

// writeJSON writes a health response and maps a down aggregate status to HTTP 503.
func (s *Server) writeJSON(w http.ResponseWriter, resp HealthResponse) {
	w.Header().Set("Content-Type", "application/json")

	switch resp.Status {
	case string(StatusDown):
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		w.WriteHeader(http.StatusOK)
	}

	err := json.NewEncoder(w).Encode(resp)
	if err != nil {
		logger.Error("Failed to encode JSON response", zap.Error(err))
	}
}

// handleInvite is the route adapter for the public invite landing page.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	s.HandleInvite(w, r)
}

// HandleInvite validates an invite code, reuses an unactivated trial from the
// visitor cookie when possible, enforces the IP rate limit, and renders the trial
// landing page for a newly created subscription.
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
		telegramLink := "https://t.me/" + s.effectiveBotUsername() + "?start=trial_" + existingSub.SubscriptionID

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		subURL := s.cfg.GlobalSubURL + existingSub.SubscriptionID
		s.renderTrialPage(w, existingSub.SubscriptionID, subURL, telegramLink, s.cfg.TrialDurationHours)

		return
	}

	ip := getClientIP(r)

	// Serialize the check-and-record pair. Without this, concurrent requests
	// from one IP can all observe the same count and bypass the limit.
	s.trialRateMu.Lock()

	count, err := s.db.CountTrialRequestsByIPLastHour(ctx, ip)
	if err != nil {
		logger.Error("Failed to check rate limit", zap.Error(err), zap.String("ip", ip))
		s.trialRateMu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		s.renderErrorPage(w, "Ошибка сервера. Попробуйте позже.")

		return
	}

	if count >= s.cfg.TrialRateLimit {
		logger.Warn("Rate limit exceeded", zap.String("ip", ip), zap.Int("count", count))
		s.trialRateMu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		s.renderErrorPage(w, "Слишком много запросов. Попробуйте позже.")

		return
	}

	err = s.db.CreateTrialRequest(ctx, ip)
	if err != nil {
		logger.Error("Failed to create trial request", zap.Error(err))
		s.trialRateMu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		s.renderErrorPage(w, "Ошибка сервера. Попробуйте позже.")

		return
	}
	s.trialRateMu.Unlock()

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

	telegramLink := "https://t.me/" + s.effectiveBotUsername() + "?start=trial_" + result.SubID
	s.renderTrialPage(w, result.SubID, result.SubscriptionURL, telegramLink, s.cfg.TrialDurationHours)
}

// getExistingTrialFromCookie checks the cookie and returns an existing unactivated trial.
// Expected business states (no cookie, empty, not a trial, already activated, expired) return (nil, nil).
// Only infrastructure/DB failures return (nil, error); those are logged as Error by the caller.
func (s *Server) getExistingTrialFromCookie(r *http.Request, ctx context.Context, code string) (*database.Subscription, error) {
	cookie, err := r.Cookie("rs8kvn_trial_" + code)
	if err != nil {
		// No cookie (new visitor) — expected, not an error.
		//nolint:nilerr // no cookie means new visitor, expected business state
		return nil, nil
	}

	subID := cookie.Value
	if subID == "" {
		// Malformed cookie — expected business state.
		return nil, nil
	}

	sub, err := s.db.GetTrialSubscriptionBySubID(ctx, subID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			// Trial was cleaned up or not found — expected, fall through to new trial creation.
			return nil, nil
		}

		return nil, fmt.Errorf("get trial subscription by sub id: %w", err)
	}

	// Already activated — expected, no existing trial to show.
	if sub.TelegramID > 0 {
		return nil, nil
	}

	plan, planErr := s.db.GetPlanByID(ctx, sub.PlanID)
	if planErr != nil {
		return nil, fmt.Errorf("get plan for trial check: %w", planErr)
	}

	if plan.Name != database.TrialPlanName {
		// Not a trial — expected business state.
		return nil, nil
	}

	// Expired — expected business state.
	if sub.ExpiresAt != nil && time.Now().After(*sub.ExpiresAt) {
		return nil, nil
	}

	return sub, nil
}

// trialPageData contains the server-generated links and duration displayed by
// the invite landing page template.
type trialPageData struct {
	HappLink     template.URL
	SubURL       string
	TelegramLink template.URL
	TrialHours   int
}

// renderTrialPage executes the invite landing page template using server-generated
// subscription and Telegram links.
func (s *Server) renderTrialPage(w http.ResponseWriter, subID, subURL, telegramLink string, trialHours int) {
	happLink := "happ://add/" + subURL

	data := trialPageData{
		HappLink:     template.URL(happLink), // #nosec G203 -- scheme is server-generated from validated subscription URL
		SubURL:       subURL,
		TelegramLink: template.URL(telegramLink), // #nosec G203 -- link is server-generated from the Telegram username and subscription ID
		TrialHours:   trialHours,
	}

	err := s.trialTemplate.Execute(w, data)
	if err != nil {
		logger.Error("Failed to render trial page", zap.Error(err))
	}
}

// errorPageData is the view model for the generic HTML error page.
type errorPageData struct {
	Message string
}

// renderErrorPage renders a user-facing error without exposing internal details.
func (s *Server) renderErrorPage(w http.ResponseWriter, message string) {
	data := errorPageData{Message: message}

	err := s.errorTemplate.Execute(w, data)
	if err != nil {
		logger.Error("Failed to render error page", zap.Error(err))
	}
}

// getClientIP extracts the client address for rate limiting and access logs.
// Proxy headers are trusted only when the direct peer is local or private.
func getClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && isLocalAddress(host) {
		// X-Real-IP is a single value set by the trusted reverse proxy.
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}

		forwarded := r.Header.Get("X-Forwarded-For")
		if forwarded != "" {
			ips := strings.Split(forwarded, ",")
			// Use the rightmost IP — set by the trusted reverse proxy.
			// The leftmost is client-controlled and spoofable.
			for _, v := range slices.Backward(ips) {
				ip := strings.TrimSpace(v)
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

// isLocalAddress reports whether a peer belongs to loopback or a private network.
// Such peers are allowed to supply reverse-proxy client IP headers.
func isLocalAddress(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	// Trust loopback and private ranges: in Docker the direct peer is the
	// bridge gateway (e.g. 172.19.0.1), which is private, not loopback.
	// nginx runs behind this gateway and sets X-Real-IP / X-Forwarded-For.
	// ponytail: trusts any private peer; tighten to specific proxy CIDRs if the bridge is ever shared with untrusted containers.
	return ip.IsLoopback() || ip.IsPrivate()
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

	var (
		rec      *statusRecorder
		response = w
	)
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

	// Use a generous timeout for multi-source aggregation: with up to 8 concurrent
	// fetches (maxSourceConcurrency) each taking up to 10s, we need headroom beyond
	// a single fetch timeout to avoid premature cancellation under load.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	logger.Debug("subscription request received",
		zap.String("sub_id", subID),
		zap.String("client_ip", clientIP),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)

	requestHeaders := subserver.FilterHeaders(r.Header)

	result, success, total, err := subserver.HandleSubscription(ctx, s.db, s.subServer, subID, clientIP, requestHeaders)
	if rec != nil {
		rec.success = success
		rec.total = total
	}

	if err != nil {
		if errors.Is(err, subserver.ErrSubscriptionNotFound) {
			logger.Warn("Subscription not found",
				zap.String("sub_id", subID),
				zap.String("client_ip", clientIP))
			writeSubscriptionText(response, http.StatusNotFound, "Subscription not found")

			return
		}

		logger.Error("Failed to process subscription",
			zap.String("sub_id", subID),
			zap.String("client_ip", clientIP),
			zap.Error(err))

		if errors.Is(err, gorm.ErrRecordNotFound) ||
			errors.Is(err, subserver.ErrNoSubscriptionItems) {
			writeSubscriptionText(response, http.StatusNotFound, "Subscription not found")
			return
		}

		writeSubscriptionText(response, http.StatusInternalServerError, "Internal Server Error")

		return
	}

	if result == nil {
		logger.Error("Empty subscription result",
			zap.String("sub_id", subID),
			zap.String("client_ip", clientIP))
		writeSubscriptionText(response, http.StatusInternalServerError, "Internal Server Error")

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

	_, err = response.Write(result.Body) // #nosec G705 -- body is intentionally returned as the subscription's plain response
	if err != nil {
		logger.Debug("failed to write subscription response", zap.Error(err))
	}
}

// logSubscriptionAccess records the completed subscription response after the
// handler has captured its status, source counts, and success count.
func (s *Server) logSubscriptionAccess(rec *statusRecorder, r *http.Request, clientIP string) {
	if s == nil || s.subserverLogger == nil || rec == nil {
		return
	}

	s.subserverLogger.Log(r, rec.StatusCode(), clientIP, rec.success, rec.total)
}

// writeSubscriptionText writes a plain-text subscription response with the
// supplied status code and a consistent content type.
func writeSubscriptionText(w http.ResponseWriter, statusCode int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)

	_, err := w.Write([]byte(body))
	if err != nil {
		logger.Debug("failed to write subscription text response", zap.Error(err))
	}
}

// statusRecorder captures the response status and subscription aggregation counts
// while preserving the underlying ResponseWriter behavior.
type statusRecorder struct {
	http.ResponseWriter

	statusCode int
	success    int
	total      int
}

// WriteHeader records the first response status before forwarding the call.
func (r *statusRecorder) WriteHeader(statusCode int) {
	if r.statusCode == 0 {
		r.statusCode = statusCode
	}

	r.ResponseWriter.WriteHeader(statusCode)
}

// Write defaults an unwritten response to 200 and forwards the body bytes.
func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}

	return r.ResponseWriter.Write(b)
}

// StatusCode returns the recorded status, defaulting to 200 when nothing was written.
func (r *statusRecorder) StatusCode() int {
	if r.statusCode == 0 {
		return http.StatusOK
	}

	return r.statusCode
}
