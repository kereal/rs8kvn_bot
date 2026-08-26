package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldSkipHTTPMetrics(t *testing.T) {
	t.Parallel()

	assert.True(t, shouldSkipHTTPMetrics("/metrics"))
	assert.True(t, shouldSkipHTTPMetrics("/static/logo.png"))
	assert.True(t, shouldSkipHTTPMetrics("/static/js/app.js"))
	assert.False(t, shouldSkipHTTPMetrics("/sub/abc"))
	assert.False(t, shouldSkipHTTPMetrics("/payment/callback"))
}

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty path", "", ""},
		{"root", "/", "/"},
		{"static health", "/healthz", "/healthz"},
		{"static ready", "/readyz", "/readyz"},
		{"static api", "/api/v1/subscriptions", "/api/v1/subscriptions"},
		{"invite with code", "/i/abc12345", "/i/:code"},
		{"invite with long code", "/i/abcdef1234567890", "/i/:code"},
		{"invite with subpath", "/i/abc/sub", "/i/:code"},
		{"subscription id", "/sub/abc-123-xyz", "/sub/:id"},
		{"subscription uuid", "/sub/550e8400-e29b-41d4-a716-446655440000", "/sub/:id"},
		{"static after slash", "/static/logo.png", "/static/logo.png"},
		{"mixed static", "/api/v1/users/123", "/api/v1/users/123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizePath(tt.in))
		})
	}
}

func TestStatusCodeString(t *testing.T) {
	t.Parallel()

	rr := &responseRecorder{statusCode: 200}
	assert.Equal(t, "OK", rr.statusCodeString())

	rr.statusCode = 404
	assert.Equal(t, "Not Found", rr.statusCodeString())

	rr.statusCode = 500
	assert.Equal(t, "Internal Server Error", rr.statusCodeString())
}

func TestNewMetricsInitialized(t *testing.T) {
	require.NotNil(t, HTTPRequestsTotal)
	require.NotNil(t, HTTPRequestDuration)
	require.NotNil(t, BotUpdatesTotal)
	require.NotNil(t, BotUpdateDuration)
	require.NotNil(t, XUIRequestsTotal)
	require.NotNil(t, XUIRequestDuration)
	require.NotNil(t, DBQueriesTotal)
	require.NotNil(t, DBQueryDuration)
	require.NotNil(t, CacheHitsTotal)
	require.NotNil(t, CacheMissesTotal)
	require.NotNil(t, ActiveSubscriptions)
	require.NotNil(t, PremiumSubscriptions)
	require.NotNil(t, FreeSubscriptions)
	require.NotNil(t, SubscriptionCreatesTotal)
	require.NotNil(t, SubscriptionRenewalsTotal)
	require.NotNil(t, SubscriptionSyncTotal)
	require.NotNil(t, SubscriptionSyncDuration)
	require.NotNil(t, SubscriptionExpireTotal)
	require.NotNil(t, SubscriptionExpireDuration)
	require.NotNil(t, ReconcileOrphanedDuration)
	require.NotNil(t, OrphanedClientsRevokedTotal)
	require.NotNil(t, SubserverSourceFetchTotal)
	require.NotNil(t, SubserverSourceFetchDuration)
	require.NotNil(t, SubserverCacheInvalidationsTotal)
	require.NotNil(t, SubserverNoItemsTotal)
	require.NotNil(t, PaymentOperationsTotal)
	require.NotNil(t, PaymentOperationDuration)
	require.NotNil(t, PaymentAmountCentsTotal)
	require.NotNil(t, PaymentIssuesTotal)
}

func TestInstrumentHTTPSkipsMetricsAndStatic(t *testing.T) {
	t.Parallel()

	handler := InstrumentHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	beforeRequests := metricSamples(t, HTTPRequestsTotal)
	beforeDuration := metricSamples(t, HTTPRequestDuration)

	for _, path := range []string{"/metrics", "/static/app.js"} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, resp.Code)
	}

	assert.Equal(t, beforeRequests, metricSamples(t, HTTPRequestsTotal), "skipped routes must not record request samples")
	assert.Equal(t, beforeDuration, metricSamples(t, HTTPRequestDuration), "skipped routes must not record duration samples")

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Greater(t, metricSamples(t, HTTPRequestsTotal), beforeRequests)
	assert.Greater(t, metricSamples(t, HTTPRequestDuration), beforeDuration)
}

func metricSamples(t *testing.T, collector prometheus.Collector) int {
	t.Helper()

	ch := make(chan prometheus.Metric, 1)
	go func() {
		collector.Collect(ch)
		close(ch)
	}()

	count := 0
	for metric := range ch {
		dtoMetric := &dto.Metric{}
		if metric.Write(dtoMetric) == nil {
			count++
		}
	}
	return count
}

func TestMetricsEndpoint(t *testing.T) {
	t.Parallel()

	handler := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{})
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "active_subscriptions")
	assert.Contains(t, resp.Body.String(), "subscription_creates_total")
	assert.Contains(t, resp.Body.String(), "subscription_renewals_total")
	assert.Contains(t, resp.Body.String(), "subscription_sync_total")
	assert.Contains(t, resp.Body.String(), "subscription_expire_total")
	assert.Contains(t, resp.Body.String(), "reconcile_orphaned_duration_seconds")
}
