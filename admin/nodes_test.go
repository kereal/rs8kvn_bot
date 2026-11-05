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

const nodePayloadBody = `{
	"name": "main-panel",
	"type": "3x-ui",
	"host": "http://203.0.113.10:2053",
	"subscription_url": "https://sub.example.com/s/",
	"inbound_ids": [1, 3],
	"api_token": "secret-token",
	"is_active": true
}`

func createNodeViaAPI(t *testing.T, srv *Server, body string) (int, map[string]any) {
	t.Helper()

	rec := do(t, srv, http.MethodPost, "/admin/api/nodes", testToken, body)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	id, ok := created["id"].(float64)
	require.True(t, ok, "id must be numeric: %v", created)

	return int(id), created
}

func TestNodesAPI_List(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	_, created := createNodeViaAPI(t, srv, nodePayloadBody)
	assert.Equal(t, "main-panel", created["name"])
	assert.Equal(t, true, created["is_active"])
	assert.Equal(t, true, created["has_api_token"])
	assert.NotContains(t, created, "api_token", "api_token write-only")

	rec := do(t, srv, http.MethodGet, "/admin/api/nodes", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Equal(t, 1, list.Total)
	require.Len(t, list.Items, 1)

	item := list.Items[0]
	assert.Equal(t, "main-panel", item["name"])
	assert.Equal(t, true, item["has_api_token"])
	assert.Equal(t, float64(0), item["plans_count"])
	assert.Equal(t, float64(0), item["subscriptions_count"])

	// Токен не должен встречаться нигде в JSON-ответе.
	assert.NotContains(t, rec.Body.String(), `"api_token"`)
	assert.NotContains(t, rec.Body.String(), "secret-token")
}

func TestNodesAPI_Create(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodPost, "/admin/api/nodes", testToken, nodePayloadBody)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.NotZero(t, created["id"])
	assert.Equal(t, "3x-ui", created["type"])
	assert.Equal(t, true, created["has_api_token"])
}

func TestNodesAPI_Create_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantCode   string
		wantDetail string
	}{
		{
			name:     "unknown field",
			body:     `{"name":"x","type":"fetch","subscription_url":"https://sub.example.com/s/","hack":1}`,
			wantCode: "bad_request",
		},
		{
			name:       "unknown node type",
			body:       `{"name":"x","type":"marzban","subscription_url":"https://sub.example.com/s/"}`,
			wantCode:   "validation",
			wantDetail: "type",
		},
		{
			name:       "empty name",
			body:       `{"name":"","type":"fetch","subscription_url":"https://sub.example.com/s/"}`,
			wantCode:   "validation",
			wantDetail: "name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newTestServer(t)

			rec := do(t, srv, http.MethodPost, "/admin/api/nodes", testToken, tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			var resp struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, tt.wantCode, resp.Code)

			if tt.wantDetail != "" {
				assert.Contains(t, resp.Details, tt.wantDetail)
			}
		})
	}
}

func TestNodesAPI_RequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	// Методы, зарегистрированные для каждого маршрута.
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/api/nodes"},
		{http.MethodPost, "/admin/api/nodes"},
		{http.MethodGet, "/admin/api/plans/1/nodes"},
		{http.MethodPut, "/admin/api/plans/1/nodes"},
		{http.MethodDelete, "/admin/api/nodes/1"},
		{http.MethodPost, "/admin/api/nodes/1/health"},
	}

	for _, tt := range cases {
		resetAuthLimiter(srv)

		rec := do(t, srv, tt.method, tt.path, "", "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", tt.method, tt.path)

		rec = do(t, srv, tt.method, tt.path, "wrong-token", "{}")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", tt.method, tt.path)
	}
}

func TestNodesAPI_Update(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	id, _ := createNodeViaAPI(t, srv, nodePayloadBody)

	// 404 на чужой id.
	rec := do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/nodes/%d", 424242), testToken, nodePayloadBody)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// 409 на смену type.
	typeChange := `{"name":"main-panel","type":"fetch","host":"","subscription_url":"https://sub.example.com/s/","inbound_ids":[],"api_token":"","is_active":true}`
	rec = do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/nodes/%d", id), testToken, typeChange)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	var conflict struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	assert.Equal(t, "protected", conflict.Code)

	// Пустой api_token = «не менять».
	update := `{"name":"renamed","type":"3x-ui","host":"http://203.0.113.20:2053","subscription_url":"https://sub.example.com/s/","inbound_ids":[2],"api_token":"","is_active":false}`
	rec = do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/nodes/%d", id), testToken, update)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var updated map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	assert.Equal(t, "renamed", updated["name"])
	assert.Equal(t, false, updated["is_active"])
	assert.Equal(t, true, updated["has_api_token"], "пустой api_token не затирает токен")

	stored, err := db.GetNodeByID(t.Context(), uint(id))
	require.NoError(t, err)
	assert.Equal(t, "secret-token", stored.APIToken)

	// Новый токен заменяется, но в ответ не попадает.
	retoken := `{"name":"renamed","type":"3x-ui","host":"http://203.0.113.20:2053","subscription_url":"https://sub.example.com/s/","inbound_ids":[2],"api_token":"fresh-secret","is_active":false}` // #nosec G101 -- test fixture, not a credential
	rec = do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/nodes/%d", id), testToken, retoken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "fresh-secret")

	stored, err = db.GetNodeByID(t.Context(), uint(id))
	require.NoError(t, err)
	assert.Equal(t, "fresh-secret", stored.APIToken)
}

func TestNodesAPI_Delete(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	id, _ := createNodeViaAPI(t, srv, nodePayloadBody)

	// 404 на чужой id.
	rec := do(t, srv, http.MethodDelete, "/admin/api/nodes/424242", testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Привязка к плану блокирует удаление: 409 in_use с счётчиками.
	plan := &database.Plan{Name: "bound-plan", IsActive: true, DevicesLimit: 1}
	require.NoError(t, db.CreatePlan(t.Context(), plan))
	_, err := db.ReplacePlanNodes(t.Context(), plan.ID, []uint{uint(id)})
	require.NoError(t, err)

	rec = do(t, srv, http.MethodDelete, fmt.Sprintf("/admin/api/nodes/%d", id), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	var conflict struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	assert.Equal(t, "in_use", conflict.Code)
	assert.Equal(t, float64(1), conflict.Details["plans"])
	assert.Equal(t, float64(0), conflict.Details["subscriptions"])

	// Убираем привязку — удаление проходит и возвращает удалённый DTO.
	_, err = db.ReplacePlanNodes(t.Context(), plan.ID, nil)
	require.NoError(t, err)

	rec = do(t, srv, http.MethodDelete, fmt.Sprintf("/admin/api/nodes/%d", id), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var deleted map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &deleted))
	assert.Equal(t, "main-panel", deleted["name"])
	assert.Equal(t, true, deleted["has_api_token"])
	assert.Equal(t, float64(0), deleted["plans_count"])
	assert.Equal(t, float64(0), deleted["subscriptions_count"])
	assert.NotContains(t, rec.Body.String(), "secret-token")
}

func TestNodesAPI_PlanNodesBinding(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	node1, _ := createNodeViaAPI(t, srv, nodePayloadBody)
	node2Body := `{"name":"second","type":"fetch","host":"","subscription_url":"https://sub.example.com/s/","inbound_ids":[],"api_token":"","is_active":true}`
	node2, _ := createNodeViaAPI(t, srv, node2Body)

	plan := &database.Plan{Name: "link-plan", IsActive: true, DevicesLimit: 1}
	require.NoError(t, db.CreatePlan(t.Context(), plan))

	// GET до привязок: пустой массив.
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/plans/%d/nodes", plan.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var got struct {
		NodeIDs []uint `json:"node_ids"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.NodeIDs)

	// PUT заменяет набор.
	put := fmt.Sprintf(`{"node_ids":[%d,%d]}`, node1, node2)
	rec = do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/plans/%d/nodes", plan.ID), testToken, put)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got.NodeIDs, 2)

	// Идемпотентный повтор того же набора.
	rec = do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/plans/%d/nodes", plan.ID), testToken, put)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got.NodeIDs, 2)

	// GET возвращает текущий набор.
	rec = do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/plans/%d/nodes", plan.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got.NodeIDs, 2)

	// Несуществующая нода — 400 validation с details.node_ids.
	putBad := `{"node_ids":[424242]}`
	rec = do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/plans/%d/nodes", plan.ID), testToken, putBad)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	var bad struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bad))
	assert.Equal(t, "validation", bad.Code)
	assert.Contains(t, bad.Details, "node_ids")

	// Несуществующий план — 404.
	rec = do(t, srv, http.MethodPut, "/admin/api/plans/424242/nodes", testToken, put)
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = do(t, srv, http.MethodGet, "/admin/api/plans/424242/nodes", testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestNodesAPI_Health(t *testing.T) {
	t.Parallel()

	// Реальный Ping против httptest.Server, отдающего 500: результат
	// диагностики — данные (200 + available:false), а не ошибка протокола.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)

	srv, _ := newTestServer(t)

	body := fmt.Sprintf(`{
		"name": "dead-panel",
		"type": "3x-ui",
		"host": %q,
		"subscription_url": "https://sub.example.com/s/",
		"inbound_ids": [1],
		"api_token": "secret-token",
		"is_active": true
	}`, failing.URL)

	id, _ := createNodeViaAPI(t, srv, body)

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/nodes/%d/health", id), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result struct {
		Available bool   `json:"available"`
		LatencyMS int64  `json:"latency_ms"`
		Error     string `json:"error"`
		CheckedAt string `json:"checked_at"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.False(t, result.Available)
	assert.NotEmpty(t, result.Error)
	assert.NotEmpty(t, result.CheckedAt)
	assert.NotContains(t, rec.Body.String(), "secret-token")

	// 404 на чужой id.
	rec = do(t, srv, http.MethodPost, "/admin/api/nodes/424242/health", testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	// fetch-нода — 400 validation details.type.
	fetchBody := `{"name":"fetcher","type":"fetch","host":"","subscription_url":"https://sub.example.com/s/","inbound_ids":[],"api_token":"","is_active":true}`
	fetchID, _ := createNodeViaAPI(t, srv, fetchBody)

	rec = do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/nodes/%d/health", fetchID), testToken, "")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	var validation struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &validation))
	assert.Equal(t, "validation", validation.Code)
	assert.Contains(t, validation.Details, "type")

	// 3x-ui без токена — 400 validation details.api_token.
	tokenlessBody := `{"name":"no-token","type":"3x-ui","host":"http://203.0.113.30:2053","subscription_url":"https://sub.example.com/s/","inbound_ids":[],"api_token":"","is_active":true}` // #nosec G101 -- test fixture, not a credential
	tokenlessID, _ := createNodeViaAPI(t, srv, tokenlessBody)

	rec = do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/nodes/%d/health", tokenlessID), testToken, "")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	validation = struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &validation))
	assert.Equal(t, "validation", validation.Code)
	assert.Equal(t, "not set", validation.Details["api_token"])
}
