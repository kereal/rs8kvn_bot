package bot

import (
	"context"
	"errors"
	"sync"
	"time"

	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// ReferralCache manages in-memory referral counts backed by database persistence.
type ReferralCache struct {
	db     interfaces.DatabaseService
	counts map[int64]int64
	mu     sync.RWMutex
	sendMu sync.Map // chatID -> lastSendTime
}

// NewReferralCache creates a new ReferralCache.
func NewReferralCache(db interfaces.DatabaseService) *ReferralCache {
	return &ReferralCache{
		db:     db,
		counts: make(map[int64]int64),
	}
}

// Load loads referral counts from database into memory.
func (rc *ReferralCache) Load(ctx context.Context) error {
	counts, err := rc.db.GetAllReferralCounts(ctx)
	if err != nil {
		return err
	}

	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.counts = counts
	return nil
}

// Get returns the cached referral count for a user.
func (rc *ReferralCache) Get(chatID int64) int64 {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	return rc.counts[chatID]
}

// Increment increments the referral count for a user in cache.
func (rc *ReferralCache) Increment(chatID int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.counts[chatID]++
}

// Decrement decrements the referral count for a user in cache.
func (rc *ReferralCache) Decrement(chatID int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if rc.counts[chatID] > 0 {
		rc.counts[chatID]--
	}
}

// Sync reloads the referral cache from database.
func (rc *ReferralCache) Sync(ctx context.Context) error {
	return rc.Load(ctx)
}

// StartSync starts periodic synchronization of referral cache.
func (rc *ReferralCache) StartSync(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		// Load initial cache
		if err := rc.Load(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				logger.Info("Referral cache load skipped (context ending)")
			} else {
				logger.Error("Failed to load referral cache", zap.Error(err))
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := rc.Sync(ctx); err != nil {
					logger.Error("Failed to sync referral cache", zap.Error(err))
				}
			}
		}
	}()
}

// CheckAdminSendRateLimit checks if an admin send is rate-limited.
func (rc *ReferralCache) CheckAdminSendRateLimit(chatID int64) bool {
	now := time.Now()

	lastSend, loaded := rc.sendMu.Load(chatID)
	if loaded {
		lastTime := lastSend.(time.Time)
		if now.Sub(lastTime) < 30*time.Second {
			return false
		}
	}

	rc.sendMu.Store(chatID, now)
	return true
}

// ClearAdminSendRateLimit clears the rate limit for a chat.
func (rc *ReferralCache) ClearAdminSendRateLimit(chatID int64) {
	rc.sendMu.Delete(chatID)
}
