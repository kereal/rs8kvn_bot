package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindOrCreatePendingPaymentOrder_ReusesActiveIntent(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 992001, "intent-reuse", "intent-client")
	plan := &Plan{Name: "intent-reuse-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Intent product", DurationDays: 30, PriceCents: 1200, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	now := time.Now().UTC().Truncate(time.Second)
	first, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, product.PriceCents, product.Currency, now)
	require.NoError(t, err)
	require.NotNil(t, first)

	first.ProviderPaymentID = "550e8400-e29b-41d4-a716-446655440201"
	first.PaymentURL = "https://pay.example/intent"
	expires := now.Add(15 * time.Minute)
	first.PaymentExpiresAt = &expires
	require.NoError(t, svc.db.Save(first).Error)

	second, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, product.PriceCents, product.Currency, now.Add(time.Minute))
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.ProviderPaymentID, second.ProviderPaymentID)
}

func TestFindOrCreatePendingPaymentOrder_ExpiresAndCreatesReplacement(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 992002, "intent-expiry", "intent-client-expiry")
	plan := &Plan{Name: "intent-expiry-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Expiring product", DurationDays: 30, PriceCents: 1300, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	now := time.Now().UTC().Truncate(time.Second)
	old, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, product.PriceCents, product.Currency, now)
	require.NoError(t, err)

	expires := now.Add(-time.Minute)
	require.NoError(t, svc.db.Model(&Order{}).Where("id = ?", old.ID).Updates(map[string]any{
		"payment_expires_at":  expires,
		"provider_payment_id": "550e8400-e29b-41d4-a716-446655440202",
		"payment_url":         "https://pay.example/old",
	}).Error)

	replacement, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, product.PriceCents, product.Currency, now)
	require.NoError(t, err)
	require.NotNil(t, replacement)
	assert.NotEqual(t, old.ID, replacement.ID)
	assert.Equal(t, OrderStatusExpired, func() OrderStatus {
		var stored Order
		require.NoError(t, svc.db.First(&stored, old.ID).Error)

		return stored.Status
	}())
	assert.Empty(t, replacement.ProviderPaymentID)
}
