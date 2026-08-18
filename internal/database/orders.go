package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
// Not-found results are normalized to ErrOrderNotFound for service-layer handling.
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

		err := s.db.WithContext(ctx).First(&current, order.ID).Error
		if err != nil {
			return nil, fmt.Errorf("reload payment order after expiry race: %w", err)
		}

		return &current, nil
	}

	return &order, nil
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
				err := tx.Model(&Order{}).Where("id = ? AND status = ?", order.ID, OrderStatusPending).
					Update("status", OrderStatusExpired).Error
				if err != nil {
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

		err := tx.Create(&order).Error
		if err != nil {
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
		Updates(map[string]any{
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

// SaveOrderPaymentAmounts persists the actual amounts known only at callback
// time: what was really charged from the customer (callbackAmountCents,
// including any customer-paid provider fee) and, when the provider API
// answered, the provider fee. A nil providerFeeCents leaves the stored fee
// value untouched, so the first call can save the callback amount and a later
// best-effort call can add the fee without disturbing it.
func (s *Service) SaveOrderPaymentAmounts(ctx context.Context, orderID uint, callbackAmountCents int64, providerFeeCents *int64) error {
	return s.db.WithContext(ctx).Model(&Order{}).Where("id = ?", orderID).Updates(map[string]any{
		"callback_amount_cents": callbackAmountCents,
		"provider_fee_cents":    providerFeeCents,
	}).Error
}

// ApplyPlanInTxFn is invoked inside the transaction that confirms an order.
// It must materialize all DB prerequisites (pending_add / pending_remove rows)
// needed by the background sync worker using the supplied tx handle.
// Returning an error aborts the transaction and rolls back the payment
// confirmation; returning nil commits it.
type ApplyPlanInTxFn func(ctx context.Context, tx *gorm.DB, subscriptionID uint, planID uint) error

// ConfirmOrderPaidCAS atomically marks an order paid and updates its subscription.
// The order and subscription expiry are both computed inside the transaction from
// the current subscription state, so the caller does not need to pre-calculate
// (and cannot pass a stale) expiry value. The OrderService may call this method
// for an expired order when its provider callback is still inside the short
// settlement grace period; the service validates that time window before
// entering this transaction. If applyPlan is non-nil and the CAS succeeds, it
// is called with the same tx used to write the subscription; on error the whole
// transaction rolls back.
func (s *Service) ConfirmOrderPaidCAS(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *Subscription, product *Product, applyPlan ApplyPlanInTxFn, callbackAmountCents int64) (bool, error) {
	var activated bool

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize every entitlement-changing payment transition on the
		// subscription row before touching its order. Chargeback acquires this
		// same lock first, so its active-coverage decision cannot be made from a
		// snapshot that races a concurrent confirmation.
		var currentSub Subscription

		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&currentSub, sub.ID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load subscription for payment: %w", ErrSubscriptionNotFound)
			}

			return fmt.Errorf("load subscription for payment: %w", err)
		}

		newExpiry := CalculatePaymentExpiry(activatedAt, &currentSub, product)

		// An expired order is accepted here only for the provider callback grace
		// path documented above. The service layer checks the grace deadline;
		// keeping both statuses in the same conditional update preserves the
		// pending/expired -> paid transition as one atomic compare-and-swap.
		result := tx.Model(&Order{}).Where("id = ? AND status IN ?", orderID, []OrderStatus{OrderStatusPending, OrderStatusExpired}).Updates(map[string]any{
			"status":                OrderStatusPaid,
			"paid_at":               paidAt,
			"activated_at":          activatedAt,
			"expires_at":            newExpiry,
			"callback_amount_cents": callbackAmountCents,
		})
		if result.Error != nil {
			return fmt.Errorf("confirm order: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return nil
		}

		result = tx.Model(&Subscription{}).Where("id = ?", sub.ID).Updates(map[string]any{
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
			err := applyPlan(ctx, tx, sub.ID, product.PlanID)
			if err != nil {
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

// CalculatePaymentExpiry is the single source of truth for payment expiry
// computation shared by the order CAS and the service layer. When the current
// plan matches the purchased product's plan and the existing expiry is still in
// the future, the new expiry extends the existing one; otherwise it starts from
// the activation time. A nil product yields the base time unchanged.
func CalculatePaymentExpiry(now time.Time, sub *Subscription, product *Product) time.Time {
	base := now
	if sub != nil && product != nil && sub.PlanID == product.PlanID && sub.ExpiresAt != nil && sub.ExpiresAt.After(now) {
		base = *sub.ExpiresAt
	}

	if product == nil {
		return base
	}

	return base.AddDate(0, 0, product.DurationDays)
}

// ChargebackPlanInTxFn materializes the DB-side VPN sync prerequisites while the
// chargeback transaction is open. It must not perform external network calls.
type ChargebackPlanInTxFn func(ctx context.Context, tx *gorm.DB, subscriptionID uint, freePlanID uint) error

// CancelPaidOrderAndDowngradeCAS atomically records a paid-order chargeback and,
// when no other currently-valid paid order covers the subscription, resets the
// subscription to the free plan and materializes its pending sync state.
//
// The order status, entitlement update, and pending node transitions share one
// transaction. A concurrent confirmation therefore cannot commit an entitlement
// after this chargeback's decision without first observing the serialized DB
// state. External VPN calls must happen after this method returns successfully.
func (s *Service) CancelPaidOrderAndDowngradeCAS(ctx context.Context, provider string, providerPaymentID uuid.UUID, now time.Time, freePlanID uint, applyPlan ChargebackPlanInTxFn) (*ChargebackResult, error) {
	var result ChargebackResult

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order Order

		query := tx.Where("payment_provider = ? AND provider_payment_id = ?", provider, providerPaymentID.String()).First(&order)
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return nil
		}

		if query.Error != nil {
			return fmt.Errorf("find chargeback order: %w", query.Error)
		}

		// Use the same subscription lock order as ConfirmOrderPaidCAS. The lock
		// is acquired before changing this order or evaluating other paid orders,
		// which makes the chargeback coverage decision linearizable per user.
		var currentSub Subscription

		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&currentSub, order.SubscriptionID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load subscription for chargeback: %w", ErrSubscriptionNotFound)
			}

			return fmt.Errorf("load subscription for chargeback: %w", err)
		}

		// Re-read the provider order after the subscription lock. A concurrent
		// confirmation may have changed it after the initial lookup.
		err = tx.First(&order, order.ID).Error
		if err != nil {
			return fmt.Errorf("reload chargeback order: %w", err)
		}

		result.Order = &order
		result.WasPaid = order.Status == OrderStatusPaid

		fromStatus := OrderStatusPending
		if result.WasPaid {
			fromStatus = OrderStatusPaid
		}

		updated := tx.Model(&Order{}).
			Where("id = ? AND status = ?", order.ID, fromStatus).
			Update("status", OrderStatusCanceled)
		if updated.Error != nil {
			return fmt.Errorf("cancel paid order: %w", updated.Error)
		}

		if updated.RowsAffected == 0 {
			return nil
		}

		result.Transitioned = true

		order.Status = OrderStatusCanceled
		if !result.WasPaid {
			result.Order = &order
			return nil
		}
		// A chargeback must never resurrect a subscription that was already
		// revoked/canceled by another lifecycle flow. The order transition is
		// still committed, but only active subscriptions may be downgraded to
		// the active free plan below.
		if currentSub.Status != string(SubscriptionStatusActive) {
			result.Order = &order
			return nil
		}

		var activePaid int64

		err = tx.Model(&Order{}).
			Where("subscription_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", order.SubscriptionID, OrderStatusPaid, now).
			Count(&activePaid).Error
		if err != nil {
			return fmt.Errorf("check active paid coverage: %w", err)
		}

		if activePaid > 0 {
			result.Order = &order
			return nil
		}

		updated = tx.Model(&Subscription{}).Where("id = ?", order.SubscriptionID).Updates(map[string]any{
			"status":           string(SubscriptionStatusActive),
			"expires_at":       nil,
			"plan_id":          freePlanID,
			"product_id":       nil,
			"started_at":       nil,
			"price_paid_cents": 0,
			"currency":         nil,
		})
		if updated.Error != nil {
			return fmt.Errorf("downgrade subscription after chargeback: %w", updated.Error)
		}

		if updated.RowsAffected == 0 {
			return fmt.Errorf("downgrade subscription after chargeback: %w", ErrSubscriptionNotFound)
		}

		if applyPlan != nil {
			err := applyPlan(ctx, tx, order.SubscriptionID, freePlanID)
			if err != nil {
				return fmt.Errorf("apply free plan after chargeback: %w", err)
			}
		}

		result.Downgraded = true
		result.Order = &order

		return nil
	})
	if err != nil {
		return nil, err
	}

	if result.Order == nil {
		return nil, nil
	}

	return &result, nil
}

// CancelOrderCAS cancels an order only from one of the supplied states. It
// returns false without error when the callback is an idempotent no-op.
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
