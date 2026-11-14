package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trialsHandler повторяет боевую разводку маршрутов из server.go: сам server.go
// в зоне интегратора, поэтому тесты регистрируют их локально.
func trialsHandler(srv *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/api/trials", srv.withAuth(srv.handleListTrials))
	mux.HandleFunc("GET /admin/api/trials/stats", srv.withAuth(srv.handleTrialStats))

	return mux
}

// doTrials — двойник общего do(t, srv, ...) для локально разведённого mux'а.
func doTrials(t *testing.T, srv *Server, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	trialsHandler(srv).ServeHTTP(rec, req)

	return rec
}

func TestAPI_TrialsList(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := context.Background()

	// Три запроса с двух IP.
	require.NoError(t, db.CreateTrialRequest(ctx, "10.0.0.1"))
	require.NoError(t, db.CreateTrialRequest(ctx, "10.0.0.2"))
	require.NoError(t, db.CreateTrialRequest(ctx, "10.0.0.2"))

	rec := doTrials(t, srv, http.MethodGet, "/admin/api/trials", testToken)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(3), body.Total)
	require.Len(t, body.Items, 3)

	// Форма строки: id, ip, created_at.
	for _, item := range body.Items {
		assert.Contains(t, item, "id")
		assert.Contains(t, item, "ip")
		assert.Contains(t, item, "created_at")
	}

	// IP echo'ится как есть.
	assert.Equal(t, "10.0.0.2", body.Items[0]["ip"])
}

func TestAPI_TrialsListPagination(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := context.Background()

	for range 3 {
		require.NoError(t, db.CreateTrialRequest(ctx, "10.0.0.1"))
	}

	rec := doTrials(t, srv, http.MethodGet, "/admin/api/trials?limit=2&offset=1", testToken)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(3), body.Total)
	require.Len(t, body.Items, 2)
}

func TestAPI_TrialsListRejectsBadParams(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := doTrials(t, srv, http.MethodGet, "/admin/api/trials?limit=0", testToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = doTrials(t, srv, http.MethodGet, "/admin/api/trials?offset=-1", testToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = doTrials(t, srv, http.MethodGet, "/admin/api/trials?limit=abc", testToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPI_TrialsStats(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := context.Background()

	// Фикстуры: один IP с пятью запросами (лидер), два других по разу.
	for range 5 {
		require.NoError(t, db.CreateTrialRequest(ctx, "10.9.9.5"))
	}
	require.NoError(t, db.CreateTrialRequest(ctx, "10.9.9.1"))
	require.NoError(t, db.CreateTrialRequest(ctx, "10.9.9.3"))

	rec := doTrials(t, srv, http.MethodGet, "/admin/api/trials/stats", testToken)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Requests24h  int64            `json:"requests_24h"`
		UniqueIPs24h int64            `json:"unique_ips_24h"`
		TopIPs       []map[string]any `json:"top_ips"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(7), body.Requests24h)
	assert.Equal(t, int64(3), body.UniqueIPs24h)
	require.Len(t, body.TopIPs, 3)

	// Топ упорядочен по убыванию числа запросов.
	assert.Equal(t, "10.9.9.5", body.TopIPs[0]["ip"])
	assert.Equal(t, float64(5), body.TopIPs[0]["count"])
	assert.Equal(t, "10.9.9.1", body.TopIPs[1]["ip"])
}

func TestAPI_TrialsStatsEmpty(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := doTrials(t, srv, http.MethodGet, "/admin/api/trials/stats", testToken)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Requests24h  int64            `json:"requests_24h"`
		UniqueIPs24h int64            `json:"unique_ips_24h"`
		TopIPs       []map[string]any `json:"top_ips"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(0), body.Requests24h)
	assert.Equal(t, int64(0), body.UniqueIPs24h)
	assert.Empty(t, body.TopIPs)
}

func TestAPI_TrialsRequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for _, path := range []string{"/admin/api/trials", "/admin/api/trials/stats"} {
		for _, token := range []string{"", "wrong"} {
			resetAuthLimiter(srv)

			rec := doTrials(t, srv, http.MethodGet, path, token)
			assert.Equal(t, http.StatusUnauthorized, rec.Code, path+" token="+token)
		}
	}
}
