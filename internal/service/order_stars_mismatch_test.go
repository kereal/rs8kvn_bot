package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

// Несентинельные ошибки валидации payload («не-Stars заказ», удалённая
// подписка, чужой пользователь) классифицируются как semantic mismatch:
// ретраем они не исправятся, поэтому charge возвращается автоматически, а не
// остаётся висеть до ручного разбора. Инфраструктурные ошибки, наоборот,
// остаются ретраибельными: повторная доставка webhook'а разберёт платёж.

func newStarsTestService(mock *testutil.ServiceStoreFake, api *testutil.BotAPI) *OrderService {
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars", PaymentEnabled: true})
	o.SetStarsClient(stars.New(api))

	return o
}

func TestConfirmStarsPayment_DeletedSubscriptionIsSemanticMismatch(t *testing.T) {
	t.Parallel()

	// Подписка физически удалена (admin /del): подтверждение классифицируется
	// как semantic mismatch (charge возвращается автоматически), а не как
	// DB-ошибка, которая повторялась бы вечно.
	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return pendingOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return nil, database.ErrSubscriptionNotFound
		},
	}
	o := newStarsTestService(mock, testutil.NewBotAPI())

	_, err := o.ConfirmStarsPayment(context.Background(), "charge_deleted_sub", "31", 199, "XTR", 42)

	var mismatch *StarsSemanticMismatchError
	require.Error(t, err)
	require.ErrorAs(t, err, &mismatch, "deleted subscription must classify as semantic mismatch, not infrastructure error")
}

func TestConfirmStarsPayment_DBInfraErrorIsNotMismatch(t *testing.T) {
	t.Parallel()

	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	refundCalled := false
	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return pendingOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return nil, errors.New("database is down")
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

	_, err := o.ConfirmStarsPayment(context.Background(), "charge_infra", "31", 199, "XTR", 42)

	require.Error(t, err)
	var mismatch *StarsSemanticMismatchError
	assert.NotErrorAs(t, err, &mismatch, "DB/infrastructure error must stay retriable")
	assert.False(t, refundCalled, "no refund on infrastructure error")
}

func TestConfirmStarsPayment_DeletedProductAutoRefunds(t *testing.T) {
	t.Parallel()

	// Товар удалён — услугу оказать нельзя: charge возвращается автоматически
	// (раньше деньги висели в pending-заказе без charge ID, /refund не работал).
	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	var refundedCharge string
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
				refundedCharge = params["telegram_payment_charge_id"]

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_deleted_product", "31", 199, "XTR", 42)
	require.NoError(t, err)
	require.NotNil(t, confirmation)
	assert.False(t, confirmation.Activated)
	assert.True(t, confirmation.AutoRefunded)
	assert.Equal(t, "charge_deleted_product", refundedCharge)
}

func TestConfirmStarsPayment_CASRaceLoserChargeAutoRefunds(t *testing.T) {
	t.Parallel()

	// CAS-гонка: два charge одного заказа обрабатываются параллельно (lock
	// keyed по charge ID). Победитель оплачивает заказ, проигравший получает
	// activated=false — его charge возвращается сразу.
	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}
	winnerOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPaid, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge_winner"}

	loserRefunded := ""
	firstRead := true
	mock := &testutil.ServiceStoreFake{
		// Первое чтение (валидация) видит pending, перечитывает после CAS — paid.
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
			if firstRead {
				firstRead = false

				return pendingOrder, nil
			}

			return winnerOrder, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) {
			return &database.Product{ID: 7, PlanID: 2, Name: "Premium", PriceCents: 199, Currency: "XTR"}, nil
		},
		ConfirmOrderPaidCASFunc: func(_ context.Context, _ database.ConfirmPaidOrderParams) (bool, error) {
			return false, nil
		},
	}
	o := newStarsTestService(mock, &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				loserRefunded = params["telegram_payment_charge_id"]

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	confirmation, err := o.ConfirmStarsPayment(context.Background(), "charge_loser", "31", 199, "XTR", 42)
	require.NoError(t, err)
	require.NotNil(t, confirmation)
	assert.False(t, confirmation.Activated)
	assert.True(t, confirmation.AutoRefunded, "losing charge of the CAS race must be refunded")
	assert.True(t, confirmation.DuplicateCharge, "losing charge of the CAS race is a duplicate")
	assert.Equal(t, "charge_loser", loserRefunded)
}

// Guard двойного возврата: charge уже подтверждённого заказа не возвращается —
// возврат вернул бы деньги за оказанную услугу. Единственный переживающий
// рестарт след подтверждения — журнал аудита (payment_confirmed).
func TestAutoRefundStarsCharge_SkipsAlreadyConfirmedCharge(t *testing.T) {
	t.Parallel()

	refundCalled := false
	mock := &testutil.ServiceStoreFake{
		HasPaymentConfirmedByProviderPaymentIDFunc: func(_ context.Context, provider string, providerPaymentID string) (bool, error) {
			require.Equal(t, database.PaymentProviderTelegramStars, provider)
			require.Equal(t, "charge_executed", providerPaymentID)

			return true, nil
		},
	}
	o := newStarsTestService(mock, &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, _ tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				refundCalled = true

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	refunded, err := o.autoRefundStarsCharge(context.Background(), nil, 42, 199, "XTR", "charge_executed", "paid_order_new_charge")
	require.NoError(t, err)
	assert.False(t, refunded, "an already-confirmed charge must not be refunded")
	assert.False(t, refundCalled, "refundStarPayment must not be called for an executed charge")
}

// Guard двойного возврата, ошибка чтения аудита: решение о возврате принять
// нельзя → ошибка наружу (не «возврата не было»), повторная доставка webhook'а
// разберёт платёж.
func TestConfirmStarsPayment_DuplicateChargeAuditFailureReturnsError(t *testing.T) {
	t.Parallel()

	paidOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPaid, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge_first"}

	refundCalled := false
	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return paidOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 4, TelegramID: 42}, nil
		},
		HasPaymentConfirmedByProviderPaymentIDFunc: func(context.Context, string, string) (bool, error) {
			return false, errors.New("audit log is down")
		},
	}
	o := newStarsTestService(mock, &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, _ tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			if endpoint == "refundStarPayment" {
				refundCalled = true

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	})

	_, err := o.ConfirmStarsPayment(context.Background(), "charge_second", "31", 199, "XTR", 42)
	require.Error(t, err, "the refund decision must stay undecided while the audit log is unavailable")
	assert.False(t, refundCalled, "no refund may run without the audit check")
}
