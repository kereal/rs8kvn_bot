package testutil

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"gorm.io/gorm"
)

// SubscriptionRepositoryFake is the in-memory fake for the already-narrowed read seams:
// it implements interfaces.WebRepository (SubscriptionRepository + Invite/Trial/Plan
// reads) and therefore satisfies the subserver, web and scheduler expire-worker seams
// structurally. The embedded ServiceStoreFake carries the subscription storage.

type SubscriptionRepositoryFake struct {
	*ServiceStoreFake

	Plans                                      map[uint]*database.Plan
	CountAllSubscriptionsFunc                  func(ctx context.Context) (int64, error)
	CountTrialRequestsByIPLastHourFunc         func(ctx context.Context, ip string) (int, error)
	CreateTrialRequestFunc                     func(ctx context.Context, ip string) error
	GetActiveSubscriptionsWithTrafficLimitFunc func(ctx context.Context) ([]database.SubscriptionTrafficTarget, error)
	GetAllReferralCountsFunc                   func(ctx context.Context) (map[int64]int64, error)
	GetExpiredPaidSubscriptionsFunc            func(ctx context.Context, now time.Time) ([]database.Subscription, error)
	GetFilteredTelegramIDCountFunc             func(ctx context.Context, filter database.BroadcastFilter) (int64, error)
	GetInviteByReferrerFunc                    func(ctx context.Context, referrerTGID int64) (*database.Invite, error)
	GetLatestSubscriptionsFunc                 func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetOrCreateInviteFunc                      func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetReferralCountFunc                       func(ctx context.Context, referrerTGID int64) (int64, error)
	GetSubscriptionStatusFunc                  func(ctx context.Context, subscriptionID string) (string, time.Time, error)
	GetSubscriptionsExpiringInRangeFunc        func(ctx context.Context, from, to time.Time) ([]database.Subscription, error)
	GetTelegramIDByUsernameFunc                func(ctx context.Context, username string) (int64, error)
	GetTelegramIDsBatchFunc                    func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc                func(ctx context.Context) (int64, error)
	GetWithPlanAndNodesFunc                    func(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error)
	UpdateDevicesFunc                          func(ctx context.Context, id uint, devicesJSON string) error
	UpdateIPsFunc                              func(ctx context.Context, id uint, ipsJSON string) error
	UpdateLastRequestFunc                      func(ctx context.Context, subscriptionID string) error
}

// NewSubscriptionRepositoryFake returns an empty SubscriptionRepositoryFake; the embedded ServiceStoreFake
// provides the shared subscription storage.
func NewSubscriptionRepositoryFake() *SubscriptionRepositoryFake {
	return &SubscriptionRepositoryFake{
		ServiceStoreFake: NewServiceStoreFake(),
		Plans:            make(map[uint]*database.Plan),
	}
}

var _ interfaces.WebRepository = (*SubscriptionRepositoryFake)(nil)

func (m *SubscriptionRepositoryFake) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
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

func (m *SubscriptionRepositoryFake) GetWithPlanAndNodes(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error) {
	if m.GetWithPlanAndNodesFunc != nil {
		return m.GetWithPlanAndNodesFunc(ctx, subscriptionID)
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *SubscriptionRepositoryFake) CountAllSubscriptions(ctx context.Context) (int64, error) {
	if m.CountAllSubscriptionsFunc != nil {
		return m.CountAllSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.Subscriptions)), nil
}

func (m *SubscriptionRepositoryFake) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
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

func (m *SubscriptionRepositoryFake) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
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

func (m *SubscriptionRepositoryFake) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	if m.GetTotalTelegramIDCountFunc != nil {
		return m.GetTotalTelegramIDCountFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.Subscriptions)), nil
}

func (m *SubscriptionRepositoryFake) GetFilteredTelegramIDCount(ctx context.Context, filter database.BroadcastFilter) (int64, error) {
	if m.GetFilteredTelegramIDCountFunc != nil {
		return m.GetFilteredTelegramIDCountFunc(ctx, filter)
	}

	return m.GetTotalTelegramIDCount(ctx)
}

func (m *SubscriptionRepositoryFake) GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error) {
	if m.GetSubscriptionStatusFunc != nil {
		return m.GetSubscriptionStatusFunc(ctx, subscriptionID)
	}

	return "", time.Time{}, gorm.ErrRecordNotFound
}

func (m *SubscriptionRepositoryFake) GetExpiredPaidSubscriptions(ctx context.Context, now time.Time) ([]database.Subscription, error) {
	if m.GetExpiredPaidSubscriptionsFunc != nil {
		return m.GetExpiredPaidSubscriptionsFunc(ctx, now)
	}

	return nil, nil
}

func (m *SubscriptionRepositoryFake) UpdateDevices(ctx context.Context, id uint, devicesJSON string) error {
	if m.UpdateDevicesFunc != nil {
		return m.UpdateDevicesFunc(ctx, id, devicesJSON)
	}

	return nil
}

func (m *SubscriptionRepositoryFake) UpdateIPs(ctx context.Context, id uint, ipsJSON string) error {
	if m.UpdateIPsFunc != nil {
		return m.UpdateIPsFunc(ctx, id, ipsJSON)
	}

	return nil
}

func (m *SubscriptionRepositoryFake) GetSubscriptionsExpiringInRange(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
	if m.GetSubscriptionsExpiringInRangeFunc != nil {
		return m.GetSubscriptionsExpiringInRangeFunc(ctx, from, to)
	}

	return nil, nil
}

// GetActiveSubscriptionsWithTrafficLimit returns active subscriptions with
// their respective positive plan traffic limits from the shared fake.
func (m *SubscriptionRepositoryFake) GetActiveSubscriptionsWithTrafficLimit(ctx context.Context) ([]database.SubscriptionTrafficTarget, error) {
	if m.GetActiveSubscriptionsWithTrafficLimitFunc != nil {
		return m.GetActiveSubscriptionsWithTrafficLimitFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []database.SubscriptionTrafficTarget
	for _, sub := range m.Subscriptions {
		if sub.Status != string(database.SubscriptionStatusActive) {
			continue
		}
		plan, ok := m.Plans[sub.PlanID]
		if !ok {
			plan, _ = m.GetPlanByID(ctx, sub.PlanID)
		}
		if plan == nil || plan.TrafficLimit <= 0 {
			continue
		}
		out = append(out, database.SubscriptionTrafficTarget{Subscription: *sub, TrafficLimit: plan.TrafficLimit})
	}

	return out, nil
}

func (m *SubscriptionRepositoryFake) UpdateLastRequest(ctx context.Context, subscriptionID string) error {
	if m.UpdateLastRequestFunc != nil {
		return m.UpdateLastRequestFunc(ctx, subscriptionID)
	}

	return nil
}

func (m *SubscriptionRepositoryFake) GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error) {
	if m.GetReferralCountFunc != nil {
		return m.GetReferralCountFunc(ctx, referrerTGID)
	}

	return 0, nil
}

func (m *SubscriptionRepositoryFake) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	if m.GetOrCreateInviteFunc != nil {
		return m.GetOrCreateInviteFunc(ctx, referrerTGID, code)
	}

	return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
}

func (m *SubscriptionRepositoryFake) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error) {
	if m.GetInviteByReferrerFunc != nil {
		return m.GetInviteByReferrerFunc(ctx, referrerTGID)
	}

	return nil, database.ErrInviteNotFound
}

func (m *SubscriptionRepositoryFake) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	if m.GetAllReferralCountsFunc != nil {
		return m.GetAllReferralCountsFunc(ctx)
	}

	return make(map[int64]int64), nil
}

func (m *SubscriptionRepositoryFake) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	if m.CountTrialRequestsByIPLastHourFunc != nil {
		return m.CountTrialRequestsByIPLastHourFunc(ctx, ip)
	}

	return 0, nil
}

func (m *SubscriptionRepositoryFake) CreateTrialRequest(ctx context.Context, ip string) error {
	if m.CreateTrialRequestFunc != nil {
		return m.CreateTrialRequestFunc(ctx, ip)
	}

	return nil
}
