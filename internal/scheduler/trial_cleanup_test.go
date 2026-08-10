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

func newTestSubService(t testing.TB, db *database.Service) *service.SubscriptionService {
	t.Helper()
	cfg := &config.Config{
		TrialDurationHours: 1,
	}
	return service.NewSubscriptionService(db, nil, nil, nil, cfg)
}

func TestTrialCleanupScheduler_New(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	assert.NotNil(t, scheduler)
}

func TestTrialCleanupScheduler_RunCleanup_NoExpiredTrials(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	ctx := context.Background()
	scheduler.runCleanup(ctx)
}

func TestTrialCleanupScheduler_RunCleanup_WithExpiredTrials(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	expiredSub := &database.Subscription{
		TelegramID:     -1001,
		ClientID:       "expired-client-1",
		SubscriptionID: "expired-sub-1",
		PlanID:         1,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour)),
		Status:         "active",
		CreatedAt:      time.Now().Add(-2 * time.Hour),
	}
	err = db.CreateSubscription(ctx, expiredSub, "")
	require.NoError(t, err)

	expiredSub2 := &database.Subscription{
		TelegramID:     -1002,
		ClientID:       "expired-client-2",
		SubscriptionID: "expired-sub-2",
		PlanID:         1,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour)),
		Status:         "active",
		CreatedAt:      time.Now().Add(-3 * time.Hour),
	}
	err = db.CreateSubscription(ctx, expiredSub2, "")
	require.NoError(t, err)

	activeSub := &database.Subscription{
		TelegramID:     -1003,
		ClientID:       "active-client",
		SubscriptionID: "active-sub",
		PlanID:         1,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(1 * time.Hour)),
		Status:         "active",
		CreatedAt:      time.Now().Add(-30 * time.Minute),
	}
	err = db.CreateSubscription(ctx, activeSub, "")
	require.NoError(t, err)

	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	scheduler.runCleanup(ctx)

	remaining, err := db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Len(t, remaining, 1, "Only the active trial should remain")
	if len(remaining) > 0 {
		assert.Equal(t, "active-sub", remaining[0].SubscriptionID)
	}
}

func TestTrialCleanupScheduler_RunCleanup_XUIFailure(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	expiredSub := &database.Subscription{
		TelegramID:     -2001,
		ClientID:       "client-xui-fail",
		SubscriptionID: "sub-xui-fail",
		PlanID:         1,
		Status:         "active",
		CreatedAt:      time.Now().Add(-2 * time.Hour),
	}
	err = db.CreateSubscription(ctx, expiredSub, "")
	require.NoError(t, err)

	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	scheduler.runCleanup(ctx)

	remaining, err := db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Empty(t, remaining)
}

func TestTrialCleanupScheduler_FallbackDeprovisionEvenWithSyncService(t *testing.T) {
	t.Parallel()

	// database.CleanupExpiredTrials returns only id/client_id/subscription_id
	// (RETURNING), so sub.Status is empty and the sync branch inside
	// SubscriptionService.CleanupExpiredTrials is unreachable: expired trial
	// clients must still be deprovisioned via the direct fallback even when a
	// sync service is wired (the wiring in main.go must not break this).
	db := &testutil.DatabaseService{
		CleanupExpiredTrialsFunc: func(context.Context, int) ([]database.Subscription, error) {
			return []database.Subscription{
				{ID: 1, ClientID: "c1", SubscriptionID: "exp1"},
				{ID: 2, ClientID: "c2", SubscriptionID: "exp2"},
			}, nil
		},
	}
	mockVPN := &mockVPNClientForExpire{}
	vpnClients := map[uint]vpn.Client{1: mockVPN}
	nodes := []database.Node{{ID: 1, IsActive: true, Host: "http://node"}}

	subService := service.NewSubscriptionService(db, nil, vpnClients, nodes, &config.Config{TrialDurationHours: 24})
	subService.SetSyncService(service.NewSyncService(db, nil, nodes))
	scheduler := NewTrialCleanupScheduler(subService)

	scheduler.runCleanup(context.Background())

	assert.True(t, mockVPN.deleteCalled, "expired trial clients must be deprovisioned via the direct fallback")
	require.Len(t, mockVPN.deleteProvisions, 2, "both expired trial clients must be deleted")
	assert.Equal(t, "exp1", mockVPN.deleteProvisions[0].SubID)
	assert.Equal(t, "c1", mockVPN.deleteProvisions[0].ClientID)
	assert.Equal(t, "exp2", mockVPN.deleteProvisions[1].SubID)
	assert.Equal(t, "c2", mockVPN.deleteProvisions[1].ClientID)
}

func TestTrialCleanupScheduler_Start_ContextCancel(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		scheduler.Start(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Scheduler should stop after context cancel")
	}
}
