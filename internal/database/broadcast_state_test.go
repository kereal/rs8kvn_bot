package database

// These tests exercise durable recipient state transitions, including leases,
// retries, cancellation, and report generation in the existing broadcasts row.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	total, sent, blocked, unreachable, failed, report, err = svc.GetBroadcastRecipientsStats(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, int64(1), sent)
	assert.Zero(t, blocked)
	assert.Zero(t, unreachable)
	assert.Zero(t, failed)
	assert.Empty(t, report.Errors)
}

func TestBroadcastRecipientProgressCheckpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 710001, Username: "user", ClientID: "progress-client", SubscriptionID: "progress-sub",
		Status: string(SubscriptionStatusActive), PlanID: testFreePlanID(t, svc),
	}).Error)

	claimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, claimed)
	_, err = svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)
	recipients, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Len(t, recipients, 1)
	r := recipients[0]

	// Прогресс фиксируется только под действующей арендой (Sending + попытка).
	require.NoError(t, svc.UpdateBroadcastRecipientProgress(ctx, b.ID, r.ID, r.Attempts, 1, time.Now().UTC()))

	// Сохранённый прогресс переживает восстановление аренды и виден при новом claim.
	require.NoError(t, svc.RecoverStaleBroadcastRecipients(ctx, b.ID, time.Now().UTC().Add(time.Minute)))
	reclaimed, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Len(t, reclaimed, 1)
	assert.Equal(t, 1, reclaimed[0].NextChunk, "recovered recipient must resume from the saved chunk")
	assert.Equal(t, 2, reclaimed[0].Attempts)

	// Lease guard: чужая попытка и отсутствующий получатель отклоняются.
	err = svc.UpdateBroadcastRecipientProgress(ctx, b.ID, reclaimed[0].ID, 99, 5, time.Now().UTC())
	require.ErrorIs(t, err, ErrBroadcastRecipientStale)
	err = svc.UpdateBroadcastRecipientProgress(ctx, b.ID, 424242, 1, 5, time.Now().UTC())
	require.ErrorIs(t, err, ErrBroadcastRecipientNotFound)

	require.NoError(t, svc.FinishBroadcastRecipient(ctx, b.ID, reclaimed[0].ID, reclaimed[0].Attempts, BroadcastRecipientSent, "", time.Now().UTC()))
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
		wg.Go(func() {
			recipients, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), 1)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				claimErrs = append(claimErrs, err)
				return
			}
			claimedCount += len(recipients)
		})
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
	total, sent, blocked, unreachable, failed, report, err := svc.GetBroadcastRecipientsStats(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Zero(t, sent)
	assert.Equal(t, int64(1), blocked)
	assert.Zero(t, unreachable)
	assert.Zero(t, failed)
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

// TestBroadcastRecipientConcurrentFinalizationsAllPersist reproduces the
// release-blocking failure: a batch of recipients whose terminal outcomes are
// recorded concurrently (the worker finalizes one recipient per goroutine, up
// to broadcastConcurrency parallel sends). Every finalization goes through
// updateBroadcastRecipientState, which used to run as a deferred GORM
// transaction: with the connection pool (MaxOpenConns=4) several goroutines
// read the same recipients_state snapshot and then tried to write it, and the
// read-to-write upgrade of a stale WAL snapshot surfaced as SQLITE_BUSY_SNAPSHOT
// ("database is locked"), which _busy_timeout does not cover. The losing
// finalization was dropped, so the campaign stayed running with an unaccounted
// recipient. All finalizations must persist: terminal states and counts are
// asserted explicitly, a silent lost write must fail this test.
func TestBroadcastRecipientConcurrentFinalizationsAllPersist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	b := createBroadcastForStateTest(t, svc)
	freePlan := testFreePlanID(t, svc)

	// More recipients than pool connections (config.MaxOpenConns == 4) so
	// several writer transactions are live at once, matching the worker's
	// concurrent batch finalization.
	const recipientsCount = 8
	for i := range recipientsCount {
		require.NoError(t, svc.db.Create(&Subscription{
			TelegramID:     710100 + int64(i),
			Username:       "finish-race",
			ClientID:       fmt.Sprintf("finish-client-%02d", i),
			SubscriptionID: fmt.Sprintf("finish-sub-%02d", i),
			Status:         string(SubscriptionStatusActive),
			PlanID:         freePlan,
		}).Error)
	}

	claimed, err := svc.ClaimBroadcast(ctx, b.ID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed)
	total, err := svc.SnapshotBroadcastRecipients(ctx, b.ID, BroadcastFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(recipientsCount), total)
	recipients, err := svc.ClaimBroadcastRecipients(ctx, b.ID, time.Now().UTC(), recipientsCount)
	require.NoError(t, err)
	require.Len(t, recipients, recipientsCount)
	for _, r := range recipients {
		require.Equal(t, BroadcastRecipientSending, r.Status, "every recipient must be claimed exactly once before finishing")
	}

	// Every recipient is finalized by a distinct goroutine, mirroring the
	// worker's per-recipient goroutines inside one claim batch.
	var wg sync.WaitGroup
	finishErrs := make([]error, recipientsCount)
	for i := range recipientsCount {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			r := recipients[slot]
			finishErrs[slot] = svc.FinishBroadcastRecipient(ctx, b.ID, r.ID, r.Attempts, BroadcastRecipientSent, "", time.Now().UTC())
		}(i)
	}
	wg.Wait()
	// A lock failure here ("database is locked") is the regression: the
	// terminal outcome must persist, not surface as an error.
	require.NoError(t, errors.Join(finishErrs...))

	storedRecipients, err := svc.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	totalStats, sent, blocked, unreachable, failed, report, err := svc.GetBroadcastRecipientsStats(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(recipientsCount), totalStats, "every claimed recipient must be finalized exactly once")
	assert.Equal(t, int64(recipientsCount), sent, "all concurrent finalizations must persist, none lost to a lock error")
	assert.Zero(t, blocked)
	assert.Zero(t, unreachable)
	assert.Zero(t, failed)
	assert.Empty(t, report.NotProcessed, "no recipient may remain sending or pending")
	require.Len(t, report.Delivered, recipientsCount)
	assert.Equal(t, string(BroadcastStatusRunning), storedRecipients.Status, "a DB-only test never runs finishCampaign: the worker transition is asserted through the recipient statistics instead")
}
