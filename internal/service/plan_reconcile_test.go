package service

// Tests for the shared subscription_nodes reconciliation core
// (plan_reconcile.go) across both entry points:
//   - background best-effort: ApplyPlanToSubscription (plan from sub.PlanID);
//   - transactional fail-fast: ApplyPlanToSubscriptionInTx (explicit planID).
//
// The parity test pins the invariant that both paths produce identical
// subscription_nodes end states for the same fixture. The tx tests close the
// historical coverage gap: ApplyPlanToSubscriptionInTx previously had a
// duplicated state machine exercised only indirectly via CAS tests.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type parityFixture struct {
	plan  *database.Plan
	nodes map[string]*database.Node
}

// createParityFixture creates a plan with three target nodes and three
// out-of-plan nodes.
func createParityFixture(t *testing.T, db *database.Service, planName string) parityFixture {
	t.Helper()

	ctx := context.Background()

	plan := &database.Plan{Name: planName, IsActive: true, DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	nodes := make(map[string]*database.Node, 7)
	for i, name := range []string{"in1", "in2", "in3", "in4", "out1", "out2", "out3"} {
		node := &database.Node{
			Name:       planName + "-" + name,
			Type:       database.NodeType3xUI,
			IsActive:   true,
			Host:       "http://" + name,
			APIToken:   name,
			InboundIDs: fmt.Sprintf(`[%d]`, i+1),
		}
		require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
		nodes[name] = node
	}

	for _, name := range []string{"in1", "in2", "in3", "in4"} {
		require.NoError(t, db.GetDB().WithContext(ctx).
			Create(&database.PlanNode{PlanID: plan.ID, NodeID: nodes[name].ID}).Error)
	}

	return parityFixture{plan: plan, nodes: nodes}
}

// createParitySubscription creates an active subscription on the fixture plan
// with the full pre-state covering every transition of the table:
//
//	in1 active          ∈ target → mark step → pending_update (retry reset)
//	in2 pending_remove  ∈ target → reactivated → pending_add (retry reset)
//	in3 pending_add     ∈ target → no transition (retry fields KEPT dirty)
//	in4 (no record)     ∈ target → INSERT pending_add
//	out1 active         ∉ target → pending_remove (retry reset)
//	out2 pending_add    ∉ target → pending_remove (retry reset)
//	out3 pending_update ∉ target → pending_remove (retry reset)
//
// out3 needs an extra node because the fixture only wires in1..in4 into the plan.
func createParitySubscription(t *testing.T, db *database.Service, fx parityFixture, telegramID int64, status string) *database.Subscription {
	t.Helper()

	ctx := context.Background()

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	sub := &database.Subscription{
		TelegramID:     telegramID,
		Username:       fmt.Sprintf("parity-user-%d", telegramID),
		ClientID:       fmt.Sprintf("parity-client-%d", telegramID),
		SubscriptionID: fmt.Sprintf("parity-sub-%d", telegramID),
		Status:         status,
		PlanID:         fx.plan.ID,
		ExpiresAt:      &expiresAt,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	dirtyRetryAt := time.Now().UTC().Add(-time.Hour)
	dirtyLastError := "stale provisioning error"

	// out3 pending_update also carries dirty retry fields.
	dirty := func(status database.SyncStatus, node *database.Node) *database.SubscriptionNode {
		retryAt := dirtyRetryAt
		lastError := dirtyLastError

		return &database.SubscriptionNode{
			SubscriptionID: sub.ID,
			NodeID:         node.ID,
			Status:         status,
			RetryCount:     3,
			RetryAt:        &retryAt,
			LastError:      &lastError,
		}
	}

	require.NoError(t, db.CreateSubscriptionNode(ctx, dirty(database.SyncStatusActive, fx.nodes["in1"])))
	require.NoError(t, db.CreateSubscriptionNode(ctx, dirty(database.SyncStatusPendingRemove, fx.nodes["in2"])))
	require.NoError(t, db.CreateSubscriptionNode(ctx, dirty(database.SyncStatusPendingAdd, fx.nodes["in3"])))
	require.NoError(t, db.CreateSubscriptionNode(ctx, dirty(database.SyncStatusActive, fx.nodes["out1"])))
	require.NoError(t, db.CreateSubscriptionNode(ctx, dirty(database.SyncStatusPendingAdd, fx.nodes["out2"])))
	require.NoError(t, db.CreateSubscriptionNode(ctx, dirty(database.SyncStatusPendingUpdate, fx.nodes["out3"])))

	return sub
}

// loadNodeStates reads subscription_nodes rows for a subscription keyed by node ID.
func loadNodeStates(t *testing.T, db *database.Service, subscriptionID uint) map[uint]database.SubscriptionNode {
	t.Helper()

	rows, err := db.GetBySubscriptionID(context.Background(), subscriptionID)
	require.NoError(t, err)

	states := make(map[uint]database.SubscriptionNode, len(rows))
	for _, row := range rows {
		states[row.NodeID] = row
	}

	return states
}

// assertParityEndState asserts the expected end state of the shared fixture:
// identical transitions for both entry points, retry fields reset on every
// transition and preserved where no transition happened.
func assertParityEndState(t *testing.T, states map[uint]database.SubscriptionNode, fx parityFixture) {
	t.Helper()

	want := map[string]database.SyncStatus{
		"in1":  database.SyncStatusPendingUpdate, // mark: active ∈ target
		"in2":  database.SyncStatusPendingAdd,    // pending_remove ∈ target → reactivated
		"in3":  database.SyncStatusPendingAdd,    // pending_add ∈ target → no transition
		"in4":  database.SyncStatusPendingAdd,    // no record → INSERT pending_add
		"out1": database.SyncStatusPendingRemove, // active ∉ target
		"out2": database.SyncStatusPendingRemove, // pending_add ∉ target
		"out3": database.SyncStatusPendingRemove, // pending_update ∉ target
	}
	for name, status := range want {
		node := fx.nodes[name]
		row, ok := states[node.ID]
		require.True(t, ok, "node %s must have a subscription_nodes row", name)
		assert.Equal(t, status, row.Status, "node %s", name)
	}

	// Retry fields reset on every transition.
	transitioned := []string{"in1", "in2", "in4", "out1", "out2", "out3"}
	for _, name := range transitioned {
		row := states[fx.nodes[name].ID]
		assert.Zero(t, row.RetryCount, "node %s retry_count must be reset", name)
		assert.Nil(t, row.RetryAt, "node %s retry_at must be reset", name)
		assert.Nil(t, row.LastError, "node %s last_error must be reset", name)
	}

	// in3 keeps its dirty retry fields: pending_add ∈ target is NOT a transition.
	in3 := states[fx.nodes["in3"].ID]
	assert.Equal(t, 3, in3.RetryCount, "pending_add in target must keep retry_count")
	assert.NotNil(t, in3.RetryAt, "pending_add in target must keep retry_at")
	assert.NotNil(t, in3.LastError, "pending_add in target must keep last_error")
}

// TestApplyPlan_Parity_BackgroundAndTxSameEndState runs the same fixture
// through both entry points and requires identical subscription_nodes end
// states, including the retry-field resets.
func TestApplyPlan_Parity_BackgroundAndTxSameEndState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	fx := createParityFixture(t, db, "parity-plan")

	bgSub := createParitySubscription(t, db, fx, 990001, "active")
	txSub := createParitySubscription(t, db, fx, 990002, "active")

	svc := NewSyncService(db, NodeClients{}, nil)

	// Background path: lock + revoked guard + joined errors; plan from sub.PlanID.
	require.NoError(t, svc.ApplyPlanToSubscription(ctx, bgSub.ID))

	// Tx path: fail-fast; plan passed explicitly.
	require.NoError(t, svc.ApplyPlanToSubscriptionInTx(ctx, db.GetDB(), txSub.ID, fx.plan.ID))

	bgStates := loadNodeStates(t, db, bgSub.ID)
	txStates := loadNodeStates(t, db, txSub.ID)

	assertParityEndState(t, bgStates, fx)
	assertParityEndState(t, txStates, fx)

	// Both paths must land on the exact same state per node.
	require.Len(t, txStates, len(bgStates))
	for nodeID, bgRow := range bgStates {
		txRow, ok := txStates[nodeID]
		require.True(t, ok, "tx path is missing node %d", nodeID)
		assert.Equal(t, bgRow.Status, txRow.Status, "node %d", nodeID)
		assert.Equal(t, bgRow.RetryCount, txRow.RetryCount, "node %d", nodeID)
		// The two subscriptions hold independent pre-state timestamps, so
		// no-transition nodes (dirty retry fields kept) can only be compared
		// by nil-ness, not by value.
		assert.Equal(t, bgRow.RetryAt == nil, txRow.RetryAt == nil, "node %d retry_at nil-ness", nodeID)
		assert.Equal(t, bgRow.LastError == nil, txRow.LastError == nil, "node %d last_error nil-ness", nodeID)
	}
}

// TestApplyPlanToSubscriptionInTx_Transitions pins the tx-side behavior of
// every transition in the shared table (the historical coverage gap).
func TestApplyPlanToSubscriptionInTx_Transitions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	fx := createParityFixture(t, db, "tx-transitions-plan")
	sub := createParitySubscription(t, db, fx, 990003, "active")

	svc := NewSyncService(db, NodeClients{}, nil)
	require.NoError(t, svc.ApplyPlanToSubscriptionInTx(ctx, db.GetDB(), sub.ID, fx.plan.ID))

	assertParityEndState(t, loadNodeStates(t, db, sub.ID), fx)
}

// TestApplyPlanToSubscriptionInTx_AddCreatesPendingAddWithCleanRetry isolates
// the ADD branch for a node without an existing record.
func TestApplyPlanToSubscriptionInTx_AddCreatesPendingAddWithCleanRetry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	fx := createParityFixture(t, db, "tx-add-plan")

	sub := &database.Subscription{
		TelegramID: 990011, Username: "tx-add-user", ClientID: "tx-add-client",
		SubscriptionID: "tx-add-sub", Status: "active", PlanID: fx.plan.ID,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	svc := NewSyncService(db, NodeClients{}, nil)
	require.NoError(t, svc.ApplyPlanToSubscriptionInTx(ctx, db.GetDB(), sub.ID, fx.plan.ID))

	states := loadNodeStates(t, db, sub.ID)
	for _, name := range []string{"in1", "in2", "in3", "in4"} {
		row, ok := states[fx.nodes[name].ID]
		require.True(t, ok, "node %s must exist", name)
		assert.Equal(t, database.SyncStatusPendingAdd, row.Status, "node %s", name)
		assert.Zero(t, row.RetryCount)
		assert.Nil(t, row.RetryAt)
		assert.Nil(t, row.LastError)
	}
}

// TestApplyPlanToSubscriptionInTx_FailFastRollsBackAll pins the fail-fast
// contract: the first write error aborts reconciliation, and the outer
// transaction rollback leaves subscription_nodes completely unchanged.
func TestApplyPlanToSubscriptionInTx_FailFastRollsBackAll(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	fx := createParityFixture(t, db, "tx-failfast-plan")
	sub := createParitySubscription(t, db, fx, 990012, "active")

	before := loadNodeStates(t, db, sub.ID)

	// Fail the only ADD-path create: node in4 has no record, so the core
	// issues an INSERT (Create) for it. All other transitions go through
	// UPDATEs (mark step, reactivation, pending_remove).
	err = db.GetDB().Callback().Create().Before("gorm:create").Register("test_fail_tx_create", func(tx *gorm.DB) {
		dest, ok := tx.Statement.Dest.(*database.SubscriptionNode)
		if ok && dest.NodeID == fx.nodes["in4"].ID {
			_ = tx.AddError(fmt.Errorf("boom: injected create failure"))
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { db.GetDB().Callback().Create().Remove("test_fail_tx_create") })

	outer := db.GetDB().Begin()
	require.NoError(t, outer.Error)

	svc := NewSyncService(db, NodeClients{}, nil)
	err = svc.ApplyPlanToSubscriptionInTx(ctx, outer, sub.ID, fx.plan.ID)
	require.Error(t, err, "fail-fast must surface the first write error")
	assert.Contains(t, err.Error(), "boom: injected create failure")

	require.NoError(t, outer.Rollback().Error)

	after := loadNodeStates(t, db, sub.ID)
	require.Len(t, after, len(before), "rollback must leave no new rows")
	for nodeID, beforeRow := range before {
		afterRow, ok := after[nodeID]
		require.True(t, ok, "rollback must not delete existing rows (node %d)", nodeID)
		assert.Equal(t, beforeRow.Status, afterRow.Status, "node %d", nodeID)
		assert.Equal(t, beforeRow.RetryCount, afterRow.RetryCount, "node %d", nodeID)
		assert.Equal(t, beforeRow.RetryAt, afterRow.RetryAt, "node %d", nodeID)
		assert.Equal(t, beforeRow.LastError, afterRow.LastError, "node %d", nodeID)
	}
}

// TestApplyPlanToSubscriptionInTx_RevokedSubscriptionIsStillReconciled pins
// the deliberate asymmetry: the tx path has NO revoked guard. A payment CAS
// moves the subscription to active before applyPlan, so paying reactivates a
// revoked subscription and the tx path must still reconcile it.
func TestApplyPlanToSubscriptionInTx_RevokedSubscriptionIsStillReconciled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	fx := createParityFixture(t, db, "tx-revoked-plan")
	sub := createParitySubscription(t, db, fx, 990013, string(database.SubscriptionStatusRevoked))

	svc := NewSyncService(db, NodeClients{}, nil)
	require.NoError(t, svc.ApplyPlanToSubscriptionInTx(ctx, db.GetDB(), sub.ID, fx.plan.ID))

	// Behavior check: revoked subscription still gets the full transition
	// set, including pending_remove for nodes outside the plan.
	states := loadNodeStates(t, db, sub.ID)
	for _, name := range []string{"out1", "out2", "out3"} {
		row, ok := states[fx.nodes[name].ID]
		require.True(t, ok, "node %s must exist", name)
		assert.Equal(t, database.SyncStatusPendingRemove, row.Status, "node %s", name)
	}
	assert.Equal(t, database.SyncStatusPendingAdd, states[fx.nodes["in2"].ID].Status)
	assert.Equal(t, database.SyncStatusPendingAdd, states[fx.nodes["in4"].ID].Status)
}

// TestApplyPlanToSubscriptionInTx_NilTransaction pins the nil-tx guard.
func TestApplyPlanToSubscriptionInTx_NilTransaction(t *testing.T) {
	t.Parallel()

	svc := NewSyncService(nil, NodeClients{}, nil)

	err := svc.ApplyPlanToSubscriptionInTx(context.Background(), nil, 1, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil transaction")
}
