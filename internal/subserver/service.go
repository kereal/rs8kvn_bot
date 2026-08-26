package subserver

import (
	"sync"
	"time"
)

// Service wraps the subscription response cache.
// It is a thin adapter that the web layer uses for cache operations.
// Since v2.3.0 GetCache/SetCache carry the response headers alongside the
// body so cache hits can be replayed verbatim.
type Service struct {
	cache *Cache
	// analyticsLocks serializes the per-subscription analytics read-modify-write
	// (devices/IPs) only across requests for the SAME subID, so cache misses for
	// different subscriptions do not block each other.
	analyticsLocks analyticsKeyedLock
}

// NewService creates a new Service with a cache TTL.
func NewService(ttl time.Duration) *Service {
	return &Service{
		cache: NewCache(ttl),
	}
}

// analyticsKeyedLock is a per-key mutex: Lock(key) returns an unlock func that
// must be called (typically via defer). Requests for the same key serialize;
// requests for different keys run in parallel. Entries are refcounted and
// removed when the last waiter leaves, so the map does not grow unboundedly.
type analyticsKeyedLock struct {
	mu    sync.Mutex
	locks map[string]*analyticsRefLock
}

type analyticsRefLock struct {
	mu  sync.Mutex
	ref int
}

// Lock serializes the caller on key and returns the matching unlock function.
func (a *analyticsKeyedLock) Lock(key string) func() {
	a.mu.Lock()
	if a.locks == nil {
		a.locks = make(map[string]*analyticsRefLock)
	}
	rl, ok := a.locks[key]
	if !ok {
		rl = &analyticsRefLock{}
		a.locks[key] = rl
	}
	rl.ref++
	a.mu.Unlock()

	rl.mu.Lock()
	return func() {
		rl.mu.Unlock()
		a.mu.Lock()
		rl.ref--
		if rl.ref == 0 {
			delete(a.locks, key)
		}
		a.mu.Unlock()
	}
}

// GetCache returns the cached body and headers for key, or nil,nil,false on miss.
func (s *Service) GetCache(key string) ([]byte, map[string]string, bool) {
	return s.cache.Get(key)
}

// SetCache stores body and headers under key in the cache.
func (s *Service) SetCache(key string, body []byte, headers map[string]string) {
	s.cache.Set(key, body, headers)
}

// InvalidateCache removes the entry for key from the cache.
func (s *Service) InvalidateCache(key string) {
	s.cache.Delete(key)
}

// Stop shuts down the background cache cleanup loop.
func (s *Service) Stop() {
	s.cache.Stop()
}
