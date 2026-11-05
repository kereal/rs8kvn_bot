package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSubSyncActions implements SubscriptionSyncActions for the forced-sync
// endpoint: the reconciler is never run, the test programs the outcome the way
// the panel would observe it (nil, error, or bindings left queued in the DB).
type fakeSubSyncActions struct {
	syncErr    error
	syncCalls  int
	lastSyncID uint
}

func (f *fakeSubSyncActions) ResetSubscriptionTraffic(context.Context, uint) (int, error) {
	return 0, nil
}

func (f *fakeSubSyncActions) SyncSubscription(_ context.Context, subscriptionID uint) error {
	f.syncCalls++
	f.lastSyncID = subscriptionID
	return f.syncErr
}

// newSyncTestServer wires the server with a real read store and a programmed
// SubscriptionSyncActions fake — the seam the sync handler depends on.
func newSyncTestServer(t *testing.T) (*Server, *database.Service, *fakeSubSyncActions) {
	t.Helper()

	srv, db := newTestServer(t)
	fake := &fakeSubSyncActions{}
	srv.SetSubscriptionServices(db, service.NewSubscriptionService(db, service.NodeClients{}, nil, &config.Config{}), fake)

	return srv, db, fake
}

// seedSubForSync inserts one active subscription row with raw SQL, as the
// assignment requires, and returns its DB id.
func seedSubForSync(t *testing.T, db *database.Service, telegramID int64, subID string) uint {
	t.Helper()

	require.NoError(t, db.GetDB().Exec(
		`INSERT INTO subscriptions (telegram_id, username, client_id, subscription_id, status)
		 VALUES (?, ?, ?, ?, 'active')`,
		telegramID, "syncuser", "c-"+subID, subID).Error)

	var id uint
	require.NoError(t, db.GetDB().Raw(
		`SELECT id FROM subscriptions WHERE subscription_id = ?`, subID).Scan(&id).Error)
	require.NotZero(t, id, "seeded subscription row must exist")

	return id
}

// decodeSyncResponse parses the sync payload: the refreshed detail plus the
// optional warning.
func decodeSyncResponse(t *testing.T, body []byte) (map[string]any, string) {
	t.Helper()

	var payload struct {
		Subscription map[string]any `json:"subscription"`
		Warning      string         `json:"warning"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))
	require.NotNil(t, payload.Subscription, "sync must return the refreshed detail")

	return payload.Subscription, payload.Warning
}

func TestAPI_SyncSubscription(t *testing.T) {
	t.Parallel()

	t.Run("no warning when queue drains", func(t *testing.T) {
		t.Parallel()

		srv, db, fake := newSyncTestServer(t)
		id := seedSubForSync(t, db, 555000555, "sub-sync-clean")

		rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/sync", id), testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		sub, warning := decodeSyncResponse(t, rec.Body.Bytes())
		assert.Equal(t, float64(id), sub["id"], "payload must carry the refreshed detail")
		assert.Empty(t, warning)
		// warning опущен, а не пустая строка: view различает «чисто» и «с
		// предупреждением».
		var rawKeys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rawKeys))
		assert.NotContains(t, rawKeys, "warning")
		assert.Equal(t, 1, fake.syncCalls)
		assert.Equal(t, id, fake.lastSyncID)
	})

	t.Run("no warning with settled active binding", func(t *testing.T) {
		t.Parallel()

		srv, db, _ := newSyncTestServer(t)
		id := seedSubForSync(t, db, 555000556, "sub-sync-active")

		node := &database.Node{Name: "sync-node", Host: "http://sync", Type: database.NodeType3xUI, IsActive: true, InboundIDs: `[1]`}
		require.NoError(t, db.GetDB().Create(node).Error)
		require.NoError(t, db.CreateSubscriptionNode(context.Background(), &database.SubscriptionNode{
			SubscriptionID: id, NodeID: node.ID, Status: database.SyncStatusActive,
		}))

		rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/sync", id), testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		sub, warning := decodeSyncResponse(t, rec.Body.Bytes())
		assert.Equal(t, float64(id), sub["id"])
		assert.Empty(t, warning)

		nodes, ok := sub["nodes"].([]any)
		require.True(t, ok, "detail must include node bindings")
		require.Len(t, nodes, 1)
		assert.Equal(t, "active", nodes[0].(map[string]any)["status"])
	})

	t.Run("warning when sync returns error", func(t *testing.T) {
		t.Parallel()

		srv, db, fake := newSyncTestServer(t)
		id := seedSubForSync(t, db, 555000557, "sub-sync-error")
		fake.syncErr = errors.New("panel unreachable")

		rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/sync", id), testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		sub, warning := decodeSyncResponse(t, rec.Body.Bytes())
		assert.Equal(t, float64(id), sub["id"], "detail must be refreshed even on partial failure")
		assert.Contains(t, warning, "сверка не удалась")
		assert.Contains(t, warning, "panel unreachable")
	})

	t.Run("warning when bindings stay queued", func(t *testing.T) {
		t.Parallel()

		srv, db, fake := newSyncTestServer(t)
		id := seedSubForSync(t, db, 555000558, "sub-sync-pending")

		node := &database.Node{Name: "stuck-node", Host: "http://stuck", Type: database.NodeType3xUI, IsActive: true, InboundIDs: `[1]`}
		require.NoError(t, db.GetDB().Create(node).Error)
		require.NoError(t, db.CreateSubscriptionNode(context.Background(), &database.SubscriptionNode{
			SubscriptionID: id, NodeID: node.ID, Status: database.SyncStatusPendingRemove,
		}))

		// Fake отрабатывает без ошибки, но очередь в пост-стейте не пуста:
		// SyncSubscription глотает поузловые сбои, поэтому честный сигнал —
		// только статус привязок.
		rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/sync", id), testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Nil(t, fake.syncErr)

		_, warning := decodeSyncResponse(t, rec.Body.Bytes())
		assert.Contains(t, warning, "в очереди")
	})

	t.Run("unknown subscription is not found", func(t *testing.T) {
		t.Parallel()

		srv, _, _ := newSyncTestServer(t)

		rec := do(t, srv, http.MethodPost, "/admin/api/subscriptions/9999/sync", testToken, "")
		require.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "not_found")
	})
}

func TestAPI_SyncSubscription_NotWired(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	id := seedSubForSync(t, db, 555000559, "sub-sync-unwired")

	// Читающий стор реальный, но sync-сервис не подключён: мутации отвечают 503.
	srv.SetSubscriptionServices(db, service.NewSubscriptionService(db, service.NodeClients{}, nil, &config.Config{}), nil)

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/sync", id), testToken, "")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "internal")
}
