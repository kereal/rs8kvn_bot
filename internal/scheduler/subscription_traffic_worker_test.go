package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTrafficRepo struct {
	targetsFn func(ctx context.Context) ([]database.SubscriptionTrafficTarget, error)
}

func (f *fakeTrafficRepo) GetActiveSubscriptionsWithTrafficLimit(ctx context.Context) ([]database.SubscriptionTrafficTarget, error) {
	return f.targetsFn(ctx)
}

type fakeTrafficSvc struct {
	processFn func(ctx context.Context, sub *database.Subscription) error
}

func (f *fakeTrafficSvc) ProcessTrafficNotifications(ctx context.Context, sub *database.Subscription) error {
	return f.processFn(ctx, sub)
}

func TestTrafficWorker_ProcessIteratesTargets(t *testing.T) {
	t.Parallel()

	calls := 0
	seen := map[uint]bool{}

	repo := &fakeTrafficRepo{
		targetsFn: func(ctx context.Context) ([]database.SubscriptionTrafficTarget, error) {
			return []database.SubscriptionTrafficTarget{
				{Subscription: database.Subscription{ID: 1, TelegramID: 101, PlanID: 1}, TrafficLimit: 1024},
				{Subscription: database.Subscription{ID: 2, TelegramID: 202, PlanID: 1}, TrafficLimit: 1024},
				// Negative telegram_id (trial) must be skipped by the worker.
				{Subscription: database.Subscription{ID: 3, TelegramID: -5, PlanID: 1}, TrafficLimit: 1024},
			}, nil
		},
	}

	svc := &fakeTrafficSvc{
		processFn: func(ctx context.Context, sub *database.Subscription) error {
			calls++
			seen[sub.ID] = true
			return nil
		},
	}

	worker := NewSubscriptionTrafficWorker(repo, svc)
	worker.process(context.Background())

	require.Equal(t, 2, calls, "only subscriptions with a valid positive telegram_id are processed")
	assert.True(t, seen[1])
	assert.True(t, seen[2])
	assert.False(t, seen[3])
}

func TestTrafficWorker_ProcessContinuesOnError(t *testing.T) {
	t.Parallel()

	repo := &fakeTrafficRepo{
		targetsFn: func(ctx context.Context) ([]database.SubscriptionTrafficTarget, error) {
			return []database.SubscriptionTrafficTarget{
				{Subscription: database.Subscription{ID: 1, TelegramID: 101, PlanID: 1}, TrafficLimit: 1024},
			}, nil
		},
	}

	svc := &fakeTrafficSvc{
		processFn: func(ctx context.Context, sub *database.Subscription) error {
			return errors.New("telegram down")
		},
	}

	worker := NewSubscriptionTrafficWorker(repo, svc)
	// Must not panic and must not abort the scan on per-subscription errors.
	assert.NotPanics(t, func() { worker.process(context.Background()) })
}

func TestTrafficWorker_ProcessStopOnRepoError(t *testing.T) {
	t.Parallel()

	repo := &fakeTrafficRepo{
		targetsFn: func(ctx context.Context) ([]database.SubscriptionTrafficTarget, error) {
			return nil, errors.New("db error")
		},
	}

	calls := 0
	svc := &fakeTrafficSvc{
		processFn: func(ctx context.Context, sub *database.Subscription) error {
			calls++
			return nil
		},
	}

	worker := NewSubscriptionTrafficWorker(repo, svc)
	assert.NotPanics(t, func() { worker.process(context.Background()) })
	assert.Equal(t, 0, calls, "no processing when repo query fails")
}