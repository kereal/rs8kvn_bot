package subserver

import "time"

// Service wraps the subscription response cache.
// It is a thin adapter that the web layer uses for cache operations.
// Since v2.3.0 GetCache/SetCache carry the response headers alongside the
// body so cache hits can be replayed verbatim.
type Service struct {
	cache *Cache
}

// NewService creates a new Service with a cache TTL.
func NewService(ttl time.Duration) *Service {
	return &Service{
		cache: NewCache(ttl),
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
