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

func createProductViaAPI(t *testing.T, srv *Server, planID uint, name string) map[string]any {
	t.Helper()

	body := `{"plan_id":` + strconv.FormatUint(uint64(planID), 10) +
		`,"name":"` + name + `","duration_days":30,"price_cents":39900,"currency":"RUB","is_active":true}`

	rec := do(t, srv, http.MethodPost, "/admin/api/products", testToken, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	return created
}

func TestAPI_ProductsListReturnsTotal(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)
	require.NoError(t, db.GetDB().Create(&database.Product{
		PlanID: plan.ID, Name: "30 дней", DurationDays: 30, PriceCents: 39900, Currency: "RUB", IsActive: true,
	}).Error)

	rec := do(t, srv, http.MethodGet, "/admin/api/products", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Items, 1)
	assert.Equal(t, 1, payload.Total)
	assert.Equal(t, "premium", payload.Items[0]["plan_name"])
	assert.Equal(t, float64(39900), payload.Items[0]["price_cents"])
	_, hasSubs := payload.Items[0]["subscriptions_count"]
	assert.True(t, hasSubs, "subscriptions_count must be present")
}

func TestAPI_ProductsCreateReturns201(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)

	created := createProductViaAPI(t, srv, plan.ID, "30 дней")
	assert.Equal(t, "30 дней", created["name"])
	assert.Equal(t, float64(30), created["duration_days"])
}

// XTR: minor units are whole Stars, so the API must echo price_cents unscaled.
func TestAPI_ProductsStarsPriceIsNotScaled(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)

	body := `{"plan_id":` + strconv.FormatUint(uint64(plan.ID), 10) +
		`,"name":"Stars 30","duration_days":30,"price_cents":250,"currency":"XTR","is_active":true}`

	rec := do(t, srv, http.MethodPost, "/admin/api/products", testToken, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, float64(250), created["price_cents"])
	assert.Equal(t, "XTR", created["currency"])

	id := strconv.FormatUint(uint64(created["id"].(float64)), 10)
	rec = do(t, srv, http.MethodPut, "/admin/api/products/"+id, testToken, body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var updated map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	assert.Equal(t, float64(250), updated["price_cents"])
	assert.Equal(t, "XTR", updated["currency"])
}

func TestAPI_ProductsCreateRejectsZeroDuration(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)

	rec := do(t, srv, http.MethodPost, "/admin/api/products", testToken,
		`{"plan_id":`+strconv.FormatUint(uint64(plan.ID), 10)+`,"duration_days":0,"price_cents":100,"is_active":true}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"validation"`)
	assert.Contains(t, rec.Body.String(), `"duration_days"`)
}

func TestAPI_ProductsRequireToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := do(t, srv, method, "/admin/api/products", "", "{}")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, method)
	}

	rec := do(t, srv, http.MethodDelete, "/admin/api/products/1", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAPI_ProductsUpdateUnknownIDReturns404(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)

	rec := do(t, srv, http.MethodPut, "/admin/api/products/999", testToken,
		`{"plan_id":`+strconv.FormatUint(uint64(plan.ID), 10)+`,"duration_days":30,"price_cents":100,"is_active":true}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = do(t, srv, http.MethodDelete, "/admin/api/products/999", testToken, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAPI_ProductsCreateDuplicatePairReturns409(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)

	createProductViaAPI(t, srv, plan.ID, "30 дней")

	rec := do(t, srv, http.MethodPost, "/admin/api/products", testToken,
		`{"plan_id":`+strconv.FormatUint(uint64(plan.ID), 10)+`,"name":"дубль","duration_days":30,"price_cents":100,"is_active":true}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name_taken"`)
	assert.Contains(t, rec.Body.String(), `"duration_days"`)
}

func TestAPI_ProductsDeleteWithDependenciesReturns409(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)

	product := &database.Product{PlanID: plan.ID, Name: "30 дней", DurationDays: 30, PriceCents: 39900, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().Create(product).Error)

	sub := &database.Subscription{
		TelegramID: 1, ClientID: "client-1", SubscriptionID: "sub-1",
		Status: string(database.SubscriptionStatusActive), PlanID: plan.ID, ProductID: &product.ID,
	}
	require.NoError(t, db.GetDB().Create(sub).Error)

	rec := do(t, srv, http.MethodDelete, "/admin/api/products/"+strconv.FormatUint(uint64(product.ID), 10), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"in_use"`)
	assert.Contains(t, rec.Body.String(), `"subscriptions":1`)
}

func TestAPI_ProductsRejectUnknownField(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)

	rec := do(t, srv, http.MethodPost, "/admin/api/products", testToken,
		`{"plan_id":`+strconv.FormatUint(uint64(plan.ID), 10)+`,"duration_days":30,"price_cents":100,"is_active":true,"hack":1}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPI_ProductsFilters(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	require.NoError(t, db.GetDB().Create(plan).Error)
	require.NoError(t, db.GetDB().Create(&database.Product{
		PlanID: plan.ID, Name: "30 дней", DurationDays: 30, PriceCents: 39900, Currency: "RUB", IsActive: true,
	}).Error)
	require.NoError(t, db.GetDB().Create(&database.Product{
		PlanID: plan.ID, Name: "скрытый", DurationDays: 90, PriceCents: 79900, Currency: "RUB", IsActive: false,
	}).Error)
	require.NoError(t, db.GetDB().Model(&database.Product{}).Where("name = ?", "скрытый").Update("is_active", false).Error)

	rec := do(t, srv, http.MethodGet, "/admin/api/products?active=false", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Items, 1)
	assert.Equal(t, "скрытый", payload.Items[0]["name"])
}

func TestAPI_ProductsPlanChangeIsProtected(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	plan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2}
	other := &database.Plan{Name: "starter", IsActive: true, DevicesLimit: 1}
	require.NoError(t, db.GetDB().Create(plan).Error)
	require.NoError(t, db.GetDB().Create(other).Error)

	created := createProductViaAPI(t, srv, plan.ID, "30 дней")
	id := strconv.Itoa(int(created["id"].(float64)))

	rec := do(t, srv, http.MethodPut, "/admin/api/products/"+id, testToken,
		`{"plan_id":`+strconv.FormatUint(uint64(other.ID), 10)+`,"duration_days":30,"price_cents":100,"is_active":true}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"protected"`)
	assert.Contains(t, rec.Body.String(), `plan_id_immutable`)
}
