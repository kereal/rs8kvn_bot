package scheduler

import (
	"context"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

func init() {
	_, _ = logger.Init("", "error")
}

func TestSubscriptionSyncWorker_Run_RecordsUnavailableNodeRetry(t *testing.T) {
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
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingAdd}))

	syncSvc := service.NewSyncService(db, nil, []database.Node{*node})
	worker := NewSubscriptionSyncWorker(syncSvc)

	workerCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		worker.Run(workerCtx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SubscriptionSyncWorker.Run should have returned after context timeout")
	}

	var nodeState database.SubscriptionNode
	require.NoError(t, db.GetDB().WithContext(ctx).
		Where("subscription_id = ? AND node_id = ?", sub.ID, node.ID).
		First(&nodeState).Error)
	require.Equal(t, 1, nodeState.RetryCount, "worker must process the pending node")
	require.NotNil(t, nodeState.LastError, "unavailable node must record retry error")
	require.Contains(t, *nodeState.LastError, "no VPN client")
}
