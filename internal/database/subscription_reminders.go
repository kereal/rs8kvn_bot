package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ClaimReminder atomically claims a reminder bit for the current expiry.
// It returns false when the bit was already claimed or the expiry changed.
func (s *Service) ClaimReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error) {
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("id = ? AND expires_at = ? AND (reminders_sent & ?) = 0", id, expiresAt, bit).
		Update("reminders_sent", gorm.Expr("reminders_sent | ?", bit))
	if result.Error != nil {
		return false, fmt.Errorf("failed to claim reminder: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return true, nil
	}

	var current Subscription
	check := s.db.WithContext(ctx).Select("id, expires_at").First(&current, id)
	if errors.Is(check.Error, gorm.ErrRecordNotFound) {
		return false, ErrSubscriptionNotFound
	}
	if check.Error != nil {
		return false, fmt.Errorf("failed to verify reminder claim: %w", check.Error)
	}
	return false, nil
}

// ReleaseReminder releases a claim only if the subscription expiry is unchanged.
func (s *Service) ReleaseReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("id = ? AND expires_at = ?", id, expiresAt).
		Update("reminders_sent", gorm.Expr("reminders_sent & ?", ^bit))
	if result.Error != nil {
		return fmt.Errorf("failed to release reminder: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}

	var current Subscription
	check := s.db.WithContext(ctx).Select("id, expires_at").First(&current, id)
	if errors.Is(check.Error, gorm.ErrRecordNotFound) {
		return ErrSubscriptionNotFound
	}
	if check.Error != nil {
		return fmt.Errorf("failed to verify reminder release: %w", check.Error)
	}
	return nil
}

// GetSubscriptionsExpiringInRange returns active paid subscriptions expiring within [from, to].
// It uses indexed expires_at and excludes perpetual, free, and trial subscriptions.
func (s *Service) GetSubscriptionsExpiringInRange(ctx context.Context, from, to time.Time) ([]Subscription, error) {
	var subs []Subscription
	nonPaidPlanSubQuery := s.db.WithContext(ctx).
		Select("id").
		Table("plans").
		Where("name IN ?", []string{FreePlanName, TrialPlanName})
	result := s.db.WithContext(ctx).
		Where("status = ? AND expires_at IS NOT NULL AND expires_at >= ? AND expires_at <= ? AND plan_id NOT IN (?)",
			"active", from, to, nonPaidPlanSubQuery).
		Find(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get subscriptions expiring in range: %w", result.Error)
	}
	return subs, nil
}
