package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrderRepository_UpdateOrderPaidStatus(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-order", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	sub := &Subscription{
		TelegramID:     909,
		Username:       "orderuser",
		ClientID:       "c-order",
		SubscriptionID: "s-order",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{
		PlanID:        plan.ID,
		Name:          "basic",
		DurationDays:  7,
		PriceCents:    9900,
		Currency:      "RUB",
		IsActive:      true,
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPending,
		AmountCents:    product.PriceCents,
		Currency:       product.Currency,
	}
	require.NoError(t, svc.CreateOrder(ctx, order))

	before := time.Now().UTC().Truncate(time.Minute)
	require.NoError(t, svc.UpdateOrderPaidStatus(ctx, order.ID))
	after := time.Now().UTC().Truncate(time.Minute)

	var got Order
	require.NoError(t, svc.db.WithContext(ctx).First(&got, order.ID).Error)
	assert.Equal(t, OrderStatusPaid, got.Status)
	assert.NotNil(t, got.PaidAt)
	assert.True(t, got.PaidAt.After(before) || got.PaidAt.Equal(before), "paid_at should be >= before")
	assert.True(t, got.PaidAt.Before(after) || got.PaidAt.Equal(after), "paid_at should be <= after")
}

func TestOrderRepository_UpdateOrderPaidStatus_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.UpdateOrderPaidStatus(ctx, 99999)
	assert.Error(t, err)
}

func TestOrderRepository_UpdateOrderActivatedAt(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-activate", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	sub := &Subscription{
		TelegramID:     919,
		Username:       "activateuser",
		ClientID:       "c-activate",
		SubscriptionID: "s-activate",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{
		PlanID:        plan.ID,
		Name:          "monthly",
		DurationDays:  30,
		PriceCents:    149900,
		Currency:      "RUB",
		IsActive:      true,
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPending,
		AmountCents:    product.PriceCents,
		Currency:       product.Currency,
	}
	require.NoError(t, svc.CreateOrder(ctx, order))

	activatedAt := time.Now().UTC()
	expiresAt := activatedAt.AddDate(0, 0, 30)
	require.NoError(t, svc.UpdateOrderActivatedAt(ctx, order.ID, activatedAt, expiresAt))

	var got Order
	require.NoError(t, svc.db.WithContext(ctx).First(&got, order.ID).Error)
	assert.Equal(t, activatedAt.UTC(), got.ActivatedAt.UTC())
	require.NotNil(t, got.ExpiresAt)
	assert.Equal(t, expiresAt.UTC(), (*got.ExpiresAt).UTC())
}

func TestOrderRepository_UpdateOrderActivatedAt_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.UpdateOrderActivatedAt(ctx, 99999, time.Now(), time.Now())
	assert.Error(t, err)
}
