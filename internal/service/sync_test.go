package service

import (
	"context"
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
	createCalled   bool
	deleteCalled   bool
	createError    error
	deleteError    error
	createSubID    string
	createUsername string
	deleteSubID    string
	deleteUsername string
}

func (m *mockVPNClient) CreateSubscription(ctx context.Context, uuid, username string) error {
	m.createCalled = true
	m.createSubID = uuid
	m.createUsername = username
	return m.createError
}

func (m *mockVPNClient) DeleteSubscription(ctx context.Context, uuid, username string) error {
	m.deleteCalled = true
	m.deleteSubID = uuid
	m.deleteUsername = username
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

func TestSyncService_RecalculateNodes_AddMissing(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	ctx := context.Background()

	plan := &database.Plan{Name: "test-plan-recalc", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node1 := &database.Node{Name: "rec-node-1", IsActive: true, Host: "http://r1", APIToken: "t1", InboundIDs: `[1]`}
	node2 := &database.Node{Name: "rec-node-2", IsActive: true, Host: "http://r2", APIToken: "t2", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node1).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node2).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node2.ID}).Error)

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

	svc := newTestSyncService(t, db, []database.Node{*node1, *node2})
	require.NoError(t, svc.RecalculateNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	statusMap := make(map[uint]database.SyncStatus)
	for _, r := range rows {
		statusMap[r.NodeID] = r.Status
	}
	assert.Equal(t, database.SyncStatusActive, statusMap[node1.ID])
	assert.Equal(t, database.SyncStatusPendingAdd, statusMap[node2.ID])
}

func TestSyncService_RecalculateNodes_RemoveExtra(t *testing.T) {
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
	require.NoError(t, svc.RecalculateNodes(ctx, sub.ID))

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

func TestSyncService_RecalculateNodes_KeepExisting(t *testing.T) {
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
	require.NoError(t, svc.RecalculateNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
}

func TestSyncService_RecalculateNodes_ReactivatePendingRemove(t *testing.T) {
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
	require.NoError(t, svc.RecalculateNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)
}

func TestSyncService_RecalculateNodes_RemovesStalePendingAdd(t *testing.T) {
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
	require.NoError(t, svc.RecalculateNodes(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, node1.ID, rows[0].NodeID)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
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
	assert.Equal(t, sub.ClientID, mockVPN.createSubID)
	assert.Equal(t, sub.Username, mockVPN.createUsername)
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
	assert.Equal(t, sub.ClientID, mockVPN.deleteSubID)
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
	assert.Equal(t, "tgId_777000", mockVPN.createUsername)
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
		{"retry 1 -> 5m", 1, 5 * time.Minute, 5*time.Minute + time.Minute},
		{"retry 2 -> 15m", 2, 15 * time.Minute, 15*time.Minute + time.Minute},
		{"retry 3 -> 1h", 3, 1 * time.Hour, 1*time.Hour + time.Minute},
		{"retry 4 -> 6h", 4, 6 * time.Hour, 6*time.Hour + time.Minute},
		{"retry 10 -> 6h", 10, 6 * time.Hour, 6*time.Hour + time.Minute},
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

func TestIsAlreadyExistsError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"already exists", fmt.Errorf("client already exists"), true},
		{"duplicate", fmt.Errorf("duplicate key error"), true},
		{"already added", fmt.Errorf("already added to inbound"), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlreadyExistsError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"not found", fmt.Errorf("client not found"), true},
		{"does not exist", fmt.Errorf("resource does not exist"), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundError(tt.err)
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

	mockVPN := &mockVPNClient{createError: fmt.Errorf("client already exists")}
	svc := NewSyncService(db, map[uint]vpn.Client{node1.ID: mockVPN}, []database.Node{*node1})

	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status, "should mark active when client already exists")
	assert.Equal(t, 0, rows[0].RetryCount, "should not increment retry count")
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

	mockVPN := &mockVPNClient{deleteError: fmt.Errorf("client not found")}
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
	assert.Error(t, err)

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)
	assert.Equal(t, 1, rows[0].RetryCount)
}
