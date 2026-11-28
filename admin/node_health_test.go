package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodesHealthCheck_AggregatesWithoutFailing(t *testing.T) {
	t.Parallel()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(healthy.Close)

	srv, _ := newTestServer(t)

	// Живая панель.
	healthyBody := fmt.Sprintf(`{"name":"alive","type":"3x-ui","host":%q,"subscription_url":"https://sub.example.com/s/","inbound_ids":[1],"api_token":"alive-token","is_active":true}`, healthy.URL)
	createNodeViaAPI(t, srv, healthyBody)

	// Панель отвечает 500 — недоступна, но прогон продолжается.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(dead.Close)

	deadBody := fmt.Sprintf(`{"name":"dead","type":"3x-ui","host":%q,"subscription_url":"https://sub.example.com/s/","inbound_ids":[1],"api_token":"dead-token","is_active":true}`, dead.URL)
	createNodeViaAPI(t, srv, deadBody)

	// fetch-нода: health-клиента нет — не вызывается и не роняет прогон.
	createNodeViaAPI(t, srv, `{"name":"fetcher","type":"fetch","host":"","subscription_url":"https://sub.example.com/s/","inbound_ids":[],"api_token":"","is_active":true}`)

	rec := do(t, srv, http.MethodPost, "/admin/api/nodes/health/check", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "alive-token")
	assert.NotContains(t, rec.Body.String(), "dead-token")

	var payload nodeHealthCheckResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	assert.Equal(t, 3, payload.Total)
	assert.Equal(t, 1, payload.Unavailable)
	assert.Equal(t, 1, payload.Unsupported)
	require.NotNil(t, payload.CheckedAt)

	byName := make(map[string]nodeHealthItem, len(payload.Items))
	for _, item := range payload.Items {
		byName[item.Name] = item
	}

	assert.True(t, byName["alive"].Available)
	assert.True(t, byName["alive"].Supported)
	assert.Empty(t, byName["alive"].Error)

	assert.False(t, byName["dead"].Available)
	assert.True(t, byName["dead"].Supported)
	assert.NotEmpty(t, byName["dead"].Error)

	assert.False(t, byName["fetcher"].Available)
	assert.False(t, byName["fetcher"].Supported)
	assert.NotEmpty(t, byName["fetcher"].Error)
}

func TestNodesHealthLast_EmptyThenStored(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/nodes/health", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var empty nodeHealthCheckResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &empty))
	assert.Nil(t, empty.CheckedAt)
	assert.Empty(t, empty.Items)
	assert.Contains(t, rec.Body.String(), `"items":[]`) // items — [], а не null

	createNodeViaAPI(t, srv, `{"name":"fetcher","type":"fetch","host":"","subscription_url":"https://sub.example.com/s/","inbound_ids":[],"api_token":"","is_active":true}`)

	rec = do(t, srv, http.MethodPost, "/admin/api/nodes/health/check", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = do(t, srv, http.MethodGet, "/admin/api/nodes/health", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var last nodeHealthCheckResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &last))
	require.NotNil(t, last.CheckedAt)
	assert.Equal(t, 1, last.Total)
	assert.Equal(t, 1, last.Unsupported)
}

func TestNodesHealthCheck_RequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodPost, "/admin/api/nodes/health/check", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = do(t, srv, http.MethodGet, "/admin/api/nodes/health", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// Текст ошибки — свободная строка HTTP-клиента: токен и credentials в URL не
// должны попасть в ответ.
func TestSanitizeNodeHealthError(t *testing.T) {
	t.Parallel()

	node := &database.Node{APIToken: "secret-token"}

	got := sanitizeNodeHealthError("dial http://admin:secret-token@203.0.113.5:2053: boom secret-token", node)
	assert.NotContains(t, got, "secret-token")
	assert.NotContains(t, got, "admin:")
	assert.Contains(t, got, "://***@")
}
