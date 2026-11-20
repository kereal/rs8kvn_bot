package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

// Регрессия: несентинельные ошибки валидации payload («не-Stars заказ»,
// удалённая подписка, чужой пользователь) раньше не классифицировались как
// semantic mismatch — recovery-сверка считала их DB/infrastructure ошибкой,
// прерывала проход и не двигала offset: одна битая транзакция навсегда
// залипала страницу getStarTransactions, recovery последующих оплат стоял.

func starsReconcilePage(txs ...stars.StarTransaction) *tgbotapi.APIResponse {
	raw, err := json.Marshal(stars.StarTransactions{Transactions: txs})
	if err != nil {
		panic(err)
	}

	return &tgbotapi.APIResponse{Ok: true, Result: raw}
}

func starsInvoiceTx(id, payload string, amount int64, userID int64) stars.StarTransaction {
	return stars.StarTransaction{
		ID:     id,
		Amount: amount,
		Source: &stars.StarTransactionPartner{
			Type:            "user",
			TransactionType: "invoice_payment",
			InvoicePayload:  payload,
			User:            &stars.StarTransactionUser{ID: userID},
		},
	}
}

func newStarsReconcileService(mock *testutil.ServiceStoreFake, api *testutil.BotAPI) *OrderService {
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), nil, "", &config.Config{PaymentProvider: "telegram_stars"})
	o.SetStarsClient(stars.New(api))

	return o
}

func TestConfirmStarsPayment_DeletedSubscriptionIsSemanticMismatch(t *testing.T) {
	t.Parallel()

	// Подписка физически удалена (admin /del): подтверждение классифицируется
	// как semantic mismatch (recovery вернёт charge и продолжит страницу),
	// а не как DB-ошибка, которая навсегда залипла бы offset. Прямой confirm
	// refund не делает — это делает recovery-сверка (см. reconcile-тест ниже).
	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return pendingOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return nil, database.ErrSubscriptionNotFound
		},
	}
	o := newStarsReconcileService(mock, testutil.NewBotAPI())

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
	o := newStarsReconcileService(mock, &testutil.BotAPI{
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
	assert.NotErrorAs(t, err, &mismatch, "DB/infrastructure error must stay retriable so recovery retries the page")
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
	o := newStarsReconcileService(mock, &testutil.BotAPI{
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
	// activated=false — его charge возвращается сразу, без ожидания recovery.
	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}
	winnerOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPaid, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge_winner"}

	loserRefunded := ""
	firstRead := true
	mock := &testutil.ServiceStoreFake{
		// Первое чтение (валидация) видит pending, перечит после CAS — paid.
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
		ConfirmOrderPaidCASFunc: func(_ context.Context, _ uint, _, _ time.Time, _ *database.Subscription, _ *database.Product, _ database.ApplyPlanInTxFn, _ int64, _ string, _ *int64) (bool, error) {
			return false, nil
		},
	}
	o := newStarsReconcileService(mock, &testutil.BotAPI{
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
	assert.Equal(t, "charge_loser", loserRefunded)
}

func TestReconcileStarsTransactions_SkipsMismatchAndAdvancesOffset(t *testing.T) {
	t.Parallel()

	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	refundedCharges := []string{}
	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return pendingOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			// Подписка удалена: обе транзакции страницы — semantic mismatch.
			return nil, database.ErrSubscriptionNotFound
		},
	}
	api := &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			switch endpoint {
			case "getStarTransactions":
				return starsReconcilePage(
					starsInvoiceTx("tx1", "31", 199, 42),
					starsInvoiceTx("tx2", "31", 199, 42),
				), nil
			case "refundStarPayment":
				refundedCharges = append(refundedCharges, params["telegram_payment_charge_id"])

				return &tgbotapi.APIResponse{Ok: true}, nil
			}

			return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(`{}`)}, nil
		},
	}
	o := newStarsReconcileService(mock, api)

	require.NoError(t, o.ReconcileStarsTransactions(context.Background()))
	assert.Equal(t, []string{"tx1", "tx2"}, refundedCharges, "every mismatched charge of the page must be refunded")
	assert.Equal(t, int64(2), o.starsOffset, "offset must advance past the mismatched page instead of sticking forever")
}

func TestReconcileStarsTransactions_HoldsOffsetOnInfraError(t *testing.T) {
	t.Parallel()

	pendingOrder := &database.Order{ID: 31, SubscriptionID: 4, ProductID: 7, Status: database.OrderStatusPending, AmountCents: 199, Currency: "XTR", PaymentProvider: database.PaymentProviderTelegramStars}

	mock := &testutil.ServiceStoreFake{
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) { return pendingOrder, nil },
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return nil, errors.New("database is down")
		},
	}
	api := &testutil.BotAPI{
		MakeRequestFunc: func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
			require.Equal(t, "getStarTransactions", endpoint)

			return starsReconcilePage(starsInvoiceTx("tx1", "31", 199, 42)), nil
		},
	}
	o := newStarsReconcileService(mock, api)

	err := o.ReconcileStarsTransactions(context.Background())
	require.Error(t, err, "infrastructure error must abort the pass so the page is retried")
	assert.Equal(t, int64(0), o.starsOffset, "offset must not move on infrastructure error")
}
