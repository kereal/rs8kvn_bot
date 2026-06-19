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

	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

func newTestSubServiceForSyncWorker(t testing.TB, db *database.Service) *service.SubscriptionService {
	t.Helper()
	cfg := &config.Config{
		TrialDurationHours: 1,
	}
	return service.NewSubscriptionService(db, nil, nil, nil, cfg, "", nil)
}

func TestSubscriptionSyncWorker_Run_CallsSyncPendingNodes(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-worker", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node := &database.Node{Name: "sync-worker-node", IsActive: true, Host: "http://sw", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     8888,
		Username:       "syncworker",
		ClientID:       "c-syncworker",
		SubscriptionID: "s-syncworker",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingAdd}))

	syncSvc := service.NewSyncService(db, nil, []database.Node{*node})
	worker := NewSubscriptionSyncWorker(syncSvc)

	workerCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		worker.Run(workerCtx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SubscriptionSyncWorker.Run should have returned after context timeout")
	}
}
