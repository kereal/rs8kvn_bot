package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ==================== GetSubscriptionStatus Tests ====================

func TestGetSubscriptionStatus_Active(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 9001, "status-user", "client-status-1")

	status, expiryTime, err := svc.GetSubscriptionStatus(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	assert.Equal(t, "active", status)
	assert.False(t, expiryTime.IsZero(), "expiry time should be set for active subscription with future expiry")
}

func TestGetSubscriptionStatus_Revoked(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 9002, "status-revoked", "client-status-2")
	require.NoError(t, svc.db.WithContext(ctx).Model(sub).Update("status", "revoked").Error)

	status, _, err := svc.GetSubscriptionStatus(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	assert.Equal(t, "revoked", status)
}

func TestGetSubscriptionStatus_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, _, err := svc.GetSubscriptionStatus(ctx, "nonexistent-sub-id")
	assert.Error(t, err)
}

// ==================== GetWithPlanAndNodes Tests ====================

func TestGetWithPlanAndNodes_ActiveSubscription(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Create a subscription with the trial plan
	plan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	sub := &Subscription{
		TelegramID:     9100,
		Username:       "plan-user",
		ClientID:       "client-plan-1",
		SubscriptionID: "sub-plan-1",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	result, err := svc.GetWithPlanAndNodes(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	assert.Equal(t, sub.SubscriptionID, result.Subscription.SubscriptionID)
	assert.Equal(t, plan.ID, result.Plan.ID)
}

func TestGetWithPlanAndNodes_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetWithPlanAndNodes(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestGetWithPlanAndNodes_RevokedSubscription(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 9101, "plan-revoked", "client-plan-rev")
	require.NoError(t, svc.db.WithContext(ctx).Model(sub).Update("status", "revoked").Error)

	_, err := svc.GetWithPlanAndNodes(ctx, sub.SubscriptionID)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

// ==================== UpdateDevices Tests ====================

func TestService_UpdateDevices_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 9200, "dev-user", "client-dev-1")

	devices := `[{"x-hwid":"abc","timestamp":"2025-01-01T00:00:00Z"}]`
	err := svc.UpdateDevices(ctx, sub.ID, devices)
	require.NoError(t, err)

	got, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.JSONEq(t, devices, got.Devices)
}

func TestService_UpdateDevices_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.UpdateDevices(ctx, 99999, "[]")
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

// ==================== UpdateIPs Tests ====================

func TestService_UpdateIPs_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 9300, "ip-user", "client-ip-1")

	ips := `[{"10.0.0.1":"2025-01-01T00:00:00Z"}]`
	err := svc.UpdateIPs(ctx, sub.ID, ips)
	require.NoError(t, err)

	got, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.JSONEq(t, ips, got.Ips)
}

func TestService_UpdateIPs_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.UpdateIPs(ctx, 99999, "[]")
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

// ==================== ExpireSubscription Tests ====================

func TestExpireSubscription_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 9400, "expire-user", "client-expire-1")
	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	err = svc.ExpireSubscription(ctx, sub.ID, freePlan.ID)
	require.NoError(t, err)

	got, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)
	assert.Nil(t, got.ExpiresAt)
	assert.Equal(t, freePlan.ID, got.PlanID)
}

func TestExpireSubscription_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.ExpireSubscription(ctx, 99999, 1)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// ==================== GetExpiredPaidSubscriptions Tests ====================

func TestGetExpiredPaidSubscriptions_WithExpiredPaidSub(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Create a paid plan subscription that has expired
	paidPlan := &Plan{Name: "paid-plan-exp", DevicesLimit: 3, TrafficLimit: 100 * 1024 * 1024 * 1024, IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(paidPlan).Error)

	pastTime := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Minute)
	sub := &Subscription{
		TelegramID:     9500,
		Username:       "expired-paid-user",
		ClientID:       "client-exp-paid-1",
		SubscriptionID: "sub-exp-paid-1",
		Status:         "active",
		PlanID:         paidPlan.ID,
		ExpiresAt:      &pastTime,
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	subs, err := svc.GetExpiredPaidSubscriptions(ctx)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, sub.SubscriptionID, subs[0].SubscriptionID)
}

func TestGetExpiredPaidSubscriptions_NoExpired(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Active subscription with future expiry — should not appear
	_ = createTestSubscription(t, svc, 9501, "not-expired", "client-not-exp")

	subs, err := svc.GetExpiredPaidSubscriptions(ctx)
	require.NoError(t, err)
	assert.Empty(t, subs)
}

func TestGetExpiredPaidSubscriptions_FreePlanExcluded(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Free plan subscription that has "expired" — should NOT be returned
	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	pastTime := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Minute)
	sub := &Subscription{
		TelegramID:     9502,
		Username:       "free-expired-user",
		ClientID:       "client-free-exp-1",
		SubscriptionID: "sub-free-exp-1",
		Status:         "active",
		PlanID:         freePlan.ID,
		ExpiresAt:      &pastTime,
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	subs, err := svc.GetExpiredPaidSubscriptions(ctx)
	require.NoError(t, err)
	assert.Empty(t, subs, "free plan subscriptions should be excluded")
}

// ==================== GetSubscription Tests ====================

func TestService_GetSubscription_Active(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 9600, "getsub-user", "client-getsub-1")

	got, err := svc.GetSubscription(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, got.ID)
	assert.Equal(t, sub.TelegramID, got.TelegramID)
}

func TestService_GetSubscription_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetSubscription(ctx, "nonexistent-sub")
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}
