package bot

import (
	"container/list"
	"context"
	"sync"
	"time"

	"rs8kvn_bot/internal/database"
)

type cacheEntry struct {
	sub       *database.Subscription
	expiresAt time.Time
}

type lruItem struct {
	telegramID int64
	entry      *cacheEntry
}

type SubscriptionCache struct {
	mu      sync.RWMutex
	items   map[int64]*list.Element // telegram_id -> list element
	lru     *list.List              // front = LRU, back = MRU
	maxSize int
	ttl     time.Duration
}

func NewSubscriptionCache(maxSize int, ttl time.Duration) *SubscriptionCache {
	return &SubscriptionCache{
		items:   make(map[int64]*list.Element),
		lru:     list.New(),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

func (c *SubscriptionCache) Get(telegramID int64) *database.Subscription {
	// Fast path: read under RLock
	c.mu.RLock()
	elem, ok := c.items[telegramID]
	if !ok {
		c.mu.RUnlock()
		return nil
	}
	item := elem.Value.(*lruItem)
	expired := time.Now().After(item.entry.expiresAt)
	sub := item.entry.sub
	c.mu.RUnlock()

	if expired {
		// Slow path: lazy eviction under exclusive Lock
		c.mu.Lock()
		// Re-check: another goroutine may have already evicted or updated it
		if elem, ok = c.items[telegramID]; ok {
			it := elem.Value.(*lruItem)
			if time.Now().After(it.entry.expiresAt) {
				c.lru.Remove(elem)
				delete(c.items, telegramID)
			}
		}
		c.mu.Unlock()
		return nil
	}

	// LRU promotion: MoveToBack under exclusive Lock
	c.mu.Lock()
	// Re-check: element may have been evicted between RUnlock and Lock
	if elem, ok = c.items[telegramID]; ok {
		c.lru.MoveToBack(elem)
	}
	c.mu.Unlock()

	return sub
}

func (c *SubscriptionCache) Set(telegramID int64, sub *database.Subscription) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[telegramID]; ok {
		item := elem.Value.(*lruItem)
		item.entry.sub = sub
		item.entry.expiresAt = time.Now().Add(c.ttl)
		c.lru.MoveToBack(elem)
		return
	}

	if len(c.items) >= c.maxSize {
		front := c.lru.Front()
		if front != nil {
			item := front.Value.(*lruItem)
			c.lru.Remove(front)
			delete(c.items, item.telegramID)
		}
	}

	newItem := &lruItem{
		telegramID: telegramID,
		entry: &cacheEntry{
			sub:       sub,
			expiresAt: time.Now().Add(c.ttl),
		},
	}
	elem := c.lru.PushBack(newItem)
	c.items[telegramID] = elem
}

func (c *SubscriptionCache) Invalidate(telegramID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[telegramID]
	if !ok {
		return
	}
	c.lru.Remove(elem)
	delete(c.items, telegramID)
}

func (c *SubscriptionCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[int64]*list.Element)
	c.lru = list.New()
}

func (c *SubscriptionCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

func (c *SubscriptionCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for elem := c.lru.Front(); elem != nil; {
		item := elem.Value.(*lruItem)
		next := elem.Next()
		if now.After(item.entry.expiresAt) {
			c.lru.Remove(elem)
			delete(c.items, item.telegramID)
		}
		elem = next
	}
}

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
