package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
func (s *Service) GetOrderByProviderPaymentID(ctx context.Context, provider string, providerPaymentID uuid.UUID) (*Order, error) {
	var order Order
	result := s.db.WithContext(ctx).Where("payment_provider = ? AND provider_payment_id = ?", provider, providerPaymentID.String()).First(&order)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get order by provider payment id: %w", result.Error)
	}
	return &order, nil
}

// UpdateOrderProviderPaymentID stores an external ID only while the order is pending.
func (s *Service) UpdateOrderProviderPaymentID(ctx context.Context, orderID uint, providerPaymentID uuid.UUID) error {
	result := s.db.WithContext(ctx).Model(&Order{}).Where("id = ? AND status = ?", orderID, OrderStatusPending).Update("provider_payment_id", providerPaymentID.String())
	if result.Error != nil {
		return fmt.Errorf("failed to update provider payment id: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order %d is not pending: %w", orderID, ErrOrderNotFound)
	}
	return nil
}

// FindPendingPaymentOrder returns the current pending payment intent. An
// expired intent is terminalized, but no replacement is created by this method.
func (s *Service) FindPendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, now time.Time) (*Order, error) {
	var order Order
	result := s.db.WithContext(ctx).
		Where("subscription_id = ? AND product_id = ? AND payment_provider = ? AND status = ?", subscriptionID, productID, "platega", OrderStatusPending).
		Order("id ASC").First(&order)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, fmt.Errorf("find pending payment order: %w", result.Error)
	}
	if order.PaymentExpiresAt != nil && !now.Before(*order.PaymentExpiresAt) {
		result := s.db.WithContext(ctx).Model(&Order{}).
			Where("id = ? AND status = ?", order.ID, OrderStatusPending).
			Update("status", OrderStatusExpired)
		if result.Error != nil {
			return nil, fmt.Errorf("expire payment order: %w", result.Error)
		}
		if result.RowsAffected == 1 {
			order.Status = OrderStatusExpired
			return &order, nil
		}
		// Another worker may have confirmed/canceled this order between the
		// lookup and the conditional update. Return the current row so the
		// caller cannot mistake the race for an absent intent and create a
		// second payment attempt for the same purchase.
		var current Order
		if err := s.db.WithContext(ctx).First(&current, order.ID).Error; err != nil {
			return nil, fmt.Errorf("reload payment order after expiry race: %w", err)
		}
		return &current, nil
	}
	return &order, nil
}

// CreatePendingPaymentOrder creates a new Platega payment intent. The partial
// unique index rejects concurrent duplicates; callers should reread the winner.
func (s *Service) CreatePendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, amountCents int64, currency string, now time.Time) (*Order, error) {
	order := &Order{SubscriptionID: subscriptionID, ProductID: productID, Status: OrderStatusPending, AmountCents: amountCents, Currency: currency, PaymentProvider: "platega", CreatedAt: now}
	if err := s.db.WithContext(ctx).Create(order).Error; err != nil {
		return nil, fmt.Errorf("create pending payment order: %w", err)
	}
	return order, nil
}

// FindOrCreatePendingPaymentOrder returns the single pending payment intent for
// a subscription/product pair, expiring it first when its local payment-link
// deadline has passed. The partial unique index is the final concurrency guard.
func (s *Service) FindOrCreatePendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, amountCents int64, currency string, now time.Time) (*Order, error) {
	var order Order
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("subscription_id = ? AND product_id = ? AND payment_provider = ? AND status = ?", subscriptionID, productID, "platega", OrderStatusPending).
			Order("id ASC").First(&order)
		if result.Error == nil {
			if order.PaymentCreationUncertain {
				return nil
			}
			if order.PaymentExpiresAt != nil && !now.Before(*order.PaymentExpiresAt) {
				if err := tx.Model(&Order{}).Where("id = ? AND status = ?", order.ID, OrderStatusPending).
					Update("status", OrderStatusExpired).Error; err != nil {
					return fmt.Errorf("expire payment order: %w", err)
				}
				order = Order{}
			} else {
				return nil
			}
		} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("find pending payment order: %w", result.Error)
		}

		order = Order{
			SubscriptionID:  subscriptionID,
			ProductID:       productID,
			Status:          OrderStatusPending,
			AmountCents:     amountCents,
			Currency:        currency,
			PaymentProvider: "platega",
			CreatedAt:       now,
		}
		if err := tx.Create(&order).Error; err != nil {
			return fmt.Errorf("create pending payment order: %w", err)
		}
		return nil
	})
	if err != nil {
		// A concurrent creator can win the partial unique-index race. The
		// transaction is rolled back, so read the winner and continue with it.
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "unique constraint") || strings.Contains(message, "idx_orders_pending_subscription_product_unique") {
			result := s.db.WithContext(ctx).
				Where("subscription_id = ? AND product_id = ? AND payment_provider = ? AND status = ?", subscriptionID, productID, "platega", OrderStatusPending).
				Order("id ASC").First(&order)
			if result.Error == nil {
				return &order, nil
			}
			return nil, fmt.Errorf("reload concurrent pending payment order: %w", result.Error)
		}
		return nil, err
	}
	return &order, nil
}

// MarkPaymentCreationUncertain atomically marks whether an outbound provider
// request has an outcome that cannot safely be retried automatically.
func (s *Service) MarkPaymentCreationUncertain(ctx context.Context, orderID uint, uncertain bool) (bool, error) {
	query := s.db.WithContext(ctx).Model(&Order{}).Where("id = ? AND status = ?", orderID, OrderStatusPending)
	if uncertain {
		query = query.Where("payment_creation_uncertain = ? AND (provider_payment_id IS NULL OR provider_payment_id = '')", false)
	} else {
		query = query.Where("payment_creation_uncertain = ?", true)
	}
	result := query.Update("payment_creation_uncertain", uncertain)
	if result.Error != nil {
		return false, fmt.Errorf("mark payment creation uncertainty: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// SavePaymentDetails stores the provider ID, link and local link deadline in a
// single update and clears the uncertainty flag.
func (s *Service) SavePaymentDetails(ctx context.Context, orderID uint, providerPaymentID uuid.UUID, paymentURL string, paymentExpiresAt time.Time) error {
	result := s.db.WithContext(ctx).Model(&Order{}).
		Where("id = ? AND status = ?", orderID, OrderStatusPending).
		Updates(map[string]interface{}{
			"provider_payment_id":        providerPaymentID.String(),
			"payment_url":                paymentURL,
			"payment_expires_at":         paymentExpiresAt.UTC(),
			"payment_creation_uncertain": false,
		})
	if result.Error != nil {
		return fmt.Errorf("save payment details: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order %d is not pending: %w", orderID, ErrOrderNotFound)
	}
	return nil
}

// ApplyPlanInTxFn is invoked inside the transaction that confirms an order.
// It must materialize all DB prerequisites (pending_add / pending_remove rows)
// needed by the background sync worker using the supplied tx handle.
// Returning an error aborts the transaction and rolls back the payment
// confirmation; returning nil commits it.
type ApplyPlanInTxFn func(ctx context.Context, tx *gorm.DB, subscriptionID uint, planID uint) error

// ConfirmOrderPaidCAS atomically marks an order paid and updates its subscription.
// If applyPlan is non-nil and the CAS succeeds, it is called with the same tx
// used to write the subscription; on error the whole transaction rolls back.
func (s *Service) ConfirmOrderPaidCAS(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *Subscription, newExpiry time.Time, product *Product, applyPlan ApplyPlanInTxFn) (bool, error) {
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

		// Read after the CAS write acquires SQLite's write lock. A concurrent
		// confirmation for another product therefore sees the already-committed
		// expiry instead of calculating from a stale caller snapshot.
		var currentSub Subscription
		if err := tx.First(&currentSub, sub.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load subscription for payment: %w", ErrSubscriptionNotFound)
			}
			return fmt.Errorf("load subscription for payment: %w", err)
		}
		newExpiry = calculatePaymentExpiry(activatedAt, &currentSub, product)
		if err := tx.Model(&Order{}).Where("id = ? AND status = ?", orderID, OrderStatusPaid).Update("expires_at", newExpiry).Error; err != nil {
			return fmt.Errorf("update order expiry after payment: %w", err)
		}
		result = tx.Model(&Subscription{}).Where("id = ?", sub.ID).Updates(map[string]interface{}{
			"plan_id": product.PlanID, "status": string(SubscriptionStatusActive),
			"expires_at": newExpiry, "product_id": product.ID, "started_at": activatedAt,
			"price_paid_cents": product.PriceCents, "currency": product.Currency, "reminders_sent": 0,
		})
		if result.Error != nil {
			return fmt.Errorf("update subscription after payment: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("update subscription after payment: %w", ErrSubscriptionNotFound)
		}
		sub.PlanID = product.PlanID
		sub.Status = string(SubscriptionStatusActive)
		sub.ProductID = &product.ID
		expiryCopy := newExpiry
		sub.ExpiresAt = &expiryCopy
		if applyPlan != nil {
			if err := applyPlan(ctx, tx, sub.ID, product.PlanID); err != nil {
				return fmt.Errorf("apply plan after payment: %w", err)
			}
		}
		activated = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return activated, nil
}

func calculatePaymentExpiry(now time.Time, sub *Subscription, product *Product) time.Time {
	base := now
	if sub != nil && product != nil && sub.PlanID == product.PlanID && sub.ExpiresAt != nil && sub.ExpiresAt.After(now) {
		base = *sub.ExpiresAt
	}
	if product == nil {
		return base
	}
	return base.AddDate(0, 0, product.DurationDays)
}

// CancelOrderCAS cancels an order only from one of the supplied states.
func (s *Service) CancelOrderCAS(ctx context.Context, provider string, providerPaymentID uuid.UUID, fromStatuses []OrderStatus) (bool, error) {
	result := s.db.WithContext(ctx).Model(&Order{}).Where("payment_provider = ? AND provider_payment_id = ? AND status IN ?", provider, providerPaymentID.String(), fromStatuses).Update("status", OrderStatusCanceled)
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
