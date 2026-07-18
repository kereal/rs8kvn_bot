package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gorm.io/gorm"
)

func orderPtrTime(t time.Time) *time.Time {
	return &t
}

func TestOrderService_ActivateProduct_NilProduct(t *testing.T) {
	db := testutil.NewDatabaseService()
	subSvc := &SubscriptionService{db: db}
	svc := NewOrderService(db, subSvc, nil)

	_, err := svc.ActivateProduct(context.Background(), 123, nil)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "product is nil")
}

func TestOrderService_ActivateProduct_PaidProduct_NoPlanChange(t *testing.T) {
	ctx := context.Background()
	product := &database.Product{
		ID: 1, PlanID: 1, Name: "Pro", DurationDays: 30,
		PriceCents: 999, Currency: "RUB", IsActive: true,
	}
	sub := &database.Subscription{
		ID: 1, TelegramID: 123, SubscriptionID: "sub-1",
		PlanID: 1, Status: "active", Username: "user",
	}

	db := testutil.NewDatabaseService()
	db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}
	db.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}
	db.CreateOrderFunc = func(ctx context.Context, order *database.Order) error {
		return nil
	}
	db.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return []database.Node{}, nil
	}
	db.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{}, nil
	}

	subSvc := &SubscriptionService{db: db}
	svc := NewOrderService(db, subSvc, nil)
	order, err := svc.ActivateProduct(ctx, 123, product)

	require.NoError(t, err)
	assert.NotNil(t, order)
	assert.Equal(t, database.OrderStatusPending, order.Status)
	assert.Equal(t, product.PriceCents, order.AmountCents)
	assert.Equal(t, product.Currency, order.Currency)
	assert.Equal(t, product.ID, order.ProductID)
}

func TestOrderService_ActivateProduct_FreeProduct_SamePlan(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	product := &database.Product{
		ID: 1, PlanID: 1, Name: "Trial", DurationDays: 7,
		PriceCents: 0, Currency: "RUB", IsActive: true,
	}
	sub := &database.Subscription{
		ID: 1, TelegramID: 123, SubscriptionID: "sub-1",
		PlanID: 1, Status: "active", Username: "user",
		ExpiresAt: orderPtrTime(now.Add(30 * 24 * time.Hour)),
	}

	db := testutil.NewDatabaseService()
	db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}
	db.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}
	db.CreateOrderFunc = func(ctx context.Context, order *database.Order) error {
		return nil
	}
	db.UpdateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return nil
	}
	db.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return []database.Node{}, nil
	}
	db.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{}, nil
	}
	db.TransactionFunc = func(ctx context.Context, fn func(*gorm.DB) error) error {
		return nil
	}

	subSvc := &SubscriptionService{db: db}
	svc := NewOrderService(db, subSvc, nil)
	order, err := svc.ActivateProduct(ctx, 123, product)

	require.NoError(t, err)
	assert.NotNil(t, order)
	assert.Equal(t, database.OrderStatusPaid, order.Status)
	assert.Equal(t, int64(0), order.AmountCents)
	assert.NotNil(t, order.PaidAt)
	assert.NotNil(t, order.ActivatedAt)
	assert.NotNil(t, order.ExpiresAt)
	assert.Equal(t, now.AddDate(0, 0, product.DurationDays+30), *order.ExpiresAt)
}

func TestOrderService_ActivateProduct_FreeProduct_DifferentPlan_UpdatesSubscription(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	product := &database.Product{
		ID: 1, PlanID: 2, Name: "Pro", DurationDays: 30,
		PriceCents: 0, Currency: "RUB", IsActive: true,
	}
	sub := &database.Subscription{
		ID: 1, TelegramID: 123, SubscriptionID: "sub-1",
		PlanID: 1, Status: "active", Username: "user",
		ExpiresAt: orderPtrTime(now.Add(30 * 24 * time.Hour)),
	}

	db := testutil.NewDatabaseService()
	db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}
	db.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}
	db.CreateOrderFunc = func(ctx context.Context, order *database.Order) error {
		return nil
	}
	db.UpdateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return nil
	}
	db.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return []database.Node{}, nil
	}
	db.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{}, nil
	}
	db.MarkActiveNodesPendingUpdateFunc = func(ctx context.Context, subID uint, targetNodeIDs []uint) error {
		return nil
	}
	db.TransactionFunc = func(ctx context.Context, fn func(*gorm.DB) error) error {
		return nil
	}

	subSvc := &SubscriptionService{db: db}
	svc := NewOrderService(db, subSvc, nil)
	order, err := svc.ActivateProduct(ctx, 123, product)

	require.NoError(t, err)
	assert.NotNil(t, order)
	assert.Equal(t, database.OrderStatusPaid, order.Status)
	assert.Equal(t, uint(2), sub.PlanID)
	assert.Equal(t, uint(1), *sub.ProductID)
	assert.Equal(t, int64(0), sub.PricePaidCents)
	require.NotNil(t, sub.Currency)
	assert.Equal(t, "RUB", *sub.Currency)
	assert.NotNil(t, sub.StartedAt)
}

func TestOrderService_ActivateProduct_GetOrCreateSubscriptionError(t *testing.T) {
	db := testutil.NewDatabaseService()
	subSvc := &SubscriptionService{db: db}
	svc := NewOrderService(db, subSvc, nil)

	db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("db error")
	}

	_, err := svc.ActivateProduct(context.Background(), 123, &database.Product{ID: 1})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "get or create subscription")
}

func TestOrderService_ActivateProduct_CreateOrderError(t *testing.T) {
	ctx := context.Background()
	product := &database.Product{
		ID: 1, PlanID: 1, Name: "Pro", DurationDays: 30,
		PriceCents: 999, Currency: "RUB", IsActive: true,
	}
	sub := &database.Subscription{
		ID: 1, TelegramID: 123, SubscriptionID: "sub-1",
		PlanID: 1, Status: "active", Username: "user",
	}

	db := testutil.NewDatabaseService()
	db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}
	db.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}
	db.CreateOrderFunc = func(ctx context.Context, order *database.Order) error {
		return errors.New("order db error")
	}
	db.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return []database.Node{}, nil
	}
	db.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{}, nil
	}

	subSvc := &SubscriptionService{db: db}
	svc := NewOrderService(db, subSvc, nil)
	_, err := svc.ActivateProduct(ctx, 123, product)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "create order")
}

func TestOrderService_ActivateProduct_PaidProduct_ExpiryNotModified(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &database.Product{
		ID: 2, PlanID: 1, Name: "Pro Extend", DurationDays: 30,
		PriceCents: 999, Currency: "RUB", IsActive: true,
	}
	sub := &database.Subscription{
		ID: 1, TelegramID: 123, SubscriptionID: "sub-1",
		PlanID: 1, Status: "active", Username: "user",
		ExpiresAt: &oldExpiry,
	}

	db := testutil.NewDatabaseService()
	db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}
	db.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}
	db.CreateOrderFunc = func(ctx context.Context, order *database.Order) error {
		return nil
	}
	db.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return []database.Node{}, nil
	}
	db.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{}, nil
	}

	subSvc := &SubscriptionService{db: db}
	svc := NewOrderService(db, subSvc, nil)
	order, err := svc.ActivateProduct(ctx, 123, product)

	require.NoError(t, err)
	assert.NotNil(t, order)
	assert.Equal(t, database.OrderStatusPending, order.Status)
	assert.Equal(t, oldExpiry, *sub.ExpiresAt)
}

func TestOrderService_ActivateProduct_FreeProduct_SyncSetupFailureReturnsError(t *testing.T) {
	ctx := context.Background()
	product := &database.Product{
		ID: 1, PlanID: 2, Name: "Pro", DurationDays: 30,
		PriceCents: 0, Currency: "RUB", IsActive: true,
	}
	sub := &database.Subscription{
		ID: 1, TelegramID: 123, SubscriptionID: "sub-1",
		PlanID: 1, Status: "active", Username: "user",
	}

	transactionCalled := false
	db := testutil.NewDatabaseService()
	db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}
	db.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}
	db.TransactionFunc = func(ctx context.Context, fn func(*gorm.DB) error) error {
		transactionCalled = true
		return nil
	}
	db.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		if planID == 1 {
			return []database.Node{}, nil
		}
		return nil, errors.New("load nodes failed")
	}
	db.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{}, nil
	}

	subSvc := &SubscriptionService{db: db}
	syncSvc := NewSyncService(db, nil, nil)
	svc := NewOrderService(db, subSvc, syncSvc)

	order, err := svc.ActivateProduct(ctx, 123, product)

	require.ErrorContains(t, err, "activate product: apply plan: apply plan to subscription 1: load plan nodes")
	assert.True(t, transactionCalled)
	assert.NotNil(t, order)
	assert.Equal(t, database.OrderStatusPaid, order.Status)
	assert.Equal(t, uint(2), sub.PlanID)
	require.NotNil(t, sub.ProductID)
	assert.Equal(t, product.ID, *sub.ProductID)
}

func TestCalculateProductExpiry_SamePlanExtends(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &database.Product{PlanID: 1, DurationDays: 30}

	result := calculateProductExpiry(now, 1, &oldExpiry, product)
	expected := oldExpiry.AddDate(0, 0, 30)
	assert.Equal(t, expected, result)
}

func TestCalculateProductExpiry_NilExpiryUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	product := &database.Product{PlanID: 1, DurationDays: 30}

	result := calculateProductExpiry(now, 1, nil, product)
	expected := now.AddDate(0, 0, 30)
	assert.Equal(t, expected, result)
}

func TestCalculateProductExpiry_DifferentPlanUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &database.Product{PlanID: 2, DurationDays: 30}

	result := calculateProductExpiry(now, 1, &oldExpiry, product)
	expected := now.AddDate(0, 0, 30)
	assert.Equal(t, expected, result)
}
