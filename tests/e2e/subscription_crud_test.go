package e2e

import (
	"context"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_CreateSubscription_Success(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "Should not have subscription initially")

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	// DB-first: XUI is called via sync module, not directly in Create()
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should exist in DB")
	assert.Equal(t, env.chatID, sub.TelegramID)
	assert.Equal(t, env.username, sub.Username)
	assert.Equal(t, "active", sub.Status)
	assert.NotEmpty(t, sub.ClientID, "ClientID should be set")
	assert.NotEmpty(t, sub.SubscriptionID, "SubscriptionID should be set")

	assert.True(t, env.botAPI.SendCalledSafe(), "Confirmation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	assert.GreaterOrEqual(t, env.botAPI.SendCount, 2, "Should send at least 2 messages: user confirmation + admin notification")
}

func TestE2E_CreateSubscription_NoDuplicate(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	create := func(messageID int) {
		env.handler.HandleCallback(ctx, tgbotapi.Update{
			CallbackQuery: &tgbotapi.CallbackQuery{
				From: &tgbotapi.User{
					ID:       env.chatID,
					UserName: env.username,
				},
				Data: "create_subscription",
				Message: &tgbotapi.Message{
					Chat:      &tgbotapi.Chat{ID: env.chatID},
					MessageID: messageID,
				},
			},
		})
	}

	create(100)
	create(200)

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Len(t, allSubs, 1, "Should have exactly one subscription despite duplicate create requests")
}

func TestE2E_CreateSubscription_AssignsFreePlan(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should exist in DB")
	freePlan, err := env.db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	assert.Equal(t, freePlan.ID, sub.PlanID, "Subscription should be assigned the free plan")
}

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
	require.NoError(t, env.db.CreateSubscription(ctx, existing, ""))

	// Creating again for the same telegram_id must be idempotent: it returns the
	// existing active subscription instead of inserting a duplicate.
	result, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)
	assert.Equal(t, existing.SubscriptionID, result.Subscription.SubscriptionID)

	all, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1, "no duplicate subscription should be created")
}

func TestE2E_CreateSubscription_RevokesOnlyActive(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	oldSub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "old-client-id",
		SubscriptionID: "old-sub-id",
		Status:         "expired",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, oldSub, ""))

	resetBotAPI(env.botAPI)

	// A second create for the same telegram_id no longer errors: it reanimates
	// the existing (non-active) subscription back to "active" instead of
	// inserting a duplicate row that would violate telegram_id uniqueness.
	// See reanimateRevokedSubscription.
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)

	// Only the original subscription should exist (reanimated, not duplicated).
	require.Len(t, allSubs, 1, "Should have only one subscription")
	assert.Equal(t, "active", allSubs[0].Status)
	require.Equal(t, oldSub.SubscriptionID, allSubs[0].SubscriptionID, "Original record should be reactivated, not replaced")
}

func TestE2E_Create_ReanimatesRevokedSubscription(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	// Simulate a subscription left "revoked" after a partially-failed /del.
	// Re-creating must reanimate the same row instead of inserting a duplicate
	// (which would violate telegram_id uniqueness).
	revoked := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "revoked-client-id",
		SubscriptionID: "revoked-sub-id",
		Status:         "revoked",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, revoked, ""))

	result, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err, "re-creating after revoke should reanimate, not error")

	assert.Equal(t, revoked.SubscriptionID, result.Subscription.SubscriptionID,
		"the same row should be reanimated")
	assert.Equal(t, "active", result.Subscription.Status)

	all, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1, "no duplicate subscription should be created during reanimation")
}
