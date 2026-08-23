package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests lock the audience contract: trials never receive broadcasts and
// paid/free targeting follows payment state rather than plan name alone.
func TestBroadcastAudienceFiltersExcludeTrialsAndDefinePaidByPayment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	freePlan := testFreePlanID(t, svc)
	trialPlan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	paidAmount := int64(990)
	subs := []Subscription{
		{TelegramID: 720001, Username: "active-free", ClientID: "filter-client-1", SubscriptionID: "filter-sub-1", PlanID: freePlan, Status: string(SubscriptionStatusActive)},
		{TelegramID: 720002, Username: "revoked-paid", ClientID: "filter-client-2", SubscriptionID: "filter-sub-2", PlanID: freePlan, Status: string(SubscriptionStatusRevoked), PricePaidCents: paidAmount},
		{TelegramID: 720003, Username: "bound-trial", ClientID: "filter-client-3", SubscriptionID: "filter-sub-3", PlanID: trialPlan.ID, Status: string(SubscriptionStatusActive)},
	}
	for i := range subs {
		require.NoError(t, svc.db.Create(&subs[i]).Error)
	}

	allCount, err := svc.GetFilteredTelegramIDCount(ctx, BroadcastFilter{SubscriptionStatus: "all"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), allCount)

	paidCount, err := svc.GetFilteredTelegramIDCount(ctx, BroadcastFilter{SubscriptionStatus: "all", PlanType: "paid"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), paidCount)

	freeCount, err := svc.GetFilteredTelegramIDCount(ctx, BroadcastFilter{SubscriptionStatus: "all", PlanType: "free"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), freeCount)
}
