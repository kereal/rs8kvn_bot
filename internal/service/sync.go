package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SyncService manages the synchronization of subscriptions with VPN nodes.
type SyncService struct {
	db         interfaces.DatabaseService
	vpnClients map[uint]vpn.Client
	nodes      []database.Node
	locks      sync.Map
}

type subscriptionSyncLock struct {
	mu sync.Mutex
}

// syncIdentifier builds a unique VPN client identifier from username and telegram ID.
func syncIdentifier(sub *database.Subscription) string {
	return XUIEmail(sub.Username, sub.TelegramID)
}

// NewSyncService creates a new SyncService.
func NewSyncService(db interfaces.DatabaseService, vpnClients map[uint]vpn.Client, nodes []database.Node) *SyncService {
	return &SyncService{db: db, vpnClients: vpnClients, nodes: nodes}
}

func (s *SyncService) lockSubscription(subscriptionID uint) func() {
	lockAny, _ := s.locks.LoadOrStore(subscriptionID, &subscriptionSyncLock{})
	lock := lockAny.(*subscriptionSyncLock)
	lock.mu.Lock()
	return lock.mu.Unlock
}

// ReconcilePlanNodes reconciles the subscription_nodes table against the current plan.
// It handles four cases:
//   - ADD: a plan node has no subscription_nodes record → pending_add
//   - CHANGED_LIMITS: an active node's plan limits differ from last provisioning → pending_add (triggers UpdateSubscription)
//   - REMOVE: a subscription_nodes node is no longer in the plan → pending_remove
//   - STALE: a pending_add node was removed from the plan → pending_remove
//
// This replaces the old RecalculateNodes which required an explicit oldPlanID
// and unconditionally re-provisioned all active nodes on every plan change.
func (s *SyncService) ReconcilePlanNodes(ctx context.Context, subscriptionID uint) error {
	logger.Debug("reconcile plan nodes",
		zap.Uint("subscription_id", subscriptionID))

	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("reconcile plan nodes: load subscription: %w", err)
	}

	targetNodes, err := s.db.GetNodesByPlanID(ctx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("reconcile plan nodes: load plan nodes: %w", err)
	}

	currentNodes, err := s.db.GetBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("reconcile plan nodes: load current nodes: %w", err)
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

	for _, target := range targetNodes {
		if _, exists := currentActive[target.ID]; exists {
			continue
		}
		if _, exists := currentPendingAdd[target.ID]; exists {
			continue
		}
		if _, exists := currentPendingUpdate[target.ID]; exists {
			logger.Debug("pending_update node in target plan, leaving as-is",
				zap.Uint("subscription_id", subscriptionID),
				zap.Uint("node_id", target.ID))
			continue
		}
		if pending, ok := currentPendingRemove[target.ID]; ok {
			if err := s.db.UpdateSubscriptionNodeStatus(ctx, pending.SubscriptionID, pending.NodeID, database.SyncStatusPendingAdd); err != nil {
				return fmt.Errorf("reconcile plan nodes: reactivate pending_remove node %d: %w", target.ID, err)
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
			return fmt.Errorf("reconcile plan nodes: upsert pending_add node %d: %w", target.ID, err)
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
			return fmt.Errorf("reconcile plan nodes: set pending_remove node %d: %w", nodeID, err)
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
			return fmt.Errorf("reconcile plan nodes: set pending_remove for stale pending_add node %d: %w", nodeID, err)
		}
		logger.Debug("set pending_remove for stale pending_add node",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", nodeID))
	}

	logger.Debug("reconcile plan nodes completed",
		zap.Uint("subscription_id", subscriptionID))

	return nil
}

// MarkAllForRemoval sets all subscription nodes (active, pending_add, pending_remove) to pending_remove status.
// Used before deleting a subscription to ensure VPN clients are removed via sync.
func (s *SyncService) MarkAllForRemoval(ctx context.Context, subscriptionID uint) error {
	logger.Debug("mark all nodes for removal",
		zap.Uint("subscription_id", subscriptionID))

	nodes, err := s.db.GetBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("mark all for removal: load nodes: %w", err)
	}

	for _, sn := range nodes {
		switch sn.Status {
		case database.SyncStatusActive, database.SyncStatusPendingAdd, database.SyncStatusPendingRemove, database.SyncStatusPendingUpdate:
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
// Background path: per-node failures are logged and processing continues; only returns error on DB/cancel failure.
func (s *SyncService) SyncSubscription(ctx context.Context, subscriptionID uint) error {
	unlock := s.lockSubscription(subscriptionID)
	defer unlock()

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

// syncNodes iterates over pending subscription nodes and dispatches add/remove/update operations.
// Individual node failures are logged; returns nil unless ctx is cancelled upstream.
func (s *SyncService) syncNodes(ctx context.Context, sub *database.Subscription, pending []database.SubscriptionNode) error {
	if sub == nil {
		return fmt.Errorf("sync subscription: nil subscription")
	}

	nodeTypes := make(map[uint]database.NodeType, len(s.nodes))
	for _, n := range s.nodes {
		nodeTypes[n.ID] = n.Type
	}

	logger.Debug("processing pending nodes",
		zap.Uint("subscription_id", sub.ID),
		zap.Int("pending_count", len(pending)))

	for _, sn := range pending {
		nodeType, hasNode := nodeTypes[sn.NodeID]
		if !hasNode {
			logger.Warn("node not found in runtime clients, skipping",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
			s.handleSyncError(ctx, &sn, fmt.Errorf("node not found in runtime clients"))
			continue
		}
		switch nodeType {
		case database.NodeType3xUI, database.NodeTypeProxman:
		default:
			logger.Warn("unsupported node type, skipping",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID),
				zap.String("node_type", string(nodeType)))
			s.handleSyncError(ctx, &sn, fmt.Errorf("unsupported node type %s", nodeType))
			continue
		}

		switch sn.Status {
		case database.SyncStatusPendingAdd:
			logger.Debug("processing pending_add",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
		if err := s.processPendingAdd(ctx, &sn, sub); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			logger.Warn("pending_add failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID),
				zap.Error(err))
		}
		case database.SyncStatusPendingRemove:
			logger.Debug("processing pending_remove",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
		if err := s.processPendingRemove(ctx, &sn, sub); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			logger.Warn("pending_remove failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID),
				zap.Error(err))
		}
		case database.SyncStatusPendingUpdate:
			logger.Debug("processing pending_update",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
		if err := s.processPendingUpdate(ctx, &sn, sub); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			logger.Warn("pending_update failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID),
				zap.Error(err))
		}
		}
	}

	return nil
}

func (s *SyncService) retryUnavailableNode(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription, operation string) error {
	node, nodeErr := s.db.GetNodeByID(ctx, sn.NodeID)
	if nodeErr != nil {
		err := fmt.Errorf("%s node %d unavailable: load node: %w", operation, sn.NodeID, nodeErr)
		logger.Warn("node unavailable for sync operation, keeping pending record",
			zap.String("operation", operation),
			zap.Uint("subscription_id", sub.ID),
			zap.Uint("node_id", sn.NodeID),
			zap.Error(err))
		s.handleSyncError(ctx, sn, err)
		return err
	}

	if !node.IsActive {
		err := fmt.Errorf("%s node %d unavailable: inactive=%t type=%s", operation, sn.NodeID, node.IsActive, node.Type)
		logger.Warn("node unsuitable for sync operation, keeping pending record",
			zap.String("operation", operation),
			zap.Uint("subscription_id", sub.ID),
			zap.Uint("node_id", sn.NodeID),
			zap.Bool("node_active", node.IsActive),
			zap.String("node_type", string(node.Type)),
			zap.Error(err))
		s.handleSyncError(ctx, sn, err)
		return err
	}

	err := fmt.Errorf("%s node %d unavailable: no VPN client", operation, sn.NodeID)
	s.handleSyncError(ctx, sn, err)
	return err
}

// processPendingUpdate updates an existing VPN subscription client configuration.
// Used when a plan change requires updating traffic limits or expiry on an active node.
func (s *SyncService) processPendingUpdate(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	client, ok := s.vpnClients[sn.NodeID]
	if !ok {
		return s.retryUnavailableNode(ctx, sn, sub, "pending_update")
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

	logger.Debug("updating VPN subscription",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	if err := client.UpdateSubscription(ctx, provision); err != nil {
		s.handleSyncError(ctx, sn, err)
		return fmt.Errorf("update VPN subscription node %d: %w", sn.NodeID, err)
	}

	if err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusActive); err != nil {
		return fmt.Errorf("mark active: %w", err)
	}
	logger.Debug("subscription updated and marked active",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))
	return nil
}

// processPendingAdd creates a VPN subscription on the target node.
// Handles idempotent "already exists" errors by synchronizing config and only then marking active.
func (s *SyncService) processPendingAdd(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	client, ok := s.vpnClients[sn.NodeID]
	if !ok {
		return s.retryUnavailableNode(ctx, sn, sub, "pending_add")
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
		if errors.Is(err, vpn.ErrSubscriptionAlreadyExists) {
			if updateErr := client.UpdateSubscription(ctx, provision); updateErr != nil {
				s.handleSyncError(ctx, sn, updateErr)
				return fmt.Errorf("update existing VPN subscription node %d: %w", sn.NodeID, updateErr)
			}
			logger.Info("client already exists on node, configuration updated/synchronized",
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
		return s.retryUnavailableNode(ctx, sn, sub, "pending_remove")
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
		if errors.Is(err, vpn.ErrSubscriptionNotFound) {
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
// Intervals: 1m → 2m → 5m → 15m → 30m → 45m → 60m (capped).
// After 6 failures further calls still get 60m and the record remains pending.
func CalculateRetryAt(retryCount int) time.Time {
	switch retryCount {
	case 0:
		return time.Now().UTC().Truncate(time.Minute).Add(1 * time.Minute)
	case 1:
		return time.Now().UTC().Truncate(time.Minute).Add(2 * time.Minute)
	case 2:
		return time.Now().UTC().Truncate(time.Minute).Add(5 * time.Minute)
	case 3:
		return time.Now().UTC().Truncate(time.Minute).Add(15 * time.Minute)
	case 4:
		return time.Now().UTC().Truncate(time.Minute).Add(30 * time.Minute)
	case 5:
		return time.Now().UTC().Truncate(time.Minute).Add(45 * time.Minute)
	default:
		return time.Now().UTC().Truncate(time.Minute).Add(60 * time.Minute)
	}
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

	var errs []error
	for subID, nodes := range groups {
		unlock := s.lockSubscription(subID)
		logger.Debug("processing subscription pending nodes",
			zap.Uint("subscription_id", subID),
			zap.Int("pending_nodes", len(nodes)))
		sub, subErr := s.db.GetByID(ctx, subID)
		if subErr != nil {
			if errors.Is(subErr, database.ErrSubscriptionNotFound) || errors.Is(subErr, gorm.ErrRecordNotFound) {
				if clErr := s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, subID); clErr != nil {
					logger.Warn("purge orphan subscription nodes failed",
						zap.Uint("subscription_id", subID),
						zap.Int("pending_nodes", len(nodes)),
						zap.Error(clErr))
				} else {
					logger.Info("purged orphan subscription nodes",
						zap.Uint("subscription_id", subID),
						zap.Int("pending_nodes", len(nodes)))
				}
			}
			err := fmt.Errorf("sync pending nodes: load subscription %d: %w", subID, subErr)
			errs = append(errs, err)
			logger.Warn("sync subscription failed",
				zap.Uint("subscription_id", subID),
				zap.Int("pending_nodes", len(nodes)),
				zap.Error(err))
			unlock()
			continue
		}

		if err := s.syncNodes(ctx, sub, nodes); err != nil {
			logger.Warn("sync subscription failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Int("pending_nodes", len(nodes)),
				zap.Error(err))
			errs = append(errs, err)
		}

		if pruneErr := s.ReconcilePlanNodes(ctx, sub.ID); pruneErr != nil {
			logger.Warn("reconcile plan nodes failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(pruneErr))
			errs = append(errs, pruneErr)
		}
		unlock()
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
