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
	URL       string
	Provider  string
	PaymentID string
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
	planChanged := sub.PlanID != product.PlanID
	oldPlanID := sub.PlanID
	newExpiry := calculateProductExpiry(now, sub.PlanID, sub.ExpiresAt, product)
	paymentInfo, err := o.requestPayment(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("request payment: %w", err)
	}

	order := &database.Order{
		SubscriptionID:    sub.ID,
		ProductID:         product.ID,
		Status:            database.OrderStatusPending,
		AmountCents:       product.PriceCents,
		Currency:          product.Currency,
		PaymentProvider:   paymentInfo.Provider,
		ProviderPaymentID: paymentInfo.PaymentID,
	}
	if product.PriceCents == 0 {
		order.Status = database.OrderStatusPaid
		order.PaidAt = &now
		order.ActivatedAt = &now
		order.ExpiresAt = &newExpiry
	}

	if err := o.db.CreateOrder(ctx, order); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	if product.PriceCents > 0 {
		order.ProviderPaymentID = paymentInfo.PaymentID
		return order, nil
	}

	sub.PlanID = product.PlanID
	sub.ProductID = &product.ID
	sub.ExpiresAt = &newExpiry
	sub.PricePaidCents = product.PriceCents
	sub.Currency = &product.Currency
	sub.StartedAt = &now
	if err := o.db.UpdateSubscription(ctx, sub); err != nil {
		return nil, fmt.Errorf("update subscription: %w", err)
	}

	if planChanged && o.syncSvc != nil {
		if err := o.syncSvc.RecalculateNodes(ctx, sub.ID, oldPlanID); err != nil {
			logger.Warn("recalculate nodes failed (will retry)", zap.Error(err))
		}
		if err := o.syncSvc.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("sync subscription failed (will retry)", zap.Error(err))
		}
	}

	return order, nil
}

// requestPayment is a stub for external payment integration.
func (o *OrderService) requestPayment(ctx context.Context, order *database.Order) (*PaymentInfo, error) {
	_ = ctx
	_ = order
	paymentID := fmt.Sprintf("fake-payment-%d", time.Now().UTC().UnixNano())
	return &PaymentInfo{
		URL:       fmt.Sprintf("https://payment.example/pay/%s", paymentID),
		Provider:  "fake-payment-provider",
		PaymentID: paymentID,
	}, nil
}
