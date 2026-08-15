package service

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncService_SyncSubscription_PendingUpdate_SendsProvisionAndActivates(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "premium-update-plan", IsActive: true, DevicesLimit: 2, TrafficLimit: 8192}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node := &database.Node{Name: "pending-update-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://update", APIToken: "token", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	expiresAt := time.Now().UTC().Add(12 * time.Hour).Truncate(time.Minute)
	sub := &database.Subscription{TelegramID: 994001, Username: "update-user", ClientID: "update-client", SubscriptionID: "update-sub", Status: "active", PlanID: plan.ID, ExpiresAt: &expiresAt}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate}))

	client := &mockVPNClient{}
	svc := NewSyncService(db, map[uint]vpn.Client{node.ID: client}, []database.Node{*node})
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
	assert.Equal(t, 0, rows[0].RetryCount)
	require.True(t, client.updateCalled)
	assert.Equal(t, sub.ClientID, client.updateProvision.ClientID)
	assert.Equal(t, XUIEmail(sub.Username, sub.TelegramID), client.updateProvision.Username)
	assert.Equal(t, sub.SubscriptionID, client.updateProvision.SubID)
	assert.Equal(t, plan.TrafficLimit, client.updateProvision.TrafficBytes)
	assert.Equal(t, -1, client.updateProvision.ResetDays)
	assert.Equal(t, expiresAt, client.updateProvision.ExpiryTime)
}

func TestSyncService_ApplyPlanToSubscription_MarksUpdatesAndReconcilesMembership(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "apply-plan", IsActive: true, DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	oldNode := &database.Node{Name: "apply-old", Type: database.NodeType3xUI, IsActive: true, Host: "http://old", APIToken: "old", InboundIDs: `[1]`}
	targetNode := &database.Node{Name: "apply-target", Type: database.NodeType3xUI, IsActive: true, Host: "http://target", APIToken: "target", InboundIDs: `[1]`}

	require.NoError(t, db.GetDB().WithContext(ctx).Create(oldNode).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(targetNode).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: targetNode.ID}).Error)

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	sub := &database.Subscription{TelegramID: 994002, Username: "apply-user", ClientID: "apply-client", SubscriptionID: "apply-sub", Status: "active", PlanID: plan.ID, ExpiresAt: &expiresAt}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: oldNode.ID, Status: database.SyncStatusActive}))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: targetNode.ID, Status: database.SyncStatusActive}))

	svc := NewSyncService(db, map[uint]vpn.Client{}, []database.Node{*oldNode, *targetNode})
	require.NoError(t, svc.ApplyPlanToSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)

	statusByNode := make(map[uint]database.SyncStatus, len(rows))
	for _, row := range rows {
		statusByNode[row.NodeID] = row.Status
	}

	assert.Equal(t, database.SyncStatusPendingRemove, statusByNode[oldNode.ID])
	assert.Equal(t, database.SyncStatusPendingUpdate, statusByNode[targetNode.ID])
}
