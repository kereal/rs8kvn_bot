package service

// This file contains the transaction-scoped variant of ApplyPlanToSubscription
// used by OrderService.ConfirmPayment. The transition table lives in the
// shared reconciliation core (plan_reconcile.go); this entry point only
// supplies the tx-scoped planNodeStore adapter and the fail-fast error
// policy, so every read/write executes against the caller-supplied *gorm.DB
// and the entire transition (order status, subscription update, plan
// reconciliation) commits or rolls back atomically inside the
// ConfirmOrderPaidCAS transaction.
//
// Per AGENTS.md: "DB-setup phase (GetNodesByPlanID, MarkActiveNodesPendingUpdate,
// ReconcilePlanNodes): pure DB operations that create/update
// pending_add/pending_remove records. These are structural prerequisites —
// without them the background worker has nothing to retry — so failures MUST
// be returned to the caller." That means the plan sync must live inside the
// same transaction as the payment confirmation, not after it.

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// ApplyPlanToSubscriptionInTx is the tx-scoped twin of ApplyPlanToSubscription.
// It is registered with ConfirmOrderPaidCAS so the order CAS, subscription
// update, and subscription_nodes reconciliation all commit or roll back
// together. If this returns an error, the outer transaction aborts and the
// user is shown a payment error (retrying via the provider is then possible).
//
// Deliberately no revoked guard: the payment CAS (e.g. ConfirmOrderPaidCAS)
// has already moved the subscription to active before this call — paying
// reactivates a revoked subscription. The guard exists only on the background
// path, as an entry-point option of the shared core.
func (s *SyncService) ApplyPlanToSubscriptionInTx(ctx context.Context, tx *gorm.DB, subscriptionID uint, planID uint) error {
	if tx == nil {
		return fmt.Errorf("apply plan to subscription %d: nil transaction", subscriptionID)
	}

	store := txPlanStore{tx: tx}

	// Load plan-linked active nodes within the tx, once for both the mark
	// step and the reconciliation core.
	targetNodes, err := store.LoadPlanNodes(ctx, planID)
	if err != nil {
		return fmt.Errorf("apply plan to subscription %d: load plan nodes: %w", subscriptionID, err)
	}

	targetNodeIDs := make([]uint, 0, len(targetNodes))
	for _, n := range targetNodes {
		if n.IsActive {
			targetNodeIDs = append(targetNodeIDs, n.ID)
		}
	}

	// Mark currently active nodes that are still in the target plan for update.
	err = store.MarkActiveNodesPendingUpdate(ctx, subscriptionID, targetNodeIDs)
	if err != nil {
		return fmt.Errorf("apply plan to subscription %d: mark active nodes pending update: %w", subscriptionID, err)
	}

	// Fail-fast: the first per-node error aborts the core, and the returned
	// error rolls back the entire outer transaction.
	return reconcilePlanNodesCore(ctx, store, subscriptionID, planReconcileOptions{
		planID:           planID,
		preloadedTargets: targetNodes,
		onNodeError: func(operation string, nodeID uint, err error) error {
			return fmt.Errorf("apply plan to subscription %d: %s node %d: %w", subscriptionID, operation, nodeID, err)
		},
	})
}
