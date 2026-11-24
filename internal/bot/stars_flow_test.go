package bot

import (
	"context"
	"errors"
	"strings"
	"sync"
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

	return newStarsTestHandlerWithStarsAPI(t, mockDB, nil)
}

// newStarsTestHandlerWithStarsAPI — как newStarsTestHandler, но Stars-клиент
// сервиса заказов можно перехватить (проверка refundStarPayment: charge id,
// транспортные ошибки возврата).
func newStarsTestHandlerWithStarsAPI(t *testing.T, mockDB *testutil.BotStoreFake, starsAPI *testutil.BotAPI) (*Handler, *testutil.BotAPI, *testutil.BotAPI) {
	t.Helper()

	cfg := &config.Config{TelegramAdminID: 123, PaymentEnabled: true, PaymentProvider: database.PaymentProviderTelegramStars}
	mainBot := testutil.NewBotAPI()
	paymentBot := testutil.NewBotAPI()

	h := NewHandler(mainBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	h.SetPaymentBot(paymentBot)

	if starsAPI == nil {
		starsAPI = testutil.NewBotAPI()
	}

	syncSvc := service.NewSyncService(mockDB, nil, nil)
	subSvc := service.NewSubscriptionService(mockDB, nil, nil, cfg)
	o := service.NewOrderService(mockDB, subSvc, syncSvc, nil, "testbot", cfg)
	o.SetStarsClient(stars.New(starsAPI))
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

// starsOrderFixture is the canonical Stars order fixture: order 3 /
// subscription 7 / product 1 (plan 2), pending, 199 XTR via telegram_stars.
type starsOrderFixture struct {
	order      *database.Order
	product    *database.Product
	plan       *database.Plan
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

func withStarsProductActive(active bool) starsOrderOpt {
	return func(f *starsOrderFixture) { f.product.IsActive = active }
}

func withStarsPlanActive(active bool) starsOrderOpt {
	return func(f *starsOrderFixture) { f.plan.IsActive = active }
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
		product: &database.Product{ID: 1, PlanID: 2, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true},
		plan:    &database.Plan{ID: 2, IsActive: true},
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
		return f.plan, nil
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

// starsAcceptUpdate собирает acceptance-callback: чат callback-сообщения и
// cardChatID в payload совпадают (валидный сценарий).
func starsAcceptUpdate(data string, chatID int64, messageID int) tgbotapi.Update {
	return tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb1",
			From: &tgbotapi.User{ID: 42, UserName: "user"},
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: chatID},
				MessageID: messageID,
			},
			Data: data,
		},
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

// drainRateLimiter исчерпывает бакет chatID в общем rate limiter.
func drainRateLimiter(h *Handler, chatID int64) {
	for range config.RateLimiterMaxTokens {
		h.checkRateLimit(chatID)
	}
}

// starsRefundCall — зафиксированный вызов refundStarPayment.
type starsRefundCall struct {
	userID   string
	chargeID string
}

// captureStarsRefunds перехватывает refundStarPayment Stars-клиента.
func captureStarsRefunds(starsAPI *testutil.BotAPI) *[]starsRefundCall {
	calls := &[]starsRefundCall{}

	var mu sync.Mutex

	starsAPI.MakeRequestFunc = func(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
		if endpoint == "refundStarPayment" {
			mu.Lock()
			*calls = append(*calls, starsRefundCall{
				userID:   params["user_id"],
				chargeID: params["telegram_payment_charge_id"],
			})
			mu.Unlock()
		}

		return &tgbotapi.APIResponse{Ok: true}, nil
	}

	return calls
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
		update := starsAcceptUpdate("stars_accept_3_1_42_99", 42, 77)
		update.CallbackQuery.ID = id

		return update
	}

	require.NoError(t, h.HandleCallback(context.Background(), acceptCallback("cb1")))

	invoices := starsInvoices(paymentBot)
	require.Len(t, invoices, 1, "exactly one invoice must be sent after acceptance")
	assert.Equal(t, int64(42), invoices[0].ChatID)
	assert.Equal(t, "3", invoices[0].Payload)

	// Кнопка согласия снята: сообщение перезаписано без ReplyMarkup.
	acceptDone := starsEditByMessageID(t, mainBot, 77)
	assert.Contains(t, acceptDone.Text, "счёт отправлен ниже")

	// Карточка переведена в «Счёт отправлен» с Back на invoice (формат несёт
	// order_id pending-заказа вместо неиспользуемого product_id).
	cardEdit := starsEditByMessageID(t, mainBot, 99)
	assert.Contains(t, cardEdit.Text, "Счёт отправлен в личном чате")
	cardKeyboard := starsInlineKeyboard(t, cardEdit.ReplyMarkup)
	require.NotNil(t, cardKeyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "stars_invoice_back_42_1_3", *cardKeyboard.InlineKeyboard[0][0].CallbackData)

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

func TestStarsBuy_ReusePendingBeforeAcceptanceShowsAwaitingText(t *testing.T) {
	t.Parallel()

	// Повторный клик по существующему pending-заказу до acceptance: invoice ещё
	// НЕ отправлен (он выставляется только из acceptance-callback), поэтому
	// «нажмите на счёт» вводит в заблуждение — ждём текст про подтверждение
	// условий в личном чате.
	mockDB := testutil.NewBotStoreFake()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, PlanID: 2, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true}, nil
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
	assert.Equal(t, msg(MsgPaymentAwaitingAcceptance), messages[0].Text)
}

func TestStarsBuy_ReusePendingAfterAcceptanceShowsInvoiceHint(t *testing.T) {
	t.Parallel()

	// После успешного acceptance (invoice отправлен, order ID в sent-set)
	// повторный клик ведёт покупателя к уже выставленному счёту.
	mockDB := testutil.NewBotStoreFake()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, PlanID: 2, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true}, nil
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

	h.starsSentInvoices.Add(3)

	err := h.handleBuyProduct(context.Background(), 42, 42, "user", 99, 1)
	require.NoError(t, err)

	assert.Empty(t, paymentBot.AllChattablesSafe(), "no second invoice must be created")
	messages := mainBot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, msg(MsgPaymentInProgressStars), messages[0].Text)
}

func TestStarsAccept_ExpiredOrderShowsNotPendingText(t *testing.T) {
	t.Parallel()

	// Acceptance по истёкшему заказу: «счёт уже запущен» неверно — заказ мёртв,
	// нужен текст с предложением выбрать тариф заново.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusExpired))
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	require.NoError(t, h.HandleCallback(context.Background(), starsAcceptUpdate("stars_accept_3_1_42_99", 42, 77)))

	assert.Empty(t, starsInvoices(paymentBot), "expired order must not be invoiced")
	edit := starsEditByMessageID(t, mainBot, 77)
	assert.Equal(t, msg(MsgStarsOrderNotPending), edit.Text)
}

func TestStarsAccept_PaidOrderShowsAlreadyPaidText(t *testing.T) {
	t.Parallel()

	// Acceptance по оплаченному заказу (протухшая кнопка согласия): покупателю
	// сообщают, что оплата получена и подписка активна.
	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusPaid))
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	require.NoError(t, h.HandleCallback(context.Background(), starsAcceptUpdate("stars_accept_3_1_42_99", 42, 77)))

	assert.Empty(t, starsInvoices(paymentBot), "paid order must not be invoiced again")
	edit := starsEditByMessageID(t, mainBot, 77)
	assert.Equal(t, msg(MsgStarsAlreadyPaid), edit.Text)
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

// TestHandleUpdate_PreCheckout covers the pre-checkout flow: the answer must
// ALWAYS be sent (within Telegram's 10s window), through paymentBot when it is
// wired. Pre-checkout bypasses the rate limiter and generic handling.
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
			name:   "no payment bot fails closed",
			mockDB: func(db *testutil.BotStoreFake) { starsOrderMocks(db) },
			// paymentBot не задан → ответ НЕ уходит через long-poll основной бот:
			// платёжные вызовы fail-closed (N4).
			update:       starsPreCheckoutUpdate(199, "3", stars.Currency),
			noPaymentBot: true,
			wantCount:    0,
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

			if tt.noPaymentBot {
				// Fail-closed: long-poll клиент не используется для ответа.
				assert.Empty(t, mainBot.AllRequestsSafe(), "payment answers must not go through the long-poll bot")
				assert.Empty(t, mainBot.GetAllSentMessages())

				return
			}

			// Ответ уходит ВСЕГДА и через payment-инстанс.
			require.Len(t, paymentBot.AllRequestsSafe(), tt.wantCount)
			answer, ok := paymentBot.AllRequestsSafe()[0].(tgbotapi.PreCheckoutConfig)
			require.True(t, ok, "expected an answerPreCheckoutQuery request")
			assert.Equal(t, tt.wantAnswerOK, answer.OK)

			if tt.wantErrorEmpty {
				assert.Empty(t, answer.ErrorMessage)
			}
			if tt.wantErrorSet {
				assert.NotEmpty(t, answer.ErrorMessage)
			}

			// Pre-checkout идёт мимо rate-limiter и generic-обработки: основной бот молчит.
			assert.Empty(t, mainBot.GetAllSentMessages())
		})
	}
}

// TestHandleUpdate_PreCheckout_TransportError — транспортный сбой ответа на
// pre_checkout_query (таймаут/обрыв) не должен ронять обработчик: апдейт
// обрабатывается, ошибка логируется, пользователю ничего не отправляется.
func TestHandleUpdate_PreCheckout_TransportError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	paymentBot.RequestError = errors.New("read: connection reset by peer")

	require.NotPanics(t, func() {
		h.HandleUpdate(context.Background(), starsPreCheckoutUpdate(199, "3", stars.Currency))
	})

	// Ответ был попытка отправить (и упал по транспорту) — без паники и без
	// сообщений пользователю.
	require.Len(t, paymentBot.AllRequestsSafe(), 1)
	_, ok := paymentBot.AllRequestsSafe()[0].(tgbotapi.PreCheckoutConfig)
	assert.True(t, ok)
	assert.Empty(t, paymentBot.GetAllSentMessages())
	assert.Empty(t, mainBot.GetAllSentMessages())
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

// TestHandleUpdate_SuccessfulPaymentAutoRefunded покрывает оба ранее
// дублировавшихся кейса (canceled-заказ и автовозврат): деньги возвращены
// сервисом, покупатель получает «Платёж возвращён», а не предупреждение об
// отменённом заказе.
func TestHandleUpdate_SuccessfulPaymentAutoRefunded(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusCanceled))
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "Платёж возвращён")
	assert.Contains(t, sent[0].Text, "возвращён")
	assert.Contains(t, sent[0].Text, "/paysupport")
	assert.NotContains(t, sent[0].Text, "отменённому заказу")
	assert.Empty(t, mainBot.GetAllSentMessages())
}

// TestHandleUpdate_SuccessfulPaymentAutoRefundCallsStarsRefund — авто-возврат
// реально ходит в Telegram refundStarPayment с правильным charge id, а не
// только меняет текст уведомления.
func TestHandleUpdate_SuccessfulPaymentAutoRefundCallsStarsRefund(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusCanceled))
	starsAPI := testutil.NewBotAPI()
	refundCalls := captureStarsRefunds(starsAPI)
	h, _, paymentBot := newStarsTestHandlerWithStarsAPI(t, mockDB, starsAPI)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-1", "3", 199))

	require.Len(t, *refundCalls, 1, "auto refund must call refundStarPayment once")
	assert.Equal(t, "42", (*refundCalls)[0].userID, "refund must target the buyer telegram id")
	assert.Equal(t, "charge-1", (*refundCalls)[0].chargeID, "refund must use the incoming charge id")

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "Платёж возвращён")
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

// TestHandleUpdate_SuccessfulPaymentDuplicateChargeRefundFailed — авто-возврат
// дубля не удался: покупатель получает явное сообщение «оплата задвоилась» с
// подсказкой /paysupport (контракт DuplicateCharge && !AutoRefunded).
func TestHandleUpdate_SuccessfulPaymentDuplicateChargeRefundFailed(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusPaid))
	starsAPI := testutil.NewBotAPI()
	starsAPI.MakeRequestError = errors.New("telegram refund timeout")
	h, _, paymentBot := newStarsTestHandlerWithStarsAPI(t, mockDB, starsAPI)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge-2", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "задвоилась")
	assert.Contains(t, sent[0].Text, "/paysupport")
	assert.NotContains(t, sent[0].Text, "Платёж возвращён", "refund failed: user must not be told it was refunded")
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
		return starsAcceptUpdate("stars_accept_3_1_42_99", 42, 77)
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
		return starsAcceptUpdate("stars_accept_3_1_42_99", 42, 77)
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

// Регрессия: заказ отменён после уже выполненного refund, Telegram повторно
// доставил successful_payment. Деньги у покупателя — сообщение должно говорить
// об уже выполненном возврате, а не о «списании по отменённому заказу».
func TestHandleUpdate_SuccessfulPaymentCanceledOrderAlreadyRefunded(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsStatus(database.OrderStatusCanceled), withStarsCharge("charge_canceled"))

	h, _, paymentBot := newStarsTestHandler(t, mockDB)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge_canceled", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.NotEmpty(t, sent, "the buyer must be notified")

	texts := make([]string, 0, len(sent))
	for _, msg := range sent {
		texts = append(texts, msg.Text)
	}

	joined := strings.Join(texts, "\n")

	assert.Contains(t, joined, "уже возвращён")
	assert.NotContains(t, joined, "Оплата по отменённому заказу")
}

// Регрессия: товар удалён, авто-возврат не удался. Это не дубль оплаты —
// покупателю уходит запрос в /paysupport, а не «оплата задвоилась».
func TestHandleUpdate_SuccessfulPaymentDeletedProductRefundFailedAsksSupport(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB, withStarsProductErr(database.ErrProductNotFound))

	starsAPI := testutil.NewBotAPI()
	starsAPI.MakeRequestFunc = func(endpoint string, _ tgbotapi.Params) (*tgbotapi.APIResponse, error) {
		if endpoint == "refundStarPayment" {
			return &tgbotapi.APIResponse{Ok: false, Description: "Bad Request"}, errors.New("Bad Request: refund failed")
		}

		return &tgbotapi.APIResponse{Ok: true}, nil
	}

	h, _, paymentBot := newStarsTestHandlerWithStarsAPI(t, mockDB, starsAPI)

	h.HandleUpdate(context.Background(), successfulPaymentUpdate("charge_deleted", "3", 199))

	sent := paymentBot.GetAllSentMessages()
	require.NotEmpty(t, sent, "the buyer must be notified")

	texts := make([]string, 0, len(sent))
	for _, msg := range sent {
		texts = append(texts, msg.Text)
	}

	joined := strings.Join(texts, "\n")

	assert.Contains(t, joined, "/paysupport")
	assert.NotContains(t, joined, "задвоилась")
}
