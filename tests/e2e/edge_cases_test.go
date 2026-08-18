package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_CreateSubscription_EmptyUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, env.chatID, "", "")
	require.NoError(t, err, "creating a subscription with an empty username should succeed")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("tgId_%d", env.chatID), sub.Username,
		"empty username must fall back to tgId_<telegram_id>")
}

func TestE2E_Subscription_SpecialCharactersInUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	// Telegram usernames may only contain [a-zA-Z0-9_]; anything else is not a
	// "real" username and must be replaced by the tgId_ fallback, not rejected.
	_, err := env.subService.Create(ctx, env.chatID, "test@user#123!", "")
	require.NoError(t, err)

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("tgId_%d", env.chatID), sub.Username,
		"invalid characters must fall back to tgId_<telegram_id>")
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
	createE2ESub(t, env.db, sub)
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
	createE2ESub(t, env.db, sub)

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.ErrorIs(t, err, database.ErrSubscriptionNotFound,
		"non-active subscription should not be returned by GetByTelegramID")
}
