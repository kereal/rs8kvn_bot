package bot

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
)

type cacheEntry struct {
	sub       *database.Subscription
	expiresAt time.Time
}

type lruItem struct {
	telegramID int64
	subID      string
	entry      *cacheEntry
}

type noCopy struct{}

func (noCopy) Lock()   {}
func (noCopy) Unlock() {}

type SubscriptionCache struct {
	noCopy  noCopy
	mu      sync.RWMutex
	items   map[int64]*list.Element  // telegram_id -> list element
	bySubID map[string]*list.Element // subscription_id -> list element
	lru     *list.List               // front = LRU, back = MRU
	maxSize int
	ttl     time.Duration
}

func NewSubscriptionCache(maxSize int, ttl time.Duration) *SubscriptionCache {
	return &SubscriptionCache{
		items:   make(map[int64]*list.Element),
		bySubID: make(map[string]*list.Element),
		lru:     list.New(),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

func (c *SubscriptionCache) Get(telegramID int64) *database.Subscription {
	c.mu.RLock()
	elem, ok := c.items[telegramID]
	if !ok {
		c.mu.RUnlock()
		metrics.CacheMissesTotal.WithLabelValues("subscription").Inc()
		return nil
	}
	item := elem.Value.(*lruItem)
	expired := time.Now().After(item.entry.expiresAt)
	sub := item.entry.sub
	c.mu.RUnlock()

	if expired {
		c.mu.Lock()
		if elem, ok = c.items[telegramID]; ok {
			it := elem.Value.(*lruItem)
			if time.Now().After(it.entry.expiresAt) {
				c.removeElement(elem)
			}
		}
		c.mu.Unlock()
		metrics.CacheMissesTotal.WithLabelValues("subscription").Inc()
		return nil
	}

	c.mu.Lock()
	if elem, ok = c.items[telegramID]; ok {
		c.lru.MoveToBack(elem)
	}
	c.mu.Unlock()

	metrics.CacheHitsTotal.WithLabelValues("subscription").Inc()
	return sub
}

func (c *SubscriptionCache) Set(telegramID int64, sub *database.Subscription) {
	if sub == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	subID := sub.SubscriptionID

	if elem, ok := c.items[telegramID]; ok {
		item := elem.Value.(*lruItem)
		item.entry.sub = sub
		item.entry.expiresAt = time.Now().Add(c.ttl)
		delete(c.bySubID, item.subID)
		item.subID = subID
		c.lru.MoveToBack(elem)
		c.bySubID[subID] = elem
		return
	}

	if len(c.items) >= c.maxSize {
		front := c.lru.Front()
		if front != nil {
			_ = front.Value.(*lruItem)
			c.removeElement(front)
		}
	}

	newItem := &lruItem{
		telegramID: telegramID,
		subID:      subID,
		entry: &cacheEntry{
			sub:       sub,
			expiresAt: time.Now().Add(c.ttl),
		},
	}
	elem := c.lru.PushBack(newItem)
	c.items[telegramID] = elem
	c.bySubID[subID] = elem
}

func (c *SubscriptionCache) Invalidate(telegramID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[telegramID]
	if !ok {
		return
	}
	c.removeElement(elem)
}

func (c *SubscriptionCache) InvalidateBySubID(subID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.bySubID[subID]
	if !ok {
		return
	}
	c.removeElement(elem)
}

func (c *SubscriptionCache) removeElement(elem *list.Element) {
	item := elem.Value.(*lruItem)
	c.lru.Remove(elem)
	delete(c.items, item.telegramID)
	if item.subID != "" {
		delete(c.bySubID, item.subID)
	}
}

func (c *SubscriptionCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[int64]*list.Element)
	c.bySubID = make(map[string]*list.Element)
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
			c.removeElement(elem)
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
