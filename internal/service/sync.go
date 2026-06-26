package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"go.uber.org/zap"
)

// SyncService manages the synchronization of subscriptions with VPN nodes.
type SyncService struct {
	db         interfaces.DatabaseService
	vpnClients map[uint]vpn.Client
	nodes      []database.Node
}

// isAlreadyExistsError returns true if the error indicates the VPN client already exists.
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "client already exists") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "already added")
}

// isNotFoundError returns true if the error indicates the VPN client was not found.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "client not found")
}

// syncIdentifier builds a unique VPN client identifier from username and telegram ID.
func syncIdentifier(sub *database.Subscription) string {
	return XUIEmail(sub.Username, sub.TelegramID)
}

// NewSyncService creates a new SyncService.
func NewSyncService(db interfaces.DatabaseService, vpnClients map[uint]vpn.Client, nodes []database.Node) *SyncService {
	return &SyncService{db: db, vpnClients: vpnClients, nodes: nodes}
}

// RecalculateNodes computes the diff between plan nodes and current subscription nodes.
// It only updates the database state, without invoking VPN operations.
func (s *SyncService) RecalculateNodes(ctx context.Context, subscriptionID uint) error {
	logger.Debug("recalculate nodes",
		zap.Uint("subscription_id", subscriptionID))

	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("recalculate nodes: load subscription: %w", err)
	}

	targetNodes, err := s.db.GetNodesByPlanID(ctx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("recalculate nodes: load plan nodes: %w", err)
	}

	currentNodes, err := s.db.GetBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("recalculate nodes: load current nodes: %w", err)
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
	for _, sn := range currentNodes {
		switch sn.Status {
		case database.SyncStatusActive:
			currentActive[sn.NodeID] = sn
		case database.SyncStatusPendingAdd:
			currentPendingAdd[sn.NodeID] = sn
		case database.SyncStatusPendingRemove:
			currentPendingRemove[sn.NodeID] = sn
		}
	}

	for _, target := range targetNodes {
		if _, exists := currentActive[target.ID]; exists {
			continue
		}
		if _, exists := currentPendingAdd[target.ID]; exists {
			continue
		}
		if pending, ok := currentPendingRemove[target.ID]; ok {
			if err := s.db.UpdateSubscriptionNodeStatus(ctx, pending.SubscriptionID, pending.NodeID, database.SyncStatusPendingAdd); err != nil {
				return fmt.Errorf("recalculate nodes: reactivate pending_remove node %d: %w", target.ID, err)
			}
			logger.Debug("reactivated pending_remove to pending_add",
				zap.Uint("subscription_id", subscriptionID),
				zap.Uint("node_id", target.ID))
			continue
		}
		if err := s.db.UpsertSubscriptionNode(ctx, &database.SubscriptionNode{
			SubscriptionID: subscriptionID,
			NodeID:         target.ID,
			Status:         database.SyncStatusPendingAdd,
		}); err != nil {
			return fmt.Errorf("recalculate nodes: upsert pending_add node %d: %w", target.ID, err)
		}
		logger.Debug("created pending_add node",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", target.ID))
	}

	for nodeID, sn := range currentActive {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}
		if err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove); err != nil {
			return fmt.Errorf("recalculate nodes: set pending_remove node %d: %w", nodeID, err)
		}
		logger.Debug("set pending_remove for active node not in target",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", nodeID))
	}

	for nodeID, sn := range currentPendingAdd {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}
		if err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove); err != nil {
			return fmt.Errorf("recalculate nodes: set pending_remove for stale pending_add node %d: %w", nodeID, err)
		}
		logger.Debug("set pending_remove for stale pending_add node",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", nodeID))
	}

	logger.Debug("recalculate nodes completed",
		zap.Uint("subscription_id", subscriptionID))

	return nil
}

// MarkAllForRemoval sets all active subscription nodes to pending_remove status.
// Used before deleting a subscription to ensure VPN clients are removed via sync.
func (s *SyncService) MarkAllForRemoval(ctx context.Context, subscriptionID uint) error {
	logger.Debug("mark all nodes for removal",
		zap.Uint("subscription_id", subscriptionID))

	nodes, err := s.db.GetBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("mark all for removal: load nodes: %w", err)
	}

	for _, sn := range nodes {
		if sn.Status == database.SyncStatusActive {
			if err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove); err != nil {
				return fmt.Errorf("mark all for removal: set pending_remove node %d: %w", sn.NodeID, err)
			}
			logger.Debug("marked node for removal",
				zap.Uint("subscription_id", subscriptionID),
				zap.Uint("node_id", sn.NodeID))
		}
	}

	logger.Debug("mark all for removal completed",
		zap.Uint("subscription_id", subscriptionID),
		zap.Int("nodes_processed", len(nodes)))

	return nil
}

// SyncSubscription performs pending VPN operations for the given subscription.
func (s *SyncService) SyncSubscription(ctx context.Context, subscriptionID uint) error {
	logger.Debug("sync subscription",
		zap.Uint("subscription_id", subscriptionID))

	pending, err := s.db.GetPendingBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("sync subscription: load nodes: %w", err)
	}

	if len(pending) == 0 {
		logger.Debug("sync subscription: no pending nodes",
			zap.Uint("subscription_id", subscriptionID))
		return nil
	}

	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("sync subscription: load subscription: %w", err)
	}

	return s.syncNodes(ctx, sub, pending)
}

// syncNodes iterates over pending subscription nodes and dispatches add/remove operations.
// Continues on individual failures, returning the last error encountered.
func (s *SyncService) syncNodes(ctx context.Context, sub *database.Subscription, pending []database.SubscriptionNode) error {
	if sub == nil {
		return fmt.Errorf("sync subscription: nil subscription")
	}

	logger.Debug("processing pending nodes",
		zap.Uint("subscription_id", sub.ID),
		zap.Int("pending_count", len(pending)))

	var lastErr error
	for _, sn := range pending {
		switch sn.Status {
		case database.SyncStatusPendingAdd:
			logger.Debug("processing pending_add",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
			if err := s.processPendingAdd(ctx, &sn, sub); err != nil {
				logger.Warn("pending_add failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Uint("node_id", sn.NodeID),
					zap.Error(err))
				lastErr = err
			}
		case database.SyncStatusPendingRemove:
			logger.Debug("processing pending_remove",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
			if err := s.processPendingRemove(ctx, &sn, sub); err != nil {
				logger.Warn("pending_remove failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Uint("node_id", sn.NodeID),
					zap.Error(err))
				lastErr = err
			}
		}
	}

	return lastErr
}

// processPendingAdd creates a VPN subscription on the target node.
// Handles idempotent "already exists" errors by marking the node active.
func (s *SyncService) processPendingAdd(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	client, ok := s.vpnClients[sn.NodeID]
	if !ok {
		node, nodeErr := s.db.GetNodeByID(ctx, sn.NodeID)
		if nodeErr != nil || !node.IsActive || node.Type != database.NodeType3xUI {
			logger.Info("node unavailable for pending_add, deleting record",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID),
				zap.Error(nodeErr))
			if err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID); err != nil {
				return fmt.Errorf("delete subscription node: %w", err)
			}
			return nil
		}
		err := fmt.Errorf("no VPN client for node %d", sn.NodeID)
		s.handleSyncError(ctx, sn, err)
		return err
	}

	plan, err := s.db.GetPlanByID(ctx, sub.PlanID)
	if err != nil {
		s.handleSyncError(ctx, sn, err)
		return fmt.Errorf("load plan for subscription sync: %w", err)
	}

	provision := vpn.SubscriptionProvision{
		ClientID:     sub.ClientID,
		Username:     syncIdentifier(sub),
		SubID:        sub.SubscriptionID,
		TrafficBytes: plan.TrafficLimit,
	}
	if plan.TrafficLimit > 0 {
		provision.ResetDays = -1
		if sub.ExpiresAt != nil {
			provision.ExpiryTime = *sub.ExpiresAt
		} else {
			provision.ExpiryTime = time.Now().Truncate(time.Minute).AddDate(0, 0, config.SubscriptionResetDay)
		}
	}

	logger.Debug("creating VPN subscription",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	if err := client.CreateSubscription(ctx, provision); err != nil {
		if isAlreadyExistsError(err) {
			logger.Info("client already exists on node, treating as success",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
			if err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusActive); err != nil {
				return fmt.Errorf("mark active: %w", err)
			}
			return nil
		}
		s.handleSyncError(ctx, sn, err)
		return err
	}

	if err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusActive); err != nil {
		return fmt.Errorf("mark active: %w", err)
	}
	logger.Debug("subscription created and marked active",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))
	return nil
}

// processPendingRemove deletes a VPN subscription from the target node.
// Handles idempotent "not found" errors by removing the DB record.
func (s *SyncService) processPendingRemove(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	client, ok := s.vpnClients[sn.NodeID]
	if !ok {
		node, nodeErr := s.db.GetNodeByID(ctx, sn.NodeID)
		if nodeErr != nil || !node.IsActive || node.Type != database.NodeType3xUI {
			logger.Info("node unavailable for pending_remove, deleting record",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID),
				zap.Error(nodeErr))
			if err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID); err != nil {
				return fmt.Errorf("delete subscription node: %w", err)
			}
			return nil
		}
		err := fmt.Errorf("no VPN client for node %d", sn.NodeID)
		s.handleSyncError(ctx, sn, err)
		return err
	}

	provision := vpn.SubscriptionProvision{
		ClientID: sub.ClientID,
		Username: syncIdentifier(sub),
		SubID:    sub.SubscriptionID,
	}

	logger.Debug("deleting VPN subscription",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	if err := client.DeleteSubscription(ctx, provision); err != nil {
		if isNotFoundError(err) {
			logger.Info("client not found on node, treating as success",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
			if err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID); err != nil {
				return fmt.Errorf("delete subscription node: %w", err)
			}
			return nil
		}
		s.handleSyncError(ctx, sn, err)
		return err
	}

	logger.Debug("subscription deleted from node",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	if err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID); err != nil {
		return fmt.Errorf("delete subscription node: %w", err)
	}
	return nil
}

// handleSyncError updates retry metadata for a failed sync operation.
// Increments RetryCount and schedules next attempt via exponential backoff.
func (s *SyncService) handleSyncError(ctx context.Context, sn *database.SubscriptionNode, err error) {
	logger.Warn("sync error, scheduling retry",
		zap.Uint("subscription_id", sn.SubscriptionID),
		zap.Uint("node_id", sn.NodeID),
		zap.Int("retry_count", sn.RetryCount),
		zap.Error(err))
	sn.RetryCount++
	errMsg := err.Error()
	sn.LastError = &errMsg
	retryAt := CalculateRetryAt(sn.RetryCount)
	sn.RetryAt = &retryAt

	if dbErr := s.db.UpdateRetry(ctx, sn.SubscriptionID, sn.NodeID, sn.RetryCount, sn.RetryAt, sn.LastError); dbErr != nil {
		logger.Warn("failed to update retry metadata",
			zap.Uint("subscription_id", sn.SubscriptionID),
			zap.Uint("node_id", sn.NodeID),
			zap.Error(dbErr))
	}
}

// CalculateRetryAt returns the next retry timestamp for the given retry count.
// Retry intervals: 1st=5min, 2nd=15min, 3rd=1h, 4+=6h. Prevents hammering failing nodes.
func CalculateRetryAt(retryCount int) time.Time {
	interval := 1 * time.Minute
	switch retryCount {
	case 1:
		interval = 5 * time.Minute
	case 2:
		interval = 15 * time.Minute
	case 3:
		interval = 1 * time.Hour
	default:
		if retryCount >= 4 {
			interval = 6 * time.Hour
		}
	}

	return time.Now().UTC().Truncate(time.Minute).Add(interval)
}

// SyncPendingNodes fetches pending nodes across all subscriptions and processes them.
func (s *SyncService) SyncPendingNodes(ctx context.Context) error {
	logger.Debug("sync pending nodes started")

	pendingNodes, err := s.db.GetPendingSync(ctx)
	if err != nil {
		return fmt.Errorf("sync pending nodes: %w", err)
	}

	logger.Debug("pending nodes fetched",
		zap.Int("total_pending", len(pendingNodes)))

	if len(pendingNodes) == 0 {
		return nil
	}

	groups := make(map[uint][]database.SubscriptionNode, 0)
	for _, sn := range pendingNodes {
		groups[sn.SubscriptionID] = append(groups[sn.SubscriptionID], sn)
	}

	logger.Debug("pending nodes grouped",
		zap.Int("subscriptions_count", len(groups)))

	var lastErr error
	for subID, nodes := range groups {
		logger.Debug("processing subscription pending nodes",
			zap.Uint("subscription_id", subID),
			zap.Int("pending_nodes", len(nodes)))
		sub, subErr := s.db.GetByID(ctx, subID)
		if subErr != nil {
			lastErr = fmt.Errorf("sync pending nodes: load subscription %d: %w", subID, subErr)
			logger.Warn("sync subscription failed",
				zap.Uint("subscription_id", subID),
				zap.Int("pending_nodes", len(nodes)),
				zap.Error(lastErr))
			continue
		}

		if err := s.syncNodes(ctx, sub, nodes); err != nil {
			logger.Warn("sync subscription failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Int("pending_nodes", len(nodes)),
				zap.Error(err))
			lastErr = err
		}

		if pruneErr := s.RecalculateNodes(ctx, sub.ID); pruneErr != nil {
			logger.Warn("recalculate nodes failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(pruneErr))
			lastErr = pruneErr
		}
	}

	logger.Debug("sync pending nodes completed",
		zap.Int("subscriptions_processed", len(groups)))

	return lastErr
}
