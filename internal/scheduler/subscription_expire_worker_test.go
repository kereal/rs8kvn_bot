package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}


type mockVPNClientForExpire struct {
	deleteCalled    bool
	deleteProvision vpn.SubscriptionProvision
}

func (m *mockVPNClientForExpire) CreateSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	return nil
}
func (m *mockVPNClientForExpire) UpdateSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	return nil
}
func (m *mockVPNClientForExpire) DeleteSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	m.deleteCalled = true
	m.deleteProvision = provision
	return nil
}
func (m *mockVPNClientForExpire) Close() error { return nil }

func newTestSubServiceForExpire(t testing.TB, db *database.Service) *service.SubscriptionService {
	t.Helper()
	cfg := &config.Config{
		TrialDurationHours: 1,
	}
	return service.NewSubscriptionService(db, nil, nil, nil, cfg)
}

func TestSubscriptionExpireWorker_process_FindsAndExpires(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	ctx := context.Background()

	plan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	expiredSub := &database.Subscription{
		TelegramID:     99991,
		Username:       "expireuser1",
		ClientID:       "c-expire1",
		SubscriptionID: "s-expire1",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour)),
		PricePaidCents: 100,
		Currency:       testutil.PtrString("RUB"),
		ProductID:      testutil.PtrUint(1),
		StartedAt:      testutil.PtrTime(time.Now().Add(-48 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, expiredSub, ""))

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	worker.process(ctx)

	var updated database.Subscription
	require.NoError(t, db.GetDB().WithContext(ctx).First(&updated, expiredSub.ID).Error)
	assert.Equal(t, "active", updated.Status)
	assert.Equal(t, plan.ID, updated.PlanID)
}

func TestSubscriptionExpireWorker_process_EmptyResult(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	ctx := context.Background()

	plan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	activeSub := &database.Subscription{
		TelegramID:     99992,
		Username:       "noexpire",
		ClientID:       "c-noexpire",
		SubscriptionID: "s-noexpire",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
		PricePaidCents: 100,
		Currency:       testutil.PtrString("RUB"),
		ProductID:      testutil.PtrUint(1),
		StartedAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, activeSub, ""))

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	worker.process(ctx)

	var unchanged database.Subscription
	require.NoError(t, db.GetDB().WithContext(ctx).First(&unchanged, activeSub.ID).Error)
	assert.Equal(t, "active", unchanged.Status)
	assert.False(t, unchanged.ExpiresAt == nil)
}

func TestSubscriptionExpireWorker_Run_ContextCancel(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SubscriptionExpireWorker.Run should stop after context cancel")
	}
}

func TestSubscriptionExpireWorker_process_PaidPlanExpires_DowngradesToFree(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	ctx := context.Background()

	freePlan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	paidPlan := &database.Plan{Name: "paid-plan-expire-e2e", DevicesLimit: 3, TrafficLimit: 1024 * 1024 * 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(paidPlan).Error)

	node := &database.Node{Name: "expire-e2e-node", IsActive: true, Host: "http://expire-e2e", APIToken: "token", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: paidPlan.ID, NodeID: node.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     99994,
		Username:       "expirepaiduser",
		ClientID:       "c-expire-paid",
		SubscriptionID: "s-expire-paid",
		Status:         "active",
		PlanID:         paidPlan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Minute)),
		PricePaidCents: 100,
		Currency:       testutil.PtrString("RUB"),
		ProductID:      testutil.PtrUint(1),
		StartedAt:      testutil.PtrTime(time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Minute)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusActive}))

	var invalidateCalled bool
	var invalidateSubID string
	mockVPN := &mockVPNClientForExpire{}
	vpnClients := map[uint]vpn.Client{node.ID: mockVPN}
	syncSvc := service.NewSyncService(db, vpnClients, []database.Node{*node})
	subService := service.NewSubscriptionService(db, nil, vpnClients, []database.Node{*node}, &config.Config{TrialDurationHours: 1})
	subService.SetSyncService(syncSvc)
	subService.SetInvalidateBySubIDFunc(func(id string) {
		invalidateCalled = true
		invalidateSubID = id
	})

	worker := NewSubscriptionExpireWorker(db, subService)

	worker.process(ctx)

	var updated database.Subscription
	require.NoError(t, db.GetDB().WithContext(ctx).First(&updated, sub.ID).Error)
	assert.Equal(t, freePlan.ID, updated.PlanID, "subscription should be downgraded to free plan")
	assert.Equal(t, "active", updated.Status)

	nodes, nodeErr := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, nodeErr)
	assert.Empty(t, nodes, "subscription nodes should be deleted after sync removes them")

	assert.True(t, mockVPN.deleteCalled, "DeleteSubscription should be called on the VPN client")
	assert.Equal(t, sub.ClientID, mockVPN.deleteProvision.ClientID)
	assert.Equal(t, sub.SubscriptionID, mockVPN.deleteProvision.SubID)

	assert.True(t, invalidateCalled, "cache invalidation should be called")
	assert.Equal(t, sub.SubscriptionID, invalidateSubID)
}
