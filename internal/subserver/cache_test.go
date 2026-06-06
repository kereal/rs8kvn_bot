package subserver

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	if err := testutil.InitLogger(m); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestCache_GetMiss(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	body, headers, ok := cache.Get("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, body)
	assert.Nil(t, headers)
}

func TestCache_SetAndGet(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	body := []byte("test-body")
	headers := map[string]string{"content-type": "text/plain"}
	cache.Set("key1", body, headers)

	gotBody, gotHeaders, ok := cache.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, body, gotBody)
	assert.Equal(t, headers, gotHeaders)
}

func TestCache_TTLExpiry(t *testing.T) {
	t.Parallel()

	cache := NewCache(10 * time.Millisecond)
	defer cache.Stop()

	cache.Set("key1", []byte("body"), nil)

	_, _, ok := cache.Get("key1")
	assert.True(t, ok)

	assert.Eventually(t, func() bool {
		_, _, ok := cache.Get("key1")
		return !ok
	}, 200*time.Millisecond, 5*time.Millisecond, "entry should expire after TTL")
}

func TestCache_Cleanup(t *testing.T) {
	t.Parallel()

	cache := NewCache(10 * time.Millisecond)
	defer cache.Stop()

	cache.Set("key1", []byte("body1"), nil)
	cache.Set("key2", []byte("body2"), nil)

	assert.Eventually(t, func() bool {
		cache.mu.RLock()
		count := len(cache.entries)
		cache.mu.RUnlock()
		return count == 0
	}, 200*time.Millisecond, 5*time.Millisecond, "all entries should be cleaned up after TTL")
}

func TestCache_Isolation(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	cache.Set("key1", []byte("body1"), nil)
	cache.Set("key2", []byte("body2"), nil)

	body1, _, ok1 := cache.Get("key1")
	body2, _, ok2 := cache.Get("key2")

	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, []byte("body1"), body1)
	assert.Equal(t, []byte("body2"), body2)
}

func TestCache_Delete(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	cache.Set("key1", []byte("body"), nil)
	cache.Delete("key1")

	_, _, ok := cache.Get("key1")
	assert.False(t, ok)
}

func TestCache_BodyCopy(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	original := []byte("original-body")
	cache.Set("key1", original, nil)

	original[0] = 'X'

	gotBody, _, _ := cache.Get("key1")
	assert.Equal(t, []byte("original-body"), gotBody)
}

func TestCache_HeadersCopy(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	headers := map[string]string{"content-type": "text/plain", "x-custom": "value"}
	cache.Set("key1", []byte("body"), headers)

	headers["content-type"] = "MUTATED"
	headers["x-new"] = "added"

	gotBody, gotHeaders, ok := cache.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, []byte("body"), gotBody)
	assert.Equal(t, "text/plain", gotHeaders["content-type"])
	assert.Equal(t, "value", gotHeaders["x-custom"])
	_, hasNew := gotHeaders["x-new"]
	assert.False(t, hasNew, "headers map should be cloned on Set")
}
