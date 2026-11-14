package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetOverviewSnapshot_Counts pins the aggregates behind the panel's state
// screen: counts come from existing columns, so the queue depth and the age of
// the oldest queued binding must be readable without any extra bookkeeping.
func TestGetOverviewSnapshot_Counts(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "overview-plan", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	node := &Node{Name: "overview-node", IsActive: true, Host: "http://n1", APIToken: "t1", InboundIDs: `[1]`}
	require.NoError(t, svc.db.WithContext(ctx).Create(node).Error)

	lastRequest := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	active := &Subscription{
		TelegramID: 700001, ClientID: "ov-c1", SubscriptionID: "ov-sub-1",
		Status: "active", PlanID: plan.ID, LastRequest: &lastRequest,
	}
	revoked := &Subscription{
		TelegramID: 700002, ClientID: "ov-c2", SubscriptionID: "ov-sub-2",
		Status: "revoked", PlanID: plan.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, active, ""))
	require.NoError(t, svc.CreateSubscription(ctx, revoked, ""))

	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{
		SubscriptionID: active.ID, NodeID: node.ID, Status: SyncStatusActive,
	}))
	require.NoError(t, svc.CreateSubscriptionNode(ctx, &SubscriptionNode{
		SubscriptionID: revoked.ID, NodeID: node.ID, Status: SyncStatusPendingRemove,
	}))

	// updated_at is owned by the ORM auto-timestamp; freeze it explicitly so
	// the age assertion is deterministic.
	stuckSince := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
	require.NoError(t, svc.db.WithContext(ctx).Model(&SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", revoked.ID, node.ID).
		Update("updated_at", stuckSince).Error)

	product := &Product{PlanID: plan.ID, Name: "overview-product", DurationDays: 30, PriceCents: 100}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&Order{
		SubscriptionID: active.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100,
	}).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(&Broadcast{
		Name: "overview-broadcast", Status: string(BroadcastStatusScheduled),
	}).Error)

	snapshot, err := svc.GetOverviewSnapshot(ctx)
	require.NoError(t, err)

	assert.Equal(t, int64(2), snapshot.SubscriptionsTotal())
	assert.Equal(t, int64(1), snapshot.SubscriptionsByStatus["active"])
	assert.Equal(t, int64(1), snapshot.SubscriptionsByStatus["revoked"])

	assert.Equal(t, int64(1), snapshot.NodesTotal)
	assert.Equal(t, int64(1), snapshot.BindingsByStatus["active"])
	assert.Equal(t, int64(1), snapshot.BindingsByStatus["pending_remove"])
	assert.Equal(t, int64(1), snapshot.PendingBindingsTotal())

	require.NotNil(t, snapshot.OldestPendingBinding)
	assert.True(t, snapshot.OldestPendingBinding.Equal(stuckSince),
		"oldest pending binding must be the frozen timestamp, got %s", snapshot.OldestPendingBinding)

	require.NotNil(t, snapshot.LastSubRequest)
	assert.True(t, snapshot.LastSubRequest.Equal(lastRequest))

	assert.Equal(t, int64(1), snapshot.OrdersByStatus["paid"])
	assert.Equal(t, int64(1), snapshot.BroadcastsByStatus[string(BroadcastStatusScheduled)])
	assert.NotEmpty(t, snapshot.DatabasePath)
	assert.Positive(t, snapshot.DatabaseSizeBytes)
}

// TestGetOverviewSnapshot_ExpiringBuckets проверяет бакеты истечения: активные
// подписки в окне 30 дней раскладываются по дням (нулевые дни включены),
// подписка за окном не считается.
func TestGetOverviewSnapshot_ExpiringBuckets(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "expiring-plan", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	// ExpiresAt ставим в середину оставшейся части суток (и того же смещения
	// на день+6), чтобы бакет не зависел от близости теста к полуночи UTC.
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	mid := today.AddDate(0, 0, 1).Sub(now) / 2
	in0 := now.Add(mid)
	in6 := today.AddDate(0, 0, 6).Add(mid)
	in40 := today.AddDate(0, 0, 40).Add(mid)

	create := func(tg int64, client, subID string, expires time.Time) {
		t.Helper()
		e := expires.UTC()
		require.NoError(t, svc.CreateSubscription(ctx, &Subscription{
			TelegramID: tg, ClientID: client, SubscriptionID: subID,
			Status: string(SubscriptionStatusActive), ExpiresAt: &e, PlanID: plan.ID,
		}, ""))
	}
	create(700201, "exp-c1", "exp-s1", in0)
	create(700202, "exp-c2", "exp-s2", in6)
	// Вне окна 30 дней и истёкшая — не считаются.
	create(700203, "exp-c3", "exp-s3", in40)
	expired := in0.Add(-48 * time.Hour)
	require.NoError(t, svc.CreateSubscription(ctx, &Subscription{
		TelegramID: 700204, ClientID: "exp-c4", SubscriptionID: "exp-s4",
		Status: string(SubscriptionStatusActive), ExpiresAt: &expired, PlanID: plan.ID,
	}, ""))

	snapshot, err := svc.GetOverviewSnapshot(ctx)
	require.NoError(t, err)

	assert.Equal(t, int64(2), snapshot.Expiring.Total)
	require.Len(t, snapshot.Expiring.Days, 31)
	assert.Equal(t, today.Format("2006-01-02"), snapshot.Expiring.Days[0].Date)
	assert.Equal(t, int64(1), snapshot.Expiring.Days[0].Count)
	assert.Equal(t, int64(1), snapshot.Expiring.Days[6].Count)
	assert.Equal(t, int64(0), snapshot.Expiring.Days[7].Count)
}

// TestGetOverviewSnapshot_EmptyQueue checks the zero case: no pending bindings
// means no timestamp, not a zero-valued time that the panel would render as
// "1970".
func TestGetOverviewSnapshot_EmptyQueue(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	snapshot, err := svc.GetOverviewSnapshot(context.Background())
	require.NoError(t, err)

	assert.Zero(t, snapshot.PendingBindingsTotal())
	assert.Nil(t, snapshot.OldestPendingBinding)
	assert.Nil(t, snapshot.LastSubRequest)
}
