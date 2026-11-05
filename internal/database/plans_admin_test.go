package database

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListPlanOverviews_CountsProductsAndSubscriptions(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "paid-30d", IsActive: true, DevicesLimit: 2, TrafficLimit: 100 << 30}
	require.NoError(t, svc.CreatePlan(ctx, plan))

	require.NoError(t, svc.db.Create(&Product{
		PlanID: plan.ID, Name: "30 дней", DurationDays: 30, PriceCents: 10000, Currency: "RUB", IsActive: true,
	}).Error)
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 555001, ClientID: "c-555001", SubscriptionID: "s-555001",
		Status: string(SubscriptionStatusActive), PlanID: plan.ID,
	}).Error)

	rows, err := svc.ListPlanOverviews(ctx)
	require.NoError(t, err)

	byName := make(map[string]PlanOverview, len(rows))
	for _, row := range rows {
		byName[row.Name] = row
	}

	got, ok := byName["paid-30d"]
	require.True(t, ok, "created plan must be listed")
	assert.Equal(t, int64(1), got.ProductsCount)
	assert.Equal(t, int64(1), got.SubscriptionsCount)
	assert.Equal(t, 2, got.DevicesLimit)
	assert.Equal(t, int64(100<<30), got.TrafficLimit)

	free, ok := byName[FreePlanName]
	require.True(t, ok, "seeded free plan must be listed")
	assert.Equal(t, int64(0), free.ProductsCount)
}

func TestCreatePlan_RejectsDuplicateName(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	first := &Plan{Name: "dup-plan", IsActive: true, DevicesLimit: 1}
	require.NoError(t, svc.CreatePlan(ctx, first))

	second := &Plan{Name: "dup-plan", IsActive: true, DevicesLimit: 1}
	err := svc.CreatePlan(ctx, second)

	require.ErrorIs(t, err, ErrPlanNameTaken)
}

func TestCreatePlan_TrimsNameAndValidatesFields(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "  spaced-plan  ", IsActive: true, DevicesLimit: 3, TrafficLimit: 1 << 30}
	require.NoError(t, svc.CreatePlan(ctx, plan))
	assert.Equal(t, "spaced-plan", plan.Name)

	empty := &Plan{Name: "   ", DevicesLimit: 1}
	require.ErrorIs(t, svc.CreatePlan(ctx, empty), ErrPlanInvalid)

	negative := &Plan{Name: "negative-traffic", DevicesLimit: 1, TrafficLimit: -1}
	require.ErrorIs(t, svc.CreatePlan(ctx, negative), ErrPlanInvalid)
}

func TestUpdatePlan_GuardsDefaultPlans(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	trial, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	renamed := *trial
	renamed.Name = "renamed-trial"
	require.ErrorIs(t, svc.UpdatePlan(ctx, &renamed), ErrPlanProtected)

	deactivated := *trial
	deactivated.IsActive = false
	require.ErrorIs(t, svc.UpdatePlan(ctx, &deactivated), ErrPlanProtected)

	// The free plan is resolved by name regardless of is_active, so deactivation
	// is allowed; renaming is not.
	free, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	freeDeactivated := *free
	freeDeactivated.IsActive = false
	require.NoError(t, svc.UpdatePlan(ctx, &freeDeactivated))

	freeRenamed := *free
	freeRenamed.Name = "renamed-free"
	require.ErrorIs(t, svc.UpdatePlan(ctx, &freeRenamed), ErrPlanProtected)
}

func TestUpdatePlan_ChangesFields(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "editable", IsActive: true, DevicesLimit: 1, TrafficLimit: 1 << 30}
	require.NoError(t, svc.CreatePlan(ctx, plan))

	plan.Name = "edited"
	plan.DevicesLimit = 4
	plan.TrafficLimit = 5 << 30
	require.NoError(t, svc.UpdatePlan(ctx, plan))

	got, err := svc.GetPlanByID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, "edited", got.Name)
	assert.Equal(t, 4, got.DevicesLimit)
	assert.Equal(t, int64(5<<30), got.TrafficLimit)
	require.ErrorIs(t, svc.UpdatePlan(ctx, &Plan{ID: 99999, Name: "ghost"}), ErrPlanNotFound)
}

func TestDeletePlan_GuardsReferences(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	trial, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)
	require.ErrorIs(t, svc.DeletePlan(ctx, trial.ID), ErrPlanProtected)

	withProduct := &Plan{Name: "with-product", IsActive: true, DevicesLimit: 1}
	require.NoError(t, svc.CreatePlan(ctx, withProduct))
	require.NoError(t, svc.db.Create(&Product{
		PlanID: withProduct.ID, Name: "p", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true,
	}).Error)
	require.ErrorIs(t, svc.DeletePlan(ctx, withProduct.ID), ErrPlanInUse)

	withSubscription := &Plan{Name: "with-sub", IsActive: true, DevicesLimit: 1}
	require.NoError(t, svc.CreatePlan(ctx, withSubscription))
	require.NoError(t, svc.db.Create(&Subscription{
		TelegramID: 555002, ClientID: "c-555002", SubscriptionID: "s-555002",
		Status: string(SubscriptionStatusActive), PlanID: withSubscription.ID,
	}).Error)
	require.ErrorIs(t, svc.DeletePlan(ctx, withSubscription.ID), ErrPlanInUse)

	free := &Plan{Name: "deletable", IsActive: true, DevicesLimit: 1}
	require.NoError(t, svc.CreatePlan(ctx, free))
	require.NoError(t, svc.DeletePlan(ctx, free.ID))

	_, err = svc.GetPlanByID(ctx, free.ID)
	require.ErrorIs(t, err, ErrPlanNotFound)

	require.ErrorIs(t, svc.DeletePlan(ctx, 99999), ErrPlanNotFound)
}

// TestPlanAuditEvents covers ADR-0002 P20 for the plan catalogue: every
// successful mutation writes an admin-attributed event (actor_id "0" = panel),
// and a rejected mutation writes nothing — "audited" must not mean "attempted".
// History outlives the row: the deleted event stays readable after the plan is
// physically gone (audit_log has no FK, migration 039).
func TestPlanAuditEvents(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "audited-plan", IsActive: true, DevicesLimit: 2, TrafficLimit: 1 << 30}
	require.NoError(t, svc.CreatePlan(ctx, plan))

	created, err := svc.GetAuditLogByEvent(ctx, AuditEventPlanCreated, 10)
	require.NoError(t, err)
	require.Len(t, created, 1)

	event := created[0]
	assert.Equal(t, EntityPlan, event.EntityType)
	require.NotNil(t, event.EntityID)
	assert.Equal(t, fmt.Sprintf("%d", plan.ID), *event.EntityID)
	assert.Equal(t, ActorAdmin, event.ActorType)
	require.NotNil(t, event.ActorID)
	assert.Equal(t, "0", *event.ActorID)
	assert.Nil(t, event.OldState)

	var newState map[string]any
	require.NotNil(t, event.NewState)
	require.NoError(t, json.Unmarshal([]byte(*event.NewState), &newState))
	assert.Equal(t, "audited-plan", newState["name"])
	assert.InDelta(t, 2, newState["devices_limit"], 0)
	assert.Equal(t, true, newState["is_active"])

	plan.Name = "audited-plan-v2"
	plan.DevicesLimit = 5
	require.NoError(t, svc.UpdatePlan(ctx, plan))

	updated, err := svc.GetAuditLogByEvent(ctx, AuditEventPlanUpdated, 10)
	require.NoError(t, err)
	require.Len(t, updated, 1)

	var oldState map[string]any
	require.NotNil(t, updated[0].OldState)
	require.NoError(t, json.Unmarshal([]byte(*updated[0].OldState), &oldState))
	assert.Equal(t, "audited-plan", oldState["name"])
	assert.InDelta(t, 2, oldState["devices_limit"], 0)
	assert.NotContains(t, oldState, "traffic_limit", "unchanged fields stay out of the diff")
	assert.NotContains(t, oldState, "is_active", "unchanged fields stay out of the diff")

	// Rejected mutation: the protected trial plan cannot be renamed, and the
	// failure must not leave a plan_updated event behind.
	trial, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	renamed := *trial
	renamed.Name = "renamed-trial"
	require.ErrorIs(t, svc.UpdatePlan(ctx, &renamed), ErrPlanProtected)

	guardedDelete := &Plan{Name: "guarded-delete", IsActive: true, DevicesLimit: 1}
	require.NoError(t, svc.CreatePlan(ctx, guardedDelete))
	require.NoError(t, svc.db.Create(&Product{
		PlanID: guardedDelete.ID, Name: "p", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true,
	}).Error)
	require.ErrorIs(t, svc.DeletePlan(ctx, guardedDelete.ID), ErrPlanInUse)

	updated, err = svc.GetAuditLogByEvent(ctx, AuditEventPlanUpdated, 10)
	require.NoError(t, err)
	assert.Len(t, updated, 1, "failed update must not write an event")

	deletedEvents, err := svc.GetAuditLogByEvent(ctx, AuditEventPlanDeleted, 10)
	require.NoError(t, err)
	assert.Empty(t, deletedEvents, "failed delete must not write an event")

	// Successful delete: snapshot → nil, and the event survives the row.
	doomed := &Plan{Name: "doomed", IsActive: true, DevicesLimit: 1, TrafficLimit: 2 << 30}
	require.NoError(t, svc.CreatePlan(ctx, doomed))
	require.NoError(t, svc.DeletePlan(ctx, doomed.ID))

	deletedEvents, err = svc.GetAuditLogByEvent(ctx, AuditEventPlanDeleted, 10)
	require.NoError(t, err)
	require.Len(t, deletedEvents, 1)
	assert.Nil(t, deletedEvents[0].NewState)

	oldState = map[string]any{}
	require.NotNil(t, deletedEvents[0].OldState)
	require.NoError(t, json.Unmarshal([]byte(*deletedEvents[0].OldState), &oldState))
	assert.Equal(t, "doomed", oldState["name"])
	assert.InDelta(t, 2<<30, oldState["traffic_limit"], 0)

	_, err = svc.GetPlanByID(ctx, doomed.ID)
	require.ErrorIs(t, err, ErrPlanNotFound)

	stored, err := svc.GetAuditLogByEntity(ctx, EntityPlan, fmt.Sprintf("%d", doomed.ID), 10)
	require.NoError(t, err)
	assert.Len(t, stored, 2, "created + deleted history outlives the row")
}
