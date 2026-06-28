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

func ptrTime(t time.Time) *time.Time { return &t }

func newTestSubServiceForExpire(t testing.TB, db *database.Service) *service.SubscriptionService {
	t.Helper()
	cfg := &config.Config{
		TrialDurationHours: 1,
	}
	return service.NewSubscriptionService(db, nil, nil, nil, cfg)
}

func TestSubscriptionExpireWorker_process_FindsAndExpires(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	ctx := context.Background()

	plan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	expiredSub := &database.Subscription{
		TelegramID:      99991,
		Username:        "expireuser1",
		ClientID:        "c-expire1",
		SubscriptionID: "s-expire1",
		Status:          "active",
		PlanID:          plan.ID,
		ExpiresAt:       ptrTime(time.Now().Add(-1 * time.Hour)),
		PricePaidCents:  100,
		Currency:       testutil.PtrString("RUB"),
		ProductID:      testutil.PtrUint(1),
		StartedAt:       ptrTime(time.Now().Add(-48 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, expiredSub, ""))

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	worker.process(ctx)

	var updated database.Subscription
require.NoError(t, db.GetDB().WithContext(ctx).First(&updated, expiredSub.ID).Error)
assert.Equal(t, "active", updated.Status)
assert.Equal(t, plan.ID, updated.PlanID)
}

func TestSubscriptionExpireWorker_process_EmptyResult(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	ctx := context.Background()

	plan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	activeSub := &database.Subscription{
		TelegramID:      99992,
		Username:        "noexpire",
		ClientID:        "c-noexpire",
		SubscriptionID: "s-noexpire",
		Status:          "active",
		PlanID:          plan.ID,
		ExpiresAt:       ptrTime(time.Now().Add(24 * time.Hour)),
		PricePaidCents:  100,
		Currency:       testutil.PtrString("RUB"),
		ProductID:      testutil.PtrUint(1),
		StartedAt:       ptrTime(time.Now().Add(-1 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, activeSub, ""))

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	worker.process(ctx)

	var unchanged database.Subscription
	require.NoError(t, db.GetDB().WithContext(ctx).First(&unchanged, activeSub.ID).Error)
	assert.Equal(t, "active", unchanged.Status)
	assert.False(t, unchanged.ExpiresAt == nil)
}

func TestSubscriptionExpireWorker_Run_ContextCancel(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SubscriptionExpireWorker.Run should stop after context cancel")
	}
}
