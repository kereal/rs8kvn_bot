package subserver

import (
	"bytes"
	"sync"
	"time"

	"rs8kvn_bot/internal/metrics"
)

// cacheEntry holds a cached response body with its expiry time.
type cacheEntry struct {
	body      []byte
	expiresAt time.Time
}

// Cache is an in-memory TTL cache for subscription responses.
// It provides concurrent-safe Get/Set/Delete and runs a background goroutine
// that evicts expired entries at half-TTL intervals.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	stopCh  chan struct{}
}

// NewCache creates a new Cache with the given TTL and starts the cleanup loop.
func NewCache(ttl time.Duration) *Cache {
	c := &Cache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

// Get returns the cached body for key and true, or nil and false on miss/expiry.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		metrics.CacheMissesTotal.WithLabelValues("subserver").Inc()
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		metrics.CacheMissesTotal.WithLabelValues("subserver").Inc()
		return nil, false
	}

	metrics.CacheHitsTotal.WithLabelValues("subserver").Inc()
	return bytes.Clone(entry.body), true
}

// Set stores body under key with the cache TTL.
func (c *Cache) Set(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &cacheEntry{
		body:      bytes.Clone(body),
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Delete removes the entry for key from the cache.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// cleanupLoop periodically evicts expired entries every TTL/2.
func (c *Cache) cleanupLoop() {
	ticker := time.NewTicker(c.ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopCh:
			return
		}
	}
}

// cleanup removes all expired entries from the cache.
func (c *Cache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

// Stop shuts down the background cleanup loop.
func (c *Cache) Stop() {
	close(c.stopCh)
}
