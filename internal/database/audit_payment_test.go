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

// TestAuditLog_RepositoryRejectsNil verifies the guard against nil entries.
func TestAuditLog_RepositoryRejectsNil(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	err := svc.InsertAuditLog(context.Background(), nil)
	require.Error(t, err)
}
