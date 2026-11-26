package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestParseStarsInvoiceBack покрывает новый формат
// stars_invoice_back_<buyer>_<invoice_msg>_<order_id>: product_id из формата
// удалён (N3), вместо него переносится order_id pending-заказа (S4).
func TestParseStarsInvoiceBack(t *testing.T) {
	t.Parallel()

	buyerID, msgID, orderID, ok := parseStarsInvoiceBack("42_55_3")
	require.True(t, ok)
	assert.Equal(t, int64(42), buyerID)
	assert.Equal(t, 55, msgID)
	assert.Equal(t, uint(3), orderID)

	invalid := []string{
		"garbage",
		"42_55",
		"42_55_3_1",
		"0_55_3",
		"42_0_3",
		"42_55_0",
		"42_55_x",
		"42_-1_3",
	}
	for _, payload := range invalid {
		_, _, _, ok = parseStarsInvoiceBack(payload)
		assert.False(t, ok, payload)
	}
}

// TestStarsAccept_CardInAnotherChatAccepted — карточка тарифа может жить в
// другом чате (группа/канал), а кнопка согласия приходит в личку покупателя:
// chatID callback-сообщения и cardChatID из payload легально различаются.
// Чужое сообщение при этом править нельзя — Telegram разрешает боту редактировать
// только собственные сообщения, а покупатель проверяется по заказу.
func TestStarsAccept_CardInAnotherChatAccepted(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	// Кнопка согласия в личке покупателя (42), карточка — в чате 5000.
	update := starsAcceptUpdate("stars_accept_3_1_5000_99", 42, 77)

	err := h.HandleCallback(context.Background(), update)
	require.NoError(t, err)

	invoices := starsInvoices(paymentBot)
	require.Len(t, invoices, 1)
	assert.Equal(t, int64(42), invoices[0].ChatID, "invoice must target the buyer telegram_id")

	cardEdit := starsEditByMessageID(t, mainBot, 99)
	assert.Equal(t, int64(5000), cardEdit.ChatID, "card must be edited in the chat it was posted in")
	assert.Equal(t, 99, cardEdit.MessageID)
	assert.Nil(t, cardEdit.ReplyMarkup, "shared group card must not carry a personal back keyboard")

	// Back-кнопка привязана к личному сообщению покупателя и ведёт на его счёт.
	acceptDone := starsEditByMessageID(t, mainBot, 77)
	assert.Equal(t, int64(42), acceptDone.ChatID, "back keyboard must live in the buyer's private chat")
	acceptKeyboard := starsInlineKeyboard(t, acceptDone.ReplyMarkup)
	require.NotNil(t, acceptKeyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "stars_invoice_back_42_1_3", *acceptKeyboard.InlineKeyboard[0][0].CallbackData)
}

// TestStarsAccept_CardChatMatchesCallbackChat — валидный acceptance: чат
// callback-сообщения совпадает с cardChatID, invoice отправляется, карточка
// редактируется в своём чате.
func TestStarsAccept_CardChatMatchesCallbackChat(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	starsOrderMocks(mockDB)
	h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

	update := starsAcceptUpdate("stars_accept_3_1_42_99", 42, 77)

	err := h.HandleCallback(context.Background(), update)
	require.NoError(t, err)

	invoices := starsInvoices(paymentBot)
	require.Len(t, invoices, 1)
	assert.Equal(t, int64(42), invoices[0].ChatID, "invoice must target the buyer telegram_id")

	cardEdit := starsEditByMessageID(t, mainBot, 99)
	assert.Equal(t, int64(42), cardEdit.ChatID, "card must be edited in the callback chat")
	assert.Equal(t, 99, cardEdit.MessageID)
}

// TestStarsAccept_InactiveProductOrPlanRejected — перед выставлением счёта
// товар и план перечитываются: неактивный товар/план отклоняются, pending-заказ
// истекает (S5).
func TestStarsAccept_InactiveProductOrPlanRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opt  starsOrderOpt
	}{
		{name: "inactive product", opt: withStarsProductActive(false)},
		{name: "inactive plan", opt: withStarsPlanActive(false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockDB := testutil.NewBotStoreFake()
			starsOrderMocks(mockDB, tt.opt)

			expiredOrderID := uint(0)
			mockDB.ExpirePendingOrderFunc = func(_ context.Context, orderID uint) (bool, error) {
				expiredOrderID = orderID

				return true, nil
			}

			h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

			update := starsAcceptUpdate("stars_accept_3_1_42_99", 42, 77)

			err := h.HandleCallback(context.Background(), update)
			require.NoError(t, err)

			assert.Empty(t, starsInvoices(paymentBot), "no invoice must be sent for an inactive product/plan")
			assert.Equal(t, uint(3), expiredOrderID, "pending order must be expired so a retry creates a new one")

			// Покупателю показана понятная ошибка вместо счёта.
			messages := mainBot.GetAllSentMessages()
			require.NotEmpty(t, messages)
			assert.Equal(t, msg(MsgSubTempError), messages[len(messages)-1].Text)
		})
	}
}

// TestStarsAccept_MismatchedOrderDoesNotExpireOrder — подделанный callback с
// чужим order_id/product_id или от чужого покупателя НЕ должен истекать
// pending-заказ: проверка принадлежности идёт до expireStarsPending.
func TestStarsAccept_MismatchedOrderDoesNotExpireOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		data   string
		mutate func(*testutil.BotStoreFake)
	}{
		{
			name: "other buyer",
			data: "stars_accept_3_1_42_99",
			mutate: func(db *testutil.BotStoreFake) {
				db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
					return nil, nil
				}
			},
		},
		{
			name: "product mismatch",
			data: "stars_accept_3_2_42_99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockDB := testutil.NewBotStoreFake()
			// Неактивный товар: до фикса эта ветка истекла бы заказ до проверки
			// принадлежности.
			starsOrderMocks(mockDB, withStarsProductActive(false))
			if tt.mutate != nil {
				tt.mutate(mockDB)
			}

			expired := false
			mockDB.ExpirePendingOrderFunc = func(context.Context, uint) (bool, error) {
				expired = true

				return true, nil
			}

			h, _, paymentBot := newStarsTestHandler(t, mockDB)

			require.NoError(t, h.HandleCallback(context.Background(), starsAcceptUpdate(tt.data, 42, 99)))

			assert.False(t, expired, "mismatched acceptance must not expire the order")
			assert.Empty(t, starsInvoices(paymentBot), "mismatched acceptance must not invoice")
		})
	}
}

// TestStarsBuy_CallbackChatDiffersFromTelegramID — callback-чат (5000) и
// Telegram user ID покупателя (42) различны: Terms-сообщение уходит в private
// chat по telegram_id, карточка редактируется в callback chat.
func TestStarsBuy_CallbackChatDiffersFromTelegramID(t *testing.T) {
	t.Parallel()

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

// TestStarsInvoiceBack — table-driven регресс кнопки «Назад»: pending-заказ
// истекается (S4), invoice-сообщение удаляется, карточка возвращается к списку
// тарифов; чужие нажатия и битый payload ничего не меняют (N6/S11.3).
func TestStarsInvoiceBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		data           string
		pressedBy      int64
		callbackChat   int64
		cardMsgID      int
		orderOwner     int64
		setup          func(*testutil.BotStoreFake)
		wantExpired    bool
		wantDeleted    []int
		wantCardRender bool
	}{
		{
			name:           "buyer in card chat expires order and deletes invoice",
			data:           "stars_invoice_back_42_55_3",
			pressedBy:      42,
			callbackChat:   42,
			cardMsgID:      99,
			orderOwner:     42,
			wantExpired:    true,
			wantDeleted:    []int{55},
			wantCardRender: true,
		},
		{
			name:           "buyer in group card chat deletes invoice in buyer private chat",
			data:           "stars_invoice_back_42_55_3",
			pressedBy:      42,
			callbackChat:   5000,
			cardMsgID:      99,
			orderOwner:     42,
			wantExpired:    true,
			wantDeleted:    []int{55},
			wantCardRender: true,
		},
		{
			name:           "foreign user is ignored",
			data:           "stars_invoice_back_42_55_3",
			pressedBy:      43,
			callbackChat:   5000,
			cardMsgID:      99,
			orderOwner:     42,
			wantExpired:    false,
			wantDeleted:    nil,
			wantCardRender: false,
		},
		{
			// Payload владения не доказывает: нажавший подставил свой telegram_id,
			// но заказ принадлежит другому пользователю — истекать его нельзя.
			name:           "foreign order is not expired",
			data:           "stars_invoice_back_42_55_3",
			pressedBy:      42,
			callbackChat:   42,
			cardMsgID:      99,
			orderOwner:     99,
			wantExpired:    false,
			wantDeleted:    []int{55},
			wantCardRender: true,
		},
		{
			// «Назад» относится к Stars-счёту: pending-заказ другого провайдера
			// (подделанный payload) не истекаем, но возврат по кнопке выполняем.
			name:         "non-stars order is not expired",
			data:         "stars_invoice_back_42_55_3",
			pressedBy:    42,
			callbackChat: 42,
			cardMsgID:    99,
			orderOwner:   42,
			setup: func(db *testutil.BotStoreFake) {
				db.GetOrderByIDFunc = func(_ context.Context, id uint) (*database.Order, error) {
					return &database.Order{
						ID: id, SubscriptionID: 7, ProductID: 1, Status: database.OrderStatusPending,
						PaymentProvider: database.PaymentProviderPlatega,
					}, nil
				}
			},
			wantExpired:    false,
			wantDeleted:    []int{55},
			wantCardRender: true,
		},
		{
			// Сбой чтения заказа повторяем: invoice не удаляем, чтобы нажатие
			// «Назад» можно было повторить, статус не меняем.
			name:         "order read error keeps invoice for retry",
			data:         "stars_invoice_back_42_55_3",
			pressedBy:    42,
			callbackChat: 42,
			cardMsgID:    99,
			orderOwner:   42,
			setup: func(db *testutil.BotStoreFake) {
				db.GetOrderByIDFunc = func(context.Context, uint) (*database.Order, error) {
					return nil, errors.New("db down")
				}
			},
			wantExpired:    false,
			wantDeleted:    nil,
			wantCardRender: true,
		},
		{
			name:           "invalid payload is ignored",
			data:           "stars_invoice_back_garbage",
			pressedBy:      42,
			callbackChat:   42,
			cardMsgID:      99,
			orderOwner:     42,
			wantExpired:    false,
			wantDeleted:    nil,
			wantCardRender: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockDB := testutil.NewBotStoreFake()
			starsOrderMocks(mockDB)
			if tt.setup != nil {
				tt.setup(mockDB)
			}
			// Владелец заказа 3 (подписка 7) задаётся вариантом кейса: payload
			// buyerTelegramID владения не доказывает, проверка идёт по подписке.
			mockDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
				return &database.Subscription{ID: 7, TelegramID: tt.orderOwner}, nil
			}
			mockDB.ListActiveProductsFunc = func(context.Context) ([]database.Product, error) {
				return []database.Product{
					{ID: 1, PlanID: 2, Name: "Месяц", PriceCents: 199, Currency: stars.Currency, IsActive: true},
				}, nil
			}

			expiredOrderID := uint(0)
			mockDB.ExpirePendingOrderFunc = func(_ context.Context, orderID uint) (bool, error) {
				expiredOrderID = orderID

				return true, nil
			}

			h, mainBot, paymentBot := newStarsTestHandler(t, mockDB)

			update := tgbotapi.Update{
				CallbackQuery: &tgbotapi.CallbackQuery{
					ID:   "cb1",
					From: &tgbotapi.User{ID: tt.pressedBy, UserName: "user"},
					Message: &tgbotapi.Message{
						Chat:      &tgbotapi.Chat{ID: tt.callbackChat},
						MessageID: tt.cardMsgID,
					},
					Data: tt.data,
				},
			}

			err := h.HandleCallback(context.Background(), update)
			require.NoError(t, err)

			if tt.wantExpired {
				assert.Equal(t, uint(3), expiredOrderID, "pending order must be expired on back")
			} else {
				assert.Zero(t, expiredOrderID, "pending order must stay untouched")
			}

			assert.Equal(t, tt.wantDeleted, paymentBot.DeletedMessageIDs)

			if !tt.wantCardRender {
				assert.Empty(t, mainBot.GetAllSentMessages())

				return
			}

			// Карточка отредактирована обратно в список тарифов, дубля нет.
			edits := mainBot.GetAllSentMessages()
			require.Len(t, edits, 1)
			assert.Equal(t, msg(MsgPremiumBenefits), edits[0].Text)

			edit, ok := mainBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
			require.True(t, ok, "card must be edited in place, not re-sent")
			assert.Equal(t, tt.callbackChat, edit.ChatID)
			assert.Equal(t, tt.cardMsgID, edit.MessageID)
			editKeyboard := starsInlineKeyboard(t, edit.ReplyMarkup)
			require.Len(t, editKeyboard.InlineKeyboard, 2)
			require.NotNil(t, editKeyboard.InlineKeyboard[0][0].CallbackData)
			assert.Equal(t, "buy_product_1", *editKeyboard.InlineKeyboard[0][0].CallbackData)
		})
	}
}
