package ratelimiter

import (
	"context"
	"sync"
	"time"
)

// TokenBucket implements a thread-safe token bucket rate limiter.
// It allows bursts up to maxTokens and refills at refillRate tokens per second.
type TokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	mu         sync.Mutex
}

// NewTokenBucket creates a new token bucket rate limiter.
// maxTokens is the maximum bucket capacity (burst size).
// refillRate is the rate at which tokens are added (tokens per second).
func NewTokenBucket(maxTokens, refillRate float64) *TokenBucket {
	return &TokenBucket{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// Allow attempts to take one token from the bucket.
// Returns true if a token was available, false otherwise.
func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

// AllowN attempts to take n tokens from the bucket.
// Returns true if enough tokens were available, false otherwise.
func (tb *TokenBucket) AllowN(n float64) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= n {
		tb.tokens -= n
		return true
	}

	return false
}

// Wait blocks until a token is available or the context is cancelled.
// Returns true if a token was acquired, false if the context was cancelled.
func (tb *TokenBucket) Wait(ctx context.Context) bool {
	return tb.WaitN(ctx, 1)
}

// WaitN blocks until n tokens are available or the context is cancelled.
// Returns true if tokens were acquired, false if the context was cancelled.
func (tb *TokenBucket) WaitN(ctx context.Context, n float64) bool {
	for {
		// Check if context is already cancelled before proceeding
		select {
		case <-ctx.Done():
			return false
		default:
		}

		tb.mu.Lock()
		tb.refill()

		if tb.tokens >= n {
			tb.tokens -= n
			tb.mu.Unlock()
			return true
		}

		// Calculate time until we have enough tokens
		tokensNeeded := n - tb.tokens
		waitDuration := time.Duration(tokensNeeded/tb.refillRate) * time.Second

		// Minimum wait of 1ms to avoid busy looping
		if waitDuration < time.Millisecond {
			waitDuration = time.Millisecond
		}

		tb.mu.Unlock()

		select {
		case <-time.After(waitDuration):
			continue
		case <-ctx.Done():
			return false
		}
	}
}

// AvailableTokens returns the current number of available tokens.
func (tb *TokenBucket) AvailableTokens() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	return tb.tokens
}

// Reset resets the token bucket to full capacity and updates lastRefill to now.
func (tb *TokenBucket) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tokens = tb.maxTokens
	tb.lastRefill = time.Now()
}

// refill adds tokens based on elapsed time since last refill.
// Must be called with tb.mu held.
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()

	if elapsed > 0 {
		tb.tokens += elapsed * tb.refillRate

		if tb.tokens > tb.maxTokens {
			tb.tokens = tb.maxTokens
		}

		tb.lastRefill = now
	}
}
