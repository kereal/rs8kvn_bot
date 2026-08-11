// Package database provides persistence, migrations, models, and repositories.
package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// ListActiveProducts returns active paid products belonging to active plans,
// sorted deterministically by price and ID.
func (s *Service) ListActiveProducts(ctx context.Context) ([]Product, error) {
	var products []Product
	result := s.db.WithContext(ctx).
		Table("products").
		Select("products.*").
		Joins("JOIN plans ON plans.id = products.plan_id").
		Where("products.is_active = ? AND products.price_cents > ? AND plans.is_active = ?", true, 0, true).
		Order("products.price_cents ASC, products.id ASC").
		Find(&products)
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

// UpdateProductGuarded updates a product while preserving the historical
// fields referenced by existing orders. Only is_active may change after the
// first order has been created.
func (s *Service) UpdateProductGuarded(ctx context.Context, product *Product) error {
	if product == nil || product.ID == 0 {
		return fmt.Errorf("update product: %w", ErrProductNotFound)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Product
		if err := tx.First(&current, product.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProductNotFound
			}
			return fmt.Errorf("load product for update: %w", err)
		}
		// Acquire SQLite's write lock before counting orders. This serializes the
		// guard's check-and-update against a concurrent first-order insert.
		if err := tx.Model(&Product{}).Where("id = ?", product.ID).Update("is_active", current.IsActive).Error; err != nil {
			return fmt.Errorf("lock product for update: %w", err)
		}
		var orderCount int64
		if err := tx.Model(&Order{}).Where("product_id = ?", product.ID).Count(&orderCount).Error; err != nil {
			return fmt.Errorf("count product orders: %w", err)
		}
		if orderCount > 0 && (current.Name != product.Name || current.PlanID != product.PlanID || current.DurationDays != product.DurationDays || current.PriceCents != product.PriceCents || current.Currency != product.Currency) {
			return ErrProductImmutable
		}
		result := tx.Model(&Product{}).Where("id = ?", product.ID).Updates(map[string]interface{}{
			"name": product.Name, "plan_id": product.PlanID, "duration_days": product.DurationDays,
			"price_cents": product.PriceCents, "currency": product.Currency, "is_active": product.IsActive,
		})
		if result.Error != nil {
			return fmt.Errorf("update product: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrProductNotFound
		}
		return nil
	})
}
