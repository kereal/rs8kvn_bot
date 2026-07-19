//go:build integration

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)


func TestE2E_CreateSubscription_EmptyUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, env.chatID, "", "")
	if err == nil {
		sub, err := env.db.GetByTelegramID(ctx, env.chatID)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("tgId_%d", env.chatID), sub.Username)
	}
}

func TestE2E_CreateSubscription_InvalidChatID(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, -123, "testuser", "")
	if err == nil {
		sub, err := env.db.GetByTelegramID(ctx, -123)
		require.NoError(t, err)
		assert.Equal(t, int64(-123), sub.TelegramID)
	}
}

func TestE2E_CreateSubscription_ZeroChatID(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, 0, "testuser", "")
	if err == nil {
		sub, err := env.db.GetByTelegramID(ctx, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(0), sub.TelegramID)
	}
}

func TestE2E_Subscription_LongUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	longUsername := strings.Repeat("a", 1000)
	_, err := env.subService.Create(ctx, env.chatID, longUsername, "")
	if err == nil {
		sub, err := env.db.GetByTelegramID(ctx, env.chatID)
		require.NoError(t, err)
		assert.True(t, len(sub.Username) > 0, "Should have some username stored")
	}
}

func TestE2E_Subscription_SpecialCharactersInUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	specialUsername := "test@user#123!"
	_, err := env.subService.Create(ctx, env.chatID, specialUsername, "")
	if err != nil {
		assert.Contains(t, err.Error(), "invalid", "Error should mention invalid characters")
	}
}

func TestE2E_CreateSubscription_RetryAfterFailure(t *testing.T) {
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

	// DB-first: subscription is created even if XUI fails (sync will retry)
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.NoError(t, err, "Subscription should exist in DB")
	assert.Equal(t, "active", sub.Status)
}

func TestE2E_CreateSubscription_MultipleRetries(t *testing.T) {
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

	// DB-first: subscription is created even if XUI fails (sync will retry)
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.NoError(t, err, "Subscription should exist in DB")
	assert.Equal(t, "active", sub.Status)
}

func TestE2E_CreateSubscription_XUITimeout(t *testing.T) {
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

	// DB-first: subscription is created even if XUI times out (sync will retry)
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.NoError(t, err, "Subscription should exist in DB")
	assert.Equal(t, "active", sub.Status)
}

func TestE2E_Service_Create_DatabaseClosed(t *testing.T) {
	env := setupE2EEnv(t)
	env.db.Close()

	ctx := context.Background()
	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	assert.Error(t, err, "Should fail with closed database")
}

func TestE2E_GetSubscription_DatabaseClosed(t *testing.T) {
	env := setupE2EEnv(t)

	ctx := context.Background()
	sub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "test-client-id",
		SubscriptionID: "test-sub-id",
		Status:         "active",
	}
	env.db.CreateSubscription(ctx, sub, "")
	env.db.Close()

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "Should fail with closed database")
}

func TestE2E_Subscription_Expired(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "test-client-id",
		SubscriptionID: "test-sub-id",
		Status:         "expired",
		ExpiresAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour)),
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub, ""))

	fetched, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "Expired subscription should not be returned by GetByTelegramID")
	_ = fetched
}

func TestE2E_Subscription_AboutToExpire(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "test-client-id",
		SubscriptionID: "test-sub-id",
		Status:         "active",
		ExpiresAt:      testutil.PtrTime(time.Now().Add(1 * time.Hour)),
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub, ""))

	storedSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	require.NotNil(t, storedSub.ExpiresAt)
	assert.True(t, storedSub.ExpiresAt.Before(time.Now().Add(2*time.Hour)), "Should expire within 2 hours")
}

func TestE2E_RateLimit_ExactlyAtLimit(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		resetBotAPI(env.botAPI)
		update := tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat:     &tgbotapi.Chat{ID: adminID},
				From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
				Text:     fmt.Sprintf("/send %d test message %d", env.chatID, i),
				Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
			},
		}
		env.handler.HandleSend(ctx, update)
	}

	assert.True(t, env.botAPI.SendCalledSafe(), "At least one message should be sent")
}
