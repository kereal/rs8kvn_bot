package ratelimiter

import (
	"context"
	"sync"
	"time"
)

// PerUserRateLimiter manages per-user token buckets to prevent
// a single user from starving others of rate limit tokens.
type PerUserRateLimiter struct {
	mu         sync.RWMutex
	buckets    map[int64]*TokenBucket
	maxTokens  float64
	refillRate float64
}

// NewPerUserRateLimiter creates a per-user rate limiter.
// Each user gets their own token bucket with the specified capacity and refill rate.
func NewPerUserRateLimiter(maxTokens, refillRate float64) *PerUserRateLimiter {
	return &PerUserRateLimiter{
		buckets:    make(map[int64]*TokenBucket),
		maxTokens:  maxTokens,
		refillRate: refillRate,
	}
}

// getOrCreateBucket returns the bucket for a user, creating one if needed.
func (p *PerUserRateLimiter) getOrCreateBucket(userID int64) *TokenBucket {
	// Fast path: read lock check
	p.mu.RLock()
	bucket, exists := p.buckets[userID]
	p.mu.RUnlock()
	if exists {
		return bucket
	}

	// Slow path: create with write lock
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if bucket, exists = p.buckets[userID]; exists {
		return bucket
	}

	bucket = NewTokenBucket(p.maxTokens, p.refillRate)
	p.buckets[userID] = bucket
	return bucket
}

// Allow checks if a user can send a message without blocking.
func (p *PerUserRateLimiter) Allow(userID int64) bool {
	bucket := p.getOrCreateBucket(userID)
	return bucket.Allow()
}

// Wait blocks until a token is available for the user or context is cancelled.
func (p *PerUserRateLimiter) Wait(ctx context.Context, userID int64) bool {
	bucket := p.getOrCreateBucket(userID)
	return bucket.Wait(ctx)
}

// Cleanup removes buckets that have been idle (no tokens consumed) for longer than maxIdle.
// It returns the number of buckets removed.
func (p *PerUserRateLimiter) Cleanup(maxIdle time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var toDelete []int64

	for userID, bucket := range p.buckets {
		bucket.mu.Lock()
		idleTime := now.Sub(bucket.lastRefill)
		bucket.mu.Unlock()

		if idleTime > maxIdle {
			toDelete = append(toDelete, userID)
		}
	}

	for _, userID := range toDelete {
		delete(p.buckets, userID)
	}

	return len(toDelete)
}

// StartCleanup runs a cleanup loop that removes stale buckets at the given interval.
// It blocks until the context is cancelled.
func (p *PerUserRateLimiter) StartCleanup(ctx context.Context, interval, maxIdle time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Cleanup(maxIdle)
		}
	}
}

// BucketCount returns the current number of user buckets.
func (p *PerUserRateLimiter) BucketCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.buckets)
}
