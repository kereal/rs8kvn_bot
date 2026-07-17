package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

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
	require.NotNil(t, HTTPRequestsInFlight)
	require.NotNil(t, BotUpdatesTotal)
	require.NotNil(t, BotUpdateErrorsTotal)
	require.NotNil(t, BotUpdateDuration)
	require.NotNil(t, XUIRequestsTotal)
	require.NotNil(t, XUIRequestDuration)
	require.NotNil(t, DBQueriesTotal)
	require.NotNil(t, DBQueryDuration)
	require.NotNil(t, CacheHitsTotal)
	require.NotNil(t, CacheMissesTotal)
	require.NotNil(t, CircuitBreakerState)
	require.NotNil(t, ActiveSubscriptions)
	require.NotNil(t, SubscriptionCreatesTotal)
	require.NotNil(t, SubscriptionRenewalsTotal)
	require.NotNil(t, SubscriptionSyncTotal)
	require.NotNil(t, SubscriptionSyncDuration)
	require.NotNil(t, SubscriptionExpireTotal)
	require.NotNil(t, SubscriptionExpireDuration)
	require.NotNil(t, ReconcileOrphanedDuration)
	require.NotNil(t, OrphanedClientsRemovedTotal)
	require.NotNil(t, SubserverSourceFetchTotal)
	require.NotNil(t, SubserverSourceFetchDuration)
	require.NotNil(t, SubserverCacheInvalidationsTotal)
	require.NotNil(t, SubserverNoItemsTotal)
	require.NotNil(t, SubserverCacheHitDuration)
	require.NotNil(t, SubserverCacheMissDuration)
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
