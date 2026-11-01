package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/kereal/rs8kvn_bot/internal/xui"

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

func newTestSyncService(t *testing.T, db interfaces.SyncStore, nodes []database.Node) *SyncService {
	t.Helper()

	clients := make(NodeClients)
	for _, n := range nodes {
		clients[n.ID] = NodeAccess{Provision: &mockVPNClient{}}
	}

	return NewSyncService(db, clients, nodes)
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

	db := testutil.NewServiceStoreFake()
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

	svc := NewSyncService(db, NodeClients{}, []database.Node{*unknownNode})

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
	svc := NewSyncService(db, NodeClients{fetchNode.ID: {Provision: fetchClient}}, []database.Node{*fetchNode})

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
	clients := NodeClients{node1.ID: {Provision: mockVPN}}
	svc := NewSyncService(db, clients, []database.Node{*node1})

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
	clients := NodeClients{node1.ID: {Provision: mockVPN}}
	svc := NewSyncService(db, clients, []database.Node{*node1})

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
		svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mockVPN}}, []database.Node{*node1})

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
		svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mockVPN}}, []database.Node{*node1})

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
	clients := NodeClients{node1.ID: {Provision: mockVPN}}
	svc := NewSyncService(db, clients, []database.Node{*node1})

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
	svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mockVPN}}, []database.Node{*node1})

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
	svc := NewSyncService(db, NodeClients{nodeDue.ID: {Provision: dueClient}, nodeLater.ID: {Provision: laterClient}}, []database.Node{*nodeDue, *nodeLater})

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
	// A network timeout is retryable: the node climbs the ladder once.
	svc.handleSyncError(ctx, sn, &url.Error{Op: "Post", URL: "http://panel/api", Err: errors.New("i/o timeout")})

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].RetryCount)
	assert.Contains(t, *rows[0].LastError, "i/o timeout")
	assert.NotNil(t, rows[0].RetryAt)
}

// TestSyncService_handleSyncError_NonRetryableParksImmediately checks that an
// error the panel answered with (non-200) parks the node on the first hit:
// retrying cannot succeed, retry_at moves beyond every planning horizon, the
// pending status and the terminal cause stay visible.
func TestSyncService_handleSyncError_NonRetryableParksImmediately(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-park-nonretry", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-park-nonretry-node", IsActive: true, Host: "http://spnr", APIToken: "tspnr", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     7778,
		Username:       "syncparknonretry",
		ClientID:       "c-syncparknonretry",
		SubscriptionID: "s-syncparknonretry",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	sn := &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd, RetryCount: 0}
	require.NoError(t, db.CreateSubscriptionNode(ctx, sn))

	svc := NewSyncService(db, nil, []database.Node{*node1})
	svc.handleSyncError(ctx, sn, fmt.Errorf("upstream returned non-200: %w", utils.ErrNon200Response))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status, "parking keeps the pending status")
	assert.Equal(t, 0, rows[0].RetryCount, "non-retryable errors must not climb the ladder")
	require.NotNil(t, rows[0].RetryAt)
	assert.True(t, rows[0].RetryAt.After(time.Now().UTC().AddDate(1, 0, 0)), "parked retry_at is beyond every planning horizon")
	require.NotNil(t, rows[0].LastError)
	assert.Contains(t, *rows[0].LastError, utils.ErrNon200Response.Error())

	// The parked record is invisible to both pending scans.
	pending, err := db.GetPendingSync(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

// TestSyncService_handleSyncError_RetryableClimbsThenParks checks that a
// network timeout retries with backoff, and that once retry_count reaches
// maxSyncRetries the node parks instead of climbing forever.
func TestSyncService_handleSyncError_RetryableClimbsThenParks(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-park-climb", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-park-climb-node", IsActive: true, Host: "http://spc", APIToken: "tspc", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     7779,
		Username:       "syncparkclimb",
		ClientID:       "c-syncparkclimb",
		SubscriptionID: "s-syncparkclimb",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	sn := &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd, RetryCount: 0}
	require.NoError(t, db.CreateSubscriptionNode(ctx, sn))

	// A network timeout surfaces as url.Error, which implements net.Error.
	timeoutErr := &url.Error{Op: "Post", URL: "http://panel/api", Err: errors.New("i/o timeout")}
	require.True(t, utils.IsRetryable(timeoutErr), "precondition: url.Error timeout is retryable")

	svc := NewSyncService(db, nil, []database.Node{*node1})

	// Climb the ladder: each hit schedules a near-future retry.
	for range maxSyncRetries {
		svc.handleSyncError(ctx, sn, timeoutErr)

		rows, err := db.GetBySubscriptionID(ctx, sub.ID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.True(t, rows[0].RetryAt.Before(time.Now().UTC().Add(2*time.Hour)), "ladder retries stay in the near future")
	}

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, maxSyncRetries, rows[0].RetryCount)
	require.NotNil(t, rows[0].RetryAt)
	assert.True(t, rows[0].RetryAt.After(time.Now().UTC()) && rows[0].RetryAt.Before(time.Now().UTC().Add(2*time.Hour)), "ladder retries stay in the near future")

	// Next hit parks.
	svc.handleSyncError(ctx, sn, timeoutErr)

	rows, err = db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, maxSyncRetries, rows[0].RetryCount, "parking does not grow the counter")
	require.NotNil(t, rows[0].RetryAt)
	assert.True(t, rows[0].RetryAt.After(time.Now().UTC().AddDate(1, 0, 0)), "parked retry_at is beyond every planning horizon")

	pending, err := db.GetPendingSync(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

// TestSyncService_handleSyncError_ContextCancelDoesNotRetry checks R3: a
// caller-ctx cancellation aborts the scan and must not write retry metadata
// for the node.
func TestSyncService_handleSyncError_ContextCancelDoesNotRetry(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-ctx", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-ctx-node", IsActive: true, Host: "http://scn", APIToken: "tscn", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     7780,
		Username:       "syncctx",
		ClientID:       "c-syncctx",
		SubscriptionID: "s-syncctx",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	sn := &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingAdd, RetryCount: 0}
	require.NoError(t, db.CreateSubscriptionNode(ctx, sn))

	svc := NewSyncService(db, nil, []database.Node{*node1})
	svc.handleSyncError(ctx, sn, context.Canceled)
	svc.handleSyncError(ctx, sn, context.DeadlineExceeded)

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Zero(t, rows[0].RetryCount, "cancellation must not climb the ladder")
	assert.Nil(t, rows[0].RetryAt)
	assert.Nil(t, rows[0].LastError)
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
	svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mockVPN}}, []database.Node{*node1})

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

	mockVPN := &mockVPNClient{createError: fmt.Errorf("%w: %w", vpn.ErrSubscriptionAlreadyExists, fmt.Errorf("client already exists")), updateError: &url.Error{Op: "Post", URL: "http://panel/api", Err: errors.New("update refused")}}
	svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mockVPN}}, []database.Node{*node1})

	err = svc.SyncSubscription(ctx, sub.ID)
	require.NoError(t, err, "background-style sync: missing VPN client must not propagate")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)
	assert.Equal(t, 1, rows[0].RetryCount)
	assert.NotNil(t, rows[0].LastError)
	assert.Contains(t, *rows[0].LastError, "update refused")
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
	svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mockVPN}}, []database.Node{*node1})

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

	// net.OpError("connection refused") is retryable by the policy, so the
	// item ladders: RetryCount 1 and a near-future retry_at.
	mockVPN := &mockVPNClient{createError: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}
	svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mockVPN}}, []database.Node{*node1})

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

	svc := NewSyncService(db, NodeClients{}, []database.Node{*node1})

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

	svc := NewSyncService(db, NodeClients{}, []database.Node{*node1})

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
	svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mock}}, []database.Node{*node1})

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
	svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mock}}, []database.Node{*node1})

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

	svc := NewSyncService(db, NodeClients{}, []database.Node{*node1})

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
	svc := NewSyncService(db, NodeClients{}, nil)

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

// A revoked subscription must never have its plan bindings rebuilt: reconcile
// is the last step of SyncPendingNodes, so recreating pending_add rows there
// would make the next worker tick re-provision VPN clients and silently restore
// access for a user whose subscription was revoked (e.g. admin /del or
// broadcast deactivation beyond the immediate-sync budget). This exercises the
// full worker path, not just reconcile in isolation — the pending_remove row
// still deprovisions while reconcile must not re-add plan nodes.
func TestSyncService_SyncPendingNodes_RevokedSubscriptionNotReprovisioned(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-sync-revoked", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "sync-revoked-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://srev", APIToken: "trev", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     9991,
		Username:       "syncrevoked",
		ClientID:       "c-syncrevoked",
		SubscriptionID: "s-syncrevoked",
		Status:         string(database.SubscriptionStatusRevoked),
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node1.ID, Status: database.SyncStatusPendingRemove}))

	mockVPN := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node1.ID: {Provision: mockVPN}}, []database.Node{*node1})

	require.NoError(t, svc.SyncPendingNodes(ctx))

	assert.True(t, mockVPN.deleteCalled, "pending_remove must still deprovision the revoked subscription")
	assert.False(t, mockVPN.createCalled, "a revoked subscription must never be re-provisioned on a node")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "reconcile must not recreate pending_add bindings for a revoked subscription")
}

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

	// A retryable network failure on node one: the item ladders and the run
	// is still reported as degraded via the aggregate error.
	failedClient := &mockVPNClient{createError: &net.OpError{Op: "dial", Err: errors.New("node one unavailable")}}
	successClient := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{nodeOne.ID: {Provision: failedClient}, nodeTwo.ID: {Provision: successClient}}, []database.Node{*nodeOne, *nodeTwo})

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
	db := testutil.NewServiceStoreFake()
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

	clients := NodeClients{1: {Provision: &mockVPNClient{}}, 2: {Provision: &mockVPNClient{}}}
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
	assert.False(t, clients[1].Provision.(*mockVPNClient).createCalled)
	assert.False(t, clients[2].Provision.(*mockVPNClient).createCalled)
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

	db := testutil.NewServiceStoreFake()
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

	svc := NewSyncService(db, NodeClients{}, nil)

	err := svc.SyncPendingNodes(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, loadErr)
	assert.ErrorIs(t, err, reconcileErr)
}

func TestSyncService_lockSubscription_Timeout(t *testing.T) {
	t.Parallel()

	db := testutil.NewServiceStoreFake()
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

	db := testutil.NewServiceStoreFake()
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

func TestSyncService_ReconcilePlanNodes_RevokedIsUntouched(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"revoked", "active", "expired"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			planRead := false
			bindingsRead := false
			db := &testutil.ServiceStoreFake{
				GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
					return &database.Subscription{ID: 42, PlanID: 1, Status: status}, nil
				},
				GetNodesByPlanIDFunc: func(context.Context, uint) ([]database.Node, error) {
					planRead = true
					return []database.Node{{ID: 1, IsActive: true}, {ID: 2, IsActive: true}}, nil
				},
				GetBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
					bindingsRead = true
					return []database.SubscriptionNode{{SubscriptionID: 42, NodeID: 1, Status: database.SyncStatusPendingRemove}}, nil
				},
			}
			var reactivated, added bool
			db.UpdateSubscriptionNodeStatusFunc = func(_ context.Context, _ uint, nodeID uint, state database.SyncStatus) error {
				require.Equal(t, uint(1), nodeID)
				require.Equal(t, database.SyncStatusPendingAdd, state)
				reactivated = true
				return nil
			}
			db.UpsertSubscriptionNodeFunc = func(_ context.Context, sn *database.SubscriptionNode) error {
				require.Equal(t, uint(2), sn.NodeID)
				require.Equal(t, database.SyncStatusPendingAdd, sn.Status)
				added = true
				return nil
			}

			require.NoError(t, NewSyncService(db, nil, nil).ReconcilePlanNodes(context.Background(), 42))
			wantChanges := status != "revoked"
			require.Equal(t, wantChanges, planRead)
			require.Equal(t, wantChanges, bindingsRead)
			require.Equal(t, wantChanges, reactivated)
			require.Equal(t, wantChanges, added)
		})
	}
}

// TestSyncService_buildNodeProvision covers the single provision assembly in a
// table: paid create (product default), trial (never), update with a panel
// profile (preserved) and the readerless fallback (default).
func TestSyncService_buildNodeProvision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	expiresAt := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)

	paidPlan := &database.Plan{Name: "prov-build-paid", DevicesLimit: 1, TrafficLimit: 4096}
	trialPlan := &database.Plan{Name: database.TrialPlanName, DevicesLimit: 1, TrafficLimit: 1024}
	// Free plan: no traffic limit — no profile and no expiry are written.
	freePlan := &database.Plan{Name: "prov-build-free", DevicesLimit: 1, TrafficLimit: 0}

	panelProfile := xui.ClientConfig{
		Reset:           7,
		ResetDay:        3,
		ResetMax:        2,
		TrafficReset:    "weekly",
		TrafficResetDay: 5,
	}

	// panelAdminEdits simulate manual admin edits in the panel: reset interval
	// 7 days, weekly reset on Friday, capped renewals. The update path must
	// carry them into the provision untouched.
	withReader := func(profile xui.ClientConfig) NodeClients {
		reader := &testutil.XUIClient{}
		reader.GetClientByEmailFunc = func(_ context.Context, _ string) (*xui.ClientRecord, error) {
			return &xui.ClientRecord{Client: profile}, nil
		}

		return NodeClients{1: {Reader: reader}}
	}

	tests := []struct {
		name    string
		plan    *database.Plan
		clients NodeClients
		policy  profilePolicy
		check   func(t *testing.T, provision vpn.SubscriptionProvision)
	}{
		{
			name:    "paid_create_writes_default_profile",
			plan:    paidPlan,
			clients: NodeClients{1: {}},
			policy:  profileDefault,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, config.SubscriptionResetDay, provision.ResetDays)
				assert.Equal(t, 1, provision.TrafficResetDay)
				assert.Equal(t, 0, provision.ResetMax)
				assert.Equal(t, "monthly", provision.TrafficReset)
			},
		},
		{
			name:    "trial_create_writes_never_profile",
			plan:    trialPlan,
			clients: NodeClients{1: {}},
			policy:  profileDefault,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, 0, provision.ResetDays, "trial must not auto-renew")
				assert.Equal(t, "never", provision.TrafficReset, "trial quota must not renew")
				assert.Equal(t, 0, provision.ResetMax)
			},
		},
		{
			name:    "trial_update_overrides_panel_profile_with_never",
			plan:    trialPlan,
			clients: withReader(panelProfile),
			policy:  profilePreserve,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, 0, provision.ResetDays, "trial must not inherit the panel reset interval")
				assert.Equal(t, "never", provision.TrafficReset, "trial must never keep a panel reset cycle")
			},
		},
		{
			name:    "paid_update_preserves_panel_profile",
			plan:    paidPlan,
			clients: withReader(panelProfile),
			policy:  profilePreserve,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, 7, provision.ResetDays)
				assert.Equal(t, 3, provision.ResetDay)
				assert.Equal(t, 2, provision.ResetMax)
				assert.Equal(t, "weekly", provision.TrafficReset)
				assert.Equal(t, 5, provision.TrafficResetDay)
			},
		},
		{
			name:    "paid_update_without_reader_falls_back_to_default",
			plan:    paidPlan,
			clients: NodeClients{1: {}},
			policy:  profilePreserve,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, config.SubscriptionResetDay, provision.ResetDays)
				assert.Equal(t, "monthly", provision.TrafficReset)
			},
		},
		{
			name:    "free_plan_gets_no_profile_and_no_expiry",
			plan:    freePlan,
			clients: withReader(panelProfile),
			policy:  profilePreserve,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Zero(t, provision.ResetDays)
				assert.Empty(t, provision.TrafficReset)
				assert.True(t, provision.ExpiryTime.IsZero())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testutil.NewServiceStoreFake()
			db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
				return tt.plan, nil
			}

			sub := &database.Subscription{
				TelegramID:     4242,
				Username:       "provuser",
				ClientID:       "c-prov",
				SubscriptionID: "s-prov",
				Status:         "active",
				PlanID:         tt.plan.ID,
				ExpiresAt:      testutil.PtrTime(expiresAt),
			}

			svc := NewSyncService(db, tt.clients, nil)

			provision, err := svc.buildNodeProvision(ctx, sub, 1, tt.policy)
			require.NoError(t, err)

			// Common client fields are identical for both paths.
			assert.Equal(t, sub.ClientID, provision.ClientID)
			assert.Equal(t, sub.Username, provision.Username)
			assert.Equal(t, sub.SubscriptionID, provision.SubID)
			assert.Equal(t, tt.plan.TrafficLimit, provision.TrafficBytes)
			assert.Equal(t, sub.TelegramID, provision.TgID)
			if tt.plan.TrafficLimit > 0 {
				assert.Equal(t, expiresAt, provision.ExpiryTime, "existing expiry must be carried as-is")
			}

			tt.check(t, provision)
		})
	}
}

// TestSyncService_buildNodeProvision_AddUpdateParity checks that both paths
// build the identical client (golden comparison of the whole provision) when
// only the profile policy differs and the panel profile equals the default.
func TestSyncService_buildNodeProvision_AddUpdateParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := testutil.NewServiceStoreFake()

	expiresAt := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Minute)

	plan := &database.Plan{Name: "prov-parity", DevicesLimit: 2, TrafficLimit: 8192}
	db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
		return plan, nil
	}

	sub := &database.Subscription{
		TelegramID:     4243,
		Username:       "parityuser",
		ClientID:       "c-parity",
		SubscriptionID: "s-parity",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(expiresAt),
	}

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(_ context.Context, _ string) (*xui.ClientRecord, error) {
		return &xui.ClientRecord{Client: xui.ClientConfig{
			Reset:           config.SubscriptionResetDay,
			ResetMax:        0,
			TrafficReset:    "monthly",
			TrafficResetDay: 1,
		}}, nil
	}

	svc := NewSyncService(db, NodeClients{1: {Reader: reader}}, nil)

	addProvision, err := svc.buildNodeProvision(ctx, sub, 1, profileDefault)
	require.NoError(t, err)

	updateProvision, err := svc.buildNodeProvision(ctx, sub, 1, profilePreserve)
	require.NoError(t, err)

	assert.Equal(t, addProvision, updateProvision,
		"add and update must build the identical client when the panel profile matches the default")
}

// TestSyncService_buildNodeProvision_DefaultExpiryWhenNone checks the fallback
// expiry for a subscription without ExpiresAt: now (minute-truncated) plus
// config.SubscriptionResetDay.
func TestSyncService_buildNodeProvision_DefaultExpiryWhenNone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := testutil.NewServiceStoreFake()

	plan := &database.Plan{Name: "prov-default-expiry", DevicesLimit: 1, TrafficLimit: 1024}
	db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
		return plan, nil
	}

	sub := &database.Subscription{
		TelegramID:     4244,
		Username:       "noexpiry",
		ClientID:       "c-noexpiry",
		SubscriptionID: "s-noexpiry",
		Status:         "active",
		PlanID:         plan.ID,
	}

	svc := NewSyncService(db, NodeClients{1: {}}, nil)

	provision, err := svc.buildNodeProvision(ctx, sub, 1, profileDefault)
	require.NoError(t, err)

	want := time.Now().UTC().Truncate(time.Minute).AddDate(0, 0, config.SubscriptionResetDay)
	assert.True(t, provision.ExpiryTime.Equal(want),
		"fallback expiry must be minute-truncated now plus SubscriptionResetDay")
}

// TestSyncService_buildNodeProvision_UpdateMissingClient checks that the
// preserve policy fails with the sentinel vpn.ErrSubscriptionNotFound when the
// client is gone from the panel — without falling back to a default profile or
// a create.
func TestSyncService_buildNodeProvision_UpdateMissingClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := testutil.NewServiceStoreFake()

	plan := &database.Plan{Name: "prov-missing", DevicesLimit: 1, TrafficLimit: 1024}
	db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
		return plan, nil
	}

	sub := &database.Subscription{
		TelegramID:     4245,
		Username:       "missinguser",
		ClientID:       "c-missing",
		SubscriptionID: "s-missing",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().UTC().Add(24 * time.Hour)),
	}

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(_ context.Context, _ string) (*xui.ClientRecord, error) {
		return nil, xui.ErrClientNotFound
	}

	svc := NewSyncService(db, NodeClients{1: {Reader: reader}}, nil)

	_, err := svc.buildNodeProvision(ctx, sub, 1, profilePreserve)
	require.Error(t, err)
	assert.ErrorIs(t, err, vpn.ErrSubscriptionNotFound,
		"a missing panel client must surface the sentinel instead of a default profile")
	assert.NotErrorIs(t, err, vpn.ErrSubscriptionAlreadyExists)
}

// TestSyncService_buildNodeProvision_UpdateProfileReadError checks that a
// panel read failure (other than "not found") fails the build: an update must
// not silently overwrite manual admin edits with the default profile.
func TestSyncService_buildNodeProvision_UpdateProfileReadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := testutil.NewServiceStoreFake()

	plan := &database.Plan{Name: "prov-read-err", DevicesLimit: 1, TrafficLimit: 1024}
	db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
		return plan, nil
	}

	sub := &database.Subscription{
		TelegramID:     4246,
		Username:       "readerruser",
		ClientID:       "c-readerr",
		SubscriptionID: "s-readerr",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().UTC().Add(24 * time.Hour)),
	}

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(_ context.Context, _ string) (*xui.ClientRecord, error) {
		return nil, errors.New("panel unreachable")
	}

	svc := NewSyncService(db, NodeClients{1: {Reader: reader}}, nil)

	_, err := svc.buildNodeProvision(ctx, sub, 1, profilePreserve)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read panel profile")
}

// TestSyncService_SyncSubscription_PendingUpdate_PreservesPanelProfile runs the
// full update path end-to-end: a plan change must not erase non-default admin
// edits of the auto-renew profile on the panel.
func TestSyncService_SyncSubscription_PendingUpdate_PreservesPanelProfile(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "prov-e2e-preserve", IsActive: true, DevicesLimit: 1, TrafficLimit: 2048}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node := &database.Node{Name: "prov-preserve-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://pp", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	expiresAt := time.Now().UTC().Add(12 * time.Hour).Truncate(time.Minute)
	sub := &database.Subscription{TelegramID: 994002, Username: "preserve-user", ClientID: "preserve-client", SubscriptionID: "preserve-sub", Status: "active", PlanID: plan.ID, ExpiresAt: &expiresAt}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate}))

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(_ context.Context, email string) (*xui.ClientRecord, error) {
		require.Equal(t, XUIEmail(sub.Username, sub.TelegramID), email)

		return &xui.ClientRecord{Client: xui.ClientConfig{
			Reset:           14,
			ResetDay:        20,
			ResetMax:        5,
			TrafficReset:    "daily",
			TrafficResetDay: 15,
		}}, nil
	}

	mockVPN := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: mockVPN, Reader: reader}}, []database.Node{*node})
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	require.True(t, mockVPN.updateCalled)
	assert.Equal(t, 14, mockVPN.updateProvision.ResetDays, "admin-edited reset interval must survive the update")
	assert.Equal(t, 20, mockVPN.updateProvision.ResetDay)
	assert.Equal(t, 5, mockVPN.updateProvision.ResetMax)
	assert.Equal(t, "daily", mockVPN.updateProvision.TrafficReset, "admin-edited reset period must survive the update")
	assert.Equal(t, 15, mockVPN.updateProvision.TrafficResetDay)
	assert.Equal(t, plan.TrafficLimit, mockVPN.updateProvision.TrafficBytes)
	assert.Equal(t, expiresAt, mockVPN.updateProvision.ExpiryTime)

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
}

// TestSyncService_SyncSubscription_PendingUpdate_MissingClientFallsBackToCreate
// checks the full update path when the client is gone from the panel: the item
// is re-queued as pending_add (create path) in the same pass — no update
// attempted, no retry ladder, next worker pass provisions the client.
func TestSyncService_SyncSubscription_PendingUpdate_MissingClientFallsBackToCreate(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "prov-e2e-missing", IsActive: true, DevicesLimit: 1, TrafficLimit: 2048}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node := &database.Node{Name: "prov-missing-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://pm", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &database.Subscription{TelegramID: 994003, Username: "gone-user", ClientID: "gone-client", SubscriptionID: "gone-sub", Status: "active", PlanID: plan.ID, ExpiresAt: testutil.PtrTime(time.Now().UTC().Add(12 * time.Hour))}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate}))

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(context.Context, string) (*xui.ClientRecord, error) {
		return nil, xui.ErrClientNotFound
	}

	mockVPN := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: mockVPN, Reader: reader}}, []database.Node{*node})

	// SyncSubscription swallows per-node failures by contract (best-effort
	// external sync); the outcome is observable in the node status.
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))
	assert.False(t, mockVPN.updateCalled, "update must not run without the panel profile")
	assert.False(t, mockVPN.createCalled, "create runs on the next pass, not in the same one")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status, "the item must be re-queued for create")
	assert.Zero(t, rows[0].RetryCount, "the status transition resets retry metadata")
	assert.Nil(t, rows[0].RetryAt)
	assert.Nil(t, rows[0].LastError)
}

// TestSyncService_SyncSubscription_PendingUpdate_MissingClientThenCreate
// runs the fallback end-to-end: after the pending_add re-queue the next pass
// creates the client and marks the node active.
func TestSyncService_SyncSubscription_PendingUpdate_MissingClientThenCreate(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "prov-e2e-fallback", IsActive: true, DevicesLimit: 1, TrafficLimit: 2048}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node := &database.Node{Name: "prov-fallback-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://pf", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &database.Subscription{TelegramID: 994004, Username: "fb-user", ClientID: "fb-client", SubscriptionID: "fb-sub", Status: "active", PlanID: plan.ID, ExpiresAt: testutil.PtrTime(time.Now().UTC().Add(12 * time.Hour))}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate}))

	clientGone := true
	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(context.Context, string) (*xui.ClientRecord, error) {
		if clientGone {
			return nil, xui.ErrClientNotFound
		}

		return &xui.ClientRecord{Client: xui.ClientConfig{Reset: 30, TrafficReset: "monthly", TrafficResetDay: 1}}, nil
	}

	mockVPN := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: mockVPN, Reader: reader}}, []database.Node{*node})

	// Pass 1: update discovers the missing client, re-queues pending_add.
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)

	// Pass 2: the create path provisions the client fresh.
	clientGone = false
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	assert.True(t, mockVPN.createCalled, "the re-queued node must be provisioned by the create path")

	rows, err = db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
	assert.Zero(t, rows[0].RetryCount)
}

func TestSyncService_RevokedPendingProvisioningIsRemoved(t *testing.T) {
	t.Parallel()

	for _, status := range []database.SyncStatus{database.SyncStatusPendingAdd, database.SyncStatusPendingUpdate} {
		for _, background := range []bool{false, true} {
			name := string(status) + "/immediate"
			if background {
				name = string(status) + "/background"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				ctx := context.Background()
				db, err := testutil.NewTestDatabaseService(t)
				require.NoError(t, err)
				node := &database.Node{Name: "revoked-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://revoked", InboundIDs: `[1]`}
				require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
				plan := &database.Plan{Name: "revoked-plan", DevicesLimit: 1}
				require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)
				sub := &database.Subscription{
					TelegramID: 9201, Username: "revoked-user", ClientID: "revoked-client",
					SubscriptionID: "revoked-sub", Status: string(database.SubscriptionStatusRevoked), PlanID: plan.ID,
				}
				require.NoError(t, db.CreateSubscription(ctx, sub, ""))
				due := time.Now().UTC().Add(-time.Minute)
				require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
					SubscriptionID: sub.ID, NodeID: node.ID, Status: status, RetryCount: 4, RetryAt: &due,
				}))

				// A transient network failure at deletion: retryable by the
				// policy, so the removal ladders (fresh retry sequence from 0).
				deleteErr := &net.OpError{Op: "dial", Err: errors.New("VPN node temporarily unavailable")}
				client := &mockVPNClient{deleteError: deleteErr}
				// Removal must use the current DB node even without a runtime snapshot.
				svc := NewSyncService(db, NodeClients{node.ID: {Provision: client}}, nil)
				sync := func() error {
					if background {
						return svc.SyncPendingNodes(ctx)
					}
					return svc.SyncSubscription(ctx, sub.ID)
				}
				err = sync()
				if background {
					require.ErrorIs(t, err, deleteErr)
				} else {
					require.NoError(t, err, "immediate external sync remains best-effort")
				}
				assert.True(t, client.deleteCalled)
				assert.Equal(t, sub.ClientID, client.deleteProvision.ClientID)
				assert.False(t, client.createCalled)
				assert.False(t, client.updateCalled)
				assert.False(t, client.resetTrafficCalled)

				bindings, err := db.GetBySubscriptionID(ctx, sub.ID)
				require.NoError(t, err)
				require.Len(t, bindings, 1)
				assert.Equal(t, database.SyncStatusPendingRemove, bindings[0].Status)
				assert.Equal(t, 1, bindings[0].RetryCount, "removal starts a fresh retry sequence")
				require.NotNil(t, bindings[0].RetryAt)
				require.NotNil(t, bindings[0].LastError)
				assert.Contains(t, *bindings[0].LastError, deleteErr.Error())

				// Make the persisted deletion due and let the next scan finish it.
				require.NoError(t, db.UpdateRetry(ctx, sub.ID, node.ID, bindings[0].RetryCount, &due, bindings[0].LastError))
				client.deleteError = nil
				client.deleteCalled = false
				require.NoError(t, sync())
				assert.True(t, client.deleteCalled)
				assert.False(t, client.createCalled)
				assert.False(t, client.updateCalled)
				bindings, err = db.GetBySubscriptionID(ctx, sub.ID)
				require.NoError(t, err)
				assert.Empty(t, bindings)
				stored, err := db.GetByID(ctx, sub.ID)
				require.NoError(t, err)
				assert.Equal(t, string(database.SubscriptionStatusRevoked), stored.Status)
			})
		}
	}
}

func TestSyncService_RevokedRemovalQueueFailure(t *testing.T) {
	t.Parallel()

	for _, queueErr := range []error{errors.New("DB write failed"), context.Canceled, context.DeadlineExceeded} {
		t.Run(queueErr.Error(), func(t *testing.T) {
			t.Parallel()

			var queued, retried, deleted []uint
			db := testutil.NewServiceStoreFake()
			db.UpdateSubscriptionNodeStatusFunc = func(_ context.Context, _ uint, nodeID uint, status database.SyncStatus) error {
				assert.Equal(t, database.SyncStatusPendingRemove, status)
				queued = append(queued, nodeID)
				if nodeID == 1 {
					return queueErr
				}
				return nil
			}
			db.UpdateRetryFunc = func(_ context.Context, _, nodeID uint, _ int, _ *time.Time, _ *string) error {
				retried = append(retried, nodeID)
				return nil
			}
			db.GetNodeByIDFunc = func(_ context.Context, nodeID uint) (*database.Node, error) {
				assert.Equal(t, uint(2), nodeID, "a failed queue write must prevent external sync")
				return &database.Node{ID: nodeID, IsActive: true}, nil
			}
			db.DeleteSubscriptionNodeFunc = func(_ context.Context, _, nodeID uint) error {
				deleted = append(deleted, nodeID)
				return nil
			}
			failedClient, nextClient := &mockVPNClient{}, &mockVPNClient{}
			svc := NewSyncService(db, NodeClients{1: {Provision: failedClient}, 2: {Provision: nextClient}}, []database.Node{
				{ID: 1, Type: database.NodeType3xUI}, {ID: 2, Type: database.NodeType3xUI},
			})
			sub := &database.Subscription{ID: 42, Status: string(database.SubscriptionStatusRevoked)}
			err := svc.syncNodes(context.Background(), sub, []database.SubscriptionNode{
				{SubscriptionID: sub.ID, NodeID: 1, Status: database.SyncStatusPendingAdd},
				{SubscriptionID: sub.ID, NodeID: 2, Status: database.SyncStatusPendingUpdate},
			})
			require.ErrorIs(t, err, queueErr)
			assert.False(t, failedClient.createCalled)
			assert.False(t, failedClient.updateCalled)
			assert.False(t, failedClient.deleteCalled)
			assert.False(t, nextClient.createCalled)
			assert.False(t, nextClient.updateCalled)
			if errors.Is(queueErr, context.Canceled) || errors.Is(queueErr, context.DeadlineExceeded) {
				assert.Equal(t, []uint{1}, queued)
				assert.Empty(t, retried)
				assert.Empty(t, deleted)
				assert.False(t, nextClient.deleteCalled)
			} else {
				assert.Equal(t, []uint{1, 2}, queued)
				assert.Equal(t, []uint{1}, retried)
				assert.Equal(t, []uint{2}, deleted)
				assert.True(t, nextClient.deleteCalled)
			}
		})
	}
}

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
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: client}}, []database.Node{*node})
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
	assert.Equal(t, 0, rows[0].RetryCount)
	require.True(t, client.updateCalled)
	assert.Equal(t, sub.ClientID, client.updateProvision.ClientID)
	assert.Equal(t, XUIEmail(sub.Username, sub.TelegramID), client.updateProvision.Username)
	assert.Equal(t, XUIEmail(sub.Username, sub.TelegramID), client.updateProvision.CurrentEmail, "CurrentEmail must identify the existing client on the panel")
	assert.Equal(t, sub.SubscriptionID, client.updateProvision.SubID)
	assert.Equal(t, sub.TelegramID, client.updateProvision.TgID, "update must carry the Telegram id")
	assert.Equal(t, plan.TrafficLimit, client.updateProvision.TrafficBytes)
	// Node has no panel reader in this setup, so the update path writes the
	// product default profile explicitly (no readerless -1 sentinel anymore).
	assert.Equal(t, config.SubscriptionResetDay, client.updateProvision.ResetDays)
	assert.Equal(t, "monthly", client.updateProvision.TrafficReset)
	assert.Equal(t, 1, client.updateProvision.TrafficResetDay)
	assert.Equal(t, 0, client.updateProvision.ResetMax)
	assert.Equal(t, expiresAt, client.updateProvision.ExpiryTime)
	assert.True(t, client.resetTrafficCalled, "ResetTraffic must be called after UpdateSubscription")
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

	svc := NewSyncService(db, NodeClients{}, []database.Node{*oldNode, *targetNode})
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

// TestSyncService_PendingUpdate_FreeToPremium_ResetTraffic verifies that
// upgrading from free (TrafficLimit=1GB) to premium (TrafficLimit=0, unlimited)
// calls ResetTraffic so the panel clears the free-tier usage.
func TestSyncService_PendingUpdate_FreeToPremium_ResetTraffic(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	premiumPlan := &database.Plan{Name: "premium", IsActive: true, DevicesLimit: 2, TrafficLimit: 0}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(premiumPlan).Error)

	node := &database.Node{Name: "upgrade-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://up", APIToken: "tok", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: premiumPlan.ID, NodeID: node.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     995001,
		Username:       "upgrade-user",
		ClientID:       "upgrade-client",
		SubscriptionID: "upgrade-sub",
		Status:         "active",
		PlanID:         premiumPlan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().UTC().Add(30 * 24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
		SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate,
	}))

	client := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: client}}, []database.Node{*node})
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)

	// UpdateSubscription must be called with unlimited traffic (totalGB=0)
	require.True(t, client.updateCalled)
	assert.Equal(t, int64(0), client.updateProvision.TrafficBytes, "premium must have unlimited traffic")

	// ResetTraffic must be called to clear free-tier usage
	assert.True(t, client.resetTrafficCalled, "ResetTraffic must be called on free→premium upgrade")
}

// TestSyncService_PendingUpdate_PremiumToFree_ResetTraffic verifies that
// downgrading from premium (TrafficLimit=0) to free (TrafficLimit=1GB) on a
// shared node calls ResetTraffic so the panel clears premium usage and the
// user is not blocked by the 1GB limit.
func TestSyncService_PendingUpdate_PremiumToFree_ResetTraffic(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	freePlan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	node := &database.Node{Name: "downgrade-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://down", APIToken: "tok", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: freePlan.ID, NodeID: node.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     995002,
		Username:       "downgrade-user",
		ClientID:       "downgrade-client",
		SubscriptionID: "downgrade-sub",
		Status:         "active",
		PlanID:         freePlan.ID,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
		SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate,
	}))

	client := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: client}}, []database.Node{*node})
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)

	// UpdateSubscription must be called with 1GB limit
	require.True(t, client.updateCalled)
	assert.Equal(t, freePlan.TrafficLimit, client.updateProvision.TrafficBytes, "free plan traffic limit must match")

	// ResetTraffic must be called to clear premium usage
	assert.True(t, client.resetTrafficCalled, "ResetTraffic must be called on premium→free downgrade")
}
