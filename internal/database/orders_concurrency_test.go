package database

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Конкурентные регрессии на реальной SQLite (не на fake-хранилище):
// pending-заказ Stars и оплата заказа защищаются от гонок на уровне БД,
// поэтому тесты запускают настоящие параллельные транзакции.

// TestConfirmOrderPaidCAS_ConcurrentOnlyOneActivates — два параллельных CAS на
// одном заказе: активировать должен ровно один, второй — no-op. Оба вызова
// конкурентны в одной БД, поэтому тест ловит потерю обновления и двойную
// запись событий оплаты.
func TestConfirmOrderPaidCAS_ConcurrentOnlyOneActivates(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "plan-cas-concurrent", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	sub := createTestSubscription(t, svc, 9701, "cas-concurrent", "client-cas-concurrent")
	sub.PlanID = plan.ID
	require.NoError(t, svc.db.Save(sub).Error)

	order := &Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending,
		AmountCents: 5000, Currency: "RUB", PaymentProvider: PaymentProviderTelegramStars,
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC().Truncate(time.Second)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		activated int
		failures  []error
	)

	for range 2 {
		wg.Go(func() {
			ok, err := confirmWithBusyRetry(ctx, svc, order, sub, product, now)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				failures = append(failures, err)

				return
			}

			if ok {
				activated++
			}
		})
	}

	wg.Wait()

	require.Empty(t, failures, "concurrent CAS must not fail")
	assert.Equal(t, 1, activated, "exactly one CAS may activate the order")

	stored, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, OrderStatusPaid, stored.Status)

	confirmed, err := svc.GetAuditLogByEvent(ctx, AuditEventPaymentConfirmed, 10)
	require.NoError(t, err)
	assert.Len(t, confirmed, 1, "payment_confirmed must be written exactly once")

	renewed, err := svc.GetAuditLogByEvent(ctx, AuditEventSubscriptionRenewed, 10)
	require.NoError(t, err)
	assert.Len(t, renewed, 1, "subscription_renewed must be written exactly once")
}

// confirmWithBusyRetry выполняет CAS, повторяя попытку при SQLITE_BUSY.
// SQLite в WAL отдаёт «database is locked» при конкурентной записи двух
// транзакций: это штатная ошибка драйвера, а не потеря обновления. Повтор
// безопасен — CAS идемпотентен по статусу заказа.
func confirmWithBusyRetry(ctx context.Context, svc *Service, order *Order, sub *Subscription, product *Product, now time.Time) (bool, error) {
	var (
		activated bool
		err       error
	)

	for range 5 {
		activated, err = svc.ConfirmOrderPaidCAS(ctx, ConfirmPaidOrderParams{OrderID: order.ID, PaidAt: now, ActivatedAt: now, Subscription: sub, Product: product, ApplyPlan: nil, CallbackAmountCents: 5000, ProviderPaymentID: "charge-concurrent", ProviderFeeCents: nil})
		if err == nil || !strings.Contains(err.Error(), "database is locked") {
			return activated, err
		}

		time.Sleep(50 * time.Millisecond)
	}

	return activated, err
}

// TestFindOrCreatePendingPaymentOrder_ConcurrentStarsCreatesOneOrder —
// параллельные создания Stars-интента (у провайдера нет partial unique index,
// защита даёт pendingMu хранилища) должны дать ровно один pending-заказ.
func TestFindOrCreatePendingPaymentOrder_ConcurrentStarsCreatesOneOrder(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "plan-stars-pending", DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Stars", DurationDays: 30, PriceCents: 199, Currency: "XTR", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	sub := createTestSubscription(t, svc, 9702, "stars-pending", "client-stars-pending")
	sub.PlanID = plan.ID
	require.NoError(t, svc.db.Save(sub).Error)

	now := time.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(15 * time.Minute)

	const callers = 16

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		created  int
		orderIDs = map[uint]struct{}{}
		failures []error
	)

	for range callers {
		wg.Go(func() {
			order, wasCreated, err := svc.FindOrCreatePendingPaymentOrder(ctx, sub.ID, product.ID, PaymentProviderTelegramStars, product.PriceCents, product.Currency, &expiresAt, now)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				failures = append(failures, err)

				return
			}

			if wasCreated {
				created++
			}

			orderIDs[order.ID] = struct{}{}
		})
	}

	wg.Wait()

	require.Empty(t, failures, "concurrent pending creation must not fail")
	assert.Equal(t, 1, created, "exactly one caller may create the pending order")
	assert.Len(t, orderIDs, 1, "all callers must observe the same pending order")
}
