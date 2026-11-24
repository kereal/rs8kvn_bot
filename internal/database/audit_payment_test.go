package database

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestAuditSubscriptionRenewed_UsesProviderActorID verifies that the actor_id
// of the subscription_renewed event carries the passed payment provider,
// falling back to Platega for an empty value.
func TestAuditSubscriptionRenewed_UsesProviderActorID(t *testing.T) {
	t.Parallel()

	sub := &Subscription{ID: 7, Status: string(SubscriptionStatusActive)}

	e := AuditSubscriptionRenewed(nil, sub, nil, PaymentProviderTelegramStars)
	require.NotNil(t, e)
	require.NotNil(t, e.ActorID)
	assert.Equal(t, ActorPaymentProvider, e.ActorType)
	assert.Equal(t, PaymentProviderTelegramStars, *e.ActorID)

	plategaEvent := AuditSubscriptionRenewed(nil, sub, nil, PaymentProviderPlatega)
	require.NotNil(t, plategaEvent.ActorID)
	assert.Equal(t, PaymentProviderPlatega, *plategaEvent.ActorID)

	// Пустой провайдер (legacy-вызов) откатывается к Platega, чтобы actor_id
	// никогда не был пустой строкой.
	legacyEvent := AuditSubscriptionRenewed(nil, sub, nil, "")
	require.NotNil(t, legacyEvent.ActorID)
	assert.Equal(t, PaymentProviderPlatega, *legacyEvent.ActorID)
}

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
	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, activatedAt, activatedAt, sub, product, nil, 500, "", nil)
	require.NoError(t, err)
	assert.True(t, activated)

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentConfirmed, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, EntityOrder, e.EntityType)
	require.NotNil(t, e.EntityID)
	assert.Equal(t, strconv.FormatUint(uint64(order.ID), 10), *e.EntityID)
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
	}, 500, "", nil)
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
	providerPaymentID := "550e8400-e29b-41d4-a716-446655440307"
	require.NoError(t, svc.db.WithContext(ctx).Model(order).Update("provider_payment_id", providerPaymentID).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, "platega", providerPaymentID, now, freePlan.ID, nil, AuditChargebackReceived)
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
	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, activatedAt, activatedAt, sub, product, nil, 700, "", nil)
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
			// new_state must describe the state the payment persisted, not the
			// pre-payment subscription (plan_id used to be the old/free plan).
			assert.Equal(t, float64(plan.ID), newState["plan_id"])
			assert.Equal(t, float64(product.ID), newState["product_id"])
			assert.Equal(t, string(SubscriptionStatusActive), newState["status"])

			var oldState map[string]any
			require.NotNil(t, e.OldState)
			require.NoError(t, json.Unmarshal([]byte(*e.OldState), &oldState))
			assert.Equal(t, float64(testFreePlanID(t, svc)), oldState["plan_id"])
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

	providerID := "550e8400-e29b-41d4-a716-446655440312"
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

	got, err := svc.FindPendingPaymentOrder(ctx, sub.ID, 1, "platega", time.Now().UTC())
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

// TestCancelPaidOrderAndDowngradeCAS_WritesPaymentRefundedAudit verifies that a
// refund passes its own audit-событие через auditFn: payment_refunded вместо
// chargeback_received.
func TestCancelPaidOrderAndDowngradeCAS_WritesPaymentRefundedAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	sub := createTestSubscription(t, svc, 994004, "audit-refund", "audit-refund-client")
	plan := &Plan{Name: "audit-refund-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Refund 30", DurationDays: 30, PriceCents: 900, Currency: "XTR", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 900, Currency: "XTR", PaymentProvider: PaymentProviderTelegramStars, PaidAt: ptrTime(time.Now().UTC()), CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC()
	providerPaymentID := "tg-charge-refund-1"
	require.NoError(t, svc.db.WithContext(ctx).Model(order).Update("provider_payment_id", providerPaymentID).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, PaymentProviderTelegramStars, providerPaymentID, now, freePlan.ID, nil, AuditPaymentRefunded)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Transitioned)
	require.True(t, result.Downgraded)

	// Событие возврата, не чарджбэка.
	refunds, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentRefunded, 10)
	require.NoError(t, err)
	require.Len(t, refunds, 1)
	assert.Equal(t, ActorPaymentProvider, refunds[0].ActorType)

	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(refunds[0].Details), &details))
	assert.Equal(t, true, details["downgraded"])
	assert.Equal(t, float64(900), details["amount_cents"])
	assert.Equal(t, PaymentProviderTelegramStars, details["provider"])

	chargebacks, err := svc.GetAuditLogByEvent(ctx, AuditEventChargebackReceived, 10)
	require.NoError(t, err)
	assert.Empty(t, chargebacks, "refund must not journal the chargeback event")
}

// TestCancelPaidOrderAndDowngradeCAS_NilAuditFnDefaultsToChargeback verifies
// the safety default: a nil auditFn still journals chargeback_received.
func TestCancelPaidOrderAndDowngradeCAS_NilAuditFnDefaultsToChargeback(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	sub := createTestSubscription(t, svc, 994005, "audit-nil-auditfn", "audit-nil-auditfn-client")
	plan := &Plan{Name: "audit-nil-auditfn-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Default audit 30", DurationDays: 30, PriceCents: 800, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 800, Currency: "RUB", PaymentProvider: PaymentProviderPlatega, PaidAt: ptrTime(time.Now().UTC()), CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC()
	providerPaymentID := "550e8400-e29b-41d4-a716-446655440304"
	require.NoError(t, svc.db.WithContext(ctx).Model(order).Update("provider_payment_id", providerPaymentID).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, PaymentProviderPlatega, providerPaymentID, now, freePlan.ID, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Downgraded)

	chargebacks, err := svc.GetAuditLogByEvent(ctx, AuditEventChargebackReceived, 10)
	require.NoError(t, err)
	require.Len(t, chargebacks, 1)
}

// TestCancelPaidOrderAndDowngradeCAS_AuditsPaidCancelWhenSubscriptionNotActive
// verifies the first no-downgrade branch: paid→canceled на неактивной (revoked)
// подписке всё равно журналирует событие чарджбэка в той же транзакции, но
// downgrade подписки не происходит, а details["downgraded"] равен false.
func TestCancelPaidOrderAndDowngradeCAS_AuditsPaidCancelWhenSubscriptionNotActive(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	paidPlan := &Plan{Name: "audit-notactive-plan", IsActive: true}
	require.NoError(t, svc.db.Create(paidPlan).Error)
	sub := &Subscription{TelegramID: 994021, Username: "audit-notactive", ClientID: "audit-notactive-client", SubscriptionID: "audit-notactive-sub", Status: "revoked", PlanID: paidPlan.ID, ExpiresAt: ptrTime(time.Now().UTC().Add(24 * time.Hour))}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{PlanID: paidPlan.ID, Name: "NotActive 30", DurationDays: 30, PriceCents: 700, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	now := time.Now().UTC()
	providerPaymentID := "550e8400-e29b-41d4-a716-446655440321"
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 700, Currency: "RUB", PaymentProvider: PaymentProviderPlatega, ProviderPaymentID: providerPaymentID, ExpiresAt: ptrTime(now.Add(time.Hour)), CreatedAt: now}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, PaymentProviderPlatega, providerPaymentID, now, freePlan.ID, nil, AuditChargebackReceived)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Transitioned)
	require.True(t, result.WasPaid)
	require.False(t, result.Downgraded)

	// Подписка не понижена: статус, план и срок не изменились.
	updated, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "revoked", updated.Status)
	assert.Equal(t, paidPlan.ID, updated.PlanID)
	require.NotNil(t, updated.ExpiresAt)

	// Событие чарджбэка существует и записано ровно один раз.
	chargebacks, err := svc.GetAuditLogByEvent(ctx, AuditEventChargebackReceived, 10)
	require.NoError(t, err)
	require.Len(t, chargebacks, 1)

	e := chargebacks[0]
	assert.Equal(t, ActorPaymentProvider, e.ActorType)
	assert.Equal(t, EntityOrder, e.EntityType)
	require.NotNil(t, e.EntityID)
	assert.Equal(t, strconv.FormatUint(uint64(order.ID), 10), *e.EntityID)

	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(e.Details), &details))
	assert.Equal(t, false, details["downgraded"])
	assert.Equal(t, float64(700), details["amount_cents"])
	assert.Equal(t, PaymentProviderPlatega, details["provider"])
}

// TestCancelPaidOrderAndDowngradeCAS_AuditsPaidCancelWhenOtherPaidOrderActive
// verifies the second no-downgrade branch: активный paid-order сохраняет
// покрытие подписки, но событие возврата всё равно журналируется в той же
// транзакции с downgraded=false, и downgrade подписки не происходит.
func TestCancelPaidOrderAndDowngradeCAS_AuditsPaidCancelWhenOtherPaidOrderActive(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	paidPlan := &Plan{Name: "audit-covered-plan", IsActive: true}
	require.NoError(t, svc.db.Create(paidPlan).Error)
	sub := &Subscription{TelegramID: 994022, Username: "audit-covered", ClientID: "audit-covered-client", SubscriptionID: "audit-covered-sub", Status: "active", PlanID: paidPlan.ID, ExpiresAt: ptrTime(time.Now().UTC().Add(48 * time.Hour))}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{PlanID: paidPlan.ID, Name: "Covered 30", DurationDays: 30, PriceCents: 900, Currency: "XTR", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	now := time.Now().UTC()
	refundID := "tg-charge-audit-covered"
	otherID := "tg-charge-audit-covered-other"
	refundOrder := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 900, Currency: "XTR", PaymentProvider: PaymentProviderTelegramStars, ProviderPaymentID: refundID, ExpiresAt: ptrTime(now.Add(time.Hour)), CreatedAt: now.Add(-2 * time.Hour)}
	otherOrder := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 900, Currency: "XTR", PaymentProvider: PaymentProviderTelegramStars, ProviderPaymentID: otherID, ExpiresAt: ptrTime(now.Add(2 * time.Hour)), CreatedAt: now.Add(-time.Hour)}
	require.NoError(t, svc.db.WithContext(ctx).Create(refundOrder).Error)
	require.NoError(t, svc.db.WithContext(ctx).Create(otherOrder).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, PaymentProviderTelegramStars, refundID, now, freePlan.ID, nil, AuditPaymentRefunded)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Transitioned)
	require.True(t, result.WasPaid)
	require.False(t, result.Downgraded)

	// Подписка не понижена: активна, план и срок сохранены.
	updated, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, string(SubscriptionStatusActive), updated.Status)
	assert.Equal(t, paidPlan.ID, updated.PlanID)
	require.NotNil(t, updated.ExpiresAt)

	// Событие возврата существует и записано ровно один раз.
	refunds, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentRefunded, 10)
	require.NoError(t, err)
	require.Len(t, refunds, 1)

	e := refunds[0]
	assert.Equal(t, ActorPaymentProvider, e.ActorType)
	assert.Equal(t, EntityOrder, e.EntityType)
	require.NotNil(t, e.EntityID)
	assert.Equal(t, strconv.FormatUint(uint64(refundOrder.ID), 10), *e.EntityID)

	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(e.Details), &details))
	assert.Equal(t, false, details["downgraded"])
	assert.Equal(t, float64(900), details["amount_cents"])
	assert.Equal(t, PaymentProviderTelegramStars, details["provider"])

	// Событие чарджбэка при возврате не журналируется.
	chargebacks, err := svc.GetAuditLogByEvent(ctx, AuditEventChargebackReceived, 10)
	require.NoError(t, err)
	assert.Empty(t, chargebacks)
}

// TestCancelPaidOrderAndDowngradeCAS_PendingCancelWritesNoChargebackAudit
// verifies that a pending→canceled transition journals only order_canceled:
// ни chargeback_received, ни payment_refunded для него не создаются.
func TestCancelPaidOrderAndDowngradeCAS_PendingCancelWritesNoChargebackAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	sub := createTestSubscription(t, svc, 994023, "audit-pending", "audit-pending-client")
	plan := &Plan{Name: "audit-pending-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Pending 30", DurationDays: 30, PriceCents: 600, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	now := time.Now().UTC()
	providerPaymentID := "550e8400-e29b-41d4-a716-446655440323"
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 600, Currency: "RUB", PaymentProvider: PaymentProviderPlatega, ProviderPaymentID: providerPaymentID, CreatedAt: now}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	result, err := svc.CancelPaidOrderAndDowngradeCAS(ctx, PaymentProviderPlatega, providerPaymentID, now, freePlan.ID, nil, AuditChargebackReceived)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Transitioned)
	require.False(t, result.WasPaid)
	require.False(t, result.Downgraded)

	chargebacks, err := svc.GetAuditLogByEvent(ctx, AuditEventChargebackReceived, 10)
	require.NoError(t, err)
	assert.Empty(t, chargebacks, "pending→canceled must not journal a chargeback event")

	refunds, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentRefunded, 10)
	require.NoError(t, err)
	assert.Empty(t, refunds, "pending→canceled must not journal a refund event")

	canceled, err := svc.GetAuditLogByEvent(ctx, AuditEventOrderCanceled, 10)
	require.NoError(t, err)
	require.Len(t, canceled, 1)
}

// TestCancelPaidOrderAndDowngradeCAS_RollsBackOnAuditError verifies that an
// auditFn error (nil entry rejected by InsertAuditLogInTx) rolls back the whole
// transaction: заказ остаётся paid, подписка не понижается, аудит-событий нет.
func TestCancelPaidOrderAndDowngradeCAS_RollsBackOnAuditError(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	freePlan, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	paidPlan := &Plan{Name: "audit-rollback-plan", IsActive: true}
	require.NoError(t, svc.db.Create(paidPlan).Error)
	sub := &Subscription{TelegramID: 994024, Username: "audit-rollback-cb", ClientID: "audit-rollback-cb-client", SubscriptionID: "audit-rollback-cb-sub", Status: "active", PlanID: paidPlan.ID, ExpiresAt: ptrTime(time.Now().UTC().Add(24 * time.Hour))}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	product := &Product{PlanID: paidPlan.ID, Name: "Rollback 30", DurationDays: 30, PriceCents: 800, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	now := time.Now().UTC()
	providerPaymentID := "550e8400-e29b-41d4-a716-446655440324"
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 800, Currency: "RUB", PaymentProvider: PaymentProviderPlatega, ProviderPaymentID: providerPaymentID, ExpiresAt: ptrTime(now.Add(time.Hour)), CreatedAt: now}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	_, err = svc.CancelPaidOrderAndDowngradeCAS(ctx, PaymentProviderPlatega, providerPaymentID, now, freePlan.ID, nil, func(*Order, *Subscription, bool) *AuditLog {
		return nil
	})
	require.Error(t, err)

	// Заказ остался paid — отмена откатилась вместе с аудитом.
	var stored Order
	require.NoError(t, svc.db.WithContext(ctx).First(&stored, order.ID).Error)
	assert.Equal(t, OrderStatusPaid, stored.Status)

	// Подписка не понижена.
	updated, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, paidPlan.ID, updated.PlanID)
	require.NotNil(t, updated.ExpiresAt)

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventOrderCanceled, 10)
	require.NoError(t, err)
	assert.Empty(t, events, "failed transaction must not leave an order_canceled audit event")

	chargebacks, err := svc.GetAuditLogByEvent(ctx, AuditEventChargebackReceived, 10)
	require.NoError(t, err)
	assert.Empty(t, chargebacks, "failed transaction must not leave a chargeback audit event")
}

// TestHasPaymentConfirmedByProviderPaymentID verifies the audit-guard lookup used
// by the Stars auto-refund: only charges recorded in a payment_confirmed event
// count as already executed.
func TestHasPaymentConfirmedByProviderPaymentID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	found, err := svc.HasPaymentConfirmedByProviderPaymentID(ctx, PaymentProviderTelegramStars, "charge_absent")
	require.NoError(t, err)
	assert.False(t, found)

	confirmed := &Order{ID: 9, SubscriptionID: 3, AmountCents: 500, Currency: "XTR", PaymentProvider: PaymentProviderTelegramStars, ProviderPaymentID: "charge_confirmed"}
	require.NoError(t, svc.InsertAuditLog(ctx, AuditPaymentConfirmed(confirmed, nil, nil, time.Now())))

	found, err = svc.HasPaymentConfirmedByProviderPaymentID(ctx, PaymentProviderTelegramStars, "charge_confirmed")
	require.NoError(t, err)
	assert.True(t, found, "a charge recorded in payment_confirmed must count as confirmed")

	// Один и тот же provider_payment_id у другого провайдера — другой платёж:
	// совпадение было бы ложным и авто-возврат Stars-платежа был бы пропущен.
	plategaOrder := &Order{ID: 11, SubscriptionID: 3, AmountCents: 500, Currency: "USD", PaymentProvider: PaymentProviderPlatega, ProviderPaymentID: "charge_confirmed"}
	require.NoError(t, svc.InsertAuditLog(ctx, AuditPaymentConfirmed(plategaOrder, nil, nil, time.Now())))

	found, err = svc.HasPaymentConfirmedByProviderPaymentID(ctx, PaymentProviderTelegramStars, "charge_confirmed")
	require.NoError(t, err)
	assert.True(t, found, "the Stars charge must still be found next to a foreign provider")

	// LIKE-паттерн не должен совпадать с чужими charge-идами, включая спецсимволы.
	for _, other := range []string{"charge_con", "charge_confirme", "harge_confirmed", "a_b", "100%match", "under_score"} {
		found, err = svc.HasPaymentConfirmedByProviderPaymentID(ctx, PaymentProviderTelegramStars, other)
		require.NoError(t, err)
		assert.False(t, found, "charge %q must not be treated as confirmed", other)
	}

	// Charge со спецсимволами ищется без ложных совпадений.
	weird := &Order{ID: 10, SubscriptionID: 3, AmountCents: 700, Currency: "XTR", PaymentProvider: PaymentProviderTelegramStars, ProviderPaymentID: "ch_100%id"}
	require.NoError(t, svc.InsertAuditLog(ctx, AuditPaymentConfirmed(weird, nil, nil, time.Now())))

	found, err = svc.HasPaymentConfirmedByProviderPaymentID(ctx, PaymentProviderTelegramStars, "ch_100%id")
	require.NoError(t, err)
	assert.True(t, found, "charge with LIKE metacharacters must still match itself")

	// Тот же id другого провайдера не считается подтверждённым для Stars.
	foreignOnly := &Order{ID: 12, SubscriptionID: 3, AmountCents: 500, Currency: "USD", PaymentProvider: PaymentProviderPlatega, ProviderPaymentID: "charge_foreign_only"}
	require.NoError(t, svc.InsertAuditLog(ctx, AuditPaymentConfirmed(foreignOnly, nil, nil, time.Now())))

	found, err = svc.HasPaymentConfirmedByProviderPaymentID(ctx, PaymentProviderTelegramStars, "charge_foreign_only")
	require.NoError(t, err)
	assert.False(t, found, "a foreign provider charge must not block the Stars auto-refund")
}

// TestAuditPaymentAutoRefunded_Constructor verifies the auto-refund audit event:
// order context when the order exists, system entity otherwise.
func TestAuditPaymentAutoRefunded_Constructor(t *testing.T) {
	t.Parallel()

	order := &Order{ID: 12, SubscriptionID: 4, AmountCents: 900, Currency: "XTR", PaymentProvider: PaymentProviderTelegramStars}
	e := AuditPaymentAutoRefunded(order, 42, 900, "XTR", "charge_dup", "paid_order_new_charge")
	require.NotNil(t, e)
	assert.Equal(t, AuditEventPaymentAutoRefunded, e.Event)
	assert.Equal(t, EntityOrder, e.EntityType)
	assert.Equal(t, ActorPaymentProvider, e.ActorType)
	require.NotNil(t, e.EntityID)
	assert.Equal(t, "12", *e.EntityID)
	assert.Contains(t, e.Details, `"provider_payment_id":"charge_dup"`)
	assert.Contains(t, e.Details, `"reason":"paid_order_new_charge"`)
	assert.Contains(t, e.Details, `"telegram_id":42`)

	systemEvent := AuditPaymentAutoRefunded(nil, 42, 900, "XTR", "charge_dup", "recovery_semantic_mismatch")
	require.NotNil(t, systemEvent)
	assert.Equal(t, EntitySystem, systemEvent.EntityType)
	assert.Nil(t, systemEvent.EntityID)
}

// TestAuditPaymentAutoRefunded_Persisted verifies the event survives a real insert.
func TestAuditPaymentAutoRefunded_Persisted(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	order := &Order{ID: 13, SubscriptionID: 4, AmountCents: 900, Currency: "XTR", PaymentProvider: PaymentProviderTelegramStars}
	require.NoError(t, svc.InsertAuditLog(ctx, AuditPaymentAutoRefunded(order, 42, 900, "XTR", "charge_dup", "cas_loser")))

	events, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentAutoRefunded, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "cas_loser", func() string {
		var details struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal([]byte(events[0].Details), &details)

		return details.Reason
	}())
}
