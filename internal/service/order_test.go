package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// fakePaymentProvider satisfies PaymentProvider for tests that never call CreateTransaction.
type fakePaymentProvider struct{}

type errorPaymentProvider struct{ err error }

type responsePaymentProvider struct {
	response *platega.CreateTransactionResponse
	err      error
}

func (p responsePaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return p.response, p.err
}

func (p errorPaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return nil, p.err
}

func (fakePaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return &platega.CreateTransactionResponse{TransactionID: "550e8400-e29b-41d4-a716-446655440099", URL: "https://example.com", ExpiresIn: "00:15:00"}, nil
}

func atomicChargebackResult(orderID, subscriptionID uint, wasPaid, downgraded bool) (*database.ChargebackResult, error) {
	return &database.ChargebackResult{
		Order:          &database.Order{ID: orderID, SubscriptionID: subscriptionID, Status: database.OrderStatusCanceled, AmountCents: 2300, Currency: "RUB"},
		WasPaid:        wasPaid,
		Transitioned:   true,
		Downgraded:     downgraded,
		SubscriptionID: subscriptionID,
	}, nil
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

func TestOrderService_NotifiesAdminForProviderRejection(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	order := &database.Order{ID: 15, SubscriptionID: 6, ProductID: 10, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
	mock := &testutil.DatabaseService{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 10, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 6, TelegramID: 45}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
		SavePaymentDetailsFunc:           func(context.Context, uint, uuid.UUID, string, time.Time) error { return nil },
	}
	o := NewOrderService(mock, nil, nil, errorPaymentProvider{err: fmt.Errorf("%w: invalid request", platega.ErrBadRequest)}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	_, _, err := o.RequestPayment(context.Background(), 45, "user", &database.Product{ID: 10})
	require.Error(t, err)
	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "provider_create_rejected")
	assert.Contains(t, messages[0].Text, "Order ID: 15")
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

func TestRequestPayment_SavesRedirectPaymentDetails(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440106")
	order := &database.Order{ID: 16, SubscriptionID: 7, ProductID: 11, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
	var savedID uuid.UUID
	var savedURL string
	var savedExpiry time.Time
	mock := &testutil.DatabaseService{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 11, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 7, TelegramID: 46}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
		SavePaymentDetailsFunc: func(_ context.Context, orderID uint, id uuid.UUID, url string, expiry time.Time) error {
			assert.Equal(t, uint(16), orderID)
			savedID, savedURL, savedExpiry = id, url, expiry
			return nil
		},
	}
	provider := responsePaymentProvider{response: &platega.CreateTransactionResponse{
		TransactionID: providerID.String(), Redirect: "https://pay.example/redirect", ExpiresIn: "00:15:00",
	}}
	o := NewOrderService(mock, nil, nil, provider, "@testbot", &config.Config{TelegramAdminID: 999})

	info, gotOrder, err := o.RequestPayment(context.Background(), 46, "user", &database.Product{ID: 11})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Same(t, order, gotOrder)
	assert.Equal(t, providerID, info.PaymentID)
	assert.Equal(t, "https://pay.example/redirect", info.URL)
	assert.Equal(t, providerID, savedID)
	assert.Equal(t, info.URL, savedURL)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), savedExpiry, 2*time.Second)
}

func TestRequestPayment_InvalidProviderResponsesNotifyAdmin(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440107"
	tests := []struct {
		name      string
		response  *platega.CreateTransactionResponse
		wantErr   string
		wantEvent string
	}{
		{name: "empty response", wantErr: "empty response", wantEvent: "provider_empty_response"},
		{name: "invalid transaction id", response: &platega.CreateTransactionResponse{TransactionID: "not-a-uuid", URL: "https://pay.example", ExpiresIn: "00:15:00"}, wantErr: "transactionId must be UUID v4", wantEvent: "provider_invalid_transaction_id"},
		{name: "missing URL", response: &platega.CreateTransactionResponse{TransactionID: providerID, ExpiresIn: "00:15:00"}, wantErr: "response has no payment URL", wantEvent: "provider_incomplete_response"},
		{name: "invalid expiry", response: &platega.CreateTransactionResponse{TransactionID: providerID, URL: "https://pay.example", ExpiresIn: "forever"}, wantErr: "parse payment expiry", wantEvent: "provider_incomplete_response"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &database.Order{ID: 17, SubscriptionID: 8, ProductID: 12, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
			adminBot := testutil.NewBotAPI()
			mock := &testutil.DatabaseService{
				GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
					return &database.Product{ID: 12, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
				},
				GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
					return &database.Subscription{ID: 8, TelegramID: 47}, nil
				},
				FindPendingPaymentOrderFunc: func(context.Context, uint, uint, time.Time) (*database.Order, error) {
					return order, nil
				},
				GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
					return &database.Plan{ID: 2, IsActive: true}, nil
				},
				MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
			}
			o := NewOrderService(mock, nil, nil, responsePaymentProvider{response: tt.response}, "", &config.Config{TelegramAdminID: 999})
			o.SetAdminBot(adminBot)

			_, _, err := o.RequestPayment(context.Background(), 47, "user", &database.Product{ID: 12})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			messages := adminBot.GetAllSentMessages()
			require.Len(t, messages, 1)
			assert.Contains(t, messages[0].Text, tt.wantEvent)
		})
	}
}

func TestRequestPayment_LatePlanLoadFailureNotifiesAdmin(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	order := &database.Order{ID: 21, SubscriptionID: 12, ProductID: 16, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
	mock := &testutil.DatabaseService{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 16, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 12, TelegramID: 50}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return nil, errors.New("late plan database failure")
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	_, _, err := o.RequestPayment(context.Background(), 50, "user", &database.Product{ID: 16})
	require.Error(t, err)
	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "load_plan_failed")
	assert.Contains(t, messages[0].Text, "late plan database failure")
}

func TestRequestPayment_SaveDetailsFailureNotifiesAdmin(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440108")
	order := &database.Order{ID: 18, SubscriptionID: 9, ProductID: 13, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
	mock := &testutil.DatabaseService{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 13, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 9, TelegramID: 48}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
		SavePaymentDetailsFunc: func(context.Context, uint, uuid.UUID, string, time.Time) error {
			return errors.New("database unavailable")
		},
	}
	o := NewOrderService(mock, nil, nil, responsePaymentProvider{response: &platega.CreateTransactionResponse{TransactionID: providerID.String(), URL: "https://pay.example", ExpiresIn: "00:15:00"}}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	_, _, err := o.RequestPayment(context.Background(), 48, "user", &database.Product{ID: 13})
	require.Error(t, err)
	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "payment_details_save_failed")
	assert.Contains(t, messages[0].Text, "database unavailable")
}

func TestConfirmPayment_ReleasesPaymentLockBeforePostCommitSync(t *testing.T) {
	firstID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440120")
	secondID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440121")
	syncStarted := make(chan struct{})
	releaseSync := make(chan struct{})
	var syncStartOnce sync.Once

	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(_ context.Context, _ string, id uuid.UUID) (*database.Order, error) {
			if id == firstID {
				return &database.Order{ID: 31, SubscriptionID: 41, ProductID: 51, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
			}
			return nil, database.ErrOrderNotFound
		},
		GetProductByIDFunc: func(_ context.Context, id uint) (*database.Product, error) {
			return &database.Product{ID: id, PlanID: 61, DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 71, PlanID: 61}, nil
		},
		ConfirmOrderPaidCASFunc: func(_ context.Context, _ uint, _, _ time.Time, sub *database.Subscription, _ *database.Product, _ database.ApplyPlanInTxFn) (bool, error) {
			sub.ExpiresAt = testutil.PtrTime(time.Now().UTC().Truncate(time.Minute).AddDate(0, 0, 30))
			return true, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			syncStartOnce.Do(func() { close(syncStarted) })
			<-releaseSync
			return nil, nil
		},
	}
	orderService := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)

	firstDone := make(chan error, 1)
	go func() {
		_, err := orderService.ConfirmPayment(context.Background(), firstID, json.Number("23.00"), "RUB")
		firstDone <- err
	}()

	select {
	case <-syncStarted:
	case <-time.After(time.Second):
		t.Fatal("post-commit sync did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := orderService.ConfirmPayment(context.Background(), secondID, json.Number("23.00"), "RUB")
		secondDone <- err
	}()

	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("second payment callback remained blocked by post-commit sync")
	}

	close(releaseSync)
	require.NoError(t, <-firstDone)
}

func TestConfirmPayment_ActivatedCASWithNilExpiryDoesNotPanic(t *testing.T) {
	// The post-CAS code mirrors sub.ExpiresAt onto the order snapshot only when
	// the CAS populated it. A CAS/fake that activates without setting
	// sub.ExpiresAt must not panic and must leave order.ExpiresAt nil.
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440122")
	beforeAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("confirmed", "METRIC"))
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return &database.Order{ID: 32, SubscriptionID: 42, ProductID: 52, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "METRIC"}, nil
		},
		GetProductByIDFunc: func(_ context.Context, id uint) (*database.Product, error) {
			return &database.Product{ID: id, PlanID: 62, DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 72, PlanID: 62}, nil
		},
		ConfirmOrderPaidCASFunc: func(_ context.Context, _ uint, _, _ time.Time, _ *database.Subscription, _ *database.Product, _ database.ApplyPlanInTxFn) (bool, error) {
			// Intentionally leaves sub.ExpiresAt nil.
			return true, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, json.Number("23.00"), "METRIC")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)
	assert.Equal(t, database.OrderStatusPaid, confirmation.Order.Status)
	assert.Nil(t, confirmation.Order.ExpiresAt, "expiry must stay nil when CAS leaves sub.ExpiresAt nil")
	afterAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("confirmed", "METRIC"))
	assert.Equal(t, float64(2300), afterAmount-beforeAmount)
}

func TestConfirmPayment_RequiresSyncServiceForPendingOrder(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440105")
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return &database.Order{ID: 11, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", nil)
	_, err := o.ConfirmPayment(context.Background(), providerID, json.Number("23.00"), "RUB")
	require.ErrorIs(t, err, ErrPaymentSyncNotReady)
}

func TestConfirmPayment_ValidationAndDatabaseFailuresNotifyAdmin(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440109")
	tests := []struct {
		name       string
		currency   string
		amount     json.Number
		productErr error
		subErr     error
		casErr     error
		wantErr    error
		wantEvent  string
	}{
		{name: "currency mismatch", currency: "USD", amount: "23.00", wantErr: ErrCurrencyMismatch, wantEvent: "callback_currency_mismatch"},
		{name: "amount mismatch", currency: "RUB", amount: "24.00", wantErr: ErrAmountMismatch, wantEvent: "callback_amount_mismatch"},
		{name: "invalid amount", currency: "RUB", amount: "23.001", wantEvent: "callback_amount_invalid"},
		{name: "product load failure", currency: "RUB", amount: "23.00", productErr: errors.New("product database failed"), wantEvent: "load_order_product_failed"},
		{name: "subscription load failure", currency: "RUB", amount: "23.00", subErr: errors.New("subscription database failed"), wantEvent: "load_order_subscription_failed"},
		{name: "transaction failure", currency: "RUB", amount: "23.00", casErr: errors.New("transaction rolled back"), wantEvent: "confirm_payment_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminBot := testutil.NewBotAPI()
			mock := &testutil.DatabaseService{
				GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
					return &database.Order{ID: 19, SubscriptionID: 10, ProductID: 14, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
				},
				GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
					if tt.productErr != nil {
						return nil, tt.productErr
					}
					return &database.Product{ID: 14, PlanID: 2, Name: "Premium", DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
				},
				GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
					if tt.subErr != nil {
						return nil, tt.subErr
					}
					return &database.Subscription{ID: 10, TelegramID: 49, PlanID: 2}, nil
				},
				ConfirmOrderPaidCASFunc: func(context.Context, uint, time.Time, time.Time, *database.Subscription, *database.Product, database.ApplyPlanInTxFn) (bool, error) {
					return false, tt.casErr
				},
			}
			o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
			o.SetAdminBot(adminBot)

			_, err := o.ConfirmPayment(context.Background(), providerID, tt.amount, tt.currency)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.Error(t, err)
			}
			messages := adminBot.GetAllSentMessages()
			require.Len(t, messages, 1)
			assert.Contains(t, messages[0].Text, tt.wantEvent)
		})
	}
}

func TestConfirmPayment_NotifiesAdminOnActivation(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440125")
	sub := &database.Subscription{ID: 85, TelegramID: 55, Username: "alice", ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour))}
	product := &database.Product{ID: 66, PlanID: 2, Name: "Premium 1 месяц", PriceCents: 230000, Currency: "RUB"}
	order := &database.Order{ID: 96, SubscriptionID: 85, ProductID: 66, Status: database.OrderStatusPending, AmountCents: 230000, Currency: "RUB", ProviderPaymentID: providerID.String()}
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return order, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, uint, time.Time, time.Time, *database.Subscription, *database.Product, database.ApplyPlanInTxFn) (bool, error) {
			return true, nil
		},
	}
	adminBot := testutil.NewBotAPI()
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, json.Number("2300.00"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)

	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1, "activated payment must produce exactly one admin notification")
	msg := messages[0]
	assert.Equal(t, int64(999), msg.ChatID)
	assert.Contains(t, msg.Text, "Покупка подтверждена")
	assert.NotContains(t, msg.Text, "Продление")
	assert.Contains(t, msg.Text, "Premium 1 месяц")
	assert.Contains(t, msg.Text, "2 300 ₽")
	assert.Contains(t, msg.Text, "@alice")
	assert.Contains(t, msg.Text, "t.me/alice")
	assert.Contains(t, msg.Text, "Telegram ID: `55`")
	assert.Contains(t, msg.Text, "Заказ: #96")

	// The fake keeps the original Chattable; assert Markdown is used so the
	// user link renders clickable in Telegram.
	chattable := adminBot.LastChattableSafe()
	msgConfig, ok := chattable.(tgbotapi.MessageConfig)
	require.True(t, ok, "admin notification must be sent as a tgbotapi message config")
	assert.Equal(t, "Markdown", msgConfig.ParseMode)
}

func TestConfirmPayment_NotifiesAdminOnRenewal(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440130")
	productID := uint(67)
	sub := &database.Subscription{ID: 87, TelegramID: 56, Username: "bob", PlanID: 2, ProductID: &productID, PricePaidCents: 230000, ExpiresAt: testutil.PtrTime(time.Now().Add(10 * 24 * time.Hour))}
	product := &database.Product{ID: 67, PlanID: 2, Name: "Premium 1 месяц", PriceCents: 230000, Currency: "RUB"}
	order := &database.Order{ID: 97, SubscriptionID: 87, ProductID: 67, Status: database.OrderStatusPending, AmountCents: 230000, Currency: "RUB", ProviderPaymentID: providerID.String()}
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return order, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, uint, time.Time, time.Time, *database.Subscription, *database.Product, database.ApplyPlanInTxFn) (bool, error) {
			return true, nil
		},
	}
	adminBot := testutil.NewBotAPI()
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, json.Number("2300.00"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)

	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1, "renewed payment must produce exactly one admin notification")
	msg := messages[0]
	assert.Equal(t, int64(999), msg.ChatID)
	assert.Contains(t, msg.Text, "Продление подтверждено")
	assert.NotContains(t, msg.Text, "Покупка")
	assert.Contains(t, msg.Text, "@bob")
	assert.Contains(t, msg.Text, "Заказ: #97")
}

func TestFormatAdminChargebackAlert(t *testing.T) {
	tests := []struct {
		name       string
		sub        *database.Subscription
		order      *database.Order
		product    *database.Product
		downgraded bool
		want       []string
		notWant    []string
	}{
		{
			name:       "downgraded to free",
			sub:        &database.Subscription{ID: 90, TelegramID: 60, Username: "carol", PlanID: 99},
			order:      &database.Order{ID: 102, SubscriptionID: 90, ProductID: 71, AmountCents: 690000, Currency: "RUB", ProviderPaymentID: "550e8400-e29b-41d4-a716-446655440131"},
			product:    &database.Product{ID: 71, Name: "Premium 3 месяца", PriceCents: 690000, Currency: "RUB"},
			downgraded: true,
			want:       []string{"Chargeback по платежу", "Premium 3 месяца", "6 900 ₽", "@carol", "понижен до бесплатного"},
			notWant:    []string{"сохранён"},
		},
		{
			name:       "access preserved",
			sub:        &database.Subscription{ID: 91, TelegramID: 61, Username: "dave"},
			order:      &database.Order{ID: 103, SubscriptionID: 91, AmountCents: 2300, Currency: "RUB"},
			downgraded: false,
			want:       []string{"Chargeback по платежу", "23 ₽", "@dave", "сохранён"},
			notWant:    []string{"понижен"},
		},
		{
			name: "nil dependencies render placeholders",
			sub:  nil, order: nil, product: nil,
			downgraded: true,
			want:       []string{"Chargeback по платежу", "Тариф: *—*"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := formatAdminChargebackAlert(tt.sub, tt.order, tt.product, tt.downgraded)
			for _, want := range tt.want {
				assert.Contains(t, text, want)
			}
			for _, nw := range tt.notWant {
				assert.NotContains(t, text, nw)
			}
		})
	}
}

func TestCancelPaymentByProvider_ChargebackNotifiesAdmin(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440132")
	tests := []struct {
		name       string
		downgraded bool
		wantAccess string
	}{
		{name: "downgrades to free", downgraded: true, wantAccess: "понижен до бесплатного"},
		{name: "preserves access with another paid order", downgraded: false, wantAccess: "сохранён"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminBot := testutil.NewBotAPI()
			sub := &database.Subscription{ID: 92, TelegramID: 62, Username: "carol", PlanID: 99, Status: "active"}
			product := &database.Product{ID: 72, PlanID: 99, Name: "Premium 3 месяца", PriceCents: 690000, Currency: "RUB", IsActive: true}
			order := &database.Order{ID: 104, SubscriptionID: 92, ProductID: 72, Status: database.OrderStatusCanceled, AmountCents: 690000, Currency: "RUB", ProviderPaymentID: providerID.String()}
			mock := &testutil.DatabaseService{
				GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
					return &database.Order{ID: 104, SubscriptionID: 92, ProductID: 72, Status: database.OrderStatusPaid, AmountCents: 690000, Currency: "RUB", ProviderPaymentID: providerID.String()}, nil
				},
				CancelPaidOrderAndDowngradeCASFunc: func(context.Context, string, uuid.UUID, time.Time, uint, database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
					return &database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: tt.downgraded, SubscriptionID: 92}, nil
				},
				GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
					return sub, nil
				},
				GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
					return product, nil
				},
			}
			o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
			o.SetAdminBot(adminBot)

			_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("6900.00"), "RUB")
			require.NoError(t, err)
			require.True(t, wasPaid)

			messages := adminBot.GetAllSentMessages()
			require.Len(t, messages, 2, "chargeback on a paid order must produce the issue alert and the buyer alert")
			assert.Contains(t, messages[0].Text, "Chargeback requires manual review")
			cb := messages[1]
			assert.Equal(t, int64(999), cb.ChatID)
			assert.Contains(t, cb.Text, "Chargeback по платежу")
			assert.Contains(t, cb.Text, "Premium 3 месяца")
			assert.Contains(t, cb.Text, "6 900 ₽")
			assert.Contains(t, cb.Text, "@carol")
			assert.Contains(t, cb.Text, tt.wantAccess)
			assert.Contains(t, cb.Text, "Заказ: #104")

			chattable := adminBot.LastChattableSafe()
			msgConfig, ok := chattable.(tgbotapi.MessageConfig)
			require.True(t, ok, "chargeback notification must be sent as a tgbotapi message config")
			assert.Equal(t, "Markdown", msgConfig.ParseMode)
		})
	}
}

func TestFormatMoneyCents(t *testing.T) {
	tests := []struct {
		name        string
		amountCents int64
		currency    string
		want        string
	}{
		{name: "whole rubles", amountCents: 230000, currency: "RUB", want: "2 300 ₽"},
		{name: "fractional rubles", amountCents: 230050, currency: "RUB", want: "2 300,50 ₽"},
		{name: "zero", amountCents: 0, currency: "RUB", want: "0 ₽"},
		{name: "non RUB currency", amountCents: 100000, currency: "USD", want: "1 000 USD"},
		{name: "small amount", amountCents: 2300, currency: "RUB", want: "23 ₽"},
		{name: "empty currency", amountCents: 100, currency: "", want: "1"},
		{name: "negative", amountCents: -2300, currency: "RUB", want: "-23 ₽"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatMoneyCents(tt.amountCents, tt.currency))
		})
	}
}

func TestCancelPaymentByProvider_ErrorsNotifyAdmin(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440110")
	tests := []struct {
		name      string
		currency  string
		amount    json.Number
		casErr    error
		wantErr   error
		wantEvent string
	}{
		{name: "currency mismatch", currency: "USD", amount: "23.00", wantErr: ErrCurrencyMismatch, wantEvent: "callback_currency_mismatch"},
		{name: "amount mismatch", currency: "RUB", amount: "24.00", wantErr: ErrAmountMismatch, wantEvent: "callback_amount_mismatch"},
		{name: "invalid amount", currency: "RUB", amount: "23.001", wantEvent: "callback_amount_invalid"},
		{name: "cancel database failure", currency: "RUB", amount: "23.00", casErr: errors.New("cancel database failed"), wantEvent: "cancel_payment_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminBot := testutil.NewBotAPI()
			mock := &testutil.DatabaseService{
				GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
					return &database.Order{ID: 20, SubscriptionID: 11, ProductID: 15, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
				},
				CancelOrderCASFunc: func(context.Context, string, uuid.UUID, []database.OrderStatus) (bool, error) {
					return false, tt.casErr
				},
			}
			o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
			o.SetAdminBot(adminBot)

			_, _, err := o.CancelPaymentByProvider(context.Background(), providerID, "CANCELED", tt.amount, tt.currency)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.Error(t, err)
			}
			messages := adminBot.GetAllSentMessages()
			require.Len(t, messages, 1)
			assert.Contains(t, messages[0].Text, tt.wantEvent)
		})
	}
}

func TestCancelPaymentByProvider_PaidChargebackReturnsWasPaid(t *testing.T) {
	// The repository is consulted twice: before the CAS (current paid state) and
	// after the CAS (reloaded canceled state). The wasPaid flag must be derived
	// from the pre-transition state.
	beforeAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("chargeback", "RUB"))
	calls := 0
	mock := &testutil.DatabaseService{
		CancelOrderCASFunc: func(ctx context.Context, provider string, providerID uuid.UUID, from []database.OrderStatus) (bool, error) {
			assert.Equal(t, "platega", provider)
			assert.Equal(t, "550e8400-e29b-41d4-a716-446655440101", providerID.String())
			assert.Contains(t, from, database.OrderStatusPaid, "chargeback must allow transition from paid")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider string, providerID uuid.UUID) (*database.Order, error) {
			calls++
			return &database.Order{ID: 7, SubscriptionID: 70, Status: database.OrderStatusPaid, PaymentProvider: provider, ProviderPaymentID: providerID.String(), AmountCents: 2300, Currency: "RUB"}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(context.Context, string, uuid.UUID, time.Time, uint, database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			return atomicChargebackResult(7, 70, true, false)
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), uuid.MustParse("550e8400-e29b-41d4-a716-446655440101"), "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.True(t, wasPaid)
	require.NotNil(t, order)
	assert.Equal(t, uint(7), order.ID)
	assert.Equal(t, 1, calls, "atomic chargeback repository owns the transition and order reload")
	afterAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("chargeback", "RUB"))
	assert.Equal(t, float64(2300), afterAmount-beforeAmount)
}

func TestCancelPaymentByProvider_ChargebackOnPendingReportsNotPaid(t *testing.T) {
	// A chargeback on an order that never reached paid must NOT report wasPaid:
	// no money was collected yet, so nothing to refund.
	beforeAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("chargeback", "RUB"))
	calls := 0
	mock := &testutil.DatabaseService{
		CancelOrderCASFunc: func(ctx context.Context, provider string, providerID uuid.UUID, from []database.OrderStatus) (bool, error) {
			assert.Contains(t, from, database.OrderStatusPaid, "chargeback must allow transition from paid")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider string, providerID uuid.UUID) (*database.Order, error) {
			calls++
			return &database.Order{ID: 9, SubscriptionID: 90, Status: database.OrderStatusPending, PaymentProvider: provider, ProviderPaymentID: providerID.String(), AmountCents: 2300, Currency: "RUB"}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(context.Context, string, uuid.UUID, time.Time, uint, database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			return atomicChargebackResult(9, 90, false, false)
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), uuid.MustParse("550e8400-e29b-41d4-a716-446655440103"), "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.False(t, wasPaid, "pending order chargeback must not report wasPaid")
	require.NotNil(t, order)
	assert.Equal(t, uint(9), order.ID)
	afterAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("chargeback", "RUB"))
	assert.Equal(t, beforeAmount, afterAmount)
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
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", nil)
	_, _, err := o.CancelPaymentByProvider(context.Background(), providerID, "PAID", json.Number("23.00"), "RUB")
	require.ErrorIs(t, err, ErrInvalidPaymentTransition)
	assert.False(t, called)
}

func TestCancelPaymentByProvider_PendingCancelNotChargeback(t *testing.T) {
	calls := 0
	mock := &testutil.DatabaseService{
		CancelOrderCASFunc: func(ctx context.Context, provider string, providerID uuid.UUID, from []database.OrderStatus) (bool, error) {
			assert.NotContains(t, from, database.OrderStatusPaid, "plain CANCELED must not touch paid orders")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider string, providerID uuid.UUID) (*database.Order, error) {
			calls++
			status := database.OrderStatusCanceled
			if calls == 1 {
				status = database.OrderStatusPending
			}
			return &database.Order{ID: 8, Status: status, AmountCents: 2300, Currency: "RUB"}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", nil)
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), uuid.MustParse("550e8400-e29b-41d4-a716-446655440102"), "CANCELED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.False(t, wasPaid)
	require.NotNil(t, order)
}

func TestConfirmPayment_DeletedSubscriptionOrProductReturnsNoop(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440123")
	tests := []struct {
		name      string
		productFn func(context.Context, uint) (*database.Product, error)
		subFn     func(context.Context, uint) (*database.Subscription, error)
		wantEvent string
	}{
		{
			name: "deleted subscription",
			productFn: func(context.Context, uint) (*database.Product, error) {
				return &database.Product{ID: 60, PlanID: 2, Name: "Premium", DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
			},
			subFn: func(context.Context, uint) (*database.Subscription, error) {
				return nil, database.ErrSubscriptionNotFound
			},
			wantEvent: "load_order_subscription_failed",
		},
		{
			name: "deleted product",
			productFn: func(context.Context, uint) (*database.Product, error) {
				return nil, database.ErrProductNotFound
			},
			subFn: func(context.Context, uint) (*database.Subscription, error) {
				return &database.Subscription{ID: 70, TelegramID: 80, PlanID: 2}, nil
			},
			wantEvent: "load_order_product_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminBot := testutil.NewBotAPI()
			casCalled := false
			mock := &testutil.DatabaseService{
				GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
					return &database.Order{ID: 90, SubscriptionID: 70, ProductID: 60, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}, nil
				},
				GetProductByIDFunc: tt.productFn,
				GetByIDFunc:        tt.subFn,
				ConfirmOrderPaidCASFunc: func(context.Context, uint, time.Time, time.Time, *database.Subscription, *database.Product, database.ApplyPlanInTxFn) (bool, error) {
					casCalled = true
					return true, nil
				},
			}
			o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
			o.SetAdminBot(adminBot)

			confirmation, err := o.ConfirmPayment(context.Background(), providerID, json.Number("23.00"), "RUB")
			require.NoError(t, err, "deleted dependency must acknowledge the callback instead of 500")
			require.False(t, confirmation.Activated)
			assert.False(t, casCalled, "no CAS may run when the product/subscription is gone")
			messages := adminBot.GetAllSentMessages()
			require.Len(t, messages, 1)
			assert.Contains(t, messages[0].Text, tt.wantEvent)
		})
	}
}

func TestRequestPayment_ConcurrentClaimReturnsAlreadyInProgress(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	order := &database.Order{ID: 95, SubscriptionID: 75, ProductID: 65, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
	latest := &database.Order{ID: 95, SubscriptionID: 75, ProductID: 65, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", PaymentCreationUncertain: true}
	mock := &testutil.DatabaseService{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 65, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 75, TelegramID: 55}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) {
			// A concurrent RequestPayment already claimed the intent.
			return false, nil
		},
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
			return latest, nil
		},
	}
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	_, gotOrder, err := o.RequestPayment(context.Background(), 55, "user", &database.Product{ID: 65})
	require.ErrorIs(t, err, ErrPaymentAlreadyInProgress)
	require.Same(t, latest, gotOrder)
	require.Empty(t, adminBot.GetAllSentMessages(), "a concurrent claim must not raise a manual-reconciliation alert")
}

func TestCancelPaymentByProvider_ChargebackDowngradesPaidSubscription(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440124")
	sub := &database.Subscription{ID: 85, TelegramID: 55, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	calls := 0
	downgradePersisted := false
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			calls++
			return &database.Order{ID: 96, SubscriptionID: 85, ProductID: 66, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(context.Context, string, uuid.UUID, time.Time, uint, database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			return atomicChargebackResult(96, 85, true, true)
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		GetNodesByPlanIDFunc: func(context.Context, uint) ([]database.Node, error) {
			return nil, nil
		},
		UpdateSubscriptionFunc: func(_ context.Context, updated *database.Subscription) error {
			downgradePersisted = true
			assert.Equal(t, uint(2), updated.PlanID, "subscription must be downgraded to the free plan")
			assert.Nil(t, updated.ExpiresAt, "free plan has no expiry")
			assert.Nil(t, updated.ProductID)
			return nil
		},
	}
	subSvc := NewSubscriptionService(mock, nil, nil, nil, &config.Config{})
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)

	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.True(t, wasPaid)
	require.NotNil(t, order)
	assert.Equal(t, uint(96), order.ID)
	assert.False(t, downgradePersisted, "the atomic repository owns the subscription downgrade")
}

func TestCancelPaymentByProvider_ChargebackKeepsAccessWithAnotherPaidOrder(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440125")
	sub := &database.Subscription{ID: 86, TelegramID: 56, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	calls := 0
	updateCalled := false
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			calls++
			return &database.Order{ID: 97, SubscriptionID: 86, ProductID: 67, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(context.Context, string, uuid.UUID, time.Time, uint, database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			return atomicChargebackResult(97, 86, true, false)
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		GetNodesByPlanIDFunc: func(context.Context, uint) ([]database.Node, error) {
			return nil, nil
		},
		UpdateSubscriptionFunc: func(context.Context, *database.Subscription) error {
			updateCalled = true
			return nil
		},
	}
	subSvc := NewSubscriptionService(mock, nil, nil, nil, &config.Config{})
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)

	_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.True(t, wasPaid)
	assert.False(t, updateCalled, "access must be preserved when another paid order exists")
	assert.Equal(t, uint(99), sub.PlanID, "subscription plan must stay untouched")
}

func TestCancelPaymentByProvider_ChargebackDowngradeFailureAlertsAdmin(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440127")
	adminBot := testutil.NewBotAPI()
	sub := &database.Subscription{ID: 88, TelegramID: 58, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	calls := 0
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			calls++
			return &database.Order{ID: 100, SubscriptionID: 88, ProductID: 69, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(context.Context, string, uuid.UUID, time.Time, uint, database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			return nil, errors.New("atomic chargeback transaction failed")
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		// Free-plan node load fails, so DowngradeToFreePlan errors out.
		GetNodesByPlanIDFunc: func(context.Context, uint) ([]database.Node, error) {
			return nil, errors.New("free plan node lookup failed")
		},
	}
	subSvc := NewSubscriptionService(mock, nil, nil, nil, &config.Config{})
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.Error(t, err, "an atomic chargeback failure must be returned for provider retry/reconciliation")
	require.False(t, wasPaid)
	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1, "atomic chargeback failure alert")
	assert.Contains(t, messages[0].Text, "cancel_payment_failed")
	assert.Contains(t, messages[0].Text, "atomic chargeback transaction failed")
}

func TestCancelPaymentByProvider_ChargebackDowngradeUsesSyncService(t *testing.T) {
	// With the sync service wired, the downgrade must deprovision premium nodes
	// via ApplyPlanToSubscription (pending_remove) + SyncSubscription instead of
	// the fallback that deletes bindings without touching the panels.
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440128")
	sub := &database.Subscription{ID: 89, TelegramID: 59, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	calls := 0
	updateCalled := false
	deleteNodesCalled := false
	syncInvoked := false
	premiumMarkedForRemoval := false
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			calls++
			return &database.Order{ID: 101, SubscriptionID: 89, ProductID: 70, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(context.Context, string, uuid.UUID, time.Time, uint, database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			return atomicChargebackResult(101, 89, true, true)
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		UpdateSubscriptionFunc: func(_ context.Context, updated *database.Subscription) error {
			updateCalled = true
			assert.Equal(t, uint(2), updated.PlanID)
			return nil
		},
		DeleteSubscriptionNodesBySubscriptionIDFunc: func(context.Context, uint) error {
			deleteNodesCalled = true
			return nil
		},
		// Sync path dependencies: the free plan has no nodes, but the subscription
		// still holds an active premium binding that must become pending_remove.
		GetNodesByPlanIDFunc: func(context.Context, uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{{SubscriptionID: 89, NodeID: 555, Status: database.SyncStatusActive}}, nil
		},
		UpdateSubscriptionNodeStatusFunc: func(_ context.Context, subID, nodeID uint, status database.SyncStatus) error {
			assert.Equal(t, uint(89), subID)
			assert.Equal(t, uint(555), nodeID)
			assert.Equal(t, database.SyncStatusPendingRemove, status, "premium node must be marked pending_remove for physical deprovision")
			premiumMarkedForRemoval = true
			return nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			syncInvoked = true
			return nil, nil
		},
	}
	subSvc := NewSubscriptionService(mock, nil, nil, nil, &config.Config{})
	subSvc.SetSyncService(NewSyncService(mock, nil, nil))
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)

	_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.True(t, wasPaid)
	assert.True(t, wasPaid)
	assert.True(t, syncInvoked, "post-commit SyncSubscription must run after the atomic downgrade")
	assert.False(t, deleteNodesCalled, "the atomic path must not use the legacy node wipe")
	assert.False(t, updateCalled, "the atomic repository owns subscription downgrade inside its transaction")
	assert.False(t, premiumMarkedForRemoval, "the fake atomic seam does not re-run plan application")
}

func TestCancelPaymentByProvider_ChargebackOnPendingDoesNotDowngrade(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440126")
	calls := 0
	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			calls++
			return &database.Order{ID: 99, SubscriptionID: 87, ProductID: 68, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(context.Context, string, uuid.UUID, time.Time, uint, database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			return atomicChargebackResult(99, 87, false, false)
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 87, TelegramID: 57, PlanID: 99}, nil
		},
	}
	subSvc := NewSubscriptionService(mock, nil, nil, nil, &config.Config{})
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)

	_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.False(t, wasPaid, "chargeback on pending order collected no money")
}
