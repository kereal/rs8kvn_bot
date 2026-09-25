package database

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestConfirmOrderPaidCAS_WritesAuditEventInTx verifies the payment
// confirmation audit event is committed atomically with the order/subscription
// transition — and rolled back with it when the transaction fails.
func TestConfirmOrderPaidCAS_WritesAuditEventInTx(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 994001, "audit-pay", "audit-pay-client")
	plan := &Plan{Name: "audit-pay-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Premium 30", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 500, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	activatedAt := time.Now().UTC()
	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, activatedAt, activatedAt, sub, product, nil, 500)
	require.NoError(t, err)
	assert.True(t, activated)

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentConfirmed, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, EntityOrder, e.EntityType)
	require.NotNil(t, e.EntityID)
	assert.Equal(t, "1", *e.EntityID) // first order in the fresh DB
	assert.Equal(t, ActorPaymentProvider, e.ActorType)

	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(e.Details), &details))
	assert.Equal(t, float64(order.ID), details["order_id"])
	assert.Equal(t, float64(500), details["amount_cents"])
	assert.Equal(t, "Premium 30", details["product_name"])

	var newState map[string]any
	require.NotNil(t, e.NewState)
	require.NoError(t, json.Unmarshal([]byte(*e.NewState), &newState))
	assert.Equal(t, string(OrderStatusPaid), newState["order_status"])
	assert.Contains(t, newState, "expires_at")
}

// TestConfirmOrderPaidCAS_RollsBackAuditWithTransaction verifies that a failed
// confirmation (applyPlan error) leaves no orphaned audit event: the journal
// reflects only committed facts.
func TestConfirmOrderPaidCAS_RollsBackAuditWithTransaction(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 994002, "audit-rollback", "audit-rollback-client")
	plan := &Plan{Name: "audit-rollback-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Rollback 30", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 500, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	activatedAt := time.Now().UTC()
	_, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, activatedAt, activatedAt, sub, product, func(_ context.Context, _ *gorm.DB, _, _ uint) error {
		return assert.AnError
	}, 500)
	require.Error(t, err)

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentConfirmed, 10)
	require.NoError(t, err)
	assert.Empty(t, events, "failed transaction must not leave an audit event")
}

// TestCancelPaidOrderAndDowngradeCAS_WritesChargebackAudit verifies the
// chargeback transaction records both the order cancellation and the
// downgraded-to-free decision.
func TestCancelPaidOrderAndDowngradeCAS_WritesChargebackAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	sub := createTestSubscription(t, svc, 994003, "audit-chargeback", "audit-chargeback-client")
	plan := &Plan{Name: "audit-chargeback-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "CB 30", DurationDays: 30, PriceCents: 900, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	// Paid order covering the subscription.
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 900, Currency: "RUB", PaymentProvider: "platega", PaidAt: ptrTime(time.Now().UTC()), CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC()
	providerPaymentID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440307")
	require.NoError(t, svc.db.WithContext(ctx).Model(order).Update("provider_payment_id", providerPaymentID.String()).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, "platega", providerPaymentID, now, freePlan.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Transitioned)
	assert.True(t, result.WasPaid)
	assert.True(t, result.Downgraded)

	// Cancellation event.
	canceled, err := svc.GetAuditLogByEvent(ctx, AuditEventOrderCanceled, 10)
	require.NoError(t, err)
	require.Len(t, canceled, 1)
	assert.Equal(t, ActorPaymentProvider, canceled[0].ActorType)

	// Chargeback event with downgraded=true.
	chargebacks, err := svc.GetAuditLogByEvent(ctx, AuditEventChargebackReceived, 10)
	require.NoError(t, err)
	require.Len(t, chargebacks, 1)

	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(chargebacks[0].Details), &details))
	assert.Equal(t, true, details["downgraded"])
	assert.Equal(t, float64(900), details["amount_cents"])
}

// TestConfirmOrderPaidCAS_WritesSubscriptionRenewed verifies that payment
// confirmation also records subscription_renewed in the subscription's own
// timeline (payment_confirmed addresses the order; the subscription timeline
// must show the extension too).
func TestConfirmOrderPaidCAS_WritesSubscriptionRenewed(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 994011, "audit-renewed", "audit-renewed-client")
	plan := &Plan{Name: "audit-renewed-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Renew 30", DurationDays: 30, PriceCents: 700, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 700, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	activatedAt := time.Now().UTC()
	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, activatedAt, activatedAt, sub, product, nil, 700)
	require.NoError(t, err)
	assert.True(t, activated)

	events, err := svc.GetAuditLogByEntity(ctx, EntitySubscription, formatSubID(sub), 10)
	require.NoError(t, err)

	var renewed bool
	for _, e := range events {
		if e.Event == AuditEventSubscriptionRenewed {
			renewed = true
			assert.Equal(t, ActorPaymentProvider, e.ActorType)
			require.NotNil(t, e.NewState)

			var newState map[string]any
			require.NoError(t, json.Unmarshal([]byte(*e.NewState), &newState))
			assert.Contains(t, newState, "expires_at")
		}
	}
	assert.True(t, renewed, "payment confirmation must record subscription_renewed in the subscription timeline")
}

// TestCancelOrderCAS_WritesOrderCanceledAudit verifies the non-chargeback
// cancellation path (CANCELED callback on a pending order) records an event.
// The chargeback path has its own in-tx audit covered separately.
func TestCancelOrderCAS_WritesOrderCanceledAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 994012, "audit-cancel", "audit-cancel-client")
	plan := &Plan{Name: "audit-cancel-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Cancel 30", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 500, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)
	require.NoError(t, svc.db.WithContext(ctx).Model(order).Update("provider_payment_id", "550e8400-e29b-41d4-a716-446655440312").Error)

	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440312")
	transitioned, err := svc.CancelOrderCAS(ctx, "platega", providerID, []OrderStatus{OrderStatusPending})
	require.NoError(t, err)
	assert.True(t, transitioned)

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventOrderCanceled, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, EntityOrder, events[0].EntityType)
	assert.Equal(t, ActorPaymentProvider, events[0].ActorType)
}

// TestFindPendingPaymentOrder_ExpiredOrderWritesAudit verifies that pending
// payment intents expiring by deadline are terminalized with an order_expired
// audit event.
func TestFindPendingPaymentOrder_ExpiredOrderWritesAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 994013, "audit-expired-order", "audit-expired-order-client")
	plan := &Plan{Name: "audit-expired-order-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Expire 30", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	past := time.Now().UTC().Add(-1 * time.Hour)
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 500, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC(), PaymentExpiresAt: &past}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	got, err := svc.FindPendingPaymentOrder(ctx, sub.ID, 1, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, OrderStatusExpired, got.Status)

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventOrderExpired, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, ActorWorker, events[0].ActorType)
	require.NotNil(t, events[0].NewState)

	var newState map[string]any
	require.NoError(t, json.Unmarshal([]byte(*events[0].NewState), &newState))
	assert.Equal(t, string(OrderStatusExpired), newState["status"])
}

// TestMarkPaymentCreationUncertain_WritesAudit verifies the signal event for
// "provider request outcome unknown, manual reconciliation required".
func TestMarkPaymentCreationUncertain_WritesAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 994014, "audit-uncertain", "audit-uncertain-client")
	plan := &Plan{Name: "audit-uncertain-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Uncertain 30", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 500, Currency: "RUB", PaymentProvider: "platega", CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	claimed, err := svc.MarkPaymentCreationUncertain(ctx, order.ID, true)
	require.NoError(t, err)
	assert.True(t, claimed)

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentCreationUncert, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, ActorSystem, events[0].ActorType)
}

// TestCleanupExpiredTrials_WritesTrialDeletedAudit verifies the second physical
// deletion path (admin /del is the first) is journaled per removed trial.
func TestCleanupExpiredTrials_WritesTrialDeletedAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// CleanupExpiredTrials резолвит план по имени TrialPlanName ("trial") и
	// удаляет по telegram_id < 0 + created_at < cutoff. Seed уже создаёт план
	// "trial" — используем его ID вместо создания дубля.
	trialPlan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	trial := &Subscription{TelegramID: -99001, SubscriptionID: "audit-trial-del", ClientID: "audit-trial-del-client", PlanID: trialPlan.ID, Status: string(SubscriptionStatusExpired), ExpiresAt: &old, CreatedAt: old}
	require.NoError(t, svc.db.WithContext(ctx).Create(trial).Error)

	subs, err := svc.CleanupExpiredTrials(ctx, 24)
	require.NoError(t, err)
	require.Len(t, subs, 1)

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventTrialDeleted, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, ActorWorker, events[0].ActorType)
	require.NotNil(t, events[0].OldState, "trial_deleted must keep a snapshot of the removed row")
}

// TestAuditLog_RepositoryRejectsNil verifies the guard against nil entries.
func TestAuditLog_RepositoryRejectsNil(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	err := svc.InsertAuditLog(context.Background(), nil)
	require.Error(t, err)
}
