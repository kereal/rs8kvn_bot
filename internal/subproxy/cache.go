package subproxy

import (
	"bytes"
	"sync"
	"time"
)

type cacheEntry struct {
	body      []byte
	headers   map[string]string
	expiresAt time.Time
}

type Cache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	stopCh  chan struct{}
}

func NewCache(ttl time.Duration) *Cache {
	c := &Cache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

func (c *Cache) Get(key string) ([]byte, map[string]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, nil, false
	}

	headersCopy := make(map[string]string, len(entry.headers))
	for k, v := range entry.headers {
		headersCopy[k] = v
	}
	return entry.body, headersCopy, true
}

func (c *Cache) Set(key string, body []byte, headers map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	headersCopy := make(map[string]string, len(headers))
	for k, v := range headers {
		headersCopy[k] = v
	}

	c.entries[key] = &cacheEntry{
		body:      bytes.Clone(body),
		headers:   headersCopy,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

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

func (c *Cache) Stop() {
	close(c.stopCh)
}
