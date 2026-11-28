package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedSyncQueueAPI сеет очередь: active-привязка (не в очереди), старая
// pending_add с длинной ошибкой и свежая pending_remove.
func seedSyncQueueAPI(t *testing.T, db *database.Service) {
	t.Helper()

	ctx := context.Background()

	plan := &database.Plan{Name: "queue-api", IsActive: true, DevicesLimit: 1, TrafficLimit: 100 << 30}
	require.NoError(t, db.CreatePlan(ctx, plan))

	nodeA := &database.Node{
		Name: "queue-a", Type: database.NodeType3xUI, IsActive: true,
		Host: "http://203.0.113.10:2053", APIToken: "token-a",
		InboundIDs: "[1]", SubscriptionURL: "https://sub.example.com/s/",
	}
	require.NoError(t, db.CreateNodeAdmin(ctx, nodeA))

	nodeB := &database.Node{
		Name: "queue-b", Type: database.NodeType3xUI, IsActive: true,
		Host: "http://203.0.113.11:2053", APIToken: "token-b",
		InboundIDs: "[1]", SubscriptionURL: "https://sub.example.com/s/",
	}
	require.NoError(t, db.CreateNodeAdmin(ctx, nodeB))

	subA := &database.Subscription{TelegramID: 990001, ClientID: "c-q-a", SubscriptionID: "pub-q-a", Status: "active", PlanID: plan.ID}
	require.NoError(t, db.GetDB().Create(subA).Error)

	subB := &database.Subscription{TelegramID: 990002, ClientID: "c-q-b", SubscriptionID: "pub-q-b", Status: "active", PlanID: plan.ID}
	require.NoError(t, db.GetDB().Create(subB).Error)

	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
		SubscriptionID: subA.ID, NodeID: nodeA.ID, Status: database.SyncStatusActive,
	}))

	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
		SubscriptionID: subA.ID, NodeID: nodeB.ID, Status: database.SyncStatusPendingAdd,
	}))

	longError := strings.Repeat("e", 400)
	require.NoError(t, db.GetDB().Model(&database.SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", subA.ID, nodeB.ID).
		Updates(map[string]any{
			"retry_count": 2,
			"retry_at":    time.Now().UTC().Add(time.Hour),
			"last_error":  longError,
			"updated_at":  time.Now().UTC().Add(-48 * time.Hour),
		}).Error)

	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
		SubscriptionID: subB.ID, NodeID: nodeA.ID, Status: database.SyncStatusPendingRemove,
	}))
}

func decodeSyncQueue(t *testing.T, body []byte) ([]map[string]any, int64) {
	t.Helper()

	var payload struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))

	return payload.Items, payload.Total
}

func TestAPI_SyncQueue(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedSyncQueueAPI(t, db)

	rec := do(t, srv, http.MethodGet, "/admin/api/sync-queue", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	items, total := decodeSyncQueue(t, rec.Body.Bytes())
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)

	// Контракт строки очереди: набор ключей — часть API (ADR-0002).
	assert.Equal(t, []string{
		"last_error", "node_id", "node_name", "retry_at", "retry_count",
		"status", "subscription_id", "subscription_id_public", "telegram_id",
		"updated_at",
	}, contractKeys(items[0]))

	// По умолчанию — самые старые сверху.
	first := items[0]
	assert.Equal(t, "pending_add", first["status"])
	assert.Equal(t, float64(990001), first["telegram_id"])
	assert.Equal(t, "pub-q-a", first["subscription_id_public"])
	assert.Equal(t, "queue-b", first["node_name"])
	assert.Equal(t, float64(2), first["retry_count"])
	assert.NotNil(t, first["retry_at"])
	assert.NotNil(t, first["updated_at"])

	// Длинная ошибка обрезается, а не отдаётся целиком.
	lastError, ok := first["last_error"].(string)
	require.True(t, ok)
	assert.LessOrEqual(t, utf8.RuneCountInString(lastError), syncQueueLastErrorLimit+1)
	assert.True(t, strings.HasSuffix(lastError, "…"))

	assert.Equal(t, "pending_remove", items[1]["status"])
}

func TestAPI_SyncQueue_StatusAndPagination(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedSyncQueueAPI(t, db)

	rec := do(t, srv, http.MethodGet, "/admin/api/sync-queue?status=pending_remove", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items, total := decodeSyncQueue(t, rec.Body.Bytes())
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "pending_remove", items[0]["status"])

	rec = do(t, srv, http.MethodGet, "/admin/api/sync-queue?limit=1&offset=1", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	page2, total := decodeSyncQueue(t, rec.Body.Bytes())
	assert.Equal(t, int64(2), total)
	require.Len(t, page2, 1)
	assert.Equal(t, "pending_remove", page2[0]["status"])
}

func TestAPI_SyncQueue_RejectsBadFilters(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	cases := []string{
		"/admin/api/sync-queue?status=active",
		"/admin/api/sync-queue?status=foo",
		"/admin/api/sync-queue?sort=last_error",
		"/admin/api/sync-queue?order=up",
		"/admin/api/sync-queue?limit=0",
		"/admin/api/sync-queue?offset=-1",
	}

	for _, path := range cases {
		rec := do(t, srv, http.MethodGet, path, testToken, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
}

func TestAPI_SyncQueue_RequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/sync-queue", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
