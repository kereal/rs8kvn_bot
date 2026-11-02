package bot

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

func TestSubscriptionCache_GetSet(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(10, 5*time.Minute)

	sub := &database.Subscription{
		TelegramID: 123,
		Username:   "testuser",
		ClientID:   "client-1",
		Status:     "active",
	}

	// Get should return nil for missing key
	assert.Nil(t, cache.Get(123), "Get(123) should return nil for missing key")

	// Set and Get should work
	cache.Set(123, sub)
	got := cache.Get(123)
	require.NotNil(t, got, "Get(123) returned nil after Set")
	assert.Equal(t, "testuser", got.Username, "Username")
}

func TestSubscriptionCache_TTL(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(10, 10*time.Millisecond)

	sub := &database.Subscription{
		TelegramID: 456,
		Username:   "ttluser",
	}

	cache.Set(456, sub)

	// Should be available immediately
	require.NotNil(t, cache.Get(456), "Get() returned nil immediately after Set")

	// Wait for TTL to expire
	assert.Eventually(t, func() bool {
		return cache.Get(456) == nil
	}, 2*time.Second, 5*time.Millisecond, "Get() should return nil after TTL expired")
}

func TestSubscriptionCache_Invalidate(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(10, 5*time.Minute)

	sub := &database.Subscription{
		TelegramID: 789,
		Username:   "invalidateme",
	}

	cache.Set(789, sub)
	require.NotNil(t, cache.Get(789), "Get() returned nil after Set")

	cache.Invalidate(789)
	assert.Nil(t, cache.Get(789), "Get() should return nil after Invalidate")
}

func TestSubscriptionCache_Clear(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(10, 5*time.Minute)

	// Add multiple entries
	for i := int64(1); i <= 5; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	assert.Equal(t, 5, cache.Size(), "Size()")

	cache.Clear()
	assert.Equal(t, 0, cache.Size(), "Size() after Clear")
}

func TestSubscriptionCache_MaxSize(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(3, 5*time.Minute)

	// Fill cache to max
	for i := int64(1); i <= 3; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	assert.Equal(t, 3, cache.Size(), "Size()")

	// Adding one more should evict the oldest entry (by expiresAt time)
	cache.Set(4, &database.Subscription{TelegramID: 4})

	// Should still have 3 entries (maxSize), with entry 1 evicted
	assert.Equal(t, 3, cache.Size(), "Size() after overflow")

	// Entry 1 should be evicted (oldest)
	assert.Nil(t, cache.Get(1), "Entry 1 should be evicted")

	// Entries 2, 3, 4 should still exist
	assert.NotNil(t, cache.Get(2), "Entry 2 should still exist")
	assert.NotNil(t, cache.Get(3), "Entry 3 should still exist")
	assert.NotNil(t, cache.Get(4), "Entry 4 should still exist")
}

func TestSubscriptionCache_Cleanup(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(10, 10*time.Millisecond)

	// Add entries
	for i := int64(1); i <= 3; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	// Wait for entries to expire
	assert.Eventually(t, func() bool {
		return cache.Get(1) == nil
	}, 2*time.Second, 5*time.Millisecond, "entries should expire")

	// Add one more entry (not expired)
	cache.Set(4, &database.Subscription{TelegramID: 4})

	// Cleanup should remove expired entries
	cache.Cleanup()

	assert.Equal(t, 1, cache.Size(), "Size() after Cleanup")
	assert.NotNil(t, cache.Get(4), "Get(4) returned nil after Cleanup, but it was just added")
}

func TestSubscriptionCache_StartCleanup(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(10, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Entries are added before the cleanup loop starts: с 2-миллисекундным
	// тикером фоновая чистка могла вытеснить записи между Set и проверкой
	// размера, и тест падал на ровном месте.
	for i := int64(1); i <= 3; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	assert.Equal(t, 3, cache.Size(), "Size()")

	go cache.StartCleanup(ctx, 2*time.Millisecond)

	// Wait for entries to expire
	assert.Eventually(t, func() bool {
		return cache.Get(1) == nil
	}, 100*time.Millisecond, 1*time.Millisecond, "entries should expire")

	// Expired entries should be removed by background cleanup
	// Note: Size may be 0 or contain only non-expired entries
	cache.Cleanup() // Force cleanup to ensure accurate count
	assert.Equal(t, 0, cache.Size(), "Size() after cleanup (all entries expired)")
}

func TestSubscriptionCache_StartCleanup_Cancellation(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(10, 5*time.Minute)
	ctx, cancel := context.WithCancel(context.Background())

	// Start background cleanup
	done := make(chan bool, 1)

	go func() {
		cache.StartCleanup(ctx, 10*time.Millisecond)

		done <- true
	}()

	// Cancel context immediately
	cancel()

	// Wait for goroutine to exit
	select {
	case <-done:
		// Goroutine exited successfully
	case <-time.After(100 * time.Millisecond):
		t.Error("StartCleanup did not exit after context cancellation")
	}
}

func TestSubscriptionCache_LRU_MultipleEvictions(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(2, 5*time.Minute) // Long TTL to avoid expiration

	// Add entries and trigger multiple evictions
	// With maxSize=2, after adding 5 entries, only the last 2 should remain
	for i := int64(1); i <= 5; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	assert.Equal(t, 2, cache.Size(), "Size()")

	// The last 2 entries (4 and 5) should exist
	// Entry 4 was added before entry 5, so it has earlier expiresAt
	// When entry 5 was added, entry 3 (oldest) was evicted
	// So entries 4 and 5 should remain
	assert.NotNil(t, cache.Get(4), "Entry 4 should still exist")
	assert.NotNil(t, cache.Get(5), "Entry 5 should still exist")

	// Earlier entries should be evicted
	assert.Nil(t, cache.Get(1), "Entry 1 should be evicted")
	assert.Nil(t, cache.Get(2), "Entry 2 should be evicted")
	assert.Nil(t, cache.Get(3), "Entry 3 should be evicted")
}
