package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// GetPlanByName returns a plan by its name.
// The free plan is always returned regardless of its active status.
func (s *Service) GetPlanByName(ctx context.Context, name string) (*Plan, error) {
	var plan Plan
	query := s.db.WithContext(ctx).Where("name = ?", name)
	if name != FreePlanName {
		query = query.Where("is_active = ?", true)
	}
	result := query.First(&plan)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("failed to get plan by name: %w", result.Error)
	}
	return &plan, nil
}

// GetPlanByID returns a plan by its ID.
// The free plan is always returned regardless of its active status.
func (s *Service) GetPlanByID(ctx context.Context, id uint) (*Plan, error) {
	var plan Plan
	result := s.db.WithContext(ctx).Where("id = ? AND (is_active = ? OR name = ?)", id, true, FreePlanName).First(&plan)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("failed to get plan by id: %w", result.Error)
	}
	return &plan, nil
}
