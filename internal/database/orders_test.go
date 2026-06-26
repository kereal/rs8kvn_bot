package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateOrder_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 100, "user1", "client-order-1")
	plan := &Plan{Name: "test-plan-order", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	product := &Product{PlanID: plan.ID, Name: "1 Month", DurationDays: 30, PriceCents: 999, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPending,
		AmountCents:    999,
		Currency:       "RUB",
	}
	err := svc.CreateOrder(ctx, order)
	require.NoError(t, err)
	assert.NotZero(t, order.ID)
	assert.Equal(t, OrderStatusPending, order.Status)
	assert.Equal(t, int64(999), order.AmountCents)
}

func TestCreateOrder_WithPaymentProvider(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 200, "user2", "client-order-2")
	plan := &Plan{Name: "plan-order-pp", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{
		SubscriptionID:    sub.ID,
		ProductID:         product.ID,
		Status:            OrderStatusPending,
		AmountCents:       500,
		Currency:          "RUB",
		PaymentProvider:   "yookassa",
		ProviderPaymentID: "pay-12345",
	}
	err := svc.CreateOrder(ctx, order)
	require.NoError(t, err)
	assert.Equal(t, "yookassa", order.PaymentProvider)
	assert.Equal(t, "pay-12345", order.ProviderPaymentID)
}

func TestGetOrderByID_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 300, "user3", "client-order-3")
	plan := &Plan{Name: "plan-order-get", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPending,
		AmountCents:    100,
		Currency:       "RUB",
	}
	require.NoError(t, svc.CreateOrder(ctx, order))

	got, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.ID, got.ID)
	assert.Equal(t, OrderStatusPending, got.Status)
	assert.Equal(t, sub.ID, got.SubscriptionID)
	assert.Equal(t, product.ID, got.ProductID)
}

func TestGetOrderByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetOrderByID(ctx, 99999)
	assert.ErrorIs(t, err, ErrOrderNotFound)
}

func TestGetOrdersBySubscriptionID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 400, "user4", "client-order-4")
	plan := &Plan{Name: "plan-order-list", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 200, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order1 := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 200, Currency: "RUB", PaymentProvider: "yookassa", ProviderPaymentID: "pay-list-1"}
	order2 := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 300, Currency: "RUB", PaymentProvider: "yookassa", ProviderPaymentID: "pay-list-2"}
	require.NoError(t, svc.CreateOrder(ctx, order1))
	require.NoError(t, svc.CreateOrder(ctx, order2))

	orders, err := svc.GetOrdersBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, orders, 2)
	// Ordered by created_at DESC, so order2 (created later) comes first
	assert.Equal(t, order2.ID, orders[0].ID)
	assert.Equal(t, order1.ID, orders[1].ID)
}

func TestGetOrdersBySubscriptionID_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	orders, err := svc.GetOrdersBySubscriptionID(ctx, 99999)
	require.NoError(t, err)
	assert.Empty(t, orders)
}

func TestUpdateOrderStatus(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 500, "user5", "client-order-5")
	plan := &Plan{Name: "plan-order-status", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 400, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 400, Currency: "RUB"}
	require.NoError(t, svc.CreateOrder(ctx, order))

	err := svc.UpdateOrderStatus(ctx, order.ID, OrderStatusPaid)
	require.NoError(t, err)

	got, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, OrderStatusPaid, got.Status)
}

func TestUpdateOrderStatus_Transitions(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 600, "user6", "client-order-6")
	plan := &Plan{Name: "plan-order-trans", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 600, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	statuses := []OrderStatus{OrderStatusPending, OrderStatusPaid, OrderStatusExpired, OrderStatusCanceled}
	for i, status := range statuses {
		order := &Order{
			SubscriptionID:    sub.ID,
			ProductID:         product.ID,
			Status:            OrderStatusPending,
			AmountCents:       600,
			Currency:          "RUB",
			PaymentProvider:   fmt.Sprintf("provider-%d", i),
			ProviderPaymentID: fmt.Sprintf("pay-trans-%d", i),
		}
		require.NoError(t, svc.CreateOrder(ctx, order))

		err := svc.UpdateOrderStatus(ctx, order.ID, status)
		require.NoError(t, err)

		got, err := svc.GetOrderByID(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, status, got.Status)
	}
}

func TestUpdateOrderPaidStatus_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 700, "user7", "client-order-7")
	plan := &Plan{Name: "plan-order-paid", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 700, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 700, Currency: "RUB"}
	require.NoError(t, svc.CreateOrder(ctx, order))

	err := svc.UpdateOrderPaidStatus(ctx, order.ID)
	require.NoError(t, err)

	got, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, OrderStatusPaid, got.Status)
	require.NotNil(t, got.PaidAt)
	// paid_at should be truncated to minute precision
	assert.WithinDuration(t, time.Now().UTC(), *got.PaidAt, 2*time.Minute)
}

func TestUpdateOrderPaidStatus_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.UpdateOrderPaidStatus(ctx, 99999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestUpdateOrderActivatedAt_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 800, "user8", "client-order-8")
	plan := &Plan{Name: "plan-order-act", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 800, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 800, Currency: "RUB"}
	require.NoError(t, svc.CreateOrder(ctx, order))

	now := time.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(30 * 24 * time.Hour)

	err := svc.UpdateOrderActivatedAt(ctx, order.ID, now, expiresAt)
	require.NoError(t, err)

	got, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ActivatedAt)
	require.NotNil(t, got.ExpiresAt)
	assert.WithinDuration(t, now, *got.ActivatedAt, time.Second)
	assert.WithinDuration(t, expiresAt, *got.ExpiresAt, time.Second)
}

func TestUpdateOrderActivatedAt_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)

	err := svc.UpdateOrderActivatedAt(ctx, 99999, now, expiresAt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestOrderStatusConstants(t *testing.T) {
	assert.Equal(t, OrderStatus("pending"), OrderStatusPending)
	assert.Equal(t, OrderStatus("paid"), OrderStatusPaid)
	assert.Equal(t, OrderStatus("expired"), OrderStatusExpired)
	assert.Equal(t, OrderStatus("canceled"), OrderStatusCanceled)
}
