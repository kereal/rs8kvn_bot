package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// revokedFixture builds a subscription service wired to a store fake with one
// active subscription bound to one node.
func revokedFixture(t *testing.T) (*SubscriptionService, *testutil.ServiceStoreFake, *database.Subscription, *mockVPNClient) {
	t.Helper()

	node := database.Node{ID: 7, Name: "node-7", Host: "node7.example", Type: database.NodeType3xUI, IsActive: true}
	sub := &database.Subscription{
		ID:             41,
		TelegramID:     9001,
		ClientID:       "client-41",
		SubscriptionID: "sub-41",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         1,
		CreatedAt:      time.Now().UTC(),
	}

	plan := &database.Plan{ID: 1, Name: "Free"}

	m := &testutil.ServiceStoreFake{
		Subscriptions:     map[int64]*database.Subscription{sub.TelegramID: sub},
		SubscriptionsByID: map[uint]*database.Subscription{sub.ID: sub},
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			if id != sub.ID {
				return nil, database.ErrSubscriptionNotFound
			}

			return sub, nil
		},
		GetBySubscriptionIDFunc: func(_ context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{{
				SubscriptionID: subscriptionID,
				NodeID:         node.ID,
				Status:         database.SyncStatusActive,
			}}, nil
		},
		GetPendingBySubscriptionIDFunc: func(_ context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{{
				SubscriptionID: subscriptionID,
				NodeID:         node.ID,
				Status:         database.SyncStatusPendingRemove,
			}}, nil
		},
		GetPlanByIDFunc: func(_ context.Context, _ uint) (*database.Plan, error) { return plan, nil },
		GetNodeByIDFunc: func(_ context.Context, _ uint) (*database.Node, error) { return &node, nil },
	}

	client := &mockVPNClient{}
	clients := NodeClients{node.ID: {Provision: client}}

	svc := NewSubscriptionService(m, clients, []database.Node{node}, &config.Config{})
	svc.SetSyncService(NewSyncService(m, clients, []database.Node{node}))

	return svc, m, sub, client
}

// TestRevokeSubscription_MarksRevokedAndDeprovisions covers the panel revoke
// action: the row flips to revoked (subserver starts answering 404), the VPN
// binding is queued for removal, and the audit trail records the event.
func TestRevokeSubscription_MarksRevokedAndDeprovisions(t *testing.T) {
	t.Parallel()

	svc, store, sub, client := revokedFixture(t)

	var queuedForRemoval bool
	store.UpdateSubscriptionNodeStatusFunc = func(_ context.Context, _, _ uint, status database.SyncStatus) error {
		if status == database.SyncStatusPendingRemove {
			queuedForRemoval = true
		}

		return nil
	}

	revoked, err := svc.RevokeSubscription(WithAuditAdmin(context.Background(), 77), sub.ID)
	require.NoError(t, err)
	require.NotNil(t, revoked)

	assert.Equal(t, string(database.SubscriptionStatusRevoked), revoked.Status,
		"the row must say revoked so the subserver answers 404 immediately")
	assert.True(t, queuedForRemoval, "VPN access must be queued for removal")
	assert.True(t, client.deleteCalled, "the immediate deprovision sync must reach the panel")

	events := store.AuditLogsByEvent(database.AuditEventSubscriptionRevoked)
	require.Len(t, events, 1, "a revoke is a mutation and must be audited")

	// Панельный отзыв — решение администратора, а не ответ воркера: журнал
	// должен отвечать «кто», иначе робот- и человек-отзывы неразличимы.
	assert.Equal(t, database.ActorAdmin, events[0].ActorType)
	require.NotNil(t, events[0].ActorID)
	assert.Equal(t, "77", *events[0].ActorID)
}

// TestRevokeSubscription_AlreadyRevoked rejects a second revoke instead of
// reporting a false success: the panel distinguishes "just revoked" from
// "already was", and the caller maps ErrAlreadyRevoked to 409.
func TestRevokeSubscription_AlreadyRevoked(t *testing.T) {
	t.Parallel()

	svc, store, sub, _ := revokedFixture(t)
	sub.Status = string(database.SubscriptionStatusRevoked)

	updates := 0

	store.UpdateSubscriptionFunc = func(_ context.Context, _ *database.Subscription) error {
		updates++

		return nil
	}

	revoked, err := svc.RevokeSubscription(context.Background(), sub.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRevoked), "expected ErrAlreadyRevoked, got %v", err)
	assert.Nil(t, revoked)
	assert.Zero(t, updates, "a rejected revoke must not write to the database")
	assert.Empty(t, store.AuditLogsByEvent(database.AuditEventSubscriptionRevoked),
		"a rejected revoke is not a mutation and must not be audited")
}

// TestRevokeSubscription_FailedDeprovisionStillAudited locks the audit and the
// cache invalidation to the durable status flip: if queueing the VPN removal
// fails (transient DB error), the subscription is still revoked and the worker
// must be able to finish the removal later. The audit row and the cache drop
// must not wait for phase two.
func TestRevokeSubscription_FailedDeprovisionStillAudited(t *testing.T) {
	t.Parallel()

	svc, store, sub, _ := revokedFixture(t)

	var invalidated []string
	svc.SetInvalidateBySubIDFunc(func(subID string) {
		invalidated = append(invalidated, subID)
	})

	// Phase 2 fails: the store cannot mark the binding pending_remove.
	store.UpdateSubscriptionNodeStatusFunc = func(_ context.Context, _, _ uint, _ database.SyncStatus) error {
		return errors.New("disk full")
	}

	revoked, err := svc.RevokeSubscription(WithAuditAdmin(context.Background(), 77), sub.ID)
	require.Error(t, err, "a failed deprovision must surface to the panel")
	require.Nil(t, revoked)

	storedPending := store.SubscriptionsByID[sub.ID].Status
	assert.Equal(t, string(database.SubscriptionStatusRevoked), storedPending,
		"the row must stay revoked even when deprovisioning failed")

	events := store.AuditLogsByEvent(database.AuditEventSubscriptionRevoked)
	require.Len(t, events, 1, "a revocation that already happened must be audited even if deprovisioning failed")
	assert.Equal(t, database.ActorAdmin, events[0].ActorType)
	assert.Equal(t, "sub-41", strings.Join(invalidated, ","),
		"the cached profile must be dropped as soon as the row says revoked")
}

// TestResetSubscriptionTraffic_OnlyActiveNodes resets the counter on active
// bindings only: pending_remove rows have no client left to reset, and counting
// them would report nodes that were never touched.
func TestResetSubscriptionTraffic_OnlyActiveNodes(t *testing.T) {
	t.Parallel()

	nodeActive := database.Node{ID: 7, Name: "active", Host: "a.example", Type: database.NodeType3xUI, IsActive: true}
	nodePending := database.Node{ID: 8, Name: "pending", Host: "b.example", Type: database.NodeType3xUI, IsActive: true}
	sub := &database.Subscription{ID: 41, TelegramID: 9001, ClientID: "client-41", SubscriptionID: "sub-41", PlanID: 1}
	plan := &database.Plan{ID: 1, Name: "Free"}

	m := &testutil.ServiceStoreFake{
		GetByIDFunc: func(_ context.Context, _ uint) (*database.Subscription, error) { return sub, nil },
		GetBySubscriptionIDFunc: func(_ context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{
				{SubscriptionID: subscriptionID, NodeID: nodeActive.ID, Status: database.SyncStatusActive},
				{SubscriptionID: subscriptionID, NodeID: nodePending.ID, Status: database.SyncStatusPendingRemove},
			}, nil
		},
		GetPlanByIDFunc: func(_ context.Context, _ uint) (*database.Plan, error) { return plan, nil },
	}

	activeClient := &mockVPNClient{}
	pendingClient := &mockVPNClient{}
	clients := NodeClients{
		nodeActive.ID:  {Provision: activeClient},
		nodePending.ID: {Provision: pendingClient},
	}

	sync := NewSyncService(m, clients, []database.Node{nodeActive, nodePending})

	reset, err := sync.ResetSubscriptionTraffic(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, reset)
	assert.True(t, activeClient.resetTrafficCalled)
	assert.False(t, pendingClient.resetTrafficCalled, "pending_remove bindings have no client to reset")
}

// TestResetSubscriptionTraffic_ReportsPartialFailure aggregates per-node
// failures while still counting the nodes that answered: the panel shows
// "reset on N, failed M" and never a blanket success.
func TestResetSubscriptionTraffic_ReportsPartialFailure(t *testing.T) {
	t.Parallel()

	okNode := database.Node{ID: 7, Name: "ok", Host: "a.example", Type: database.NodeType3xUI, IsActive: true}
	badNode := database.Node{ID: 8, Name: "bad", Host: "b.example", Type: database.NodeType3xUI, IsActive: true}
	sub := &database.Subscription{ID: 41, TelegramID: 9001, ClientID: "client-41", SubscriptionID: "sub-41", PlanID: 1}
	plan := &database.Plan{ID: 1, Name: "Free"}

	m := &testutil.ServiceStoreFake{
		GetByIDFunc: func(_ context.Context, _ uint) (*database.Subscription, error) { return sub, nil },
		GetBySubscriptionIDFunc: func(_ context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{
				{SubscriptionID: subscriptionID, NodeID: okNode.ID, Status: database.SyncStatusActive},
				{SubscriptionID: subscriptionID, NodeID: badNode.ID, Status: database.SyncStatusActive},
			}, nil
		},
		GetPlanByIDFunc: func(_ context.Context, _ uint) (*database.Plan, error) { return plan, nil },
	}

	failingClient := &mockVPNClient{resetTrafficError: errors.New("panel unavailable")}
	clients := NodeClients{
		okNode.ID:  {Provision: &mockVPNClient{}},
		badNode.ID: {Provision: failingClient},
	}

	sync := NewSyncService(m, clients, []database.Node{okNode, badNode})

	reset, err := sync.ResetSubscriptionTraffic(context.Background(), sub.ID)
	require.Error(t, err, "a failed node must surface as an error")
	assert.Equal(t, 1, reset, "the successful node still counts")
	assert.Contains(t, err.Error(), "node 8")
}

// TestResetSubscriptionTraffic_UnknownSubscription returns the not-found error
// untouched so the handler can map it to 404 instead of 500.
func TestResetSubscriptionTraffic_UnknownSubscription(t *testing.T) {
	t.Parallel()

	m := &testutil.ServiceStoreFake{
		GetByIDFunc: func(_ context.Context, _ uint) (*database.Subscription, error) {
			return nil, database.ErrSubscriptionNotFound
		},
	}

	sync := NewSyncService(m, NodeClients{}, nil)

	reset, err := sync.ResetSubscriptionTraffic(context.Background(), 404)
	require.Error(t, err)
	assert.True(t, errors.Is(err, database.ErrSubscriptionNotFound))
	assert.Zero(t, reset)
}

// TestAuditTrafficReset_CarriesNodeCounts pins the failure detail: a reset with
// partial success must be reconstructible from the audit log alone.
func TestAuditTrafficReset_CarriesNodeCounts(t *testing.T) {
	t.Parallel()

	sub := &database.Subscription{ID: 41, TelegramID: 9001, ClientID: "client-41", SubscriptionID: "sub-41"}
	entry := database.AuditTrafficReset(sub, 2, 1)

	assert.Equal(t, database.AuditEventTrafficReset, entry.Event)
	assert.Equal(t, database.EntitySubscription, entry.EntityType)
	assert.Equal(t, database.ActorAdmin, entry.ActorType)
	assert.Contains(t, entry.Details, `"nodes_ok":2`)
	assert.Contains(t, entry.Details, `"nodes_failed":1`)
}
