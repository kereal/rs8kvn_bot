package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// GetByTelegramID retrieves an active subscription by Telegram ID.
func (s *Service) GetByTelegramID(ctx context.Context, telegramID int64) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).
		Where("telegram_id = ? AND status = ?", telegramID, "active").
		Order("created_at DESC").
		First(&sub)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription by telegram ID: %w", result.Error)
	}
	return &sub, nil
}

// GetByID retrieves a subscription by its database ID.
func (s *Service) GetByID(ctx context.Context, id uint) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).First(&sub, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription: %w", result.Error)
	}
	return &sub, nil
}

// CreateSubscription creates a new subscription.
// If inviteCode is non-empty and resolves to a valid Invite, sub.InviteCode and sub.ReferredBy
// are populated atomically inside the same transaction.
func (s *Service) CreateSubscription(ctx context.Context, sub *Subscription, inviteCode string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if inviteCode != "" {
			var inv Invite
			if err := tx.Where("code = ?", inviteCode).First(&inv).Error; err == nil {
				sub.InviteCode = inviteCode
				sub.ReferredBy = inv.ReferrerTGID
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("failed to resolve invite: %w", err)
			}
		}

		if err := tx.Create(sub).Error; err != nil {
			return fmt.Errorf("failed to create new subscription: %w", err)
		}

		return nil
	})
}

// UpdateSubscription updates an existing subscription.
func (s *Service) UpdateSubscription(ctx context.Context, sub *Subscription) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("id = ?", sub.ID).
		Select("telegram_id", "username", "client_id", "subscription_id", "expires_at", "status", "invite_code", "plan_id", "referred_by", "devices", "ips", "product_id", "started_at", "price_paid_cents", "currency").
		Updates(sub)
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription: %w", result.Error)
	}
	return nil
}

// DeleteSubscription deletes a subscription.
func (s *Service) DeleteSubscription(ctx context.Context, telegramID int64) error {
	result := s.db.WithContext(ctx).Where("telegram_id = ?", telegramID).Delete(&Subscription{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete subscription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("no subscription found for telegram_id %d", telegramID)
	}
	return nil
}

// DeleteSubscriptionByID soft-deletes a subscription by its database ID.
func (s *Service) DeleteSubscriptionByID(ctx context.Context, id uint) (*Subscription, error) {
	var deleted Subscription
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.First(&deleted, id)
		if result.Error != nil {
			return result.Error
		}

		result = tx.Delete(&deleted)
		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to find subscription: %w", err)
		}
		return nil, fmt.Errorf("failed to delete subscription: %w", err)
	}

	return &deleted, nil
}

// GetLatestSubscriptions retrieves the latest N subscriptions ordered by creation date.
func (s *Service) GetLatestSubscriptions(ctx context.Context, limit int) ([]Subscription, error) {
	var subs []Subscription
	result := s.db.WithContext(ctx).
		Where("status = ?", "active").
		Order("created_at DESC").
		Limit(limit).
		Find(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get latest subscriptions: %w", result.Error)
	}
	return subs, nil
}

// GetAllSubscriptions retrieves all subscriptions (for admin stats and reconciliation).
func (s *Service) GetAllSubscriptions(ctx context.Context) ([]Subscription, error) {
	var subs []Subscription
	result := s.db.WithContext(ctx).Find(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get all subscriptions: %w", result.Error)
	}
	return subs, nil
}

// CountAllSubscriptions returns the total number of subscriptions.
func (s *Service) CountAllSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count all subscriptions: %w", result.Error)
	}
	return count, nil
}

// CountActiveSubscriptions returns the number of active subscriptions.
func (s *Service) CountActiveSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Where("status = ?", "active").
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count active subscriptions: %w", result.Error)
	}
	return count, nil
}

// CountExpiredSubscriptions returns the number of expired subscriptions.
func (s *Service) CountExpiredSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Where("expires_at <= ?", time.Now()).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count expired subscriptions: %w", result.Error)
	}
	return count, nil
}

// GetSubscriptionBySubscriptionID returns a subscription by its subscription ID.
func (s *Service) GetSubscriptionBySubscriptionID(ctx context.Context, subscriptionID string) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).Where("subscription_id = ?", subscriptionID).First(&sub)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription by subscription_id: %w", result.Error)
	}
	return &sub, nil
}

// GetSubscriptionStatus returns only the status and expiry time for a subscription
// by its subscription_id. It is intended for cheap cache-hit checks in the
// subscription server (since v2.3.0) — it avoids the full JOIN with plans and
// sources required by GetSubscriptionWithPlanAndNodes. Returns
// gorm.ErrRecordNotFound if no row matches.
func (s *Service) GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error) {
	var row struct {
		Status    string
		ExpiresAt time.Time
	}
	result := s.db.WithContext(ctx).
		Table("subscriptions").
		Select("status, expires_at").
		Where("subscription_id = ?", subscriptionID).
		Scan(&row)
	if result.Error != nil {
		return "", time.Time{}, result.Error
	}
	if result.RowsAffected == 0 {
		return "", time.Time{}, gorm.ErrRecordNotFound
	}
	return row.Status, row.ExpiresAt, nil
}

// GetSubscriptionWithPlanAndNodes returns a subscription (status=active) by subscription ID
// together with its plan and active nodes, via JOINs through plan_nodes.
func (s *Service) GetSubscriptionWithPlanAndNodes(ctx context.Context, subscriptionID string) (*SubscriptionFull, error) {
	var result SubscriptionFull

	subQuery := s.db.WithContext(ctx).Where("subscription_id = ? AND status = ?", subscriptionID, "active")

	if err := subQuery.First(&result.Subscription).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	if err := s.db.WithContext(ctx).First(&result.Plan, result.Subscription.PlanID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("failed to get plan: %w", err)
	}

	if err := s.db.WithContext(ctx).
		Table("nodes").
		Select("nodes.*").
		Joins("JOIN plan_nodes ON plan_nodes.node_id = nodes.id").
		Where("plan_nodes.plan_id = ? AND nodes.is_active = ?", result.Plan.ID, true).
		Find(&result.Nodes).Error; err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	return &result, nil
}

// UpdateSubscriptionDevices updates only the devices JSON column for a subscription.
func (s *Service) UpdateSubscriptionDevices(ctx context.Context, id uint, devicesJSON string) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).Where("id = ?", id).Update("devices", devicesJSON)
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription devices: %w", result.Error)
	}
	return nil
}

// UpdateSubscriptionIPs updates only the ips JSON column for a subscription.
func (s *Service) UpdateSubscriptionIPs(ctx context.Context, id uint, ipsJSON string) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).Where("id = ?", id).Update("ips", ipsJSON)
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription ips: %w", result.Error)
	}
	return nil
}

// ExpireSubscription downgrades the subscription to the free plan and clears expires_at.
func (s *Service) ExpireSubscription(ctx context.Context, id uint, freePlanID uint) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     "active",
			"expires_at": nil,
			"plan_id":    freePlanID,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to expire subscription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// GetExpiredPaidSubscriptions returns active subscriptions that have expired and are not on the free plan.
func (s *Service) GetExpiredPaidSubscriptions(ctx context.Context) ([]Subscription, error) {
	var subs []Subscription
	freePlanSubQuery := s.db.WithContext(ctx).Select("id").Table("plans").Where("name = ?", FreePlanName)
	result := s.db.WithContext(ctx).
		Where("expires_at <= ? AND status = ? AND plan_id NOT IN (?)",
			time.Now().UTC().Truncate(time.Minute), "active", freePlanSubQuery).
		Find(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get expired paid subscriptions: %w", result.Error)
	}
	return subs, nil
}

// GetAllTelegramIDs returns all unique Telegram IDs from subscriptions.
func (s *Service) GetAllTelegramIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Distinct("telegram_id").
		Pluck("telegram_id", &ids)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get telegram IDs: %w", result.Error)
	}
	return ids, nil
}

// GetTelegramIDByUsername returns the Telegram ID for a given username.
func (s *Service) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).Where("username = ?", username).First(&sub)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return 0, ErrSubscriptionNotFound
		}
		return 0, fmt.Errorf("failed to find user by username: %w", result.Error)
	}
	return sub.TelegramID, nil
}

// GetTelegramIDsBatch returns a batch of unique Telegram IDs for broadcast.
// offset is the starting position, limit is the maximum number of IDs to return.
func (s *Service) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	var ids []int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Distinct("telegram_id").
		Order("telegram_id ASC").
		Limit(limit).
		Offset(offset).
		Pluck("telegram_id", &ids)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get telegram IDs batch: %w", result.Error)
	}
	return ids, nil
}

// GetTotalTelegramIDCount returns the total count of unique Telegram IDs.
func (s *Service) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Distinct("telegram_id").
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count telegram IDs: %w", result.Error)
	}
	return count, nil
}
