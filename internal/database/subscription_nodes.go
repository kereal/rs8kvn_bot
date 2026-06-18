package database

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

// GetBySubscriptionID returns subscription node records for the given subscription.
func (s *Service) GetBySubscriptionID(ctx context.Context, subscriptionID uint) ([]SubscriptionNode, error) {
	var rows []SubscriptionNode
	result := s.db.WithContext(ctx).Where("subscription_id = ?", subscriptionID).Find(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get subscription nodes: %w", result.Error)
	}
	return rows, nil
}

// GetByNodeID returns subscription node records for the given node.
func (s *Service) GetByNodeID(ctx context.Context, nodeID uint) ([]SubscriptionNode, error) {
	var rows []SubscriptionNode
	result := s.db.WithContext(ctx).Where("node_id = ?", nodeID).Find(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get subscription nodes by node id: %w", result.Error)
	}
	return rows, nil
}

// GetPendingSync returns subscription nodes awaiting synchronization, limited by retry window.
func (s *Service) GetPendingSync(ctx context.Context) ([]SubscriptionNode, error) {
	var rows []SubscriptionNode
	nowUTC := time.Now().UTC().Truncate(time.Minute)
	result := s.db.WithContext(ctx).
		Where("status IN ?", []SyncStatus{SyncStatusPendingAdd, SyncStatusPendingRemove}).
		Where("retry_at IS NULL OR retry_at <= ?", nowUTC).
		Find(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get pending sync nodes: %w", result.Error)
	}
	return rows, nil
}

// GetPendingByNodeID returns pending subscription nodes for the given node.
func (s *Service) GetPendingByNodeID(ctx context.Context, nodeID uint) ([]SubscriptionNode, error) {
	var rows []SubscriptionNode
	nowUTC := time.Now().UTC().Truncate(time.Minute)
	result := s.db.WithContext(ctx).
		Where("node_id = ? AND status IN ? AND (retry_at IS NULL OR retry_at <= ?)",
			nodeID, []SyncStatus{SyncStatusPendingAdd, SyncStatusPendingRemove}, nowUTC).
		Find(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get pending nodes by node id: %w", result.Error)
	}
	return rows, nil
}

// CreateSubscriptionNode inserts a new subscription node record.
func (s *Service) CreateSubscriptionNode(ctx context.Context, sn *SubscriptionNode) error {
	if err := s.db.WithContext(ctx).Create(sn).Error; err != nil {
		return fmt.Errorf("failed to create subscription node: %w", err)
	}
	return nil
}

// UpdateSubscriptionNodeStatus sets the status for the subscription-node pair.
func (s *Service) UpdateSubscriptionNodeStatus(ctx context.Context, subID, nodeID uint, status SyncStatus) error {
	result := s.db.WithContext(ctx).Model(&SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", subID, nodeID).
		Updates(map[string]interface{}{
			"status":     status,
			"retry_at":   nil,
			"last_error": nil,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription node status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("subscription node not found for sub_id=%d node_id=%d", subID, nodeID)
	}
	return nil
}

// UpsertSubscriptionNode inserts or updates a subscription node by its composite key.
func (s *Service) UpsertSubscriptionNode(ctx context.Context, sn *SubscriptionNode) error {
	result := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(sn)
	if result.Error != nil {
		return fmt.Errorf("failed to upsert subscription node: %w", result.Error)
	}
	return nil
}

// DeleteSubscriptionNode removes a subscription node record.
func (s *Service) DeleteSubscriptionNode(ctx context.Context, subID, nodeID uint) error {
	result := s.db.WithContext(ctx).
		Where("subscription_id = ? AND node_id = ?", subID, nodeID).
		Delete(&SubscriptionNode{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete subscription node: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("subscription node not found for sub_id=%d node_id=%d", subID, nodeID)
	}
	return nil
}

// UpdateRetry updates retry metadata for a subscription node.
func (s *Service) UpdateRetry(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error {
	result := s.db.WithContext(ctx).Model(&SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", subID, nodeID).
		Updates(map[string]interface{}{
			"retry_count": retryCount,
			"retry_at":    retryAt,
			"last_error":  lastErr,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update retry: %w", result.Error)
	}
	return nil
}
