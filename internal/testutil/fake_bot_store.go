package testutil

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"gorm.io/gorm"
)

// BotStoreFake is the in-memory fake for bot-package tests. Bot tests wire the handler
// together with SubscriptionService/OrderService over a single instance, so it embeds
// ServiceStoreFake (interfaces.SubscriptionStore + SyncStore + OrderStore) and adds the
// interfaces.BotStore surface (broadcast management, menu lookups, referral reads,
// audit writes).

type BotStoreFake struct {
	*ServiceStoreFake

	Products                             map[uint]*database.Product
	CancelBroadcastFunc                  func(ctx context.Context, id uint, now time.Time) (bool, error)
	ClaimBroadcastFunc                   func(ctx context.Context, id uint, now time.Time) (bool, error)
	ClaimBroadcastRecipientsFunc         func(ctx context.Context, broadcastID uint, now time.Time, limit int) ([]database.BroadcastRecipient, error)
	CountAllSubscriptionsFunc            func(ctx context.Context) (int64, error)
	CreateBroadcastFunc                  func(ctx context.Context, b *database.Broadcast) error
	FinishBroadcastRecipientFunc         func(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, status database.BroadcastRecipientStatus, lastError string, now time.Time) error
	GetAllReferralCountsFunc             func(ctx context.Context) (map[int64]int64, error)
	GetBroadcastFunc                     func(ctx context.Context, id uint) (*database.Broadcast, error)
	GetFilteredTelegramIDCountFunc       func(ctx context.Context, filter database.BroadcastFilter) (int64, error)
	GetInviteByReferrerFunc              func(ctx context.Context, referrerTGID int64) (*database.Invite, error)
	GetLatestSubscriptionsFunc           func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetOrCreateInviteFunc                func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetRunnableBroadcastsFunc            func(ctx context.Context, now time.Time) ([]database.Broadcast, error)
	GetTelegramIDByUsernameFunc          func(ctx context.Context, username string) (int64, error)
	GetTelegramIDsBatchFunc              func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc          func(ctx context.Context) (int64, error)
	ListActiveProductsFunc               func(ctx context.Context) ([]database.Product, error)
	ListBroadcastsFunc                   func(ctx context.Context, limit int) ([]database.Broadcast, error)
	RecoverStaleBroadcastRecipientsFunc  func(ctx context.Context, broadcastID uint, before time.Time) error
	ResetBroadcastFailedRecipientsFunc   func(ctx context.Context, id uint, now time.Time) error
	SnapshotBroadcastRecipientsFunc      func(ctx context.Context, broadcastID uint, filter database.BroadcastFilter) (int64, error)
	UpdateBroadcastFunc                  func(ctx context.Context, b *database.Broadcast) error
	UpdateBroadcastRecipientProgressFunc func(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, nextChunk int, now time.Time) error
}

// NewBotStoreFake returns an empty BotStoreFake; the embedded ServiceStoreFake
// provides the shared subscription storage.
func NewBotStoreFake() *BotStoreFake {
	return &BotStoreFake{
		ServiceStoreFake: NewServiceStoreFake(),
		Products:         make(map[uint]*database.Product),
	}
}

// errBotStoreFakeFuncNotSet is the explicit error a nil Func-field default
// produces, so tests fail loudly instead of silently passing on missing wiring.
func errBotStoreFakeFuncNotSet(name string) error {
	return fmt.Errorf("fake: %s is not set", name)
}

var (
	_ interfaces.BotStore          = (*BotStoreFake)(nil)
	_ interfaces.SubscriptionStore = (*BotStoreFake)(nil)
	_ interfaces.OrderStore        = (*BotStoreFake)(nil)
)

func (m *BotStoreFake) CancelBroadcast(ctx context.Context, id uint, now time.Time) (bool, error) {
	if m.CancelBroadcastFunc != nil {
		return m.CancelBroadcastFunc(ctx, id, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[id]
	if !ok {
		return false, database.ErrBroadcastNotFound
	}
	if b.Status != string(database.BroadcastStatusScheduled) && b.Status != string(database.BroadcastStatusRunning) {
		return false, nil
	}
	// Mirrors production: sending leases are left in place so in-flight
	// deliveries can still record their terminal outcome.
	b.Status = string(database.BroadcastStatusCanceled)
	b.FinishedAt = &now
	return true, nil
}

func (m *BotStoreFake) ClaimBroadcast(ctx context.Context, id uint, now time.Time) (bool, error) {
	if m.ClaimBroadcastFunc != nil {
		return m.ClaimBroadcastFunc(ctx, id, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[id]
	if !ok {
		return false, database.ErrBroadcastNotFound
	}
	if b.Status != string(database.BroadcastStatusScheduled) {
		return false, nil
	}
	b.Status = string(database.BroadcastStatusRunning)
	b.StartedAt = &now
	return true, nil
}

func (m *BotStoreFake) ClaimBroadcastRecipients(ctx context.Context, broadcastID uint, now time.Time, limit int) ([]database.BroadcastRecipient, error) {
	if m.ClaimBroadcastRecipientsFunc != nil {
		return m.ClaimBroadcastRecipientsFunc(ctx, broadcastID, now, limit)
	}
	// The recipient lease state machine mirrors production CAS logic; tests that
	// need it run against the real database (testutil.NewTestDatabaseService).
	return nil, errBotStoreFakeFuncNotSet("ClaimBroadcastRecipientsFunc")
}

func (m *BotStoreFake) CountAllSubscriptions(ctx context.Context) (int64, error) {
	if m.CountAllSubscriptionsFunc != nil {
		return m.CountAllSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.Subscriptions)), nil
}

func (m *BotStoreFake) CreateBroadcast(ctx context.Context, b *database.Broadcast) error {
	if m.CreateBroadcastFunc != nil {
		return m.CreateBroadcastFunc(ctx, b)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Broadcasts == nil {
		m.Broadcasts = make(map[uint]*database.Broadcast)
	}

	if b.ID == 0 {
		// #nosec G115 -- map length is non-negative
		b.ID = uint(len(m.Broadcasts) + 1)
	}

	stored := *b
	m.Broadcasts[b.ID] = &stored

	return nil
}

func (m *BotStoreFake) FinishBroadcastRecipient(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, status database.BroadcastRecipientStatus, lastError string, now time.Time) error {
	if m.FinishBroadcastRecipientFunc != nil {
		return m.FinishBroadcastRecipientFunc(ctx, broadcastID, id, expectedAttempts, status, lastError, now)
	}
	// Lease CAS mirrors production logic; tests that need it run on real SQLite.
	return errBotStoreFakeFuncNotSet("FinishBroadcastRecipientFunc")
}

func (m *BotStoreFake) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	if m.GetAllReferralCountsFunc != nil {
		return m.GetAllReferralCountsFunc(ctx)
	}

	return make(map[int64]int64), nil
}

func (m *BotStoreFake) GetBroadcast(ctx context.Context, id uint) (*database.Broadcast, error) {
	if m.GetBroadcastFunc != nil {
		return m.GetBroadcastFunc(ctx, id)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if b, ok := m.Broadcasts[id]; ok {
		copy := *b
		return &copy, nil
	}

	return nil, database.ErrBroadcastNotFound
}

func (m *BotStoreFake) GetFilteredTelegramIDCount(ctx context.Context, filter database.BroadcastFilter) (int64, error) {
	if m.GetFilteredTelegramIDCountFunc != nil {
		return m.GetFilteredTelegramIDCountFunc(ctx, filter)
	}

	return m.GetTotalTelegramIDCount(ctx)
}

func (m *BotStoreFake) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error) {
	if m.GetInviteByReferrerFunc != nil {
		return m.GetInviteByReferrerFunc(ctx, referrerTGID)
	}

	return nil, database.ErrInviteNotFound
}

func (m *BotStoreFake) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	if m.GetLatestSubscriptionsFunc != nil {
		return m.GetLatestSubscriptionsFunc(ctx, limit)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []database.Subscription

	for _, sub := range m.Subscriptions {
		if sub.Status == string(database.SubscriptionStatusActive) {
			result = append(result, *sub)
		}
	}

	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

func (m *BotStoreFake) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	if m.GetOrCreateInviteFunc != nil {
		return m.GetOrCreateInviteFunc(ctx, referrerTGID, code)
	}

	return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
}

func (m *BotStoreFake) GetRunnableBroadcasts(ctx context.Context, now time.Time) ([]database.Broadcast, error) {
	if m.GetRunnableBroadcastsFunc != nil {
		return m.GetRunnableBroadcastsFunc(ctx, now)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]database.Broadcast, 0)
	for _, b := range m.Broadcasts {
		if (b.Status == string(database.BroadcastStatusScheduled) || b.Status == string(database.BroadcastStatusRunning)) &&
			(b.RetryAt == nil || !b.RetryAt.After(now)) &&
			(b.PlannedAt == nil || !b.PlannedAt.After(now)) {
			out = append(out, *b)
		}
	}
	return out, nil
}

func (m *BotStoreFake) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	if m.GetTelegramIDByUsernameFunc != nil {
		return m.GetTelegramIDByUsernameFunc(ctx, username)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sub := range m.Subscriptions {
		if sub.Username == username {
			return sub.TelegramID, nil
		}
	}

	return 0, gorm.ErrRecordNotFound
}

func (m *BotStoreFake) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	if m.GetTelegramIDsBatchFunc != nil {
		return m.GetTelegramIDsBatchFunc(ctx, offset, limit)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var ids []int64
	for id := range m.Subscriptions {
		ids = append(ids, id)
	}

	if offset >= len(ids) {
		return []int64{}, nil
	}

	end := min(offset+limit, len(ids))

	return ids[offset:end], nil
}

func (m *BotStoreFake) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	if m.GetTotalTelegramIDCountFunc != nil {
		return m.GetTotalTelegramIDCountFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.Subscriptions)), nil
}

func (m *BotStoreFake) ListActiveProducts(ctx context.Context) ([]database.Product, error) {
	if m.ListActiveProductsFunc != nil {
		return m.ListActiveProductsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	products := make([]database.Product, 0, len(m.Products))
	for _, product := range m.Products {
		if product.IsActive && product.PriceCents > 0 {
			products = append(products, *product)
		}
	}

	return products, nil
}

func (m *BotStoreFake) ListBroadcasts(ctx context.Context, limit int) ([]database.Broadcast, error) {
	if m.ListBroadcastsFunc != nil {
		return m.ListBroadcastsFunc(ctx, limit)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]uint, 0, len(m.Broadcasts))
	for id := range m.Broadcasts {
		ids = append(ids, id)
	}
	// Same order as production: newest first.
	slices.SortFunc(ids, func(a, b uint) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		default:
			return 0
		}
	})

	// Negative limit means "no limit", matching the production GORM query
	// (LIMIT is omitted when limit < 0, returning all broadcasts).
	capacity := len(ids)
	if limit >= 0 && limit < capacity {
		capacity = limit
	}
	out := make([]database.Broadcast, 0, capacity)
	for _, id := range ids {
		if limit >= 0 && len(out) >= limit {
			break
		}
		out = append(out, *m.Broadcasts[id])
	}

	return out, nil
}

func (m *BotStoreFake) RecoverStaleBroadcastRecipients(ctx context.Context, broadcastID uint, before time.Time) error {
	if m.RecoverStaleBroadcastRecipientsFunc != nil {
		return m.RecoverStaleBroadcastRecipientsFunc(ctx, broadcastID, before)
	}
	// Stale-lease recovery mirrors production state logic; tests that need it
	// run on real SQLite.
	return errBotStoreFakeFuncNotSet("RecoverStaleBroadcastRecipientsFunc")
}

func (m *BotStoreFake) ResetBroadcastFailedRecipients(ctx context.Context, id uint, now time.Time) error {
	if m.ResetBroadcastFailedRecipientsFunc != nil {
		return m.ResetBroadcastFailedRecipientsFunc(ctx, id, now)
	}
	// Retry reset rewrites recipient lease state; tests that need it run on
	// real SQLite.
	return errBotStoreFakeFuncNotSet("ResetBroadcastFailedRecipientsFunc")
}

func (m *BotStoreFake) SnapshotBroadcastRecipients(ctx context.Context, broadcastID uint, filter database.BroadcastFilter) (int64, error) {
	if m.SnapshotBroadcastRecipientsFunc != nil {
		return m.SnapshotBroadcastRecipientsFunc(ctx, broadcastID, filter)
	}
	// Audience snapshot materialization mirrors production logic; tests that
	// need it run on real SQLite or set SnapshotBroadcastRecipientsFunc.
	return 0, errBotStoreFakeFuncNotSet("SnapshotBroadcastRecipientsFunc")
}

func (m *BotStoreFake) UpdateBroadcast(ctx context.Context, b *database.Broadcast) error {
	if m.UpdateBroadcastFunc != nil {
		return m.UpdateBroadcastFunc(ctx, b)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.Broadcasts[b.ID]
	if !ok {
		return database.ErrBroadcastNotFound
	}

	// Merge semantics mirror production: only mutable lifecycle/stats fields
	// are updated; name/filters/message_text/planned_at/started_at are never
	// overwritten — started_at is owned exclusively by ClaimBroadcast.
	existing.Status = b.Status
	existing.FinishedAt = b.FinishedAt
	existing.RecipientsTotal = b.RecipientsTotal
	existing.SentCount = b.SentCount
	existing.BlockedCount = b.BlockedCount
	existing.UnreachableCount = b.UnreachableCount
	existing.FailedCount = b.FailedCount
	existing.LastError = b.LastError
	existing.RetryAt = b.RetryAt
	existing.RetryCount = b.RetryCount
	existing.DeliveryReport = b.DeliveryReport
	if b.RecipientsState != "" {
		existing.RecipientsState = b.RecipientsState
	}
	existing.UpdatedAt = b.UpdatedAt

	return nil
}

func (m *BotStoreFake) UpdateBroadcastRecipientProgress(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, nextChunk int, now time.Time) error {
	if m.UpdateBroadcastRecipientProgressFunc != nil {
		return m.UpdateBroadcastRecipientProgressFunc(ctx, broadcastID, id, expectedAttempts, nextChunk, now)
	}
	// Lease CAS mirrors production logic; tests that need it run on real SQLite.
	return errBotStoreFakeFuncNotSet("UpdateBroadcastRecipientProgressFunc")
}
