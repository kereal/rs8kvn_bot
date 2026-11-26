package bot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
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
	mockDB.ConfirmOrderPaidCASFunc = func(context.Context, database.ConfirmPaidOrderParams) (bool, error) {
		return true, nil
	}
	mockDB.CancelPaidOrderAndDowngradeCASFunc = func(context.Context, database.CancelPaidOrderParams) (*database.ChargebackResult, error) {
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

		// Уже отменённый заказ: честный ответ вместо ложного «✅».
		messages := noopMain.GetAllSentMessages()
		require.NotEmpty(t, messages)
		assert.Contains(t, messages[len(messages)-1].Text, "↩️ Возврат по заказу #3 уже выполнен ранее")
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
		failedDB.CancelPaidOrderAndDowngradeCASFunc = func(context.Context, database.CancelPaidOrderParams) (*database.ChargebackResult, error) {
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
	mockDB.CancelPaidOrderAndDowngradeCASFunc = func(context.Context, database.CancelPaidOrderParams) (*database.ChargebackResult, error) {
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
	// /paysupport — одно сообщение; /terms длиннее лимита Telegram (4096):
	// отправляется кусками через splitMessage. Telegram считает лимит
	// «after entities parsing», поэтому кусок с открытым Markdown-энтити
	// может быть длиннее 4096 сырых символов — проверяем распарсенную длину.
	require.GreaterOrEqual(t, len(messages), 3, "terms text exceeds the Telegram limit and must be split")
	assert.Equal(t, SupportText(), messages[0].Text)

	termsChunks := chunkTexts(messages[1:])
	for i, chunk := range termsChunks {
		assert.LessOrEqual(t, parsedLen(chunk), config.MaxTelegramMessageLen,
			"terms chunk %d must fit the Telegram message limit after entity parsing", i)
	}

	// Границы кусков могут приходиться на пробел: сверяем содержимое
	// с нормализованными пробелами — ни слово не потеряно и не задвоено.
	normalize := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	assert.Equal(t, normalize(TermsText()), normalize(strings.Join(termsChunks, " ")),
		"terms chunks must preserve the full text")
}

// parsedLen — верхняя оценка длины текста после разбора Telegram-энтити:
// убирает маркеры разметки (`*`, `_`, “ ` “, `~`, `[`, `]`).
func parsedLen(text string) int {
	replacer := strings.NewReplacer("*", "", "_", "", "`", "", "~", "", "[", "", "]", "")

	return len([]rune(replacer.Replace(text)))
}

func chunkTexts(messages []testutil.SentMessage) []string {
	texts := make([]string, 0, len(messages))
	for _, m := range messages {
		texts = append(texts, m.Text)
	}

	return texts
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
	// /terms шлётся кусками (текст длиннее лимита Telegram): проверяем
	// paysupport-сообщение и полный terms-текст по всем кускам.
	require.GreaterOrEqual(t, len(messages), 3, "both commands must be answered despite exhausted rate limit")
	assert.Contains(t, messages[0].Text, "Поддержка Telegram не решает вопросы оплаты")
	assert.Contains(t, strings.Join(chunkTexts(messages[1:]), " "), "7.7.")
}
