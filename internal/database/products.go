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
		Table("products").
		Select("products.*").
		Joins("JOIN plans ON plans.id = products.plan_id").
		Where("products.plan_id = ? AND products.is_active = ? AND (plans.is_active = ? OR plans.name = ?)", planID, true, true, FreePlanName).
		Find(&products)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get products: %w", result.Error)
	}
	return products, nil
}

// ListActiveProducts returns active paid products sorted by price and ID.
func (s *Service) ListActiveProducts(ctx context.Context) ([]Product, error) {
	var products []Product
	result := s.db.WithContext(ctx).
		Where("is_active = ? AND price_cents > ?", true, 0).
		Order("price_cents ASC, id ASC").Find(&products)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list active products: %w", result.Error)
	}
	return products, nil
}

// GetProductByID returns a product by database ID.
func (s *Service) GetProductByID(ctx context.Context, id uint) (*Product, error) {
	var product Product
	result := s.db.WithContext(ctx).First(&product, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("failed to get product: %w", result.Error)
	}
	return &product, nil
}
