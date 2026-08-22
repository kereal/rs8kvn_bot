package e2e

import (
	"context"
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

// resetTrafficPaymentID is a canonical UUID for the reset-traffic test.
const resetTrafficPaymentID = "660e8400-e29b-41d4-a716-446655440099"

type fixedResetTrafficProvider struct{}

func (fixedResetTrafficProvider) CreateTransaction(_ context.Context, _ platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	return &platega.CreateTransactionResponse{
		TransactionID: resetTrafficPaymentID,
		URL:           "https://pay.example/checkout",
		ExpiresIn:     "00:15:00",
	}, nil
}

func (fixedResetTrafficProvider) GetTransactionStatus(_ context.Context, _ uuid.UUID) (*platega.TransactionStatusResponse, error) {
	return nil, fmt.Errorf("status not configured")
}

// TestE2E_PaymentCycle_ResetTraffic verifies that the full payment cycle
// (free→premium) triggers ResetTraffic on the VPN panel, ensuring the
// free-tier traffic counter is cleared when the user upgrades.
func TestE2E_PaymentCycle_ResetTraffic(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	// Seed a premium plan + product.
	premiumPlan := &database.Plan{Name: "premium-reset", IsActive: true, DevicesLimit: 2, TrafficLimit: 0}
	require.NoError(t, env.db.GetDB().Create(premiumPlan).Error)
	require.NoError(t, env.db.LinkNodeToPlan(ctx, "premium-reset", 1), "link node to premium plan")

	product := &database.Product{
		PlanID:       premiumPlan.ID,
		Name:         "Premium 1 месяц",
		DurationDays: 30,
		PriceCents:   2300,
		Currency:     "RUB",
		IsActive:     true,
	}
	require.NoError(t, env.db.GetDB().Create(product).Error)

	// User starts on the free plan.
	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	freeSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.NotEqual(t, premiumPlan.ID, freeSub.PlanID, "user should start on the free plan")

	// Wire the payment provider.
	orderSvc := service.NewOrderService(env.db, env.subService, env.syncService, fixedResetTrafficProvider{}, env.botConfig.Username, env.cfg)
	orderSvc.SetAdminBot(env.botAPI)
	env.handler.SetOrderService(orderSvc)

	// 1. Open tariff list.
	resetBotAPI(env.botAPI)
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From:    &tgbotapi.User{ID: env.chatID, UserName: env.username},
			Data:    "buy_premium_list",
			Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: env.chatID}, MessageID: 100},
		},
	})
	assert.Contains(t, env.botAPI.LastSentText, "Выберите тариф")

	// 2. Pick the product.
	resetBotAPI(env.botAPI)
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From:    &tgbotapi.User{ID: env.chatID, UserName: env.username},
			Data:    fmt.Sprintf("buy_product_%d", product.ID),
			Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: env.chatID}, MessageID: 100},
		},
	})
	assert.Contains(t, env.botAPI.LastSentText, "Тариф:")

	// 3. Start web server and deliver the CONFIRMED webhook.
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, env.subService, nil)
	srv.SetOrderService(orderSvc)
	srv.SetBot(env.botAPI)
	srv.SetPaymentConfig(&web.PaymentConfig{Enabled: true, MerchantID: "merchant", Secret: "secret"})
	srv.SetPaymentReady(true)

	srvCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Start(srvCtx))
	defer srv.Stop(context.Background())

	body := fmt.Sprintf(`{"id":%q,"amount":"23.00","currency":"RUB","status":"CONFIRMED"}`, resetTrafficPaymentID)
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/payment/callback", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Merchantid", "merchant")
	req.Header.Set("X-Secret", "secret")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 4. Verify the subscription was upgraded.
	paidSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, premiumPlan.ID, paidSub.PlanID, "subscription should be upgraded to premium")
	assert.Equal(t, "active", paidSub.Status)

	// 5. Verify ResetTraffic was called on the VPN panel (shared-node scenario).
	assert.True(t, env.xui.ResetTrafficCalled, "ResetTraffic must be called during free→premium upgrade")
}
