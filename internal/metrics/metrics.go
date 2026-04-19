package metrics

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP metrics
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	HTTPRequestsInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Current number of HTTP requests being processed",
		},
		[]string{"method", "path"},
	)

	// Bot metrics
	BotUpdatesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bot_updates_total",
			Help: "Total number of bot updates processed",
		},
		[]string{"command", "result"}, // result: success, error, rate_limited
	)

	BotUpdateErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bot_update_errors_total",
			Help: "Total number of errors during bot update processing",
		},
		[]string{"type"},
	)

	BotUpdateDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "bot_update_duration_seconds",
			Help:    "Bot update processing duration in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.3, 0.6, 1, 2},
		},
	)

	// XUI client metrics
	XUIRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xui_requests_total",
			Help: "Total number of requests to 3x-ui panel",
		},
		[]string{"operation", "result"}, // result: success, error
	)

	XUIRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xui_request_duration_seconds",
			Help:    "XUI request duration in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.3, 0.6, 1, 2, 5},
		},
		[]string{"operation"},
	)

	// Database metrics
	DBQueriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_queries_total",
			Help: "Total number of database queries",
		},
		[]string{"operation", "result"},
	)

	DBQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database query duration in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
		},
		[]string{"operation"},
	)

	// Cache metrics
	CacheHitsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_hits_total",
			Help: "Total number of cache hits",
		},
		[]string{"cache"}, // cache: subscription, referral, subproxy
	)

	CacheMissesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_misses_total",
			Help: "Total number of cache misses",
		},
		[]string{"cache"},
	)

	// Circuit breaker metrics
	CircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
		},
		[]string{"target"},
	)

	// Subscription metrics
	ActiveSubscriptions = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_subscriptions",
			Help: "Current number of active subscriptions",
		},
	)

	TrialConversionsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "trial_conversions_total",
			Help: "Total number of trial conversions to paid subscriptions",
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
	// Dynamic invite code: /i/<code> -> /i/:code
	if len(p) > 3 && p[:2] == "/i" && p[2] != '/' {
		// e.g., /iABC123 without slash - treat as /i with code
		return "/i/:code"
	}
	// Known dynamic routes with slash separator
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

func (r *responseRecorder) statusCodeString() string {
	return http.StatusText(r.statusCode)
}
