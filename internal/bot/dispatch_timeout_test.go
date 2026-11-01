package bot

import (
	"context"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Timeout применяется один раз на диспетчеризации (HandleUpdate), а не
// копируется в каждый handler. Поведение: любой путь — включая callback,
// у которого раньше дедлайна не было — получает контекст с HandlerTimeout.
func TestHandleUpdate_AppliesTimeoutAtDispatch(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramBotToken: "test_token",
	}

	var gotDeadline bool
	mockDB := testutil.NewBotStoreFake()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		_, ok := ctx.Deadline()
		gotDeadline = ok
		return &database.Subscription{
			TelegramID:     telegramID,
			SubscriptionID: "sub",
			Status:         string(database.SubscriptionStatusActive),
		}, nil
	}

	handler := NewHandler(testutil.NewBotAPI(), cfg, mockDB, NewTestBotConfig(), nil, "")

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			Data: "create_subscription",
			From: &tgbotapi.User{ID: 111, UserName: "user1"},
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: 111},
			},
		},
	}
	handler.HandleUpdate(context.Background(), update)

	require.True(t, gotDeadline, "handler must run with a deadline-bearing context")
}
