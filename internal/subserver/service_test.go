package subserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestAccessLogger_CloseWithContextCanceled(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "subserver.log")
	accessLogger, err := NewAccessLogger(logPath)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = accessLogger.CloseWithContext(ctx)
	// CloseWithContext must close the file even if the context is already
	// cancelled (returns context.Canceled on the slow path, or nil if the
	// async writer already finished — both are valid terminal states).
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseWithContext returned unexpected error: %v", err)
	}

	_, err = os.Stat(logPath)
	assert.NoError(t, err)
}
