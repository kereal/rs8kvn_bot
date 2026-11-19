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
	first, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderPlatega, product.PriceCents, product.Currency, nil, now)
	require.NoError(t, err)
	require.NotNil(t, first)

	first.ProviderPaymentID = "550e8400-e29b-41d4-a716-446655440201"
	first.PaymentURL = "https://pay.example/intent"
	expires := now.Add(15 * time.Minute)
	first.PaymentExpiresAt = &expires
	require.NoError(t, svc.db.Save(first).Error)

	second, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderPlatega, product.PriceCents, product.Currency, nil, now.Add(time.Minute))
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.ProviderPaymentID, second.ProviderPaymentID)
}

func TestFindOrCreatePendingPaymentOrder_DoesNotAuditExpiryOnRollback(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 992003, "intent-rollback", "intent-client-rollback")
	plan := &Plan{Name: "intent-rollback-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Rollback product", DurationDays: 30, PriceCents: 1400, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	now := time.Now().UTC().Truncate(time.Second)
	old, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderPlatega, product.PriceCents, product.Currency, nil, now)
	require.NoError(t, err)

	expires := now.Add(-time.Minute)
	require.NoError(t, svc.db.Model(&Order{}).Where("id = ?", old.ID).Update("payment_expires_at", expires).Error)

	// A negative amount violates the orders CHECK constraint *after* the expiry
	// update inside the same transaction, so the transaction rolls back.
	_, err = svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderPlatega, -1, product.Currency, nil, now)
	require.Error(t, err)

	var stored Order
	require.NoError(t, svc.db.First(&stored, old.ID).Error)
	assert.Equal(t, OrderStatusPending, stored.Status, "a rolled-back transaction must leave the old order untouched")

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventOrderExpired, 10)
	require.NoError(t, err)
	assert.Empty(t, events, "order_expired must not be journaled when the transaction rolled back")
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
	old, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderPlatega, product.PriceCents, product.Currency, nil, now)
	require.NoError(t, err)

	expires := now.Add(-time.Minute)
	require.NoError(t, svc.db.Model(&Order{}).Where("id = ?", old.ID).Updates(map[string]any{
		"payment_expires_at":  expires,
		"provider_payment_id": "550e8400-e29b-41d4-a716-446655440202",
		"payment_url":         "https://pay.example/old",
	}).Error)

	replacement, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderPlatega, product.PriceCents, product.Currency, nil, now)
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

// TestFindOrCreatePendingPaymentOrder_SetsPaymentExpiresAtAtomically verifies
// that a non-nil paymentExpiresAt is written in the same INSERT as the pending
// status (Stars: локальный TTL без отдельного SavePaymentDetails), а nil —
// сохраняется как NULL (Platega: срок приходит из ответа провайдера).
func TestFindOrCreatePendingPaymentOrder_SetsPaymentExpiresAtAtomically(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 992005, "intent-ttl", "intent-client-ttl")
	plan := &Plan{Name: "intent-ttl-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "TTL product", DurationDays: 30, PriceCents: 900, Currency: "XTR", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	plategaProduct := &Product{PlanID: plan.ID, Name: "Platega product", DurationDays: 60, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(plategaProduct).Error)

	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(30 * time.Minute)

	stars, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderTelegramStars, product.PriceCents, product.Currency, &expires, now)
	require.NoError(t, err)
	require.NotNil(t, stars)
	require.NotNil(t, stars.PaymentExpiresAt, "Stars TTL must be part of the pending INSERT")
	assert.True(t, expires.Equal(*stars.PaymentExpiresAt))

	stored, err := svc.GetOrderByID(ctx, stars.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.PaymentExpiresAt, "TTL must be persisted in the same INSERT as status='pending'")
	assert.True(t, expires.Equal(*stored.PaymentExpiresAt))
	assert.Equal(t, PaymentProviderTelegramStars, stored.PaymentProvider)

	platega, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, plategaProduct.ID, PaymentProviderPlatega, plategaProduct.PriceCents, plategaProduct.Currency, nil, now)
	require.NoError(t, err)
	require.NotNil(t, platega)

	storedPlatega, err := svc.GetOrderByID(ctx, platega.ID)
	require.NoError(t, err)
	assert.Nil(t, storedPlatega.PaymentExpiresAt, "nil TTL (Platega path) must stay NULL")
}

// TestFindOrCreatePendingPaymentOrder_ProviderFilterPendingLookup verifies that
// pending-lookup фильтруется по провайдеру: Stars-pending не находится при
// поиске platega и наоборот.
func TestFindOrCreatePendingPaymentOrder_ProviderFilterPendingLookup(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 992006, "intent-provider", "intent-client-provider")
	plan := &Plan{Name: "intent-provider-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Provider product", DurationDays: 30, PriceCents: 900, Currency: "XTR", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	plategaProduct := &Product{PlanID: plan.ID, Name: "Provider platega product", DurationDays: 60, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(plategaProduct).Error)

	now := time.Now().UTC().Truncate(time.Second)
	stars, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderTelegramStars, product.PriceCents, product.Currency, nil, now)
	require.NoError(t, err)
	require.NotNil(t, stars)

	got, err := svc.FindPendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderPlatega, now)
	require.NoError(t, err)
	assert.Nil(t, got, "Stars pending must not be found when looking up platega intents")

	foundStars, err := svc.FindPendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderTelegramStars, now)
	require.NoError(t, err)
	require.NotNil(t, foundStars)
	assert.Equal(t, stars.ID, foundStars.ID)

	platega, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, plategaProduct.ID, PaymentProviderPlatega, plategaProduct.PriceCents, plategaProduct.Currency, nil, now)
	require.NoError(t, err)
	require.NotNil(t, platega)

	foundPlatega, err := svc.FindPendingPaymentOrder(ctx, sub.ID, plategaProduct.ID, PaymentProviderPlatega, now)
	require.NoError(t, err)
	require.NotNil(t, foundPlatega)
	assert.Equal(t, platega.ID, foundPlatega.ID)

	notStars, err := svc.FindPendingPaymentOrder(ctx, sub.ID, plategaProduct.ID, PaymentProviderTelegramStars, now)
	require.NoError(t, err)
	assert.Nil(t, notStars, "platega pending must not be found when looking up Stars intents")
}
