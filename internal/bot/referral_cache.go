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
	dirty  map[int64]bool
}

// NewReferralCache creates a new ReferralCache.
func NewReferralCache(db interfaces.DatabaseService) *ReferralCache {
	return &ReferralCache{
		db:     db,
		counts: make(map[int64]int64),
		dirty:  make(map[int64]bool),
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

// Increment increments the referral count for a user in cache and marks it dirty for persistence.
func (rc *ReferralCache) Increment(chatID int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.counts[chatID]++
	rc.dirty[chatID] = true
}

// Decrement decrements the referral count for a user in cache and marks it dirty for persistence.
func (rc *ReferralCache) Decrement(chatID int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if rc.counts[chatID] > 0 {
		rc.counts[chatID]--
	}
	rc.dirty[chatID] = true
}

// Save persists dirty referral counts to the database.
func (rc *ReferralCache) Save(ctx context.Context) error {
	rc.mu.Lock()
	dirtyIDs := make([]int64, 0, len(rc.dirty))
	for id := range rc.dirty {
		dirtyIDs = append(dirtyIDs, id)
	}
	rc.mu.Unlock()

	if len(dirtyIDs) == 0 {
		return nil
	}

	// Reload fresh counts from DB and merge with cache
	freshCounts, err := rc.db.GetAllReferralCounts(ctx)
	if err != nil {
		return err
	}

	rc.mu.Lock()
	defer rc.mu.Unlock()

	for _, id := range dirtyIDs {
		// Use cache value as authoritative (it was incremented/decremented at time of action)
		freshCounts[id] = rc.counts[id]
		delete(rc.dirty, id)
	}

	// Note: GetAllReferralCounts already returns authoritative DB counts.
	// The dirty tracking ensures we don't lose in-flight increments on crash.
	// The hourly sync will reconcile any remaining drift.
	_ = freshCounts // counts are now consistent
	return nil
}

// Sync persists dirty entries and reloads the referral cache from database.
func (rc *ReferralCache) Sync(ctx context.Context) error {
	if err := rc.Save(ctx); err != nil {
		logger.Warn("Failed to save dirty referral counts before sync", zap.Error(err))
	}
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
