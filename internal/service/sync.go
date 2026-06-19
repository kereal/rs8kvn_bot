package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"go.uber.org/zap"
)

// SyncService manages the synchronization of subscriptions with VPN nodes.
type SyncService struct {
	db          interfaces.DatabaseService
	vpnClients  map[uint]vpn.Client
	nodes       []database.Node
}

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
			continue
		}
		if err := s.db.UpsertSubscriptionNode(ctx, &database.SubscriptionNode{
			SubscriptionID: subscriptionID,
			NodeID:         target.ID,
			Status:         database.SyncStatusPendingAdd,
		}); err != nil {
			return fmt.Errorf("recalculate nodes: upsert pending_add node %d: %w", target.ID, err)
		}
	}

	for nodeID, sn := range currentActive {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}
		if err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove); err != nil {
			return fmt.Errorf("recalculate nodes: set pending_remove node %d: %w", nodeID, err)
		}
	}

	for nodeID, sn := range currentPendingAdd {
		if _, inTarget := targetSet[nodeID]; inTarget {
			continue
		}
		if err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID); err != nil {
			return fmt.Errorf("recalculate nodes: delete stale pending_add node %d: %w", nodeID, err)
		}
	}

	return nil
}

// SyncSubscription performs pending VPN operations for the given subscription.
func (s *SyncService) SyncSubscription(ctx context.Context, subscriptionID uint) error {
	pending, err := s.db.GetBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("sync subscription: load nodes: %w", err)
	}

	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("sync subscription: load subscription: %w", err)
	}

	return s.syncNodes(ctx, sub, pending)
}

func (s *SyncService) syncNodes(ctx context.Context, sub *database.Subscription, pending []database.SubscriptionNode) error {
	if sub == nil {
		return fmt.Errorf("sync subscription: nil subscription")
	}

	var lastErr error
	for _, sn := range pending {
		switch sn.Status {
		case database.SyncStatusPendingAdd:
			if err := s.processPendingAdd(ctx, &sn, sub); err != nil {
				logger.Warn("pending_add failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Uint("node_id", sn.NodeID),
					zap.Error(err))
				lastErr = err
			}
		case database.SyncStatusPendingRemove:
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

func (s *SyncService) processPendingAdd(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	client, ok := s.vpnClients[sn.NodeID]
	if !ok {
		return fmt.Errorf("no VPN client for node %d", sn.NodeID)
	}

	if err := client.CreateSubscription(ctx, sub.ClientID, syncIdentifier(sub)); err != nil {
		s.handleSyncError(sn, err)
		return err
	}

	if err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusActive); err != nil {
		return fmt.Errorf("mark active: %w", err)
	}
	return nil
}

func (s *SyncService) processPendingRemove(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	client, ok := s.vpnClients[sn.NodeID]
	if !ok {
		return fmt.Errorf("no VPN client for node %d", sn.NodeID)
	}

	if err := client.DeleteSubscription(ctx, sub.ClientID, syncIdentifier(sub)); err != nil {
		s.handleSyncError(sn, err)
		return err
	}

	if err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID); err != nil {
		return fmt.Errorf("delete subscription node: %w", err)
	}
	return nil
}

func (s *SyncService) handleSyncError(sn *database.SubscriptionNode, err error) {
	sn.RetryCount++
	errMsg := err.Error()
	sn.LastError = &errMsg
	retryAt := CalculateRetryAt(sn.RetryCount)
	sn.RetryAt = &retryAt

	_ = s.db.UpdateRetry(nil, sn.SubscriptionID, sn.NodeID, sn.RetryCount, sn.RetryAt, sn.LastError)
}

// CalculateRetryAt returns the next retry timestamp for the given retry count.
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
	pendingNodes, err := s.db.GetPendingSync(ctx)
	if err != nil {
		return fmt.Errorf("sync pending nodes: %w", err)
	}

	if len(pendingNodes) == 0 {
		return nil
	}

	groups := make(map[uint][]database.SubscriptionNode, 0)
	for _, sn := range pendingNodes {
		groups[sn.SubscriptionID] = append(groups[sn.SubscriptionID], sn)
	}

	var lastErr error
	for subID, nodes := range groups {
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
				zap.Uint("subscription_id", subID),
				zap.Int("pending_nodes", len(nodes)),
				zap.Error(err))
			lastErr = err
		}
	}

	return lastErr
}
