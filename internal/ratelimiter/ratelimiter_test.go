package ratelimiter

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenBucket(t *testing.T) {
	tests := []struct {
		name       string
		maxTokens  float64
		refillRate float64
	}{
		{name: "standard bucket", maxTokens: 30, refillRate: 5},
		{name: "small bucket", maxTokens: 1, refillRate: 1},
		{name: "large bucket", maxTokens: 1000, refillRate: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := NewTokenBucket(tt.maxTokens, tt.refillRate)
			require.NotNil(t, tb, "NewTokenBucket returned nil")
			assert.Equal(t, tt.maxTokens, tb.tokens, "initial tokens")
			assert.Equal(t, tt.maxTokens, tb.maxTokens, "maxTokens")
			assert.Equal(t, tt.refillRate, tb.refillRate, "refillRate")
		})
	}
}

func TestTokenBucket_Allow_WhenTokensAvailable(t *testing.T) {
	tb := NewTokenBucket(10, 1)

	// Should allow when tokens available
	for i := 0; i < 10; i++ {
		assert.True(t, tb.Allow(), "Allow() on iteration %d, expected true", i)
	}

	// Should not allow when no tokens
	assert.False(t, tb.Allow(), "Allow() when no tokens available, expected false")
}

func TestTokenBucket_Allow_ConsumesTokens(t *testing.T) {
	tb := NewTokenBucket(5, 1)

	// Consume all tokens
	for i := 0; i < 5; i++ {
		tb.Allow()
	}

	// Verify no tokens left
	assert.False(t, tb.Allow(), "Allow() should return false when tokens depleted")
}

func TestTokenBucket_Allow_RefillsOverTime(t *testing.T) {
	tb := NewTokenBucket(10, 100) // 100 tokens per second

	// Consume all tokens
	for i := 0; i < 10; i++ {
		tb.Allow()
	}

	// Wait for refill (100 tokens/sec = 1 token per 10ms)
	time.Sleep(50 * time.Millisecond)

	// Should have tokens again
	assert.True(t, tb.Allow(), "Allow() after waiting for refill, expected true")
}

func TestTokenBucket_Wait_WhenTokenAvailable(t *testing.T) {
	tb := NewTokenBucket(10, 1)
	ctx := context.Background()

	start := time.Now()
	result := tb.Wait(ctx)
	elapsed := time.Since(start)

	assert.True(t, result, "Wait() when token available, expected true")
	assert.Less(t, elapsed, 100*time.Millisecond, "Wait() should return immediately when token available")
}

func TestTokenBucket_Wait_WhenNoTokenAvailable(t *testing.T) {
	tb := NewTokenBucket(1, 100) // 100 tokens per second = 1 token per 10ms

	// Consume the only token
	tb.Allow()

	ctx := context.Background()
	start := time.Now()
	result := tb.Wait(ctx)
	elapsed := time.Since(start)

	assert.True(t, result, "Wait() expected true after waiting")
	assert.GreaterOrEqual(t, elapsed, 5*time.Millisecond, "Wait() should have waited for token refill")
}

func TestTokenBucket_Wait_ContextCancellation(t *testing.T) {
	tb := NewTokenBucket(1, 0.001) // Very slow refill rate

	// Consume the only token
	tb.Allow()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := tb.Wait(ctx)
	assert.False(t, result, "Wait() after context cancellation, expected false")
}

func TestTokenBucket_Wait_ContextTimeout(t *testing.T) {
	tb := NewTokenBucket(1, 0.001) // Very slow refill rate

	// Consume the only token
	tb.Allow()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := tb.Wait(ctx)
	elapsed := time.Since(start)

	assert.False(t, result, "Wait() after context timeout, expected false")
	assert.Less(t, elapsed, 100*time.Millisecond, "Wait() should respect context timeout")
}

func TestTokenBucket_Refill_DoesNotExceedMax(t *testing.T) {
	tb := NewTokenBucket(10, 100)

	// Wait for potential refill
	time.Sleep(100 * time.Millisecond)

	tb.mu.Lock()
	tokens := tb.tokens
	tb.mu.Unlock()

	assert.LessOrEqual(t, tokens, tb.maxTokens, "tokens should not exceed maxTokens")
}

func TestTokenBucket_ConcurrentAccess(t *testing.T) {
	tb := NewTokenBucket(100, 100)
	done := make(chan bool)

	// Multiple goroutines trying to get tokens concurrently
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				tb.Allow()
			}
			done <- true
		}()
	}

	// Wait for all goroutines to finish
	for i := 0; i < 10; i++ {
		<-done
	}

	// Bucket should be empty or near empty
	tb.mu.Lock()
	tokens := tb.tokens
	tb.mu.Unlock()

	assert.LessOrEqual(t, tokens, 10.0, "tokens after concurrent access, should be near 0")
}

func TestTokenBucket_ZeroRefillRate(t *testing.T) {
	tb := NewTokenBucket(1, 0)

	// Should allow first request
	assert.True(t, tb.Allow(), "Allow() with 1 token, expected true")

	// Should not allow second request (no refill)
	assert.False(t, tb.Allow(), "Allow() with 0 tokens and 0 refill rate, expected false")

	// Wait should block indefinitely, but we'll use context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := tb.Wait(ctx)
	assert.False(t, result, "Wait() should return false when no refill and context times out")
}

func TestTokenBucket_AvailableTokens(t *testing.T) {
	tb := NewTokenBucket(10, 5)

	// Initial available tokens should be 10
	assert.Equal(t, 10.0, tb.AvailableTokens(), "AvailableTokens() initial")

	// Consume 3 tokens
	require.True(t, tb.AllowN(3), "AllowN(3) failed")

	// Now should have ~7 (allow floating point tolerance)
	got := tb.AvailableTokens()
	assert.InDelta(t, 7.0, got, 0.1, "AvailableTokens() after consuming 3")
}

func TestTokenBucket_AllowN_MoreThanAvailable(t *testing.T) {
	tb := NewTokenBucket(5, 0)

	// Try to consume more than available
	assert.False(t, tb.AllowN(10), "AllowN(10) on bucket with 5 tokens should return false")
}

func TestTokenBucket_WaitN_Success(t *testing.T) {
	tb := NewTokenBucket(10, 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Should succeed immediately
	assert.True(t, tb.WaitN(ctx, 5), "WaitN() should succeed with available tokens")
}

func TestTokenBucket_WaitN_Timeout(t *testing.T) {
	tb := NewTokenBucket(1, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Consume the only token
	tb.Allow()

	// Should timeout
	assert.False(t, tb.WaitN(ctx, 1), "WaitN() should timeout when no tokens available")
}

func TestRateLimiter_NewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(5, 10)
	assert.NotNil(t, rl, "NewRateLimiter() returned nil")
}

func TestRateLimiter_Wait(t *testing.T) {
	rl := NewRateLimiter(3, 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// First wait should succeed
	assert.True(t, rl.Wait(ctx), "Wait() should succeed")
}

func TestRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(2, 1000)

	// First two should succeed
	assert.True(t, rl.Allow(), "First Allow() should succeed")
	assert.True(t, rl.Allow(), "Second Allow() should succeed")

	// Third should fail (no refill)
	assert.False(t, rl.Allow(), "Third Allow() should fail")
}
