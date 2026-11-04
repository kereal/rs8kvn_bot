package e2e

import (
	"context"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_MultipleUsers_Isolation(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	users := []struct {
		chatID   int64
		username string
	}{
		{111, "user1"},
		{222, "user2"},
		{333, "user3"},
	}

	for _, u := range users {
		env.handler.HandleCallback(ctx, tgbotapi.Update{
			CallbackQuery: &tgbotapi.CallbackQuery{
				From: &tgbotapi.User{
					ID:       u.chatID,
					UserName: u.username,
				},
				Data: "create_subscription",
				Message: &tgbotapi.Message{
					Chat:      &tgbotapi.Chat{ID: u.chatID},
					MessageID: 100,
				},
			},
		})
	}

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, len(allSubs), "Should have 3 subscriptions")

	for _, u := range users {
		sub, err := env.db.GetByTelegramID(ctx, u.chatID)
		require.NoError(t, err)
		assert.Equal(t, u.username, sub.Username, "Username should match for user %d", u.chatID)
	}
}

func TestE2E_Create_ReturnsExistingActiveSubscription(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	existing := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "existing-client-id",
		SubscriptionID: "existing-sub-id",
		Status:         "active",
	}
	createE2ESub(t, env.db, existing)

	// Creating again for the same telegram_id must be idempotent: it returns the
	// existing active subscription instead of inserting a duplicate.
	result, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)
	assert.Equal(t, existing.SubscriptionID, result.Subscription.SubscriptionID)

	// Repeated creates must keep returning the same row (no duplicate insert).
	repeat, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)
	assert.Equal(t, existing.SubscriptionID, repeat.Subscription.SubscriptionID)

	all, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1, "no duplicate subscription should be created")
}
