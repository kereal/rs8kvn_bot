package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeVPNClient implements vpn.Client for reset-traffic HTTP tests: the panel
// is never contacted, the node answers exactly as the test programs it.
type fakeVPNClient struct {
	resetError error
	resetCalls int
}

func (c *fakeVPNClient) CreateSubscription(context.Context, vpn.SubscriptionProvision) error {
	return nil
}
func (c *fakeVPNClient) UpdateSubscription(context.Context, vpn.SubscriptionProvision) error {
	return nil
}
func (c *fakeVPNClient) DeleteSubscription(context.Context, vpn.SubscriptionProvision) error {
	return nil
}
func (c *fakeVPNClient) Close() error { return nil }

func (c *fakeVPNClient) ResetTraffic(context.Context, vpn.SubscriptionProvision) error {
	c.resetCalls++
	return c.resetError
}

// newSubscriptionTestServer wires the admin server with real subscription and
// sync services over the test database, as cmd/bot does in production.
func newSubscriptionTestServer(t *testing.T) (*Server, *database.Service) {
	t.Helper()

	srv, db := newTestServer(t)

	subSvc := service.NewSubscriptionService(db, service.NodeClients{}, nil, &config.Config{})
	syncSvc := service.NewSyncService(db, service.NodeClients{}, nil)
	srv.SetSubscriptionServices(db, subSvc, syncSvc)

	return srv, db
}

// seedSubsForAPI creates a plan, a product and subscriptions with distinct
// statuses; bindings are created via subscription_nodes when withNodes.
func seedSubsForAPI(t *testing.T, db *database.Service) []*database.Subscription {
	t.Helper()
	ctx := context.Background()

	plan := &database.Plan{Name: "api-premium", IsActive: true, DevicesLimit: 3, TrafficLimit: 100 << 30}
	require.NoError(t, db.CreatePlan(ctx, plan))

	product := &database.Product{PlanID: plan.ID, Name: "30 дней", DurationDays: 30, PriceCents: 39900, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().Create(product).Error)

	in6h := time.Now().UTC().Add(6 * time.Hour)
	in30d := time.Now().UTC().Add(30 * 24 * time.Hour)

	currency := "RUB"
	subs := []*database.Subscription{
		{
			TelegramID: 111000111, Username: "kereal", ClientID: "c-111", SubscriptionID: "sub-111",
			Status: "active", PlanID: plan.ID, ProductID: &product.ID,
			PricePaidCents: 39900, Currency: &currency, ExpiresAt: &in6h,
		},
		{
			TelegramID: 222000222, ClientID: "c-222", SubscriptionID: "sub-222",
			Status: "active", PlanID: plan.ID, ExpiresAt: &in30d,
		},
		{
			TelegramID: 333000333, ClientID: "c-333", SubscriptionID: "sub-333",
			Status: string(database.SubscriptionStatusRevoked), PlanID: plan.ID,
		},
	}
	for i := range subs {
		require.NoError(t, db.GetDB().Create(subs[i]).Error)
	}

	require.NoError(t, db.GetDB().Model(&database.Subscription{}).
		Where("id = ?", subs[0].ID).Update("invite_code", "AB12").Error)

	return subs
}

func decodeSubscriptionsList(t *testing.T, body []byte) ([]map[string]any, int64) {
	t.Helper()

	var payload struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))

	return payload.Items, payload.Total
}

func TestAPI_SubscriptionsList(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	seedSubsForAPI(t, db)

	rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	items, total := decodeSubscriptionsList(t, rec.Body.Bytes())
	assert.Equal(t, int64(3), total)
	require.Len(t, items, 3)

	// Default order: id desc — последняя созданная (sub-333) первая.
	assert.Greater(t, items[0]["id"], items[1]["id"])
	assert.Equal(t, "sub-333", items[0]["subscription_id"])

	bySub := make(map[string]map[string]any, len(items))
	for i := range items {
		bySub[items[i]["subscription_id"].(string)] = items[i]
	}
	first := bySub["sub-111"]
	assert.Equal(t, float64(111000111), first["telegram_id"])
	assert.Equal(t, "kereal", first["username"])
	assert.Equal(t, "active", first["status"])
	assert.Equal(t, "api-premium", first["plan_name"])
	assert.NotNil(t, first["plan_id"])
	assert.NotNil(t, first["expires_at"])
	assert.Equal(t, float64(0), first["nodes_active"])
	assert.Equal(t, float64(0), first["nodes_total"])
	// Список не тянет тяжёлые поля.
	assert.NotContains(t, rec.Body.String(), "devices")
}

func TestAPI_SubscriptionsList_StatusAndExpiringFilters(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	seedSubsForAPI(t, db)

	rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions?status=active", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	items, total := decodeSubscriptionsList(t, rec.Body.Bytes())
	assert.Equal(t, int64(2), total)
	for i := range items {
		assert.Equal(t, "active", items[i]["status"])
	}

	rec = do(t, srv, http.MethodGet, "/admin/api/subscriptions?expiring_in=24", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	items, total = decodeSubscriptionsList(t, rec.Body.Bytes())
	assert.Equal(t, int64(1), total, "only the subscription expiring in 6h matches")
	assert.Equal(t, "sub-111", items[0]["subscription_id"])

	rec = do(t, srv, http.MethodGet, "/admin/api/subscriptions?q=222000222", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	_, total = decodeSubscriptionsList(t, rec.Body.Bytes())
	assert.Equal(t, int64(1), total, "telegram_id search is exact")

	rec = do(t, srv, http.MethodGet, "/admin/api/subscriptions?q=ere", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	_, total = decodeSubscriptionsList(t, rec.Body.Bytes())
	assert.Equal(t, int64(1), total, "username search is partial (kereal)")
}

func TestAPI_SubscriptionsList_BadRequests(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	seedSubsForAPI(t, db)

	cases := []struct {
		name string
		qs   string
	}{
		{"unknown status", "status=foo"},
		{"unknown sort", "sort=evil"},
		{"unknown order", "order=up"},
		{"negative offset", "offset=-1"},
		{"zero limit", "limit=0"},
		{"malformed plan_id", "plan_id=abc"},
		{"malformed expiring_in", "expiring_in=-4"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions?"+tc.qs, testToken, "")
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "bad_request")
		})
	}
}

func TestAPI_SubscriptionsList_ClampsLimit(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	seedSubsForAPI(t, db)

	rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions?limit=1000", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	// Страница clamped до 200, total остаётся реальным.
	items, total := decodeSubscriptionsList(t, rec.Body.Bytes())
	assert.Equal(t, int64(3), total)
	require.LessOrEqual(t, len(items), 200)
}

func TestAPI_SubscriptionsRequireToken(t *testing.T) {
	t.Parallel()

	srv, _ := newSubscriptionTestServer(t)

	// Методы должны совпадать с зарегистрированными маршрутами: Go 1.22 mux
	// отвечает 405 на незарегистрированную пару метод/путь до авторизации.
	probes := []struct{ method, path string }{
		{http.MethodGet, "/admin/api/subscriptions"},
		{http.MethodGet, "/admin/api/subscriptions/1"},
		{http.MethodPost, "/admin/api/subscriptions/1/plan"},
		{http.MethodPost, "/admin/api/subscriptions/1/revoke"},
		{http.MethodPost, "/admin/api/subscriptions/1/reset-traffic"},
		{http.MethodDelete, "/admin/api/subscriptions/1"},
	}

	for _, token := range []string{"", "wrong"} {
		for _, p := range probes {
			rec := do(t, srv, p.method, p.path, token, "")
			assert.Equal(t, http.StatusUnauthorized, rec.Code,
				"token=%q method=%s path=%s", token, p.method, p.path)
		}
	}
}

func TestAPI_SubscriptionsDetail(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	subs := seedSubsForAPI(t, db)

	node := &database.Node{Name: "nl-1", Host: "http://n1", Type: database.NodeType3xUI, IsActive: true, InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().Create(node).Error)
	require.NoError(t, db.CreateSubscriptionNode(context.Background(), &database.SubscriptionNode{
		SubscriptionID: subs[0].ID, NodeID: node.ID, Status: database.SyncStatusActive,
	}))

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/subscriptions/%d", subs[0].ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))

	assert.Equal(t, float64(subs[0].ID), detail["id"])
	assert.Equal(t, "AB12", detail["invite_code"])
	assert.Equal(t, float64(39900), detail["price_paid_cents"])
	assert.Equal(t, "RUB", detail["currency"])
	assert.Equal(t, float64(30), detail["duration_days"])
	assert.NotNil(t, detail["product_id"])
	assert.Equal(t, "/sub/sub-111", detail["sub_url"])

	nodes, ok := detail["nodes"].([]any)
	require.True(t, ok, "nodes must be an array")
	require.Len(t, nodes, 1)
	assert.Equal(t, "nl-1", nodes[0].(map[string]any)["node_name"])
	assert.Equal(t, "active", nodes[0].(map[string]any)["status"])

	// devices/ips отдаются валидным JSON (пустые массивы по умолчанию).
	assert.Equal(t, "[]", strings.TrimSpace(rawJSONField(t, rec.Body.Bytes(), "devices")))
}

func rawJSONField(t *testing.T, body []byte, field string) string {
	t.Helper()

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))
	return string(raw[field])
}

func TestAPI_SubscriptionsDetail_NotFound(t *testing.T) {
	t.Parallel()

	srv, _ := newSubscriptionTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions/9999", testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not_found")
}

func TestAPI_SubscriptionsSetPlan(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	subs := seedSubsForAPI(t, db)

	var plans []database.Plan
	require.NoError(t, db.GetDB().Where("name = ?", "api-premium").Find(&plans).Error)
	require.Len(t, plans, 1)
	planID := plans[0].ID

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/plan", subs[0].ID), testToken,
		fmt.Sprintf(`{"plan_id":%d,"days":14}`, planID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var sub map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sub))
	assert.Equal(t, "active", sub["status"])
	assert.NotNil(t, sub["expires_at"], "days=14 must set a future expiry")

	// Строка реально обновлена.
	var row database.Subscription
	require.NoError(t, db.GetDB().First(&row, subs[0].ID).Error)
	require.NotNil(t, row.ExpiresAt)
	assert.WithinDuration(t, time.Now().UTC().Add(14*24*time.Hour), *row.ExpiresAt, time.Minute)

	// Аудит plan_changed.
	var audits []database.AuditLog
	require.NoError(t, db.GetDB().Where("event = ?", database.AuditEventPlanChanged).Find(&audits).Error)
	require.Len(t, audits, 1)
	assert.Equal(t, database.ActorAdmin, audits[0].ActorType)
}

func TestAPI_SubscriptionsSetPlan_BadRequests(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	subs := seedSubsForAPI(t, db)

	cases := []struct {
		name    string
		subID   uint
		body    string
		code    int
		message string
	}{
		{"days not a number", subs[0].ID, `{"plan_id":1,"days":"abc"}`, http.StatusBadRequest, "bad_request"},
		{"negative days", subs[0].ID, `{"plan_id":1,"days":-1}`, http.StatusBadRequest, "validation"},
		{"days above maximum", subs[0].ID, `{"plan_id":1,"days":4000}`, http.StatusBadRequest, "validation"},
		{"unknown plan", subs[0].ID, `{"plan_id":9999,"days":30}`, http.StatusBadRequest, "validation"},
		{"unknown subscription", 9999, `{"plan_id":1,"days":30}`, http.StatusNotFound, "not_found"},
		{"missing plan_id", subs[0].ID, `{"days":30}`, http.StatusBadRequest, "bad_request"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/plan", tc.subID), testToken, tc.body)
			require.Equal(t, tc.code, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.message)
		})
	}
}

func TestAPI_SubscriptionsRevoke(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	subs := seedSubsForAPI(t, db)

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/revoke", subs[0].ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var sub map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sub))
	assert.Equal(t, "revoked", sub["status"])

	var row database.Subscription
	require.NoError(t, db.GetDB().First(&row, subs[0].ID).Error)
	assert.Equal(t, string(database.SubscriptionStatusRevoked), row.Status, "row must be revoked in the DB")

	var audits []database.AuditLog
	require.NoError(t, db.GetDB().Where("event = ?", database.AuditEventSubscriptionRevoked).Find(&audits).Error)
	require.Len(t, audits, 1)
	assert.Equal(t, database.AuditEventSubscriptionRevoked, audits[0].Event)
	assert.Equal(t, "1", *audits[0].EntityID, "entity_id is the numeric DB id")
}

func TestAPI_SubscriptionsRevoke_AlreadyRevokedConflict(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	subs := seedSubsForAPI(t, db)

	// Первый отзыв проходит, второй — 409 stale: панель должна отличать
	// «только что отозвал» от «уже было отозвано».
	first := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/revoke", subs[0].ID), testToken, "")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	second := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/revoke", subs[0].ID), testToken, "")
	require.Equal(t, http.StatusConflict, second.Code)
	assert.Contains(t, second.Body.String(), "stale")
	assert.Contains(t, second.Body.String(), "status")

	rec := do(t, srv, http.MethodPost, "/admin/api/subscriptions/9999/revoke", testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not_found")
}

func TestAPI_SubscriptionsDelete(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	subs := seedSubsForAPI(t, db)

	rec := do(t, srv, http.MethodDelete, fmt.Sprintf("/admin/api/subscriptions/%d", subs[0].ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"deleted":true`)

	var count int64
	require.NoError(t, db.GetDB().Model(&database.Subscription{}).Where("id = ?", subs[0].ID).Count(&count).Error)
	assert.Zero(t, count, "the row must be physically deleted")

	var audits []database.AuditLog
	require.NoError(t, db.GetDB().Where("event = ?", database.AuditEventSubscriptionDeleted).Find(&audits).Error)
	require.Len(t, audits, 1)

	rec = do(t, srv, http.MethodDelete, fmt.Sprintf("/admin/api/subscriptions/%d", subs[0].ID), testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not_found")
}

func TestAPI_SubscriptionsResetTraffic_NoActiveNodes(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	subs := seedSubsForAPI(t, db)

	// Ни одной привязки — сбрасывать нечего: 409 in_use, details.nodes=0.
	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/reset-traffic", subs[0].ID), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "in_use")

	rec = do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/reset-traffic", subs[2].ID), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "stale", "revoked subscription must be rejected")
}

func TestAPI_SubscriptionsResetTraffic_PartialFailure(t *testing.T) {
	t.Parallel()

	srv, db := newSubscriptionTestServer(t)
	subs := seedSubsForAPI(t, db)

	nodeOK := &database.Node{Name: "ok-node", Host: "http://ok", Type: database.NodeType3xUI, IsActive: true, InboundIDs: `[1]`}
	nodeBad := &database.Node{Name: "bad-node", Host: "http://bad", Type: database.NodeType3xUI, IsActive: true, InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().Create(nodeOK).Error)
	require.NoError(t, db.GetDB().Create(nodeBad).Error)

	require.NoError(t, db.CreateSubscriptionNode(context.Background(), &database.SubscriptionNode{
		SubscriptionID: subs[0].ID, NodeID: nodeOK.ID, Status: database.SyncStatusActive,
	}))
	require.NoError(t, db.CreateSubscriptionNode(context.Background(), &database.SubscriptionNode{
		SubscriptionID: subs[0].ID, NodeID: nodeBad.ID, Status: database.SyncStatusActive,
	}))

	// Подменяем реестр клиентов: один узел отвечает, второй падает.
	syncSvc := service.NewSyncService(db, service.NodeClients{
		nodeOK.ID:  {Provision: &fakeVPNClient{}},
		nodeBad.ID: {Provision: &fakeVPNClient{resetError: fmt.Errorf("panel unavailable")}},
	}, []database.Node{*nodeOK, *nodeBad})
	srv.SetSubscriptionServices(db, service.NewSubscriptionService(db, service.NodeClients{}, nil, &config.Config{}), syncSvc)

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/reset-traffic", subs[0].ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, float64(1), payload["reset_nodes"])
	assert.Equal(t, float64(1), payload["failed_nodes"])
	assert.Contains(t, payload, "warning", "partial failure must be visible to the admin")

	// Аудит traffic_reset записан с числом успешных и упавших узлов.
	var audits []database.AuditLog
	require.NoError(t, db.GetDB().Where("event = ?", database.AuditEventTrafficReset).Find(&audits).Error)
	require.Len(t, audits, 1)
	assert.Equal(t, database.AuditEventTrafficReset, audits[0].Event)
	assert.Contains(t, audits[0].Details, "nodes_ok")
	assert.Contains(t, audits[0].Details, "nodes_failed")
}
