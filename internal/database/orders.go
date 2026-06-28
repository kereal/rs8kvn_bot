package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// CreateOrder inserts a new order record.
func (s *Service) CreateOrder(ctx context.Context, order *Order) error {
	if err := s.db.WithContext(ctx).Create(order).Error; err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}
	return nil
}

// GetOrderByID retrieves an order by its ID.
func (s *Service) GetOrderByID(ctx context.Context, id uint) (*Order, error) {
	var order Order
	result := s.db.WithContext(ctx).First(&order, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("failed to get order: %w", result.Error)
	}
	return &order, nil
}

// GetOrdersBySubscriptionID returns orders for the given subscription.
func (s *Service) GetOrdersBySubscriptionID(ctx context.Context, subscriptionID uint) ([]Order, error) {
	var orders []Order
	result := s.db.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		Order("created_at DESC").
		Find(&orders)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list orders: %w", result.Error)
	}
	return orders, nil
}

// UpdateOrderStatus updates the status of an order by ID.
func (s *Service) UpdateOrderStatus(ctx context.Context, id uint, status OrderStatus) error {
	result := s.db.WithContext(ctx).Model(&Order{}).Where("id = ?", id).Update("status", status)
	if result.Error != nil {
		return fmt.Errorf("failed to update order status: %w", result.Error)
	}
	return nil
}

// UpdateOrderPaidStatus sets the order as paid with a paid_at timestamp.
func (s *Service) UpdateOrderPaidStatus(ctx context.Context, id uint) error {
	now := time.Now().UTC().Truncate(time.Minute)
	result := s.db.WithContext(ctx).Model(&Order{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":  OrderStatusPaid,
		"paid_at": now,
	})
	if result.Error != nil {
		return fmt.Errorf("failed to update order paid status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order %d not found for paid status update", id)
	}
	return nil
}

// UpdateOrderActivatedAt sets activation and expiry timestamps for an order.
func (s *Service) UpdateOrderActivatedAt(ctx context.Context, id uint, activatedAt, expiresAt time.Time) error {
	result := s.db.WithContext(ctx).Model(&Order{}).Where("id = ?", id).Updates(map[string]interface{}{
		"activated_at": activatedAt,
		"expires_at":   expiresAt,
	})
	if result.Error != nil {
		return fmt.Errorf("failed to update order activation: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order %d not found for activation update", id)
	}
	return nil
}
