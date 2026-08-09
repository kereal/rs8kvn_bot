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

// GetOrderByProviderPaymentID finds an order by provider and external payment ID.
func (s *Service) GetOrderByProviderPaymentID(ctx context.Context, provider, providerPaymentID string) (*Order, error) {
	var order Order
	result := s.db.WithContext(ctx).Where("payment_provider = ? AND provider_payment_id = ?", provider, providerPaymentID).First(&order)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get order by provider payment id: %w", result.Error)
	}
	return &order, nil
}

// UpdateOrderProviderPaymentID stores an external ID only while the order is pending.
func (s *Service) UpdateOrderProviderPaymentID(ctx context.Context, orderID uint, providerPaymentID string) error {
	result := s.db.WithContext(ctx).Model(&Order{}).Where("id = ? AND status = ?", orderID, OrderStatusPending).Update("provider_payment_id", providerPaymentID)
	if result.Error != nil {
		return fmt.Errorf("failed to update provider payment id: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order %d is not pending: %w", orderID, ErrOrderNotFound)
	}
	return nil
}

// ConfirmOrderPaidCAS atomically marks an order paid and updates its subscription.
func (s *Service) ConfirmOrderPaidCAS(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *Subscription, newExpiry time.Time, product *Product) (bool, error) {
	var activated bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Order{}).Where("id = ? AND status = ?", orderID, OrderStatusPending).Updates(map[string]interface{}{
			"status": OrderStatusPaid, "paid_at": paidAt, "activated_at": activatedAt, "expires_at": newExpiry,
		})
		if result.Error != nil {
			return fmt.Errorf("confirm order: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		result = tx.Model(&Subscription{}).Where("id = ?", sub.ID).Updates(map[string]interface{}{
			"expires_at": newExpiry, "product_id": product.ID, "started_at": activatedAt,
			"price_paid_cents": product.PriceCents, "currency": product.Currency, "reminders_sent": 0,
		})
		if result.Error != nil {
			return fmt.Errorf("update subscription after payment: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("update subscription after payment: %w", ErrSubscriptionNotFound)
		}
		activated = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return activated, nil
}

// CancelOrderCAS cancels an order only from one of the supplied states.
func (s *Service) CancelOrderCAS(ctx context.Context, provider, providerPaymentID string, fromStatuses []OrderStatus) (bool, error) {
	result := s.db.WithContext(ctx).Model(&Order{}).Where("payment_provider = ? AND provider_payment_id = ? AND status IN ?", provider, providerPaymentID, fromStatuses).Update("status", OrderStatusCanceled)
	if result.Error != nil {
		return false, fmt.Errorf("cancel order: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
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
	if result.RowsAffected == 0 {
		return fmt.Errorf("order %d not found for status update: %w", id, ErrOrderNotFound)
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
		return fmt.Errorf("order %d not found for paid status update: %w", id, ErrOrderNotFound)
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
		return fmt.Errorf("order %d not found for activation update: %w", id, ErrOrderNotFound)
	}
	return nil
}
