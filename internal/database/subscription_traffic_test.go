package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrafficReminder_ClaimAndRelease(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "traffic-claim-plan", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	sub := &Subscription{
		TelegramID:     900001,
		Username:       "trafficclaim",
		ClientID:       "client-trafficclaim",
		SubscriptionID: "sub-trafficclaim",
		Status:         "active",
		PlanID:         plan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	claimed, err := svc.ClaimTrafficReminder(ctx, sub.ID, TrafficBit90)
	require.NoError(t, err)
	assert.True(t, claimed, "first claim must succeed")

	// Second claim of the same bit is a no-op.
	claimedAgain, err := svc.ClaimTrafficReminder(ctx, sub.ID, TrafficBit90)
	require.NoError(t, err)
	assert.False(t, claimedAgain, "bit is already set")

	// Different bit can still be claimed.
	claimedExhausted, err := svc.ClaimTrafficReminder(ctx, sub.ID, TrafficBitExhausted)
	require.NoError(t, err)
	assert.True(t, claimedExhausted)

	// Release clears the bit so it can fire again.
	require.NoError(t, svc.ReleaseTrafficReminder(ctx, sub.ID, TrafficBit90))
	claimedAfterRelease, err := svc.ClaimTrafficReminder(ctx, sub.ID, TrafficBit90)
	require.NoError(t, err)
	assert.True(t, claimedAfterRelease, "released bit must be claimable again")
}

func TestTrafficReminder_ClaimNotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.ClaimTrafficReminder(ctx, 999999, TrafficBit90)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestTrafficReminder_ReleaseNotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.ReleaseTrafficReminder(ctx, 999999, TrafficBit90)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestGetActiveSubscriptionsWithTrafficLimit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Plan with a traffic quota.
	limitedPlan := &Plan{Name: "limited-plan-1", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(limitedPlan).Error)

	// Active subscription on the limited plan -> should be returned.
	active := &Subscription{
		TelegramID:     900101,
		Username:       "limited-active",
		ClientID:       "client-limited-active",
		SubscriptionID: "sub-limited-active",
		Status:         "active",
		PlanID:         limitedPlan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, active, ""))

	// Revoked subscription on the limited plan -> must be excluded.
	revoked := &Subscription{
		TelegramID:     900102,
		Username:       "limited-revoked",
		ClientID:       "client-limited-rev",
		SubscriptionID: "sub-limited-rev",
		Status:         "revoked",
		PlanID:         limitedPlan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, revoked, ""))

	// Unlimited plan -> must be excluded regardless of status.
	unlimitedPlan := &Plan{Name: "unlimited-plan-1", DevicesLimit: 1, TrafficLimit: 0}
	require.NoError(t, svc.db.WithContext(ctx).Create(unlimitedPlan).Error)

	unlimited := &Subscription{
		TelegramID:     900103,
		Username:       "unlimited-user",
		ClientID:       "client-unlimited",
		SubscriptionID: "sub-unlimited",
		Status:         "active",
		PlanID:         unlimitedPlan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, unlimited, ""))

	targets, err := svc.GetActiveSubscriptionsWithTrafficLimit(ctx)
	require.NoError(t, err)

	require.Len(t, targets, 1)
	assert.Equal(t, active.ID, targets[0].ID)
	assert.Equal(t, int64(1024), targets[0].TrafficLimit)
}

func TestGetActiveSubscriptionsWithTrafficLimit_NoLimited(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	unlimitedPlan := &Plan{Name: "unlimited-plan-2", DevicesLimit: 1, TrafficLimit: 0}
	require.NoError(t, svc.db.WithContext(ctx).Create(unlimitedPlan).Error)

	sub := &Subscription{
		TelegramID:     900104,
		Username:       "unlimited-two",
		ClientID:       "client-unlimited-two",
		SubscriptionID: "sub-unlimited-two",
		Status:         "active",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		PlanID:         unlimitedPlan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	targets, err := svc.GetActiveSubscriptionsWithTrafficLimit(ctx)
	require.NoError(t, err)
	assert.Empty(t, targets)
}