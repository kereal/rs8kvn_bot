package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newReminderTestSub(t *testing.T, svc *Service, telegramID int64, expiresAt time.Time) *Subscription {
	t.Helper()

	sub := &Subscription{
		TelegramID:     telegramID,
		Username:       "reminder-user",
		ClientID:       "client-reminder",
		SubscriptionID: "sub-reminder",
		ExpiresAt:      ptrTime(expiresAt),
		Status:         "active",
		PlanID:         testFreePlanID(t, svc),
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), sub, ""))

	return sub
}

// TestClaimReminder_ExpiryMatchGatesClaim verifies the atomic bit semantics:
// the bit is set only when both the subscription id and the stored expires_at
// match the caller's view.
func TestClaimReminder_ExpiryMatchGatesClaim(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	expiresAt := time.Now().UTC().Truncate(time.Second)
	sub := newReminderTestSub(t, svc, 920001, expiresAt)

	claimed, err := svc.ClaimReminder(ctx, sub.ID, 1, expiresAt)
	require.NoError(t, err)
	assert.True(t, claimed, "first claim with matching expiry must set the bit")

	claimedAgain, err := svc.ClaimReminder(ctx, sub.ID, 1, expiresAt)
	require.NoError(t, err)
	assert.False(t, claimedAgain, "second claim of the same bit must be a no-op")

	// A claim whose expires_at does not match the stored value must not set
	// the bit and must not be reported as claimed.
	staleExpiry := expiresAt.Add(-time.Hour)
	claimedStale, err := svc.ClaimReminder(ctx, sub.ID, 2, staleExpiry)
	require.NoError(t, err)
	assert.False(t, claimedStale, "claim with mismatched expiry must not set the bit")

	var got Subscription
	require.NoError(t, svc.db.First(&got, sub.ID).Error)
	assert.Equal(t, 1, got.RemindersSent, "only the first claim must have mutated the bit")
}

// TestReleaseReminder_KeepsBitWhenExpiryChanged locks the double-letter
// invariant: releasing a reminder against a stale expiry (the subscription was
// renewed in between) must not clear the bit, so a renewed subscription is not
// reminded twice for the same cycle.
func TestReleaseReminder_KeepsBitWhenExpiryChanged(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	expiresAt := time.Now().UTC().Truncate(time.Second)
	sub := newReminderTestSub(t, svc, 920002, expiresAt)

	claimed, err := svc.ClaimReminder(ctx, sub.ID, 1, expiresAt)
	require.NoError(t, err)
	require.True(t, claimed)

	// Renewal moves expires_at after the reminder was claimed. Real callers
	// load the row first because UpdateSubscription persists reminders_sent.
	renewed := expiresAt.Add(30 * 24 * time.Hour)
	reloaded, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	reloaded.ExpiresAt = ptrTime(renewed)
	require.NoError(t, svc.UpdateSubscription(ctx, reloaded))

	// Release against the pre-renewal expiry must be a no-op on the bit...
	require.NoError(t, svc.ReleaseReminder(ctx, sub.ID, 1, expiresAt))

	var got Subscription
	require.NoError(t, svc.db.First(&got, sub.ID).Error)
	assert.Equal(t, 1, got.RemindersSent, "release with stale expiry must not clear the bit")

	// ...while release against the current expiry does clear it.
	require.NoError(t, svc.ReleaseReminder(ctx, sub.ID, 1, renewed))
	require.NoError(t, svc.db.First(&got, sub.ID).Error)
	assert.Equal(t, 0, got.RemindersSent, "release with matching expiry must clear the bit")
}

func TestClaimReminder_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.ClaimReminder(context.Background(), 999999, 1, time.Now().UTC())
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestReleaseReminder_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	err := svc.ReleaseReminder(context.Background(), 999999, 1, time.Now().UTC())
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}
