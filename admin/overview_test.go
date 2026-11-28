package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingOverviewStore имитирует сбой хранилища: экран обязан превратить
// неожиданную ошибку чтения снапшота в 500, а не отдать пустые нули.
type failingOverviewStore struct{}

func (failingOverviewStore) GetOverviewSnapshot(context.Context) (*database.OverviewSnapshot, error) {
	return nil, errors.New("store down")
}

// TestAPI_Overview проверяет экран состояния поверх живого хранилища.
func TestAPI_Overview(t *testing.T) {
	t.Run("aggregates seeded database", func(t *testing.T) {
		srv, db := newTestServer(t)
		ctx := context.Background()

		plan := &database.Plan{Name: "ov-plan", DevicesLimit: 1, TrafficLimit: 1024}
		require.NoError(t, db.GetDB().Create(plan).Error)

		node := &database.Node{Name: "ov-node", IsActive: true, Host: "http://n1", APIToken: "t1", InboundIDs: `[1]`}
		require.NoError(t, db.GetDB().Create(node).Error)

		// Активная подписка, истекающая сегодня-завтра: попадает в блок expiring.
		expiresAt := time.Now().UTC().Add(2 * time.Hour)
		sub := &database.Subscription{
			TelegramID: 700101, ClientID: "ov-c1", SubscriptionID: "ov-sub-1",
			Status: "active", PlanID: plan.ID, ExpiresAt: &expiresAt,
		}
		require.NoError(t, db.CreateSubscription(ctx, sub, ""))
		// pending_remove — единственная запись в очереди узлов этого снапшота.
		require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
			SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingRemove,
		}))

		product := &database.Product{PlanID: plan.ID, Name: "ov-product", DurationDays: 30, PriceCents: 100}
		require.NoError(t, db.GetDB().Create(product).Error)
		require.NoError(t, db.GetDB().Create(&database.Order{
			SubscriptionID: sub.ID, ProductID: product.ID, Status: database.OrderStatusPaid, AmountCents: 100,
		}).Error)

		rec := do(t, srv, http.MethodGet, "/admin/api/overview", testToken, "")
		require.Equal(t, http.StatusOK, rec.Code)

		var payload struct {
			Subscriptions struct {
				Total int64 `json:"total"`
			} `json:"subscriptions"`
			Nodes struct {
				Total int64 `json:"total"`
			} `json:"nodes"`
			Bindings struct {
				ByStatus        map[string]int64 `json:"by_status"`
				PendingTotal    int64            `json:"pending_total"`
				OldestPendingAt *string          `json:"oldest_pending_at"`
			} `json:"bindings"`
			Expiring struct {
				Total int64 `json:"total"`
				Days  []struct {
					Date  string `json:"date"`
					Count int64  `json:"count"`
				} `json:"days"`
			} `json:"expiring"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

		// Блок истечений присутствует в JSON всегда; окно 30 дней = 31 бакет,
		// total совпадает с суммой дневных бакетов.
		require.Len(t, payload.Expiring.Days, 31)
		var daysSum int64
		for _, d := range payload.Expiring.Days {
			daysSum += d.Count
		}
		assert.Equal(t, payload.Expiring.Total, daysSum)
		assert.GreaterOrEqual(t, payload.Expiring.Total, int64(1))

		assert.Equal(t, int64(1), payload.Bindings.PendingTotal)
		assert.NotNil(t, payload.Bindings.OldestPendingAt)
		assert.Equal(t, int64(1), payload.Bindings.ByStatus[string(database.SyncStatusPendingRemove)])
		assert.GreaterOrEqual(t, payload.Subscriptions.Total, int64(1))
		assert.GreaterOrEqual(t, payload.Nodes.Total, int64(1))
	})

	t.Run("requires token", func(t *testing.T) {
		srv, _ := newTestServer(t)

		rec := do(t, srv, http.MethodGet, "/admin/api/overview", "", "")

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("store failure is internal error", func(t *testing.T) {
		srv, _ := newTestServer(t)
		srv.overview = failingOverviewStore{}

		rec := do(t, srv, http.MethodGet, "/admin/api/overview", testToken, "")

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("counts orders requiring reconciliation", func(t *testing.T) {
		srv, db := newTestServer(t)

		plan := &database.Plan{Name: "ov-unc-plan", DevicesLimit: 1, TrafficLimit: 1024}
		require.NoError(t, db.GetDB().Create(plan).Error)
		sub := &database.Subscription{
			TelegramID: 700102, ClientID: "ov-c2", SubscriptionID: "ov-sub-2", Status: "active", PlanID: plan.ID,
		}
		require.NoError(t, db.GetDB().Create(sub).Error)
		product := &database.Product{PlanID: plan.ID, Name: "ov-product-2", DurationDays: 30, PriceCents: 100}
		require.NoError(t, db.GetDB().Create(product).Error)
		require.NoError(t, db.GetDB().Create(&database.Order{
			SubscriptionID: sub.ID, ProductID: product.ID, Status: database.OrderStatusPending, AmountCents: 100,
		}).Error)
		require.NoError(t, db.GetDB().Create(&database.Order{
			SubscriptionID: sub.ID, ProductID: product.ID, Status: database.OrderStatusPending, AmountCents: 200,
			PaymentCreationUncertain: true,
		}).Error)

		rec := do(t, srv, http.MethodGet, "/admin/api/overview", testToken, "")
		require.Equal(t, http.StatusOK, rec.Code)

		var payload struct {
			Orders struct {
				Total     int64            `json:"total"`
				Uncertain int64            `json:"uncertain"`
				ByStatus  map[string]int64 `json:"by_status"`
			} `json:"orders"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

		assert.Equal(t, int64(2), payload.Orders.Total)
		assert.Equal(t, int64(1), payload.Orders.Uncertain)
		assert.Equal(t, int64(2), payload.Orders.ByStatus["pending"])
	})
}
