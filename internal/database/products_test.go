package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetActiveByPlanID_WithActiveProduct(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Use the trial plan (auto-seeded) which is active
	plan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	product := &Product{PlanID: plan.ID, Name: "Trial 3 Days", DurationDays: 3, PriceCents: 0, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	products, err := svc.GetActiveByPlanID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Len(t, products, 1)
	assert.Equal(t, "Trial 3 Days", products[0].Name)
	assert.Equal(t, 3, products[0].DurationDays)
	assert.True(t, products[0].IsActive)
}

func TestGetActiveByPlanID_ExcludesInactiveProduct(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	activeProduct := &Product{PlanID: plan.ID, Name: "Active Product", DurationDays: 7, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(activeProduct).Error)
	inactiveProduct := &Product{PlanID: plan.ID, Name: "Inactive Product", DurationDays: 14, PriceCents: 200, Currency: "RUB"}
	require.NoError(t, svc.db.WithContext(ctx).Create(inactiveProduct).Error)
	// Force is_active=false (GORM skips zero-value bools on Create)
	require.NoError(t, svc.db.WithContext(ctx).Model(inactiveProduct).Update("is_active", false).Error)

	products, err := svc.GetActiveByPlanID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Len(t, products, 1)
	assert.Equal(t, "Active Product", products[0].Name)
}

func TestGetActiveByPlanID_ExcludesInactivePlan(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Create an inactive plan (not the auto-seeded trial/free plans)
	inactivePlan := &Plan{Name: "inactive-plan", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(inactivePlan).Error)
	// Force is_active=false (GORM skips zero-value bools on Create)
	require.NoError(t, svc.db.WithContext(ctx).Model(inactivePlan).Update("is_active", false).Error)

	product := &Product{PlanID: inactivePlan.ID, Name: "Product on Inactive Plan", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	products, err := svc.GetActiveByPlanID(ctx, inactivePlan.ID)
	require.NoError(t, err)
	assert.Empty(t, products, "active products on an inactive plan should not be returned")
}

func TestGetActiveByPlanID_FreePlanAllowedEvenIfInactive(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Free plan is auto-seeded and its products should be returned
	// even when plans.is_active is not true (because of the OR plans.name = FreePlanName condition)
	plan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	product := &Product{PlanID: plan.ID, Name: "Free Tier", DurationDays: 0, PriceCents: 0, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	products, err := svc.GetActiveByPlanID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Len(t, products, 1)
	assert.Equal(t, "Free Tier", products[0].Name)
}

func TestGetActiveByPlanID_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Custom plan with no products
	plan := &Plan{Name: "empty-plan-products", DevicesLimit: 1, TrafficLimit: 1024, IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	products, err := svc.GetActiveByPlanID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Empty(t, products)
}

func TestGetProductByID_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	product := &Product{PlanID: plan.ID, Name: "1 Month", DurationDays: 30, PriceCents: 999, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	got, err := svc.GetProductByID(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, product.ID, got.ID)
	assert.Equal(t, "1 Month", got.Name)
	assert.Equal(t, 30, got.DurationDays)
	assert.Equal(t, int64(999), got.PriceCents)
	assert.Equal(t, "RUB", got.Currency)
	assert.True(t, got.IsActive)
}

func TestGetProductByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetProductByID(ctx, 99999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "product not found")
}
