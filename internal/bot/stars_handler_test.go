package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStarsTestHandler builds a Handler wired for the Telegram Stars provider:
// paymentBot is a separate fake BotAPI (D1), the OrderService has a stars
// client and a sync service so ConfirmStarsPayment reaches the CAS.
func newStarsTestHandler(t *testing.T, mockDB *testutil.BotStoreFake) (*Handler, *testutil.BotAPI, *testutil.BotAPI) {
	t.Helper()

	cfg := &config.Config{TelegramAdminID: 123, PaymentEnabled: true, PaymentProvider: database.PaymentProviderTelegramStars}
	mainBot := testutil.NewBotAPI()
	paymentBot := testutil.NewBotAPI()

	h := NewHandler(mainBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	h.SetPaymentBot(paymentBot)

	syncSvc := service.NewSyncService(mockDB, nil, nil)
	subSvc := service.NewSubscriptionService(mockDB, nil, nil, cfg)
	o := service.NewOrderService(mockDB, subSvc, syncSvc, nil, "testbot", cfg)
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))
	h.SetOrderService(o)

	return h, mainBot, paymentBot
}

// starsOrderMocks wires the fake DB for a successful Stars purchase of product 1.
func starsOrderMocks(mockDB *testutil.BotStoreFake, amount int64) {
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, Name: "Месяц", PriceCents: amount, Currency: stars.Currency, IsActive: true}, nil
	}
	mockDB.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
	mockDB.FindPendingPaymentOrderFunc = func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
		return nil, nil
	}
	mockDB.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 2, IsActive: true}, nil
	}
	mockDB.FindOrCreatePendingPaymentOrderFunc = func(_ context.Context, _, _ uint, provider string, amountCents int64, _ string, expiresAt *time.Time, now time.Time) (*database.Order, error) {
		if provider != database.PaymentProviderTelegramStars {
			return nil, errors.New("fake: expected telegram_stars provider")
		}

		order := &database.Order{
			ID: 3, SubscriptionID: 7, ProductID: 1, Status: database.OrderStatusPending,
			AmountCents: amountCents, Currency: stars.Currency, CreatedAt: now,
		}
		if expiresAt != nil {
			order.PaymentExpiresAt = expiresAt
		}

		return order, nil
	}
}

func TestStarsBuy_SendsInvoiceAndEditsCard(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, 199)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	err := h.handleBuyProduct(context.Background(), 42, 42, "user", 99, 1)
	require.NoError(t, err)

	// Invoice уходит через payment-инстанс, НЕ через основной бот (D1/D3).
	require.Len(t, paymentBot.AllChattablesSafe(), 1)

	invoice, ok := paymentBot.AllChattablesSafe()[0].(tgbotapi.InvoiceConfig)
	require.True(t, ok, "expected an InvoiceConfig sent via paymentBot")

	assert.Equal(t, int64(42), invoice.ChatID)
	assert.Equal(t, stars.Currency, invoice.Currency)
	assert.Empty(t, invoice.ProviderToken, "Stars invoice must have an empty provider_token")
	assert.Len(t, invoice.Prices, 1, "Stars invoice must carry exactly one price")
	assert.Equal(t, 199, invoice.Prices[0].Amount)
	assert.Equal(t, "3", invoice.Payload, "payload must be the decimal order id")
	assert.Equal(t, "stars-3", invoice.StartParameter)
	assert.False(t, invoice.IsFlexible)

	// Основной бот получает только редактирование карточки.
	edits := mainBot.GetAllSentMessages()
	require.Len(t, edits, 1)

	edit, ok := mainBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "expected the card to be edited in place")
	assert.Equal(t, "Markdown", edit.ParseMode)
	assert.Contains(t, edit.Text, "Счёт отправлен ниже")
	assert.Contains(t, edit.Text, "Пользовательское соглашение (/terms)")
	assert.NotContains(t, edit.Text, "комиссию", "Stars card must not mention the provider fee")
	assert.NotContains(t, edit.Text, "199₽")
	assert.Contains(t, edit.Text, "199 XTR")

	// Кнопка «Назад» ведёт на stars_invoice_back_<invoice_msg_id>_<product_id>.
	require.NotNil(t, edit.ReplyMarkup)
	require.Len(t, edit.ReplyMarkup.InlineKeyboard, 1)
	require.NotNil(t, edit.ReplyMarkup.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "stars_invoice_back_1_1", *edit.ReplyMarkup.InlineKeyboard[0][0].CallbackData)
}

func TestStarsBuy_InvoiceSendErrorShowsSeparateText(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, 199)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	paymentBot.SendError = errors.New("telegram down")

	err := h.handleBuyProduct(context.Background(), 42, 42, "user", 99, 1)
	require.NoError(t, err)

	// M5: отдельный текст — не «счёт уже отправлен».
	messages := mainBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, msg(MsgInvoiceSendFailed), messages[0].Text)
}

func TestStarsBuy_AlreadyInProgressShowsStarsText(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true}, nil
	}
	mockDB.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
	mockDB.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 2, IsActive: true}, nil
	}
	mockDB.FindPendingPaymentOrderFunc = func(context.Context, uint, uint, string, time.Time) (*database.Order, error) {
		return &database.Order{ID: 3, Status: database.OrderStatusPending}, nil
	}
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	err := h.handleBuyProduct(context.Background(), 42, 42, "user", 99, 1)
	require.NoError(t, err)

	assert.Empty(t, paymentBot.AllChattablesSafe(), "no second invoice must be created")
	messages := mainBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, msg(MsgPaymentInProgressStars), messages[0].Text)
}

func TestStarsBuyPremiumList_FiltersNonXTRProducts(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	mockDB.ListActiveProductsFunc = func(context.Context) ([]database.Product, error) {
		return []database.Product{
			{ID: 1, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true},
			{ID: 2, Name: "Месяц RUB", PriceCents: 19900, Currency: "RUB", IsActive: true},
		}, nil
	}
	h, mainBot, _ := newStarsTestHandler(t, mockDB)

	err := h.handleBuyPremiumList(context.Background(), 42, "user", 99)
	require.NoError(t, err)

	edit, ok := mainBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok)
	require.NotNil(t, edit.ReplyMarkup)

	// Последняя строка — «Назад»; продуктовая строка ровно одна (XTR).
	rows := edit.ReplyMarkup.InlineKeyboard
	require.Len(t, rows, 2)
	require.NotNil(t, rows[0][0].CallbackData)
	assert.Equal(t, "buy_product_1", *rows[0][0].CallbackData)
	assert.Contains(t, rows[0][0].Text, "199 XTR")
}

func starsPreCheckoutUpdate(amount int, payload, currency string) tgbotapi.Update {
	return tgbotapi.Update{
		PreCheckoutQuery: &tgbotapi.PreCheckoutQuery{
			ID:             "q1",
			From:           &tgbotapi.User{ID: 42},
			Currency:       currency,
			TotalAmount:    amount,
			InvoicePayload: payload,
		},
	}
}

func starsPreCheckoutMocks(mockDB *testutil.BotStoreFake, status database.OrderStatus) {
	mockDB.GetOrderByIDFunc = func(_ context.Context, id uint) (*database.Order, error) {
		return &database.Order{
			ID: 3, SubscriptionID: 7, ProductID: 1, Status: status,
			AmountCents: 199, Currency: stars.Currency, PaymentProvider: database.PaymentProviderTelegramStars,
		}, nil
	}
	mockDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
}

func TestHandleUpdate_PreCheckoutValidAnswersOK(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsPreCheckoutMocks(mockDB, database.OrderStatusPending)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), starsPreCheckoutUpdate(199, "3", stars.Currency))

	// Ответ уходит ВСЕГДА и через payment-инстанс.
	require.Len(t, paymentBot.AllRequestsSafe(), 1)
	answer, ok := paymentBot.AllRequestsSafe()[0].(tgbotapi.PreCheckoutConfig)
	require.True(t, ok, "expected an answerPreCheckoutQuery request")
	assert.True(t, answer.OK)
	assert.Empty(t, answer.ErrorMessage)

	// Pre-checkout идёт мимо rate-limiter и generic-обработки: основной бот молчит.
	assert.Empty(t, mainBot.GetAllSentMessages())
}

func TestHandleUpdate_PreCheckoutInvalidAnswersNotOK(t *testing.T) {
	t.Parallel()

	// Неверная сумма: точное равенство XTR не масштабируется (D5).
	mockDB := testutil.NewBotStoreFake()
	starsPreCheckoutMocks(mockDB, database.OrderStatusPending)
	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), starsPreCheckoutUpdate(100, "3", stars.Currency))

	require.Len(t, paymentBot.AllRequestsSafe(), 1)
	answer, ok := paymentBot.AllRequestsSafe()[0].(tgbotapi.PreCheckoutConfig)
	require.True(t, ok)
	assert.False(t, answer.OK)
	assert.NotEmpty(t, answer.ErrorMessage)
}

func TestHandleUpdate_PreCheckoutDBErrorStillAnswers(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	mockDB.GetOrderByIDFunc = func(context.Context, uint) (*database.Order, error) {
		return nil, errors.New("db down")
	}
	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), starsPreCheckoutUpdate(199, "3", stars.Currency))

	require.Len(t, paymentBot.AllRequestsSafe(), 1)
	answer, ok := paymentBot.AllRequestsSafe()[0].(tgbotapi.PreCheckoutConfig)
	require.True(t, ok)
	assert.False(t, answer.OK, "pre-checkout must never be left unanswered")
}

func TestHandleUpdate_PreCheckoutFallsBackToMainBot(t *testing.T) {
	t.Parallel()

	// paymentBot не задан (тестовый Handler) → ответ уходит через основной бот.
	mockDB := testutil.NewBotStoreFake()
	starsPreCheckoutMocks(mockDB, database.OrderStatusPending)
	cfg := &config.Config{TelegramAdminID: 123, PaymentEnabled: true, PaymentProvider: database.PaymentProviderTelegramStars}
	mainBot := testutil.NewBotAPI()
	h := NewHandler(mainBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	o := service.NewOrderService(mockDB, nil, nil, nil, "testbot", cfg)
	o.SetStarsClient(stars.New(testutil.NewBotAPI()))
	h.SetOrderService(o)

	h.HandleUpdate(context.Background(), starsPreCheckoutUpdate(199, "3", stars.Currency))

	require.Len(t, mainBot.AllRequestsSafe(), 1)
	_, ok := mainBot.AllRequestsSafe()[0].(tgbotapi.PreCheckoutConfig)
	require.True(t, ok)
}

func successfulPaymentUpdate(chargeID, payload string, amount int) tgbotapi.Update {
	return tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 42},
			From: &tgbotapi.User{ID: 42, UserName: "user"},
			SuccessfulPayment: &tgbotapi.SuccessfulPayment{
				Currency:                stars.Currency,
				TotalAmount:             amount,
				InvoicePayload:          payload,
				TelegramPaymentChargeID: chargeID,
			},
		},
	}
}

func TestHandleUpdate_SuccessfulPaymentConfirmsAndNotifies(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsPreCheckoutMocks(mockDB, database.OrderStatusPending)
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true}, nil
	}
	mockDB.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 2, IsActive: true}, nil
	}
	mockDB.ConfirmOrderPaidCASFunc = func(_ context.Context, orderID uint, _, _ time.Time, _ *database.Subscription, _ *database.Product, _ database.ApplyPlanInTxFn, callbackAmountCents int64, providerPaymentID string, providerFeeCents *int64) (bool, error) {
		assert.Equal(t, uint(3), orderID)
		assert.Equal(t, int64(199), callbackAmountCents, "XTR amount must not be scaled ×100")
		assert.Equal(t, "charge-1", providerPaymentID)
		require.NotNil(t, providerFeeCents)
		assert.Equal(t, int64(0), *providerFeeCents, "Stars fee must be 0")

		return true, nil
	}
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	// Уведомление уходит через payment-инстанс: BuildPaidUserNotification → Send.
	sent := paymentBot.GetAllSentMessages()
	require.NotEmpty(t, sent, "paid notification must be sent via paymentBot")
	assert.Contains(t, sent[0].Text, "Оплата подтверждена")
	assert.Empty(t, mainBot.GetAllSentMessages())
}

func TestHandleUpdate_SuccessfulPaymentCanceledOrderAlertsUser(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsPreCheckoutMocks(mockDB, database.OrderStatusCanceled)
	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	// Деньги списаны, доступ отозван — сообщение о проблеме.
	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "отменённому заказу")
	assert.Contains(t, sent[0].Text, "/paysupport")
}

func TestHandleUpdate_SuccessfulPaymentServiceErrorAsksForSupport(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	mockDB.GetOrderByIDFunc = func(context.Context, uint) (*database.Order, error) {
		return nil, errors.New("db down")
	}
	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "/paysupport")
}

func TestHandleUpdate_SuccessfulPaymentDuplicatePaidIsSilentNoop(t *testing.T) {
	t.Parallel()

	// Повторная доставка successful_payment по уже оплаченному заказу:
	// идемпотентный no-op — без «отменённого заказа» и без повторного уведомления.
	mockDB := testutil.NewBotStoreFake()
	starsPreCheckoutMocks(mockDB, database.OrderStatusPaid)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	assert.Empty(t, paymentBot.GetAllSentMessages(), "duplicate paid delivery must not re-notify the user")
	assert.Empty(t, mainBot.GetAllSentMessages())
}

func TestHandleUpdate_SuccessfulPaymentNilOrderAsksSupportNotCanceled(t *testing.T) {
	t.Parallel()

	// Заказ ссылается на удалённый product: подтверждение возвращает
	// confirmation.Order == nil — сообщение в /paysupport, не «отменённый заказ».
	mockDB := testutil.NewBotStoreFake()
	starsPreCheckoutMocks(mockDB, database.OrderStatusPending)
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return nil, database.ErrProductNotFound
	}
	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "/paysupport")
	assert.NotContains(t, sent[0].Text, "отменённому заказу")
}

func TestStarsBuy_CallbackChatDiffersFromTelegramID(t *testing.T) {
	t.Parallel()

	// callback-чат (5000) и Telegram user ID покупателя (42) различны:
	// invoice уходит в private chat по telegram_id, карточка редактируется в chatID.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, 199)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb1",
			From: &tgbotapi.User{ID: 42, UserName: "user"},
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: 5000},
				MessageID: 99,
			},
			Data: "buy_product_1",
		},
	}

	err := h.HandleCallback(context.Background(), update)
	require.NoError(t, err)

	require.NotEmpty(t, paymentBot.AllChattablesSafe(), "invoice must be sent via paymentBot")
	invoice, ok := paymentBot.AllChattablesSafe()[0].(tgbotapi.InvoiceConfig)
	require.True(t, ok, "expected an InvoiceConfig sent via paymentBot")
	assert.Equal(t, int64(42), invoice.ChatID, "invoice must target the buyer telegram_id, not the callback chat")

	edit, ok := mainBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "expected the card to be edited in place")
	assert.Equal(t, int64(5000), edit.ChatID, "card must be edited in the callback chat")
	assert.Equal(t, 99, edit.MessageID)
}

func TestStarsInvoiceBack_DeletesOnlyInvoiceAndRendersList(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	mockDB.ListActiveProductsFunc = func(context.Context) ([]database.Product, error) {
		return []database.Product{
			{ID: 1, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true},
		}, nil
	}
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb1",
			From: &tgbotapi.User{ID: 42, UserName: "user"},
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: 42},
				MessageID: 99,
			},
			Data: "stars_invoice_back_55_1",
		},
	}

	err := h.HandleCallback(context.Background(), update)
	require.NoError(t, err)

	// Удалён ТОЛЬКО invoice (55), карточка (99) не удаляется.
	assert.Equal(t, []int{55}, paymentBot.DeletedMessageIDs)

	// Карточка отредактирована обратно в список тарифов, дубля нет.
	edits := mainBot.GetAllSentMessages()
	require.Len(t, edits, 1)
	assert.Equal(t, msg(MsgPremiumBenefits), edits[0].Text)

	edit, ok := mainBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "card must be edited in place, not re-sent")
	assert.Equal(t, 99, edit.MessageID)
	require.NotNil(t, edit.ReplyMarkup)
	require.Len(t, edit.ReplyMarkup.InlineKeyboard, 2)
	require.NotNil(t, edit.ReplyMarkup.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "buy_product_1", *edit.ReplyMarkup.InlineKeyboard[0][0].CallbackData)
}

func TestStarsInvoiceBack_InvalidPayloadIgnored(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:      "cb1",
			From:    &tgbotapi.User{ID: 42},
			Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 42}, MessageID: 99},
			Data:    "stars_invoice_back_garbage",
		},
	}

	err := h.HandleCallback(context.Background(), update)
	require.NoError(t, err)

	assert.Empty(t, paymentBot.DeletedMessageIDs)
	assert.Empty(t, mainBot.GetAllSentMessages())
}

func TestHandleRefund_NonAdminSilent(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	h, mainBot, _ := newStarsTestHandler(t, mockDB)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 42},
			From: &tgbotapi.User{ID: 42},
			Text: "/refund 3",
		},
	}

	err := h.HandleRefund(context.Background(), update)
	require.NoError(t, err)
	assert.Empty(t, mainBot.GetAllSentMessages(), "non-admin must be silently rejected")
}

func TestHandleRefund_AdminUsageAndSuccess(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	refunded := false
	mockDB.GetOrderByIDFunc = func(_ context.Context, id uint) (*database.Order, error) {
		return &database.Order{
			ID: 3, SubscriptionID: 7, Status: database.OrderStatusPaid,
			AmountCents: 199, Currency: stars.Currency,
			PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge-1",
		}, nil
	}
	mockDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
	mockDB.GetPlanByNameFunc = func(context.Context, string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: "free", IsActive: true}, nil
	}
	mockDB.ConfirmOrderPaidCASFunc = func(context.Context, uint, time.Time, time.Time, *database.Subscription, *database.Product, database.ApplyPlanInTxFn, int64, string, *int64) (bool, error) {
		return true, nil
	}
	mockDB.CancelPaidOrderAndDowngradeCASFunc = func(context.Context, string, string, time.Time, uint, database.ChargebackPlanInTxFn, func(*database.Order, *database.Subscription, bool) *database.AuditLog) (*database.ChargebackResult, error) {
		refunded = true

		return &database.ChargebackResult{Order: &database.Order{ID: 3}, WasPaid: true, Transitioned: true, Downgraded: false}, nil
	}
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	t.Run("usage", func(t *testing.T) {
		update := tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: 123},
				From: &tgbotapi.User{ID: 123},
				Text: "/refund",
			},
		}

		err := h.HandleRefund(context.Background(), update)
		require.NoError(t, err)

		messages := mainBot.GetAllSentMessages()
		require.Len(t, messages, 1)
		assert.Contains(t, messages[0].Text, "/refund <order_id>")
	})

	t.Run("success", func(t *testing.T) {
		update := tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat:     &tgbotapi.Chat{ID: 123},
				From:     &tgbotapi.User{ID: 123},
				Text:     "/refund 3",
				Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len("/refund")}},
			},
		}

		err := h.HandleRefund(context.Background(), update)
		require.NoError(t, err)

		assert.True(t, refunded, "RefundStarsOrder must reach the cancel CAS")

		// Покупатель получает уведомление о возврате через payment-инстанс:
		// заказ, сумма и валюта.
		notifications := paymentBot.GetAllSentMessages()
		require.Len(t, notifications, 1)
		assert.Equal(t, int64(42), notifications[0].ChatID, "refund notice must go to the buyer telegram_id")
		assert.Contains(t, notifications[0].Text, "#3")
		assert.Contains(t, notifications[0].Text, "199 XTR")

		// Админский success-текст сохранён.
		messages := mainBot.GetAllSentMessages()
		require.NotEmpty(t, messages)
		assert.Contains(t, messages[len(messages)-1].Text, "✅ Возврат по заказу #3")
	})

	t.Run("noop sends no user notification", func(t *testing.T) {
		noopedDB := testutil.NewBotStoreFake()
		noopedDB.GetOrderByIDFunc = func(context.Context, uint) (*database.Order, error) {
			// Заказ уже отменён: RefundStarsOrder — идемпотентный no-op (nil result).
			return &database.Order{
				ID: 3, SubscriptionID: 7, Status: database.OrderStatusCanceled,
				AmountCents: 199, Currency: stars.Currency,
				PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge-1",
			}, nil
		}
		noopH, noopMain, noopPayment := newStarsTestHandler(t, noopedDB)

		update := tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat:     &tgbotapi.Chat{ID: 123},
				From:     &tgbotapi.User{ID: 123},
				Text:     "/refund 3",
				Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len("/refund")}},
			},
		}

		err := noopH.HandleRefund(context.Background(), update)
		require.NoError(t, err)

		assert.Empty(t, noopPayment.GetAllSentMessages(), "no-op refund must not re-notify the buyer")

		messages := noopMain.GetAllSentMessages()
		require.NotEmpty(t, messages)
		assert.Contains(t, messages[len(messages)-1].Text, "✅ Возврат по заказу #3")
	})

	t.Run("user notification error keeps admin success", func(t *testing.T) {
		failedDB := testutil.NewBotStoreFake()
		failedDB.GetOrderByIDFunc = func(context.Context, uint) (*database.Order, error) {
			return &database.Order{
				ID: 3, SubscriptionID: 7, Status: database.OrderStatusPaid,
				AmountCents: 199, Currency: stars.Currency,
				PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge-1",
			}, nil
		}
		failedDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 7, TelegramID: 42}, nil
		}
		failedDB.GetPlanByNameFunc = func(context.Context, string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: "free", IsActive: true}, nil
		}
		failedDB.CancelPaidOrderAndDowngradeCASFunc = func(context.Context, string, string, time.Time, uint, database.ChargebackPlanInTxFn, func(*database.Order, *database.Subscription, bool) *database.AuditLog) (*database.ChargebackResult, error) {
			return &database.ChargebackResult{Order: &database.Order{ID: 3}, WasPaid: true, Transitioned: true, Downgraded: false}, nil
		}
		failedH, failedMain, failedPayment := newStarsTestHandler(t, failedDB)
		failedPayment.SendError = errors.New("telegram down")

		update := tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat:     &tgbotapi.Chat{ID: 123},
				From:     &tgbotapi.User{ID: 123},
				Text:     "/refund 3",
				Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len("/refund")}},
			},
		}

		err := failedH.HandleRefund(context.Background(), update)
		require.NoError(t, err, "user notification failure must not fail the refund")

		messages := failedMain.GetAllSentMessages()
		require.NotEmpty(t, messages)
		assert.Contains(t, messages[len(messages)-1].Text, "✅ Возврат по заказу #3")
	})
}

func TestHandlePaySupportAndTerms_Commands(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	h, mainBot, _ := newStarsTestHandler(t, mockDB)

	t.Run("paysupport", func(t *testing.T) {
		update := tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: 42},
				From: &tgbotapi.User{ID: 42},
				Text: "/paysupport",
			},
		}

		err := h.HandlePaySupport(context.Background(), update)
		require.NoError(t, err)
	})

	t.Run("terms", func(t *testing.T) {
		update := tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: 42},
				From: &tgbotapi.User{ID: 42},
				Text: "/terms",
			},
		}

		err := h.HandleTerms(context.Background(), update)
		require.NoError(t, err)
	})

	messages := mainBot.GetAllSentMessages()
	require.Len(t, messages, 2)
	assert.Equal(t, SupportText(), messages[0].Text)
	assert.Equal(t, TermsText(), messages[1].Text)
}
