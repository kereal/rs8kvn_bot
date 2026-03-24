package bot

import (
	"context"
	"sync"
	"time"

	"rs8kvn_bot/internal/database"
)

// cacheEntry represents a cached subscription with expiration time.
type cacheEntry struct {
	sub       *database.Subscription
	expiresAt time.Time
}

// SubscriptionCache is a thread-safe LRU cache for subscriptions with TTL.
type SubscriptionCache struct {
	mu      sync.RWMutex
	items   map[int64]*cacheEntry // telegram_id -> cache entry
	maxSize int
	ttl     time.Duration
}

// NewSubscriptionCache creates a new subscription cache.
// maxSize is the maximum number of entries, ttl is the time-to-live for each entry.
func NewSubscriptionCache(maxSize int, ttl time.Duration) *SubscriptionCache {
	return &SubscriptionCache{
		items:   make(map[int64]*cacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves a subscription from cache.
// Returns nil if not found or expired.
func (c *SubscriptionCache) Get(telegramID int64) *database.Subscription {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.items[telegramID]
	if !ok {
		return nil
	}

	if time.Now().After(entry.expiresAt) {
		// Entry expired, remove it
		delete(c.items, telegramID)
		return nil
	}

	return entry.sub
}

// Set adds or updates a subscription in cache.
func (c *SubscriptionCache) Set(telegramID int64, sub *database.Subscription) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If at capacity, evict oldest entry (by expiresAt time)
	if len(c.items) >= c.maxSize {
		var oldestID int64
		var oldestTime time.Time
		for id, entry := range c.items {
			if oldestTime.IsZero() || entry.expiresAt.Before(oldestTime) {
				oldestID = id
				oldestTime = entry.expiresAt
			}
		}
		if oldestID != 0 {
			delete(c.items, oldestID)
		}
	}

	c.items[telegramID] = &cacheEntry{
		sub:       sub,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate removes a subscription from cache.
func (c *SubscriptionCache) Invalidate(telegramID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, telegramID)
}

// Clear removes all entries from cache.
func (c *SubscriptionCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[int64]*cacheEntry)
}

// Size returns the current number of entries in cache.
func (c *SubscriptionCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Cleanup removes expired entries. Should be called periodically.
func (c *SubscriptionCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for telegramID, entry := range c.items {
		if now.After(entry.expiresAt) {
			delete(c.items, telegramID)
		}
	}
}

// StartCleanup starts a background goroutine that periodically removes expired entries.
// Call with context.Background() or a long-lived context, and cancel when application stops.
func (c *SubscriptionCache) StartCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.Cleanup()
		case <-ctx.Done():
			return
		}
	}
}
