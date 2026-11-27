package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedOrders creates plan -> product -> subscription -> n paid orders with
// distinct created_at and amount_cents. Returns the subscription telegram id
// and the first order id.
func seedOrders(t *testing.T, db *database.Service, n int, createdAt time.Time) (int64, uint) {
	t.Helper()

	plan := &database.Plan{Name: "paid-30d", IsActive: true, DevicesLimit: 2, TrafficLimit: 100 << 30}
	require.NoError(t, db.CreatePlan(context.Background(), plan))

	product := &database.Product{PlanID: plan.ID, Name: "30 дней", DurationDays: 30, PriceCents: 39900, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().Create(product).Error)

	sub := &database.Subscription{TelegramID: 111000111, ClientID: "c-111000111", SubscriptionID: "s-111000111", Status: "active", PlanID: plan.ID}
	require.NoError(t, db.GetDB().Create(sub).Error)

	firstID := uint(0)
	for i := range n {
		order := &database.Order{
			SubscriptionID:  sub.ID,
			ProductID:       product.ID,
			Status:          database.OrderStatusPaid,
			AmountCents:     int64(100+i) * 100,
			Currency:        "RUB",
			PaymentProvider: "platega",
			CreatedAt:       createdAt.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, db.GetDB().Create(order).Error)
		if i == 0 {
			firstID = order.ID
		}
	}

	return sub.TelegramID, firstID
}

func decodeOrdersList(t *testing.T, body []byte) ([]map[string]any, int64) {
	t.Helper()

	var payload struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))

	return payload.Items, payload.Total
}

func TestAPI_OrdersList(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	_, _ = seedOrders(t, db, 3, time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))

	rec := do(t, srv, http.MethodGet, "/admin/api/orders", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	items, total := decodeOrdersList(t, rec.Body.Bytes())
	assert.Equal(t, int64(3), total)
	require.Len(t, items, 3)

	// Default sort created_at desc: newest first.
	assert.Equal(t, float64(3), items[0]["id"])
	assert.Equal(t, float64(111000111), items[0]["telegram_id"])
	assert.Equal(t, "paid", items[0]["status"])
	assert.Equal(t, "paid-30d", items[0]["plan_name"])
	assert.Equal(t, "30 дней", items[0]["product_name"])
	assert.Equal(t, float64(30), items[0]["duration_days"])
	// callback_amount_cents не записан — null, а не ноль.
	assert.Nil(t, items[0]["callback_amount_cents"])
	assert.Nil(t, items[0]["provider_fee_cents"])
}

func TestAPI_OrdersDetail(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	_, firstID := seedOrders(t, db, 1, time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/orders/%d", firstID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))

	assert.Equal(t, float64(firstID), detail["id"])
	assert.Equal(t, float64(1), detail["product_id"])
	assert.Equal(t, "platega", detail["payment_provider"])
	// payment_url отдаётся только в детали.
	assert.Contains(t, detail, "payment_url")
	assert.Contains(t, detail, "payment_expires_at")
	assert.Contains(t, detail, "expires_at")
}

func TestAPI_OrdersDetail_NotFound(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/orders/9999", testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not_found")
}

func TestAPI_OrdersList_BadRequests(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedOrders(t, db, 1, time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))

	cases := []struct {
		name string
		qs   string
	}{
		{"unknown status", "status=foo"},
		{"unknown sort", "sort=evil"},
		{"unknown order", "order=up"},
		{"negative offset", "offset=-1"},
		{"malformed from", "from=2026-13-99"},
		{"malformed to", "to=01.11.2026"},
		{"non-numeric id", "id=abc"},
		{"zero id", "id=0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, srv, http.MethodGet, "/admin/api/orders?"+tc.qs, testToken, "")
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "bad_request")
		})
	}
}

func TestAPI_OrdersList_IDFilter(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	_, firstID := seedOrders(t, db, 3, time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/orders?id=%d", firstID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	items, total := decodeOrdersList(t, rec.Body.Bytes())
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, float64(firstID), items[0]["id"])

	// Несуществующий id — валидный фильтр, пустая страница, не 404.
	rec = do(t, srv, http.MethodGet, "/admin/api/orders?id=999999", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	items, total = decodeOrdersList(t, rec.Body.Bytes())
	assert.Equal(t, int64(0), total)
	assert.Empty(t, items)
}

func TestAPI_OrdersList_RequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for _, token := range []string{"", "wrong"} {
		rec := do(t, srv, http.MethodGet, "/admin/api/orders", token, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "token=%q", token)
		rec = do(t, srv, http.MethodGet, "/admin/api/orders/1", token, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "token=%q", token)
	}
}

func TestAPI_OrdersList_ClampsLimit(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedOrders(t, db, 205, time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))

	rec := do(t, srv, http.MethodGet, "/admin/api/orders?limit=1000", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	items, total := decodeOrdersList(t, rec.Body.Bytes())
	// Страница clamped до 200, но total остаётся реальным, а не clamped.
	require.Len(t, items, 200)
	assert.Equal(t, int64(205), total)
}
