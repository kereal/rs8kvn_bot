package ratelimiter

import (
	"context"
	"testing"
	"time"
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
			if tb == nil {
				t.Fatal("NewTokenBucket returned nil")
			}
			if tb.tokens != tt.maxTokens {
				t.Errorf("initial tokens = %v, want %v", tb.tokens, tt.maxTokens)
			}
			if tb.maxTokens != tt.maxTokens {
				t.Errorf("maxTokens = %v, want %v", tb.maxTokens, tt.maxTokens)
			}
			if tb.refillRate != tt.refillRate {
				t.Errorf("refillRate = %v, want %v", tb.refillRate, tt.refillRate)
			}
		})
	}
}

func TestTokenBucket_Allow_WhenTokensAvailable(t *testing.T) {
	tb := NewTokenBucket(10, 1)

	// Should allow when tokens available
	for i := 0; i < 10; i++ {
		if !tb.Allow() {
			t.Errorf("Allow() = false on iteration %d, expected true", i)
		}
	}

	// Should not allow when no tokens
	if tb.Allow() {
		t.Error("Allow() = true when no tokens available, expected false")
	}
}

func TestTokenBucket_Allow_ConsumesTokens(t *testing.T) {
	tb := NewTokenBucket(5, 1)

	// Consume all tokens
	for i := 0; i < 5; i++ {
		tb.Allow()
	}

	// Verify no tokens left
	if tb.Allow() {
		t.Error("Allow() should return false when tokens depleted")
	}
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
	if !tb.Allow() {
		t.Error("Allow() = false after waiting for refill, expected true")
	}
}

func TestTokenBucket_Wait_WhenTokenAvailable(t *testing.T) {
	tb := NewTokenBucket(10, 1)
	ctx := context.Background()

	start := time.Now()
	result := tb.Wait(ctx)
	elapsed := time.Since(start)

	if !result {
		t.Error("Wait() = false when token available, expected true")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("Wait() took %v, should return immediately when token available", elapsed)
	}
}

func TestTokenBucket_Wait_WhenNoTokenAvailable(t *testing.T) {
	tb := NewTokenBucket(1, 100) // 100 tokens per second = 1 token per 10ms

	// Consume the only token
	tb.Allow()

	ctx := context.Background()
	start := time.Now()
	result := tb.Wait(ctx)
	elapsed := time.Since(start)

	if !result {
		t.Error("Wait() = false, expected true after waiting")
	}
	if elapsed < 5*time.Millisecond {
		t.Errorf("Wait() took %v, should have waited for token refill", elapsed)
	}
}

func TestTokenBucket_Wait_ContextCancellation(t *testing.T) {
	tb := NewTokenBucket(1, 0.001) // Very slow refill rate

	// Consume the only token
	tb.Allow()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := tb.Wait(ctx)
	if result {
		t.Error("Wait() = true after context cancellation, expected false")
	}
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

	if result {
		t.Error("Wait() = true after context timeout, expected false")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("Wait() took %v, should respect context timeout", elapsed)
	}
}

func TestTokenBucket_Refill_DoesNotExceedMax(t *testing.T) {
	tb := NewTokenBucket(10, 100)

	// Wait for potential refill
	time.Sleep(100 * time.Millisecond)

	tb.mu.Lock()
	if tb.tokens > tb.maxTokens {
		t.Errorf("tokens = %v, should not exceed maxTokens = %v", tb.tokens, tb.maxTokens)
	}
	tb.mu.Unlock()
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

	if tokens > 10 {
		t.Errorf("tokens = %v after concurrent access, should be near 0", tokens)
	}
}

func TestTokenBucket_ZeroRefillRate(t *testing.T) {
	tb := NewTokenBucket(1, 0)

	// Should allow first request
	if !tb.Allow() {
		t.Error("Allow() = false with 1 token, expected true")
	}

	// Should not allow second request (no refill)
	if tb.Allow() {
		t.Error("Allow() = true with 0 tokens and 0 refill rate, expected false")
	}

	// Wait should block indefinitely, but we'll use context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := tb.Wait(ctx)
	if result {
		t.Error("Wait() should return false when no refill and context times out")
	}
}

func TestTokenBucket_AvailableTokens(t *testing.T) {
	tb := NewTokenBucket(10, 5)

	// Initial available tokens should be 10
	if got := tb.AvailableTokens(); got != 10 {
		t.Errorf("AvailableTokens() = %v, want 10", got)
	}

	// Consume 3 tokens
	if !tb.AllowN(3) {
		t.Fatal("AllowN(3) failed")
	}

	// Now should have ~7 (allow floating point tolerance)
	got := tb.AvailableTokens()
	if got < 6.9 || got > 7.1 {
		t.Errorf("AvailableTokens() = %v, want ~7", got)
	}
}

func TestTokenBucket_AllowN_MoreThanAvailable(t *testing.T) {
	tb := NewTokenBucket(5, 0)

	// Try to consume more than available
	if tb.AllowN(10) {
		t.Error("AllowN(10) on bucket with 5 tokens should return false")
	}
}

func TestTokenBucket_WaitN_Success(t *testing.T) {
	tb := NewTokenBucket(10, 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Should succeed immediately
	if !tb.WaitN(ctx, 5) {
		t.Error("WaitN() should succeed with available tokens")
	}
}

func TestTokenBucket_WaitN_Timeout(t *testing.T) {
	tb := NewTokenBucket(1, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Consume the only token
	tb.Allow()

	// Should timeout
	if tb.WaitN(ctx, 1) {
		t.Error("WaitN() should timeout when no tokens available")
	}
}

func TestRateLimiter_NewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(5, 10)
	if rl == nil {
		t.Error("NewRateLimiter() returned nil")
	}
}

func TestRateLimiter_Wait(t *testing.T) {
	rl := NewRateLimiter(3, 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// First wait should succeed
	if !rl.Wait(ctx) {
		t.Error("Wait() should succeed")
	}
}

func TestRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(2, 1000)

	// First two should succeed
	if !rl.Allow() {
		t.Error("First Allow() should succeed")
	}
	if !rl.Allow() {
		t.Error("Second Allow() should succeed")
	}

	// Third should fail (no refill)
	if rl.Allow() {
		t.Error("Third Allow() should fail")
	}
}
