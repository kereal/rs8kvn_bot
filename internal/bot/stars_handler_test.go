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

// starsInvoices extracts the InvoiceConfig payloads sent via bot in order.
func starsInvoices(bot *testutil.BotAPI) []tgbotapi.InvoiceConfig {
	var out []tgbotapi.InvoiceConfig
	for _, c := range bot.AllChattablesSafe() {
		if inv, ok := c.(tgbotapi.InvoiceConfig); ok {
			out = append(out, inv)
		}
	}

	return out
}

func TestParseStarsAcceptPayload(t *testing.T) {
	t.Parallel()

	orderID, productID, cardChatID, cardMsgID, ok := parseStarsAcceptPayload("3_7_42_99")
	require.True(t, ok)
	assert.Equal(t, uint(3), orderID)
	assert.Equal(t, uint(7), productID)
	assert.Equal(t, int64(42), cardChatID)
	assert.Equal(t, 99, cardMsgID)

	invalid := []string{
		"3_7_42",
		"0_7_42_99",
		"3_0_42_99",
		"3_7_0_99",
		"3_7_42_-1",
		"x_7_42_99",
	}
	for _, payload := range invalid {
		_, _, _, _, ok = parseStarsAcceptPayload(payload)
		assert.False(t, ok, payload)
	}
}

// starsOrderFixture is the canonical Stars order fixture: order 3 /
// subscription 7 / product 1, pending, 199 XTR via telegram_stars.
type starsOrderFixture struct {
	order      *database.Order
	product    *database.Product
	productErr error
	orderErr   error
}

// starsOrderOpt tunes the order fixture built by starsOrderMocks.
type starsOrderOpt func(*starsOrderFixture)

func withStarsStatus(status database.OrderStatus) starsOrderOpt {
	return func(f *starsOrderFixture) { f.order.Status = status }
}

func withStarsCharge(chargeID string) starsOrderOpt {
	return func(f *starsOrderFixture) { f.order.ProviderPaymentID = chargeID }
}

func withStarsProductErr(err error) starsOrderOpt {
	return func(f *starsOrderFixture) { f.productErr = err }
}

func withStarsOrderErr(err error) starsOrderOpt {
	return func(f *starsOrderFixture) { f.orderErr = err }
}

// starsOrderMocks wires the fake DB for the canonical Stars order fixture.
// The fixture serves the buy flow (FindOrCreatePendingPaymentOrder), the
// pre-checkout flow and the successful-payment flow (GetOrderByID) alike.
func starsOrderMocks(mockDB *testutil.BotStoreFake, opts ...starsOrderOpt) {
	f := &starsOrderFixture{
		order: &database.Order{
			ID: 3, SubscriptionID: 7, ProductID: 1, Status: database.OrderStatusPending,
			AmountCents: 199, Currency: stars.Currency, PaymentProvider: database.PaymentProviderTelegramStars,
		},
		product: &database.Product{ID: 1, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true},
	}
	for _, opt := range opts {
		opt(f)
	}

	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return f.product, f.productErr
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
	mockDB.FindOrCreatePendingPaymentOrderFunc = func(_ context.Context, _, _ uint, provider string, amountCents int64, _ string, expiresAt *time.Time, now time.Time) (*database.Order, bool, error) {
		if provider != database.PaymentProviderTelegramStars {
			return nil, false, errors.New("fake: expected telegram_stars provider")
		}

		order := &database.Order{
			ID: 3, SubscriptionID: 7, ProductID: 1, Status: database.OrderStatusPending,
			AmountCents: amountCents, Currency: stars.Currency, CreatedAt: now,
		}
		if expiresAt != nil {
			order.PaymentExpiresAt = expiresAt
		}

		return order, true, nil
	}
	mockDB.GetOrderByIDFunc = func(_ context.Context, id uint) (*database.Order, error) {
		if f.orderErr != nil {
			return nil, f.orderErr
		}

		order := *f.order
		order.ID = id

		return &order, nil
	}
	mockDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
}

func starsInlineKeyboard(t *testing.T, markup any) tgbotapi.InlineKeyboardMarkup {
	t.Helper()

	keyboard, ok := markup.(*tgbotapi.InlineKeyboardMarkup)
	require.True(t, ok, "expected *tgbotapi.InlineKeyboardMarkup")

	return *keyboard
}

func starsEditByMessageID(t *testing.T, bot *testutil.BotAPI, messageID int) tgbotapi.EditMessageTextConfig {
	t.Helper()

	var found tgbotapi.EditMessageTextConfig
	ok := false

	for _, chattable := range bot.AllChattablesSafe() {
		edit, isEdit := chattable.(tgbotapi.EditMessageTextConfig)
		if isEdit && edit.MessageID == messageID {
			found = edit
			ok = true
		}
	}

	if !ok {
		t.Fatalf("edit for message %d not found", messageID)
	}

	return found
}

func TestStarsBuy_FirstClickSendsTermsAcceptanceNoInvoice(t *testing.T) {
	t.Parallel()

	// Invoice получить без явного acceptance-callback НЕЛЬЗЯ: первый клик
	// создаёт pending-заказ и отправляет сообщение с условиями и кнопкой.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	err := h.handleBuyProduct(context.Background(), 42, 42, "user", 99, 1)
	require.NoError(t, err)

	// Ни одного invoice через payment-инстанс до acceptance.
	for _, c := range paymentBot.AllChattablesSafe() {
		_, isInvoice := c.(tgbotapi.InvoiceConfig)
		assert.False(t, isInvoice, "no invoice must be sent before the acceptance callback")
	}

	// Сообщение с условиями уходит покупателю в личный чат через payment-инстанс.
	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Equal(t, int64(42), sent[0].ChatID)
	assert.Contains(t, sent[0].Text, "Пользовательское соглашение (/terms)")
	assert.Contains(t, sent[0].Text, "поддержка Telegram НЕ помогает")

	// Кнопка согласия несёт order/product/card message id.
	require.Len(t, paymentBot.AllChattablesSafe(), 1)
	acceptMsg, ok := paymentBot.AllChattablesSafe()[0].(tgbotapi.MessageConfig)
	require.True(t, ok, "expected the terms acceptance MessageConfig")
	acceptKeyboard := starsInlineKeyboard(t, acceptMsg.ReplyMarkup)
	require.Len(t, acceptKeyboard.InlineKeyboard, 1)
	require.NotNil(t, acceptKeyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "stars_accept_3_1_42_99", *acceptKeyboard.InlineKeyboard[0][0].CallbackData)

	// Карточка тарифа отредактирована: подтверждение уходит в личный чат.
	edit, ok := mainBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "expected the card to be edited in place")
	assert.Contains(t, edit.Text, "Подтвердите условия оплаты в личном чате")
	assert.NotContains(t, edit.Text, "Счёт отправлен ниже", "card must not claim the invoice was sent")
}

func TestStarsBuy_AcceptanceCallbackSendsInvoiceOnce(t *testing.T) {
	t.Parallel()

	// Acceptance-callback переиспользует pending-заказ (reuse по RequestPayment)
	// и отправляет ровно один invoice; повторный callback дубликат не создаёт.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	require.NoError(t, h.handleBuyProduct(context.Background(), 42, 42, "user", 99, 1))

	acceptCallback := func(id string) tgbotapi.Update {
		return tgbotapi.Update{
			CallbackQuery: &tgbotapi.CallbackQuery{
				ID:   id,
				From: &tgbotapi.User{ID: 42, UserName: "user"},
				Message: &tgbotapi.Message{
					Chat:      &tgbotapi.Chat{ID: 42},
					MessageID: 77,
				},
				Data: "stars_accept_3_1_42_99",
			},
		}
	}

	require.NoError(t, h.HandleCallback(context.Background(), acceptCallback("cb1")))

	invoices := starsInvoices(paymentBot)
	require.Len(t, invoices, 1, "exactly one invoice must be sent after acceptance")
	assert.Equal(t, int64(42), invoices[0].ChatID)
	assert.Equal(t, "3", invoices[0].Payload)

	// Кнопка согласия снята: сообщение перезаписано без ReplyMarkup.
	acceptDone := starsEditByMessageID(t, mainBot, 77)
	assert.Contains(t, acceptDone.Text, "счёт отправлен ниже")

	// Карточка переведена в «Счёт отправлен» с Back на invoice.
	cardEdit := starsEditByMessageID(t, mainBot, 99)
	assert.Contains(t, cardEdit.Text, "Счёт отправлен в личном чате")
	cardKeyboard := starsInlineKeyboard(t, cardEdit.ReplyMarkup)
	require.NotNil(t, cardKeyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "stars_invoice_back_42_1_1", *cardKeyboard.InlineKeyboard[0][0].CallbackData)

	// Повторный (протухший) acceptance-callback: второй invoice не создаётся.
	require.NoError(t, h.HandleCallback(context.Background(), acceptCallback("cb2")))
	invoices = starsInvoices(paymentBot)
	assert.Len(t, invoices, 1, "duplicate acceptance must not send a second invoice")
}

func TestStarsBuy_AcceptanceSendErrorShowsSeparateText(t *testing.T) {
	t.Parallel()

	// M5: отправка сообщения согласия не удалась — отдельный текст,
	// не «счёт отправлен» и не второй invoice.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	paymentBot.SendError = errors.New("telegram down")

	err := h.handleBuyProduct(context.Background(), 42, 42, "user", 99, 1)
	require.NoError(t, err)

	messages := mainBot.GetAllSentMessages()
	require.Len(t, messages, 2)
	assert.Equal(t, msg(MsgInvoiceSendFailed), messages[1].Text)
	assert.Empty(t, starsInvoices(paymentBot), "no invoice must be sent on acceptance failure")
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
	mockDB.FindOrCreatePendingPaymentOrderFunc = func(context.Context, uint, uint, string, int64, string, *time.Time, time.Time) (*database.Order, bool, error) {
		return &database.Order{ID: 3, SubscriptionID: 7, ProductID: 1, Status: database.OrderStatusPending, AmountCents: 199, Currency: stars.Currency}, false, nil
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

// TestHandleUpdate_PreCheckout covers the pre-checkout flow: the answer must
// ALWAYS be sent (within Telegram's 10s window), through paymentBot when it is
// wired, otherwise through the main bot. Pre-checkout bypasses the rate
// limiter and generic handling.
func TestHandleUpdate_PreCheckout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		mockDB         func(*testutil.BotStoreFake)
		update         tgbotapi.Update
		noPaymentBot   bool
		wantAnswerOK   bool
		wantErrorEmpty bool
		wantErrorSet   bool
		wantCount      int
	}{
		{
			name:           "valid answers OK",
			mockDB:         func(db *testutil.BotStoreFake) { starsOrderMocks(db) },
			update:         starsPreCheckoutUpdate(199, "3", stars.Currency),
			wantAnswerOK:   true,
			wantErrorEmpty: true,
			wantCount:      1,
		},
		{
			name:   "invalid amount answers not OK",
			mockDB: func(db *testutil.BotStoreFake) { starsOrderMocks(db) },
			// Неверная сумма: точное равенство XTR не масштабируется (D5).
			update:       starsPreCheckoutUpdate(100, "3", stars.Currency),
			wantErrorSet: true,
			wantCount:    1,
		},
		{
			name:   "db error still answers",
			mockDB: func(db *testutil.BotStoreFake) { starsOrderMocks(db, withStarsOrderErr(errors.New("db down"))) },
			// Pre-checkout must never be left unanswered.
			update:    starsPreCheckoutUpdate(199, "3", stars.Currency),
			wantCount: 1,
		},
		{
			name:   "falls back to main bot without payment bot",
			mockDB: func(db *testutil.BotStoreFake) { starsOrderMocks(db) },
			// paymentBot не задан (тестовый Handler) → ответ уходит через основной бот.
			update:       starsPreCheckoutUpdate(199, "3", stars.Currency),
			noPaymentBot: true,
			wantAnswerOK: true,
			wantCount:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockDB := testutil.NewBotStoreFake()
			tt.mockDB(mockDB)

			var mainBot, paymentBot *testutil.BotAPI
			var h *Handler
			if tt.noPaymentBot {
				cfg := &config.Config{TelegramAdminID: 123, PaymentEnabled: true, PaymentProvider: database.PaymentProviderTelegramStars}
				mainBot = testutil.NewBotAPI()
				h = NewHandler(mainBot, cfg, mockDB, NewTestBotConfig(), nil, "")
				o := service.NewOrderService(mockDB, nil, nil, nil, "testbot", cfg)
				o.SetStarsClient(stars.New(testutil.NewBotAPI()))
				h.SetOrderService(o)
			} else {
				h, mainBot, paymentBot = newStarsTestHandler(t, mockDB)
			}

			h.HandleUpdate(context.Background(), tt.update)

			// Ответ уходит ВСЕГДА и через payment-инстанс (или основной бот).
			answerBot := paymentBot
			if tt.noPaymentBot {
				answerBot = mainBot
			}
			require.Len(t, answerBot.AllRequestsSafe(), tt.wantCount)
			answer, ok := answerBot.AllRequestsSafe()[0].(tgbotapi.PreCheckoutConfig)
			require.True(t, ok, "expected an answerPreCheckoutQuery request")
			assert.Equal(t, tt.wantAnswerOK, answer.OK)

			if tt.wantErrorEmpty {
				assert.Empty(t, answer.ErrorMessage)
			}
			if tt.wantErrorSet {
				assert.NotEmpty(t, answer.ErrorMessage)
			}

			// Pre-checkout идёт мимо rate-limiter и generic-обработки: основной бот молчит.
			if !tt.noPaymentBot {
				assert.Empty(t, mainBot.GetAllSentMessages())
			}
		})
	}
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
	starsOrderMocks(mockDB)
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
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusCanceled))
	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	// Ошибочная оплата отменённого заказа автоматически возвращена.
	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "Платёж возвращён")
	assert.Contains(t, sent[0].Text, "/paysupport")
}

func TestHandleUpdate_SuccessfulPaymentServiceErrorAsksForSupport(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsOrderErr(errors.New("db down")))
	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "/paysupport")
}

func TestHandleUpdate_SuccessfulPaymentDifferentChargeAutoRefunds(t *testing.T) {
	t.Parallel()

	// Новый charge на уже оплаченном заказе (двойная оплата той же покупки):
	// charge возвращается автоматически, покупателю — «Платёж возвращён».
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusPaid))
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1, "a different charge on a paid order must be refunded and reported")
	assert.Contains(t, sent[0].Text, "Платёж возвращён")
	assert.Empty(t, mainBot.GetAllSentMessages())
}

func TestHandleUpdate_SuccessfulPaymentSameChargeIsSilentNoop(t *testing.T) {
	t.Parallel()

	// Повторная доставка successful_payment (тот же charge ID на paid-заказе):
	// идемпотентный no-op — без второго продления и без уведомлений.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusPaid), withStarsCharge("charge-1"))
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	assert.Empty(t, paymentBot.GetAllSentMessages(), "same-charge redelivery must be a silent no-op")
	assert.Empty(t, mainBot.GetAllSentMessages())
}

func TestHandleUpdate_SuccessfulPaymentDeletedProductAutoRefunds(t *testing.T) {
	t.Parallel()

	// Заказ ссылается на удалённый product: активировать нечего — charge
	// возвращается автоматически, покупателю сообщают о возврате (не
	// «отменённый заказ», ручной разбор не требуется).
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsProductErr(database.ErrProductNotFound))
	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "Платёж возвращён")
	assert.NotContains(t, sent[0].Text, "отменённому заказу")
}

func TestStarsBuy_CallbackChatDiffersFromTelegramID(t *testing.T) {
	t.Parallel()

	// callback-чат (5000) и Telegram user ID покупателя (42) различны:
	// Terms-сообщение уходит в private chat по telegram_id, карточка
	// редактируется в callback chat.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
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

	// Terms-сообщение уходит покупателю в private chat через payment-инстанс.
	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Equal(t, int64(42), sent[0].ChatID, "terms acceptance must target the buyer telegram_id, not the callback chat")
	assert.Contains(t, sent[0].Text, "Принимаю условия")

	// Кнопка согласия несёт card message id (99) для последующего Back.
	require.Len(t, paymentBot.AllChattablesSafe(), 1)
	acceptMsg, ok := paymentBot.AllChattablesSafe()[0].(tgbotapi.MessageConfig)
	require.True(t, ok)
	acceptKeyboard := starsInlineKeyboard(t, acceptMsg.ReplyMarkup)
	require.NotNil(t, acceptKeyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "stars_accept_3_1_5000_99", *acceptKeyboard.InlineKeyboard[0][0].CallbackData)

	edit, ok := mainBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "expected the card to be edited in place")
	assert.Equal(t, int64(5000), edit.ChatID, "card must be edited in the callback chat")
	assert.Equal(t, 99, edit.MessageID)
}

func TestStarsAccept_CallbackChatDiffersFromBuyerChat(t *testing.T) {
	t.Parallel()

	// Acceptance в личном чате покупателя (42): invoice отправляется в чат 42,
	// карточка (callback chat 5000, msg 99 из кнопки) редактируется в chatID.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb1",
			From: &tgbotapi.User{ID: 42, UserName: "user"},
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: 42},
				MessageID: 77,
			},
			Data: "stars_accept_3_1_5000_99",
		},
	}

	err := h.HandleCallback(context.Background(), update)
	require.NoError(t, err)

	invoices := starsInvoices(paymentBot)
	require.Len(t, invoices, 1)
	assert.Equal(t, int64(42), invoices[0].ChatID, "invoice must target the buyer telegram_id")

	// Карточка (99 в чате 5000) отредактирована с кнопкой Back на invoice.
	cardEdit := starsEditByMessageID(t, mainBot, 99)
	assert.Equal(t, int64(5000), cardEdit.ChatID, "card must be edited in the callback chat")
	assert.Equal(t, 99, cardEdit.MessageID)
}

func TestStarsAccept_InFlightDoubleTapSendsSingleInvoice(t *testing.T) {
	t.Parallel()

	// Параллельный double-tap acceptance: in-flight guard пропускает первого,
	// второй уходит — invoice отправляется ровно один. Первый тап удерживает
	// guard, блокируясь в отправке invoice; второй тап гарантированно
	// наталкивается на коллизию LoadOrStore.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	sendGate := make(chan struct{})
	releaseSend := make(chan struct{})
	firstSend := false
	paymentBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if !firstSend {
			firstSend = true
			close(sendGate)
			<-releaseSend
		}

		return tgbotapi.Message{MessageID: 55}, nil
	}

	accept := func() tgbotapi.Update {
		return tgbotapi.Update{
			CallbackQuery: &tgbotapi.CallbackQuery{
				ID:   "cb1",
				From: &tgbotapi.User{ID: 42, UserName: "user"},
				Message: &tgbotapi.Message{
					Chat:      &tgbotapi.Chat{ID: 42},
					MessageID: 77,
				},
				Data: "stars_accept_3_1_5000_99",
			},
		}
	}

	done := make(chan error, 1)
	go func() { done <- h.HandleCallback(context.Background(), accept()) }()
	<-sendGate

	// Второй тап синхронно: guard удержан первым — подавлен без нового invoice.
	// (Не запрашиваем фейк под гейтом: Send держит свой мьютекс внутри SendFunc.)
	require.NoError(t, h.HandleCallback(context.Background(), accept()))

	close(releaseSend)
	require.NoError(t, <-done)

	// Ровно один invoice и две карточных правки (только победившим тапом).
	invoices := starsInvoices(paymentBot)
	require.Len(t, invoices, 1, "a concurrent double-tap must produce exactly one invoice")

	// Карточка и accept-done редактируются ровно по одному разу (только
	// победившим тапом); проигравший ничего не отправляет.
	edits := mainBot.GetAllSentMessages()
	require.Len(t, edits, 2)
	assert.Contains(t, edits[0].Text, "счёт отправлен ниже")
	assert.Contains(t, edits[1].Text, "Счёт отправлен в личном чате")
}

func TestStarsAccept_RedeliveryAfterSuccessIsSilentNoop(t *testing.T) {
	t.Parallel()

	// Повторный callback после успешного invoice (sent-set): дубликат
	// подавляется молча — ни второго invoice, ни карточных правок.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	accept := func() tgbotapi.Update {
		return tgbotapi.Update{
			CallbackQuery: &tgbotapi.CallbackQuery{
				ID:   "cb1",
				From: &tgbotapi.User{ID: 42, UserName: "user"},
				Message: &tgbotapi.Message{
					Chat:      &tgbotapi.Chat{ID: 42},
					MessageID: 77,
				},
				Data: "stars_accept_3_1_5000_99",
			},
		}
	}

	require.NoError(t, h.HandleCallback(context.Background(), accept()))
	require.Len(t, starsInvoices(paymentBot), 1)
	baseEdits := len(mainBot.GetAllSentMessages())

	require.NoError(t, h.HandleCallback(context.Background(), accept()))
	require.Len(t, starsInvoices(paymentBot), 1, "redelivery must not re-invoice")
	assert.Len(t, mainBot.GetAllSentMessages(), baseEdits, "redelivery must not re-edit anything")
}

func TestStarsBuy_InvoiceSendFailureExpiresPendingOrder(t *testing.T) {
	t.Parallel()

	// Сбой отправки Terms-сообщения: заказ остался pending — сразу истекаем,
	// чтобы повторная покупка создала новый заказ, а не упиралась в
	// «счёт уже отправлен» по несуществующему счёту.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)

	expired := false
	mockDB.ExpirePendingOrderFunc = func(context.Context, uint) (bool, error) {
		expired = true

		return true, nil
	}

	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)
	paymentBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		return tgbotapi.Message{}, errors.New("network error")
	}

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
	require.NoError(t, err, "send failure is a recoverable handler error, not a bot error")

	assert.True(t, expired, "pending order must be expired so a retry creates a new one")

	// Карточка отредактирована в buy-состояние, но не в «счёт отправлен».
	sent := mainBot.GetAllSentMessages()
	require.NotEmpty(t, sent)
	assert.NotContains(t, sent[len(sent)-1].Text, "счёт отправлен")
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
			Data: "stars_invoice_back_42_55_1",
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
	editKeyboard := starsInlineKeyboard(t, edit.ReplyMarkup)
	require.Len(t, editKeyboard.InlineKeyboard, 2)
	require.NotNil(t, editKeyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "buy_product_1", *editKeyboard.InlineKeyboard[0][0].CallbackData)
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

func TestStarsInvoiceBack_ForeignUserIgnored(t *testing.T) {
	t.Parallel()

	// Чужой в групповом чате жмёт «⬅️ Назад» на чужой карточке: кнопка несёт
	// telegram_id покупателя (42), нажавший (43) — не покупатель. Ничего не
	// удаляется (в противном случае удалялось бы произвольное сообщение с тем
	// же message_id в ЛИЧНОМ чате чужака) и ничего не редактируется.
	mockDB := testutil.NewBotStoreFake()
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb1",
			From: &tgbotapi.User{ID: 43, UserName: "stranger"},
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: 5000},
				MessageID: 99,
			},
			Data: "stars_invoice_back_42_55_1",
		},
	}

	err := h.HandleCallback(context.Background(), update)
	require.NoError(t, err)

	assert.Empty(t, paymentBot.DeletedMessageIDs, "foreign user must not delete the invoice")
	assert.Empty(t, mainBot.GetAllSentMessages(), "foreign user must not re-render the tariff list")
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

// drainRateLimiter исчерпывает бакет chatID в общем rate limiter.
func drainRateLimiter(h *Handler, chatID int64) {
	for range config.RateLimiterMaxTokens {
		h.checkRateLimit(chatID)
	}
}

func TestHandleUpdate_SuccessfulPaymentBypassesRateLimiter(t *testing.T) {
	t.Parallel()

	// Финансовый апдейт не отбрасывается rate limiter-ом: бакет исчерпан,
	// successful_payment всё равно доходит до подтверждения.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	mockDB.ConfirmOrderPaidCASFunc = func(context.Context, uint, time.Time, time.Time, *database.Subscription, *database.Product, database.ApplyPlanInTxFn, int64, string, *int64) (bool, error) {
		return true, nil
	}
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	drainRateLimiter(h, 42)
	assert.False(t, h.checkRateLimit(42), "precondition: rate limit exhausted")

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.NotEmpty(t, sent, "successful_payment must be processed despite exhausted rate limit")
	assert.Contains(t, sent[0].Text, "Оплата подтверждена")
	assert.Empty(t, mainBot.GetAllSentMessages())
}

func TestHandleUpdate_PaySupportTermsBypassRateLimiter(t *testing.T) {
	t.Parallel()

	// /paysupport и /terms доступны вне rate limit (Telegram compliance 6.2):
	// заблокированный пользователь всё равно получает текст.
	mockDB := testutil.NewBotStoreFake()
	h, mainBot, _ := newStarsTestHandler(t, mockDB)

	drainRateLimiter(h, 42)
	assert.False(t, h.checkRateLimit(42), "precondition: rate limit exhausted")

	paysupport := tgbotapi.Update{Message: &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 42}, From: &tgbotapi.User{ID: 42}, Text: "/paysupport",
		Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 11}},
	}}
	terms := tgbotapi.Update{Message: &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 42}, From: &tgbotapi.User{ID: 42}, Text: "/terms",
		Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 6}},
	}}

	require.True(t, paysupport.Message.IsCommand())
	require.Equal(t, "paysupport", paysupport.Message.Command())
	require.True(t, terms.Message.IsCommand())
	require.Equal(t, "terms", terms.Message.Command())

	h.HandleUpdate(context.Background(), paysupport)
	h.HandleUpdate(context.Background(), terms)

	messages := mainBot.GetAllSentMessages()
	require.Len(t, messages, 2, "both commands must be answered despite exhausted rate limit")
	assert.Contains(t, messages[0].Text, "Поддержка Telegram не решает вопросы оплаты")
	assert.Contains(t, messages[1].Text, "7.7.")
}

func TestFilterProductsByProvider_Symmetric(t *testing.T) {
	t.Parallel()

	products := []database.Product{
		{ID: 1, Name: "XTR", PriceCents: 199, Currency: stars.Currency, IsActive: true},
		{ID: 2, Name: "RUB", PriceCents: 19900, Currency: "RUB", IsActive: true},
	}

	// Stars: только XTR.
	h, _, _ := newStarsTestHandler(t, testutil.NewBotStoreFake())
	filtered := h.getSubHandler().filterProductsByProvider(products)
	require.Len(t, filtered, 1)
	assert.Equal(t, uint(1), filtered[0].ID)

	// Platega: XTR исключён, RUB остаётся.
	plategaCfg := &config.Config{TelegramAdminID: 123, PaymentEnabled: true, PaymentProvider: database.PaymentProviderPlatega}
	ph := NewHandler(testutil.NewBotAPI(), plategaCfg, testutil.NewBotStoreFake(), NewTestBotConfig(), nil, "")
	filtered = ph.getSubHandler().filterProductsByProvider(products)
	require.Len(t, filtered, 1)
	assert.Equal(t, uint(2), filtered[0].ID)
}

func TestStarsInvoiceBack_DeletesInvoiceInBuyerChat(t *testing.T) {
	t.Parallel()

	// Back: invoice удаляется в личном чате покупателя (telegram user ID 42),
	// карточка (callback chat 5000, msg 99) редактируется в исходном чате.
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
				Chat:      &tgbotapi.Chat{ID: 5000},
				MessageID: 99,
			},
			Data: "stars_invoice_back_42_55_1",
		},
	}

	err := h.HandleCallback(context.Background(), update)
	require.NoError(t, err)

	assert.Equal(t, []int{55}, paymentBot.DeletedMessageIDs, "invoice must be deleted via payment API")

	edit, ok := mainBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "card must be edited in place")
	assert.Equal(t, int64(5000), edit.ChatID, "card must be edited in the callback chat")
	assert.Equal(t, 99, edit.MessageID)
}

func TestHandleRefund_SplitBrainStateError(t *testing.T) {
	t.Parallel()

	// Split-brain: Telegram вернул Stars, но CAS/состояние не применилось —
	// админу сообщают, что деньги УЖЕ возвращены и нужен reconcile; повторный
	// /refund не предлагается и «возврат не выполнен» не говорится.
	mockDB := testutil.NewBotStoreFake()
	mockDB.GetOrderByIDFunc = func(context.Context, uint) (*database.Order, error) {
		return &database.Order{
			ID: 3, SubscriptionID: 7, Status: database.OrderStatusPaid,
			AmountCents: 199, Currency: stars.Currency,
			PaymentProvider: database.PaymentProviderTelegramStars, ProviderPaymentID: "charge-1",
		}, nil
	}
	mockDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
	mockDB.CancelPaidOrderAndDowngradeCASFunc = func(context.Context, string, string, time.Time, uint, database.ChargebackPlanInTxFn, func(*database.Order, *database.Subscription, bool) *database.AuditLog) (*database.ChargebackResult, error) {
		return nil, errors.New("cas conflict")
	}
	h, mainBot, _ := newStarsTestHandler(t, mockDB)

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

	messages := mainBot.GetAllSentMessages()
	require.NotEmpty(t, messages)
	text := messages[len(messages)-1].Text
	assert.Contains(t, text, "УЖЕ возвращены")
	assert.Contains(t, text, "reconcile")
	assert.Contains(t, text, "#3")
	assert.NotContains(t, text, "не выполнен", "must not claim the refund failed")
	assert.NotContains(t, text, "Проверьте статус заказа и баланс")
}

func TestHandleUpdate_SuccessfulPaymentAutoRefunded(t *testing.T) {
	t.Parallel()

	// Canceled заказ: сервис автовозвращает Stars — покупателю уходит понятное
	// «оплата возвращена», а не предупреждение об отменённом заказе.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusCanceled))
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "возвращён")
	assert.NotContains(t, sent[0].Text, "отменённому заказу")
	assert.Empty(t, mainBot.GetAllSentMessages())
}
