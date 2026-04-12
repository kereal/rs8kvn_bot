package subproxy

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
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
	headers := map[string]string{"Content-Type": "text/plain"}
	cache.Set("key1", body, headers)

	gotBody, gotHeaders, ok := cache.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, body, gotBody)
	assert.Equal(t, "text/plain", gotHeaders["Content-Type"])
}

func TestCache_TTLExpiry(t *testing.T) {
	t.Parallel()

	cache := NewCache(10 * time.Millisecond)
	defer cache.Stop()

	cache.Set("key1", []byte("body"), map[string]string{"K": "V"})

	_, _, ok := cache.Get("key1")
	assert.True(t, ok)

	time.Sleep(20 * time.Millisecond)

	_, _, ok = cache.Get("key1")
	assert.False(t, ok)
}

func TestCache_Cleanup(t *testing.T) {
	t.Parallel()

	cache := NewCache(10 * time.Millisecond)
	defer cache.Stop()

	cache.Set("key1", []byte("body1"), map[string]string{})
	cache.Set("key2", []byte("body2"), map[string]string{})

	time.Sleep(20 * time.Millisecond)

	cache.mu.RLock()
	count := len(cache.entries)
	cache.mu.RUnlock()

	assert.Equal(t, 0, count)
}

func TestCache_Isolation(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	cache.Set("key1", []byte("body1"), map[string]string{"H": "1"})
	cache.Set("key2", []byte("body2"), map[string]string{"H": "2"})

	body1, h1, ok1 := cache.Get("key1")
	body2, h2, ok2 := cache.Get("key2")

	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, []byte("body1"), body1)
	assert.Equal(t, []byte("body2"), body2)
	assert.Equal(t, "1", h1["H"])
	assert.Equal(t, "2", h2["H"])
}

func TestCache_Delete(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	cache.Set("key1", []byte("body"), map[string]string{})
	cache.Delete("key1")

	_, _, ok := cache.Get("key1")
	assert.False(t, ok)
}

func TestCache_HeadersCopy(t *testing.T) {
	t.Parallel()

	cache := NewCache(time.Minute)
	defer cache.Stop()

	original := map[string]string{"K": "V"}
	cache.Set("key1", []byte("body"), original)

	original["K"] = "modified"

	_, gotHeaders, _ := cache.Get("key1")
	assert.Equal(t, "V", gotHeaders["K"])
}
