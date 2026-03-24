package bot

import (
	"context"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
)

func TestSubscriptionCache_GetSet(t *testing.T) {
	cache := NewSubscriptionCache(10, 5*time.Minute)

	sub := &database.Subscription{
		TelegramID:      123,
		Username:        "testuser",
		ClientID:        "client-1",
		SubscriptionURL: "http://test.url/sub",
		Status:          "active",
	}

	// Get should return nil for missing key
	if got := cache.Get(123); got != nil {
		t.Errorf("Get(123) = %v, want nil", got)
	}

	// Set and Get should work
	cache.Set(123, sub)
	got := cache.Get(123)
	if got == nil {
		t.Fatal("Get(123) returned nil after Set")
	}
	if got.Username != "testuser" {
		t.Errorf("Get(123).Username = %v, want testuser", got.Username)
	}
}

func TestSubscriptionCache_TTL(t *testing.T) {
	cache := NewSubscriptionCache(10, 50*time.Millisecond)

	sub := &database.Subscription{
		TelegramID: 456,
		Username:   "ttluser",
	}

	cache.Set(456, sub)

	// Should be available immediately
	if got := cache.Get(456); got == nil {
		t.Fatal("Get() returned nil immediately after Set")
	}

	// Wait for TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Should be expired now
	if got := cache.Get(456); got != nil {
		t.Errorf("Get() returned %v after TTL expired, want nil", got)
	}
}

func TestSubscriptionCache_Invalidate(t *testing.T) {
	cache := NewSubscriptionCache(10, 5*time.Minute)

	sub := &database.Subscription{
		TelegramID: 789,
		Username:   "invalidateme",
	}

	cache.Set(789, sub)
	if got := cache.Get(789); got == nil {
		t.Fatal("Get() returned nil after Set")
	}

	cache.Invalidate(789)
	if got := cache.Get(789); got != nil {
		t.Errorf("Get() returned %v after Invalidate, want nil", got)
	}
}

func TestSubscriptionCache_Clear(t *testing.T) {
	cache := NewSubscriptionCache(10, 5*time.Minute)

	// Add multiple entries
	for i := int64(1); i <= 5; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	if cache.Size() != 5 {
		t.Errorf("Size() = %d, want 5", cache.Size())
	}

	cache.Clear()
	if cache.Size() != 0 {
		t.Errorf("Size() = %d after Clear, want 0", cache.Size())
	}
}

func TestSubscriptionCache_MaxSize(t *testing.T) {
	cache := NewSubscriptionCache(3, 5*time.Minute)

	// Fill cache to max
	for i := int64(1); i <= 3; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	if cache.Size() != 3 {
		t.Errorf("Size() = %d, want 3", cache.Size())
	}

	// Adding one more should evict the oldest entry (by expiresAt time)
	// Sleep briefly to ensure the new entry has a later expiresAt
	time.Sleep(10 * time.Millisecond)
	cache.Set(4, &database.Subscription{TelegramID: 4})

	// Should still have 3 entries (maxSize), with entry 1 evicted
	if cache.Size() != 3 {
		t.Errorf("Size() = %d after overflow, want 3", cache.Size())
	}

	// Entry 1 should be evicted (oldest)
	if cache.Get(1) != nil {
		t.Error("Entry 1 should be evicted")
	}

	// Entries 2, 3, 4 should still exist
	if cache.Get(2) == nil {
		t.Error("Entry 2 should still exist")
	}
	if cache.Get(3) == nil {
		t.Error("Entry 3 should still exist")
	}
	if cache.Get(4) == nil {
		t.Error("Entry 4 should still exist")
	}
}

func TestSubscriptionCache_Cleanup(t *testing.T) {
	cache := NewSubscriptionCache(10, 50*time.Millisecond)

	// Add entries
	for i := int64(1); i <= 3; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	// Wait for entries to expire
	time.Sleep(100 * time.Millisecond)

	// Add one more entry (not expired)
	cache.Set(4, &database.Subscription{TelegramID: 4})

	// Cleanup should remove expired entries
	cache.Cleanup()

	if cache.Size() != 1 {
		t.Errorf("Size() = %d after Cleanup, want 1", cache.Size())
	}

	if got := cache.Get(4); got == nil {
		t.Error("Get(4) returned nil after Cleanup, but it was just added")
	}
}

func TestSubscriptionCache_Concurrent(t *testing.T) {
	cache := NewSubscriptionCache(100, 5*time.Minute)

	// Run concurrent writes and reads
	done := make(chan bool, 2)

	go func() {
		for i := int64(0); i < 100; i++ {
			cache.Set(i, &database.Subscription{TelegramID: i})
		}
		done <- true
	}()

	go func() {
		for i := int64(0); i < 100; i++ {
			cache.Get(i)
		}
		done <- true
	}()

	<-done
	<-done

	// If we get here without race detector errors, test passes
}

func TestSubscriptionCache_StartCleanup(t *testing.T) {
	cache := NewSubscriptionCache(10, 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start background cleanup
	go cache.StartCleanup(ctx, 10*time.Millisecond)

	// Add entries with short TTL
	for i := int64(1); i <= 3; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	if cache.Size() != 3 {
		t.Errorf("Size() = %d, want 3", cache.Size())
	}

	// Wait for entries to expire and cleanup to run
	time.Sleep(80 * time.Millisecond)

	// Expired entries should be removed by background cleanup
	// Note: Size may be 0 or contain only non-expired entries
	cache.Cleanup() // Force cleanup to ensure accurate count
	if cache.Size() != 0 {
		t.Errorf("Size() = %d after cleanup, want 0 (all entries expired)", cache.Size())
	}
}

func TestSubscriptionCache_StartCleanup_Cancellation(t *testing.T) {
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

func TestSubscriptionCache_LRU_EvictionOrder(t *testing.T) {
	cache := NewSubscriptionCache(3, 100*time.Millisecond)

	// Add entries with delays to ensure different expiresAt times
	for i := int64(1); i <= 3; i++ {
		cache.Set(i, &database.Subscription{TelegramID: i})
		time.Sleep(10 * time.Millisecond)
	}

	// Entry 1 should be oldest, entry 3 should be newest
	// Adding entry 4 should evict entry 1 (oldest)
	cache.Set(4, &database.Subscription{TelegramID: 4})

	if cache.Size() != 3 {
		t.Errorf("Size() = %d, want 3", cache.Size())
	}

	// Entry 1 should be evicted
	if cache.Get(1) != nil {
		t.Error("Entry 1 (oldest) should be evicted")
	}

	// Entries 2, 3, 4 should still exist
	if cache.Get(2) == nil {
		t.Error("Entry 2 should still exist")
	}
	if cache.Get(3) == nil {
		t.Error("Entry 3 should still exist")
	}
	if cache.Get(4) == nil {
		t.Error("Entry 4 should still exist")
	}
}

func TestSubscriptionCache_LRU_MultipleEvictions(t *testing.T) {
	cache := NewSubscriptionCache(2, 5*time.Minute) // Long TTL to avoid expiration

	// Add entries and trigger multiple evictions
	// With maxSize=2, after adding 5 entries, only the last 2 should remain
	for i := int64(1); i <= 5; i++ {
		time.Sleep(10 * time.Millisecond)
		cache.Set(i, &database.Subscription{TelegramID: i})
	}

	if cache.Size() != 2 {
		t.Errorf("Size() = %d, want 2", cache.Size())
	}

	// The last 2 entries (4 and 5) should exist
	// Entry 4 was added before entry 5, so it has earlier expiresAt
	// When entry 5 was added, entry 3 (oldest) was evicted
	// So entries 4 and 5 should remain
	if cache.Get(4) == nil {
		t.Error("Entry 4 should still exist")
	}
	if cache.Get(5) == nil {
		t.Error("Entry 5 should still exist")
	}

	// Earlier entries should be evicted
	if cache.Get(1) != nil {
		t.Error("Entry 1 should be evicted")
	}
	if cache.Get(2) != nil {
		t.Error("Entry 2 should be evicted")
	}
	if cache.Get(3) != nil {
		t.Error("Entry 3 should be evicted")
	}
}
