package bot

import (
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

	// Adding one more should clear all entries (simple eviction strategy)
	cache.Set(4, &database.Subscription{TelegramID: 4})
	if cache.Size() != 1 {
		t.Errorf("Size() = %d after overflow, want 1", cache.Size())
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
