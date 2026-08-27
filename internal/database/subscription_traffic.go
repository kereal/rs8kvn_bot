package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// Bits in subscriptions.traffic_reminders_sent (see migration 038).
const (
	// TrafficBit90 marks the "quota almost exhausted" warning sent.
	TrafficBit90 = 1 << iota
	// TrafficBitExhausted marks the "quota exceeded / disabled" notification sent.
	TrafficBitExhausted
	// TrafficBitReset marks the "traffic reset, you're back online" notification sent.
	TrafficBitReset
)

// GetActiveSubscriptionsWithTrafficLimit returns active subscriptions whose plan
// has a non-zero traffic limit, exposing that limit in bytes. Subscriptions on
// unlimited plans (traffic_limit = 0) are excluded so the traffic-notification
// worker only scans plans that actually enforce a quota.
func (s *Service) GetActiveSubscriptionsWithTrafficLimit(ctx context.Context) ([]SubscriptionTrafficTarget, error) {
	var targets []SubscriptionTrafficTarget

	result := s.db.WithContext(ctx).
		Table("subscriptions").
		Select("subscriptions.*, plans.traffic_limit AS traffic_limit").
		Joins("JOIN plans ON plans.id = subscriptions.plan_id").
		Where("subscriptions.status = ? AND plans.traffic_limit > 0", string(SubscriptionStatusActive)).
		Order("subscriptions.id ASC").
		Scan(&targets)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get subscriptions with traffic limit: %w", result.Error)
	}

	return targets, nil
}

// ClaimTrafficReminder atomically claims a traffic-notification bit for the
// subscription. It returns false when the bit is already set, so each
// notification is sent at most once until released.
func (s *Service) ClaimTrafficReminder(ctx context.Context, id uint, bit int) (bool, error) {
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("id = ? AND (traffic_reminders_sent & ?) = 0", id, bit).
		Update("traffic_reminders_sent", gorm.Expr("traffic_reminders_sent | ?", bit))
	if result.Error != nil {
		return false, fmt.Errorf("failed to claim traffic reminder: %w", result.Error)
	}

	if result.RowsAffected > 0 {
		return true, nil
	}

	var current Subscription

	check := s.db.WithContext(ctx).Select("id").First(&current, id)
	if errors.Is(check.Error, gorm.ErrRecordNotFound) {
		return false, ErrSubscriptionNotFound
	}

	if check.Error != nil {
		return false, fmt.Errorf("failed to verify traffic reminder claim: %w", check.Error)
	}

	return false, nil
}

// ReleaseTrafficReminder clears a traffic-notification bit so the notification
// can be sent again once the condition re-triggers (e.g. the "exhausted" bit is
// released after the client is reset and re-enabled, allowing the next exhaustion
// cycle to warn again).
func (s *Service) ReleaseTrafficReminder(ctx context.Context, id uint, bit int) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("id = ?", id).
		Update("traffic_reminders_sent", gorm.Expr("traffic_reminders_sent & ?", ^bit))
	if result.Error != nil {
		return fmt.Errorf("failed to release traffic reminder: %w", result.Error)
	}

	if result.RowsAffected > 0 {
		return nil
	}

	var current Subscription

	check := s.db.WithContext(ctx).Select("id").First(&current, id)
	if errors.Is(check.Error, gorm.ErrRecordNotFound) {
		return ErrSubscriptionNotFound
	}

	if check.Error != nil {
		return fmt.Errorf("failed to verify traffic reminder release: %w", check.Error)
	}

	return nil
}