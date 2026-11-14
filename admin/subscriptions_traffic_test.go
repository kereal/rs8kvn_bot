package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"github.com/stretchr/testify/require"
)

// trafficFixture создаёт план с заданным лимитом, продукт и одну активную
// подписку; возвращает сервер с подключённым трафик-сервисом. Ноды не
// привязываются: опрос 3x-ui в юнит-тесте не происходит, форма ответа и
// граничные состояния проверяются без живых панелей.
func trafficFixture(t *testing.T, limitBytes int64) (*Server, []*database.Subscription) {
	t.Helper()

	srv, db := newSubscriptionTestServer(t)
	ctx := context.Background()

	plan := &database.Plan{Name: "traffic-plan", IsActive: true, DevicesLimit: 2, TrafficLimit: limitBytes}
	require.NoError(t, db.CreatePlan(ctx, plan))

	product := &database.Product{PlanID: plan.ID, Name: "30 дней", DurationDays: 30, PriceCents: 10000, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().Create(product).Error)

	sub := &database.Subscription{
		TelegramID: 777000777, Username: "traffic-user", ClientID: "c-777", SubscriptionID: "sub-777",
		Status: "active", PlanID: plan.ID, ProductID: &product.ID,
	}
	require.NoError(t, db.GetDB().Create(sub).Error)

	// Тот же сервис, что в newSubscriptionTestServer, отдельно как TrafficSource.
	srv.SetTrafficSource(service.NewSubscriptionService(db, service.NodeClients{}, nil, &config.Config{}))

	return srv, []*database.Subscription{sub}
}

func TestAPI_SubscriptionTrafficForm(t *testing.T) {
	t.Parallel()

	// Лимит 100 ГБ: нод нет, чтение недоступно, но форма ответа полная.
	srv, _ := trafficFixture(t, 100<<30)

	rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions/1/traffic", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	for _, key := range []string{"used_gb", "limit_gb", "percentage", "unlimited", "available", "reset_info", "plan_name"} {
		require.Contains(t, payload, key)
	}
	require.Equal(t, false, payload["unlimited"])
	require.Equal(t, false, payload["available"])
	require.Equal(t, "30 дней", payload["plan_name"]) // продукт перекрывает имя плана
	require.Equal(t, float64(100), payload["limit_gb"])
}

func TestAPI_SubscriptionTrafficUnlimited(t *testing.T) {
	t.Parallel()

	// Лимит 0 — безлимит: узлы не опрашиваются, unlimited=true.
	srv, _ := trafficFixture(t, 0)

	rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions/1/traffic", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, true, payload["unlimited"])
	require.Equal(t, float64(0), payload["limit_gb"])
}

func TestAPI_SubscriptionTrafficNotFound(t *testing.T) {
	t.Parallel()

	srv, _ := trafficFixture(t, 100<<30)

	rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions/9999/traffic", testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "not_found")
}

func TestAPI_SubscriptionTrafficNotWired(t *testing.T) {
	t.Parallel()

	// Без SetTrafficSource эндпоинт отвечает 503, а не падает.
	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/subscriptions/1/traffic", testToken, "")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
