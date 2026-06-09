package subserver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kereal/rs8kvn_bot/internal/config"
)

func TestService_CacheThroughService(t *testing.T) {
	t.Parallel()

	svc := NewService(config.SubServerCacheTTL)
	defer svc.Stop()

	svc.SetCache("key1", []byte("hello"), nil)
	body, _, ok := svc.GetCache("key1")
	assert.True(t, ok)
	assert.Equal(t, []byte("hello"), body)
}

func TestService_CacheMiss(t *testing.T) {
	t.Parallel()

	svc := NewService(config.SubServerCacheTTL)
	defer svc.Stop()

	body, _, ok := svc.GetCache("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, body)
}

func TestService_InvalidateCache(t *testing.T) {
	t.Parallel()

	svc := NewService(config.SubServerCacheTTL)
	defer svc.Stop()

	svc.SetCache("key1", []byte("data"), nil)
	svc.InvalidateCache("key1")

	body, _, ok := svc.GetCache("key1")
	assert.False(t, ok)
	assert.Nil(t, body)
}
