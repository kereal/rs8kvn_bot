package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
)

type fakeRepo struct {
	subsFn func(ctx context.Context, from, to time.Time) ([]database.Subscription, error)
}

func (f *fakeRepo) GetSubscriptionsExpiringInRange(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
	return f.subsFn(ctx, from, to)
}

func (f *fakeRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) GetAnyByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) CreateSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) error {
	return nil
}
func (f *fakeRepo) UpdateSubscription(ctx context.Context, sub *database.Subscription) error {
	return nil
}
func (f *fakeRepo) DeleteSubscription(ctx context.Context, telegramID int64) error { return nil }
func (f *fakeRepo) GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) CountAllSubscriptions(ctx context.Context) (int64, error)           { return 0, nil }
func (f *fakeRepo) CountActiveSubscriptions(ctx context.Context) (int64, error)        { return 0, nil }
func (f *fakeRepo) CountTrialSubscriptions(ctx context.Context) (int64, error)         { return 0, nil }
func (f *fakeRepo) CountExpiredSubscriptions(ctx context.Context) (int64, error)       { return 0, nil }
func (f *fakeRepo) CountExpiredAtTime(ctx context.Context, t time.Time) (int64, error) { return 0, nil }
func (f *fakeRepo) GetExpiredSubscriptionsBefore(ctx context.Context, t time.Time) ([]database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) GetExpiredPaidAtTime(ctx context.Context, t time.Time) ([]database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) ExpireSubscription(ctx context.Context, id uint, freePlanID uint) error {
	return nil
}
func (f *fakeRepo) GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error) {
	return 0, nil
}
func (f *fakeRepo) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	return nil, nil
}
func (f *fakeRepo) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	return nil, nil
}
func (f *fakeRepo) GetTotalTelegramIDCount(ctx context.Context) (int64, error) { return 0, nil }
func (f *fakeRepo) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) GetBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	return nil, nil
}
func (f *fakeRepo) GetByNodeID(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error) {
	return nil, nil
}
func (f *fakeRepo) GetPendingSync(ctx context.Context) ([]database.SubscriptionNode, error) {
	return nil, nil
}
func (f *fakeRepo) GetPendingBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	return nil, nil
}
func (f *fakeRepo) GetPendingByNodeID(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error) {
	return nil, nil
}
func (f *fakeRepo) GetNodeByID(ctx context.Context, id uint) (*database.Node, error) { return nil, nil }
func (f *fakeRepo) GetNodesByPlanID(ctx context.Context, planID uint) ([]database.Node, error) {
	return nil, nil
}
func (f *fakeRepo) ListEnabled(ctx context.Context) ([]database.Node, error) { return nil, nil }
func (f *fakeRepo) GetProductByID(ctx context.Context, id uint) (*database.Product, error) {
	return nil, nil
}
func (f *fakeRepo) CreateSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error {
	return nil
}
func (f *fakeRepo) UpdateSubscriptionNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error {
	return nil
}
func (f *fakeRepo) UpsertSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error {
	return nil
}
func (f *fakeRepo) DeleteSubscriptionNode(ctx context.Context, subID, nodeID uint) error { return nil }
func (f *fakeRepo) DeleteSubscriptionNodesBySubscriptionID(ctx context.Context, subID uint) error {
	return nil
}
func (f *fakeRepo) MarkActiveNodesPendingUpdate(ctx context.Context, subID uint, targetNodeIDs []uint) error {
	return nil
}
func (f *fakeRepo) UpdateRetry(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error {
	return nil
}
func (f *fakeRepo) GetPlanByName(ctx context.Context, name string) (*database.Plan, error) {
	return nil, nil
}
func (f *fakeRepo) GetPlanByID(ctx context.Context, planID uint) (*database.Plan, error) {
	return nil, nil
}
func (f *fakeRepo) GetAllPlans(ctx context.Context) ([]database.Plan, error)  { return nil, nil }
func (f *fakeRepo) CreatePlan(ctx context.Context, plan *database.Plan) error { return nil }
func (f *fakeRepo) UpdatePlan(ctx context.Context, plan *database.Plan) error { return nil }
func (f *fakeRepo) DeletePlan(ctx context.Context, planID uint) error         { return nil }
func (f *fakeRepo) GetNodesByPlanName(ctx context.Context, planName string) ([]database.Node, error) {
	return nil, nil
}
func (f *fakeRepo) GetPlansBySourceID(ctx context.Context, sourceID uint) ([]database.Product, error) {
	return nil, nil
}
func (f *fakeRepo) AddSourceToPlan(ctx context.Context, planID, sourceID uint) error      { return nil }
func (f *fakeRepo) RemoveSourceFromPlan(ctx context.Context, planID, sourceID uint) error { return nil }
func (f *fakeRepo) GetSubscription(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	return 0, nil
}
func (f *fakeRepo) CreateTrialRequest(ctx context.Context, ip string) error { return nil }
func (f *fakeRepo) CleanupExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) GetPoolStats() (*database.PoolStats, error) { return nil, nil }
func (f *fakeRepo) GetWithPlanAndNodes(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error) {
	return nil, nil
}
func (f *fakeRepo) GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (f *fakeRepo) UpdateDevices(ctx context.Context, id uint, devicesJSON string) error { return nil }
func (f *fakeRepo) UpdateIPs(ctx context.Context, id uint, ipsJSON string) error         { return nil }
func (f *fakeRepo) UpdateLastRequest(ctx context.Context, subscriptionID string) error   { return nil }
func (f *fakeRepo) GetActiveByPlanID(ctx context.Context, planID uint) ([]database.Product, error) {
	return nil, nil
}
func (f *fakeRepo) CreateOrder(ctx context.Context, order *database.Order) error { return nil }
func (f *fakeRepo) GetOrderByID(ctx context.Context, id uint) (*database.Order, error) {
	return nil, nil
}
func (f *fakeRepo) GetOrdersBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.Order, error) {
	return nil, nil
}
func (f *fakeRepo) UpdateOrderStatus(ctx context.Context, id uint, status database.OrderStatus) error {
	return nil
}
func (f *fakeRepo) UpdateOrderPaidStatus(ctx context.Context, id uint) error { return nil }
func (f *fakeRepo) UpdateOrderActivatedAt(ctx context.Context, id uint, activatedAt, expiresAt time.Time) error {
	return nil
}
func (f *fakeRepo) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	return nil, nil
}
func (f *fakeRepo) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	return nil, nil
}
func (f *fakeRepo) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error) {
	return nil, nil
}
func (f *fakeRepo) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) ListNodes(ctx context.Context) ([]database.Node, error) { return nil, nil }
func (f *fakeRepo) SeedDefaultData(ctx context.Context) error              { return nil }
func (f *fakeRepo) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	return 0, nil
}
func (f *fakeRepo) GetAllTelegramIDs(ctx context.Context) ([]int64, error) { return nil, nil }
func (f *fakeRepo) GetExpiredPaidSubscriptions(ctx context.Context, now time.Time) ([]database.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) Ping(ctx context.Context) error { return nil }
func (f *fakeRepo) Close() error                   { return nil }
func (f *fakeRepo) ClaimReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error) {
	return true, nil
}
func (f *fakeRepo) ReleaseReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) error {
	return nil
}
func (f *fakeRepo) DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return nil, nil
}

type fakeSubSvc struct {
	bot    interfaces.BotAPI
	sendFn func(ctx context.Context, sub *database.Subscription, bit int, daysLeft int, hoursLeft int) error
}

func (f *fakeSubSvc) SendExpiryReminder(ctx context.Context, sub *database.Subscription, window interfaces.SubscriptionReminderWindow) error {
	daysLeft, hoursLeft := service.ReminderWindowRemaining(time.Now().UTC(), *sub.ExpiresAt)
	return f.sendFn(ctx, sub, window.Bit, daysLeft, hoursLeft)
}

func TestSubscriptionReminderWorker_process_SendsReminderForMatchingWindow(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sub := database.Subscription{
		ID:         1,
		TelegramID: 101,
		Status:     "active",
		ExpiresAt:  testutil.PtrTime(now.Add(72*time.Hour + 10*time.Minute)),
	}

	repo := &fakeRepo{
		subsFn: func(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
			if from.Sub(now) >= 71*time.Hour && from.Sub(now) <= 73*time.Hour {
				return []database.Subscription{sub}, nil
			}
			return nil, nil
		},
	}

	var sent []struct {
		sub       *database.Subscription
		bit       int
		daysLeft  int
		hoursLeft int
	}
	svc := &fakeSubSvc{
		bot: testutil.NewBotAPI(),
		sendFn: func(ctx context.Context, s *database.Subscription, bit int, daysLeft int, hoursLeft int) error {
			sent = append(sent, struct {
				sub       *database.Subscription
				bit       int
				daysLeft  int
				hoursLeft int
			}{s, bit, daysLeft, hoursLeft})
			return nil
		},
	}

	ctx := context.Background()
	worker := NewSubscriptionReminderWorker(repo, svc)
	worker.process(ctx)

	assert.Equal(t, 0, sent[0].hoursLeft, "72h10m -> whole hours % 24 == 0")
	assert.Equal(t, service.ReminderBit3Days, sent[0].bit)
	assert.Equal(t, 3, sent[0].daysLeft)
	assert.Equal(t, 0, sent[0].hoursLeft)
}

func TestSubscriptionReminderWorker_process_SkipsSendError(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	repo := &fakeRepo{
		subsFn: func(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
			if from.Sub(now) >= 23*time.Hour && from.Sub(now) <= 25*time.Hour {
				return []database.Subscription{{
					ID:         2,
					TelegramID: 202,
					Status:     "active",
					ExpiresAt:  testutil.PtrTime(now.Add(24*time.Hour - 5*time.Minute)),
				}}, nil
			}
			return nil, nil
		},
	}

	callCount := 0
	svc := &fakeSubSvc{
		bot: testutil.NewBotAPI(),
		sendFn: func(ctx context.Context, sub *database.Subscription, bit int, daysLeft int, hoursLeft int) error {
			callCount++
			return errors.New("telegram down")
		},
	}

	ctx := context.Background()
	worker := NewSubscriptionReminderWorker(repo, svc)
	worker.process(ctx)

	assert.Equal(t, 1, callCount, "worker should attempt once per window despite send error")
}
