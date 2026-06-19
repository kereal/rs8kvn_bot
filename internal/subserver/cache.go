package subserver

import (
	"bytes"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/metrics"
)

type noCopy struct{}

func (noCopy) Lock()   {}
func (noCopy) Unlock() {}

// cacheEntry holds a cached response body and headers with its expiry time.
type cacheEntry struct {
	body      []byte
	headers   map[string]string
	expiresAt time.Time
}

// Cache is an in-memory TTL cache for subscription responses.
type Cache struct {
	noCopy noCopy
	mu          sync.RWMutex
	entries     map[string]*cacheEntry
	ttl         time.Duration
	stopCh      chan struct{}
	stopped     atomic.Bool
	hits        atomic.Int64
	misses      atomic.Int64
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

// Get returns the cached body and headers for key, or nil,nil,false on miss/expiry.
func (c *Cache) Get(key string) ([]byte, map[string]string, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	expired := ok && time.Now().After(entry.expiresAt)
	c.mu.RUnlock()

	if !ok || expired {
		c.misses.Add(1)
		metrics.CacheMissesTotal.WithLabelValues("subserver").Inc()
		return nil, nil, false
	}

	c.hits.Add(1)
	metrics.CacheHitsTotal.WithLabelValues("subserver").Inc()
	return bytes.Clone(entry.body), cloneHeaders(entry.headers), true
}

// Set stores body and headers under key with the cache TTL.
func (c *Cache) Set(key string, body []byte, headers map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &cacheEntry{
		body:      bytes.Clone(body),
		headers:   cloneHeaders(headers),
		expiresAt: time.Now().Add(c.ttl),
	}
}

// cloneHeaders returns a shallow copy of a string map.
func cloneHeaders(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
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
	now := time.Now()

	c.mu.RLock()
	keys := make([]string, 0, len(c.entries))
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			keys = append(keys, k)
		}
	}
	c.mu.RUnlock()

	if len(keys) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		if e, ok := c.entries[k]; ok && now.After(e.expiresAt) {
			delete(c.entries, k)
		}
	}
}

// Stop shuts down the background cleanup loop. Safe to call multiple times.
func (c *Cache) Stop() {
	if c.stopped.Swap(true) {
		return
	}
	close(c.stopCh)
}
