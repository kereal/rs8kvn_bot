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

// TestService_CacheIntegration confirms the Service cache adapter routes Set,
// Get and Invalidate to the underlying cache in one pass instead of three
// per-method clones of the cache_test.go coverage. The behavior itself (TTL,
// isolation, copies) is exercised directly in cache_test.go.
func TestService_CacheIntegration(t *testing.T) {
	t.Parallel()

	svc := NewService(config.SubServerCacheTTL)
	defer svc.Stop()

	// Set then Get round-trips through the Service.
	svc.SetCache("key1", []byte("hello"), nil)
	body, headers, ok := svc.GetCache("key1")
	require.True(t, ok)
	require.Equal(t, []byte("hello"), body)
	require.Nil(t, headers)

	// An unknown key is a miss.
	missBody, _, miss := svc.GetCache("nonexistent")
	require.False(t, miss)
	require.Nil(t, missBody)

	// Invalidate removes the entry so the next Get misses again.
	svc.InvalidateCache("key1")
	_, _, gone := svc.GetCache("key1")
	require.False(t, gone)
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
