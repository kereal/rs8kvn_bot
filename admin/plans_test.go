package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createPlanViaAPI posts a plan payload and returns the decoded 201 body. The
// plans screen predates the spec set (doc/specs/admin/00-common.md), so these
// tests are its contract: catalogue CRUD against the seeded free/trial plans.
func createPlanViaAPI(t *testing.T, srv *Server, name string, devices int, traffic int64, active bool) map[string]any {
	t.Helper()

	body := `{"name":"` + name + `","devices_limit":` + strconv.Itoa(devices) +
		`,"traffic_limit":` + strconv.FormatInt(traffic, 10) +
		`,"is_active":` + strconv.FormatBool(active) + `}`

	rec := do(t, srv, http.MethodPost, "/admin/api/plans", testToken, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	return created
}

func listPlansViaAPI(t *testing.T, srv *Server) []map[string]any {
	t.Helper()

	rec := do(t, srv, http.MethodGet, "/admin/api/plans", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	return payload.Items
}

func planByName(t *testing.T, items []map[string]any, name string) map[string]any {
	t.Helper()

	for _, item := range items {
		if item["name"] == name {
			return item
		}
	}

	require.FailNow(t, "plan not found in list", "name=%s items=%v", name, items)

	return nil
}

func TestAPI_PlansListReturnsUsageCounts(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	created := createPlanViaAPI(t, srv, "premium", 3, 1<<30, true)
	planID := uint(created["id"].(float64))

	require.NoError(t, db.GetDB().Create(&database.Product{
		PlanID: planID, Name: "30 дней", DurationDays: 30, PriceCents: 10000, Currency: "RUB", IsActive: true,
	}).Error)
	require.NoError(t, db.GetDB().Create(&database.Subscription{
		TelegramID: 700001, ClientID: "c-700001", SubscriptionID: "s-700001",
		Status: string(database.SubscriptionStatusActive), PlanID: planID,
	}).Error)

	items := listPlansViaAPI(t, srv)
	require.GreaterOrEqual(t, len(items), 3, "seeded free and trial plans plus the created one")

	item := planByName(t, items, "premium")
	assert.Equal(t, float64(3), item["devices_limit"])
	assert.Equal(t, float64(1<<30), item["traffic_limit"])
	assert.Equal(t, true, item["is_active"])
	assert.Equal(t, float64(1), item["products_count"], "usage counts drive the delete guard dialog")
	assert.Equal(t, float64(1), item["subscriptions_count"])

	// Ordering is by id: the SPA renders the list as-is, so a changed order
	// would silently reshuffle the table.
	for i := 1; i < len(items); i++ {
		assert.Less(t, items[i-1]["id"].(float64), items[i]["id"].(float64))
	}
}

func TestAPI_PlanCreateUpdateRoundTrip(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	created := createPlanViaAPI(t, srv, "roundtrip", 1, 1<<30, true)
	id := created["id"].(float64)

	rec := do(t, srv, http.MethodPut, "/admin/api/plans/"+strconv.FormatUint(uint64(id), 10), testToken,
		`{"name":"roundtrip-v2","devices_limit":7,"traffic_limit":`+strconv.FormatInt(9<<30, 10)+`,"is_active":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"ok":true}`, rec.Body.String())

	item := planByName(t, listPlansViaAPI(t, srv), "roundtrip-v2")
	assert.Equal(t, float64(7), item["devices_limit"])
	assert.Equal(t, float64(9<<30), item["traffic_limit"])
	assert.Equal(t, false, item["is_active"])

	rec = do(t, srv, http.MethodPut, "/admin/api/plans/99999", testToken,
		`{"name":"ghost","devices_limit":1,"traffic_limit":0,"is_active":true}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `"not_found"`)
}

func TestAPI_PlanValidationFailuresAreLoud(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	createPlanViaAPI(t, srv, "taken-name", 1, 0, true)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{
			name:   "empty name",
			method: http.MethodPost,
			path:   "/admin/api/plans",
			body:   `{"name":"   ","devices_limit":1,"traffic_limit":0,"is_active":true}`,
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "negative devices limit",
			method: http.MethodPost,
			path:   "/admin/api/plans",
			body:   `{"name":"negative","devices_limit":-1,"traffic_limit":0,"is_active":true}`,
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "duplicate name",
			method: http.MethodPost,
			path:   "/admin/api/plans",
			body:   `{"name":"taken-name","devices_limit":1,"traffic_limit":0,"is_active":true}`,
			status: http.StatusConflict,
			code:   "name_taken",
		},
		{
			// A typo in the SPA payload must fail instead of silently keeping
			// the previous value.
			name:   "unknown field",
			method: http.MethodPost,
			path:   "/admin/api/plans",
			body:   `{"name":"typo","device_limit":1,"traffic_limit":0,"is_active":true}`,
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "malformed json",
			method: http.MethodPost,
			path:   "/admin/api/plans",
			body:   `{"name":"x"`,
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "non-numeric id",
			method: http.MethodPut,
			path:   "/admin/api/plans/abc",
			body:   `{"name":"x","devices_limit":1,"traffic_limit":0,"is_active":true}`,
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "wrong method on collection",
			method: http.MethodDelete,
			path:   "/admin/api/plans",
			body:   "",
			status: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, srv, tc.method, tc.path, testToken, tc.body)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())

			if tc.code != "" {
				assert.Contains(t, rec.Body.String(), `"`+tc.code+`"`)
			}
		})
	}
}

func TestAPI_PlanDeleteGuardsAndSuccess(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	guarded := createPlanViaAPI(t, srv, "guarded", 1, 0, true)
	guardedID := uint(guarded["id"].(float64))
	require.NoError(t, db.GetDB().Create(&database.Product{
		PlanID: guardedID, Name: "p", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true,
	}).Error)

	rec := do(t, srv, http.MethodDelete, "/admin/api/plans/"+strconv.FormatUint(uint64(guardedID), 10), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"in_use"`)
	assert.Contains(t, rec.Body.String(), "1 products", "the SPA shows the dependency count to the admin")

	items := listPlansViaAPI(t, srv)
	trial := planByName(t, items, database.TrialPlanName)

	rec = do(t, srv, http.MethodDelete,
		"/admin/api/plans/"+strconv.FormatUint(uint64(trial["id"].(float64)), 10), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"protected"`)

	doomed := createPlanViaAPI(t, srv, "doomed", 1, 0, true)
	doomedID := uint(doomed["id"].(float64))

	rec = do(t, srv, http.MethodDelete, "/admin/api/plans/"+strconv.FormatUint(uint64(doomedID), 10), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"ok":true}`, rec.Body.String())

	for _, item := range listPlansViaAPI(t, srv) {
		assert.NotEqual(t, float64(doomedID), item["id"], "deleted plan must leave the list")
	}

	rec = do(t, srv, http.MethodDelete, "/admin/api/plans/"+strconv.FormatUint(uint64(doomedID), 10), testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `"not_found"`)
}
