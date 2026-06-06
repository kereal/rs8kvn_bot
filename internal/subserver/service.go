package subserver

import "time"

// Service wraps the subscription response cache.
// It is a thin adapter that the web layer uses for cache operations.
type Service struct {
	cache *Cache
}

// NewService creates a new Service with a cache TTL.
func NewService(ttl time.Duration) *Service {
	return &Service{
		cache: NewCache(ttl),
	}
}

// GetCache returns the cached body for key, or nil,false on miss.
func (s *Service) GetCache(key string) ([]byte, bool) {
	return s.cache.Get(key)
}

// SetCache stores body under key in the cache.
func (s *Service) SetCache(key string, body []byte) {
	s.cache.Set(key, body)
}

// InvalidateCache removes the entry for key from the cache.
func (s *Service) InvalidateCache(key string) {
	s.cache.Delete(key)
}

// Stop shuts down the background cache cleanup loop.
func (s *Service) Stop() {
	s.cache.Stop()
}
