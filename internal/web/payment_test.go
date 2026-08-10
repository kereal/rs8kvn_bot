package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// §6.4 Web payment callback endpoint tests.

var testPaymentID = uuid.MustParse("550e8400-e29b-41d4-a716-446655440111")

func TestHandlePaymentCallback_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	srv := NewServer(":0", nil, &config.Config{}, "bot", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/payment/callback", nil)
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "POST", rec.Header().Get("Allow"))
}

func TestHandlePaymentCallback_NotReady(t *testing.T) {
	t.Parallel()

	srv, _, _ := newPaymentTestServer(t, nil)
	srv.SetPaymentReady(false)
	req := httptest.NewRequest(http.MethodPost, "/payment/callback", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandlePaymentCallback_ServiceUnavailable(t *testing.T) {
	t.Parallel()

	srv := NewServer(":0", nil, &config.Config{}, "bot", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/payment/callback", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, strings.ToLower(rec.Body.String()), "payments not available")
}

func TestHandlePaymentCallback_Unauthorized(t *testing.T) {
	t.Parallel()

	srv, _, _ := newPaymentTestServer(t, &database.Order{ID: 1, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"})
	req := httptest.NewRequest(http.MethodPost, "/payment/callback", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandlePaymentCallback_InvalidJSON(t *testing.T) {
	t.Parallel()

	srv, _, _ := newPaymentTestServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/payment/callback", strings.NewReader(`{invalid`))
	req.Header.Set("X-MerchantId", "merchant")
	req.Header.Set("X-Secret", "secret")
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandlePaymentCallback_RequiresUUIDProviderTransactionID(t *testing.T) {
	t.Parallel()

	_, err := platega.ParseTransactionID("tx-123")
	assert.Error(t, err)
}

func TestHandlePaymentCallback_BodySizeLimit(t *testing.T) {
	t.Parallel()

	body := strings.NewReader(strings.Repeat("x", 300<<10))
	srv, _, _ := newPaymentTestServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/payment/callback", body)
	req.Header.Set("X-MerchantId", "merchant")
	req.Header.Set("X-Secret", "secret")
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandlePaymentCallback_ConfirmedActivatesOrder(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 2, SubscriptionID: 20, ProductID: 30, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, db, bot := newPaymentTestServer(t, order)
	req := paymentRequest("CONFIRMED", testPaymentID, `23.00`)
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ok":true`)
	stored, err := db.GetOrderByID(context.Background(), order.ID)
	require.NoError(t, err)
	assert.Equal(t, database.OrderStatusPaid, stored.Status)
	assert.Equal(t, 0, bot.SendCountSafe()) // Telegram success is skipped for invalid Telegram ID
}

func TestHandlePaymentCallback_DuplicateConfirmedIsIdempotent(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 8, SubscriptionID: 26, ProductID: 36, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, db, bot := newPaymentTestServer(t, order)
	confirmCalls := 0
	db.ConfirmOrderPaidCASFunc = func(_ context.Context, orderID uint, paidAt, activatedAt time.Time, sub *database.Subscription, _ *database.Product, _ database.ApplyPlanInTxFn) (bool, error) {
		confirmCalls++
		if orderID != order.ID || order.Status != database.OrderStatusPending {
			return false, nil
		}
		order.Status = database.OrderStatusPaid
		order.PaidAt = &paidAt
		order.ActivatedAt = &activatedAt
		expiry := paidAt.AddDate(0, 0, 30)
		order.ExpiresAt = &expiry
		sub.ExpiresAt = &expiry
		return true, nil
	}

	first := httptest.NewRecorder()
	srv.handlePaymentCallback(first, paymentRequest("CONFIRMED", testPaymentID, `23.00`))
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, database.OrderStatusPaid, order.Status)

	second := httptest.NewRecorder()
	srv.handlePaymentCallback(second, paymentRequest("CONFIRMED", testPaymentID, `23.00`))
	require.Equal(t, http.StatusOK, second.Code)
	require.Contains(t, second.Body.String(), `"ok":true`)

	stored, err := db.GetOrderByID(context.Background(), order.ID)
	require.NoError(t, err)
	require.Equal(t, database.OrderStatusPaid, stored.Status)
	require.Equal(t, 1, confirmCalls, "duplicate callback must not repeat activation CAS")
	assert.Empty(t, bot.GetAllSentMessages(), "invalid Telegram ID should not produce duplicate notifications")
}

func TestHandlePaymentCallback_CurrencyMismatchLeavesOrderPending(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 9, SubscriptionID: 27, ProductID: 37, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, db, _ := newPaymentTestServer(t, order)
	req := paymentRequestWithCurrency("CONFIRMED", testPaymentID, `23.00`, "USD")
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	stored, err := db.GetOrderByID(context.Background(), order.ID)
	require.NoError(t, err)
	assert.Equal(t, database.OrderStatusPending, stored.Status)
}

func TestHandlePaymentCallback_CanceledAmountMismatch(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 4, SubscriptionID: 22, ProductID: 32, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, _, _ := newPaymentTestServer(t, order)
	req := paymentRequest("CANCELED", testPaymentID, `24.00`)
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, database.OrderStatusPending, order.Status)
}

func TestHandlePaymentCallback_ChargebackNotifiesAdmin(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 5, SubscriptionID: 23, ProductID: 33, Status: database.OrderStatusPaid, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, db, bot := newPaymentTestServer(t, order)
	// The chargeback on a paid order must downgrade the subscription to free.
	downgradeCalled := false
	db.GetNodesByPlanIDFunc = func(context.Context, uint) ([]database.Node, error) { return nil, nil }
	db.UpdateSubscriptionFunc = func(_ context.Context, sub *database.Subscription) error {
		downgradeCalled = true
		assert.Equal(t, uint(2), sub.PlanID, "chargeback must downgrade the subscription to the free plan")
		assert.Nil(t, sub.ExpiresAt)
		return nil
	}
	req := paymentRequest("CHARGEBACKED", testPaymentID, `23.00`)
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, database.OrderStatusCanceled, order.Status)
	assert.True(t, downgradeCalled, "chargeback on a paid order must downgrade access to free")
	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Chargeback requires manual review")
}

func TestHandlePaymentCallback_ConfirmedForDeletedSubscription(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 40, SubscriptionID: 41, ProductID: 42, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, db, bot := newPaymentTestServer(t, order)
	db.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	req := paymentRequest("CONFIRMED", testPaymentID, `23.00`)
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "deleted subscription must not turn the callback into a retried 5xx")
	assert.Equal(t, database.OrderStatusPending, order.Status, "order must not be activated without its subscription")
	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, int64(999), messages[0].ChatID)
	assert.Contains(t, messages[0].Text, "load_order_subscription_failed")
}

func TestHandlePaymentCallback_ConfirmedForDeletedProduct(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 43, SubscriptionID: 44, ProductID: 45, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, db, bot := newPaymentTestServer(t, order)
	db.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return nil, database.ErrProductNotFound
	}
	req := paymentRequest("CONFIRMED", testPaymentID, `23.00`)
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "deleted product must not turn the callback into a retried 5xx")
	assert.Equal(t, database.OrderStatusPending, order.Status)
	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "load_order_product_failed")
}

func TestHandlePaymentCallback_LateConfirmedNotifiesAdmin(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 3, SubscriptionID: 21, ProductID: 31, Status: database.OrderStatusExpired, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, _, bot := newPaymentTestServer(t, order)
	req := paymentRequest("CONFIRMED", testPaymentID, `23.00`)
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, int64(999), messages[0].ChatID)
	assert.Contains(t, messages[0].Text, "Late confirmed payment")
	assert.Contains(t, messages[0].Text, "Order ID: 3")
}

func TestHandlePaymentCallback_PostCommitNotificationBuildFailureAlertsAdmin(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 6, SubscriptionID: 24, ProductID: 34, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, db, bot := newPaymentTestServer(t, order)
	db.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{ID: 24, TelegramID: 42, PlanID: 1, Status: "active"}, nil
	}
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return nil, errors.New("traffic lookup failed")
	}

	rec := httptest.NewRecorder()
	srv.handlePaymentCallback(rec, paymentRequest("CONFIRMED", testPaymentID, `23.00`))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, database.OrderStatusPaid, order.Status)
	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, int64(999), messages[0].ChatID)
	assert.Contains(t, messages[0].Text, "paid_notification_build_failed")
}

func TestHandlePaymentCallback_PostCommitNotificationSendFailureAlertsAdmin(t *testing.T) {
	t.Parallel()

	order := &database.Order{ID: 7, SubscriptionID: 25, ProductID: 35, Status: database.OrderStatusPending, ProviderPaymentID: testPaymentID.String(), AmountCents: 2300, Currency: "RUB"}
	srv, db, bot := newPaymentTestServer(t, order)
	db.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{ID: 25, TelegramID: 42, PlanID: 1, Status: "active"}, nil
	}
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return &database.Subscription{ID: 25, TelegramID: 42, PlanID: 1, Status: "active"}, nil
	}
	bot.SendFunc = func(chattable tgbotapi.Chattable) (tgbotapi.Message, error) {
		message, ok := chattable.(tgbotapi.MessageConfig)
		if ok && message.ChatID == 42 {
			return tgbotapi.Message{}, errors.New("user delivery failed")
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	rec := httptest.NewRecorder()
	srv.handlePaymentCallback(rec, paymentRequest("CONFIRMED", testPaymentID, `23.00`))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, database.OrderStatusPaid, order.Status)
	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 2)
	assert.Equal(t, int64(42), messages[0].ChatID)
	assert.Equal(t, int64(999), messages[1].ChatID)
	assert.Contains(t, messages[1].Text, "paid_notification_send_failed")
}

func TestHandlePaymentCallback_PendingDoesNotNotifyAdmin(t *testing.T) {
	t.Parallel()

	srv, _, bot := newPaymentTestServer(t, nil)
	req := paymentRequest("PENDING", testPaymentID, `23.00`)
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, bot.GetAllSentMessages())
}

func TestHandlePaymentCallback_MalformedPayloadNotifiesAdmin(t *testing.T) {
	t.Parallel()

	srv, _, bot := newPaymentTestServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/payment/callback", strings.NewReader(`{"id":`))
	req.Header.Set("X-MerchantId", "merchant")
	req.Header.Set("X-Secret", "secret")
	rec := httptest.NewRecorder()

	srv.handlePaymentCallback(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, int64(999), messages[0].ChatID)
	assert.Contains(t, messages[0].Text, "malformed_callback")
}

func paymentRequest(status string, id uuid.UUID, amount string) *http.Request {
	return paymentRequestWithCurrency(status, id, amount, "RUB")
}

func paymentRequestWithCurrency(status string, id uuid.UUID, amount, currency string) *http.Request {
	body := map[string]any{"id": id.String(), "amount": json.Number(amount), "currency": currency, "status": status}
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/payment/callback", strings.NewReader(string(encoded)))
	req.Header.Set("X-MerchantId", "merchant")
	req.Header.Set("X-Secret", "secret")
	return req
}

func newPaymentTestServer(t *testing.T, order *database.Order) (*Server, *testutil.DatabaseService, *testutil.BotAPI) {
	t.Helper()
	db := testutil.NewDatabaseService()
	if order != nil {
		db.Orders = map[uint]*database.Order{order.ID: order}
		db.OrdersBySubscriptionID = map[uint][]database.Order{order.SubscriptionID: {*order}}
	}
	db.GetOrderByProviderPaymentIDFunc = func(_ context.Context, _ string, providerID uuid.UUID) (*database.Order, error) {
		if order != nil && providerID.String() == order.ProviderPaymentID {
			return order, nil
		}
		return nil, database.ErrOrderNotFound
	}
	db.GetByIDFunc = func(_ context.Context, id uint) (*database.Subscription, error) {
		telegramID := int64(0)
		if order != nil && order.Status == database.OrderStatusExpired {
			telegramID = 12345
		}
		return &database.Subscription{ID: id, TelegramID: telegramID, PlanID: 1, Status: "active"}, nil
	}
	db.GetProductByIDFunc = func(_ context.Context, id uint) (*database.Product, error) {
		return &database.Product{ID: id, PlanID: 1, DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}, nil
	}
	if order != nil {
		db.ConfirmOrderPaidCASFunc = func(_ context.Context, orderID uint, paidAt, activatedAt time.Time, sub *database.Subscription, product *database.Product, _ database.ApplyPlanInTxFn) (bool, error) {
			if orderID != order.ID || order.Status != database.OrderStatusPending {
				return false, nil
			}
			order.Status = database.OrderStatusPaid
			order.PaidAt = &paidAt
			order.ActivatedAt = &activatedAt
			expiry := paidAt.AddDate(0, 0, 30)
			order.ExpiresAt = &expiry
			sub.ExpiresAt = &expiry
			return true, nil
		}
		db.CancelOrderCASFunc = func(_ context.Context, _ string, _ uuid.UUID, from []database.OrderStatus) (bool, error) {
			if order.Status == database.OrderStatusPending || (order.Status == database.OrderStatusPaid && len(from) > 1) {
				order.Status = database.OrderStatusCanceled
				return true, nil
			}
			return false, nil
		}
	}
	bot := testutil.NewBotAPI()
	cfg := &config.Config{TelegramAdminID: 999, GlobalSubURL: "https://example.com/sub/"}
	provider := testPaymentProvider{}
	subSvc := service.NewSubscriptionService(db, nil, nil, nil, cfg)
	syncSvc := service.NewSyncService(db, nil, nil)
	o := service.NewOrderService(db, subSvc, syncSvc, provider, "bot", cfg)
	o.SetAdminBot(bot)
	srv := NewServer(":0", nil, cfg, "bot", subSvc, nil)
	srv.SetOrderService(o)
	srv.SetBot(bot)
	srv.SetPaymentConfig(&PaymentConfig{Enabled: true, MerchantID: "merchant", Secret: "secret"})
	srv.SetPaymentReady(true)
	return srv, db, bot
}

type testPaymentProvider struct{}

func (testPaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return &platega.CreateTransactionResponse{TransactionID: testPaymentID.String(), URL: "https://pay.example", ExpiresIn: "00:15:00"}, nil
}
