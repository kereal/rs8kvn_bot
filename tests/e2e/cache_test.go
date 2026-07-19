

package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/database"

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
		TelegramID:     chatID,
		Username:       "cacheduser",
		ClientID:       "client-123",
		SubscriptionID: "sub-123",
		Status:         "active",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub, ""))

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
	assert.True(t, errors.Is(err, database.ErrSubscriptionNotFound),
		"error should match database.ErrSubscriptionNotFound: %v", err)
}

func TestE2E_Cache_DbHitOnCacheMiss(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()
	chatID := int64(900003)

	sub := &database.Subscription{
		TelegramID:     chatID,
		Username:       "dbuser",
		ClientID:       "client-789",
		SubscriptionID: "sub-789",
		Status:         "active",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub, ""))

	// Clear the subscription cache so the subsequent lookup is served from the
	// database, not from a stale in-memory entry.
	env.handler.Cache().Invalidate(chatID)
	assert.Nil(t, env.handler.Cache().Get(chatID), "subscription must be absent from cache before GetByTelegramID")

	fetched, err := env.db.GetByTelegramID(ctx, chatID)
	require.NoError(t, err)
	assert.Equal(t, chatID, fetched.TelegramID)
}
func TestE2E_Cache_ExpiredEntry(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	// Detached test cache with a deterministic short TTL so the entry expires
	// without relying on a fixed time.Sleep.
	cache := bot.NewSubscriptionCache(100, 5*time.Millisecond)
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	cache.Set(env.chatID, sub)

	// Entry must be gone after the short TTL elapses.
	require.Eventually(t, func() bool {
		return cache.Get(env.chatID) == nil
	}, 100*time.Millisecond, 1*time.Millisecond,
		"entry should expire from the test cache after the short TTL")

	// GetByTelegramID still restores and returns the subscription from the DB.
	restored, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, restored.TelegramID)
}
func TestE2E_Cache_CacheInvalidation(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	env.handler.Cache().Invalidate(env.chatID)

	ctx := context.Background()

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	// Initial read populates the handler cache with the subscription.
	sub1 := env.handler.Cache().Get(env.chatID)
	if sub1 == nil {
		sub1, err = env.db.GetByTelegramID(ctx, env.chatID)
		require.NoError(t, err)
		env.handler.Cache().Set(env.chatID, sub1)
	}
	require.NoError(t, err)
	assert.Equal(t, env.username, sub1.Username)

	// Mutate a non-status field through the database, then invalidate the
	// cache so a subsequent read is forced to hit the database. (Status is
	// intentionally left "active": GetByTelegramID filters on active rows.)
	sub1.Username = "changed_username"
	require.NoError(t, env.db.UpdateSubscription(ctx, sub1))
	env.handler.Cache().Invalidate(env.chatID)

	// Cache entry must be gone after invalidation.
	assert.Nil(t, env.handler.Cache().Get(env.chatID), "cache entry should be absent after invalidation")

	// Re-reading from the database returns the fresh, changed value — not the
	// original username cached earlier.
	sub2, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, "changed_username", sub2.Username)
}
