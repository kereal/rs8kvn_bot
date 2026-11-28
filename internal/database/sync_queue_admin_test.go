package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedSyncQueue сеет две подписки и три привязки: active (не очередь),
// pending_add (старая запись с ошибкой) и pending_remove (свежая).
func seedSyncQueue(t *testing.T, svc *Service) (pendingAdd, pendingRemove SubscriptionNode) {
	t.Helper()

	ctx := context.Background()

	plan := &Plan{Name: "queue-30d", IsActive: true, DevicesLimit: 1, TrafficLimit: 100 << 30}
	require.NoError(t, svc.CreatePlan(ctx, plan))

	nodeA := seedNode(t, svc, "queue-node-a")
	nodeB := seedNode(t, svc, "queue-node-b")

	subA := &Subscription{TelegramID: 770001, ClientID: "c-q-a", SubscriptionID: "pub-queue-a", Status: string(SubscriptionStatusActive), PlanID: plan.ID}
	require.NoError(t, svc.db.Create(subA).Error)

	subB := &Subscription{TelegramID: 770002, ClientID: "c-q-b", SubscriptionID: "pub-queue-b", Status: string(SubscriptionStatusActive), PlanID: plan.ID}
	require.NoError(t, svc.db.Create(subB).Error)

	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{
		SubscriptionID: subA.ID, NodeID: nodeA.ID, Status: SyncStatusActive,
	}))

	old := time.Now().UTC().Add(-72 * time.Hour)
	failed := "node unreachable"
	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{
		SubscriptionID: subA.ID, NodeID: nodeB.ID, Status: SyncStatusPendingAdd,
	}))
	require.NoError(t, svc.db.Model(&SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", subA.ID, nodeB.ID).
		Updates(map[string]any{
			"retry_count": 3,
			"retry_at":    old.Add(time.Hour),
			"last_error":  failed,
			"updated_at":  old,
		}).Error)

	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{
		SubscriptionID: subB.ID, NodeID: nodeA.ID, Status: SyncStatusPendingRemove,
	}))

	var addRow SubscriptionNode
	require.NoError(t, svc.db.Where("subscription_id = ? AND node_id = ?", subA.ID, nodeB.ID).First(&addRow).Error)

	var removeRow SubscriptionNode
	require.NoError(t, svc.db.Where("subscription_id = ? AND node_id = ?", subB.ID, nodeA.ID).First(&removeRow).Error)

	return addRow, removeRow
}

func TestListSyncQueue_DefaultOnlyPendingOldestFirst(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	addRow, removeRow := seedSyncQueue(t, svc)

	items, total, err := svc.ListSyncQueue(ctx, SyncQueueFilter{Limit: 50})
	require.NoError(t, err)

	// active в очередь не входит: total и items описывают только pending_*.
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)
	assert.Equal(t, addRow.SubscriptionID, items[0].SubscriptionID)
	assert.Equal(t, removeRow.SubscriptionID, items[1].SubscriptionID)
}

func TestListSyncQueue_StatusFilterUniformWhere(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	addRow, _ := seedSyncQueue(t, svc)

	items, total, err := svc.ListSyncQueue(ctx, SyncQueueFilter{Status: string(SyncStatusPendingAdd), Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, string(SyncStatusPendingAdd), items[0].Status)

	_, total, err = svc.ListSyncQueue(ctx, SyncQueueFilter{Status: string(SyncStatusPendingRemove), Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// Явный фильтр — это фильтр по колонке; active отсекает хендлер (400),
	// потому что очередь по определению состоит из pending_*.
	_, total, err = svc.ListSyncQueue(ctx, SyncQueueFilter{Status: string(SyncStatusActive), Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	assert.Equal(t, addRow.SubscriptionID, items[0].SubscriptionID)
}

func TestListSyncQueue_RowFieldsAndPagination(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	seedSyncQueue(t, svc)

	items, total, err := svc.ListSyncQueue(ctx, SyncQueueFilter{Limit: 1, Offset: 0})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 1)

	row := items[0]
	assert.Equal(t, int64(770001), row.TelegramID)
	assert.Equal(t, "pub-queue-a", row.SubscriptionIDPublic)
	assert.Equal(t, "queue-node-b", row.NodeName)
	assert.Equal(t, 3, row.RetryCount)
	require.NotNil(t, row.LastError)
	assert.Equal(t, "node unreachable", *row.LastError)
	require.NotNil(t, row.RetryAt)
	assert.False(t, row.UpdatedAt.IsZero())

	second, _, err := svc.ListSyncQueue(ctx, SyncQueueFilter{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.NotEqual(t, row.NodeID, second[0].NodeID, "страницы не пересекаются")

	desc, _, err := svc.ListSyncQueue(ctx, SyncQueueFilter{Limit: 50, Desc: true})
	require.NoError(t, err)
	require.Len(t, desc, 2)
	assert.Equal(t, second[0].NodeID, desc[0].NodeID, "desc — свежие сверху")
}
