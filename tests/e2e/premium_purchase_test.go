package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/web"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// premiumPaymentID is a canonical lowercase UUID v4 accepted by
// platega.ParseTransactionID. The mock provider always returns it, so the test
// can reference the same id in the webhook callback.
const premiumPaymentID = "550e8400-e29b-41d4-a716-446655440099"

// fixedPaymentProvider is a PaymentProvider stub returning a deterministic
// transaction id and payment link.
type fixedPaymentProvider struct{}

func (fixedPaymentProvider) CreateTransaction(_ context.Context, _ platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return &platega.CreateTransactionResponse{
		TransactionID: premiumPaymentID,
		URL:           "https://pay.example/checkout",
		ExpiresIn:     "00:15:00",
	}, nil
}

func (fixedPaymentProvider) GetTransactionStatus(_ context.Context, _ uuid.UUID) (*platega.TransactionStatusResponse, error) {
	return nil, errors.New("status not configured")
}

// TestE2E_PremiumPurchase_FullFlow drives the whole purchase path against a real
// SQLite database: open the tariff list, pick a product, get a payment link,
// then receive the provider's CONFIRMED webhook and verify the subscription is
// upgraded to the paid plan.
func TestE2E_PremiumPurchase_FullFlow(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	// Seed a purchasable premium product bound to a paid plan.
	paidPlan := &database.Plan{Name: "premium-e2e", IsActive: true, DevicesLimit: 2}
	require.NoError(t, env.db.GetDB().Create(paidPlan).Error)

	product := &database.Product{
		PlanID:       paidPlan.ID,
		Name:         "Premium 1 месяц",
		DurationDays: 30,
		PriceCents:   2300,
		Currency:     "RUB",
		IsActive:     true,
	}
	require.NoError(t, env.db.GetDB().Create(product).Error)

	// The user starts on the free plan.
	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	freeSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.NotEqual(t, paidPlan.ID, freeSub.PlanID, "user should start on the free plan")
	assert.Nil(t, freeSub.ProductID, "free subscription has no product yet")

	// Wire the payment provider and order service into the bot handler.
	orderSvc := service.NewOrderService(env.db, env.subService, env.syncService, fixedPaymentProvider{}, env.botConfig.Username, env.cfg)
	orderSvc.SetAdminBot(env.botAPI)
	env.handler.SetOrderService(orderSvc)

	// 1. Open the tariff list.
	resetBotAPI(env.botAPI)
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From:    &tgbotapi.User{ID: env.chatID, UserName: env.username},
			Data:    "buy_premium_list",
			Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: env.chatID}, MessageID: 100},
		},
	})
	assert.Contains(t, env.botAPI.LastSentText, "Выберите тариф", "tariff list should be shown")

	// 2. Pick the product: a pending order and payment link are created.
	resetBotAPI(env.botAPI)
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From:    &tgbotapi.User{ID: env.chatID, UserName: env.username},
			Data:    fmt.Sprintf("buy_product_%d", product.ID),
			Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: env.chatID}, MessageID: 100},
		},
	})
	assert.Contains(t, env.botAPI.LastSentText, "Тариф:", "payment confirmation screen should be shown")

	orders, err := env.db.GetOrdersBySubscriptionID(ctx, freeSub.ID)
	require.NoError(t, err)
	require.Len(t, orders, 1, "exactly one pending order should be created")
	assert.Equal(t, database.OrderStatusPending, orders[0].Status)
	assert.Equal(t, product.ID, orders[0].ProductID)
	assert.Equal(t, premiumPaymentID, orders[0].ProviderPaymentID, "provider payment id should be saved")

	// 3. Start the web server and deliver the provider's CONFIRMED webhook.
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, env.subService, nil)
	srv.SetOrderService(orderSvc)
	srv.SetBot(env.botAPI)
	srv.SetPaymentConfig(&web.PaymentConfig{Enabled: true, MerchantID: "merchant", Secret: "secret"})
	srv.SetPaymentReady(true)

	srvCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Start(srvCtx))
	defer srv.Stop(context.Background())

	body := fmt.Sprintf(`{"id":%q,"amount":"23.00","currency":"RUB","status":"CONFIRMED"}`, premiumPaymentID)
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/payment/callback", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Merchantid", "merchant")
	req.Header.Set("X-Secret", "secret")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "confirmed webhook should be accepted")

	// 4. The order is paid and the subscription is upgraded to the paid plan.
	orders, err = env.db.GetOrdersBySubscriptionID(ctx, freeSub.ID)
	require.NoError(t, err)
	require.Len(t, orders, 1)
	assert.Equal(t, database.OrderStatusPaid, orders[0].Status, "order should be paid")

	paidSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, paidPlan.ID, paidSub.PlanID, "subscription should be upgraded to the paid plan")
	require.NotNil(t, paidSub.ProductID, "subscription should reference the purchased product")
	assert.Equal(t, product.ID, *paidSub.ProductID)
	assert.Equal(t, int64(2300), paidSub.PricePaidCents, "price paid should be recorded")
	require.NotNil(t, paidSub.ExpiresAt, "paid subscription should have an expiry")
	assert.Equal(t, "active", paidSub.Status)
}
