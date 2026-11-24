package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestHandleRefund_SplitBrainStateError — split-brain: Telegram вернул Stars,
// но CAS/состояние не применилось. Админу сообщается, что возврат уже выполнен
// и повторный /refund безопасен (S9).
func TestHandleRefund_SplitBrainStateError(t *testing.T) {
	t.Parallel()

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
	assert.Contains(t, text, "Telegram-возврат уже выполнен")
	assert.Contains(t, text, "повторите `/refund <id>`")
	assert.Contains(t, text, "#3")
	assert.NotContains(t, text, "не выполнен", "must not claim the refund failed")
	assert.NotContains(t, text, "Проверьте статус заказа и баланс")
	assert.NotContains(t, text, "Повторный /refund не нужен", "repeat refund is safe and must be offered")
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
