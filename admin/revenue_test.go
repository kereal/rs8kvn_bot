package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// revenueRequest обслуживает GET /admin/api/revenue через локальный mux с тем
// же withAuth, что и в server.go: роут регистрирует интегратор, а тест обязан
// проверять хендлер и его guard'ы без правки server.go.
func revenueRequest(t *testing.T, srv *Server, token, query string) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/api/revenue", srv.withAuth(srv.handleRevenue))

	req := httptest.NewRequest(http.MethodGet, "/admin/api/revenue"+query, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	return rec
}

// revenueSeedTelegramID выдаёт уникальный telegram_id для каждого сида:
// subscriptions.telegram_id — uniqueIndex, а хелпер может вызываться
// несколько раз в одном тесте (смешанные валюты).
var revenueSeedTelegramID atomic.Int64

// seedRevenueOrder создаёт план -> продукт -> подписку -> один оплаченный
// заказ сегодня в заданной валюте: минимальный сет для проверки формы ответа
// выручки.
func seedRevenueOrder(t *testing.T, db *database.Service, currency string, amountCents int64) {
	t.Helper()
	ctx := context.Background()

	plan := &database.Plan{Name: "rev-plan-" + currency, DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().Create(plan).Error)

	product := &database.Product{PlanID: plan.ID, Name: "rev-product-" + currency, DurationDays: 30, PriceCents: amountCents, Currency: currency}
	require.NoError(t, db.GetDB().Create(product).Error)

	sub := &database.Subscription{
		TelegramID: revenueSeedTelegramID.Add(1), ClientID: "rev-c1-" + currency, SubscriptionID: "rev-sub-1-" + currency,
		Status: "active", PlanID: plan.ID,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	// Оплата фиксируется текущим моментом: окно выручки покрывает
	// последние дни, поэтому заказ остаётся в окне даже если между
	// созданием заказа и запросом сменились сутки (00:00 UTC).
	paidAt := time.Now().UTC()
	require.NoError(t, db.GetDB().Create(&database.Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: database.OrderStatusPaid,
		AmountCents: amountCents, Currency: currency, PaidAt: &paidAt,
	}).Error)
}

// TestAPI_Revenue проверяет экран выручки: окно дней, авторизацию, клампы и
// поведение при сбое хранилища.
func TestAPI_Revenue(t *testing.T) {
	t.Parallel()

	t.Run("returns zero-filled window with seeded order", func(t *testing.T) {
		t.Parallel()

		srv, db := newTestServer(t)
		seedRevenueOrder(t, db, "RUB", 49900)

		rec := revenueRequest(t, srv, testToken, "?days=5")
		require.Equal(t, http.StatusOK, rec.Code)

		var payload revenueResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

		require.Len(t, payload.Days, 5)
		// Дата конкретного дня с заказом не фиксируется: handler сам берёт
		// time.Now().UTC(). На границе 00:00 UTC заказ остаётся внутри
		// многодневного окна, но может попасть в предыдущий календарный день.
		// Проверяем инвариант: ровно один день с заказом, остальные дни окна
		// нулевые.
		var paidDays int
		for _, day := range payload.Days {
			if day.Orders != 0 || day.RevenueCents != 0 {
				paidDays++
				assert.Equal(t, int64(1), day.Orders)
				assert.Equal(t, int64(49900), day.RevenueCents)
				continue
			}
			assert.Equal(t, int64(0), day.Orders)
			assert.Equal(t, int64(0), day.RevenueCents)
		}
		assert.Equal(t, 1, paidDays)
		assert.Equal(t, int64(1), payload.Totals.PaidOrders)
		assert.Equal(t, int64(49900), payload.Totals.RevenueCents)
		assert.Equal(t, int64(49900), payload.Totals.AvgCheckCents)
		assert.Equal(t, int64(0), payload.Totals.Canceled)
		assert.Equal(t, int64(0), payload.Totals.Expired)
		assert.Equal(t, "RUB", payload.Currency)
		assert.Equal(t, []string{"RUB"}, payload.Currencies)
	})

	// Смешанные RUB+XTR: авто-выбор остаётся на RUB, суммы и счётчики —
	// строго выбранной валюты, currencies перечисляет обе.
	t.Run("mixed currencies keep amounts in selected currency", func(t *testing.T) {
		t.Parallel()

		srv, db := newTestServer(t)
		seedRevenueOrder(t, db, "RUB", 49900)
		seedRevenueOrder(t, db, "XTR", 199)

		rec := revenueRequest(t, srv, testToken, "?days=5")
		require.Equal(t, http.StatusOK, rec.Code)

		var payload revenueResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		assert.Equal(t, "RUB", payload.Currency)
		assert.Equal(t, []string{"RUB", "XTR"}, payload.Currencies)
		assert.Equal(t, int64(1), payload.Totals.PaidOrders)
		assert.Equal(t, int64(49900), payload.Totals.RevenueCents)
		assert.Equal(t, int64(49900), payload.Totals.AvgCheckCents)

		rec = revenueRequest(t, srv, testToken, "?days=5&currency=XTR")
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		assert.Equal(t, "XTR", payload.Currency)
		assert.Equal(t, []string{"RUB", "XTR"}, payload.Currencies)
		assert.Equal(t, int64(1), payload.Totals.PaidOrders)
		assert.Equal(t, int64(199), payload.Totals.RevenueCents)
		assert.Equal(t, int64(199), payload.Totals.AvgCheckCents)
	})

	// Запрошенной валюты нет среди оплаченных: нулевая статистика этой
	// валюты и доступный список currencies.
	t.Run("missing currency returns empty stats with available list", func(t *testing.T) {
		t.Parallel()

		srv, db := newTestServer(t)
		seedRevenueOrder(t, db, "RUB", 49900)

		rec := revenueRequest(t, srv, testToken, "?days=5&currency=USD")
		require.Equal(t, http.StatusOK, rec.Code)

		var payload revenueResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		assert.Equal(t, "USD", payload.Currency)
		assert.Equal(t, []string{"RUB"}, payload.Currencies)
		assert.Equal(t, int64(0), payload.Totals.PaidOrders)
		assert.Equal(t, int64(0), payload.Totals.RevenueCents)
		assert.Len(t, payload.Days, 5)
		for _, day := range payload.Days {
			assert.Equal(t, int64(0), day.RevenueCents)
		}
	})

	t.Run("malformed currency is bad request", func(t *testing.T) {
		t.Parallel()

		srv, _ := newTestServer(t)

		rec := revenueRequest(t, srv, testToken, "?currency=RUBLES")
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		rec = revenueRequest(t, srv, testToken, "?currency=R%20B")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("requires token", func(t *testing.T) {
		t.Parallel()

		srv, _ := newTestServer(t)

		rec := revenueRequest(t, srv, "", "")

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("malformed days is bad request", func(t *testing.T) {
		t.Parallel()

		srv, _ := newTestServer(t)

		rec := revenueRequest(t, srv, testToken, "?days=abc")

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("days out of range are clamped", func(t *testing.T) {
		t.Parallel()

		srv, _ := newTestServer(t)

		rec := revenueRequest(t, srv, testToken, "?days=100000")
		require.Equal(t, http.StatusOK, rec.Code)

		var payload revenueResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		assert.Len(t, payload.Days, 365)
	})

	t.Run("store failure is internal error", func(t *testing.T) {
		t.Parallel()

		srv, db := newTestServer(t)
		require.NoError(t, db.Close())

		rec := revenueRequest(t, srv, testToken, "")

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
