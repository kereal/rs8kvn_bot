package service

// This file owns the single copy of the subscription_nodes transition table
// used by both ApplyPlan entry points:
//
//   - background best-effort sync (reconcilePlanNodesLocked in sync.go,
//     backed by interfaces.SyncStore, per-node errors joined);
//   - transactional payment CAS (ApplyPlanToSubscriptionInTx in sync_tx.go,
//     backed by the caller's *gorm.DB, fail-fast with rollback).
//
// The transition table (identical for both paths):
//
//	Mark step (ApplyPlan only): active ∧ node ∈ target → pending_update.
//	ADD for nodes ∈ target:      no record → INSERT pending_add;
//	                             active / pending_add / pending_update → no-op;
//	                             pending_remove → pending_add.
//	REMOVE for nodes ∉ target:   active / pending_add / pending_update → pending_remove;
//	                             pending_remove → no-op.
//
// Every transition resets retry_count = 0, retry_at = nil, last_error = nil.
//
// Entry-point-specific behavior (locking, the revoked guard, error policy,
// logging) stays OUT of the core: the core only applies the table through the
// planNodeStore seam and delegates per-node error decisions to the
// onNodeError policy callback.

import (
	"context"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// planNodeStore is the consumer-defined seam the reconciliation core operates
// on. It exposes the minimal storage operations the transition table needs so
// the same rules run against the background DatabaseService path and inside a
// payment CAS transaction.
type planNodeStore interface {
	// LoadSubscription loads the subscription row (revoked guard + plan
	// resolution on the background path).
	LoadSubscription(ctx context.Context, subscriptionID uint) (*database.Subscription, error)
	// LoadPlanNodes loads the active nodes linked to the plan.
	LoadPlanNodes(ctx context.Context, planID uint) ([]database.Node, error)
	// LoadSubscriptionNodes loads all current subscription_nodes rows.
	LoadSubscriptionNodes(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	// MarkActiveNodesPendingUpdate applies the mark step: active ∧ node ∈
	// target → pending_update, with retry fields reset.
	MarkActiveNodesPendingUpdate(ctx context.Context, subscriptionID uint, targetNodeIDs []uint) error
	// SetNodeStatus transitions an existing subscription node to the given
	// status, resetting retry_count, retry_at and last_error.
	SetNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error
	// UpsertPendingAdd inserts a pending_add record or updates an existing
	// one by the composite key, resetting retry fields on conflict.
	UpsertPendingAdd(ctx context.Context, sn *database.SubscriptionNode) error
}

// planReconcileOptions tunes the shared core for one entry point.
type planReconcileOptions struct {
	// readSubscription makes the core load the subscription, skip revoked
	// subscriptions and reconcile against sub.PlanID. Only the background
	// path sets this. The tx path MUST NOT: the payment CAS (e.g.
	// ConfirmOrderPaidCAS) has already reactivated the subscription before
	// calling applyPlan, and the caller supplies the product plan
	// explicitly, which may differ from the stale sub.PlanID.
	readSubscription bool
	// planID is the target plan when readSubscription is false.
	planID uint
	// preloadedTargets, when non-nil, replaces the store LoadPlanNodes call
	// (the tx entry point loads targets once for both mark step and core).
	preloadedTargets []database.Node
	// onNodeError applies the entry point's per-node error policy (join for
	// the background path, fail-fast for the tx path). A non-nil return
	// aborts reconciliation immediately.
	onNodeError func(operation string, nodeID uint, err error) error
}

// reconcilePlanNodesCore reconciles subscription_nodes for one subscription
// against its target plan, applying the single copy of the transition table.
// Load errors are returned wrapped; per-node operation errors go through
// opt.onNodeError, whose non-nil return aborts the core.
func reconcilePlanNodesCore(ctx context.Context, store planNodeStore, subscriptionID uint, opt planReconcileOptions) error {
	planID := opt.planID

	if opt.readSubscription {
		sub, err := store.LoadSubscription(ctx, subscriptionID)
		if err != nil {
			return fmt.Errorf("load subscription: %w", err)
		}
		if sub.Status == string(database.SubscriptionStatusRevoked) {
			return nil
		}

		planID = sub.PlanID
	}

	targetNodes := opt.preloadedTargets
	if targetNodes == nil {
		var err error

		targetNodes, err = store.LoadPlanNodes(ctx, planID)
		if err != nil {
			return fmt.Errorf("load plan nodes: %w", err)
		}
	}

	currentNodes, err := store.LoadSubscriptionNodes(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("load current nodes: %w", err)
	}

	targetSet := make(map[uint]struct{}, len(targetNodes))
	for _, n := range targetNodes {
		if n.IsActive {
			targetSet[n.ID] = struct{}{}
		}
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

	nodeError := func(operation string, nodeID uint, err error) error {
		if opt.onNodeError != nil {
			return opt.onNodeError(operation, nodeID, err)
		}

		return fmt.Errorf("%s node %d: %w", operation, nodeID, err)
	}

	// ADD path: target node has no record, or pending_remove needs reactivating.
	// Iterates the target slice (not targetSet) to keep a deterministic
	// processing order, matching the historical background behavior.
	for _, target := range targetNodes {
		if !target.IsActive {
			continue
		}

		nodeID := target.ID

		if _, exists := currentActive[nodeID]; exists {
			continue
		}

		if _, exists := currentPendingAdd[nodeID]; exists {
			continue
		}

		if _, exists := currentPendingUpdate[nodeID]; exists {
			logger.Debug("pending_update node in target plan, leaving as-is",
				zap.Uint("subscription_id", subscriptionID),
				zap.Uint("node_id", nodeID))

			continue
		}

		if pending, ok := currentPendingRemove[nodeID]; ok {
			err := store.SetNodeStatus(ctx, pending.SubscriptionID, pending.NodeID, database.SyncStatusPendingAdd)
			if err != nil {
				stopErr := nodeError("reactivate pending_remove", nodeID, err)
				if stopErr != nil {
					return stopErr
				}

				continue
			}

			logger.Debug("reactivated pending_remove to pending_add",
				zap.Uint("subscription_id", subscriptionID),
				zap.Uint("node_id", nodeID))

			continue
		}

		err := store.UpsertPendingAdd(ctx, &database.SubscriptionNode{
			SubscriptionID: subscriptionID,
			NodeID:         nodeID,
			Status:         database.SyncStatusPendingAdd,
		})
		if err != nil {
			stopErr := nodeError("upsert pending_add", nodeID, err)
			if stopErr != nil {
				return stopErr
			}

			continue
		}

		logger.Debug("created pending_add node",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", nodeID))
	}

	// REMOVE paths: any record not in targetSet transitions to pending_remove.
	for nodeID, sn := range currentActive {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}

		err := store.SetNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove)
		if err != nil {
			stopErr := nodeError("set pending_remove", nodeID, err)
			if stopErr != nil {
				return stopErr
			}

			continue
		}

		logger.Debug("set pending_remove for active node not in target",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", nodeID))
	}

	for nodeID, sn := range currentPendingAdd {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}

		err := store.SetNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove)
		if err != nil {
			stopErr := nodeError("set pending_remove for stale pending_add", nodeID, err)
			if stopErr != nil {
				return stopErr
			}

			continue
		}

		logger.Debug("set pending_remove for stale pending_add node",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", nodeID))
	}

	for nodeID, sn := range currentPendingUpdate {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}

		err := store.SetNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove)
		if err != nil {
			stopErr := nodeError("set pending_remove for stale pending_update", nodeID, err)
			if stopErr != nil {
				return stopErr
			}

			continue
		}

		logger.Debug("set pending_remove for stale pending_update node",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", nodeID))
	}

	return nil
}

// dbServicePlanStore adapts the reconciliation core to interfaces.SyncStore
// for the background best-effort sync path.
type dbServicePlanStore struct {
	db interfaces.SyncStore
}

func (s dbServicePlanStore) LoadSubscription(ctx context.Context, subscriptionID uint) (*database.Subscription, error) {
	return s.db.GetByID(ctx, subscriptionID)
}

func (s dbServicePlanStore) LoadPlanNodes(ctx context.Context, planID uint) ([]database.Node, error) {
	return s.db.GetNodesByPlanID(ctx, planID)
}

func (s dbServicePlanStore) LoadSubscriptionNodes(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	return s.db.GetBySubscriptionID(ctx, subscriptionID)
}

func (s dbServicePlanStore) MarkActiveNodesPendingUpdate(ctx context.Context, subscriptionID uint, targetNodeIDs []uint) error {
	return s.db.MarkActiveNodesPendingUpdate(ctx, subscriptionID, targetNodeIDs)
}

func (s dbServicePlanStore) SetNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error {
	return s.db.UpdateSubscriptionNodeStatus(ctx, subID, nodeID, status)
}

func (s dbServicePlanStore) UpsertPendingAdd(ctx context.Context, sn *database.SubscriptionNode) error {
	return s.db.UpsertSubscriptionNode(ctx, sn)
}

// txPlanStore adapts the reconciliation core to a caller-supplied *gorm.DB
// for the transactional payment CAS path. The SQL mirrors the corresponding
// database.Service statements so both paths persist identical rows; all
// reads/writes execute inside the caller's transaction.
type txPlanStore struct {
	tx *gorm.DB
}

func (s txPlanStore) LoadSubscription(ctx context.Context, subscriptionID uint) (*database.Subscription, error) {
	var sub database.Subscription

	err := s.tx.WithContext(ctx).First(&sub, subscriptionID).Error
	if err != nil {
		return nil, err
	}

	return &sub, nil
}

func (s txPlanStore) LoadPlanNodes(ctx context.Context, planID uint) ([]database.Node, error) {
	var targetNodes []database.Node

	err := s.tx.WithContext(ctx).
		Table("nodes").
		Select("nodes.*").
		Joins("JOIN plan_nodes ON plan_nodes.node_id = nodes.id").
		Where("plan_nodes.plan_id = ? AND nodes.is_active = ?", planID, true).
		Find(&targetNodes).Error
	if err != nil {
		return nil, err
	}

	return targetNodes, nil
}

func (s txPlanStore) LoadSubscriptionNodes(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	var currentNodes []database.SubscriptionNode

	err := s.tx.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		Find(&currentNodes).Error
	if err != nil {
		return nil, err
	}

	return currentNodes, nil
}

func (s txPlanStore) MarkActiveNodesPendingUpdate(ctx context.Context, subscriptionID uint, targetNodeIDs []uint) error {
	if len(targetNodeIDs) == 0 {
		return nil
	}

	return s.tx.WithContext(ctx).Model(&database.SubscriptionNode{}).
		Where("subscription_id = ? AND node_id IN (?) AND status = ?", subscriptionID, targetNodeIDs, database.SyncStatusActive).
		Updates(map[string]any{
			"status":      database.SyncStatusPendingUpdate,
			"retry_count": 0,
			"retry_at":    nil,
			"last_error":  nil,
		}).Error
}

func (s txPlanStore) SetNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error {
	return s.tx.WithContext(ctx).Model(&database.SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", subID, nodeID).
		Updates(map[string]any{
			"status":      status,
			"retry_count": 0,
			"retry_at":    nil,
			"last_error":  nil,
		}).Error
}

func (s txPlanStore) UpsertPendingAdd(ctx context.Context, sn *database.SubscriptionNode) error {
	return s.tx.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "subscription_id"}, {Name: "node_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"status", "retry_count", "retry_at", "last_error", "updated_at"}),
		}).
		Create(sn).Error
}
