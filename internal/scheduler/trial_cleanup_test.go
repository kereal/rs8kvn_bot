package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

func newTestSubService(t testing.TB, db *database.Service) *service.SubscriptionService {
	t.Helper()
	cfg := &config.Config{
		TrialDurationHours: 1,
	}
	return service.NewSubscriptionService(db, nil, nil, cfg, "", nil)
}

func TestTrialCleanupScheduler_New(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	assert.NotNil(t, scheduler)
}

func TestTrialCleanupScheduler_RunCleanup_NoExpiredTrials(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	ctx := context.Background()
	scheduler.runCleanup(ctx)
}

func TestTrialCleanupScheduler_RunCleanup_WithExpiredTrials(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	expiredSub := &database.Subscription{
		TelegramID:     0,
		ClientID:       "expired-client-1",
		SubscriptionID: "expired-sub-1",
		PlanID:         1,
		ExpiresAt:      time.Now().Add(-1 * time.Hour),
		Status:         "active",
		CreatedAt:      time.Now().Add(-2 * time.Hour),
	}
	err = db.CreateSubscription(ctx, expiredSub, "")
	require.NoError(t, err)

	expiredSub2 := &database.Subscription{
		TelegramID:     0,
		ClientID:       "expired-client-2",
		SubscriptionID: "expired-sub-2",
		PlanID:         1,
		ExpiresAt:      time.Now().Add(-1 * time.Hour),
		Status:         "active",
		CreatedAt:      time.Now().Add(-3 * time.Hour),
	}
	err = db.CreateSubscription(ctx, expiredSub2, "")
	require.NoError(t, err)

	activeSub := &database.Subscription{
		TelegramID:     0,
		ClientID:       "active-client",
		SubscriptionID: "active-sub",
		PlanID:         1,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		Status:         "active",
		CreatedAt:      time.Now().Add(-30 * time.Minute),
	}
	err = db.CreateSubscription(ctx, activeSub, "")
	require.NoError(t, err)

	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	scheduler.runCleanup(ctx)

	remaining, err := db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Len(t, remaining, 1, "Only the active trial should remain")
	if len(remaining) > 0 {
		assert.Equal(t, "active-sub", remaining[0].SubscriptionID)
	}
}

func TestTrialCleanupScheduler_RunCleanup_XUIFailure(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	expiredSub := &database.Subscription{
		TelegramID:     0,
		ClientID:       "client-xui-fail",
		SubscriptionID: "sub-xui-fail",
		PlanID:         1,
		Status:         "active",
		CreatedAt:      time.Now().Add(-2 * time.Hour),
	}
	err = db.CreateSubscription(ctx, expiredSub, "")
	require.NoError(t, err)

	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	scheduler.runCleanup(ctx)

	remaining, err := db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Empty(t, remaining)
}

func TestTrialCleanupScheduler_Start_ContextCancel(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	subService := newTestSubService(t, db)
	scheduler := NewTrialCleanupScheduler(subService)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		scheduler.Start(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Scheduler should stop after context cancel")
	}
}
