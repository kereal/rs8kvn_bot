package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListActiveProducts_FiltersAndSortsByActivePlan(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	activePlan := &Plan{Name: "active-paid-plan", IsActive: true, DevicesLimit: 1}
	inactivePlan := &Plan{Name: "inactive-paid-plan", IsActive: false, DevicesLimit: 1}

	require.NoError(t, svc.db.Create(activePlan).Error)
	require.NoError(t, svc.db.Create(inactivePlan).Error)
	require.NoError(t, svc.db.Model(inactivePlan).Update("is_active", false).Error)
	require.NoError(t, svc.db.Create(&Product{PlanID: activePlan.ID, Name: "expensive", DurationDays: 30, PriceCents: 200, Currency: "RUB", IsActive: true}).Error)
	require.NoError(t, svc.db.Create(&Product{PlanID: activePlan.ID, Name: "cheap", DurationDays: 31, PriceCents: 100, Currency: "RUB", IsActive: true}).Error)
	require.NoError(t, svc.db.Create(&Product{PlanID: inactivePlan.ID, Name: "inactive plan", DurationDays: 30, PriceCents: 1, Currency: "RUB", IsActive: true}).Error)
	require.NoError(t, svc.db.Create(&Product{PlanID: activePlan.ID, Name: "free", DurationDays: 32, PriceCents: 0, Currency: "RUB", IsActive: true}).Error)
	products, err := svc.ListActiveProducts(ctx)
	require.NoError(t, err)
	require.Len(t, products, 2)
	assert.Equal(t, "cheap", products[0].Name)
	assert.Equal(t, "expensive", products[1].Name)
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

func TestUpdateProductGuarded_AllowsChangesBeforeFirstOrder(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	product := &Product{PlanID: plan.ID, Name: "Original", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	product.Name = "Updated"
	product.DurationDays = 60
	product.PriceCents = 700
	require.NoError(t, svc.UpdateProductGuarded(ctx, product))

	got, err := svc.GetProductByID(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated", got.Name)
	assert.Equal(t, 60, got.DurationDays)
	assert.Equal(t, int64(700), got.PriceCents)
}

func TestUpdateProductGuarded_ProtectsImmutableFieldsAfterOrder(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 991001, "guard-user", "guard-client")
	plan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	product := &Product{PlanID: plan.ID, Name: "Original", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 500, Currency: "RUB"}).Error)

	fields := []struct {
		name   string
		mutate func(*Product)
	}{
		{"name", func(p *Product) { p.Name = "Changed" }},
		{"plan_id", func(p *Product) { p.PlanID++ }},
		{"duration_days", func(p *Product) { p.DurationDays++ }},
		{"price_cents", func(p *Product) { p.PriceCents++ }},
		{"currency", func(p *Product) { p.Currency = "USD" }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			candidate := *product
			field.mutate(&candidate)
			require.ErrorIs(t, svc.UpdateProductGuarded(ctx, &candidate), ErrProductImmutable)
		})
	}
}

func TestUpdateProductGuarded_AllowsDeactivationAfterOrder(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 991002, "guard-user-2", "guard-client-2")
	plan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	product := &Product{PlanID: plan.ID, Name: "Original", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 500, Currency: "RUB"}).Error)

	product.IsActive = false
	require.NoError(t, svc.UpdateProductGuarded(ctx, product))
	got, err := svc.GetProductByID(ctx, product.ID)
	require.NoError(t, err)
	assert.False(t, got.IsActive)
}

func TestGetProductByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetProductByID(ctx, 99999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "product not found")
}
