package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionNodeRepository_GetBySubscriptionID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-sub-node", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node1 := &Node{Name: "node-1", IsActive: true, Host: "http://n1", APIToken: "t1", InboundIDs: `[1]`}
	node2 := &Node{Name: "node-2", IsActive: true, Host: "http://n2", APIToken: "t2", InboundIDs: `[1]`}

	require.NoError(t, svc.db.WithContext(ctx).Create(node1).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(node2).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node2.ID}).Error)

	sub := &Subscription{
		TelegramID:     111111,
		Username:       "subnodeuser",
		ClientID:       "subnode-client",
		SubscriptionID: "subnode-sub",
		Status:         "active",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		PlanID:         plan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: SyncStatusActive}))
	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node2.ID, Status: SyncStatusPendingAdd}))

	rows, err := svc.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	nodeIDs := make(map[uint]bool)
	for _, r := range rows {
		nodeIDs[r.NodeID] = true
	}

	assert.True(t, nodeIDs[node1.ID])
	assert.True(t, nodeIDs[node2.ID])
}

func TestSubscriptionNodeRepository_GetBySubscriptionID_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	rows, err := svc.GetBySubscriptionID(ctx, 99999)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestSubscriptionNodeRepository_GetPendingSync(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-pending", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node1 := &Node{Name: "node-pending-1", IsActive: true, Host: "http://p1", APIToken: "t", InboundIDs: `[1]`}
	node2 := &Node{Name: "node-pending-2", IsActive: true, Host: "http://p2", APIToken: "t", InboundIDs: `[1]`}
	node3 := &Node{Name: "node-pending-3", IsActive: true, Host: "http://p3", APIToken: "t", InboundIDs: `[1]`}

	require.NoError(t, svc.db.WithContext(ctx).Create(node1).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(node2).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(node3).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node2.ID}).Error)

	sub := &Subscription{TelegramID: 202, Username: "pendinguser", ClientID: "c-pending", SubscriptionID: "s-pending", Status: "active", PlanID: plan.ID}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: SyncStatusPendingAdd}))
	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node2.ID, Status: SyncStatusActive}))

	future := time.Now().UTC().Add(10 * time.Minute)
	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node3.ID, Status: SyncStatusPendingRemove, RetryAt: &future}))

	rows, err := svc.GetPendingSync(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, SyncStatusPendingAdd, rows[0].Status)
}

func TestSubscriptionNodeRepository_GetPendingSync_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	rows, err := svc.GetPendingSync(ctx)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestSubscriptionNodeRepository_CreateSubscriptionNode(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-create-sn", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node := &Node{Name: "node-create-sn", IsActive: true, Host: "http://cs", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(node).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &Subscription{TelegramID: 404, Username: "create-sn", ClientID: "c-csn", SubscriptionID: "s-csn", Status: "active", PlanID: plan.ID}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	sn := &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: SyncStatusPendingAdd}
	require.NoError(t, svc.CreateSubscriptionNode(ctx, sn))

	var found SubscriptionNode
	require.NoError(t, svc.db.WithContext(ctx).Where("subscription_id = ? AND node_id = ?", sub.ID, node.ID).First(&found).Error)
	assert.Equal(t, SyncStatusPendingAdd, found.Status)
}

func TestSubscriptionNodeRepository_UpdateSubscriptionNodeStatus(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-update-status", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node := &Node{Name: "node-update-status", IsActive: true, Host: "http://us", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(node).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &Subscription{TelegramID: 505, Username: "updatestatus", ClientID: "c-us", SubscriptionID: "s-us", Status: "active", PlanID: plan.ID}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	errMsg := "boom"
	retryAt := time.Now().UTC().Add(10 * time.Minute)
	require.NoError(t, svc.db.WithContext(ctx).Create(&SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: SyncStatusPendingAdd, RetryCount: 3, RetryAt: &retryAt, LastError: &errMsg}).Error)

	require.NoError(t, svc.UpdateSubscriptionNodeStatus(ctx, sub.ID, node.ID, SyncStatusActive))

	var found SubscriptionNode
	require.NoError(t, svc.db.WithContext(ctx).Where("subscription_id = ? AND node_id = ?", sub.ID, node.ID).First(&found).Error)
	assert.Equal(t, SyncStatusActive, found.Status)
	assert.Equal(t, 0, found.RetryCount)
	assert.Nil(t, found.RetryAt)
	assert.Nil(t, found.LastError)
}

func TestSubscriptionNodeRepository_UpdateSubscriptionNodeStatus_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.UpdateSubscriptionNodeStatus(ctx, 99999, 99999, SyncStatusActive)
	assert.Error(t, err)
}

func TestSubscriptionNodeRepository_UpsertSubscriptionNode(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-upsert", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node := &Node{Name: "node-upsert", IsActive: true, Host: "http://u", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(node).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &Subscription{TelegramID: 606, Username: "upsertuser", ClientID: "c-upsert", SubscriptionID: "s-upsert", Status: "active", PlanID: plan.ID}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	sn := &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: SyncStatusPendingAdd}
	require.NoError(t, svc.UpsertSubscriptionNode(ctx, sn))

	var found SubscriptionNode
	require.NoError(t, svc.db.WithContext(ctx).Where("subscription_id = ? AND node_id = ?", sub.ID, node.ID).First(&found).Error)
	assert.Equal(t, SyncStatusPendingAdd, found.Status)

	sn2 := &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: SyncStatusActive}
	require.NoError(t, svc.UpsertSubscriptionNode(ctx, sn2))

	var found2 SubscriptionNode
	require.NoError(t, svc.db.WithContext(ctx).Where("subscription_id = ? AND node_id = ?", sub.ID, node.ID).First(&found2).Error)
	assert.Equal(t, SyncStatusActive, found2.Status)
}

func TestSubscriptionNodeRepository_DeleteSubscriptionNode(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-delete-sn", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node := &Node{Name: "node-delete-sn", IsActive: true, Host: "http://d", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(node).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &Subscription{TelegramID: 707, Username: "deletesn", ClientID: "c-dsn", SubscriptionID: "s-dsn", Status: "active", PlanID: plan.ID}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
	require.NoError(t, svc.db.WithContext(ctx).Create(&SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: SyncStatusActive}).Error)

	require.NoError(t, svc.DeleteSubscriptionNode(ctx, sub.ID, node.ID))

	var found SubscriptionNode

	err := svc.db.WithContext(ctx).Where("subscription_id = ? AND node_id = ?", sub.ID, node.ID).First(&found).Error
	assert.Error(t, err)
}

func TestSubscriptionNodeRepository_DeleteSubscriptionNode_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.DeleteSubscriptionNode(ctx, 99999, 99999)
	assert.Error(t, err)
}

func TestSubscriptionNodeRepository_UpdateRetry(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-update-retry", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node := &Node{Name: "node-update-retry", IsActive: true, Host: "http://r", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(node).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &Subscription{TelegramID: 808, Username: "retryuser", ClientID: "c-retry", SubscriptionID: "s-retry", Status: "active", PlanID: plan.ID}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
	require.NoError(t, svc.db.WithContext(ctx).Create(&SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: SyncStatusPendingAdd}).Error)

	errMsg := "connection refused"
	retryAt := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Minute)
	require.NoError(t, svc.UpdateRetry(ctx, sub.ID, node.ID, 2, &retryAt, &errMsg))

	var found SubscriptionNode
	require.NoError(t, svc.db.WithContext(ctx).Where("subscription_id = ? AND node_id = ?", sub.ID, node.ID).First(&found).Error)
	assert.Equal(t, 2, found.RetryCount)
	assert.Equal(t, retryAt, *found.RetryAt)
	assert.Equal(t, errMsg, *found.LastError)
}
