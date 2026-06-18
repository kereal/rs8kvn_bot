package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
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

// GetProductByID returns a product by database ID.
func (s *Service) GetProductByID(ctx context.Context, id uint) (*Product, error) {
	var product Product
	result := s.db.WithContext(ctx).First(&product, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("product not found: %w", result.Error)
		}
		return nil, fmt.Errorf("failed to get product: %w", result.Error)
	}
	return &product, nil
}
