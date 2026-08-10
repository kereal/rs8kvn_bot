package service

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePaymentProvider satisfies PaymentProvider for tests that never call CreateTransaction.
type fakePaymentProvider struct{}

func (fakePaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return &platega.CreateTransactionResponse{TransactionID: "fake", URL: "https://example.com"}, nil
}

func TestCalculateProductExpiry_SamePlanExtends(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &database.Product{PlanID: 1, DurationDays: 30}

	result := calculateProductExpiry(now, 1, &oldExpiry, product)
	assert.Equal(t, oldExpiry.AddDate(0, 0, 30), result)
}

func TestCalculateProductExpiry_NilExpiryUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	product := &database.Product{PlanID: 1, DurationDays: 30}

	result := calculateProductExpiry(now, 1, nil, product)
	assert.Equal(t, now.AddDate(0, 0, 30), result)
}

func TestCalculateProductExpiry_DifferentPlanUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &database.Product{PlanID: 2, DurationDays: 30}

	result := calculateProductExpiry(now, 1, &oldExpiry, product)
	assert.Equal(t, now.AddDate(0, 0, 30), result)
}

func TestCancelPaymentByProvider_PaidChargebackReturnsWasPaid(t *testing.T) {
	mock := &testutil.DatabaseService{
		CancelOrderCASFunc: func(ctx context.Context, provider, providerID string, from []database.OrderStatus) (bool, error) {
			assert.Equal(t, "platega", provider)
			assert.Equal(t, "pay-1", providerID)
			assert.Contains(t, from, database.OrderStatusPaid, "chargeback must allow transition from paid")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider, providerID string) (*database.Order, error) {
			return &database.Order{ID: 7, Status: database.OrderStatusCanceled, PaymentProvider: provider, ProviderPaymentID: providerID}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{})
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), "pay-1", "CHARGEBACKED")
	require.NoError(t, err)
	assert.True(t, wasPaid)
	require.NotNil(t, order)
	assert.Equal(t, uint(7), order.ID)
}

func TestCancelPaymentByProvider_PendingCancelNotChargeback(t *testing.T) {
	mock := &testutil.DatabaseService{
		CancelOrderCASFunc: func(ctx context.Context, provider, providerID string, from []database.OrderStatus) (bool, error) {
			assert.NotContains(t, from, database.OrderStatusPaid, "plain CANCELED must not touch paid orders")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider, providerID string) (*database.Order, error) {
			return &database.Order{ID: 8, Status: database.OrderStatusCanceled}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{})
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), "pay-2", "CANCELED")
	require.NoError(t, err)
	assert.False(t, wasPaid)
	require.NotNil(t, order)
}

func TestHandleChargeback_DowngradeToFreePlan(t *testing.T) {
	var updatedSub *database.Subscription
	mock := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 42, Status: "active", PlanID: 5}, nil
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: name}, nil
		},
		UpdateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			updatedSub = sub
			return nil
		},
		DeleteSubscriptionNodesBySubscriptionIDFunc: func(ctx context.Context, subID uint) error {
			return nil
		},
		GetNodesByPlanIDFunc: func(ctx context.Context, planID uint) ([]database.Node, error) {
			return nil, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	subSvc := SubscriptionService{db: mock}
	o := NewOrderService(mock, &subSvc, nil, fakePaymentProvider{}) // syncSvc nil — skip sync
	chatID, text, err := o.HandleChargeback(context.Background(), &database.Order{ID: 9, SubscriptionID: 11})
	require.NoError(t, err)
	assert.Equal(t, int64(42), chatID)
	assert.Contains(t, text, "бесплатный тариф")
	require.NotNil(t, updatedSub)
	assert.Equal(t, "active", updatedSub.Status, "subscription must be active on free plan after chargeback")
	assert.Equal(t, uint(1), updatedSub.PlanID, "subscription must be on free plan after chargeback")
}

func TestHandleChargeback_FallbackRevokedWhenNoSubSvc(t *testing.T) {
	var updatedSub *database.Subscription
	mock := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 42, Status: "active"}, nil
		},
		UpdateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			updatedSub = sub
			return nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}) // subSvc nil — fallback to revoked
	chatID, text, err := o.HandleChargeback(context.Background(), &database.Order{ID: 9, SubscriptionID: 11})
	require.NoError(t, err)
	assert.Equal(t, int64(42), chatID)
	assert.Contains(t, text, "chargeback")
	require.NotNil(t, updatedSub)
	assert.Equal(t, "revoked", updatedSub.Status, "subscription must be revoked on chargeback fallback")
}
