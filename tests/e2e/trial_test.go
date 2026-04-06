package e2e

import (
	"context"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_TrialBind_Success(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	trialSubID := "trial-abc-123"
	_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", 1, 1073741824, time.Now().Add(24*time.Hour), "https://example.com/sub/trial-abc-123")
	require.NoError(t, err)

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Activation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Подписка активирована", "Should confirm activation")

	bound, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, bound.TelegramID, "TelegramID should be set")
	assert.Equal(t, env.username, bound.Username, "Username should be set")
	assert.False(t, bound.IsTrial, "IsTrial should be false after bind")
}

func TestE2E_TrialBind_AlreadyHasSubscription(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	existingSub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "existing-client",
		SubscriptionID: "existing-sub",
		InboundID:      1,
		TrafficLimit:   107374182400,
		Status:         "active",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, existingSub))

	trialSubID := "trial-xyz-789"
	_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", 1, 1073741824, time.Now().Add(24*time.Hour), "https://example.com/sub/trial-xyz-789")
	require.NoError(t, err)

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Error message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "уже есть активная подписка", "Should reject with existing subscription message")
}

func TestE2E_TrialBind_NotFound(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_nonexistent", 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Error message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Не удалось активировать", "Should show activation error message")
}

func TestE2E_TrialBind_AlreadyActivated(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	trialSubID := "trial-double-123"
	_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", 1, 1073741824, time.Now().Add(24*time.Hour), "https://example.com/sub/trial-double-123")
	require.NoError(t, err)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Error message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "уже есть активная подписка", "Should reject already-bound trial")
}
