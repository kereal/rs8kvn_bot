package ratelimiter

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPerUserRateLimiter(t *testing.T) {
	rl := NewPerUserRateLimiter(10, 5)
	require.NotNil(t, rl)
	assert.Equal(t, 0, rl.BucketCount())
}

func TestPerUserRateLimiter_Allow_CreatesBucket(t *testing.T) {
	rl := NewPerUserRateLimiter(5, 0)

	assert.True(t, rl.Allow(1))
	assert.Equal(t, 1, rl.BucketCount())
}

func TestPerUserRateLimiter_Allow_DifferentUsers(t *testing.T) {
	rl := NewPerUserRateLimiter(3, 0)

	// Each user gets their own bucket
	assert.True(t, rl.Allow(1))
	assert.True(t, rl.Allow(2))
	assert.True(t, rl.Allow(3))
	assert.Equal(t, 3, rl.BucketCount())
}

func TestPerUserRateLimiter_Allow_SameUserDepletes(t *testing.T) {
	rl := NewPerUserRateLimiter(2, 0)

	assert.True(t, rl.Allow(1))
	assert.True(t, rl.Allow(1))
	assert.False(t, rl.Allow(1)) // depleted
}

func TestPerUserRateLimiter_Allow_DoesNotCrossUsers(t *testing.T) {
	rl := NewPerUserRateLimiter(1, 0)

	assert.True(t, rl.Allow(1))
	assert.False(t, rl.Allow(1)) // user 1 depleted
	assert.True(t, rl.Allow(2))  // user 2 has own bucket
	assert.False(t, rl.Allow(2)) // user 2 depleted
	assert.False(t, rl.Allow(1)) // user 1 still depleted
}

func TestPerUserRateLimiter_Wait_Success(t *testing.T) {
	rl := NewPerUserRateLimiter(1, 100) // fast refill
	ctx := context.Background()

	assert.True(t, rl.Wait(ctx, 1))
}

func TestPerUserRateLimiter_Wait_ContextCancelled(t *testing.T) {
	rl := NewPerUserRateLimiter(1, 0) // no refill
	ctx, cancel := context.WithCancel(context.Background())

	rl.Allow(1) // consume the token
	cancel()

	assert.False(t, rl.Wait(ctx, 1))
}

func TestPerUserRateLimiter_Wait_ContextTimeout(t *testing.T) {
	rl := NewPerUserRateLimiter(1, 0.001) // very slow refill
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	rl.Allow(1) // consume the token

	start := time.Now()
	result := rl.Wait(ctx, 1)
	elapsed := time.Since(start)

	assert.False(t, result)
	assert.Less(t, elapsed, 100*time.Millisecond)
}

func TestPerUserRateLimiter_Cleanup_RemovesStale(t *testing.T) {
	rl := NewPerUserRateLimiter(5, 100)

	// Create buckets for 3 users
	rl.Allow(1)
	rl.Allow(2)
	rl.Allow(3)
	assert.Equal(t, 3, rl.BucketCount())

	// All buckets are fresh, cleanup should remove none
	removed := rl.Cleanup(1 * time.Hour)
	assert.Equal(t, 0, removed)
	assert.Equal(t, 3, rl.BucketCount())

	// Manually age a bucket by setting lastRefill far in the past
	rl.mu.RLock()
	bucket := rl.buckets[2]
	rl.mu.RUnlock()

	bucket.mu.Lock()
	bucket.lastRefill = time.Now().Add(-2 * time.Hour)
	bucket.mu.Unlock()

	// Cleanup with 1h maxIdle should remove bucket 2
	removed = rl.Cleanup(1 * time.Hour)
	assert.Equal(t, 1, removed)
	assert.Equal(t, 2, rl.BucketCount())
}

func TestPerUserRateLimiter_Cleanup_KeepsRecent(t *testing.T) {
	rl := NewPerUserRateLimiter(5, 0)

	rl.Allow(1)
	rl.Allow(2)

	// Cleanup with very long maxIdle keeps everything
	removed := rl.Cleanup(24 * time.Hour)
	assert.Equal(t, 0, removed)
	assert.Equal(t, 2, rl.BucketCount())
}

func TestPerUserRateLimiter_Cleanup_Empty(t *testing.T) {
	rl := NewPerUserRateLimiter(5, 0)

	removed := rl.Cleanup(1 * time.Hour)
	assert.Equal(t, 0, removed)
}

func TestPerUserRateLimiter_StartCleanup_ContextCancellation(t *testing.T) {
	rl := NewPerUserRateLimiter(5, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		rl.StartCleanup(ctx, 10*time.Millisecond, 1*time.Hour)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Success: goroutine exited
	case <-time.After(1 * time.Second):
		t.Fatal("StartCleanup did not exit after context cancellation")
	}
}

func TestPerUserRateLimiter_ConcurrentAllow(t *testing.T) {
	rl := NewPerUserRateLimiter(100, 0)

	done := make(chan bool)
	for i := int64(0); i < 10; i++ {
		go func(userID int64) {
			for j := 0; j < 10; j++ {
				rl.Allow(userID)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	assert.Equal(t, 10, rl.BucketCount())
}

func TestPerUserRateLimiter_GetOrCreateBucket_Deduplicates(t *testing.T) {
	rl := NewPerUserRateLimiter(10, 0)

	// Multiple calls for same user should return same bucket
	b1 := rl.getOrCreateBucket(1)
	b2 := rl.getOrCreateBucket(1)
	assert.Same(t, b1, b2)
	assert.Equal(t, 1, rl.BucketCount())
}

func TestPerUserRateLimiter_Refill_Isolated(t *testing.T) {
	rl := NewPerUserRateLimiter(2, 100) // 100 tokens/sec

	// Deplete user 1
	rl.Allow(1)
	rl.Allow(1)
	assert.False(t, rl.Allow(1))

	// User 2 untouched
	assert.True(t, rl.Allow(2))
	assert.True(t, rl.Allow(2))
	assert.False(t, rl.Allow(2))

	// Wait for user 1 refill
	time.Sleep(30 * time.Millisecond)
	assert.True(t, rl.Allow(1))
}
