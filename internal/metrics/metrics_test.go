package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	prometheus_testutil "github.com/prometheus/client_golang/prometheus/testutil"
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
		{"empty path", "", "other"},
		{"static health", "/healthz", "/healthz"},
		{"static ready", "/readyz", "/readyz"},
		{"unknown path is collapsed", "/api/v1/subscriptions", "other"},
		{"scanner noise is collapsed", "/wp-login.php", "other"},
		{"root is not a known route", "/", "other"},
		{"invite with code", "/i/abc12345", "/i/:code"},
		{"invite with long code", "/i/abcdef1234567890", "/i/:code"},
		{"invite with subpath", "/i/abc/sub", "/i/:code"},
		{"subscription id", "/sub/abc-123-xyz", "/sub/:id"},
		{"subscription uuid", "/sub/550e8400-e29b-41d4-a716-446655440000", "/sub/:id"},
		{"static after slash", "/static/logo.png", "/static/logo.png"},
		{"mixed static", "/api/v1/users/123", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizePath(tt.in))
		})
	}
}

func TestCodeLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code int
		want string
	}{
		{200, "200"},
		{404, "404"},
		{503, "503"},
		{0, "0"},
	}

	for _, tt := range tests {
		rr := &responseRecorder{statusCode: tt.code}
		assert.Equal(t, tt.want, rr.codeLabel(), "статус %d должен оставаться числом", tt.code)
	}
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
	require.NotNil(t, SubscriptionReanimationsTotal)
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

	// Число серий по конкретному пути, а не по коллектору: глобальное сравнение
	// «до/после» ломается при -count>1, где серия создана предыдущим прогоном.
	for _, path := range []string{"/metrics", "/static/app.js"} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, resp.Code)
	}

	assert.Equal(t, 0, samplesForPath(t, HTTPRequestsTotal, "/metrics"), "skipped /metrics не создаёт серий")
	assert.Equal(t, 0, samplesForPath(t, HTTPRequestDuration, "/metrics"), "skipped /metrics не создаёт серий латентности")
	assert.Equal(t, 0, samplesForPath(t, HTTPRequestsTotal, "/static/app.js"), "static не создаёт серий")

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, 1, samplesForPath(t, HTTPRequestsTotal, "/healthz"), "обычный путь обязан создать серию")
	assert.Equal(t, 1, samplesForPath(t, HTTPRequestDuration, "/healthz"), "обычный путь обязан создать серию латентности")
}

// samplesForPath считает серии коллектора с меткой path, равной указанной.
func samplesForPath(t *testing.T, collector prometheus.Collector, path string) int {
	t.Helper()

	ch := make(chan prometheus.Metric, 1)
	go func() {
		collector.Collect(ch)
		close(ch)
	}()

	count := 0

	for metric := range ch {
		dtoMetric := &dto.Metric{}
		if metric.Write(dtoMetric) != nil {
			continue
		}

		for _, label := range dtoMetric.GetLabel() {
			if label.GetName() == "path" && label.GetValue() == path {
				count++
				break
			}
		}
	}

	return count
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

// Не параллельный: публикует свою версию в общий AppInfo, а соседний тест
// проверяет, что после Reset остаётся ровно одна серия.
func TestMetricsEndpoint(t *testing.T) {
	SetBuildInfo("rs8kvn_bot@test", "0000000")

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
	assert.Contains(t, resp.Body.String(), "db_pool_waits_total")
	assert.Contains(t, resp.Body.String(), "subserver_not_found_total")

	body := resp.Body.String()
	for _, want := range []string{"app_info", "subserver_requests_total", "subscriptions_expired_total"} {
		assert.Contains(t, body, want)
	}

	// Нулевые серии публикуются заранее: иначе «событий не было» и «метрика
	// сломана» выглядят одинаково.
	assert.Contains(t, body, `subscription_sync_total{result="ok"} 0`)
	assert.Contains(t, body, `subscription_sync_total{result="degraded"} 0`)
	assert.Contains(t, body, `subserver_requests_total{format="clash"} 0`)
}

// Не параллельный: SetBuildInfo делает Reset() глобального AppInfo, поэтому
// любой соседний тест, публикующий свою версию, ломает подсчёт серий.
func TestSetBuildInfoPublishesSingleSeries(t *testing.T) {
	SetBuildInfo("rs8kvn_bot@v2.7.0", "9d04409")
	SetBuildInfo("rs8kvn_bot@v2.7.1", "3afc5bd")

	assert.Equal(t, 1.0, prometheus_testutil.ToFloat64(AppInfo.WithLabelValues("rs8kvn_bot@v2.7.1", "3afc5bd")))
	assert.Equal(t, 1, metricSamples(t, AppInfo), "после переинициализации должна остаться одна серия")
}
