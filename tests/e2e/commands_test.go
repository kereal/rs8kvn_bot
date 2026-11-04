package e2e

import (
	"context"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
)

func TestE2E_MySubscription(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "test-client-id",
		SubscriptionID: "test-sub-id",
		Status:         "active",
	}
	createE2ESub(t, env.db, sub)

	resetBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "menu_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Subscription info should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")
	assert.Contains(t, env.botAPI.LastSentText, "https://example.com/sub/test-sub-id", "Should contain subscription URL")
}
