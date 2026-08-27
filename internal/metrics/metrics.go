// Package metrics defines Prometheus collectors and HTTP middleware for
// observability across the bot, web, subserver, xui, and service layers.
// All collectors are registered at init time via promauto, so they appear
// on the /metrics endpoint without explicit registration.
package metrics

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTPRequestsTotal is a counter of application HTTP requests. The metrics
	// endpoint and static assets are intentionally excluded.
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration is a histogram of application HTTP request durations.
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	// BotUpdatesTotal is a counter of bot updates processed with labels: command, result.
	// result values: success, error, rate_limited
	BotUpdatesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bot_updates_total",
			Help: "Total number of bot updates processed",
		},
		[]string{"command", "result"},
	)

	// BotUpdateDuration is a histogram of bot update processing duration in seconds with label: command.
	BotUpdateDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "bot_update_duration_seconds",
			Help:    "Bot update processing duration in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.3, 0.6, 1, 2},
		},
		[]string{"command"},
	)

	// XUIRequestsTotal is a counter of requests to 3x-ui panel with labels: operation, result.
	// result values: success, error
	XUIRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xui_requests_total",
			Help: "Total number of requests to 3x-ui panel",
		},
		[]string{"operation", "result"},
	)

	// XUIRequestDuration is a histogram of XUI request duration in seconds with label: operation.
	XUIRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xui_request_duration_seconds",
			Help:    "XUI request duration in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.3, 0.6, 1, 2, 5},
		},
		[]string{"operation"},
	)

	// TelegramAPICallsTotal is a counter of Telegram Bot API calls with labels: method, result.
	TelegramAPICallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "telegram_api_calls_total",
			Help: "Total number of Telegram Bot API calls",
		},
		[]string{"method", "result"},
	)

	// TelegramAPIDuration is a histogram of Telegram Bot API call duration in seconds.
	TelegramAPIDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "telegram_api_duration_seconds",
			Help:    "Telegram Bot API call duration in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.3, 0.6, 1, 2, 5},
		},
		[]string{"method"},
	)

	// DBQueriesTotal is a counter of database queries with labels: operation, result.
	DBQueriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_queries_total",
			Help: "Total number of database queries",
		},
		[]string{"operation", "result"},
	)

	// DBQueryDuration is a histogram of database query duration in seconds with label: operation.
	DBQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database query duration in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
		},
		[]string{"operation"},
	)

	// DBPoolWait is a gauge reflecting the cumulative number of times a
	// database connection wait exceeded the pool (sql.DBStats.WaitCount).
	DBPoolWait = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "db_pool_wait",
			Help: "Total number of times a database connection wait exceeded the pool",
		},
	)

	// CacheHitsTotal is a counter of cache hits with label: cache.
	// cache values: subscription, referral, subserver
	CacheHitsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_hits_total",
			Help: "Total number of cache hits",
		},
		[]string{"cache"},
	)

	// CacheMissesTotal is a counter of cache misses with label: cache.
	CacheMissesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_misses_total",
			Help: "Total number of cache misses",
		},
		[]string{"cache"},
	)

	// ActiveSubscriptions is a gauge of current active subscriptions.
	ActiveSubscriptions = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_subscriptions",
			Help: "Current number of active subscriptions",
		},
	)

	// PremiumSubscriptions is a gauge of current active paid subscriptions.
	PremiumSubscriptions = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "premium_subscriptions",
			Help: "Current number of active paid subscriptions",
		},
	)

	// FreeSubscriptions is a gauge of active free subscriptions.
	FreeSubscriptions = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "free_subscriptions",
			Help: "Current number of active free subscriptions",
		},
	)

	// TrialSubscriptions is a gauge of current trial subscriptions (telegramID=0).
	TrialSubscriptions = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "trial_subscriptions",
			Help: "Current number of trial subscriptions",
		},
	)

	// SubscriptionCreatesTotal counts new subscription creations.
	SubscriptionCreatesTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "subscription_creates_total",
			Help: "Total number of subscription creations",
		},
	)

	// SubscriptionRenewalsTotal counts subscription renewals/activations.
	SubscriptionRenewalsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "subscription_renewals_total",
			Help: "Total number of subscription renewals or activations",
		},
	)

	// SubscriptionSyncTotal counts subscription sync worker runs.
	SubscriptionSyncTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "subscription_sync_total",
			Help: "Total number of subscription sync worker runs",
		},
	)

	// SubscriptionSyncDuration is a histogram of subscription sync worker duration.
	SubscriptionSyncDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "subscription_sync_duration_seconds",
			Help:    "Subscription sync worker duration in seconds",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
	)

	// SubscriptionExpireTotal counts subscription expire worker runs.
	SubscriptionExpireTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "subscription_expire_total",
			Help: "Total number of subscription expire worker runs",
		},
	)

	// SubscriptionExpireDuration is a histogram of subscription expire worker duration.
	SubscriptionExpireDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "subscription_expire_duration_seconds",
			Help:    "Subscription expire worker duration in seconds",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
	)

	// ReconcileOrphanedDuration is a histogram of orphaned client reconciliation duration.
	ReconcileOrphanedDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "reconcile_orphaned_duration_seconds",
			Help:    "Orphaned client reconciliation duration in seconds",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
	)

	// OrphanedClientsRevokedTotal counts subscriptions revoked (not deleted —
	// policy: subscriptions are never removed except via admin /del) during
	// background reconciliation of orphaned entries.
	OrphanedClientsRevokedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "bot_orphaned_clients_revoked_total",
			Help: "Total number of orphaned subscriptions revoked during reconciliation",
		},
	)

	// SubserverSourceFetchTotal is a counter of upstream source fetches
	// with labels: result (success|error), format (json|base64|plain|unknown).
	SubserverSourceFetchTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "subserver_source_fetch_total",
			Help: "Total upstream source fetches by result and detected format",
		},
		[]string{"result", "format"},
	)

	// SubserverSourceFetchDuration is a histogram of time spent fetching a
	// single upstream subscription source, with label: result (success|error).
	SubserverSourceFetchDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "subserver_source_fetch_duration_seconds",
			Help:    "Time spent fetching a single upstream subscription source",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
		},
		[]string{"result"},
	)

	// SubserverCacheInvalidationsTotal is a counter of cache invalidations
	// with label: reason (revoked|expired|status_error|not_found).
	SubserverCacheInvalidationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "subserver_cache_invalidations_total",
			Help: "Cache invalidations by reason",
		},
		[]string{"reason"},
	)

	// SubserverNoItemsTotal is a counter of requests where no subscription
	// items could be collected from any source.
	SubserverNoItemsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "subserver_no_items_total",
			Help: "Total subscription requests with no items collected",
		},
	)

	// PaymentOperationsTotal counts payment service operations by operation and result.
	// Operation values: request, confirm, cancel. Result values: success, error.
	PaymentOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_operations_total",
			Help: "Total payment service operations by operation and result",
		},
		[]string{"operation", "result"},
	)

	// PaymentOperationDuration measures payment service operation latency.
	// Operation values: request, confirm, cancel.
	PaymentOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_operation_duration_seconds",
			Help:    "Payment service operation duration in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30},
		},
		[]string{"operation"},
	)

	// PaymentAmountCentsTotal counts monetary amounts in cents by business
	// outcome and currency. Operation values: confirmed, chargeback.
	// Currency is expected to be a small ISO 4217-like set (for example, RUB).
	PaymentAmountCentsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_amount_cents_total",
			Help: "Total payment amounts in cents by outcome and currency",
		},
		[]string{"operation", "currency"},
	)

	// PaymentIssuesTotal counts operational payment issues by stable event name.
	// Event names are defined by the payment integration and never contain IDs.
	PaymentIssuesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_issues_total",
			Help: "Total operational payment issues by event",
		},
		[]string{"event"},
	)
) // SubscriptionRemindersTotal counts reminder sends by expiry window and result.
var SubscriptionRemindersTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "subscription_reminders_total",
		Help: "Total number of subscription expiry reminder sends by window and result.",
	},
	[]string{"window", "result"},
)

// SubscriptionReminderRunsTotal counts reminder worker scans.
var SubscriptionReminderRunsTotal = promauto.NewCounter(
	prometheus.CounterOpts{Name: "subscription_reminder_runs_total",

		Help: "Total number of subscription reminder worker scans.",
	},
)

// TrafficNotificationsTotal counts traffic notification sends by kind and result.
var TrafficNotificationsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "traffic_notifications_total",
		Help: "Total number of traffic notification sends by kind and result.",
	},
	[]string{"kind", "result"},
)

// TrafficNotificationRunsTotal counts traffic notification worker scans.
var TrafficNotificationRunsTotal = promauto.NewCounter(
	prometheus.CounterOpts{Name: "traffic_notification_runs_total",

		Help: "Total number of traffic notification worker scans.",
	},
)

// InstrumentHTTP middleware records metrics for HTTP requests.
func InstrumentHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldSkipHTTPMetrics(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		path := normalizePath(r.URL.Path)
		method := r.Method

		start := time.Now()
		rr := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rr, r)

		duration := time.Since(start).Seconds()
		HTTPRequestDuration.WithLabelValues(method, path).Observe(duration)
		HTTPRequestsTotal.WithLabelValues(method, path, rr.statusCodeString()).Inc()
	})
}

// normalizePath reduces cardinality by replacing dynamic path segments
// (such as invite codes, subscription IDs, UUIDs) with placeholders.
func shouldSkipHTTPMetrics(path string) bool {
	return path == "/metrics" || path == "/static/logo.png" || strings.HasPrefix(path, "/static/")
}

func normalizePath(p string) string {
	// Dynamic routes with slash separator
	if strings.HasPrefix(p, "/i/") {
		return "/i/:code"
	}

	if strings.HasPrefix(p, "/sub/") {
		return "/sub/:id"
	}

	// Static/known application paths pass through unchanged.
	return p
}

// responseRecorder wraps ResponseWriter to capture status code.
type responseRecorder struct {
	http.ResponseWriter

	statusCode int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker by delegating to the underlying ResponseWriter.
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}

	return nil, nil, http.ErrNotSupported
}

// Flush implements http.Flusher by delegating to the underlying ResponseWriter.
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Push implements http.Pusher by delegating to the underlying ResponseWriter.
func (r *responseRecorder) Push(target string, opts *http.PushOptions) error {
	if p, ok := r.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}

	return http.ErrNotSupported
}

func (r *responseRecorder) statusCodeString() string {
	return http.StatusText(r.statusCode)
}
