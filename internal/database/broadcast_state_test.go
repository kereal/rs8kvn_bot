package database

// These tests exercise durable recipient state transitions, including leases,
// retries, cancellation, and report generation in the existing broadcasts row.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createBroadcastForStateTest(t *testing.T, svc *Service) *Broadcast {
	t.Helper()
	b := &Broadcast{Name: "state", MessageText: "text", Status: string(BroadcastStatusScheduled)}
	require.NoError(t, svc.CreateBroadcast(context.Background(), b))
	return b
}

func TestBroadcastRecipientStateLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)

	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710001, Username: "user", ClientID: "state-client", SubscriptionID: "state-sub",
		Status: string(SubscriptionStatusActive), PlanID: testFreePlanID(t, svc),
	}).Error)

	claimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, claimed)

	total, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	recipients, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Len(t, recipients, 1)
	assert.Equal(t, BroadcastRecipientSending, recipients[0].Status)
	assert.Equal(t, 1, recipients[0].Attempts)

	require.NoError(t, svc.FinishBroadcastRecipient(ctx, b.ID, recipients[0].ID, recipients[0].Attempts, BroadcastRecipientFailed, "temporary", time.Now().UTC()))
	total, sent, blocked, unreachable, failed, report, err := svc.GetBroadcastRecipientsStats(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Zero(t, sent)
	assert.Zero(t, blocked)
	assert.Zero(t, unreachable)
	assert.Equal(t, int64(1), failed)
	require.Len(t, report.Errors, 1)
	assert.Equal(t, int64(710001), report.Errors[0].TelegramID)

	require.NoError(t, svc.ResetBroadcastFailedRecipients(ctx, b.ID, time.Now().UTC()))
	recipients, err = svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Len(t, recipients, 1)
	assert.Equal(t, 2, recipients[0].Attempts)

	require.NoError(t, svc.FinishBroadcastRecipient(ctx, b.ID, recipients[0].ID, recipients[0].Attempts, BroadcastRecipientSent, "", time.Now().UTC()))
	_, sent, _, _, failed, _, err = svc.GetBroadcastRecipientsStats(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), sent)
	assert.Zero(t, failed)
}

func TestBroadcastRecipientSnapshotIsImmutableAndStoredOnBroadcast(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	freePlan := testFreePlanID(t, svc)

	for i, id := range []int64{710011, 710012} {
		require.NoError(t, svc.db.Create(&Subscription{
			TelegramID: id, Username: "user", ClientID: "immutable-client-" + string(rune('a'+i)), SubscriptionID: "immutable-sub-" + string(rune('a'+i)),
			Status: string(SubscriptionStatusActive), PlanID: freePlan,
		}).Error)
	}

	first, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), first)

	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710013, Username: "late", ClientID: "immutable-client-c", SubscriptionID: "immutable-sub-c",
		Status: string(SubscriptionStatusActive), PlanID: freePlan,
	}).Error)
	second, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), second)

	stored, err := svc.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, stored.RecipientsState)
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(stored.RecipientsState), &raw))
	assert.Equal(t, true, raw["snapshot"])
}

func TestBroadcastRecipientClaimIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	freePlan := testFreePlanID(t, svc)
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710021, Username: "race", ClientID: "race-client", SubscriptionID: "race-sub",
		Status: string(SubscriptionStatusActive), PlanID: freePlan,
	}).Error)
	broadcastClaimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, broadcastClaimed)
	require.NoError(t, func() error { _, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{}); return err }())

	var wg sync.WaitGroup
	var mu sync.Mutex
	claimedCount := 0
	var claimErrs []error
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recipients, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 1)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				claimErrs = append(claimErrs, err)
				return
			}
			claimedCount += len(recipients)
		}()
	}
	wg.Wait()
	require.NoError(t, errors.Join(claimErrs...))
	assert.Equal(t, 1, claimedCount)
}

func TestBroadcastRecipientFinishRejectsStaleLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	freePlan := testFreePlanID(t, svc)
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710041, Username: "stale", ClientID: "stale-client", SubscriptionID: "stale-sub",
		Status: string(SubscriptionStatusActive), PlanID: freePlan,
	}).Error)
	require.True(t, func() bool {
		claimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
		return err == nil && claimed
	}())
	_, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)
	first, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NoError(t, svc.ResetBroadcastFailedRecipients(ctx, b.ID, time.Now().UTC()))
	second, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Greater(t, second[0].Attempts, first[0].Attempts)
	err = svc.FinishBroadcastRecipient(ctx, b.ID, first[0].ID, first[0].Attempts, BroadcastRecipientSent, "", time.Now().UTC())
	assert.ErrorIs(t, err, ErrBroadcastRecipientStale)
	require.NoError(t, svc.FinishBroadcastRecipient(ctx, b.ID, second[0].ID, second[0].Attempts, BroadcastRecipientBlocked, "blocked", time.Now().UTC()))
	_, sent, blocked, _, _, report, err := svc.GetBroadcastRecipientsStats(ctx, b.ID)
	require.NoError(t, err)
	assert.Zero(t, sent)
	assert.Equal(t, int64(1), blocked)
	assert.Equal(t, []int64{710041}, report.Blocked)
}

func TestBroadcastRecipientFinishMissingRecipient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	freePlan := testFreePlanID(t, svc)
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710042, Username: "ghost", ClientID: "ghost-client", SubscriptionID: "ghost-sub",
		Status: string(SubscriptionStatusActive), PlanID: freePlan,
	}).Error)
	require.True(t, func() bool {
		claimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
		return err == nil && claimed
	}())
	_, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)

	// The broadcast row exists, but this recipient ID was never claimed:
	// the error must name the recipient, not the campaign.
	err = svc.FinishBroadcastRecipient(ctx, b.ID, 999999, 1, BroadcastRecipientFailed, "boom", time.Now().UTC())
	assert.ErrorIs(t, err, ErrBroadcastRecipientNotFound)
	assert.NotErrorIs(t, err, ErrBroadcastNotFound)
}

func TestBroadcastRecipientClaimStopsAfterCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	freePlan := testFreePlanID(t, svc)
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710051, Username: "cancel", ClientID: "cancel-client", SubscriptionID: "cancel-sub",
		Status: string(SubscriptionStatusActive), PlanID: freePlan,
	}).Error)
	claimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)
	canceled, err := svc.CancelBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, canceled)
	recipients, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 1)
	require.NoError(t, err)
	assert.Empty(t, recipients)
}

func TestBroadcastRecipientLeaseRecoveryAndCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	freePlan := testFreePlanID(t, svc)
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710031, Username: "lease", ClientID: "lease-client", SubscriptionID: "lease-sub",
		Status: string(SubscriptionStatusActive), PlanID: freePlan,
	}).Error)
	broadcastClaimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, broadcastClaimed)
	require.NoError(t, func() error { _, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{}); return err }())

	old := time.Now().UTC().Add(-broadcastRecipientLease - time.Second)
	recipients, err := svc.ClaimBroadcastRecipients(ctx, b.ID, old, 1)
	require.NoError(t, err)
	require.Len(t, recipients, 1)
	require.NoError(t, svc.RecoverStaleBroadcastRecipients(ctx, b.ID, time.Now().UTC().Add(-broadcastRecipientLease)))
	recipients, err = svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.Len(t, recipients, 1)

	canceled, err := svc.CancelBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, canceled)
	stored, err := svc.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, string(BroadcastStatusCanceled), stored.Status)
}

// TestBroadcastRecipientFinishSucceedsAfterCancel guards against the cancel
// releasing sending leases: an in-flight delivery that completes after the
// cancel transition must still be recorded (and counted), not rejected as stale.
func TestBroadcastRecipientFinishSucceedsAfterCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	freePlan := testFreePlanID(t, svc)
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710061, Username: "inflight", ClientID: "inflight-client", SubscriptionID: "inflight-sub",
		Status: string(SubscriptionStatusActive), PlanID: freePlan,
	}).Error)
	require.True(t, func() bool {
		claimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
		return err == nil && claimed
	}())
	_, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)
	claimed, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	canceled, err := svc.CancelBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, canceled)

	// The in-flight delivery finishes after the cancel transition: it must be
	// accepted and counted as delivered, not rejected as a stale lease.
	require.NoError(t, svc.FinishBroadcastRecipient(ctx, b.ID, claimed[0].ID, claimed[0].Attempts, BroadcastRecipientSent, "", time.Now().UTC()))
	total, sent, blocked, unreachable, failed, report, err := svc.GetBroadcastRecipientsStats(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, int64(1), sent)
	assert.Zero(t, blocked)
	assert.Zero(t, unreachable)
	assert.Zero(t, failed)
	assert.Equal(t, []int64{710061}, report.Delivered)
}
