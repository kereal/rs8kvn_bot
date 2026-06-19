package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== Node Lifecycle Tests ====================

func TestListNodes_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	sources, err := svc.ListNodes(context.Background())
	assert.NoError(t, err)
	assert.Len(t, sources, 0)
}

func TestSeedDefaultNode_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.SeedDefaultNode(ctx, "main", "http://xui:2053", "token-abc", []int{1}, "https://sub.example.com")
	require.NoError(t, err)

	sources, err := svc.ListNodes(ctx)
	require.NoError(t, err)
	require.Len(t, sources, 1)

	mainSrc := &sources[0]
	assert.Equal(t, "main", mainSrc.Name)
	assert.Equal(t, "http://xui:2053", mainSrc.Host)
	assert.Equal(t, "token-abc", mainSrc.APIToken)
	inboundIDs, parseErr := mainSrc.GetInboundIDs()
	require.NoError(t, parseErr)
	assert.Equal(t, []int{1}, inboundIDs)
	assert.Equal(t, "https://sub.example.com", mainSrc.SubscriptionURL)
	assert.Equal(t, NodeType3xUI, mainSrc.Type)
	assert.True(t, mainSrc.IsActive)
}

func TestIsNodesEmpty_TrueAndFalse(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	empty, err := svc.IsNodesEmpty(ctx)
	require.NoError(t, err)
	assert.True(t, empty, "no node seeded after NewService")

	require.NoError(t, svc.SeedDefaultNode(ctx, "main", "http://xui:2053", "token-abc", []int{1}, "https://sub.example.com"))
	empty, err = svc.IsNodesEmpty(ctx)
	require.NoError(t, err)
	assert.False(t, empty, "nodes should not be empty after seeding")
}

// ==================== Plan Tests ====================

func TestGetPlanByName_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	plan, err := svc.GetPlanByName(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, plan)
}

func TestGetPlanByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	plan, err := svc.GetPlanByID(context.Background(), 9999)
	assert.Error(t, err)
	assert.Nil(t, plan)
}

func TestGetPlanByID_AfterCreate(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{
		Name:         "pro",
		DevicesLimit: 3,
		TrafficLimit: 50 * 1024 * 1024 * 1024,
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	require.NotZero(t, plan.ID)

	got, err := svc.GetPlanByID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, "pro", got.Name)
	assert.Equal(t, 3, got.DevicesLimit)
}

func TestGetPlanByName_AfterCreate(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "vip", DevicesLimit: 5, TrafficLimit: 100 * 1024 * 1024 * 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	got, err := svc.GetPlanByName(ctx, "vip")
	require.NoError(t, err)
	assert.Equal(t, plan.ID, got.ID)
	assert.Equal(t, "vip", got.Name)
}

func TestGetPlanByName_DefaultTrialPlan(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	got, err := svc.GetPlanByName(context.Background(), TrialPlanName)
	require.NoError(t, err)
	assert.Equal(t, TrialPlanName, got.Name)
	assert.Equal(t, 1, got.DevicesLimit)
}

func TestGetPlanByName_DefaultFreePlan(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	got, err := svc.GetPlanByName(context.Background(), FreePlanName)
	require.NoError(t, err)
	assert.Equal(t, FreePlanName, got.Name)
}

// ==================== GetNodesByPlanName Tests ====================

func TestGetNodesByPlanName_NoLinks(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	sources, err := svc.GetNodesByPlanName(context.Background(), "nonexistent_plan")
	assert.NoError(t, err)
	assert.Empty(t, sources)

	trialNodes, err := svc.GetNodesByPlanName(context.Background(), TrialPlanName)
	assert.NoError(t, err)
	assert.Empty(t, trialNodes)
}

func TestGetNodesByPlanName_ReturnsLinkedNodes(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	require.NoError(t, svc.SeedDefaultNode(ctx, "primary", "http://x1", "t1", []int{1}, ""))
	require.NoError(t, svc.SeedDefaultNode(ctx, "backup", "http://x2", "t2", []int{1}, ""))

	linked, err := svc.GetNodesByPlanName(ctx, "trial")
	require.NoError(t, err)
	assert.Len(t, linked, 2)

	names := []string{linked[0].Name, linked[1].Name}
	assert.Contains(t, names, "primary")
	assert.Contains(t, names, "backup")
}

func TestGetNodesByPlanName_FilterByName(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	require.NoError(t, svc.SeedDefaultNode(ctx, "default", "http://x1", "t1", []int{1}, ""))

	trialNodes, err := svc.GetNodesByPlanName(ctx, "trial")
	require.NoError(t, err)
	assert.Len(t, trialNodes, 1)
	assert.Equal(t, "default", trialNodes[0].Name)

	freeNodes, err := svc.GetNodesByPlanName(ctx, "free")
	require.NoError(t, err)
	assert.Len(t, freeNodes, 1)
	assert.Equal(t, "default", freeNodes[0].Name)
}

// ==================== Subscription Active Check (model) ====================

func TestSubscription_IsActive_StatusCases(t *testing.T) {
	t.Parallel()

	now := time.Now()
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	tests := []struct {
		name       string
		status     string
		expiryTime time.Time
		want       bool
	}{
		{"active + future expiry", "active", future, true},
		{"active + past expiry", "active", past, false},
		{"active + zero expiry", "active", time.Time{}, true},
		{"revoked + future expiry", "revoked", future, false},
		{"expired + future expiry", "expired", future, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &Subscription{Status: tt.status, ExpiresAt: tt.expiryTime}
			assert.Equal(t, tt.want, sub.IsActive())
		})
	}
}

// ==================== Node Repository Tests ====================

func TestNodeRepository_GetNodeByID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := &Node{Name: "get-by-id-node", IsActive: true, Host: "http://get", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(node).Error)

	got, err := svc.GetNodeByID(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, node.ID, got.ID)
	assert.Equal(t, "get-by-id-node", got.Name)
	assert.Equal(t, NodeType3xUI, got.Type)
}

func TestNodeRepository_GetNodeByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetNodeByID(ctx, 99999)
	assert.Error(t, err)
}

func TestNodeRepository_ListEnabled(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	activeNode := &Node{Name: "active-node", IsActive: true, Host: "http://a", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(activeNode).Error)
	inactiveNode := &Node{Name: "inactive-node", Host: "http://i", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(inactiveNode).Error)
	require.NoError(t, svc.db.WithContext(ctx).Model(inactiveNode).Updates(map[string]interface{}{"is_active": false}).Error)

	nodes, err := svc.ListEnabled(ctx)
	require.NoError(t, err)
	assert.Len(t, nodes, 1)
	assert.Equal(t, "active-node", nodes[0].Name)
	assert.True(t, nodes[0].IsActive)
}

func TestNodeRepository_ListEnabled_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	nodes, err := svc.ListEnabled(ctx)
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestNodeRepository_GetNodesByPlanID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-by-id-nodes", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node1 := &Node{Name: "plan-node-1", IsActive: true, Host: "http://p1", APIToken: "t1", InboundIDs: `[1]`}
	node2 := &Node{Name: "plan-node-2", IsActive: true, Host: "http://p2", APIToken: "t2", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(node1).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(node2).Error)

	inactiveNode := &Node{Name: "inactive-plan-node", Host: "http://ip", APIToken: "t3", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(inactiveNode).Error)
	require.NoError(t, svc.db.WithContext(ctx).Model(inactiveNode).Updates(map[string]interface{}{"is_active": false}).Error)

	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node1.ID}).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: node2.ID}).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: inactiveNode.ID}).Error)

	nodes, err := svc.GetNodesByPlanID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Len(t, nodes, 2)

	names := []string{nodes[0].Name, nodes[1].Name}
	assert.Contains(t, names, "plan-node-1")
	assert.Contains(t, names, "plan-node-2")
}

func TestNodeRepository_GetNodesByPlanID_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-empty-nodes", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	nodes, err := svc.GetNodesByPlanID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Empty(t, nodes)
}
