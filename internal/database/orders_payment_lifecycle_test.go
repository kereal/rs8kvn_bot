package database

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrderByProviderPaymentID_FindsAndNormalizesNotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 993001, "provider-lookup", "provider-lookup-client")
	plan := &Plan{Name: "provider-lookup-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Provider lookup", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440301")
	order := &Order{
		SubscriptionID:    sub.ID,
		ProductID:         product.ID,
		Status:            OrderStatusPending,
		AmountCents:       100,
		Currency:          "RUB",
		PaymentProvider:   "platega",
		ProviderPaymentID: providerID.String(),
		CreatedAt:         time.Now().UTC(),
	}
	require.NoError(t, svc.CreateOrder(ctx, order))

	got, err := svc.GetOrderByProviderPaymentID(ctx, "platega", providerID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, order.ID, got.ID)
	assert.Equal(t, providerID.String(), got.ProviderPaymentID)

	_, err = svc.GetOrderByProviderPaymentID(ctx, "platega", uuid.MustParse("550e8400-e29b-41d4-a716-446655440302"))
	require.ErrorIs(t, err, ErrOrderNotFound)
}

func TestUpdateOrderProviderPaymentID_OnlyPendingOrders(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 993002, "provider-update", "provider-update-client")
	plan := &Plan{Name: "provider-update-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Provider update", DurationDays: 30, PriceCents: 200, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	pending := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 200, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.CreateOrder(ctx, pending))
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440303")
	require.NoError(t, svc.UpdateOrderProviderPaymentID(ctx, pending.ID, providerID))
	stored, err := svc.GetOrderByID(ctx, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, providerID.String(), stored.ProviderPaymentID)

	paid := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 200, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.CreateOrder(ctx, paid))
	err = svc.UpdateOrderProviderPaymentID(ctx, paid.ID, uuid.MustParse("550e8400-e29b-41d4-a716-446655440304"))
	require.ErrorIs(t, err, ErrOrderNotFound)
	stored, err = svc.GetOrderByID(ctx, paid.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.ProviderPaymentID, "a completed order must not receive a new provider ID")
}

func TestPaymentCreationUncertainty_IsClaimedAndClearedOnce(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 993003, "uncertain-payment", "uncertain-payment-client")
	plan := &Plan{Name: "uncertain-payment-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Uncertain payment", DurationDays: 30, PriceCents: 300, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 300, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.CreateOrder(ctx, order))

	claimed, err := svc.MarkPaymentCreationUncertain(ctx, order.ID, true)
	require.NoError(t, err)
	assert.True(t, claimed)
	claimed, err = svc.MarkPaymentCreationUncertain(ctx, order.ID, true)
	require.NoError(t, err)
	assert.False(t, claimed, "only one caller may claim provider creation")

	stored, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.True(t, stored.PaymentCreationUncertain)

	cleared, err := svc.MarkPaymentCreationUncertain(ctx, order.ID, false)
	require.NoError(t, err)
	assert.True(t, cleared)
	cleared, err = svc.MarkPaymentCreationUncertain(ctx, order.ID, false)
	require.NoError(t, err)
	assert.False(t, cleared, "clearing an already-clear uncertainty flag is a no-op")
}

func TestSavePaymentDetails_StoresDetailsOnlyForPendingOrder(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 993004, "save-payment", "save-payment-client")
	plan := &Plan{Name: "save-payment-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Save payment", DurationDays: 30, PriceCents: 400, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 400, Currency: "RUB", PaymentProvider: "platega", PaymentCreationUncertain: true, CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.CreateOrder(ctx, order))

	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440305")
	expiresAt := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)
	require.NoError(t, svc.SavePaymentDetails(ctx, order.ID, providerID, "https://pay.example/400", expiresAt))

	stored, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, providerID.String(), stored.ProviderPaymentID)
	assert.Equal(t, "https://pay.example/400", stored.PaymentURL)
	require.NotNil(t, stored.PaymentExpiresAt)
	assert.WithinDuration(t, expiresAt, *stored.PaymentExpiresAt, time.Second)
	assert.False(t, stored.PaymentCreationUncertain)

	paid := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 400, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.CreateOrder(ctx, paid))
	err = svc.SavePaymentDetails(ctx, paid.ID, uuid.MustParse("550e8400-e29b-41d4-a716-446655440306"), "https://pay.example/paid", expiresAt)
	require.ErrorIs(t, err, ErrOrderNotFound)
}

func TestCancelOrderCAS_IsIdempotentAndHonorsAllowedStatuses(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 993005, "cancel-cas", "cancel-cas-client")
	plan := &Plan{Name: "cancel-cas-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Cancel CAS", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440307")
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 500, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: providerID.String(), CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.CreateOrder(ctx, order))

	changed, err := svc.CancelOrderCAS(ctx, "platega", providerID, []OrderStatus{OrderStatusPending})
	require.NoError(t, err)
	assert.True(t, changed)
	stored, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, OrderStatusCanceled, stored.Status)

	changed, err = svc.CancelOrderCAS(ctx, "platega", providerID, []OrderStatus{OrderStatusPending})
	require.NoError(t, err)
	assert.False(t, changed, "repeated cancellation must be a no-op")

	paidID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440308")
	paid := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 500, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: paidID.String(), CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.CreateOrder(ctx, paid))
	changed, err = svc.CancelOrderCAS(ctx, "platega", paidID, []OrderStatus{OrderStatusPending})
	require.NoError(t, err)
	assert.False(t, changed, "paid order must not be canceled by a pending-only transition")
	stored, err = svc.GetOrderByID(ctx, paid.ID)
	require.NoError(t, err)
	assert.Equal(t, OrderStatusPaid, stored.Status)
}
