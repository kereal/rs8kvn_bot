package database

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedNode создаёт валидную 3x-ui-ноду через админский путь.
func seedNode(t *testing.T, svc *Service, name string) Node {
	t.Helper()

	node := &Node{
		Name:            name,
		Type:            NodeType3xUI,
		IsActive:        true,
		Host:            "http://203.0.113.10:2053",
		APIToken:        "token-" + name,
		InboundIDs:      "[1]",
		SubscriptionURL: "https://sub.example.com/s/",
	}
	require.NoError(t, svc.CreateNodeAdmin(context.Background(), node))

	return *node
}

// seedAdminPlan создаёт план напрямую (ботовский CreatePlan без аудита).
func seedAdminPlan(t *testing.T, svc *Service, name string) Plan {
	t.Helper()

	plan := &Plan{Name: name, IsActive: true, DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.Create(plan).Error)

	return *plan
}

// nodeEvents возвращает события аудита ноды.
func nodeEvents(t *testing.T, svc *Service, nodeID uint, event string) []AuditLog {
	t.Helper()

	id := ""
	if nodeID > 0 {
		id = intToString(nodeID)
	}

	rows, err := svc.GetAuditLogByEntity(context.Background(), EntityNode, id, 50)
	require.NoError(t, err)

	var matched []AuditLog
	for _, row := range rows {
		if row.Event == event {
			matched = append(matched, row)
		}
	}

	return matched
}

func intToString(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func TestCreateNodeAdmin_Table(t *testing.T) {
	t.Parallel()

	valid := Node{
		Name:            "main-panel",
		Type:            NodeType3xUI,
		IsActive:        true,
		Host:            "http://203.0.113.10:2053",
		APIToken:        "secret-token",
		InboundIDs:      "[1,3]",
		SubscriptionURL: "https://sub.example.com/s/",
	}

	tests := []struct {
		name    string
		mutate  func(n *Node)
		wantErr bool
	}{
		{name: "valid 3x-ui", mutate: func(n *Node) {}},
		{
			name: "fetch without token and host",
			mutate: func(n *Node) {
				n.Type = NodeTypeFetch
				n.Host = ""
				n.APIToken = ""
				n.InboundIDs = "[]"
			},
		},
		{
			name: "proxman with host",
			mutate: func(n *Node) {
				n.Type = NodeTypeProxman
				n.APIToken = ""
				n.InboundIDs = "[]"
			},
		},
		{
			name:    "empty name",
			mutate:  func(n *Node) { n.Name = "   " },
			wantErr: true,
		},
		{
			name:    "name over 100 runes",
			mutate:  func(n *Node) { n.Name = makeStringOfRunes(101, 'н') },
			wantErr: true,
		},
		{
			name:    "empty subscription_url",
			mutate:  func(n *Node) { n.SubscriptionURL = "" },
			wantErr: true,
		},
		{
			name:    "ftp subscription_url",
			mutate:  func(n *Node) { n.SubscriptionURL = "ftp://sub.example.com/s/" },
			wantErr: true,
		},
		{
			name:    "unknown type",
			mutate:  func(n *Node) { n.Type = "marzban" },
			wantErr: true,
		},
		{
			name:    "3x-ui without host",
			mutate:  func(n *Node) { n.Host = "" },
			wantErr: true,
		},
		{
			name:    "host without scheme",
			mutate:  func(n *Node) { n.Host = "203.0.113.10:2053" },
			wantErr: true,
		},
		{
			name:    "broken inbound_ids json",
			mutate:  func(n *Node) { n.InboundIDs = `["a"]` },
			wantErr: true,
		},
		{
			name:    "negative inbound id",
			mutate:  func(n *Node) { n.InboundIDs = "[-1]" },
			wantErr: true,
		},
		{
			name:    "null inbound_ids json",
			mutate:  func(n *Node) { n.InboundIDs = "null" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestService(t)
			ctx := context.Background()

			node := valid
			tt.mutate(&node)

			err := svc.CreateNodeAdmin(ctx, &node)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrNodeInvalid)

				return
			}

			require.NoError(t, err)
			require.NotZero(t, node.ID)

			stored, err := svc.GetNodeByID(ctx, node.ID)
			require.NoError(t, err)
			assert.Equal(t, node.Name, stored.Name)
			assert.Equal(t, node.Type, stored.Type)
			assert.Equal(t, node.InboundIDs, stored.InboundIDs)

			events := nodeEvents(t, svc, node.ID, AuditEventNodeCreated)
			require.Len(t, events, 1)
			assert.Equal(t, string(ActorAdmin), string(events[0].ActorType))

			var snapshot map[string]any
			require.NoError(t, json.Unmarshal([]byte(*events[0].NewState), &snapshot))
			assert.NotContains(t, snapshot, "api_token", "токен не попадает в аудит")
			assert.Equal(t, node.APIToken != "", snapshot["has_api_token"])
		})
	}
}

// makeStringOfRunes строит строку из count одинаковых символов.
func makeStringOfRunes(count int, r rune) string {
	return strings.Repeat(string(r), count)
}

// seedPlanNode пишет строку plan_nodes напрямую (мимо админского API).
func seedPlanNode(t *testing.T, svc *Service, planID, nodeID uint) {
	t.Helper()

	require.NoError(t, svc.db.Create(&PlanNode{PlanID: planID, NodeID: nodeID}).Error)
}

func TestUpdateNodeAdmin_FieldsAndAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "panel-a")
	before := node

	node.Name = "panel-a-renamed"
	node.Host = "http://203.0.113.20:2053"
	node.InboundIDs = "[2,5]"
	node.IsActive = false

	require.NoError(t, svc.UpdateNodeAdmin(ctx, &node))

	stored, err := svc.GetNodeByID(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, "panel-a-renamed", stored.Name)
	assert.Equal(t, "http://203.0.113.20:2053", stored.Host)
	assert.Equal(t, "[2,5]", stored.InboundIDs)
	assert.False(t, stored.IsActive)

	events := nodeEvents(t, svc, node.ID, AuditEventNodeUpdated)
	require.Len(t, events, 1)

	var oldState, newState map[string]any
	require.NoError(t, json.Unmarshal([]byte(*events[0].OldState), &oldState))
	require.NoError(t, json.Unmarshal([]byte(*events[0].NewState), &newState))
	assert.Equal(t, before.Name, oldState["name"])
	assert.Equal(t, node.Name, newState["name"])
	assert.NotContains(t, oldState, "api_token")
	assert.NotContains(t, newState, "api_token")
}

func TestUpdateNodeAdmin_PreservesCreatedAt(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "panel-created")
	original := node.CreatedAt.Truncate(time.Second).UTC()

	// Хендлер собирает структуру начисто (CreatedAt = time.Time{}), поэтому
	// Save-путь затирал созданную строку нулём. Обновление через Updates(map)
	// не трогает created_at.
	fresh := Node{
		ID:              node.ID,
		Name:            "panel-created",
		Type:            NodeType3xUI,
		Host:            "http://203.0.113.11:2053",
		SubscriptionURL: "https://sub.example.com/s/",
		InboundIDs:      "[]",
		IsActive:        true,
	}

	require.NoError(t, svc.UpdateNodeAdmin(ctx, &fresh))

	stored, err := svc.GetNodeByID(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, original, stored.CreatedAt.Truncate(time.Second).UTC())
}

func TestUpdateNodeAdmin_KeepsTokenOnEmpty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "panel-token")

	// Пустая строка в PUT = «не менять»: токен в БД не затирается.
	update := node
	update.APIToken = ""

	require.NoError(t, svc.UpdateNodeAdmin(ctx, &update))

	stored, err := svc.GetNodeByID(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, "token-panel-token", stored.APIToken)
}

func TestUpdateNodeAdmin_ReplacesToken(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "panel-token2")

	update := node
	update.APIToken = "new-secret"

	require.NoError(t, svc.UpdateNodeAdmin(ctx, &update))

	stored, err := svc.GetNodeByID(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, "new-secret", stored.APIToken)
}

func TestUpdateNodeAdmin_RejectsTypeChange(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "panel-type")

	update := node
	update.Type = NodeTypeFetch

	err := svc.UpdateNodeAdmin(ctx, &update)
	require.ErrorIs(t, err, ErrNodeTypeProtected)

	stored, err := svc.GetNodeByID(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, NodeType3xUI, stored.Type)
}

func TestUpdateNodeAdmin_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	node := &Node{
		ID:              424242,
		Name:            "ghost",
		Type:            NodeType3xUI,
		Host:            "http://203.0.113.10:2053",
		SubscriptionURL: "https://sub.example.com/s/",
		InboundIDs:      "[]",
	}

	err := svc.UpdateNodeAdmin(context.Background(), node)
	require.ErrorIs(t, err, ErrNodeNotFound)
}

func TestDeleteNode_WithoutDependencies(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "panel-gone")

	deleted, err := svc.DeleteNode(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, node.ID, deleted.ID)
	assert.Equal(t, "panel-gone", deleted.Name)

	_, err = svc.GetNodeByID(ctx, node.ID)
	require.ErrorIs(t, err, ErrNodeNotFound)

	require.Len(t, nodeEvents(t, svc, node.ID, AuditEventNodeDeleted), 1)
}

func TestDeleteNode_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.DeleteNode(context.Background(), 999999)
	require.ErrorIs(t, err, ErrNodeNotFound)
}

func TestDeleteNode_Guards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		withPlan  bool
		withSub   bool
		wantPlans int64
		wantSubs  int64
	}{
		{name: "plan binding only", withPlan: true, wantPlans: 1, wantSubs: 0},
		{name: "subscription binding only", withSub: true, wantPlans: 0, wantSubs: 1},
		{name: "both bindings", withPlan: true, withSub: true, wantPlans: 1, wantSubs: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestService(t)
			ctx := context.Background()

			node := seedNode(t, svc, "panel-used")

			if tt.withPlan {
				plan := seedAdminPlan(t, svc, "plan-"+tt.name)
				seedPlanNode(t, svc, plan.ID, node.ID)
			}

			if tt.withSub {
				sub := &Subscription{
					TelegramID:     424200,
					ClientID:       "client-" + tt.name,
					SubscriptionID: "sub-" + tt.name,
					Status:         string(SubscriptionStatusActive),
					ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
					PlanID:         seedAdminPlan(t, svc, "subplan-"+tt.name).ID,
				}
				require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
				require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{
					SubscriptionID: sub.ID,
					NodeID:         node.ID,
					Status:         SyncStatusActive,
				}))
			}

			_, err := svc.DeleteNode(ctx, node.ID)
			require.ErrorIs(t, err, ErrNodeInUse)

			var inUse *NodeInUseError
			require.ErrorAs(t, err, &inUse)
			assert.Equal(t, tt.wantPlans, inUse.Details["plans"])
			assert.Equal(t, tt.wantSubs, inUse.Details["subscriptions"])

			// Строка осталась на месте после блокировки.
			_, getErr := svc.GetNodeByID(ctx, node.ID)
			require.NoError(t, getErr)
		})
	}
}

func TestReplacePlanNodes(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node1 := seedNode(t, svc, "node-1")
	node2 := seedNode(t, svc, "node-2")
	node3 := seedNode(t, svc, "node-3")

	plan := seedAdminPlan(t, svc, "plan-multi")

	ids, err := svc.ReplacePlanNodes(ctx, plan.ID, []uint{node1.ID, node2.ID})
	require.NoError(t, err)
	assert.Equal(t, []uint{node1.ID, node2.ID}, ids)

	// Повтор того же набора — no-op: строки не меняются, результат равен входу.
	rowsBefore := countPlanNodes(t, svc, plan.ID)
	ids, err = svc.ReplacePlanNodes(ctx, plan.ID, []uint{node2.ID, node1.ID})
	require.NoError(t, err)
	assert.Equal(t, []uint{node1.ID, node2.ID}, ids)
	assert.Equal(t, rowsBefore, countPlanNodes(t, svc, plan.ID))

	// [1,2] → [2,3]: 1 отвязана, 3 привязана, 2 не тронута.
	ids, err = svc.ReplacePlanNodes(ctx, plan.ID, []uint{node2.ID, node3.ID})
	require.NoError(t, err)
	assert.Equal(t, []uint{node2.ID, node3.ID}, ids)

	stored, err := svc.GetPlanNodeIDs(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, []uint{node2.ID, node3.ID}, stored)

	// GetAuditLogByEntity отдаёт события от новых к старым: [0] — последний.
	// Ровно 2 события: no-op повтор аудита не пишет.
	events := nodePlanEvents(t, svc, plan.ID)
	require.Len(t, events, 2)

	latest := events[0]
	assert.Equal(t, AuditEventPlanNodesChanged, latest.Event)

	var details struct {
		Added   []uint `json:"added"`
		Removed []uint `json:"removed"`
	}
	require.NoError(t, json.Unmarshal([]byte(latest.Details), &details))
	assert.Equal(t, []uint{node3.ID}, details.Added)
	assert.Equal(t, []uint{node1.ID}, details.Removed)
}

func TestReplacePlanNodes_Validation(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "node-link")
	plan := seedAdminPlan(t, svc, "plan-link")

	_, err := svc.ReplacePlanNodes(ctx, 999999, []uint{node.ID})
	require.ErrorIs(t, err, ErrPlanNotFound)

	_, err = svc.ReplacePlanNodes(ctx, plan.ID, []uint{424242})
	require.ErrorIs(t, err, ErrNodeInvalid)

	// После отказов привязок нет.
	ids, err := svc.GetPlanNodeIDs(ctx, plan.ID)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestReplacePlanNodes_UnlinkNodeWithSubscriptions(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "node-busy")
	plan := seedAdminPlan(t, svc, "plan-busy")
	seedPlanNode(t, svc, plan.ID, node.ID)

	sub := &Subscription{
		TelegramID:     424201,
		ClientID:       "client-unlink",
		SubscriptionID: "sub-unlink",
		Status:         string(SubscriptionStatusActive),
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		PlanID:         plan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{
		SubscriptionID: sub.ID,
		NodeID:         node.ID,
		Status:         SyncStatusActive,
	}))

	// Отвязка используемой ноды разрешена: панель меняет конфигурацию,
	// а не состояния; subscription_nodes остаются нетронутыми.
	ids, err := svc.ReplacePlanNodes(ctx, plan.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, ids)

	subs, err := svc.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, node.ID, subs[0].NodeID)
	assert.Equal(t, SyncStatusActive, subs[0].Status)
}

func TestGetPlanNodeIDs_EmptyAndMissingPlan(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := seedAdminPlan(t, svc, "plan-empty")

	ids, err := svc.GetPlanNodeIDs(ctx, plan.ID)
	require.NoError(t, err)
	assert.NotNil(t, ids)
	assert.Empty(t, ids)

	_, err = svc.GetPlanNodeIDs(ctx, 999999)
	require.ErrorIs(t, err, ErrPlanNotFound)
}

func TestListNodeOverviews_CountsWithoutNPlusOne(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	node := seedNode(t, svc, "node-overview")
	node2 := seedNode(t, svc, "node-overview-2")

	plan := seedAdminPlan(t, svc, "plan-overview")
	seedPlanNode(t, svc, plan.ID, node.ID)

	sub := &Subscription{
		TelegramID:     424202,
		ClientID:       "client-overview",
		SubscriptionID: "sub-overview",
		Status:         string(SubscriptionStatusActive),
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		PlanID:         plan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{
		SubscriptionID: sub.ID,
		NodeID:         node.ID,
		Status:         SyncStatusPendingAdd,
	}))

	rows, err := svc.ListNodeOverviews(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	byID := make(map[uint]NodeOverview, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}

	got := byID[node.ID]
	assert.Equal(t, int64(1), got.PlansCount)
	assert.Equal(t, int64(1), got.SubscriptionsCount)
	assert.Equal(t, "token-node-overview", got.APIToken)

	got2 := byID[node2.ID]
	assert.Equal(t, int64(0), got2.PlansCount)
	assert.Equal(t, int64(0), got2.SubscriptionsCount)

	overview, err := svc.GetNodeOverview(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), overview.PlansCount)
}

// --- Хелперы проверки plan_nodes -------------------------------------------

func countPlanNodes(t *testing.T, svc *Service, planID uint) int64 {
	t.Helper()

	var count int64
	require.NoError(t, svc.db.Model(&PlanNode{}).Where("plan_id = ?", planID).Count(&count).Error)

	return count
}

func nodePlanEvents(t *testing.T, svc *Service, planID uint) []AuditLog {
	t.Helper()

	rows, err := svc.GetAuditLogByEntity(context.Background(), EntityPlan, intToString(planID), 50)
	require.NoError(t, err)

	var matched []AuditLog
	for _, row := range rows {
		if row.Event == AuditEventPlanNodesChanged {
			matched = append(matched, row)
		}
	}

	return matched
}
