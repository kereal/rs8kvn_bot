package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockVPNClient struct {
	createCalled       bool
	deleteCalled       bool
	updateCalled       bool
	resetTrafficCalled bool
	createError        error
	deleteError        error
	updateError        error
	resetTrafficError  error
	createProvision    vpn.SubscriptionProvision
	deleteProvision    vpn.SubscriptionProvision
	updateProvision    vpn.SubscriptionProvision
}

func (m *mockVPNClient) CreateSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	m.createCalled = true
	m.createProvision = provision

	return m.createError
}

func (m *mockVPNClient) UpdateSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	m.updateCalled = true
	m.updateProvision = provision

	return m.updateError
}

func (m *mockVPNClient) DeleteSubscription(ctx context.Context, provision vpn.SubscriptionProvision) error {
	m.deleteCalled = true
	m.deleteProvision = provision

	return m.deleteError
}

func (m *mockVPNClient) ResetTraffic(_ context.Context, _ vpn.SubscriptionProvision) error {
	m.resetTrafficCalled = true

	return m.resetTrafficError
}

func (m *mockVPNClient) Close() error {
	return nil
}

func newTestSyncService(t *testing.T, db interfaces.DatabaseService, nodes []database.Node) *SyncService {
	t.Helper()

	vpnClients := make(map[uint]vpn.Client)
	for _, n := range nodes {
		vpnClients[n.ID] = &mockVPNClient{}
	}

	return NewSyncService(db, vpnClients, nodes)
}

func TestSyncService_ReconcilePlanNodes_AddMissing(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-recalc", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "rec-node-1", IsActive: true, Host: "http://r1", APIToken: "t1", InboundIDs: `[1]`}
	node2 := &database.Node{Name: "rec-node-2", IsActive: true, Host: "http://r2", APIToken: "t2", InboundIDs: `[1]`}
	node3 := &database.Node{Name: "rec-node-3", IsActive: false, Host: "http://r3", APIToken: "t3", InboundIDs: `[1]`}

	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node2).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node3).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Model(node3).Update("is_active", false).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node2.ID}).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node3.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     1111,
		Username:       "recuser",
		ClientID:       "c-rec",
		SubscriptionID: "s-rec",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusActive}))

	svc := newTestSyncService(t, db, []database.Node{*node1, *node2, *node3})
	require.NoError(t, svc.ReconcilePlanNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	statusMap := make(map[uint]database.SyncStatus)
	for _, r := range rows {
		statusMap[r.NodeID] = r.Status
	}

	assert.Equal(t, database.SyncStatusActive, statusMap[node1.ID])
	assert.Equal(t, database.SyncStatusPendingAdd, statusMap[node2.ID])
	assert.Empty(t, statusMap[node3.ID])
}

func TestSyncService_ReconcilePlanNodes_ContinuesAfterNodeFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	firstErr := errors.New("node one upsert failed")
	processed := make([]uint, 0, 2)

	db := testutil.NewDatabaseService()
	db.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{ID: 42, PlanID: 7}, nil
	}
	db.GetNodesByPlanIDFunc = func(context.Context, uint) ([]database.Node, error) {
		return []database.Node{
			{ID: 1, IsActive: true},
			{ID: 2, IsActive: true},
		}, nil
	}
	db.GetBySubscriptionIDFunc = func(context.Context, uint) ([]database.SubscriptionNode, error) {
		return nil, nil
	}
	db.UpsertSubscriptionNodeFunc = func(_ context.Context, sn *database.SubscriptionNode) error {
		processed = append(processed, sn.NodeID)
		if sn.NodeID == 1 {
			return firstErr
		}

		return nil
	}

	svc := NewSyncService(db, nil, nil)

	err := svc.ReconcilePlanNodes(ctx, 42)
	require.Error(t, err)
	assert.ErrorIs(t, err, firstErr)
	assert.Equal(t, []uint{1, 2}, processed, "a failed node must not prevent the next node from reconciling")
}

func TestSyncService_ReconcilePlanNodes_RemoveExtra(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-recalc-rm", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "recrm-1", IsActive: true, Host: "http://rr1", APIToken: "t1", InboundIDs: `[1]`}
	node2 := &database.Node{Name: "recrm-2", IsActive: false, Host: "http://rr2", APIToken: "t2", InboundIDs: `[1]`}

	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node2).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     2222,
		Username:       "recrmuser",
		ClientID:       "c-recrm",
		SubscriptionID: "s-recrm",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusActive}))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node2.ID, Status: database.SyncStatusActive}))

	svc := newTestSyncService(t, db, []database.Node{*node1})
	require.NoError(t, svc.ReconcilePlanNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	statusMap := make(map[uint]database.SyncStatus)
	for _, row := range rows {
		statusMap[row.NodeID] = row.Status
	}

	assert.Equal(t, database.SyncStatusActive, statusMap[node1.ID])
	assert.Equal(t, database.SyncStatusPendingRemove, statusMap[node2.ID])
}

func TestSyncService_ReconcilePlanNodes_KeepExisting(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-recalc-keep", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "keep-1", IsActive: true, Host: "http://k1", APIToken: "t1", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     3333,
		Username:       "keepuser",
		ClientID:       "c-keep",
		SubscriptionID: "s-keep",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusActive}))

	svc := newTestSyncService(t, db, []database.Node{*node1})
	require.NoError(t, svc.ReconcilePlanNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
}

func TestSyncService_SyncNodes_SkipsUnknownNodeType(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-unknown-type", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	unknownNode := &database.Node{Name: "unknown-node", Type: database.NodeType("unknown"), IsActive: true, Host: "http://unknown", APIToken: "token", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(unknownNode).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: unknownNode.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9999,
		Username:       "unknowntype",
		ClientID:       "c-unknowntype",
		SubscriptionID: "s-unknowntype",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: unknownNode.ID, Status: database.SyncStatusPendingAdd}))

	svc := NewSyncService(db, map[uint]vpn.Client{}, []database.Node{*unknownNode})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err, "unknown node type should be skipped without error")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status, "unknown node type record should remain pending")
	assert.Greater(t, rows[0].RetryCount, 0, "unknown node type should schedule retry")
	assert.NotNil(t, rows[0].RetryAt, "retry_at should be set")
	assert.NotNil(t, rows[0].LastError, "last_error should be set")
}

func TestSyncService_SyncNodes_FetchType_PendingAddBecomesActive(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-fetch", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	fetchNode := &database.Node{Name: "fetch-node", Type: database.NodeTypeFetch, IsActive: true, Host: "http://fetch", APIToken: "token", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(fetchNode).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: fetchNode.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     8888,
		Username:       "fetchuser",
		ClientID:       "c-fetch",
		SubscriptionID: "s-fetch",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: fetchNode.ID, Status: database.SyncStatusPendingAdd}))

	fetchClient := vpn.NewFetchClient()
	svc := NewSyncService(db, map[uint]vpn.Client{fetchNode.ID: fetchClient}, []database.Node{*fetchNode})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err)

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status, "fetch node pending_add should become active via no-op CreateSubscription")
	assert.Equal(t, 0, rows[0].RetryCount, "no retry expected for no-op fetch client")
}

func TestSyncService_ReconcilePlanNodes_ReactivatePendingRemove(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-recalc-react", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "react-1", IsActive: true, Host: "http://rt1", APIToken: "t1", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     4444,
		Username:       "reactuser",
		ClientID:       "c-react",
		SubscriptionID: "s-react",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingRemove}))

	svc := newTestSyncService(t, db, []database.Node{*node1})
	require.NoError(t, svc.ReconcilePlanNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)
}

func TestSyncService_ReconcilePlanNodes_RemovesStalePendingAdd(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-recalc-stale", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "stale-1", IsActive: true, Host: "http://st1", APIToken: "t1", InboundIDs: `[1]`}
	node2 := &database.Node{Name: "stale-2", IsActive: true, Host: "http://st2", APIToken: "t2", InboundIDs: `[1]`}

	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node2).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     4545,
		Username:       "staleuser",
		ClientID:       "c-stale",
		SubscriptionID: "s-stale",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusActive}))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node2.ID, Status: database.SyncStatusPendingAdd}))

	svc := newTestSyncService(t, db, []database.Node{*node1, *node2})
	require.NoError(t, svc.ReconcilePlanNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.Equal(t, node1.ID, rows[0].NodeID)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
	assert.Equal(t, node2.ID, rows[1].NodeID)
	assert.Equal(t, database.SyncStatusPendingRemove, rows[1].Status)
}

func TestSyncService_ReconcilePlanNodes_PlanChange_KeepsActiveWhenSamePlan(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	planFree := &database.Plan{Name: "test-plan-keep-free", DevicesLimit: 1, TrafficLimit: 1024}
	planPremium := &database.Plan{Name: "test-plan-keep-premium", DevicesLimit: 1, TrafficLimit: 0}

	require.NoError(t, db.GetDB().WithContext(ctx).Create(planFree).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(planPremium).Error)

	node1 := &database.Node{Name: "plan-keep-node", IsActive: true, Host: "http://pk1", APIToken: "tpk", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: planPremium.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     8888,
		Username:       "plankeepuser",
		ClientID:       "c-plankeep",
		SubscriptionID: "s-plankeep",
		Status:         "active",
		PlanID:         planPremium.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusActive}))

	svc := newTestSyncService(t, db, []database.Node{*node1})
	require.NoError(t, svc.ReconcilePlanNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status, "active node should stay active when still in the current plan")
}

func TestSyncService_SyncSubscription_PendingAdd(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-add", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-add-node", IsActive: true, Host: "http://sa", APIToken: "ta", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     5555,
		Username:       "syncadduser",
		ClientID:       "c-syncadd",
		SubscriptionID: "s-syncadd",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

	mockVPN := &mockVPNClient{}
	vpnClients := map[uint]vpn.Client{node1.ID: mockVPN}
	svc := NewSyncService(db, vpnClients, []database.Node{*node1})

	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
	assert.True(t, mockVPN.createCalled, "CreateSubscription should be called on the VPN client")
	assert.Equal(t, sub.ClientID, mockVPN.createProvision.ClientID)
	assert.Equal(t, sub.SubscriptionID, mockVPN.createProvision.SubID)
	assert.Equal(t, int64(1024), mockVPN.createProvision.TrafficBytes)
	assert.Equal(t, XUIEmail(sub.Username, sub.TelegramID), mockVPN.createProvision.Username)
}

func TestSyncService_SyncSubscription_PendingAdd_UnlimitedPlan(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-add-unlimited", DevicesLimit: 1, TrafficLimit: 0}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-add-unlimited-node", IsActive: true, Host: "http://sa", APIToken: "ta", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     5556,
		Username:       "syncaddunlimited",
		ClientID:       "c-syncadd-unlimited",
		SubscriptionID: "s-syncadd-unlimited",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

	mockVPN := &mockVPNClient{}
	vpnClients := map[uint]vpn.Client{node1.ID: mockVPN}
	svc := NewSyncService(db, vpnClients, []database.Node{*node1})

	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
	assert.True(t, mockVPN.createCalled, "CreateSubscription should be called on the VPN client")
	assert.Equal(t, int64(0), mockVPN.createProvision.TrafficBytes)
	assert.Equal(t, 0, mockVPN.createProvision.ResetDays, "ResetDays must be 0 for unlimited plan")
	assert.True(t, mockVPN.createProvision.ExpiryTime.IsZero(), "ExpiryTime must be zero for unlimited plan")
}

func TestSyncService_SyncPendingAdd_KeepsExpiredExpiry(t *testing.T) {
	t.Parallel()

	// The sync layer must NOT move an already-expired expiry by itself: trials
	// must never be extended, and free-client recovery is the xui layer's job
	// (doUpdateClient, guarded by resetDays > 0).
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	node1 := &database.Node{Name: "sync-expired-node", IsActive: true, Host: "http://se", APIToken: "te", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)

	expired := testutil.PtrTime(time.Now().Add(-time.Hour))

	t.Run("free_plan", func(t *testing.T) {
		plan := &database.Plan{Name: "test-plan-sync-expired-free", DevicesLimit: 1, TrafficLimit: 1024}
		require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)
		require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

		sub := &database.Subscription{
			TelegramID:     9101,
			Username:       "expiredfree",
			ClientID:       "c-expiredfree",
			SubscriptionID: "s-expiredfree",
			Status:         "active",
			PlanID:         plan.ID,
			ExpiresAt:      expired,
		}
		require.NoError(t, db.CreateSubscription(ctx, sub, ""))
		require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

		mockVPN := &mockVPNClient{}
		svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mockVPN}, []database.Node{*node1})

		require.NoError(t, svc.SyncSubscription(ctx, sub.ID))
		assert.True(t, mockVPN.createCalled)
		assert.True(t, mockVPN.createProvision.ExpiryTime.Equal(*expired), "sync must not extend an expired free client; recovery happens in the xui update layer")
		assert.Equal(t, "monthly", mockVPN.createProvision.TrafficReset)
	})

	t.Run("trial_plan", func(t *testing.T) {
		// The trial plan is seeded by database.NewService; reuse it instead of
		// creating a duplicate (plans.name is unique).
		plan, err := db.GetPlanByName(ctx, database.TrialPlanName)
		require.NoError(t, err)
		require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

		sub := &database.Subscription{
			TelegramID:     9102,
			Username:       "expiredtrial",
			ClientID:       "c-expiredtrial",
			SubscriptionID: "s-expiredtrial",
			Status:         "active",
			PlanID:         plan.ID,
			ExpiresAt:      expired,
		}
		require.NoError(t, db.CreateSubscription(ctx, sub, ""))
		require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

		mockVPN := &mockVPNClient{}
		svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mockVPN}, []database.Node{*node1})

		require.NoError(t, svc.SyncSubscription(ctx, sub.ID))
		assert.True(t, mockVPN.createCalled)
		assert.True(t, mockVPN.createProvision.ExpiryTime.Equal(*expired), "sync must never extend an expired trial")
		assert.Equal(t, 0, mockVPN.createProvision.ResetDays, "trials must keep reset=0")
	})
}

func TestSyncService_SyncSubscription_PendingRemove(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-rm", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-rm-node", IsActive: true, Host: "http://sr", APIToken: "tr", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     6666,
		Username:       "syncrmuser",
		ClientID:       "c-syncrm",
		SubscriptionID: "s-syncrm",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingRemove}))

	mockVPN := &mockVPNClient{}
	vpnClients := map[uint]vpn.Client{node1.ID: mockVPN}
	svc := NewSyncService(db, vpnClients, []database.Node{*node1})

	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "pending_remove should delete the subscription node record")
	assert.True(t, mockVPN.deleteCalled, "DeleteSubscription should be called on the VPN client")
	assert.Equal(t, sub.ClientID, mockVPN.deleteProvision.ClientID)
	assert.Equal(t, sub.SubscriptionID, mockVPN.deleteProvision.SubID)
}

func TestSyncService_SyncSubscription_UsesFallbackXUIIdentifier(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-fallback", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-fallback-node", IsActive: true, Host: "http://sf", APIToken: "tf", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     777000,
		Username:       "",
		ClientID:       "c-syncfallback",
		SubscriptionID: "s-syncfallback",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

	mockVPN := &mockVPNClient{}
	svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mockVPN}, []database.Node{*node1})

	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))
	assert.Equal(t, "tgId_777000", mockVPN.createProvision.Username)
}

func TestSyncService_SyncPendingNodes_ProcessesOnlyDueNodes(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-due-only", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	nodeDue := &database.Node{Name: "sync-due-node", IsActive: true, Host: "http://sd1", APIToken: "t1", InboundIDs: `[1]`}
	nodeLater := &database.Node{Name: "sync-later-node", IsActive: true, Host: "http://sd2", APIToken: "t2", InboundIDs: `[1]`}

	require.NoError(t, db.GetDB().WithContext(ctx).Create(nodeDue).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(nodeLater).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: nodeDue.ID}).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: nodeLater.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     8888,
		Username:       "dueuser",
		ClientID:       "c-due",
		SubscriptionID: "s-due",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	future := time.Now().UTC().Add(10 * time.Minute)

	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: nodeDue.ID, Status: database.SyncStatusPendingAdd}))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: nodeLater.ID, Status: database.SyncStatusPendingAdd, RetryAt: &future}))

	dueClient := &mockVPNClient{}
	laterClient := &mockVPNClient{}
	svc := NewSyncService(db, map[uint]vpn.Client{nodeDue.ID: dueClient, nodeLater.ID: laterClient}, []database.Node{*nodeDue, *nodeLater})

	require.NoError(t, svc.SyncPendingNodes(ctx))
	assert.True(t, dueClient.createCalled)
	assert.False(t, laterClient.createCalled)

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)

	statusByNode := make(map[uint]database.SubscriptionNode)
	for _, row := range rows {
		statusByNode[row.NodeID] = row
	}

	assert.Equal(t, database.SyncStatusActive, statusByNode[nodeDue.ID].Status)
	assert.Equal(t, database.SyncStatusPendingAdd, statusByNode[nodeLater.ID].Status)
	assert.NotNil(t, statusByNode[nodeLater.ID].RetryAt)
}

func TestSyncService_handleSyncError_IncrementsRetry(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-err", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-err-node", IsActive: true, Host: "http://se", APIToken: "te", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     7777,
		Username:       "syncerruser",
		ClientID:       "c-syncerr",
		SubscriptionID: "s-syncerr",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	sn := &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd, RetryCount: 0}
	require.NoError(t, db.CreateSubscriptionNode(ctx, sn))

	svc := NewSyncService(db, nil, []database.Node{*node1})
	svc.handleSyncError(ctx, sn, assert.AnError)

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].RetryCount)
	assert.Equal(t, "assert.AnError general error for testing", *rows[0].LastError)
	assert.NotNil(t, rows[0].RetryAt)
}

func TestCalculateRetryAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		retryCount int
		wantMin    time.Duration
		wantMax    time.Duration
	}{
		{"retry 0 -> 1m", 0, 1 * time.Minute, 1*time.Minute + time.Minute},
		{"retry 1 -> 2m", 1, 2 * time.Minute, 2*time.Minute + time.Minute},
		{"retry 2 -> 5m", 2, 5 * time.Minute, 5*time.Minute + time.Minute},
		{"retry 3 -> 15m", 3, 15 * time.Minute, 15*time.Minute + time.Minute},
		{"retry 4 -> 30m", 4, 30 * time.Minute, 30*time.Minute + time.Minute},
		{"retry 5 -> 45m", 5, 45 * time.Minute, 45*time.Minute + time.Minute},
		{"retry 6 -> 60m", 6, 60 * time.Minute, 60*time.Minute + time.Minute},
		{"retry 10 -> 60m", 10, 60 * time.Minute, 60*time.Minute + time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateRetryAt(tt.retryCount)
			diff := got.Sub(time.Now().UTC().Truncate(time.Minute))
			assert.GreaterOrEqual(t, diff, tt.wantMin)
			assert.Less(t, diff, tt.wantMax)
		})
	}
}

func TestVPNAlreadyExistsError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"already exists", fmt.Errorf("3x-ui create subscription: %w", fmt.Errorf("%w: %w", vpn.ErrSubscriptionAlreadyExists, fmt.Errorf("client already exists"))), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errors.Is(tt.err, vpn.ErrSubscriptionAlreadyExists)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVPNNotFoundError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"not found", fmt.Errorf("3x-ui delete subscription: %w", fmt.Errorf("%w: %w", vpn.ErrSubscriptionNotFound, fmt.Errorf("client not found"))), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errors.Is(tt.err, vpn.ErrSubscriptionNotFound)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSyncService_SyncSubscription_PendingAdd_AlreadyExists(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-add-exists", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-add-exists-node", IsActive: true, Host: "http://sae", APIToken: "tae", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9991,
		Username:       "syncaddexists",
		ClientID:       "c-syncaddexists",
		SubscriptionID: "s-syncaddexists",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

	mockVPN := &mockVPNClient{createError: fmt.Errorf("%w: %w", vpn.ErrSubscriptionAlreadyExists, fmt.Errorf("client already exists"))}
	svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mockVPN}, []database.Node{*node1})

	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status, "should mark active when client already exists")
	assert.Equal(t, 0, rows[0].RetryCount, "should not increment retry count")
	assert.True(t, mockVPN.updateCalled, "UpdateSubscription should be called when client already exists")
	assert.Equal(t, XUIEmail(sub.Username, sub.TelegramID), mockVPN.updateProvision.CurrentEmail, "CurrentEmail must identify the existing client on the panel")
	assert.Equal(t, XUIEmail(sub.Username, sub.TelegramID), mockVPN.updateProvision.Username)
	assert.Equal(t, sub.TelegramID, mockVPN.createProvision.TgID, "create must carry the Telegram id")
	assert.Equal(t, sub.TelegramID, mockVPN.updateProvision.TgID, "update must preserve the Telegram id")
}

func TestSyncService_SyncSubscription_PendingAdd_AlreadyExistsUpdateFails(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-add-exists-update-fails", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-add-exists-update-fails-node", IsActive: true, Host: "http://saeuf", APIToken: "taeuf", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9994,
		Username:       "syncaddexistsupdatefails",
		ClientID:       "c-syncaddexistsupdatefails",
		SubscriptionID: "s-syncaddexistsupdatefails",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

	mockVPN := &mockVPNClient{createError: fmt.Errorf("%w: %w", vpn.ErrSubscriptionAlreadyExists, fmt.Errorf("client already exists")), updateError: fmt.Errorf("update refused")}
	svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mockVPN}, []database.Node{*node1})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err, "background-style sync: missing VPN client must not propagate")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)
	assert.Equal(t, 1, rows[0].RetryCount)
	assert.NotNil(t, rows[0].LastError)
	assert.Equal(t, "update refused", *rows[0].LastError)
	assert.True(t, mockVPN.updateCalled)
}

func TestSyncService_SyncSubscription_PendingRemove_NotFound(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-rm-notfound", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-rm-notfound-node", IsActive: true, Host: "http://srnf", APIToken: "trnf", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9992,
		Username:       "syncrmnotfound",
		ClientID:       "c-syncrmnotfound",
		SubscriptionID: "s-syncrmnotfound",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingRemove}))

	mockVPN := &mockVPNClient{deleteError: fmt.Errorf("%w: %w", vpn.ErrSubscriptionNotFound, fmt.Errorf("client not found"))}
	svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mockVPN}, []database.Node{*node1})

	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "should delete subscription node when client not found")
}

func TestSyncService_SyncSubscription_PendingAdd_RetryOnOtherError(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-add-retry", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-add-retry-node", IsActive: true, Host: "http://sar", APIToken: "tar", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9993,
		Username:       "syncaddretry",
		ClientID:       "c-syncaddretry",
		SubscriptionID: "s-syncaddretry",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

	mockVPN := &mockVPNClient{createError: fmt.Errorf("connection refused")}
	svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mockVPN}, []database.Node{*node1})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err, "background-style sync: per-node failure must not propagate")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)
	assert.Equal(t, 1, rows[0].RetryCount)
}

func TestSyncService_SyncSubscription_PendingAdd_NoVPNClientKeepsPending(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-add-no-vpn-client", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-add-no-vpn-client-node", IsActive: true, Host: "http://sanvc", APIToken: "tanvc", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9995,
		Username:       "syncaddnovpnclient",
		ClientID:       "c-syncaddnovpnclient",
		SubscriptionID: "s-syncaddnovpnclient",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd}))

	svc := NewSyncService(db, map[uint]vpn.Client{}, []database.Node{*node1})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err, "background-style sync: missing VPN client must not propagate")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)
	assert.Equal(t, 1, rows[0].RetryCount)
	assert.NotNil(t, rows[0].LastError)
}

func TestSyncService_SyncSubscription_PendingUpdate_NoVPNClientKeepsPending(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-update-no-vpn-client", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-update-no-vpn-client-node", IsActive: true, Host: "http://sunvc", APIToken: "tunvc", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9996,
		Username:       "syncupdatenovpnclient",
		ClientID:       "c-syncupdatenovpnclient",
		SubscriptionID: "s-syncupdatenovpnclient",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingUpdate}))

	svc := NewSyncService(db, map[uint]vpn.Client{}, []database.Node{*node1})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err, "background-style sync: missing VPN client must not propagate")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingUpdate, rows[0].Status)
	assert.Equal(t, 1, rows[0].RetryCount)
}

func TestSyncService_SyncSubscription_PendingUpdate_ResetsTraffic(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-update-reset", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-update-reset-node", IsActive: true, Host: "http://sur", APIToken: "tur", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9998,
		Username:       "syncupdatereset",
		ClientID:       "c-syncupdatereset",
		SubscriptionID: "s-syncupdatereset",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingUpdate}))

	mock := &mockVPNClient{}
	svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mock}, []database.Node{*node1})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err)

	assert.True(t, mock.updateCalled, "plan change must update the VPN client")
	assert.True(t, mock.resetTrafficCalled, "plan change must reset the traffic counter")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
}

func TestSyncService_SyncSubscription_PendingUpdate_ResetTrafficBestEffort(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-update-reset-fail", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-update-reset-fail-node", IsActive: true, Host: "http://surf", APIToken: "turf", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9999,
		Username:       "syncupdateresetfail",
		ClientID:       "c-syncupdateresetfail",
		SubscriptionID: "s-syncupdateresetfail",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingUpdate}))

	mock := &mockVPNClient{resetTrafficError: errors.New("panel reset failed")}
	svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mock}, []database.Node{*node1})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err, "reset traffic failure is best-effort and must not fail the sync")

	assert.True(t, mock.resetTrafficCalled)

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status, "node must still be marked active despite reset failure")
}

func TestSyncService_SyncSubscription_PendingRemove_NoVPNClientKeepsPending(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-remove-no-vpn-client", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-remove-no-vpn-client-node", IsActive: true, Host: "http://srnvc", APIToken: "trnvc", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9997,
		Username:       "syncremovenovpnclient",
		ClientID:       "c-syncremovenovpnclient",
		SubscriptionID: "s-syncremovenovpnclient",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingRemove}))

	svc := NewSyncService(db, map[uint]vpn.Client{}, []database.Node{*node1})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err, "background-style sync: missing VPN client must not propagate")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingRemove, rows[0].Status)
	assert.Equal(t, 1, rows[0].RetryCount)
}

func TestSyncService_SyncSubscription_PendingRemove_InactiveNodeDropsBinding(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-rm-inactive", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-rm-inactive-node", Host: "http://srni", APIToken: "trni", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	// GORM's default:true tag omits the zero-valued bool on Create, so flip the
	// column explicitly to produce a genuinely inactive node.
	require.NoError(t, db.GetDB().WithContext(ctx).Model(&database.Node{}).Where("id = ?", node1.ID).Update("is_active", false).Error)

	sub := &database.Subscription{
		TelegramID:     9998,
		Username:       "syncrminactive",
		ClientID:       "c-syncrminactive",
		SubscriptionID: "s-syncrminactive",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingRemove}))

	// Inactive nodes are not loaded into the runtime snapshot, so there is no
	// VPN client — the stale binding must be dropped, not retried forever.
	svc := NewSyncService(db, map[uint]vpn.Client{}, nil)

	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "pending_remove on an inactive node must drop the stale binding")
}

// The former TestSyncService_SyncSubscription_PendingRemove_MissingNodeDropsBinding
// was removed: with SQLite foreign-key enforcement enabled (DSN _foreign_keys=on)
// and the ON DELETE CASCADE on subscription_nodes.node_id (migration 022), a
// binding referencing a nonexistent node cannot be inserted in the first place.
// The drop-binding path is still covered by
// TestSyncService_SyncSubscription_PendingRemove_InactiveNodeDropsBinding and the
// retry path by TestSyncService_SyncSubscription_PendingRemove_NoVPNClientKeepsPending.

func TestSyncService_SyncPendingNodes_ContinuesAfterNodeFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	planOne := &database.Plan{Name: "test-plan-sync-continue-one", DevicesLimit: 1, TrafficLimit: 1024}
	planTwo := &database.Plan{Name: "test-plan-sync-continue-two", DevicesLimit: 1, TrafficLimit: 1024}

	require.NoError(t, db.GetDB().WithContext(ctx).Create(planOne).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(planTwo).Error)

	nodeOne := &database.Node{Name: "sync-continue-one", Type: database.NodeType3xUI, IsActive: true, Host: "http://one", APIToken: "one", InboundIDs: `[1]`}
	nodeTwo := &database.Node{Name: "sync-continue-two", Type: database.NodeType3xUI, IsActive: true, Host: "http://two", APIToken: "two", InboundIDs: `[1]`}

	require.NoError(t, db.GetDB().WithContext(ctx).Create(nodeOne).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(nodeTwo).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: planOne.ID, NodeID: nodeOne.ID}).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: planTwo.ID, NodeID: nodeTwo.ID}).Error)

	subOne := &database.Subscription{TelegramID: 9101, Username: "sync-continue-one", ClientID: "client-one", SubscriptionID: "sub-one", Status: "active", PlanID: planOne.ID, ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	subTwo := &database.Subscription{TelegramID: 9102, Username: "sync-continue-two", ClientID: "client-two", SubscriptionID: "sub-two", Status: "active", PlanID: planTwo.ID, ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}

	require.NoError(t, db.CreateSubscription(ctx, subOne, ""))
	require.NoError(t, db.CreateSubscription(ctx, subTwo, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: subOne.ID, NodeID: nodeOne.ID, Status: database.SyncStatusPendingAdd}))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: subTwo.ID, NodeID: nodeTwo.ID, Status: database.SyncStatusPendingAdd}))

	failedClient := &mockVPNClient{createError: errors.New("node one unavailable")}
	successClient := &mockVPNClient{}
	svc := NewSyncService(db, map[uint]vpn.Client{nodeOne.ID: failedClient, nodeTwo.ID: successClient}, []database.Node{*nodeOne, *nodeTwo})

	err = svc.SyncPendingNodes(ctx)
	require.Error(t, err, "a per-node failure must surface as an aggregate error so the caller can observe degraded runs")
	assert.ErrorIs(t, err, failedClient.createError, "the aggregate error must contain the nodeOne failure via errors.Join")

	rowsOne, err := db.GetBySubscriptionID(ctx, subOne.ID)
	require.NoError(t, err)
	require.Len(t, rowsOne, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rowsOne[0].Status)
	assert.Equal(t, 1, rowsOne[0].RetryCount)
	require.NotNil(t, rowsOne[0].LastError)
	assert.Contains(t, *rowsOne[0].LastError, "node one unavailable")

	rowsTwo, err := db.GetBySubscriptionID(ctx, subTwo.ID)
	require.NoError(t, err)
	require.Len(t, rowsTwo, 1)
	assert.Equal(t, database.SyncStatusActive, rowsTwo[0].Status)
	assert.Equal(t, 0, rowsTwo[0].RetryCount)
	assert.True(t, failedClient.createCalled)
	assert.True(t, successClient.createCalled)
}

func TestSyncService_SyncNodes_ReferrerErrorRetriesAndContinues(t *testing.T) {
	t.Parallel()

	referrerErr := errors.New("referrer lookup failed")
	retryNodes := make([]uint, 0, 2)
	db := testutil.NewDatabaseService()
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 7, Name: database.FreePlanName, TrafficLimit: 1024}, nil
	}
	db.GetInviteByCodeFunc = func(context.Context, string) (*database.Invite, error) {
		return nil, referrerErr
	}
	db.UpdateRetryFunc = func(_ context.Context, _, nodeID uint, _ int, _ *time.Time, _ *string) error {
		retryNodes = append(retryNodes, nodeID)
		return nil
	}

	clients := map[uint]vpn.Client{
		1: &mockVPNClient{},
		2: &mockVPNClient{},
	}
	svc := NewSyncService(db, clients, []database.Node{
		{ID: 1, Type: database.NodeType3xUI, IsActive: true},
		{ID: 2, Type: database.NodeType3xUI, IsActive: true},
	})
	sub := &database.Subscription{
		ID:             42,
		PlanID:         7,
		TelegramID:     100,
		Username:       "sync-user",
		ClientID:       "client-42",
		SubscriptionID: "sub-42",
		InviteCode:     testutil.PtrString("REFER123"),
	}
	pending := []database.SubscriptionNode{
		{SubscriptionID: 42, NodeID: 1, Status: database.SyncStatusPendingAdd},
		{SubscriptionID: 42, NodeID: 2, Status: database.SyncStatusPendingAdd},
	}

	err := svc.syncNodes(context.Background(), sub, pending)
	require.Error(t, err)
	assert.ErrorIs(t, err, referrerErr)
	assert.ElementsMatch(t, []uint{1, 2}, retryNodes)
	assert.False(t, clients[1].(*mockVPNClient).createCalled)
	assert.False(t, clients[2].(*mockVPNClient).createCalled)
}

func TestSyncService_SyncPendingNodes_JoinsErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loadErr := errors.New("load subscription failed")
	reconcileErr := errors.New("load plan nodes failed")
	sub := &database.Subscription{ID: 10, TelegramID: 9998, Username: "syncjoinerrors", PlanID: 1}
	pending := []database.SubscriptionNode{
		{SubscriptionID: 10, NodeID: 1, Status: database.SyncStatusPendingAdd},
		{SubscriptionID: 20, NodeID: 2, Status: database.SyncStatusPendingAdd},
	}

	db := testutil.NewDatabaseService()
	db.GetPendingSyncFunc = func(ctx context.Context) ([]database.SubscriptionNode, error) {
		return pending, nil
	}
	db.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		switch id {
		case 10:
			return sub, nil
		case 20:
			return nil, loadErr
		default:
			return nil, errors.New("unexpected subscription")
		}
	}
	db.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return nil, reconcileErr
	}
	db.GetPendingBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		if subscriptionID == 10 {
			return []database.SubscriptionNode{
				{SubscriptionID: 10, NodeID: 1, Status: database.SyncStatusPendingAdd},
			}, nil
		}

		return []database.SubscriptionNode{}, nil
	}
	db.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{}, nil
	}

	svc := NewSyncService(db, map[uint]vpn.Client{}, nil)

	err := svc.SyncPendingNodes(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, loadErr)
	assert.ErrorIs(t, err, reconcileErr)
}

func TestSyncService_lockSubscription_Timeout(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	svc := newTestSyncService(t, db, nil)
	// Pin the production timeout so the elapsed-time assertion is stable.
	svc.lockTimeout = 2 * time.Minute

	// Holder grabs the lock and keeps it indefinitely.
	holdCtx := context.Background()
	release, err := svc.lockSubscription(holdCtx, 42)
	require.NoError(t, err)

	defer release()

	// A waiter must not block forever when the holder hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = svc.lockSubscription(ctx, 42)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, svc.lockTimeout,
		"waiter must not wait the full subscriptionLockTimeout when ctx is short")
}

func TestSyncService_lockSubscription_TimeoutWhenAlreadyHeld(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	svc := newTestSyncService(t, db, nil)
	// Override the production two-minute timeout so the already-held case is
	// bounded to a short duration in this test.
	svc.lockTimeout = 30 * time.Millisecond

	// Take the lock and never release it.
	holdCtx := context.Background()
	release, err := svc.lockSubscription(holdCtx, 7)
	require.NoError(t, err)
	// NB: intentionally do not call release() — simulate a stuck holder.
	_ = release

	// Without an outer deadline, the acquisition is bounded by the internal
	// subscriptionLockTimeout so other goroutines can't block forever.
	start := time.Now()
	_, err = svc.lockSubscription(context.Background(), 7)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.GreaterOrEqual(t, elapsed, svc.lockTimeout,
		"waiter should wait up to subscriptionLockTimeout when ctx has no deadline")
	assert.Less(t, elapsed, svc.lockTimeout+30*time.Second,
		"waiter must not wait unbounded")
}
