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

func ptrTime(t time.Time) *time.Time { return &t }

type mockVPNClient struct {
	createCalled    bool
	deleteCalled    bool
	updateCalled    bool
	createError     error
	deleteError     error
	updateError     error
	createProvision vpn.SubscriptionProvision
	deleteProvision vpn.SubscriptionProvision
	updateProvision vpn.SubscriptionProvision
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
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
