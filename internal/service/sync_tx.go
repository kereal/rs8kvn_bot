package service

// This file contains the transaction-scoped variant of ApplyPlanToSubscription
// used by OrderService.ConfirmPayment. It mirrors the semantics of
// SyncService.applyPlanToSubscriptionLocked / reconcilePlanNodesLocked but
// executes every read/write against the caller-supplied *gorm.DB so the entire
// transition (order status, subscription update, plan reconciliation) commits
// or rolls back atomically inside the ConfirmOrderPaidCAS transaction.
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

	"github.com/kereal/rs8kvn_bot/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ApplyPlanToSubscriptionInTx is the tx-scoped twin of ApplyPlanToSubscription.
// It is registered with ConfirmOrderPaidCAS so the order CAS, subscription
// update, and subscription_nodes reconciliation all commit or roll back
// together. If this returns an error, the outer transaction aborts and the
// user is shown a payment error (retrying via the provider is then possible).
func (s *SyncService) ApplyPlanToSubscriptionInTx(ctx context.Context, tx *gorm.DB, subscriptionID uint, planID uint) error {
	if tx == nil {
		return fmt.Errorf("apply plan to subscription %d: nil transaction", subscriptionID)
	}

	// Load plan-linked active nodes within the tx.
	var targetNodes []database.Node
	if err := tx.WithContext(ctx).
		Table("nodes").
		Select("nodes.*").
		Joins("JOIN plan_nodes ON plan_nodes.node_id = nodes.id").
		Where("plan_nodes.plan_id = ? AND nodes.is_active = ?", planID, true).
		Find(&targetNodes).Error; err != nil {
		return fmt.Errorf("apply plan to subscription %d: load plan nodes: %w", subscriptionID, err)
	}

	targetNodeIDs := make([]uint, 0, len(targetNodes))
	targetSet := make(map[uint]struct{}, len(targetNodes))
	for _, n := range targetNodes {
		if !n.IsActive {
			continue
		}
		targetNodeIDs = append(targetNodeIDs, n.ID)
		targetSet[n.ID] = struct{}{}
	}

	// Mark currently active nodes that are still in the target plan for update.
	if len(targetNodeIDs) > 0 {
		if err := tx.WithContext(ctx).Model(&database.SubscriptionNode{}).
			Where("subscription_id = ? AND node_id IN (?) AND status = ?", subscriptionID, targetNodeIDs, database.SyncStatusActive).
			Updates(map[string]interface{}{
				"status":      database.SyncStatusPendingUpdate,
				"retry_count": 0,
				"retry_at":    nil,
				"last_error":  nil,
			}).Error; err != nil {
			return fmt.Errorf("apply plan to subscription %d: mark active nodes pending update: %w", subscriptionID, err)
		}
	}

	// Re-fetch the freshly-marked set to drive reconcile semantics.
	var currentNodes []database.SubscriptionNode
	if err := tx.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		Find(&currentNodes).Error; err != nil {
		return fmt.Errorf("apply plan to subscription %d: load current nodes: %w", subscriptionID, err)
	}

	currentActive := make(map[uint]database.SubscriptionNode)
	currentPendingAdd := make(map[uint]database.SubscriptionNode)
	currentPendingRemove := make(map[uint]database.SubscriptionNode)
	currentPendingUpdate := make(map[uint]database.SubscriptionNode)
	for _, sn := range currentNodes {
		switch sn.Status {
		case database.SyncStatusActive:
			currentActive[sn.NodeID] = sn
		case database.SyncStatusPendingAdd:
			currentPendingAdd[sn.NodeID] = sn
		case database.SyncStatusPendingRemove:
			currentPendingRemove[sn.NodeID] = sn
		case database.SyncStatusPendingUpdate:
			currentPendingUpdate[sn.NodeID] = sn
		}
	}

	// ADD path: target node has no record, or pending_remove needs reactivating.
	for nodeID := range targetSet {
		if _, exists := currentActive[nodeID]; exists {
			continue
		}
		if _, exists := currentPendingAdd[nodeID]; exists {
			continue
		}
		if _, exists := currentPendingUpdate[nodeID]; exists {
			// pending_update nodes already on plan: leave as-is.
			continue
		}
		if pending, ok := currentPendingRemove[nodeID]; ok {
			if err := tx.WithContext(ctx).Model(&database.SubscriptionNode{}).
				Where("subscription_id = ? AND node_id = ?", pending.SubscriptionID, pending.NodeID).
				Updates(map[string]interface{}{
					"status":      database.SyncStatusPendingAdd,
					"retry_count": 0,
					"retry_at":    nil,
					"last_error":  nil,
				}).Error; err != nil {
				return fmt.Errorf("apply plan to subscription %d: reactivate pending_remove node %d: %w", subscriptionID, nodeID, err)
			}
			continue
		}
		newNode := &database.SubscriptionNode{
			SubscriptionID: subscriptionID,
			NodeID:         nodeID,
			Status:         database.SyncStatusPendingAdd,
		}
		if err := tx.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "subscription_id"}, {Name: "node_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"status", "retry_count", "retry_at", "last_error", "updated_at"}),
			}).
			Create(newNode).Error; err != nil {
			return fmt.Errorf("apply plan to subscription %d: upsert pending_add node %d: %w", subscriptionID, nodeID, err)
		}
	}

	// REMOVE paths: any record not in targetSet transitions to pending_remove.
	transitionToPendingRemove := func(sn database.SubscriptionNode) error {
		return tx.WithContext(ctx).Model(&database.SubscriptionNode{}).
			Where("subscription_id = ? AND node_id = ?", sn.SubscriptionID, sn.NodeID).
			Updates(map[string]interface{}{
				"status":      database.SyncStatusPendingRemove,
				"retry_count": 0,
				"retry_at":    nil,
				"last_error":  nil,
			}).Error
	}
	for nodeID, sn := range currentActive {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}
		if err := transitionToPendingRemove(sn); err != nil {
			return fmt.Errorf("apply plan to subscription %d: set pending_remove node %d: %w", subscriptionID, nodeID, err)
		}
	}
	for nodeID, sn := range currentPendingAdd {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}
		if err := transitionToPendingRemove(sn); err != nil {
			return fmt.Errorf("apply plan to subscription %d: set pending_remove for stale pending_add node %d: %w", subscriptionID, nodeID, err)
		}
	}
	for nodeID, sn := range currentPendingUpdate {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}
		if err := transitionToPendingRemove(sn); err != nil {
			return fmt.Errorf("apply plan to subscription %d: set pending_remove for stale pending_update node %d: %w", subscriptionID, nodeID, err)
		}
	}
	return nil
}
