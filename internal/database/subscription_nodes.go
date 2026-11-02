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

// GetPendingSync returns subscription nodes awaiting synchronization, limited by retry window.
func (s *Service) GetPendingSync(ctx context.Context) ([]SubscriptionNode, error) {
	var rows []SubscriptionNode

	nowUTC := time.Now().UTC().Truncate(time.Minute)

	result := s.db.WithContext(ctx).
		Where("status IN ?", []SyncStatus{SyncStatusPendingAdd, SyncStatusPendingRemove, SyncStatusPendingUpdate}).
		Where("retry_at IS NULL OR retry_at <= ?", nowUTC).
		Find(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get pending sync nodes: %w", result.Error)
	}

	return rows, nil
}

// GetPendingBySubscriptionID returns pending subscription nodes for the given subscription.
func (s *Service) GetPendingBySubscriptionID(ctx context.Context, subscriptionID uint) ([]SubscriptionNode, error) {
	var rows []SubscriptionNode

	nowUTC := time.Now().UTC().Truncate(time.Minute)

	result := s.db.WithContext(ctx).
		Where("subscription_id = ? AND status IN ? AND (retry_at IS NULL OR retry_at <= ?)",
			subscriptionID, []SyncStatus{SyncStatusPendingAdd, SyncStatusPendingRemove, SyncStatusPendingUpdate}, nowUTC).
		Find(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get pending nodes by subscription id: %w", result.Error)
	}

	return rows, nil
}

// CreateSubscriptionNode inserts a new subscription node record.
func (s *Service) CreateSubscriptionNode(ctx context.Context, sn *SubscriptionNode) error {
	err := s.db.WithContext(ctx).Create(sn).Error
	if err != nil {
		return fmt.Errorf("failed to create subscription node: %w", err)
	}

	return nil
}

// UpdateSubscriptionNodeStatus sets the status for the subscription-node pair.
func (s *Service) UpdateSubscriptionNodeStatus(ctx context.Context, subID, nodeID uint, status SyncStatus) error {
	result := s.db.WithContext(ctx).Model(&SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", subID, nodeID).
		Updates(map[string]any{
			"status":      status,
			"retry_count": 0,
			"retry_at":    nil,
			"last_error":  nil,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription node status: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("subscription node not found for sub_id=%d node_id=%d: %w", subID, nodeID, ErrSubscriptionNodeNotFound)
	}

	return nil
}

// UpsertSubscriptionNode inserts or updates a subscription node by its composite key.
// Only mutable fields (status, retry_count, retry_at, last_error) are updated on conflict.
func (s *Service) UpsertSubscriptionNode(ctx context.Context, sn *SubscriptionNode) error {
	result := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "subscription_id"}, {Name: "node_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"status", "retry_count", "retry_at", "last_error", "updated_at"}),
		}).
		Create(sn)
	if result.Error != nil {
		return fmt.Errorf("failed to upsert subscription node: %w", result.Error)
	}

	return nil
}

// MarkActiveNodesPendingUpdate sets status to pending_update for active nodes
// that belong to the given target node IDs. Used when a plan change requires
// updating existing VPN client configurations.
func (s *Service) MarkActiveNodesPendingUpdate(ctx context.Context, subID uint, targetNodeIDs []uint) error {
	if len(targetNodeIDs) == 0 {
		return nil
	}

	result := s.db.WithContext(ctx).Model(&SubscriptionNode{}).
		Where("subscription_id = ? AND node_id IN (?) AND status = ?", subID, targetNodeIDs, SyncStatusActive).
		Updates(map[string]any{
			"status":      SyncStatusPendingUpdate,
			"retry_count": 0,
			"retry_at":    nil,
			"last_error":  nil,
		})
	if result.Error != nil {
		return fmt.Errorf("mark active nodes pending update: %w", result.Error)
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
		return fmt.Errorf("subscription node not found for sub_id=%d node_id=%d: %w", subID, nodeID, ErrSubscriptionNodeNotFound)
	}

	return nil
}

// DeleteSubscriptionNodesBySubscriptionID removes all subscription node records for a given subscription.
func (s *Service) DeleteSubscriptionNodesBySubscriptionID(ctx context.Context, subID uint) error {
	result := s.db.WithContext(ctx).
		Where("subscription_id = ?", subID).
		Delete(&SubscriptionNode{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete subscription nodes for sub_id=%d: %w", subID, result.Error)
	}

	return nil
}

// UpdateRetry updates retry metadata for a subscription node.
func (s *Service) UpdateRetry(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error {
	result := s.db.WithContext(ctx).Model(&SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", subID, nodeID).
		Updates(map[string]any{
			"retry_count": retryCount,
			"retry_at":    retryAt,
			"last_error":  lastErr,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update retry: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("subscription node not found for sub_id=%d node_id=%d: %w", subID, nodeID, ErrSubscriptionNodeNotFound)
	}

	return nil
}

// GetNodesWithParkedSync возвращает ID нод, у которых есть запаркованные
// биндинги — записи с retry_at дальше parkedAfter (парковка ставит дату в 9999
// году, обычная ступень ладдера не превышает 60 минут).
func (s *Service) GetNodesWithParkedSync(ctx context.Context, parkedAfter time.Time) ([]uint, error) {
	var nodeIDs []uint

	result := s.db.WithContext(ctx).
		Model(&SubscriptionNode{}).
		Where("retry_at > ?", parkedAfter).
		Distinct().
		Pluck("node_id", &nodeIDs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list nodes with parked sync: %w", result.Error)
	}

	return nodeIDs, nil
}

// ReleaseParkedNodesOnNode снимает парковку с биндингов ноды: сбрасывает
// retry-состояние, чтобы ближайший проход синка подхватил записи снова.
// Возвращает число освобождённых записей.
func (s *Service) ReleaseParkedNodesOnNode(ctx context.Context, nodeID uint, parkedAfter time.Time) (int64, error) {
	result := s.db.WithContext(ctx).
		Model(&SubscriptionNode{}).
		Where("node_id = ? AND retry_at > ?", nodeID, parkedAfter).
		Updates(map[string]any{
			"retry_count": 0,
			"retry_at":    nil,
			"last_error":  nil,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to release parked sync for node %d: %w", nodeID, result.Error)
	}

	return result.RowsAffected, nil
}

// CountSubscriptionNodesByStatus возвращает число биндингов по статусам синка.
func (s *Service) CountSubscriptionNodesByStatus(ctx context.Context) (map[string]int64, error) {
	var rows []struct {
		Status string
		Count  int64
	}

	result := s.db.WithContext(ctx).
		Model(&SubscriptionNode{}).
		Select("status, COUNT(*) AS count").
		Group("status").
		Scan(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to count subscription nodes by status: %w", result.Error)
	}

	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Status] = row.Count
	}

	return counts, nil
}

// CountParkedSubscriptionNodes возвращает число запаркованных биндингов.
func (s *Service) CountParkedSubscriptionNodes(ctx context.Context, parkedAfter time.Time) (int64, error) {
	var count int64

	result := s.db.WithContext(ctx).
		Model(&SubscriptionNode{}).
		Where("retry_at > ?", parkedAfter).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count parked sync nodes: %w", result.Error)
	}

	return count, nil
}
