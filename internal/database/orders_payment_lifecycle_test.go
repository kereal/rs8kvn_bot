package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	got, err := svc.GetOrderByProviderPaymentID(ctx, "platega", providerID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, order.ID, got.ID)
	assert.Equal(t, providerID.String(), got.ProviderPaymentID)

	_, err = svc.GetOrderByProviderPaymentID(ctx, "platega", uuid.MustParse("550e8400-e29b-41d4-a716-446655440302"))
	require.ErrorIs(t, err, ErrOrderNotFound)
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
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

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
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

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
	require.NoError(t, svc.db.WithContext(ctx).Create(paid).Error)
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
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

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
	require.NoError(t, svc.db.WithContext(ctx).Create(paid).Error)
	changed, err = svc.CancelOrderCAS(ctx, "platega", paidID, []OrderStatus{OrderStatusPending})
	require.NoError(t, err)
	assert.False(t, changed, "paid order must not be canceled by a pending-only transition")

	stored, err = svc.GetOrderByID(ctx, paid.ID)
	require.NoError(t, err)
	assert.Equal(t, OrderStatusPaid, stored.Status)
}

func TestCancelPaidOrderAndDowngradeCAS_IgnoresExpiredPaidOrders(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	paidPlan := &Plan{Name: "chargeback-paid", IsActive: true}
	require.NoError(t, svc.db.Create(paidPlan).Error)

	sub := &Subscription{TelegramID: 994001, Username: "chargeback-expired", ClientID: "chargeback-expired-client", SubscriptionID: "chargeback-expired-sub", Status: "active", PlanID: paidPlan.ID, ExpiresAt: ptrTime(time.Now().UTC().Add(24 * time.Hour))}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{PlanID: paidPlan.ID, Name: "chargeback", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440399")
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)
	chargebackOrder := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: providerID.String(), ExpiresAt: ptrTime(now.Add(time.Hour)), CreatedAt: now.Add(-2 * time.Hour)}
	historical := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "550e8400-e29b-41d4-a716-446655440398", ExpiresAt: &expired, CreatedAt: now.Add(-48 * time.Hour)}

	require.NoError(t, svc.db.WithContext(ctx).Create(chargebackOrder).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(historical).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, "platega", providerID, now, freePlan.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.WasPaid)
	assert.True(t, result.Transitioned)
	assert.True(t, result.Downgraded)

	updated, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, freePlan.ID, updated.PlanID)
	assert.Nil(t, updated.ExpiresAt)
}

func TestCancelPaidOrderAndDowngradeCAS_RollsBackOnPlanApplyFailure(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	paidPlan := &Plan{Name: "chargeback-rollback", IsActive: true}
	require.NoError(t, svc.db.Create(paidPlan).Error)
	sub := &Subscription{TelegramID: 994004, Username: "chargeback-rollback", ClientID: "chargeback-rollback-client", SubscriptionID: "chargeback-rollback-sub", Status: "active", PlanID: paidPlan.ID, ExpiresAt: ptrTime(time.Now().UTC().Add(24 * time.Hour))}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{PlanID: paidPlan.ID, Name: "chargeback rollback", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440395")
	now := time.Now().UTC()
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: providerID.String(), ExpiresAt: ptrTime(now.Add(time.Hour)), CreatedAt: now}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	wantErr := errors.New("sync setup failed")
	_, err = svc.CancelPaidOrderAndDowngradeCAS(ctx, "platega", providerID, now, freePlan.ID, func(context.Context, *gorm.DB, uint, uint) error {
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)

	storedOrder, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, OrderStatusPaid, storedOrder.Status, "chargeback status must roll back with sync setup")

	storedSub, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, paidPlan.ID, storedSub.PlanID, "subscription downgrade must roll back with sync setup")
	assert.NotNil(t, storedSub.ExpiresAt)
}

func TestCancelPaidOrderAndDowngradeCAS_DoesNotReactivateRevokedSubscription(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	paidPlan := &Plan{Name: "chargeback-revoked", IsActive: true}
	require.NoError(t, svc.db.Create(paidPlan).Error)
	sub := &Subscription{TelegramID: 994006, Username: "chargeback-revoked", ClientID: "chargeback-revoked-client", SubscriptionID: "chargeback-revoked-sub", Status: "revoked", PlanID: paidPlan.ID, ExpiresAt: ptrTime(time.Now().UTC().Add(24 * time.Hour))}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{PlanID: paidPlan.ID, Name: "revoked chargeback", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440394")
	now := time.Now().UTC()
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: providerID.String(), ExpiresAt: ptrTime(now.Add(time.Hour)), CreatedAt: now}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, "platega", providerID, now, freePlan.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.WasPaid)
	assert.True(t, result.Transitioned)
	assert.False(t, result.Downgraded)

	updated, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "revoked", updated.Status)
	assert.Equal(t, paidPlan.ID, updated.PlanID)
}

func TestCancelPaidOrderAndDowngradeCAS_PreservesActivePaidCoverage(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	paidPlan := &Plan{Name: "chargeback-covered", IsActive: true}
	require.NoError(t, svc.db.Create(paidPlan).Error)
	sub := &Subscription{TelegramID: 994002, Username: "chargeback-covered", ClientID: "chargeback-covered-client", SubscriptionID: "chargeback-covered-sub", Status: "active", PlanID: paidPlan.ID, ExpiresAt: ptrTime(time.Now().UTC().Add(24 * time.Hour))}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{PlanID: paidPlan.ID, Name: "covered", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	now := time.Now().UTC()
	chargebackID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440397")
	chargebackOrder := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: chargebackID.String(), ExpiresAt: ptrTime(now.Add(time.Hour)), CreatedAt: now.Add(-2 * time.Hour)}
	activeOther := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "550e8400-e29b-41d4-a716-446655440396", ExpiresAt: ptrTime(now.Add(2 * time.Hour)), CreatedAt: now.Add(-time.Hour)}

	require.NoError(t, svc.db.WithContext(ctx).Create(chargebackOrder).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(activeOther).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, "platega", chargebackID, now, freePlan.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Transitioned)
	assert.False(t, result.Downgraded)

	updated, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, paidPlan.ID, updated.PlanID)
}
