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

// referralEntry holds a cached referral count for a single referrer.
// Note: referral counts are derived from the subscriptions table
// (SELECT referred_by, COUNT(*) GROUP BY referred_by), so there is
// no separate "dirty" state to persist — the DB is always the source
// of truth. Increment/Decrement update the cache for real-time display
// until the next Load/Sync refreshes from DB.
type referralEntry struct {
	count int64
}

type ReferralCache struct {
	db     interfaces.DatabaseService
	data   map[int64]*referralEntry
	mu     sync.RWMutex
	sendMu sync.Map
}

func NewReferralCache(db interfaces.DatabaseService) *ReferralCache {
	return &ReferralCache{
		db:   db,
		data: make(map[int64]*referralEntry),
	}
}

func (rc *ReferralCache) Load(ctx context.Context) error {
	counts, err := rc.db.GetAllReferralCounts(ctx)
	if err != nil {
		return err
	}

	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.data = make(map[int64]*referralEntry, len(counts))
	for id, count := range counts {
		rc.data[id] = &referralEntry{count: count}
	}
	return nil
}

func (rc *ReferralCache) Get(chatID int64) int64 {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	if entry, ok := rc.data[chatID]; ok {
		return entry.count
	}
	return 0
}

func (rc *ReferralCache) GetAll() map[int64]int64 {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	result := make(map[int64]int64, len(rc.data))
	for id, entry := range rc.data {
		result[id] = entry.count
	}
	return result
}

func (rc *ReferralCache) SetForTest(chatID int64, count int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.data[chatID] = &referralEntry{count: count}
}

func (rc *ReferralCache) Increment(chatID int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if entry, ok := rc.data[chatID]; ok {
		entry.count++
	} else {
		rc.data[chatID] = &referralEntry{count: 1}
	}
}

func (rc *ReferralCache) Decrement(chatID int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if entry, ok := rc.data[chatID]; ok {
		if entry.count > 0 {
			entry.count--
		}
	} else {
		rc.data[chatID] = &referralEntry{count: 0}
	}
}

// Save is a no-op. Referral counts are derived from the subscriptions
// table, so there is nothing to persist — the DB already reflects the
// correct count after each subscription is created or deleted.
// Increment/Decrement update the in-memory cache for real-time display.
func (rc *ReferralCache) Save(_ context.Context) error {
	return nil
}

// Sync refreshes the cache from the database, replacing all in-memory
// counts with the authoritative values computed from subscriptions.
func (rc *ReferralCache) Sync(ctx context.Context) error {
	return rc.Load(ctx)
}

func (rc *ReferralCache) StartSync(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

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

func (rc *ReferralCache) CheckAdminSendRateLimit(chatID int64) bool {
	now := time.Now()

	lastSend, loaded := rc.sendMu.Load(chatID)
	if loaded {
		lastTime, ok := lastSend.(time.Time)
		if ok && now.Sub(lastTime) < 30*time.Second {
			return false
		}
	}

	rc.sendMu.Store(chatID, now)
	return true
}

func (rc *ReferralCache) ClearAdminSendRateLimit(chatID int64) {
	rc.sendMu.Delete(chatID)
}
