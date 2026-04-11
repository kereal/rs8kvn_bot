package subproxy

import (
	"sync"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

type Service struct {
	cache   *Cache
	cfg     *config.Config
	headers map[string]string
	servers []string
	mu      sync.RWMutex
}

func NewService(cfg *config.Config) *Service {
	svc := &Service{
		cache:   NewCache(config.SubProxyCacheTTL),
		cfg:     cfg,
		headers: make(map[string]string),
	}

	if cfg.SubExtraServersEnabled && cfg.SubExtraServersFile != "" {
		extra, err := LoadExtraConfig(cfg.SubExtraServersFile)
		if err != nil {
			logger.Warn("Subscription proxy: failed to load extra config file",
				zap.String("file", cfg.SubExtraServersFile),
				zap.Error(err))
		} else {
			svc.headers = extra.Headers
			svc.servers = extra.Servers
			logger.Info("Subscription proxy: extra config loaded",
				zap.String("file", cfg.SubExtraServersFile),
				zap.Int("headers", len(extra.Headers)),
				zap.Int("servers", len(extra.Servers)))
		}
	} else {
		logger.Info("Subscription proxy: extra config disabled or no file configured")
	}

	return svc
}

func (s *Service) GetExtraHeaders() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]string, len(s.headers))
	for k, v := range s.headers {
		result[k] = v
	}
	return result
}

func (s *Service) GetExtraServers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]string, len(s.servers))
	copy(result, s.servers)
	return result
}

func (s *Service) ReloadConfig() {
	if !s.cfg.SubExtraServersEnabled || s.cfg.SubExtraServersFile == "" {
		s.mu.Lock()
		s.headers = make(map[string]string)
		s.servers = nil
		s.mu.Unlock()
		return
	}

	extra, err := LoadExtraConfig(s.cfg.SubExtraServersFile)
	if err != nil {
		logger.Warn("Subscription proxy: failed to reload extra config",
			zap.String("file", s.cfg.SubExtraServersFile),
			zap.Error(err))
		return
	}

	s.mu.Lock()
	oldHeaders := len(s.headers)
	oldServers := len(s.servers)
	s.headers = extra.Headers
	s.servers = extra.Servers
	s.mu.Unlock()

	logger.Debug("Subscription proxy: extra config reloaded",
		zap.String("file", s.cfg.SubExtraServersFile),
		zap.Int("old_headers", oldHeaders),
		zap.Int("new_headers", len(extra.Headers)),
		zap.Int("old_servers", oldServers),
		zap.Int("new_servers", len(extra.Servers)))
}

func (s *Service) GetCache(key string) ([]byte, map[string]string, bool) {
	return s.cache.Get(key)
}

func (s *Service) SetCache(key string, body []byte, headers map[string]string) {
	s.cache.Set(key, body, headers)
}

func (s *Service) InvalidateCache(key string) {
	s.cache.Delete(key)
}

func (s *Service) Stop() {
	s.cache.Stop()
}

func (s *Service) StartReloadLoop(interval time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.ReloadConfig()
		case <-stopCh:
			return
		}
	}
}
