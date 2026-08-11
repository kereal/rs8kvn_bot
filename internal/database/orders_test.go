package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

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

func TestOrderStatusConstants(t *testing.T) {
	assert.Equal(t, OrderStatus("pending"), OrderStatusPending)
	assert.Equal(t, OrderStatus("paid"), OrderStatusPaid)
	assert.Equal(t, OrderStatus("expired"), OrderStatusExpired)
	assert.Equal(t, OrderStatus("canceled"), OrderStatusCanceled)
}

func TestCalculatePaymentExpiry_SamePlanExtends(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &Product{PlanID: 1, DurationDays: 30}

	result := CalculatePaymentExpiry(now, &Subscription{PlanID: 1, ExpiresAt: &oldExpiry}, product)
	assert.Equal(t, oldExpiry.AddDate(0, 0, 30), result)
}

func TestCalculatePaymentExpiry_NilExpiryUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	product := &Product{PlanID: 1, DurationDays: 30}

	result := CalculatePaymentExpiry(now, &Subscription{PlanID: 1, ExpiresAt: nil}, product)
	assert.Equal(t, now.AddDate(0, 0, 30), result)
}

func TestCalculatePaymentExpiry_DifferentPlanUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &Product{PlanID: 2, DurationDays: 30}

	result := CalculatePaymentExpiry(now, &Subscription{PlanID: 1, ExpiresAt: &oldExpiry}, product)
	assert.Equal(t, now.AddDate(0, 0, 30), result)
}

func TestCalculatePaymentExpiry_NilProductUsesBase(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)

	result := CalculatePaymentExpiry(now, &Subscription{PlanID: 1, ExpiresAt: &oldExpiry}, nil)
	assert.Equal(t, now, result)
}

func TestConfirmOrderPaidCAS_RecalculatesFromCurrentSubscription(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	plan := &Plan{Name: "plan-current-expiry", DevicesLimit: 1}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	currentExpiry := now.Add(10 * 24 * time.Hour)
	sub := createTestSubscription(t, svc, 1901, "current-expiry", "client-current-expiry")
	sub.PlanID = plan.ID
	sub.ExpiresAt = &currentExpiry
	require.NoError(t, svc.db.Save(sub).Error)
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 100, Currency: "RUB"}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	// The CAS must recompute expiry from the current subscription, not from any
	// stale caller snapshot: the expired-at value passed in is irrelevant.
	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, &Subscription{ID: sub.ID, PlanID: plan.ID}, product, nil)
	require.NoError(t, err)
	assert.True(t, activated)
	got, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ExpiresAt)
	assert.Equal(t, currentExpiry.AddDate(0, 0, 30), *got.ExpiresAt)
}

func TestConfirmOrderPaidCAS_SwitchesPlanAndStatus(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()

	// trial plan + paid plan; sub starts on trial with expired status.
	trialPlan := &Plan{Name: "plan-cas-trial", DevicesLimit: 1, TrafficLimit: 100}
	require.NoError(t, svc.db.WithContext(ctx).Create(trialPlan).Error)
	paidPlan := &Plan{Name: "plan-cas-premium", DevicesLimit: 2, TrafficLimit: 20000}
	require.NoError(t, svc.db.WithContext(ctx).Create(paidPlan).Error)
	product := &Product{PlanID: paidPlan.ID, Name: "1M", DurationDays: 30, PriceCents: 499, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	sub := createTestSubscription(t, svc, 900, "usercas", "client-cas")
	sub.PlanID = trialPlan.ID
	sub.Status = "expired"
	require.NoError(t, svc.db.Save(sub).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPending,
		AmountCents:    499,
		Currency:       "RUB",
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC().Truncate(time.Second)

	var gotPlanID uint
	applyPlan := func(_ context.Context, tx *gorm.DB, subscriptionID uint, planID uint) error {
		assert.Equal(t, sub.ID, subscriptionID)
		gotPlanID = planID
		return nil
	}

	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, applyPlan)
	require.NoError(t, err)
	assert.True(t, activated)
	// applyPlan must receive the PRODUCT plan, not the stale sub.PlanID.
	assert.Equal(t, paidPlan.ID, gotPlanID)

	got, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, paidPlan.ID, got.PlanID, "subscription must switch to the purchased plan")
	assert.Equal(t, string(SubscriptionStatusActive), got.Status, "payment must reactivate the subscription")
	assert.Equal(t, product.ID, *got.ProductID)

	// Idempotent retry: order already paid, no second activation.
	activated, err = svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, applyPlan)
	require.NoError(t, err)
	assert.False(t, activated)
}
