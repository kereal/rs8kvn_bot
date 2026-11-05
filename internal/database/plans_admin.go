package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PlanOverview is a plan row enriched with usage counts. The admin panel needs
// both to disable destructive actions before they hit a foreign key.
type PlanOverview struct {
	ID                 uint
	Name               string
	IsActive           bool
	DevicesLimit       int
	TrafficLimit       int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ProductsCount      int64
	SubscriptionsCount int64
}

// ListPlanOverviews returns every plan (active and inactive) with the number of
// products and subscriptions that reference it. A single query keeps the panel
// fast on a laptop-sized dataset.
func (s *Service) ListPlanOverviews(ctx context.Context) ([]PlanOverview, error) {
	var rows []PlanOverview

	result := s.db.WithContext(ctx).
		Model(&Plan{}).
		Select(`plans.id, plans.name, plans.is_active, plans.devices_limit, plans.traffic_limit,
			plans.created_at, plans.updated_at,
			(SELECT COUNT(*) FROM products WHERE products.plan_id = plans.id) AS products_count,
			(SELECT COUNT(*) FROM subscriptions WHERE subscriptions.plan_id = plans.id) AS subscriptions_count`).
		Order("plans.id ASC").
		Scan(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list plans: %w", result.Error)
	}

	return rows, nil
}

// CreatePlan inserts a plan. The name is trimmed and must be unique; traffic
// and device limits must be non-negative.
func (s *Service) CreatePlan(ctx context.Context, plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("create plan: %w", ErrPlanInvalid)
	}

	name := strings.TrimSpace(plan.Name)
	err := validatePlanFields(name, plan)
	if err != nil {
		return err
	}

	plan.Name = name

	err = s.db.WithContext(ctx).Create(plan).Error
	if err != nil {
		return classifyPlanWriteError("create plan", err)
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditPlanCreated(plan))

	return nil
}

// UpdatePlan rewrites the mutable plan fields. Plans named "free" and "trial"
// are load-bearing (see GetPlanByName callers): they cannot be renamed, and the
// trial plan cannot be deactivated because GetPlanByName filters inactive plans.
func (s *Service) UpdatePlan(ctx context.Context, plan *Plan) error {
	if plan == nil || plan.ID == 0 {
		return fmt.Errorf("update plan: %w", ErrPlanNotFound)
	}

	name := strings.TrimSpace(plan.Name)

	var before map[string]any

	err := validatePlanFields(name, plan)
	if err != nil {
		return err
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Plan

		err := tx.Where("id = ?", plan.ID).First(&current).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("plan %d: %w", plan.ID, ErrPlanNotFound)
			}

			return fmt.Errorf("load plan for update: %w", err)
		}

		if isProtectedPlanName(current.Name) && current.Name != name {
			return fmt.Errorf("cannot rename plan %q: %w", current.Name, ErrPlanProtected)
		}

		if current.Name == TrialPlanName && !plan.IsActive {
			return fmt.Errorf("cannot deactivate plan %q: %w", current.Name, ErrPlanProtected)
		}

		before = planAuditState(current)

		result := tx.Model(&Plan{}).Where("id = ?", plan.ID).Updates(map[string]any{
			"name": plan.Name, "devices_limit": plan.DevicesLimit,
			"traffic_limit": plan.TrafficLimit, "is_active": plan.IsActive,
		})
		if result.Error != nil {
			return classifyPlanWriteError("update plan", result.Error)
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf("plan %d: %w", plan.ID, ErrPlanNotFound)
		}

		return nil
	})
	if err != nil {
		return err
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditPlanUpdated(plan.ID, before, planAuditState(*plan)))

	return nil
}

// DeletePlan removes a plan that nothing depends on. Plans referenced by
// products cannot be deleted directly: products.plan_id cascades, so a silent
// delete would erase catalogue entries (and with them order history).
func (s *Service) DeletePlan(ctx context.Context, id uint) error {
	var deleted Plan

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan Plan

		err := tx.Where("id = ?", id).First(&plan).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("plan %d: %w", id, ErrPlanNotFound)
			}

			return fmt.Errorf("load plan for delete: %w", err)
		}

		if isProtectedPlanName(plan.Name) {
			return fmt.Errorf("cannot delete plan %q: %w", plan.Name, ErrPlanProtected)
		}

		var products int64

		err = tx.Model(&Product{}).Where("plan_id = ?", id).Count(&products).Error
		if err != nil {
			return fmt.Errorf("count plan products: %w", err)
		}

		var subscriptions int64

		err = tx.Model(&Subscription{}).Where("plan_id = ?", id).Count(&subscriptions).Error
		if err != nil {
			return fmt.Errorf("count plan subscriptions: %w", err)
		}

		if products > 0 || subscriptions > 0 {
			return fmt.Errorf("plan %d has %d products and %d subscriptions: %w", id, products, subscriptions, ErrPlanInUse)
		}

		result := tx.Delete(&Plan{}, id)
		if result.Error != nil {
			return fmt.Errorf("delete plan %w", result.Error)
		}

		deleted = plan

		return nil
	})
	if err != nil {
		return err
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditPlanDeleted(&deleted))

	return nil
}

// isProtectedPlanName reports whether the plan name is referenced by name from
// lifecycle code (free-plan downgrades and trial provisioning).
func isProtectedPlanName(name string) bool {
	return name == FreePlanName || name == TrialPlanName
}

func validatePlanFields(name string, plan *Plan) error {
	if name == "" {
		return fmt.Errorf("plan name is required: %w", ErrPlanInvalid)
	}

	if len(name) > 50 {
		return fmt.Errorf("plan name must be at most 50 characters: %w", ErrPlanInvalid)
	}

	if plan.DevicesLimit < 0 {
		return fmt.Errorf("devices_limit must be non-negative: %w", ErrPlanInvalid)
	}

	if plan.TrafficLimit < 0 {
		return fmt.Errorf("traffic_limit must be non-negative: %w", ErrPlanInvalid)
	}

	return nil
}

// classifyPlanWriteError maps SQLite's unique-index violation onto a sentinel
// the HTTP layer can translate into 409 instead of 500.
func classifyPlanWriteError(op string, err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: plans.name") {
		return fmt.Errorf("%s: %w", op, ErrPlanNameTaken)
	}

	return fmt.Errorf("%s: %w", op, err)
}
