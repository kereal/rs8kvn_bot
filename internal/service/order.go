package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// OrderService handles order creation and activation flows.
type OrderService struct {
	db      interfaces.DatabaseService
	subSvc  *SubscriptionService
	syncSvc *SyncService
}

// PaymentInfo contains payment details for an order.
type PaymentInfo struct {
	URL string
}

// NewOrderService creates a new OrderService.
func NewOrderService(db interfaces.DatabaseService, subSvc *SubscriptionService, syncSvc *SyncService) *OrderService {
	return &OrderService{db: db, subSvc: subSvc, syncSvc: syncSvc}
}

// ActivateProduct creates an order and initializes a paid subscription for the user.
// If the subscription does not exist, a free subscription is created first.
func (o *OrderService) ActivateProduct(ctx context.Context, telegramID int64, product *database.Product) (*database.Order, error) {
	if product == nil {
		return nil, errors.New("product is nil")
	}

	sub, err := o.subSvc.GetOrCreateSubscription(ctx, telegramID, "", "")
	if err != nil {
		return nil, fmt.Errorf("get or create subscription: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	maxExpiry := now
	if sub.ExpiresAt.After(now) {
		maxExpiry = sub.ExpiresAt
	}
	newExpiry := maxExpiry.AddDate(0, 0, product.DurationDays)

	order := &database.Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         database.OrderStatusPending,
		AmountCents:    product.PriceCents,
		Currency:       product.Currency,
	}

	if err := o.db.CreateOrder(ctx, order); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	info, requestErr := o.requestPayment(ctx, order)
	if requestErr != nil {
		logger.Warn("payment request failed", zap.Error(requestErr), zap.Uint("order_id", order.ID))
	}
	_ = info

	if err := o.db.UpdateOrderActivatedAt(ctx, order.ID, now, newExpiry); err != nil {
		return nil, fmt.Errorf("update order activation: %w", err)
	}

	sub.PlanID = product.PlanID
	sub.ProductID = product.ID
	sub.ExpiresAt = newExpiry
	sub.PricePaidCents = product.PriceCents
	sub.Currency = product.Currency
	sub.StartedAt = now
	if err := o.db.UpdateSubscription(ctx, sub); err != nil {
		return nil, fmt.Errorf("update subscription: %w", err)
	}

	if o.syncSvc != nil {
		if err := o.syncSvc.RecalculateNodes(ctx, sub.ID); err != nil {
			return nil, fmt.Errorf("recalculate nodes: %w", err)
		}
		if err := o.syncSvc.SyncSubscription(ctx, sub.ID); err != nil {
			return nil, fmt.Errorf("sync subscription: %w", err)
		}
	}

	return order, nil
}

// requestPayment is a stub for external payment integration.
func (o *OrderService) requestPayment(ctx context.Context, order *database.Order) (*PaymentInfo, error) {
	return nil, fmt.Errorf("payment integration not implemented for order %d", order.ID)
}
