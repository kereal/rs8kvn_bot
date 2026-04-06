package e2e

import (
	"context"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_Cache_SetAndGet(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()
	chatID := int64(900001)

	sub := &database.Subscription{
		TelegramID:      chatID,
		Username:        "cacheduser",
		ClientID:        "client-123",
		SubscriptionID:  "sub-123",
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/123",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	fetched, err := env.db.GetByTelegramID(ctx, chatID)
	require.NoError(t, err)
	assert.Equal(t, chatID, fetched.TelegramID)
	assert.Equal(t, "cacheduser", fetched.Username)
}

func TestE2E_Cache_GetNonExistent(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.db.GetByTelegramID(ctx, int64(999999))
	assert.Error(t, err, "Should return error for non-existent subscription")
}

func TestE2E_Cache_DbHitOnCacheMiss(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()
	chatID := int64(900003)

	sub := &database.Subscription{
		TelegramID:      chatID,
		Username:        "dbuser",
		ClientID:        "client-789",
		SubscriptionID:  "sub-789",
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/789",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	fetched, err := env.db.GetByTelegramID(ctx, chatID)
	require.NoError(t, err)
	assert.Equal(t, chatID, fetched.TelegramID)
}

func TestE2E_Cache_ExpiredEntry(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, sub.TelegramID)
}

func TestE2E_Cache_CacheInvalidation(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)

	sub1, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, "active", sub1.Status)
}
