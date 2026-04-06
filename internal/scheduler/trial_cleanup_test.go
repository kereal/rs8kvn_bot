package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

type mockXUIClientForCleanup struct {
	deletedClients map[string]int
	deleteErr      error
	mu             sync.Mutex
}

func (m *mockXUIClientForCleanup) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedClients[clientID] = inboundID
	return nil
}

func TestTrialCleanupScheduler_New(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	scheduler := NewTrialCleanupScheduler(db, &mockXUIClientForCleanup{}, 3)

	assert.NotNil(t, scheduler)
	assert.Equal(t, 3, scheduler.trialHours)
}

func TestTrialCleanupScheduler_RunCleanup_NoExpiredTrials(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	mockXUI := &mockXUIClientForCleanup{deletedClients: make(map[string]int)}
	scheduler := NewTrialCleanupScheduler(db, mockXUI, 3)

	ctx := context.Background()
	scheduler.runCleanup(ctx)

	assert.Empty(t, mockXUI.deletedClients)
}

func TestTrialCleanupScheduler_RunCleanup_WithExpiredTrials(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	expiredSub := &database.Subscription{
		TelegramID:     0, // unbound trial
		ClientID:       "expired-client-1",
		SubscriptionID: "expired-sub-1",
		InboundID:      1,
		IsTrial:        true,
		ExpiryTime:     time.Now().Add(-1 * time.Hour),
		Status:         "active",
		CreatedAt:      time.Now().Add(-2 * time.Hour), // old created_at - will be cleaned
	}
	err = db.CreateSubscription(ctx, expiredSub)
	require.NoError(t, err)

	expiredSub2 := &database.Subscription{
		TelegramID:     0, // unbound trial
		ClientID:       "expired-client-2",
		SubscriptionID: "expired-sub-2",
		InboundID:      1,
		IsTrial:        true,
		ExpiryTime:     time.Now().Add(-1 * time.Hour),
		Status:         "active",
		CreatedAt:      time.Now().Add(-3 * time.Hour), // old created_at - will be cleaned
	}
	err = db.CreateSubscription(ctx, expiredSub2)
	require.NoError(t, err)

	activeSub := &database.Subscription{
		TelegramID:     0, // unbound trial
		ClientID:       "active-client",
		SubscriptionID: "active-sub",
		InboundID:      1,
		IsTrial:        true,
		ExpiryTime:     time.Now().Add(1 * time.Hour),
		Status:         "active",
		CreatedAt:      time.Now().Add(-30 * time.Minute), // recent - won't be cleaned
	}
	err = db.CreateSubscription(ctx, activeSub)
	require.NoError(t, err)

	mockXUI := &mockXUIClientForCleanup{deletedClients: make(map[string]int)}
	scheduler := NewTrialCleanupScheduler(db, mockXUI, 1) // 1 hour cutoff

	scheduler.runCleanup(ctx)

	assert.Len(t, mockXUI.deletedClients, 2, "Should delete both old trials (created > 1h ago)")
	assert.Contains(t, mockXUI.deletedClients, "expired-client-1")
	assert.Contains(t, mockXUI.deletedClients, "expired-client-2")

	_, err = db.GetByTelegramID(ctx, 111111)
	assert.Error(t, err, "Unbound trial with telegram_id != 0 won't be found by GetByTelegramID")
}

func TestTrialCleanupScheduler_RunCleanup_XUIFailure(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	expiredSub := &database.Subscription{
		TelegramID:     0, // unbound trial
		ClientID:       "client-xui-fail",
		SubscriptionID: "sub-xui-fail",
		InboundID:      1,
		IsTrial:        true,
		Status:         "active",
		CreatedAt:      time.Now().Add(-2 * time.Hour), // old enough to be cleaned
	}
	err = db.CreateSubscription(ctx, expiredSub)
	require.NoError(t, err)

	mockXUI := &mockXUIClientForCleanup{
		deletedClients: make(map[string]int),
		deleteErr:      assert.AnError,
	}
	scheduler := NewTrialCleanupScheduler(db, mockXUI, 1)

	scheduler.runCleanup(ctx)

	assert.Empty(t, mockXUI.deletedClients, "Should not delete when XUI fails")
}

func TestTrialCleanupScheduler_Start_ContextCancel(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	mockXUI := &mockXUIClientForCleanup{deletedClients: make(map[string]int)}
	scheduler := NewTrialCleanupScheduler(db, mockXUI, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		scheduler.Start(ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Scheduler should stop after context cancel")
	}
}
