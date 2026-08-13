package bot

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePaymentProvider satisfies service.PaymentProvider for bot-level tests.
type fakePaymentProvider struct{}

func (fakePaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return &platega.CreateTransactionResponse{
		TransactionID: "550e8400-e29b-41d4-a716-446655440099",
		URL:           "https://pay.example",
		ExpiresIn:     "00:15:00",
	}, nil
}

// newPaymentTestHandler builds a Handler with payment enabled and an OrderService
// backed by the provided DB mock and provider. Passing a nil provider yields an
// OrderService with disabled payments (ErrPaymentDisabled from RequestPayment).
func newPaymentTestHandler(t *testing.T, mockDB *testutil.DatabaseService, provider service.PaymentProvider) (*Handler, *testutil.BotAPI) {
	t.Helper()

	cfg := &config.Config{TelegramAdminID: 123, PaymentEnabled: true}
	bot := testutil.NewBotAPI()
	h := NewHandler(bot, cfg, mockDB, NewTestBotConfig(), nil, "")
	o := service.NewOrderService(mockDB, nil, nil, provider, "testbot", cfg)
	h.SetOrderService(o)

	return h, bot
}

func TestHandleBuyPremiumList_NotConfigured(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123, PaymentEnabled: false}
	mockDB := testutil.NewDatabaseService()
	bot := testutil.NewBotAPI()
	h := NewHandler(bot, cfg, mockDB, NewTestBotConfig(), nil, "")

	err := h.handleBuyPremiumList(context.Background(), 42, "user", 99)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Платежи временно недоступны")
}

func TestHandleBuyPremiumList_ListErrorShowsTempError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.ListActiveProductsFunc = func(context.Context) ([]database.Product, error) {
		return nil, assert.AnError
	}
	h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

	err := h.handleBuyPremiumList(context.Background(), 42, "user", 99)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, msg(MsgSubTempError), messages[0].Text)
}

func TestHandleBuyPremiumList_EmptyListShowsNoTariffs(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.ListActiveProductsFunc = func(context.Context) ([]database.Product, error) {
		return nil, nil
	}
	h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

	err := h.handleBuyPremiumList(context.Background(), 42, "user", 99)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Нет доступных тарифов")
}

func TestHandleBuyPremiumList_SuccessShowsProducts(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.ListActiveProductsFunc = func(context.Context) ([]database.Product, error) {
		return []database.Product{
			{ID: 1, Name: "Месяц", PriceCents: 19900, IsActive: true},
			{ID: 2, Name: "Год", PriceCents: 199900, IsActive: true},
		}, nil
	}
	h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

	err := h.handleBuyPremiumList(context.Background(), 42, "user", 99)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Выберите тариф для оплаты:")

	edit, ok := bot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "expected an edited message with a keyboard")
	require.NotNil(t, edit.ReplyMarkup, "product list must carry the BuyProductList keyboard")
	require.NotEmpty(t, edit.ReplyMarkup.InlineKeyboard)

	for _, row := range edit.ReplyMarkup.InlineKeyboard[:len(edit.ReplyMarkup.InlineKeyboard)-1] {
		require.NotNil(t, row[0].CallbackData)
		assert.Contains(t, *row[0].CallbackData, "buy_product_", "product rows use buy_product_{id} callbacks")
	}
}

func TestHandleBuyProduct_NotConfigured(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123, PaymentEnabled: false}
	mockDB := testutil.NewDatabaseService()
	bot := testutil.NewBotAPI()
	h := NewHandler(bot, cfg, mockDB, NewTestBotConfig(), nil, "")

	err := h.handleBuyProduct(context.Background(), 42, "user", 99, 1)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Платежи временно недоступны")
}

func TestHandleBuyProduct_ProductLoadErrorShowsTempError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return nil, assert.AnError
	}
	h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

	err := h.handleBuyProduct(context.Background(), 42, "user", 99, 1)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, msg(MsgSubTempError), messages[0].Text)
}

func TestHandleBuyProduct_TariffUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		product *database.Product
	}{
		{name: "nil product", product: nil},
		{name: "inactive product", product: &database.Product{ID: 1, Name: "Месяц", PriceCents: 19900, IsActive: false}},
		{name: "free product", product: &database.Product{ID: 1, Name: "Free", PriceCents: 0, IsActive: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockDB := testutil.NewDatabaseService()
			mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
				return tt.product, nil
			}
			h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

			err := h.handleBuyProduct(context.Background(), 42, "user", 99, 1)
			require.NoError(t, err)

			messages := bot.GetAllSentMessages()
			require.Len(t, messages, 1)
			assert.Contains(t, messages[0].Text, "Тариф недоступен")
		})
	}
}

func TestHandleBuyProduct_PaymentDisabled(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, Name: "Месяц", PriceCents: 19900, IsActive: true}, nil
	}
	// orderService with a nil provider → RequestPayment returns ErrPaymentDisabled.
	h, bot := newPaymentTestHandler(t, mockDB, nil)

	err := h.handleBuyProduct(context.Background(), 42, "user", 99, 1)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Платежи временно недоступны")
}

func TestHandleBuyProduct_PaymentAlreadyInProgress(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, Name: "Месяц", PriceCents: 19900, Currency: "RUB", IsActive: true}, nil
	}
	mockDB.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
	mockDB.FindPendingPaymentOrderFunc = func(context.Context, uint, uint, time.Time) (*database.Order, error) {
		// Pending intent already claimed by a provider transaction without a
		// usable link → RequestPayment must report ErrPaymentAlreadyInProgress.
		return &database.Order{
			ID: 3, Status: database.OrderStatusPending, ProviderPaymentID: "550e8400-e29b-41d4-a716-446655440099",
		}, nil
	}
	h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

	err := h.handleBuyProduct(context.Background(), 42, "user", 99, 1)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, msg(MsgPaymentInProgress), messages[0].Text)
}

func TestHandleBuyProduct_PaymentNeedsReview(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, Name: "Месяц", PriceCents: 19900, Currency: "RUB", IsActive: true}, nil
	}
	mockDB.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
	mockDB.FindPendingPaymentOrderFunc = func(context.Context, uint, uint, time.Time) (*database.Order, error) {
		return &database.Order{ID: 3, Status: database.OrderStatusPending, PaymentCreationUncertain: true}, nil
	}
	h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

	err := h.handleBuyProduct(context.Background(), 42, "user", 99, 1)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, msg(MsgPaymentNeedsReview), messages[0].Text)
}

func TestHandleBuyProduct_GenericRequestErrorShowsTempError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, Name: "Месяц", PriceCents: 19900, Currency: "RUB", IsActive: true}, nil
	}
	mockDB.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
	mockDB.FindPendingPaymentOrderFunc = func(context.Context, uint, uint, time.Time) (*database.Order, error) {
		return nil, assert.AnError
	}
	h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

	err := h.handleBuyProduct(context.Background(), 42, "user", 99, 1)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, msg(MsgSubTempError), messages[0].Text)
}

func TestHandleBuyProduct_SuccessShowsPaymentButton(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetProductByIDFunc = func(context.Context, uint) (*database.Product, error) {
		return &database.Product{ID: 1, Name: "Месяц", PriceCents: 19900, Currency: "RUB", IsActive: true}, nil
	}
	mockDB.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return &database.Subscription{ID: 7, TelegramID: 42}, nil
	}
	mockDB.FindPendingPaymentOrderFunc = func(context.Context, uint, uint, time.Time) (*database.Order, error) {
		return nil, nil
	}
	mockDB.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 2, IsActive: true}, nil
	}
	mockDB.FindOrCreatePendingPaymentOrderFunc = func(context.Context, uint, uint, int64, string, time.Time) (*database.Order, error) {
		return &database.Order{ID: 3, SubscriptionID: 7, ProductID: 1, Status: database.OrderStatusPending, AmountCents: 19900, Currency: "RUB"}, nil
	}
	mockDB.MarkPaymentCreationUncertainFunc = func(context.Context, uint, bool) (bool, error) { return true, nil }
	mockDB.SavePaymentDetailsFunc = func(context.Context, uint, uuid.UUID, string, time.Time) error { return nil }
	h, bot := newPaymentTestHandler(t, mockDB, fakePaymentProvider{})

	err := h.handleBuyProduct(context.Background(), 42, "user", 99, 1)
	require.NoError(t, err)

	messages := bot.GetAllSentMessages()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Text, "Тариф: 💎 *Месяц*")
	assert.Contains(t, messages[0].Text, "Стоимость: *199₽*")
	assert.Contains(t, messages[0].Text, "После оплаты тариф активируется автоматически.")
	assert.Contains(t, messages[0].Text, "Если тариф уже активен, новые дни прибавятся к текущему сроку.")
	assert.Contains(t, messages[0].Text, "_Платёжная система может дополнительно взимать комиссию._")

	edit, ok := bot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "expected an edited message with a payment keyboard")
	require.Equal(t, "Markdown", edit.ParseMode)
	require.NotNil(t, edit.ReplyMarkup, "payment screen must carry the BuyProductConfirm keyboard")
	require.NotEmpty(t, edit.ReplyMarkup.InlineKeyboard)
	urlButton := edit.ReplyMarkup.InlineKeyboard[0][0]
	require.NotNil(t, urlButton.URL, "payment button must carry the provider URL")
	assert.Equal(t, "https://pay.example", *urlButton.URL, "payment button must link to the provider URL")
	assert.Equal(t, "💳 Оплатить 199₽", urlButton.Text)
}
