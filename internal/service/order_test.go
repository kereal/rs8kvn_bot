package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePaymentProvider satisfies PaymentProvider for tests that never call CreateTransaction.
type fakePaymentProvider struct{}

type errorPaymentProvider struct{ err error }

func (p errorPaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return nil, p.err
}

func (fakePaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return &platega.CreateTransactionResponse{TransactionID: "550e8400-e29b-41d4-a716-446655440099", URL: "https://example.com", ExpiresIn: "00:15:00"}, nil
}

func TestOrderService_NotifiesAdminForUncertainPayment(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	order := &database.Order{ID: 12, SubscriptionID: 3, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", PaymentCreationUncertain: true}
	mock := &testutil.DatabaseService{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 3, TelegramID: 42}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, time.Time) (*database.Order, error) {
			return order, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	_, gotOrder, err := o.RequestPayment(context.Background(), 42, "user", &database.Product{ID: 7})
	require.ErrorIs(t, err, ErrPaymentCreationUncertain)
	require.Same(t, order, gotOrder)
	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, int64(999), messages[0].ChatID)
	assert.Contains(t, messages[0].Text, "Order ID: 12")
	assert.Contains(t, messages[0].Text, "Telegram ID: 42")
	assert.Contains(t, messages[0].Text, "Product ID: 7")
}

func TestOrderService_NotifiesAdminForLateConfirmedPayment(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440103")
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return &database.Order{ID: 13, SubscriptionID: 4, ProductID: 8, Status: database.OrderStatusExpired, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 43}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.False(t, confirmation.Activated)
	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Late confirmed payment")
	assert.Contains(t, messages[0].Text, providerID.String())
	assert.Contains(t, messages[0].Text, "Telegram ID: 43")
}

func TestOrderService_NotifiesAdminWhenProviderOutcomeIsUncertain(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	providerErr := errors.New("provider timeout")
	order := &database.Order{ID: 14, SubscriptionID: 5, ProductID: 9, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
	mock := &testutil.DatabaseService{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 9, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 5, TelegramID: 44}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
	}
	o := NewOrderService(mock, nil, nil, errorPaymentProvider{err: providerErr}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	_, _, err := o.RequestPayment(context.Background(), 44, "user", &database.Product{ID: 9})
	require.Error(t, err)
	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "outcome is uncertain")
	assert.Contains(t, messages[0].Text, "Order ID: 14")
	assert.Contains(t, messages[0].Text, "provider timeout")
}

func TestConfirmPayment_RequiresSyncServiceForPendingOrder(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440105")
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return &database.Order{ID: 11, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{})
	_, err := o.ConfirmPayment(context.Background(), providerID, json.Number("23.00"), "RUB")
	require.ErrorIs(t, err, ErrPaymentSyncNotReady)
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
		CancelOrderCASFunc: func(ctx context.Context, provider string, providerID uuid.UUID, from []database.OrderStatus) (bool, error) {
			assert.Equal(t, "platega", provider)
			assert.Equal(t, "550e8400-e29b-41d4-a716-446655440101", providerID.String())
			assert.Contains(t, from, database.OrderStatusPaid, "chargeback must allow transition from paid")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider string, providerID uuid.UUID) (*database.Order, error) {
			return &database.Order{ID: 7, Status: database.OrderStatusCanceled, PaymentProvider: provider, ProviderPaymentID: providerID.String(), AmountCents: 2300, Currency: "RUB"}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{})
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), uuid.MustParse("550e8400-e29b-41d4-a716-446655440101"), "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.True(t, wasPaid)
	require.NotNil(t, order)
	assert.Equal(t, uint(7), order.ID)
}

func TestCancelPaymentByProvider_RejectsUnknownStatus(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440104")
	called := false
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return &database.Order{ID: 10, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
		},
		CancelOrderCASFunc: func(context.Context, string, uuid.UUID, []database.OrderStatus) (bool, error) {
			called = true
			return true, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{})
	_, _, err := o.CancelPaymentByProvider(context.Background(), providerID, "PAID", json.Number("23.00"), "RUB")
	require.ErrorIs(t, err, ErrInvalidPaymentTransition)
	assert.False(t, called)
}

func TestCancelPaymentByProvider_PendingCancelNotChargeback(t *testing.T) {
	mock := &testutil.DatabaseService{
		CancelOrderCASFunc: func(ctx context.Context, provider string, providerID uuid.UUID, from []database.OrderStatus) (bool, error) {
			assert.NotContains(t, from, database.OrderStatusPaid, "plain CANCELED must not touch paid orders")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider string, providerID uuid.UUID) (*database.Order, error) {
			return &database.Order{ID: 8, Status: database.OrderStatusCanceled, AmountCents: 2300, Currency: "RUB"}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{})
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), uuid.MustParse("550e8400-e29b-41d4-a716-446655440102"), "CANCELED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.False(t, wasPaid)
	require.NotNil(t, order)
}
