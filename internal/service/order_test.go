package service

import (
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
)

func TestCalculateProductExpiry_SamePlanExtends(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &database.Product{PlanID: 1, DurationDays: 30}

	result := calculateProductExpiry(now, 1, &oldExpiry, product)
	assert.Equal(t, oldExpiry.AddDate(0, 0, 30), result)
}

func TestCalculateProductExpiry_NilExpiryUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	product := &database.Product{PlanID: 1, DurationDays: 30}

	result := calculateProductExpiry(now, 1, nil, product)
	assert.Equal(t, now.AddDate(0, 0, 30), result)
}

func TestCalculateProductExpiry_DifferentPlanUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &database.Product{PlanID: 2, DurationDays: 30}

	result := calculateProductExpiry(now, 1, &oldExpiry, product)
	assert.Equal(t, now.AddDate(0, 0, 30), result)
}
