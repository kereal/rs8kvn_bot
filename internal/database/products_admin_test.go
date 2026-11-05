package database

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createProductTestPlan(t *testing.T, svc *Service, name string) *Plan {
	t.Helper()

	plan := &Plan{Name: name, IsActive: true, DevicesLimit: 2, TrafficLimit: 100 << 30}
	require.NoError(t, svc.CreatePlan(context.Background(), plan))

	return plan
}

func validProduct(planID uint) *Product {
	return &Product{
		PlanID: planID, Name: "30 дней", DurationDays: 30, PriceCents: 39900, Currency: "RUB", IsActive: true,
	}
}

func productAuditEvents(t *testing.T, svc *Service, event string) []AuditLog {
	t.Helper()

	var rows []AuditLog

	require.NoError(t, svc.db.Where("entity_type = ? AND event = ?", EntityProduct, event).Find(&rows).Error)

	return rows
}

func TestCreateProduct_CreatesRowAndAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	product := validProduct(plan.ID)
	require.NoError(t, svc.CreateProduct(ctx, product))
	require.NotZero(t, product.ID)

	got, err := svc.GetProductByID(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.ID, got.PlanID)
	assert.Equal(t, "30 дней", got.Name)
	assert.Equal(t, 30, got.DurationDays)
	assert.Equal(t, int64(39900), got.PriceCents)
	assert.Equal(t, "RUB", got.Currency)
	assert.True(t, got.IsActive)

	events := productAuditEvents(t, svc, AuditEventProductCreated)
	require.Len(t, events, 1)
	assert.Equal(t, ActorAdmin, events[0].ActorType)
	assert.Equal(t, "0", *events[0].ActorID)
	assert.Contains(t, *events[0].NewState, `"price_cents":39900`)
}

func TestCreateProduct_RejectsDuplicatePlanDuration(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	require.NoError(t, svc.CreateProduct(ctx, validProduct(plan.ID)))

	err := svc.CreateProduct(ctx, validProduct(plan.ID))
	require.ErrorIs(t, err, ErrProductNameTaken)
}

func TestCreateProduct_RejectsInvalidDuration(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	for _, days := range []int{0, 3651} {
		product := validProduct(plan.ID)
		product.DurationDays = days

		err := svc.CreateProduct(ctx, product)
		require.ErrorIs(t, err, ErrProductInvalid, "duration_days=%d", days)
	}
}

func TestCreateProduct_RejectsUnknownPlan(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	product := validProduct(9999)
	err := svc.CreateProduct(ctx, product)
	require.ErrorIs(t, err, ErrProductInvalid)

	var fieldErr *ProductFieldError
	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, "plan_id", fieldErr.Field)
}

func TestUpdateProductAdmin_ChangesPriceAndAuditsOldPrice(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	product := validProduct(plan.ID)
	require.NoError(t, svc.CreateProduct(ctx, product))

	product.PriceCents = 49900
	require.NoError(t, svc.UpdateProductAdmin(ctx, product))

	got, err := svc.GetProductByID(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(49900), got.PriceCents)

	events := productAuditEvents(t, svc, AuditEventProductUpdated)
	require.Len(t, events, 1)
	assert.Contains(t, *events[0].OldState, `"price_cents":39900`)
	assert.Contains(t, *events[0].NewState, `"price_cents":49900`)
}

func TestUpdateProductAdmin_RejectsPlanChange(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")
	other := createProductTestPlan(t, svc, "starter")

	product := validProduct(plan.ID)
	require.NoError(t, svc.CreateProduct(ctx, product))

	product.PlanID = other.ID
	err := svc.UpdateProductAdmin(ctx, product)
	require.ErrorIs(t, err, ErrProductProtected)
}

func TestUpdateProductAdmin_RejectsDurationChangeWithActiveSubscriptions(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	product := validProduct(plan.ID)
	require.NoError(t, svc.CreateProduct(ctx, product))

	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 100, ClientID: "client-1", SubscriptionID: "sub-1",
		Status: string(SubscriptionStatusActive), PlanID: plan.ID, ProductID: &product.ID,
	}).Error)

	product.DurationDays = 60
	err := svc.UpdateProductAdmin(ctx, product)
	require.ErrorIs(t, err, ErrProductInUse)

	var inUse *ProductInUseError
	require.ErrorAs(t, err, &inUse)
	assert.Equal(t, int64(1), inUse.Details["subscriptions"])
}

func TestDeleteProduct_DeletesWithoutReferencesAndAudits(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	product := validProduct(plan.ID)
	require.NoError(t, svc.CreateProduct(ctx, product))

	require.NoError(t, svc.DeleteProduct(ctx, product.ID))

	_, err := svc.GetProductByID(ctx, product.ID)
	require.ErrorIs(t, err, ErrProductNotFound)

	events := productAuditEvents(t, svc, AuditEventProductDeleted)
	require.Len(t, events, 1)
	assert.Equal(t, ActorAdmin, events[0].ActorType)
	assert.Contains(t, *events[0].OldState, `"price_cents":39900`)
}

func TestDeleteProduct_RejectsWithSubscription(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	product := validProduct(plan.ID)
	require.NoError(t, svc.CreateProduct(ctx, product))

	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 200, ClientID: "client-2", SubscriptionID: "sub-2",
		Status: string(SubscriptionStatusActive), PlanID: plan.ID, ProductID: &product.ID,
	}).Error)

	err := svc.DeleteProduct(ctx, product.ID)
	require.ErrorIs(t, err, ErrProductInUse)

	var inUse *ProductInUseError
	require.ErrorAs(t, err, &inUse)
	assert.Equal(t, int64(1), inUse.Details["subscriptions"])
	assert.Equal(t, int64(0), inUse.Details["orders"])
}

func TestDeleteProduct_RejectsWithOrder(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	product := validProduct(plan.ID)
	require.NoError(t, svc.CreateProduct(ctx, product))

	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 300, ClientID: "client-3", SubscriptionID: "sub-3",
		Status: string(SubscriptionStatusActive), PlanID: plan.ID,
	}).Error)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: 1, ProductID: product.ID, Status: OrderStatusPending,
		AmountCents: 39900, Currency: "RUB",
	}).Error)

	err := svc.DeleteProduct(ctx, product.ID)
	require.ErrorIs(t, err, ErrProductInUse)

	var inUse *ProductInUseError
	require.ErrorAs(t, err, &inUse)
	assert.Equal(t, int64(1), inUse.Details["orders"])
}

func TestListProductOverviews_CountsInSingleRow(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	plan := createProductTestPlan(t, svc, "premium")

	product := validProduct(plan.ID)
	require.NoError(t, svc.CreateProduct(ctx, product))

	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 400, ClientID: "client-4", SubscriptionID: "sub-4",
		Status: string(SubscriptionStatusActive), PlanID: plan.ID, ProductID: &product.ID,
	}).Error)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: 1, ProductID: product.ID, Status: OrderStatusPending,
		AmountCents: 39900, Currency: "RUB",
	}).Error)

	rows, err := svc.ListProductOverviews(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	row := rows[0]
	assert.Equal(t, product.ID, row.ID)
	assert.Equal(t, plan.ID, row.PlanID)
	assert.Equal(t, plan.Name, row.PlanName)
	assert.Equal(t, int64(1), row.SubscriptionsCount)
	assert.Equal(t, int64(1), row.OrdersCount)
}

func TestErrors_AreDistinctSentinels(t *testing.T) {
	assert.False(t, errors.Is(ErrProductInvalid, ErrProductInUse))
	assert.False(t, errors.Is(ErrProductProtected, ErrProductInUse))
}
