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
	// HTTPRequestsTotal is a counter of total HTTP requests with labels: method, path, status.
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration is a histogram of HTTP request durations in seconds with labels: method, path.
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	// HTTPRequestsInFlight is a gauge of current HTTP requests being processed with labels: method, path.
	HTTPRequestsInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Current number of HTTP requests being processed",
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

	// BotUpdateErrorsTotal is a counter of errors during bot update processing with label: type.
	BotUpdateErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bot_update_errors_total",
			Help: "Total number of errors during bot update processing",
		},
		[]string{"type"},
	)

	// BotUpdateDuration is a histogram of bot update processing duration in seconds.
	BotUpdateDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "bot_update_duration_seconds",
			Help:    "Bot update processing duration in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.3, 0.6, 1, 2},
		},
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

	// CircuitBreakerState is a gauge of circuit breaker state (0=closed, 1=open, 2=half-open) with label: target.
	CircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
		},
		[]string{"target"},
	)

	// ActiveSubscriptions is a gauge of current active subscriptions.
	ActiveSubscriptions = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_subscriptions",
			Help: "Current number of active subscriptions",
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

	// OrphanedClientsRemovedTotal counts XUI clients/subscriptions removed
	// during background reconciliation of orphaned entries.
	OrphanedClientsRemovedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "bot_orphaned_clients_removed_total",
			Help: "Total number of orphaned XUI clients/subscriptions removed during reconciliation",
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

	// SubserverCacheHitDuration is a histogram of time spent serving
	// a subscription response from cache (including DB revalidation).
	SubserverCacheHitDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "subserver_cache_hit_duration_seconds",
			Help:    "Time spent serving subscription from cache",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
	)

	// SubserverCacheMissDuration is a histogram of time spent building
	// a subscription response on cache miss (DB load + upstream fetch + aggregation).
	SubserverCacheMissDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "subserver_cache_miss_duration_seconds",
			Help:    "Time spent building subscription response on cache miss",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		},
	)
)

// InstrumentHTTP middleware records metrics for HTTP requests.
func InstrumentHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := normalizePath(r.URL.Path)
		method := r.Method

		HTTPRequestsInFlight.WithLabelValues(method, path).Inc()
		defer HTTPRequestsInFlight.WithLabelValues(method, path).Dec()

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
func normalizePath(p string) string {
	// Dynamic routes with slash separator
	if strings.HasPrefix(p, "/i/") {
		return "/i/:code"
	}

	if strings.HasPrefix(p, "/sub/") {
		return "/sub/:id"
	}

	// Static/known paths pass through unchanged
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
