package database

import (
	"context"
	"fmt"
)

// GetActiveByPlanID returns active products for the given plan.
func (s *Service) GetActiveByPlanID(ctx context.Context, planID uint) ([]Product, error) {
	var products []Product
	result := s.db.WithContext(ctx).
		Where("plan_id = ? AND is_active = ?", planID, true).
		Find(&products)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get products: %w", result.Error)
	}
	return products, nil
}
