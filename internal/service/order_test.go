package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// funcPaymentProvider is the single payment-provider fake of order_test.go.
// A nil field falls back to the no-op behavior the old single-purpose fakes
// had: CreateTransaction returns a well-formed response, GetTransactionStatus
// reports "status not configured" so the best-effort fee fetch stays out of
// the way. Configure createFunc/statusFunc to script failures or statuses.
type funcPaymentProvider struct {
	createFunc func(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error)
	statusFunc func(context.Context, string) (*platega.TransactionStatusResponse, error)
}

func (p *funcPaymentProvider) CreateTransaction(ctx context.Context, req platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	if p.createFunc == nil {
		return &platega.CreateTransactionResponse{TransactionID: "550e8400-e29b-41d4-a716-446655440099", URL: "https://example.com", ExpiresIn: "00:15:00"}, nil
	}

	return p.createFunc(ctx, req)
}

func (p *funcPaymentProvider) GetTransactionStatus(ctx context.Context, id string) (*platega.TransactionStatusResponse, error) {
	if p.statusFunc == nil {
		return nil, errors.New("status not configured")
	}

	return p.statusFunc(ctx, id)
}

// chargebackAuditFn is the audit-log callback the service passes into
// CancelPaidOrderAndDowngradeCAS.
type chargebackAuditFn = func(*database.Order, *database.Subscription, bool) *database.AuditLog

// casChargebackFn is the CancelPaidOrderAndDowngradeCAS seam signature.
type casChargebackFn = func(context.Context, database.CancelPaidOrderParams) (*database.ChargebackResult, error)

// atomicChargebackResult builds the result the real atomic chargeback
// repository would return: the order reloaded as canceled.
func atomicChargebackResult(orderID, subscriptionID uint, wasPaid, downgraded bool) *database.ChargebackResult {
	return &database.ChargebackResult{
		Order:        &database.Order{ID: orderID, SubscriptionID: subscriptionID, Status: database.OrderStatusCanceled, AmountCents: 2300, Currency: "RUB"},
		WasPaid:      wasPaid,
		Transitioned: true,
		Downgraded:   downgraded,
	}
}

// casChargeback builds a CancelPaidOrderAndDowngradeCASFunc stub that always
// answers with result/err without performing any state transition. onCall,
// when set, receives the provider ids and the audit callback so tests can
// capture what the service passed in.
func casChargeback(result *database.ChargebackResult, err error, onCall ...func(p database.CancelPaidOrderParams)) casChargebackFn {
	return func(_ context.Context, p database.CancelPaidOrderParams) (*database.ChargebackResult, error) {
		for _, fn := range onCall {
			fn(p)
		}

		return result, err
	}
}

func TestOrderService_NotifiesAdminForUncertainPayment(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	order := &database.Order{ID: 12, SubscriptionID: 3, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", PaymentCreationUncertain: true}
	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 3, TelegramID: 42}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			return order, nil
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
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
	providerID := "550e8400-e29b-41d4-a716-446655440103"
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			expiresAt := time.Now().UTC().Add(-6 * time.Minute)

			return &database.Order{ID: 13, SubscriptionID: 4, ProductID: 8, Status: database.OrderStatusExpired, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID, PaymentExpiresAt: &expiresAt}, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 43}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
	o.SetAdminBot(adminBot)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.False(t, confirmation.Activated)

	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Late confirmed payment")
	assert.Contains(t, messages[0].Text, providerID)
	assert.Contains(t, messages[0].Text, "Telegram ID: 43")
}

func TestOrderService_ActivatesExpiredPaymentWithinSettlementGrace(t *testing.T) {
	t.Parallel()

	providerID := "550e8400-e29b-41d4-a716-446655440107"
	expiresAt := time.Now().UTC().Add(-4 * time.Minute)
	order := &database.Order{
		ID:                17,
		SubscriptionID:    8,
		ProductID:         12,
		Status:            database.OrderStatusExpired,
		AmountCents:       2300,
		Currency:          "RUB",
		ProviderPaymentID: providerID,
		PaymentExpiresAt:  &expiresAt,
	}
	product := &database.Product{ID: 12, PlanID: 2, Name: "Premium", DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}
	sub := &database.Subscription{ID: 8, TelegramID: 44, PlanID: 1, Status: "active"}
	casCalls := 0

	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return order, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			casCalls++
			order.Status = database.OrderStatusPaid

			return true, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated, "a callback arriving four minutes after the provider deadline is inside the settlement grace period")
	assert.Equal(t, database.OrderStatusPaid, order.Status)
	assert.Equal(t, 1, casCalls)
}

func TestOrderService_NotifiesAdminForProviderRejection(t *testing.T) {
	adminBot := testutil.NewBotAPI()
	order := &database.Order{ID: 15, SubscriptionID: 6, ProductID: 10, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 10, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 6, TelegramID: 45}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
		SavePaymentDetailsFunc:           func(context.Context, uint, string, string, time.Time) error { return nil },
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{createFunc: func(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
		return nil, fmt.Errorf("%w: invalid request", platega.ErrBadRequest)
	}}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
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
	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 9, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 5, TelegramID: 44}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{createFunc: func(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
		return nil, providerErr
	}}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
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
	providerID := "550e8400-e29b-41d4-a716-446655440106"
	order := &database.Order{ID: 16, SubscriptionID: 7, ProductID: 11, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}

	var (
		savedID     string
		savedURL    string
		savedExpiry time.Time
	)

	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 11, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 7, TelegramID: 46}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
		SavePaymentDetailsFunc: func(_ context.Context, orderID uint, id string, url string, expiry time.Time) error {
			assert.Equal(t, uint(16), orderID)

			savedID, savedURL, savedExpiry = id, url, expiry

			return nil
		},
	}
	provider := &funcPaymentProvider{createFunc: func(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
		return &platega.CreateTransactionResponse{
			TransactionID: providerID, Redirect: "https://pay.example/redirect", ExpiresIn: "00:15:00",
		}, nil
	}}
	o := NewOrderService(mock, nil, nil, provider, "@testbot", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})

	info, gotOrder, err := o.RequestPayment(context.Background(), 46, "user", &database.Product{ID: 11})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Same(t, order, gotOrder)
	assert.Equal(t, "https://pay.example/redirect", info.URL)
	assert.Equal(t, providerID, savedID)
	assert.Equal(t, info.URL, savedURL)
	assert.Equal(t, info.ExpiresAt, savedExpiry, "the same computed expiry handed to PaymentInfo must be persisted")
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
			mock := &testutil.ServiceStoreFake{
				GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
					return &database.Product{ID: 12, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
				},
				GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
					return &database.Subscription{ID: 8, TelegramID: 47}, nil
				},
				FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
					return order, nil
				},
				GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
					return &database.Plan{ID: 2, IsActive: true}, nil
				},
				MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
			}
			o := NewOrderService(mock, nil, nil, &funcPaymentProvider{createFunc: func(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
				return tt.response, nil
			}}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
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
	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 16, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 12, TelegramID: 50}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return nil, errors.New("late plan database failure")
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
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
	providerID := "550e8400-e29b-41d4-a716-446655440108"
	order := &database.Order{ID: 18, SubscriptionID: 9, ProductID: 13, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}
	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 13, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 9, TelegramID: 48}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			return order, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		MarkPaymentCreationUncertainFunc: func(context.Context, uint, bool) (bool, error) { return true, nil },
		SavePaymentDetailsFunc: func(context.Context, uint, string, string, time.Time) error {
			return errors.New("database unavailable")
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{createFunc: func(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
		return &platega.CreateTransactionResponse{TransactionID: providerID, URL: "https://pay.example", ExpiresIn: "00:15:00"}, nil
	}}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
	o.SetAdminBot(adminBot)

	_, _, err := o.RequestPayment(context.Background(), 48, "user", &database.Product{ID: 13})
	require.Error(t, err)

	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "payment_details_save_failed")
	assert.Contains(t, messages[0].Text, "database unavailable")
}

func TestConfirmPayment_ReleasesPaymentLockBeforePostCommitSync(t *testing.T) {
	firstID := "550e8400-e29b-41d4-a716-446655440120"
	secondID := "550e8400-e29b-41d4-a716-446655440121"
	syncStarted := make(chan struct{})
	releaseSync := make(chan struct{})

	var syncStartOnce sync.Once

	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(_ context.Context, _ string, id string) (*database.Order, error) {
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
		ConfirmOrderPaidCASFunc: func(_ context.Context, p database.ConfirmPaidOrderParams) (bool, error) {
			p.Subscription.ExpiresAt = testutil.PtrTime(time.Now().UTC().Truncate(time.Minute).AddDate(0, 0, 30))
			return true, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			syncStartOnce.Do(func() { close(syncStarted) })
			<-releaseSync

			return nil, nil
		},
	}
	orderService := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	firstDone := make(chan error, 1)

	go func() {
		_, err := orderService.ConfirmPayment(context.Background(), firstID, "", json.Number("23.00"), "RUB")
		firstDone <- err
	}()

	select {
	case <-syncStarted:
	case <-time.After(time.Second):
		t.Fatal("post-commit sync did not start")
	}

	secondDone := make(chan error, 1)

	go func() {
		_, err := orderService.ConfirmPayment(context.Background(), secondID, "", json.Number("23.00"), "RUB")
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

// TestRequestPayment_ReusesLivePaymentLinkWithoutRecreating pins the double-
// charge regression guard: an order with a saved provider payment ID, URL and
// a still-valid expiry returns the stored link as-is — CreateTransaction must
// not be called again (a second provider transaction would bill the user
// twice for one intent).
func TestRequestPayment_ReusesLivePaymentLinkWithoutRecreating(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440127"
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	order := &database.Order{
		ID: 18, SubscriptionID: 9, ProductID: 13, Status: database.OrderStatusPending,
		AmountCents: 2300, Currency: "RUB",
		ProviderPaymentID: providerID,
		PaymentURL:        "https://pay.example/live",
		PaymentExpiresAt:  &expiresAt,
	}

	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 13, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 9, TelegramID: 48}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			return order, nil
		},
	}
	createCalls := 0
	provider := &funcPaymentProvider{createFunc: func(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
		createCalls++

		return nil, errors.New("create transaction must not be called")
	}}
	o := NewOrderService(mock, nil, nil, provider, "@testbot", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})

	info, gotOrder, err := o.RequestPayment(context.Background(), 48, "user", &database.Product{ID: 13})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Same(t, order, gotOrder)

	assert.Equal(t, "https://pay.example/live", info.URL, "the stored live link must be returned as-is")
	assert.Equal(t, expiresAt, info.ExpiresAt)
	assert.Zero(t, createCalls, "a live link must never trigger a second provider transaction")
}

// TestConfirmPayment_PostCommitSyncFailureStillActivates pins the two-phase
// guard: the DB CAS activation is the durable outcome; a failing post-commit
// SyncSubscription is logged best-effort and must not turn the successful
// callback into an error (Platega would otherwise retry a settled payment).
func TestConfirmPayment_PostCommitSyncFailureStillActivates(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440128"
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return &database.Order{ID: 34, SubscriptionID: 44, ProductID: 54, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
		},
		GetProductByIDFunc: func(_ context.Context, id uint) (*database.Product, error) {
			return &database.Product{ID: id, PlanID: 64, DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 72, PlanID: 64}, nil
		},
		ConfirmOrderPaidCASFunc: func(_ context.Context, p database.ConfirmPaidOrderParams) (bool, error) {
			p.Subscription.ExpiresAt = testutil.PtrTime(time.Now().UTC().AddDate(0, 0, 30))

			return true, nil
		},
		// The post-commit sync fails on the node queue load.
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, errors.New("database unavailable")
		},
	}
	orderService := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	confirmation, err := orderService.ConfirmPayment(context.Background(), providerID, "", json.Number("23.00"), "RUB")

	require.NoError(t, err, "post-commit sync failure must not fail the settled callback")
	require.NotNil(t, confirmation)
	assert.True(t, confirmation.Activated, "the durable CAS activation must surface as Activated")
	require.NotNil(t, confirmation.Order)
	assert.Equal(t, database.OrderStatusPaid, confirmation.Order.Status)
}

func TestConfirmPayment_ActivatedCASWithNilExpiryDoesNotPanic(t *testing.T) {
	// The post-CAS code mirrors sub.ExpiresAt onto the order snapshot only when
	// the CAS populated it. A CAS/fake that activates without setting
	// sub.ExpiresAt must not panic and must leave order.ExpiresAt nil.
	providerID := "550e8400-e29b-41d4-a716-446655440122"
	beforeAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("confirmed", "METRIC"))
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return &database.Order{ID: 32, SubscriptionID: 42, ProductID: 52, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "METRIC"}, nil
		},
		GetProductByIDFunc: func(_ context.Context, id uint) (*database.Product, error) {
			return &database.Product{ID: id, PlanID: 62, DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 72, PlanID: 62}, nil
		},
		ConfirmOrderPaidCASFunc: func(_ context.Context, _ database.ConfirmPaidOrderParams) (bool, error) {
			// Intentionally leaves sub.ExpiresAt nil.
			return true, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("23.00"), "METRIC")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)
	assert.Equal(t, database.OrderStatusPaid, confirmation.Order.Status)
	assert.Nil(t, confirmation.Order.ExpiresAt, "expiry must stay nil when CAS leaves sub.ExpiresAt nil")

	afterAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("confirmed", "METRIC"))
	assert.Equal(t, float64(2300), afterAmount-beforeAmount)
}

func TestConfirmPayment_RequiresSyncServiceForPendingOrder(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440105"
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return &database.Order{ID: 11, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", nil)
	_, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("23.00"), "RUB")
	require.ErrorIs(t, err, ErrPaymentSyncNotReady)
}

func TestConfirmPayment_ValidationAndDatabaseFailuresNotifyAdmin(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440109"
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
		{name: "amount mismatch", currency: "RUB", amount: "22.00", wantErr: ErrAmountMismatch, wantEvent: "callback_amount_mismatch"},
		{name: "invalid amount", currency: "RUB", amount: "23.001", wantEvent: "callback_amount_invalid"},
		{name: "product load failure", currency: "RUB", amount: "23.00", productErr: errors.New("product database failed"), wantEvent: "load_order_product_failed"},
		{name: "subscription load failure", currency: "RUB", amount: "23.00", subErr: errors.New("subscription database failed"), wantEvent: "load_order_subscription_failed"},
		{name: "transaction failure", currency: "RUB", amount: "23.00", casErr: errors.New("transaction rolled back"), wantEvent: "confirm_payment_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminBot := testutil.NewBotAPI()
			mock := &testutil.ServiceStoreFake{
				GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
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
				ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
					return false, tt.casErr
				},
			}
			o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
			o.SetAdminBot(adminBot)

			_, err := o.ConfirmPayment(context.Background(), providerID, "", tt.amount, tt.currency)
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
	beforeRenewals := promtestutil.ToFloat64(metrics.SubscriptionRenewalsTotal)
	providerID := "550e8400-e29b-41d4-a716-446655440125"
	sub := &database.Subscription{ID: 85, TelegramID: 55, Username: "alice", ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour))}
	product := &database.Product{ID: 66, PlanID: 2, Name: "Premium 1 месяц", PriceCents: 230000, Currency: "RUB"}
	order := &database.Order{ID: 96, SubscriptionID: 85, ProductID: 66, Status: database.OrderStatusPending, AmountCents: 230000, Currency: "RUB", ProviderPaymentID: providerID}
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return order, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			return true, nil
		},
	}
	adminBot := testutil.NewBotAPI()
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
	o.SetAdminBot(adminBot)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("2300.00"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)
	assert.Equal(t, float64(0), promtestutil.ToFloat64(metrics.SubscriptionRenewalsTotal)-beforeRenewals, "first activation must not be counted as a renewal")

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
	beforeRenewals := promtestutil.ToFloat64(metrics.SubscriptionRenewalsTotal)
	providerID := "550e8400-e29b-41d4-a716-446655440130"
	productID := uint(67)
	sub := &database.Subscription{ID: 87, TelegramID: 56, Username: "bob", PlanID: 2, ProductID: &productID, PricePaidCents: 230000, ExpiresAt: testutil.PtrTime(time.Now().Add(10 * 24 * time.Hour))}
	product := &database.Product{ID: 67, PlanID: 2, Name: "Premium 1 месяц", PriceCents: 230000, Currency: "RUB"}
	order := &database.Order{ID: 97, SubscriptionID: 87, ProductID: 67, Status: database.OrderStatusPending, AmountCents: 230000, Currency: "RUB", ProviderPaymentID: providerID}
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return order, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			return true, nil
		},
	}
	adminBot := testutil.NewBotAPI()
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
	o.SetAdminBot(adminBot)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("2300.00"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)
	assert.Equal(t, float64(1), promtestutil.ToFloat64(metrics.SubscriptionRenewalsTotal)-beforeRenewals)

	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1, "renewed payment must produce exactly one admin notification")
	msg := messages[0]
	assert.Equal(t, int64(999), msg.ChatID)
	assert.Contains(t, msg.Text, "Продление подтверждено")
	assert.NotContains(t, msg.Text, "Покупка")
	assert.Contains(t, msg.Text, "@bob")
	assert.Contains(t, msg.Text, "Заказ: #97")
}

func TestConfirmPayment_InvalidatesSubserverCacheOnActivation(t *testing.T) {
	t.Parallel()

	providerID := "550e8400-e29b-41d4-a716-446655440131"
	sub := &database.Subscription{ID: 95, TelegramID: 57, Username: "carol", SubscriptionID: "sub-abc", ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour))}
	product := &database.Product{ID: 68, PlanID: 2, Name: "Premium 1 месяц", PriceCents: 230000, Currency: "RUB"}
	order := &database.Order{ID: 98, SubscriptionID: 95, ProductID: 68, Status: database.OrderStatusPending, AmountCents: 230000, Currency: "RUB", ProviderPaymentID: providerID}
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return order, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			return true, nil
		},
	}

	var invalidated []string

	subSvc := NewSubscriptionService(mock, nil, nil, nil)
	subSvc.SetInvalidateBySubIDFunc(func(subID string) { invalidated = append(invalidated, subID) })

	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("2300.00"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)
	assert.Equal(t, []string{"sub-abc"}, invalidated,
		"activated payment must invalidate the subserver response cache (stale profile-title/traffic limit)")
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
	providerID := "550e8400-e29b-41d4-a716-446655440132"

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
			order := &database.Order{ID: 104, SubscriptionID: 92, ProductID: 72, Status: database.OrderStatusCanceled, AmountCents: 690000, Currency: "RUB", ProviderPaymentID: providerID}
			mock := &testutil.ServiceStoreFake{
				GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
					return &database.Order{ID: 104, SubscriptionID: 92, ProductID: 72, Status: database.OrderStatusPaid, AmountCents: 690000, Currency: "RUB", ProviderPaymentID: providerID}, nil
				},
				CancelPaidOrderAndDowngradeCASFunc: casChargeback(&database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: tt.downgraded}, nil),
				GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
					return sub, nil
				},
				GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
					return product, nil
				},
			}
			o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
			o.SetAdminBot(adminBot)

			_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("6900.00"), "RUB")
			require.NoError(t, err)
			require.True(t, wasPaid)

			messages := adminBot.GetAllSentMessages()
			require.Len(t, messages, 1, "chargeback on a paid order must produce a single buyer alert")
			cb := messages[0]
			assert.Equal(t, int64(999), cb.ChatID)
			assert.Contains(t, cb.Text, "Chargeback по платежу")
			assert.Contains(t, cb.Text, "Premium 3 месяца")
			assert.Contains(t, cb.Text, "6 900 ₽")
			assert.Contains(t, cb.Text, "@carol")
			assert.Contains(t, cb.Text, tt.wantAccess)
			assert.Contains(t, cb.Text, "Заказ: #104")
			assert.NotContains(t, cb.Text, "manual review", "deduplicated chargeback path must not reuse the integration-issue header")

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
		{name: "whole stars", amountCents: 199, currency: "XTR", want: "199 XTR"},
		{name: "thousands stars", amountCents: 230050, currency: "XTR", want: "230 050 XTR"},
		{name: "negative stars", amountCents: -2300, currency: "XTR", want: "-2 300 XTR"},
		{name: "zero stars", amountCents: 0, currency: "XTR", want: "0 XTR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatMoneyCents(tt.amountCents, tt.currency))
		})
	}
}

func TestCancelPaymentByProvider_ErrorsNotifyAdmin(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440110"
	tests := []struct {
		name      string
		currency  string
		amount    json.Number
		casErr    error
		wantErr   error
		wantEvent string
	}{
		{name: "currency mismatch", currency: "USD", amount: "23.00", wantErr: ErrCurrencyMismatch, wantEvent: "callback_currency_mismatch"},
		{name: "amount mismatch", currency: "RUB", amount: "22.00", wantErr: ErrAmountMismatch, wantEvent: "callback_amount_mismatch"},
		{name: "invalid amount", currency: "RUB", amount: "23.001", wantEvent: "callback_amount_invalid"},
		{name: "cancel database failure", currency: "RUB", amount: "23.00", casErr: errors.New("cancel database failed"), wantEvent: "cancel_payment_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminBot := testutil.NewBotAPI()
			mock := &testutil.ServiceStoreFake{
				GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
					return &database.Order{ID: 20, SubscriptionID: 11, ProductID: 15, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
				},
				CancelOrderCASFunc: func(context.Context, string, string, []database.OrderStatus) (bool, error) {
					return false, tt.casErr
				},
			}
			o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
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
	mock := &testutil.ServiceStoreFake{
		CancelOrderCASFunc: func(ctx context.Context, provider string, providerID string, from []database.OrderStatus) (bool, error) {
			assert.Equal(t, "platega", provider)
			assert.Equal(t, "550e8400-e29b-41d4-a716-446655440101", providerID)
			assert.Contains(t, from, database.OrderStatusPaid, "chargeback must allow transition from paid")

			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider string, providerID string) (*database.Order, error) {
			calls++
			return &database.Order{ID: 7, SubscriptionID: 70, Status: database.OrderStatusPaid, PaymentProvider: provider, ProviderPaymentID: providerID, AmountCents: 2300, Currency: "RUB"}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(atomicChargebackResult(7, 70, true, false), nil),
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), "550e8400-e29b-41d4-a716-446655440101", "CHARGEBACKED", json.Number("23.00"), "RUB")
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
	mock := &testutil.ServiceStoreFake{
		CancelOrderCASFunc: func(ctx context.Context, provider string, providerID string, from []database.OrderStatus) (bool, error) {
			assert.Contains(t, from, database.OrderStatusPaid, "chargeback must allow transition from paid")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider string, providerID string) (*database.Order, error) {
			calls++
			return &database.Order{ID: 9, SubscriptionID: 90, Status: database.OrderStatusPending, PaymentProvider: provider, ProviderPaymentID: providerID, AmountCents: 2300, Currency: "RUB"}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(atomicChargebackResult(9, 90, false, false), nil),
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), "550e8400-e29b-41d4-a716-446655440103", "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.False(t, wasPaid, "pending order chargeback must not report wasPaid")
	require.NotNil(t, order)
	assert.Equal(t, uint(9), order.ID)

	afterAmount := promtestutil.ToFloat64(metrics.PaymentAmountCentsTotal.WithLabelValues("chargeback", "RUB"))
	assert.Equal(t, beforeAmount, afterAmount)
}

func TestCancelPaymentByProvider_RejectsUnknownStatus(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440104"
	called := false
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return &database.Order{ID: 10, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
		},
		CancelOrderCASFunc: func(context.Context, string, string, []database.OrderStatus) (bool, error) {
			called = true
			return true, nil
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", nil)
	_, _, err := o.CancelPaymentByProvider(context.Background(), providerID, "PAID", json.Number("23.00"), "RUB")
	require.ErrorIs(t, err, ErrInvalidPaymentTransition)
	assert.False(t, called)
}

func TestCancelPaymentByProvider_PendingCancelNotChargeback(t *testing.T) {
	calls := 0
	mock := &testutil.ServiceStoreFake{
		CancelOrderCASFunc: func(ctx context.Context, provider string, providerID string, from []database.OrderStatus) (bool, error) {
			assert.NotContains(t, from, database.OrderStatusPaid, "plain CANCELED must not touch paid orders")
			return true, nil
		},
		GetOrderByProviderPaymentIDFunc: func(ctx context.Context, provider string, providerID string) (*database.Order, error) {
			calls++

			status := database.OrderStatusCanceled
			if calls == 1 {
				status = database.OrderStatusPending
			}

			return &database.Order{ID: 8, Status: status, AmountCents: 2300, Currency: "RUB"}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", nil)
	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), "550e8400-e29b-41d4-a716-446655440102", "CANCELED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.False(t, wasPaid)
	require.NotNil(t, order)
}

func TestConfirmPayment_DeletedSubscriptionOrProductReturnsNoop(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440123"
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
			mock := &testutil.ServiceStoreFake{
				GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
					return &database.Order{ID: 90, SubscriptionID: 70, ProductID: 60, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID}, nil
				},
				GetProductByIDFunc: tt.productFn,
				GetByIDFunc:        tt.subFn,
				ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
					casCalled = true
					return true, nil
				},
			}
			o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
			o.SetAdminBot(adminBot)

			confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("23.00"), "RUB")
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
	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 65, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 75, TelegramID: 55}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
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
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
	o.SetAdminBot(adminBot)

	_, gotOrder, err := o.RequestPayment(context.Background(), 55, "user", &database.Product{ID: 65})
	require.ErrorIs(t, err, ErrPaymentAlreadyInProgress)
	require.Same(t, latest, gotOrder)
	require.Empty(t, adminBot.GetAllSentMessages(), "a concurrent claim must not raise a manual-reconciliation alert")
}

func TestCancelPaymentByProvider_ChargebackDowngradesPaidSubscription(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440124"
	sub := &database.Subscription{ID: 85, TelegramID: 55, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	calls := 0
	downgradePersisted := false
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			calls++
			return &database.Order{ID: 96, SubscriptionID: 85, ProductID: 66, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(atomicChargebackResult(96, 85, true, true), nil),
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
	subSvc := NewSubscriptionService(mock, nil, nil, &config.Config{})
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	order, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.True(t, wasPaid)
	require.NotNil(t, order)
	assert.Equal(t, uint(96), order.ID)
	assert.False(t, downgradePersisted, "the atomic repository owns the subscription downgrade")
}

func TestCancelPaymentByProvider_ChargebackKeepsAccessWithAnotherPaidOrder(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440125"
	sub := &database.Subscription{ID: 86, TelegramID: 56, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	calls := 0
	updateCalled := false
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			calls++
			return &database.Order{ID: 97, SubscriptionID: 86, ProductID: 67, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(atomicChargebackResult(97, 86, true, false), nil),
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
	subSvc := NewSubscriptionService(mock, nil, nil, &config.Config{})
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.True(t, wasPaid)
	assert.False(t, updateCalled, "access must be preserved when another paid order exists")
	assert.Equal(t, uint(99), sub.PlanID, "subscription plan must stay untouched")
}

func TestCancelPaymentByProvider_ChargebackDowngradeFailureAlertsAdmin(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440127"
	adminBot := testutil.NewBotAPI()
	sub := &database.Subscription{ID: 88, TelegramID: 58, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	calls := 0
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			calls++
			return &database.Order{ID: 100, SubscriptionID: 88, ProductID: 69, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(nil, errors.New("atomic chargeback transaction failed")),
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		// Free-plan node load fails, so DowngradeToFreePlan errors out.
		GetNodesByPlanIDFunc: func(context.Context, uint) ([]database.Node, error) {
			return nil, errors.New("free plan node lookup failed")
		},
	}
	subSvc := NewSubscriptionService(mock, nil, nil, &config.Config{})
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", &config.Config{TelegramAdminID: 999, PaymentEnabled: true})
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
	providerID := "550e8400-e29b-41d4-a716-446655440128"
	sub := &database.Subscription{ID: 89, TelegramID: 59, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	calls := 0
	updateCalled := false
	deleteNodesCalled := false
	syncInvoked := false
	premiumMarkedForRemoval := false
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			calls++
			return &database.Order{ID: 101, SubscriptionID: 89, ProductID: 70, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(atomicChargebackResult(101, 89, true, true), nil),
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
	subSvc := NewSubscriptionService(mock, nil, nil, &config.Config{})
	subSvc.SetSyncService(NewSyncService(mock, nil, nil))
	o := NewOrderService(mock, subSvc, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.True(t, wasPaid)
	assert.True(t, wasPaid)
	assert.True(t, syncInvoked, "post-commit SyncSubscription must run after the atomic downgrade")
	assert.False(t, deleteNodesCalled, "the atomic path must not use the legacy node wipe")
	assert.False(t, updateCalled, "the atomic repository owns subscription downgrade inside its transaction")
	assert.False(t, premiumMarkedForRemoval, "the fake atomic seam does not re-run plan application")
}

type paymentAmountsSave struct {
	orderID       uint
	callbackCents int64
	feeCents      *int64
}

func TestConfirmPayment_PersistsCallbackAmountAndProviderFee(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440127"
	var casAmount int64 = -1
	var saves []paymentAmountsSave

	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return &database.Order{ID: 33, SubscriptionID: 43, ProductID: 53, Status: database.OrderStatusPending, AmountCents: 5000, Currency: "RUB"}, nil
		},
		GetProductByIDFunc: func(_ context.Context, id uint) (*database.Product, error) {
			return &database.Product{ID: id, PlanID: 63, DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}, nil
		},
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 73, PlanID: 63}, nil
		},
		ConfirmOrderPaidCASFunc: func(_ context.Context, p database.ConfirmPaidOrderParams) (bool, error) {
			casAmount = p.CallbackAmountCents
			p.Subscription.ExpiresAt = testutil.PtrTime(time.Now().UTC().Truncate(time.Minute).AddDate(0, 0, 30))
			return true, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
		GetPaidOrdersWithoutProviderFeeAfterIDFunc: func(_ context.Context, afterID uint, _ int) ([]database.Order, error) {
			if afterID >= 33 {
				return nil, nil
			}

			return []database.Order{{ID: 33, AmountCents: 5000, CallbackAmountCents: testutil.PtrInt64(5250), PaymentProvider: database.PaymentProviderPlatega, ProviderPaymentID: providerID}}, nil
		},
		SaveOrderPaymentAmountsFunc: func(_ context.Context, orderID uint, callbackCents int64, feeCents *int64) error {
			saves = append(saves, paymentAmountsSave{orderID: orderID, callbackCents: callbackCents, feeCents: feeCents})
			return nil
		},
	}
	provider := &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		return &platega.TransactionStatusResponse{Status: "CONFIRMED", Commission: json.Number("4.5")}, nil
	}}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), provider, "", nil)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("52.50"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)

	assert.Equal(t, int64(5250), casAmount, "callback amount must be persisted atomically inside the paid CAS")
	assert.Empty(t, saves, "the callback must not wait for the provider fee lookup")

	require.NoError(t, o.SyncProviderFees(context.Background(), 10))
	require.Len(t, saves, 1, "the background fee retry must persist the commission")
	assert.Equal(t, uint(33), saves[0].orderID)
	assert.Equal(t, int64(5250), saves[0].callbackCents)
	require.NotNil(t, saves[0].feeCents)
	assert.Equal(t, int64(450), *saves[0].feeCents, "4.5 RUB commission must be stored as 450 cents")
}

func TestConfirmPayment_ProviderFeeUnavailableKeepsCallbackAmount(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440128"
	var casAmount int64 = -1
	var saves []paymentAmountsSave

	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return &database.Order{ID: 34, SubscriptionID: 44, ProductID: 54, Status: database.OrderStatusPending, AmountCents: 5000, Currency: "RUB"}, nil
		},
		GetProductByIDFunc: func(_ context.Context, id uint) (*database.Product, error) {
			return &database.Product{ID: id, PlanID: 64, DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}, nil
		},
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 74, PlanID: 64}, nil
		},
		ConfirmOrderPaidCASFunc: func(_ context.Context, p database.ConfirmPaidOrderParams) (bool, error) {
			casAmount = p.CallbackAmountCents
			p.Subscription.ExpiresAt = testutil.PtrTime(time.Now().UTC().Truncate(time.Minute).AddDate(0, 0, 30))
			return true, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
		SaveOrderPaymentAmountsFunc: func(_ context.Context, orderID uint, callbackCents int64, feeCents *int64) error {
			saves = append(saves, paymentAmountsSave{orderID: orderID, callbackCents: callbackCents, feeCents: feeCents})
			return nil
		},
	}
	provider := &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		return nil, errors.New("transaction API unreachable")
	}}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), provider, "", nil)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("52.50"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated)

	assert.Equal(t, int64(5250), casAmount, "callback amount must be persisted even when the fee API is unavailable")
	assert.Empty(t, saves, "no provider fee is written when the transaction API fails")
}

func TestSyncProviderFees_NotFoundIsTerminal(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440129"
	var saves []paymentAmountsSave

	mock := &testutil.ServiceStoreFake{
		GetPaidOrdersWithoutProviderFeeAfterIDFunc: func(_ context.Context, afterID uint, _ int) ([]database.Order, error) {
			if afterID >= 35 {
				return nil, nil
			}

			return []database.Order{{ID: 35, AmountCents: 5000, CallbackAmountCents: testutil.PtrInt64(5250), PaymentProvider: database.PaymentProviderPlatega, ProviderPaymentID: providerID}}, nil
		},
		SaveOrderPaymentAmountsFunc: func(_ context.Context, orderID uint, callbackCents int64, feeCents *int64) error {
			saves = append(saves, paymentAmountsSave{orderID: orderID, callbackCents: callbackCents, feeCents: feeCents})
			return nil
		},
	}
	provider := &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		return nil, platega.ErrTransactionNotFound
	}}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), provider, "", nil)

	require.NoError(t, o.SyncProviderFees(context.Background(), 10))
	require.Len(t, saves, 1, "a purged transaction must clear the retry marker instead of retrying forever")
	assert.Equal(t, uint(35), saves[0].orderID)
	assert.Equal(t, int64(5250), saves[0].callbackCents)
	require.NotNil(t, saves[0].feeCents)
	assert.Zero(t, *saves[0].feeCents, "fee is recorded as unavailable (0) so the order leaves the retry set")
}

func TestSyncProviderFees_NotConfirmedDeferred(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440130"
	var saves []paymentAmountsSave

	mock := &testutil.ServiceStoreFake{
		GetPaidOrdersWithoutProviderFeeAfterIDFunc: func(_ context.Context, afterID uint, _ int) ([]database.Order, error) {
			if afterID >= 36 {
				return nil, nil
			}

			return []database.Order{{ID: 36, AmountCents: 5000, CallbackAmountCents: testutil.PtrInt64(5250), PaymentProvider: database.PaymentProviderPlatega, ProviderPaymentID: providerID}}, nil
		},
		SaveOrderPaymentAmountsFunc: func(_ context.Context, orderID uint, callbackCents int64, feeCents *int64) error {
			saves = append(saves, paymentAmountsSave{orderID: orderID, callbackCents: callbackCents, feeCents: feeCents})
			return nil
		},
	}
	provider := &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		return &platega.TransactionStatusResponse{Status: "PENDING", Commission: json.Number("4.5")}, nil
	}}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), provider, "", nil)

	require.NoError(t, o.SyncProviderFees(context.Background(), 10))
	assert.Empty(t, saves, "a non-confirmed transaction must keep the NULL fee retry marker")
}

func TestSyncProviderFees_ContinuesPastDeferredFirstPage(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440131"
	orders := make([]database.Order, 0, 51)
	for id := uint(1); id <= 51; id++ {
		providerPaymentID := fmt.Sprintf("invalid-%d", id)
		if id == 51 {
			providerPaymentID = providerID
		}
		orders = append(orders, database.Order{ID: id, AmountCents: 5000, PaymentProvider: database.PaymentProviderPlatega, ProviderPaymentID: providerPaymentID})
	}

	var saves []paymentAmountsSave
	mock := &testutil.ServiceStoreFake{
		GetPaidOrdersWithoutProviderFeeAfterIDFunc: func(_ context.Context, afterID uint, limit int) ([]database.Order, error) {
			page := make([]database.Order, 0, limit)
			for _, order := range orders {
				if order.ID <= afterID {
					continue
				}
				page = append(page, order)
				if len(page) == limit {
					break
				}
			}
			return page, nil
		},
		SaveOrderPaymentAmountsFunc: func(_ context.Context, orderID uint, callbackCents int64, feeCents *int64) error {
			saves = append(saves, paymentAmountsSave{orderID: orderID, callbackCents: callbackCents, feeCents: feeCents})
			return nil
		},
	}
	provider := &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		return &platega.TransactionStatusResponse{Status: "CONFIRMED", Commission: json.Number("4.5")}, nil
	}}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), provider, "", nil)

	err := o.SyncProviderFees(context.Background(), 50)
	require.Error(t, err, "invalid IDs remain retryable and should be reported")
	require.Len(t, saves, 1, "the second page must be processed despite failures on the first page")
	assert.Equal(t, uint(51), saves[0].orderID)
}

// Тест поведения: не-Platega заказ (Stars) не имеет комиссии и не должен
// попадать в Platega API — скип не считается ошибкой синхронизации.
func TestSyncProviderFees_SkipsNonPlategaOrders(t *testing.T) {
	var saves []paymentAmountsSave

	mock := &testutil.ServiceStoreFake{
		GetPaidOrdersWithoutProviderFeeAfterIDFunc: func(_ context.Context, afterID uint, _ int) ([]database.Order, error) {
			if afterID > 0 {
				return nil, nil
			}

			return []database.Order{{ID: 37, AmountCents: 500, CallbackAmountCents: testutil.PtrInt64(500), PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "stars-invoice-37"}}, nil
		},
		SaveOrderPaymentAmountsFunc: func(_ context.Context, orderID uint, callbackCents int64, feeCents *int64) error {
			saves = append(saves, paymentAmountsSave{orderID: orderID, callbackCents: callbackCents, feeCents: feeCents})
			return nil
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		t.Fatal("провайдер не должен вызываться для не-Platega заказа")

		return nil, nil
	}}, "", nil)

	require.NoError(t, o.SyncProviderFees(context.Background(), 10), "skip не-Platega заказа не должен возвращать ошибку")
	assert.Empty(t, saves, "Stars-заказ не должен получать комиссию Platega")
}

func TestFormatPaymentIssue_TruncatesUntrustedFields(t *testing.T) {
	message := formatPaymentIssue(PaymentIssue{Event: "event", Reason: strings.Repeat("x", 5000), Payload: strings.Repeat("y", 5000)})

	assert.LessOrEqual(t, len(message), 5000)
	assert.Contains(t, message, "[truncated]")
}

func TestBuildPaidUserNotification_MissingSubscriptionServiceReturnsError(t *testing.T) {
	providerID := "550e8400-e29b-41d4-a716-446655440211"
	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, TelegramID: 42}, nil
		},
	}
	o := NewOrderService(db, nil, nil, &funcPaymentProvider{}, "", &config.Config{})

	_, _, err := o.BuildPaidUserNotification(context.Background(), &database.Order{ID: 1, SubscriptionID: 1, ProviderPaymentID: providerID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscription service is not configured")
}

// Тест поведения: callback, пришедший в окно между CreateTransaction и
// SavePaymentDetails, находится по payload (= order.ID), а id транзакции
// привязывается CAS — иначе деньги списаны, а активации нет.
func TestOrderService_ConfirmPayment_FindsOrderByPayloadWhenProviderIDLost(t *testing.T) {
	t.Parallel()

	providerID := "550e8400-e29b-41d4-a716-446655440118"
	order := &database.Order{
		ID:             33,
		SubscriptionID: 9,
		ProductID:      14,
		Status:         database.OrderStatusPending,
		AmountCents:    2300,
		Currency:       "RUB",
	}
	product := &database.Product{ID: 14, PlanID: 2, Name: "Premium", DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}
	sub := &database.Subscription{ID: 9, TelegramID: 47, PlanID: 1, Status: "active"}
	attached := 0

	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return nil, database.ErrOrderNotFound
		},
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
			return order, nil
		},
		AttachProviderPaymentIDFunc: func(_ context.Context, orderID uint, provider string, id string) (bool, error) {
			attached++
			assert.Equal(t, order.ID, orderID)
			assert.Equal(t, "platega", provider)
			assert.Equal(t, providerID, id)
			order.ProviderPaymentID = id

			return true, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			order.Status = database.OrderStatusPaid

			return true, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "33", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	require.True(t, confirmation.Activated, "платёж должен активироваться по payload")
	assert.Equal(t, 1, attached)
	assert.Equal(t, database.OrderStatusPaid, order.Status)
}

// Тест поведения: заказ с уже привязанной другой транзакцией не перехватывается
// по устаревшему payload.
func TestOrderService_ConfirmPayment_DoesNotHijackOrderWithOtherProviderID(t *testing.T) {
	t.Parallel()

	providerID := "550e8400-e29b-41d4-a716-446655440119"
	otherID := "550e8400-e29b-41d4-a716-446655440120"
	order := &database.Order{ID: 34, SubscriptionID: 9, ProductID: 14, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: otherID}

	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return nil, database.ErrOrderNotFound
		},
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
			return order, nil
		},
		AttachProviderPaymentIDFunc: func(context.Context, uint, string, string) (bool, error) {
			t.Fatal("нельзя привязывать вторую транзакцию к заказу")

			return false, nil
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", nil)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "34", json.Number("23.00"), "RUB")
	require.NoError(t, err)
	assert.False(t, confirmation.Activated)
}

// Тест поведения: Stars-заказ нельзя сверять с Platega API, но истекает
// локально, когда срок invoice истёк — иначе заказ висел бы pending вечно.
func TestOrderService_ReconcilePendingOrders_ExpiresStarsPendingOrder(t *testing.T) {
	t.Parallel()

	order := &database.Order{
		ID: 44, SubscriptionID: 13, ProductID: 18, Status: database.OrderStatusPending,
		AmountCents: 2300, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars,
		ProviderPaymentID: "stars-invoice-44", PaymentExpiresAt: testutil.PtrTime(time.Now().UTC().Add(-24 * time.Hour)),
	}
	expired := 0

	mock := &testutil.ServiceStoreFake{
		GetStalePendingOrdersAfterIDFunc: func(_ context.Context, _ time.Time, afterID uint, _ int) ([]database.Order, error) {
			if order.ID <= afterID {
				return nil, nil
			}

			return []database.Order{*order}, nil
		},
		ExpirePendingOrderFunc: func(_ context.Context, orderID uint) (bool, error) {
			expired++
			assert.Equal(t, order.ID, orderID)
			order.Status = database.OrderStatusExpired

			return true, nil
		},
	}

	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		t.Fatal("провайдер не должен вызываться для Stars-заказа")

		return nil, nil
	}}, "", nil)

	require.NoError(t, o.ReconcilePendingOrders(context.Background(), 10))
	assert.Equal(t, 1, expired)
	assert.Equal(t, database.OrderStatusExpired, order.Status, "Stars-заказ истекает локально")
}

func TestOrderService_ReconcilePendingOrders_ConfirmsPaidOrder(t *testing.T) {
	t.Parallel()

	providerID := "550e8400-e29b-41d4-a716-446655440121"
	order := &database.Order{
		ID: 41, SubscriptionID: 10, ProductID: 15, Status: database.OrderStatusPending,
		AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID,
	}
	product := &database.Product{ID: 15, PlanID: 2, Name: "Premium", DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}
	sub := &database.Subscription{ID: 10, TelegramID: 51, PlanID: 1, Status: "active"}

	mock := &testutil.ServiceStoreFake{
		GetStalePendingOrdersAfterIDFunc: func(_ context.Context, _ time.Time, afterID uint, _ int) ([]database.Order, error) {
			if order.Status != database.OrderStatusPending || order.ID <= afterID {
				return nil, nil
			}

			return []database.Order{*order}, nil
		},
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return order, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			order.Status = database.OrderStatusPaid

			return true, nil
		},
	}

	provider := &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		return &platega.TransactionStatusResponse{
			ID:     providerID,
			Status: "CONFIRMED",
			PaymentDetails: struct {
				Amount   json.Number `json:"amount"`
				Currency string      `json:"currency"`
			}{Amount: json.Number("23.00"), Currency: "RUB"},
		}, nil
	}}

	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), provider, "", nil)

	require.NoError(t, o.ReconcilePendingOrders(context.Background(), 10))
	assert.Equal(t, database.OrderStatusPaid, order.Status, "оплаченный заказ должен активироваться сверкой")
}

// Тест поведения: провайдер ответил терминальным отказом — заказ истекает, а не
// висит вечно.
func TestOrderService_ReconcilePendingOrders_ExpiresCanceledOrder(t *testing.T) {
	t.Parallel()

	providerID := "550e8400-e29b-41d4-a716-446655440122"
	order := &database.Order{
		ID: 42, SubscriptionID: 11, ProductID: 16, Status: database.OrderStatusPending,
		AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID,
	}
	expired := 0

	mock := &testutil.ServiceStoreFake{
		GetStalePendingOrdersAfterIDFunc: func(_ context.Context, _ time.Time, afterID uint, _ int) ([]database.Order, error) {
			if order.ID <= afterID {
				return nil, nil
			}

			return []database.Order{*order}, nil
		},
		ExpirePendingOrderFunc: func(_ context.Context, orderID uint) (bool, error) {
			expired++
			assert.Equal(t, order.ID, orderID)

			return true, nil
		},
	}

	provider := &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		return &platega.TransactionStatusResponse{ID: providerID, Status: "CANCELED"}, nil
	}}
	o := NewOrderService(mock, nil, nil, provider, "", nil)

	require.NoError(t, o.ReconcilePendingOrders(context.Background(), 10))
	assert.Equal(t, 1, expired)
}

// Тест поведения: провайдер недоступен — заказ остаётся pending и ошибка
// возвращается, чтобы деградация была видна в логах воркера.
func TestOrderService_ReconcilePendingOrders_ProviderErrorKeepsOrder(t *testing.T) {
	t.Parallel()

	providerID := "550e8400-e29b-41d4-a716-446655440123"
	order := &database.Order{
		ID: 43, SubscriptionID: 12, ProductID: 17, Status: database.OrderStatusPending,
		AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID,
	}

	mock := &testutil.ServiceStoreFake{
		GetStalePendingOrdersAfterIDFunc: func(_ context.Context, _ time.Time, afterID uint, _ int) ([]database.Order, error) {
			if order.ID <= afterID {
				return nil, nil
			}

			return []database.Order{*order}, nil
		},
		ExpirePendingOrderFunc: func(context.Context, uint) (bool, error) {
			t.Fatal("заказ нельзя истекать, пока провайдер не ответил")

			return false, nil
		},
	}

	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{statusFunc: func(context.Context, string) (*platega.TransactionStatusResponse, error) {
		return nil, errors.New("provider timeout")
	}}, "", nil)

	require.Error(t, o.ReconcilePendingOrders(context.Background(), 10))
	assert.Equal(t, database.OrderStatusPending, order.Status)
}

// ─── Stars payment tests ─────────────────────────────────────────────────────

func TestRequestPayment_StarsCreatesPendingWithTTL(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{PaymentProvider: "telegram_stars", StarsInvoiceTTL: 15 * time.Minute, TelegramAdminID: 999, PaymentEnabled: true}
	product := &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: "XTR", IsActive: true}

	var (
		gotProvider string
		gotTTL      *time.Time
		gotCurrency string
		gotAmount   int64
	)

	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return product, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 3, TelegramID: 42}, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		FindOrCreatePendingPaymentOrderFunc: func(_ context.Context, _, _ uint, provider string, amountCents int64, currency string, paymentExpiresAt *time.Time, now time.Time) (*database.Order, bool, error) {
			gotProvider, gotCurrency, gotAmount, gotTTL = provider, currency, amountCents, paymentExpiresAt

			return &database.Order{ID: 55, SubscriptionID: 3, ProductID: 7, Status: database.OrderStatusPending, AmountCents: amountCents, Currency: currency, PaymentProvider: provider, PaymentExpiresAt: paymentExpiresAt, CreatedAt: now}, true, nil
		},
	}
	o := NewOrderService(mock, nil, nil, nil, "", cfg)
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	info, order, err := o.RequestPayment(context.Background(), 42, "user", &database.Product{ID: 7})
	require.NoError(t, err)
	require.NotNil(t, order)

	assert.Equal(t, "telegram_stars", gotProvider)
	assert.Equal(t, "XTR", gotCurrency)
	assert.Equal(t, int64(199), gotAmount)
	require.NotNil(t, gotTTL, "TTL must be set atomically at creation (D8)")
	assert.WithinDuration(t, time.Now().UTC().Add(15*time.Minute), *gotTTL, 10*time.Second)
	assert.Equal(t, "telegram_stars", info.Provider)
	assert.Equal(t, *gotTTL, info.ExpiresAt)
	assert.Empty(t, info.URL, "stars flow returns no payment URL")
}

func TestRequestPayment_StarsLivePendingReturnsAlreadyInProgress(t *testing.T) {
	t.Parallel()

	liveExpiry := time.Now().UTC().Add(10 * time.Minute)
	pending := &database.Order{ID: 55, SubscriptionID: 3, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, PaymentExpiresAt: &liveExpiry}

	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: "XTR", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 3, TelegramID: 42}, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		FindOrCreatePendingPaymentOrderFunc: func(context.Context, uint, uint, string, int64, string, *time.Time, time.Time) (*database.Order, bool, error) {
			// reuse через created=false: победителя определяет репозиторий.
			return pending, false, nil
		},
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true, TelegramAdminID: 999})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	_, gotOrder, err := o.RequestPayment(context.Background(), 42, "user", &database.Product{ID: 7})
	require.ErrorIs(t, err, ErrPaymentAlreadyInProgress)
	require.Same(t, pending, gotOrder)
}

func TestRequestPayment_StarsRUBProductFailsClosed(t *testing.T) {
	t.Parallel()

	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 230000, Currency: "RUB", IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 3, TelegramID: 42}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true, TelegramAdminID: 999})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	_, _, err := o.RequestPayment(context.Background(), 42, "user", &database.Product{ID: 7})
	require.Error(t, err, "RUB products must fail closed in Stars mode: ренты в Stars не отправляются")
	assert.NotErrorIs(t, err, ErrPaymentDisabled)
}

func TestRequestPayment_StarsExistingSubscriptionRejectsInactiveProduct(t *testing.T) {
	t.Parallel()

	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: stars.Currency, IsActive: false}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 3, TelegramID: 42}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			t.Fatal("inactive product must not reach pending lookup")

			return nil, errors.New("unreachable")
		},
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: database.PaymentProviderTelegramStars, PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	_, _, err := o.RequestPayment(context.Background(), 42, "user", &database.Product{ID: 7})
	require.Error(t, err, "inactive Stars product must be rejected even when a subscription exists")
}

func TestRequestPayment_StarsExistingSubscriptionRejectsInactivePlan(t *testing.T) {
	t.Parallel()

	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: stars.Currency, IsActive: true}, nil
		},
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return &database.Subscription{ID: 3, TelegramID: 42}, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: false}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			t.Fatal("inactive plan must not reach pending lookup")

			return nil, errors.New("unreachable")
		},
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: database.PaymentProviderTelegramStars, PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	_, _, err := o.RequestPayment(context.Background(), 42, "user", &database.Product{ID: 7})
	require.Error(t, err, "inactive Stars plan must be rejected even when a subscription exists")
}

func TestValidateStarsPreCheckout_ValidPayloadReturnsNil(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}
	sub := &database.Subscription{ID: 4, TelegramID: 42}

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc:      func(context.Context, uint) (*database.Subscription, error) { return sub, nil },
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	err := o.ValidateStarsPreCheckout(context.Background(), "31", 42, 199, "XTR")
	require.NoError(t, err, "valid payload must pass pre-checkout")
}

func TestValidateStarsPreCheckout_RejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		payload    string
		telegramID int64
		amount     int64
		currency   string
		wantErr    string // substring
	}{
		{name: "order not found", payload: "999999", telegramID: 42, amount: 199, currency: "XTR", wantErr: "not found"},
		{name: "wrong user", payload: "31", telegramID: 99, amount: 199, currency: "XTR", wantErr: "different telegram user"},
		{name: "amount mismatch", payload: "31", telegramID: 42, amount: 200, currency: "XTR", wantErr: "amount"},
		{name: "currency mismatch", payload: "31", telegramID: 42, amount: 199, currency: "RUB", wantErr: "currency"},
		{name: "wrong provider", payload: "31", telegramID: 42, amount: 199, currency: "XTR", wantErr: "not a Telegram Stars"},
		{name: "already paid", payload: "31", telegramID: 42, amount: 199, currency: "XTR", wantErr: "invalid payment transition"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPaid, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}
			// "wrong provider" overrides provider to platega
			if tt.name == "wrong provider" {
				order.PaymentProvider = database.PaymentProviderPlatega
			}
			// "already paid" keeps status paid
			// "order not found" has GetOrderByID return error
			mock := &testutil.ServiceStoreFake{
				GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
					if tt.name == "order not found" {
						return nil, gorm.ErrRecordNotFound
					}

					return order, nil
				},
				GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
					return &database.Subscription{ID: 4, TelegramID: 42}, nil
				},
			}
			o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
			o.SetStarsClient(stars.New(testutil.NewBotAPI()))

			err := o.ValidateStarsPreCheckout(context.Background(), tt.payload, tt.telegramID, tt.amount, tt.currency)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestConfirmStarsPayment_FirstConfirmActivatesAndRecordsFeeZero(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}
	sub := &database.Subscription{ID: 4, TelegramID: 42, PlanID: 2} // free, not renewal
	product := &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: "XTR"}

	var capturedChargeID string
	var capturedFee *int64

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc:   func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc:        func(context.Context, uint) (*database.Subscription, error) { return sub, nil },
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) { return product, nil },
		ConfirmOrderPaidCASFunc: func(_ context.Context, p database.ConfirmPaidOrderParams) (bool, error) {
			capturedChargeID, capturedFee = p.ProviderPaymentID, p.ProviderFeeCents

			return true, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_abc", "31", 199, "XTR", 42)
	require.NoError(t, err)
	require.True(t, confirmation.Activated, "first confirm must activate subscription")
	assert.Equal(t, "charge_abc", capturedChargeID)
	require.NotNil(t, capturedFee)
	assert.Equal(t, int64(0), *capturedFee, "Stars fee must be zero")
	assert.False(t, confirmation.IsRenewal, "free→paid is not a renewal")
}

func TestConfirmStarsPayment_AlreadyPaidIsIdempotent(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPaid, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	var casCalled bool

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			casCalled = true

			return false, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_dup", "31", 199, "XTR", 42)
	require.NoError(t, err)
	assert.False(t, confirmation.Activated, "duplicate confirm must not re-activate")
	assert.False(t, casCalled, "CAS must not run for already-paid orders")
}

func TestConfirmStarsPayment_ExpiredOrderActivatesWithoutGrace(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusExpired, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}
	sub := &database.Subscription{ID: 4, TelegramID: 42, PlanID: 2}
	product := &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: "XTR"}

	var casCalled bool

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc:   func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc:        func(context.Context, uint) (*database.Subscription, error) { return sub, nil },
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) { return product, nil },
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			casCalled = true

			return true, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_exp", "31", 199, "XTR", 42)
	require.NoError(t, err)
	assert.True(t, confirmation.Activated, "expired→paid must activate without grace period (D7)")
	assert.True(t, casCalled)
}

func TestConfirmStarsPayment_DuplicateChargeOnPaidOrderAutoRefundsAndSetsFlag(t *testing.T) {
	t.Parallel()

	existingCharge := "charge_existing"
	duplicateCharge := "charge_duplicate"

	paidOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPaid, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: existingCharge}
	product := &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: "XTR"}

	var capturedRefundTelegramID int64
	var capturedRefundChargeID string

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return paidOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) { return product, nil },
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(&testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				capturedRefundTelegramID, _ = strconv.ParseInt(params["user_id"], 10, 64)
				capturedRefundChargeID = params["telegram_payment_charge_id"]

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	}))

	confirmation, err := o.ConfirmStarsPayment(context.Background(), duplicateCharge, "31", 199, "XTR", 42)
	require.NoError(t, err)
	require.NotNil(t, confirmation)
	assert.False(t, confirmation.Activated)
	assert.True(t, confirmation.AutoRefunded)
	assert.Equal(t, existingCharge, confirmation.Order.ProviderPaymentID)
	assert.Equal(t, int64(42), capturedRefundTelegramID)
	assert.Equal(t, duplicateCharge, capturedRefundChargeID)
}

func TestConfirmStarsPayment_DuplicateChargeOnCanceledOrderAutoRefundsAndSetsFlag(t *testing.T) {
	t.Parallel()

	duplicateCharge := "charge_dup_cancel"

	canceledOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusCanceled, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge_canc"}

	var capturedRefundTelegramID int64
	var capturedRefundChargeID string

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return canceledOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 99}, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(&testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				capturedRefundTelegramID, _ = strconv.ParseInt(params["user_id"], 10, 64)
				capturedRefundChargeID = params["telegram_payment_charge_id"]

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	}))

	confirmation, err := o.ConfirmStarsPayment(context.Background(), duplicateCharge, "31", 199, "XTR", 99)
	require.NoError(t, err)
	require.NotNil(t, confirmation)
	assert.False(t, confirmation.Activated)
	assert.True(t, confirmation.AutoRefunded)
	assert.Equal(t, int64(99), capturedRefundTelegramID)
	assert.Equal(t, duplicateCharge, capturedRefundChargeID)
}

// TestConfirmStarsPayment_DuplicateChargeRefundFailsReturnsError — сбой
// авто-возврата дубля возвращает ошибку (R8): recovery повторит транзакцию
// и вернёт charge, offset не двигается мимо неоплаченного возврата.
func TestConfirmStarsPayment_DuplicateChargeRefundFailsReturnsError(t *testing.T) {
	t.Parallel()

	paidOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPaid, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge_first"}

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return paidOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
	}

	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(&testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			return nil, errors.New("telegram rate limit")
		},
	}))

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_second", "31", 199, "XTR", 42)
	require.Error(t, err, "refund failure must surface to the caller: recovery retries and holds its offset")
	require.Nil(t, confirmation)
	assert.Equal(t, database.OrderStatusPaid, paidOrder.Status, "order must remain paid after a failed refund")
}

func TestConfirmStarsPayment_CanceledOrderAlertsAdmin(t *testing.T) {
	t.Parallel()

	adminBot := testutil.NewBotAPI()
	order := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusCanceled, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	var casCalled bool

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			casCalled = true

			return false, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true, TelegramAdminID: 999})
	o.SetAdminBot(adminBot)
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_canc", "31", 199, "XTR", 42)
	require.NoError(t, err)
	assert.False(t, confirmation.Activated, "canceled→paid must not activate")
	assert.False(t, casCalled, "CAS must not run for canceled orders")
	assert.True(t, confirmation.AutoRefunded)

	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "stars_duplicate_charge_refunded")
}

func TestConfirmStarsPayment_ClientDisabledReturnsPaymentDisabled(t *testing.T) {
	t.Parallel()

	mock := &testutil.ServiceStoreFake{}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	// intentionally not calling SetStarsClient

	_, err := o.ConfirmStarsPayment(context.Background(), "charge", "31", 199, "XTR", 42)
	require.ErrorIs(t, err, ErrPaymentDisabled)
}

func starsRefundOrder() *database.Order {
	return &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPaid, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge_ref"}
}

func TestRefundStarsOrder_AdminActorAndResult(t *testing.T) {
	t.Parallel()

	order := starsRefundOrder()
	var auditEvent *database.AuditLog

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
			return order, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetPlanByNameFunc: func(context.Context, string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: database.FreePlanName}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(&database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: true}, nil, func(p database.CancelPaidOrderParams) {
			auditEvent = p.Audit(order, &database.Subscription{ID: 4}, true)
		}),
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: database.PaymentProviderTelegramStars, PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	ctx := WithAuditAdmin(context.Background(), 999)
	result, err := o.RefundStarsOrder(ctx, 31)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, uint(31), result.OrderID)
	assert.Equal(t, int64(42), result.TelegramID)
	assert.Equal(t, int64(199), result.AmountCents)
	assert.Equal(t, stars.Currency, result.Currency)
	assert.True(t, result.Downgraded)

	require.NotNil(t, auditEvent)
	assert.Equal(t, database.AuditEventPaymentRefunded, auditEvent.Event)
	assert.Equal(t, database.ActorAdmin, auditEvent.ActorType)
	require.NotNil(t, auditEvent.ActorID)
	assert.Equal(t, "999", *auditEvent.ActorID)
}

func TestRefundStarsOrder_SuccessCancelsDowngradesAndAudits(t *testing.T) {
	t.Parallel()

	adminBot := testutil.NewBotAPI()
	order := starsRefundOrder()

	var (
		casProvider string
		casChargeID string
		auditEvent  *database.AuditLog
	)

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetPlanByNameFunc: func(_ context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: name}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(&database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: true}, nil, func(p database.CancelPaidOrderParams) {
			casProvider, casChargeID = p.Provider, p.ProviderPaymentID
			auditEvent = p.Audit(order, &database.Subscription{ID: 4}, true)
		}),
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true, TelegramAdminID: 999})
	o.SetAdminBot(adminBot)
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.NoError(t, err)

	assert.Equal(t, "telegram_stars", casProvider)
	assert.Equal(t, "charge_ref", casChargeID)
	require.NotNil(t, auditEvent)
	assert.Equal(t, database.AuditEventPaymentRefunded, auditEvent.Event)

	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Возврат Stars-платежа")
	assert.Contains(t, messages[0].Text, "понижен до бесплатного")
}

func TestRefundStarsOrder_AlreadyCanceledIsNoOp(t *testing.T) {
	t.Parallel()

	order := starsRefundOrder()
	order.Status = database.OrderStatusCanceled

	var makeRequestCalled bool

	starsBot := testutil.NewBotAPI()
	starsBot.MakeRequestFunc = func(string, tgbotapi.Params) (*tgbotapi.APIResponse, error) {
		makeRequestCalled = true

		return &tgbotapi.APIResponse{Ok: true}, nil
	}

	var casCalled bool

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc:                   func(context.Context, uint) (*database.Order, error) { return order, nil },
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(nil, errors.New("unreachable"), func(database.CancelPaidOrderParams) { casCalled = true }),
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(starsBot))

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.ErrorIs(t, err, ErrStarsOrderAlreadyRefunded)
	assert.False(t, makeRequestCalled, "canceled order must not hit Telegram")
	assert.False(t, casCalled, "canceled order must not run CAS")
}

func TestRefundStarsOrder_TelegramErrorKeepsDBUntouched(t *testing.T) {
	t.Parallel()

	adminBot := testutil.NewBotAPI()
	order := starsRefundOrder()

	starsBot := testutil.NewBotAPI()
	starsBot.MakeRequestFunc = func(string, tgbotapi.Params) (*tgbotapi.APIResponse, error) {
		return &tgbotapi.APIResponse{Ok: false, Description: "Bad Request: STARS_WITHDRAWAL_BALANCE_TOO_LOW"}, errors.New("Bad Request: STARS_WITHDRAWAL_BALANCE_TOO_LOW")
	}

	var casCalled bool

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(nil, errors.New("unreachable"), func(database.CancelPaidOrderParams) { casCalled = true }),
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true, TelegramAdminID: 999})
	o.SetAdminBot(adminBot)
	o.SetStarsClient(stars.New(starsBot))

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.Error(t, err)
	assert.False(t, casCalled, "failed Telegram refund must not touch the DB")

	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "stars_refund_failed")
	assert.Equal(t, database.OrderStatusPaid, order.Status, "order must remain paid after a failed Telegram refund")
}

func TestRefundStarsOrder_ChargeAlreadyRefundedProceedsToCAS(t *testing.T) {
	t.Parallel()

	order := starsRefundOrder()

	starsBot := testutil.NewBotAPI()
	starsBot.MakeRequestFunc = func(string, tgbotapi.Params) (*tgbotapi.APIResponse, error) {
		return &tgbotapi.APIResponse{Ok: false, Description: "CHARGE_ALREADY_REFUNDED"}, errors.New("Bad Request: CHARGE_ALREADY_REFUNDED")
	}

	var casCalled bool

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetPlanByNameFunc: func(_ context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: name}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(&database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: false}, nil, func(database.CancelPaidOrderParams) { casCalled = true }),
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(starsBot))

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.NoError(t, err, "CHARGE_ALREADY_REFUNDED must be treated as success (M2)")
	assert.True(t, casCalled, "already-refunded charge must still reach the cancel CAS")
}

// TestRefundStarsOrder_RetryAfterFailedCASCompletesState — split-brain:
// Telegram-возврат прошёл, а CAS не применился. Повторный /refund должен
// безопасно завершить состояние заказа: Telegram сообщает
// CHARGE_ALREADY_REFUNDED, команда трактует это как успех и доводит CAS.
func TestRefundStarsOrder_RetryAfterFailedCASCompletesState(t *testing.T) {
	t.Parallel()

	order := starsRefundOrder()

	refundCalls := 0
	starsBot := testutil.NewBotAPI()
	starsBot.MakeRequestFunc = func(string, tgbotapi.Params) (*tgbotapi.APIResponse, error) {
		refundCalls++
		if refundCalls == 1 {
			return &tgbotapi.APIResponse{Ok: true}, nil
		}

		return &tgbotapi.APIResponse{Ok: false, Description: "CHARGE_ALREADY_REFUNDED"}, errors.New("Bad Request: CHARGE_ALREADY_REFUNDED")
	}

	casCalls := 0
	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetPlanByNameFunc: func(_ context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: name}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(_ context.Context, _ database.CancelPaidOrderParams) (*database.ChargebackResult, error) {
			casCalls++
			if casCalls == 1 {
				return nil, errors.New("database is down")
			}

			return &database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: false}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(starsBot))

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.Error(t, err, "the failed CAS must be reported")
	require.ErrorIs(t, err, ErrStarsRefundStatePending, "Telegram already refunded the money")

	result, err := o.RefundStarsOrder(context.Background(), 31)
	require.NoError(t, err, "the retry must complete the order state")
	require.NotNil(t, result)
	assert.Equal(t, uint(31), result.OrderID)
	assert.Equal(t, 2, casCalls, "the retry must reach the cancel CAS again")
}

func TestRefundStarsOrder_DowngradedRunsPostCommitSync(t *testing.T) {
	t.Parallel()

	order := starsRefundOrder()

	syncInvoked := false

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetPlanByNameFunc: func(_ context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: name}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(&database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: true}, nil),
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			syncInvoked = true

			return nil, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.NoError(t, err)
	assert.True(t, syncInvoked, "downgrade after refund must run post-commit SyncSubscription")
}

func TestRefundStarsOrder_NonStarsOrderRejected(t *testing.T) {
	t.Parallel()

	order := starsRefundOrder()
	order.PaymentProvider = database.PaymentProviderPlatega

	var makeRequestCalled bool

	starsBot := testutil.NewBotAPI()
	starsBot.MakeRequestFunc = func(string, tgbotapi.Params) (*tgbotapi.APIResponse, error) {
		makeRequestCalled = true

		return &tgbotapi.APIResponse{Ok: true}, nil
	}

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(starsBot))

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.Error(t, err, "non-Stars orders must not go through the Stars refund path")
	assert.False(t, makeRequestCalled)
}

func TestRefundStarsOrder_ClientDisabledReturnsPaymentDisabled(t *testing.T) {
	t.Parallel()

	mock := &testutil.ServiceStoreFake{}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	// intentionally not calling SetStarsClient

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.ErrorIs(t, err, ErrPaymentDisabled)
}

func TestRequestPayment_PlategaRejectsXTRBeforePendingLookup(t *testing.T) {
	t.Parallel()

	pendingLookupCalled := false
	mock := &testutil.ServiceStoreFake{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 9, PlanID: 2, Name: "Stars", PriceCents: 199, Currency: stars.Currency, IsActive: true}, nil
		},
		FindPendingPaymentOrderFunc: func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
			pendingLookupCalled = true

			return nil, nil
		},
	}
	o := NewOrderService(mock, nil, nil, &funcPaymentProvider{}, "", &config.Config{PaymentProvider: database.PaymentProviderPlatega, PaymentEnabled: true})

	_, _, err := o.RequestPayment(context.Background(), 42, "user", &database.Product{ID: 9})
	require.ErrorIs(t, err, ErrCurrencyMismatch)
	assert.False(t, pendingLookupCalled, "XTR must be rejected before a Platega pending intent can be reused")
}

func TestOrderService_ReconcilePendingOrders_SkipsPlategaWhenClientUnavailable(t *testing.T) {
	t.Parallel()

	order := database.Order{
		ID: 91, SubscriptionID: 13, ProductID: 18, Status: database.OrderStatusPending,
		AmountCents: 2300, Currency: "RUB", PaymentProvider: database.PaymentProviderPlatega,
		ProviderPaymentID: "550e8400-e29b-41d4-a716-446655440291", PaymentExpiresAt: testutil.PtrTime(time.Now().UTC().Add(-time.Hour)),
	}
	mock := &testutil.ServiceStoreFake{
		GetStalePendingOrdersAfterIDFunc: func(_ context.Context, _ time.Time, afterID uint, _ int) ([]database.Order, error) {
			if order.ID <= afterID {
				return nil, nil
			}

			return []database.Order{order}, nil
		},
		ExpirePendingOrderFunc: func(context.Context, uint) (bool, error) {
			t.Fatal("unavailable Platega client must not mutate the legacy order")

			return false, nil
		},
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: database.PaymentProviderTelegramStars, PaymentEnabled: true})

	require.NoError(t, o.ReconcilePendingOrders(context.Background(), 10))
}

// S1-регрессия: free-plan грузится ДО возврата Telegram. Ошибка загрузки плана
// — обычная ошибка (не split-brain): деньги ещё не возвращены, Telegram не
// дёргается, БД не трогается.
func TestRefundStarsOrder_FreePlanLoadFailsBeforeTelegramRefund(t *testing.T) {
	t.Parallel()

	order := starsRefundOrder()
	refundCalled := false
	starsBot := testutil.NewBotAPI()
	starsBot.MakeRequestFunc = func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
		if endpoint == "refundStarPayment" {
			refundCalled = true
		}

		return &tgbotapi.APIResponse{Ok: true}, nil
	}
	casCalled := false
	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetPlanByNameFunc: func(context.Context, string) (*database.Plan, error) {
			return nil, errors.New("database unavailable")
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(nil, errors.New("unreachable"), func(database.CancelPaidOrderParams) { casCalled = true }),
	}
	o := NewOrderService(mock, nil, nil, nil, "", &config.Config{PaymentProvider: database.PaymentProviderTelegramStars, PaymentEnabled: true})
	o.SetStarsClient(stars.New(starsBot))

	_, err := o.RefundStarsOrder(context.Background(), 31)
	require.Error(t, err)
	assert.False(t, refundCalled, "Telegram refund must not run before the free plan is loaded")
	assert.False(t, casCalled, "CAS must not run when the free plan is unavailable")
	assert.Equal(t, database.OrderStatusPaid, order.Status, "order must remain paid: money was not refunded")

	var stateErr *StarsRefundStateError
	assert.NotErrorAs(t, err, &stateErr, "pre-refund plan failure is a plain error, not split-brain")
	assert.NotErrorIs(t, err, ErrStarsRefundStatePending)
}

// M3-регрессия: ошибка перечитывания заказа после проигранного CAS не глотается —
// ConfirmStarsPayment возвращает ошибку (recovery повторит страницу), refund не
// выполняется, админ получает alert.
func TestConfirmStarsPayment_CASLoserRereadFailureReturnsError(t *testing.T) {
	t.Parallel()

	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	refundCalled := false
	firstRead := true
	mock := &testutil.ServiceStoreFake{
		// Первое чтение (валидация) видит pending, перечит после CAS — ошибка.
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
			if firstRead {
				firstRead = false

				return pendingOrder, nil
			}

			return nil, errors.New("database is down")
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: "XTR"}, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			return false, nil
		},
	}
	o := newStarsTestService(mock, &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				refundCalled = true

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_reread_fail", "31", 199, "XTR", 42)
	require.Error(t, err, "reread failure after lost CAS race must surface as an error")
	assert.Nil(t, confirmation)
	assert.False(t, refundCalled, "reread failure must not refund: the order state is unknown")
}

// M3-регрессия: заказ перечитан, но его статус не paid/canceled (expired) —
// конфликт перехода возвращается как ошибка, а не как «успешное» подтверждение.
func TestConfirmStarsPayment_CASLoserUnexpectedStatusReturnsError(t *testing.T) {
	t.Parallel()

	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}
	expiredOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusExpired, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	refundCalled := false
	firstRead := true
	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
			if firstRead {
				firstRead = false

				return pendingOrder, nil
			}

			return expiredOrder, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: "XTR"}, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			return false, nil
		},
	}
	o := newStarsTestService(mock, &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				refundCalled = true

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_unexpected", "31", 199, "XTR", 42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPaymentTransition)
	assert.Nil(t, confirmation)
	assert.False(t, refundCalled, "an unexpected status must not trigger auto-refund")
}

// S2-регрессия: canceled-заказ, возвращённый по этому же charge, не возвращается
// повторно — идемпотентный no-op без refund и без alert.
func TestConfirmStarsPayment_CanceledOrderSameChargeIsNoOp(t *testing.T) {
	t.Parallel()

	canceledOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusCanceled, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge_same"}

	refundCalled := false
	makeRequestCalls := 0
	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return canceledOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
	}
	o := newStarsTestService(mock, &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			makeRequestCalls++
			if endpoint == "refundStarPayment" {
				refundCalled = true
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_same", "31", 199, "XTR", 42)
	require.NoError(t, err)
	require.NotNil(t, confirmation)
	assert.False(t, confirmation.Activated)
	assert.False(t, confirmation.AutoRefunded, "the same charge must not be refunded twice")
	assert.False(t, confirmation.DuplicateCharge)
	assert.False(t, refundCalled)
	assert.Zero(t, makeRequestCalls, "Telegram API must not be called for the already-refunded charge")
}

// S3-регрессия: новый charge на отменённый заказ — дубль с авто-возвратом.
func TestConfirmStarsPayment_CanceledOrderNewChargeAutoRefunds(t *testing.T) {
	t.Parallel()

	canceledOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusCanceled, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge_original"}

	var refundedCharge string
	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return canceledOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
	}
	o := newStarsTestService(mock, &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				refundedCharge = params["telegram_payment_charge_id"]

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_new", "31", 199, "XTR", 42)
	require.NoError(t, err)
	require.NotNil(t, confirmation)
	assert.False(t, confirmation.Activated)
	assert.True(t, confirmation.AutoRefunded)
	assert.True(t, confirmation.DuplicateCharge, "a new charge on a canceled order is a duplicate")
	assert.Equal(t, "charge_new", refundedCharge)
}

// S8-регрессия: применённый авто-возврат пишется в audit_log событием
// payment_auto_refunded с charge ID и причиной.
func TestConfirmStarsPayment_DeletedProductWritesAutoRefundAudit(t *testing.T) {
	t.Parallel()

	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return pendingOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return nil, database.ErrProductNotFound
		},
	}
	o := newStarsTestService(mock, &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_audited", "31", 199, "XTR", 42)
	require.NoError(t, err)
	assert.True(t, confirmation.AutoRefunded)

	var autoRefund *database.AuditLog
	for _, entry := range mock.AuditLogs {
		if entry.Event == database.AuditEventPaymentAutoRefunded {
			autoRefund = &entry
			break
		}
	}

	require.NotNil(t, autoRefund, "applied auto-refund must be recorded in audit_log")
	assert.Equal(t, database.EntityOrder, autoRefund.EntityType)
	assert.Equal(t, database.ActorPaymentProvider, autoRefund.ActorType)
	assert.Contains(t, autoRefund.Details, `"provider_payment_id":"charge_audited"`)
	assert.Contains(t, autoRefund.Details, `"reason":"product_deleted"`)
}

// M4-регрессия: PAYMENT_ENABLED=false блокирует выдачу новых платёжных ссылок
// до создания заказа/интента и до обращения к провайдеру.
func TestRequestPayment_PaymentDisabledGuard(t *testing.T) {
	t.Parallel()

	createCalled := false
	provider := &funcPaymentProvider{createFunc: func(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
		createCalled = true

		return nil, errors.New("provider must not be called")
	}}
	orderCreated := false
	mock := &testutil.ServiceStoreFake{
		FindOrCreatePendingPaymentOrderFunc: func(context.Context, uint, uint, string, int64, string, *time.Time, time.Time) (*database.Order, bool, error) {
			orderCreated = true

			return nil, false, errors.New("order must not be created")
		},
	}
	o := NewOrderService(mock, nil, nil, provider, "@testbot", &config.Config{PaymentEnabled: false})

	_, _, err := o.RequestPayment(context.Background(), 42, "user", &database.Product{ID: 9, IsActive: true, PriceCents: 2300, Currency: "RUB"})
	require.ErrorIs(t, err, ErrPaymentDisabled)
	assert.False(t, createCalled, "disabled payment must not reach the provider")
	assert.False(t, orderCreated, "disabled payment must not create an order")
}

// M5-регрессия: возврат с понижением тарифа инвалидирует кэш подписки — клиенты
// не должны получать оплаченный формат из кэша после возврата денег.
func TestRefundStarsOrder_DowngradeInvalidatesSubscriptionCache(t *testing.T) {
	t.Parallel()

	order := starsRefundOrder()
	sub := &database.Subscription{ID: 4, SubscriptionID: "sub-uuid-4", TelegramID: 42}

	var invalidated []string
	subSvc := NewSubscriptionService(testutil.NewServiceStoreFake(), nil, nil, &config.Config{})
	subSvc.SetInvalidateBySubIDFunc(func(subID string) { invalidated = append(invalidated, subID) })

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return order, nil },
		GetByIDFunc:      func(context.Context, uint) (*database.Subscription, error) { return sub, nil },
		GetPlanByNameFunc: func(_ context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: name}, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: casChargeback(&database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: true}, nil, func(database.CancelPaidOrderParams) {}),
	}
	o := NewOrderService(mock, subSvc, nil, nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))

	result, err := o.RefundStarsOrder(context.Background(), 31)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Downgraded)
	assert.Equal(t, []string{"sub-uuid-4"}, invalidated, "refund+downgrade must invalidate the subscription cache")
}

// Регрессия: confirmPayment не освобождал per-payment lock на error-путях
// resolvePlategaCallbackOrder (несовпадение валюты/суммы, ошибка загрузки
// заказа). Утечка токена отравляла provider_payment_id: каждый повторный
// callback по нему ждал timeout лока и падал, пока процесс не перезапущен.
func TestConfirmPayment_ReleasesLockOnValidationError(t *testing.T) {
	t.Parallel()

	providerID := "550e8400-e29b-41d4-a716-446655440140"
	mock := &testutil.ServiceStoreFake{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, string) (*database.Order, error) {
			return &database.Order{ID: 40, SubscriptionID: 50, ProductID: 60, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB"}, nil
		},
		GetProductByIDFunc: func(_ context.Context, id uint) (*database.Product, error) {
			return &database.Product{ID: id, PlanID: 70, DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
		},
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 77, PlanID: 70}, nil
		},
		ConfirmOrderPaidCASFunc: func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
			return true, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), &funcPaymentProvider{}, "", nil)
	o.paymentLockTimeout = 200 * time.Millisecond

	_, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("23.00"), "USD")
	require.ErrorIs(t, err, ErrCurrencyMismatch)

	confirmation, err := o.ConfirmPayment(context.Background(), providerID, "", json.Number("23.00"), "RUB")
	require.NoError(t, err, "retry after a failed validation must not wait for the leaked lock")
	require.NotNil(t, confirmation)
	assert.True(t, confirmation.Activated)
}
