package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// CreateTrialSubscription creates a new trial subscription.
func (s *Service) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*Subscription, error) {
	planID, err := s.resolveTrialPlanID(ctx)
	if err != nil {
		return nil, err
	}

	sub := &Subscription{
		TelegramID:     0,
		SubscriptionID: subscriptionID,
		ClientID:       clientID,
		InviteCode:     inviteCode,
		ExpiresAt:      expiryTime,
		PlanID:         planID,
		Status:         "active",
	}
	if err := s.db.WithContext(ctx).Create(sub).Error; err != nil {
		return nil, fmt.Errorf("failed to create trial subscription: %w", err)
	}
	return sub, nil
}

// resolveTrialPlanID looks up the trial plan by name and returns its ID.
func (s *Service) resolveTrialPlanID(ctx context.Context) (uint, error) {
	var plan Plan
	if err := s.db.WithContext(ctx).Where("name = ?", TrialPlanName).First(&plan).Error; err != nil {
		return 0, fmt.Errorf("trial plan not found: %w", err)
	}
	return plan.ID, nil
}

// GetTrialSubscriptionBySubID returns a trial subscription by its subscription ID.
// A subscription is considered trial if its plan has name 'trial'.
func (s *Service) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		First(&sub)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get trial subscription by subscription_id: %w", result.Error)
	}

	var plan Plan
	if err := s.db.WithContext(ctx).Where("id = ?", sub.PlanID).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("subscription is not a trial")
		}
		return nil, fmt.Errorf("failed to get plan for trial check: %w", err)
	}
	if plan.Name != TrialPlanName {
		return nil, fmt.Errorf("subscription is not a trial")
	}
	return &sub, nil
}

// BindTrialSubscription binds a trial subscription to a Telegram user.
// Uses UPDATE with WHERE to prevent race conditions — if telegram_id was already set
// by a concurrent bind, RowsAffected will be 0.
func (s *Service) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*Subscription, error) {
	var sub Subscription
	var referredBy int64
	var freePlanID uint

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var trialPlan Plan
		if err := tx.Where("name = ?", TrialPlanName).First(&trialPlan).Error; err != nil {
			return fmt.Errorf("failed to resolve trial plan: %w", err)
		}
		planID := trialPlan.ID

		if err := tx.Where("subscription_id = ? AND plan_id = ? AND telegram_id = ?", subscriptionID, planID, 0).First(&sub).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("trial subscription not found or already activated")
			}
			return fmt.Errorf("failed to get trial subscription: %w", err)
		}

		if sub.InviteCode != "" {
			var invite Invite
			if err := tx.Where("code = ?", sub.InviteCode).First(&invite).Error; err == nil {
				referredBy = invite.ReferrerTGID
			}
		}

		var freePlan Plan
		if err := tx.Where("name = ?", FreePlanName).First(&freePlan).Error; err != nil {
			return fmt.Errorf("failed to resolve free plan: %w", err)
		}
		freePlanID = freePlan.ID
		result := tx.Model(&Subscription{}).
			Where("id = ? AND telegram_id = ? AND plan_id = ?", sub.ID, 0, planID).
			Updates(map[string]any{
				"telegram_id": telegramID,
				"username":    username,
				"plan_id":     freePlanID,
				"referred_by": referredBy,
			})
		if result.Error != nil {
			return fmt.Errorf("failed to bind trial subscription: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("trial subscription not found or already activated")
		}

		// Defensive: revoke any other active subscription the user may already have
		// (e.g. a free-plan sub created concurrently via /start). Without this, the
		// user could end up with two active subscriptions.
		if err := tx.Model(&Subscription{}).
			Where("telegram_id = ? AND status = ? AND id <> ?", telegramID, "active", sub.ID).
			Update("status", "revoked").Error; err != nil {
			return fmt.Errorf("failed to revoke pre-existing active subscriptions: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sub.TelegramID = telegramID
	sub.Username = username
	sub.PlanID = freePlanID
	sub.ReferredBy = referredBy
	return &sub, nil
}

// CountTrialRequestsByIPLastHour returns the number of trial requests from an IP in the last hour.
func (s *Service) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	var count int64
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	result := s.db.WithContext(ctx).
		Model(&TrialRequest{}).
		Where("ip = ? AND created_at > ?", ip, oneHourAgo).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count trial requests: %w", result.Error)
	}
	return int(count), nil
}

// CreateTrialRequest records a new trial request.
func (s *Service) CreateTrialRequest(ctx context.Context, ip string) error {
	req := &TrialRequest{
		IP: ip,
	}
	if err := s.db.WithContext(ctx).Create(req).Error; err != nil {
		return fmt.Errorf("failed to create trial request: %w", err)
	}
	return nil
}

// CleanupExpiredTrials deletes trial subscriptions that have expired without being activated.
// Uses atomic DELETE ... RETURNING to prevent race conditions with concurrent trial activation.
func (s *Service) CleanupExpiredTrials(ctx context.Context, hours int) ([]Subscription, error) {
	var trialPlan Plan
	if err := s.db.WithContext(ctx).Where("name = ?", TrialPlanName).First(&trialPlan).Error; err != nil {
		return nil, fmt.Errorf("failed to resolve trial plan: %w", err)
	}

	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	var subs []Subscription
	result := s.db.WithContext(ctx).Raw(
		`DELETE FROM subscriptions
		 WHERE plan_id = ? AND telegram_id = ? AND created_at < ?
		 RETURNING id, client_id, subscription_id`,
		trialPlan.ID, 0, cutoff,
	).Scan(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to cleanup expired trials: %w", result.Error)
	}

	rateLimitCutoff := time.Now().Add(-1*time.Hour + 1*time.Second)
	s.db.WithContext(ctx).
		Where("created_at < ?", rateLimitCutoff).
		Delete(&TrialRequest{})

	return subs, nil
}
