package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductRepository_GetProductByID_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "test-plan-product", DevicesLimit: 2, TrafficLimit: 2048}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	product := &Product{
		PlanID:        plan.ID,
		Name:          "premium",
		DurationDays:  30,
		PriceCents:    49900,
		Currency:      "RUB",
		IsActive:      true,
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	got, err := svc.GetProductByID(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, product.ID, got.ID)
	assert.Equal(t, "premium", got.Name)
	assert.Equal(t, uint(plan.ID), got.PlanID)
	assert.Equal(t, 30, got.DurationDays)
	assert.Equal(t, int64(49900), got.PriceCents)
	assert.Equal(t, "RUB", got.Currency)
	assert.True(t, got.IsActive)
}

func TestProductRepository_GetProductByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetProductByID(ctx, 99999)
	assert.Error(t, err)
}
