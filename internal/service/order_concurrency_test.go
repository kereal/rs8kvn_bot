package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfirmPayment_SameOrderParallelCallbacksActivateOnlyOnce verifies that two
// concurrent ConfirmPayment calls for the same provider_payment_id serialize on
// the per-order lock: only the first pending→paid CAS mutates the order, every
// later caller observes Activated=false (idempotent duplicate).
func TestConfirmPayment_SameOrderParallelCallbacksActivateOnlyOnce(t *testing.T) {
	t.Parallel()

	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440140")
	sub := &database.Subscription{ID: 41, TelegramID: 71, PlanID: 2}
	product := &database.Product{ID: 51, PlanID: 2, Name: "Premium", DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}
	order := &database.Order{ID: 31, SubscriptionID: 41, ProductID: 51, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}

	var firstStartedOnce sync.Once
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var casCalls atomic.Int32

	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return order, nil
		},
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) { return product, nil },
		GetByIDFunc:        func(context.Context, uint) (*database.Subscription, error) { return sub, nil },
		ConfirmOrderPaidCASFunc: func(_ context.Context, orderID uint, _, _ time.Time, _ *database.Subscription, _ *database.Product, _ database.ApplyPlanInTxFn) (bool, error) {
			count := casCalls.Add(1)
			if count == 1 {
				firstStartedOnce.Do(func() { close(firstStarted) })
				// Hold the lock holder in CAS until the test signals release. Any
				// second caller queued on the per-order lock will block on lock
				// acquisition long before reaching this point.
				<-release
				order.Status = database.OrderStatusPaid
				return true, nil
			}
			return false, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{})

	const workers = 4
	type result struct {
		activated bool
		err       error
	}
	results := make([]result, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			confirmation, err := o.ConfirmPayment(context.Background(), providerID, json.Number("23.00"), "RUB")
			if confirmation != nil {
				results[i] = result{activated: confirmation.Activated, err: err}
			} else {
				results[i] = result{err: err}
			}
		}()
	}
	close(start)

	// Wait for the first goroutine to enter CAS, then release it. Other
	// goroutines must queue on the per-order lock and only reach CAS after
	// the first one finishes.
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first ConfirmPayment did not reach CAS")
	}

	close(release)
	wg.Wait()

	activated := 0
	for _, r := range results {
		require.NoError(t, r.err)
		if r.activated {
			activated++
		}
	}
	assert.Equal(t, 1, activated, "exactly one ConfirmPayment caller may transition pending→paid")
	// After the first goroutine activates the order, every later caller should
	// take the paid/duplicate early-return path and must NOT reach the CAS.
	assert.Equal(t, int32(1), casCalls.Load(), "duplicates must not re-enter ConfirmOrderPaidCAS (early-return on paid status)")
}

// TestConfirmPayment_DifferentOrdersRunInParallel is the inverse: two concurrent
// ConfirmPayment calls for DIFFERENT provider_payment_ids must NOT serialize on
// the per-order lock. If the lock were process-global, one goroutine would still
// be in CAS while the second waited; we make both block in CAS so the test can
// prove they reach it concurrently.
func TestConfirmPayment_DifferentOrdersRunInParallel(t *testing.T) {
	t.Parallel()

	firstID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440141")
	secondID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440142")

	sub1 := &database.Subscription{ID: 51, TelegramID: 81, PlanID: 3}
	sub2 := &database.Subscription{ID: 52, TelegramID: 82, PlanID: 3}
	order1 := &database.Order{ID: 41, SubscriptionID: 51, ProductID: 61, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: firstID.String()}
	order2 := &database.Order{ID: 42, SubscriptionID: 52, ProductID: 62, Status: database.OrderStatusPending, AmountCents: 2400, Currency: "RUB", ProviderPaymentID: secondID.String()}
	productFor := func(id uint) *database.Product {
		return &database.Product{ID: id, PlanID: 3, Name: "Premium", DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}
	}

	orders := map[uuid.UUID]*database.Order{firstID: order1, secondID: order2}
	// confirmPayment calls GetByID(ctx, order.SubscriptionID) — key by the
	// subscription primary key, not the order ID.
	subsBySubscriptionID := map[uint]*database.Subscription{sub1.ID: sub1, sub2.ID: sub2}

	started1 := make(chan struct{})
	started2 := make(chan struct{})
	release1 := make(chan struct{})
	release2 := make(chan struct{})
	var startOnce1, startOnce2 sync.Once
	var casCalls atomic.Int32

	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(_ context.Context, _ string, id uuid.UUID) (*database.Order, error) {
			o, ok := orders[id]
			if !ok {
				return nil, database.ErrOrderNotFound
			}
			return o, nil
		},
		GetProductByIDFunc: func(_ context.Context, id uint) (*database.Product, error) { return productFor(id), nil },
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			sub, ok := subsBySubscriptionID[id]
			if !ok {
				return nil, database.ErrSubscriptionNotFound
			}
			return sub, nil
		},
		ConfirmOrderPaidCASFunc: func(_ context.Context, orderID uint, _, _ time.Time, _ *database.Subscription, _ *database.Product, _ database.ApplyPlanInTxFn) (bool, error) {
			casCalls.Add(1)
			if orderID == order1.ID {
				startOnce1.Do(func() { close(started1) })
				<-release1
				order1.Status = database.OrderStatusPaid
				return true, nil
			}
			if orderID == order2.ID {
				startOnce2.Do(func() { close(started2) })
				<-release2
				order2.Status = database.OrderStatusPaid
				return true, nil
			}
			return false, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{})

	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	go func() {
		defer wg.Done()
		<-start
		_, _ = o.ConfirmPayment(context.Background(), firstID, json.Number("23.00"), "RUB")
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _ = o.ConfirmPayment(context.Background(), secondID, json.Number("24.00"), "RUB")
	}()
	close(start)

	// Both goroutines must enter ConfirmOrderPaidCAS within 1 second; on a global
	// lock the second would wait for the first to release fully.
	select {
	case <-started1:
	case <-time.After(time.Second):
		t.Fatal("first ConfirmPayment did not reach CAS")
	}
	select {
	case <-started2:
	case <-time.After(time.Second):
		t.Fatal("second ConfirmPayment did not reach CAS concurrently with the first — per-order lock is not working")
	}

	close(release1)
	close(release2)
	wg.Wait()

	assert.Equal(t, int32(2), casCalls.Load(), "every distinct order must reach its own CAS independently")
}

// TestCancelPaymentByProvider_DifferentOrdersRunInParallel is the analogue for
// the cancellation path. Two CHARGEBACKED callbacks for different order IDs must
// not block each other on the payment lock.
func TestCancelPaymentByProvider_DifferentOrdersRunInParallel(t *testing.T) {
	t.Parallel()

	firstID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440143")
	secondID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440144")
	order1 := &database.Order{ID: 51, SubscriptionID: 61, ProductID: 71, Status: database.OrderStatusPaid, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: firstID.String()}
	order2 := &database.Order{ID: 52, SubscriptionID: 62, ProductID: 72, Status: database.OrderStatusPaid, AmountCents: 2400, Currency: "RUB", ProviderPaymentID: secondID.String()}
	orders := map[uuid.UUID]*database.Order{firstID: order1, secondID: order2}

	started1 := make(chan struct{})
	started2 := make(chan struct{})
	release1 := make(chan struct{})
	release2 := make(chan struct{})
	var startOnce1, startOnce2 sync.Once

	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(_ context.Context, _ string, id uuid.UUID) (*database.Order, error) {
			o, ok := orders[id]
			if !ok {
				return nil, database.ErrOrderNotFound
			}
			return o, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(_ context.Context, _ string, id uuid.UUID, _ time.Time, _ uint, _ database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			o := orders[id]
			if id == firstID {
				startOnce1.Do(func() { close(started1) })
				<-release1
				o.Status = database.OrderStatusCanceled
				return &database.ChargebackResult{Order: o, WasPaid: true, Transitioned: true, Downgraded: true}, nil
			}
			if id == secondID {
				startOnce2.Do(func() { close(started2) })
				<-release2
				o.Status = database.OrderStatusCanceled
				return &database.ChargebackResult{Order: o, WasPaid: true, Transitioned: true, Downgraded: true}, nil
			}
			t.Fatalf("unexpected provider id %s", id.String())
			return nil, nil
		},
	}
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", nil)

	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	go func() {
		defer wg.Done()
		<-start
		_, _, _ = o.CancelPaymentByProvider(context.Background(), firstID, "CHARGEBACKED", json.Number("23.00"), "RUB")
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _, _ = o.CancelPaymentByProvider(context.Background(), secondID, "CHARGEBACKED", json.Number("24.00"), "RUB")
	}()
	close(start)

	select {
	case <-started1:
	case <-time.After(time.Second):
		t.Fatal("first CancelPaymentByProvider did not reach CAS")
	}
	select {
	case <-started2:
	case <-time.After(time.Second):
		t.Fatal("second CancelPaymentByProvider did not run concurrently — per-order lock regression?")
	}

	close(release1)
	close(release2)
	wg.Wait()
}

// countingPaymentProvider records how many times CreateTransaction was called
// and returns a stable, well-formed response on every call so concurrent
// RequestPayment goroutines can each finish the post-create half of the flow
// (SavePaymentDetails, etc.) when the test wants them to.
type countingPaymentProvider struct {
	calls atomic.Int32
}

func (p *countingPaymentProvider) CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error) {
	p.calls.Add(1)
	return &platega.CreateTransactionResponse{
		TransactionID: "550e8400-e29b-41d4-a716-446655440199",
		URL:           "https://pay.example",
		ExpiresIn:     "00:15:00",
	}, nil
}

// TestRequestPayment_ConcurrentCallersSingleProviderTransaction verifies the
// per-(subscription,product) claim race: even when many goroutines race for the
// same pending intent, only one goroutine reaches the PaymentProvider. Each
// subsequent caller must observe ErrPaymentAlreadyInProgress from the claim
// race resolution path. The invariant matches the production DB partial unique
// index alongside the in-memory MarkPaymentCreationUncertain compare-and-swap
// guard.
func TestRequestPayment_ConcurrentCallersSingleProviderTransaction(t *testing.T) {
	t.Parallel()

	product := &database.Product{ID: 65, PlanID: 2, Name: "Premium", PriceCents: 2300, Currency: "RUB", IsActive: true}
	sub := &database.Subscription{ID: 75, TelegramID: 55}

	var claimCount atomic.Int32
	// Isolate each goroutine's view of the pending intent: FindPendingPaymentOrder
	// returns a fresh *Order each call, so no concurrent reader sees another
	// goroutine's in-place mutations. This mirrors the DB row per read and
	// keeps the race detector quiet.
	provider := &countingPaymentProvider{}

	mock := &testutil.DatabaseService{
		GetProductByIDFunc: func(context.Context, uint) (*database.Product, error) { return product, nil },
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, IsActive: true}, nil
		},
		FindPendingPaymentOrderFunc: func(_ context.Context, subID, productID uint, _ time.Time) (*database.Order, error) {
			return &database.Order{
				ID: 95, SubscriptionID: subID, ProductID: productID,
				Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB",
			}, nil
		},
		MarkPaymentCreationUncertainFunc: func(_ context.Context, _ uint, uncertain bool) (bool, error) {
			if !uncertain {
				return false, errors.New("race test only exercises the claim path")
			}
			// Atomic CAS: the first goroutine to reach the claim wins the
			// pending intent. Every subsequent caller observes claim=false
			// and reloads — which then hits the race resolution branch in
			// RequestPayment and returns ErrPaymentAlreadyInProgress.
			return claimCount.Add(1) == 1, nil
		},
		SavePaymentDetailsFunc: func(context.Context, uint, uuid.UUID, string, time.Time) error {
			return nil
		},
		// Reload from "DB" after a failed claim. Return the same pending order
		// but with PaymentCreationUncertain=true so the loser sees the
		// flag-conflict branch and returns ErrPaymentAlreadyInProgress, not
		// the "provider_payment_id already set → reuse" branch.
		GetOrderByIDFunc: func(context.Context, uint) (*database.Order, error) {
			return &database.Order{
				ID: 95, SubscriptionID: 75, ProductID: 65,
				Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB",
				PaymentCreationUncertain: true,
			}, nil
		},
	}
	o := NewOrderService(mock, nil, nil, provider, "", &config.Config{})

	const workers = 6
	type outcome struct {
		info *PaymentInfo
		err  error
	}
	results := make([]outcome, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			info, _, err := o.RequestPayment(context.Background(), sub.TelegramID, "user", product)
			results[i] = outcome{info: info, err: err}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), provider.calls.Load(),
		"the per-claim guard must let at most one goroutine reach the PaymentProvider")
	assert.Equal(t, int32(workers), claimCount.Load(),
		"every goroutine must attempt the claim (CAS decides the winner)")

	successes := 0
	losers := 0
	for _, r := range results {
		switch {
		case r.err == nil && r.info != nil:
			successes++
		case errors.Is(r.err, ErrPaymentAlreadyInProgress):
			losers++
		default:
			t.Fatalf("unexpected outcome: info=%+v err=%v", r.info, r.err)
		}
	}
	assert.Equal(t, 1, successes,
		"exactly one goroutine may produce a payment link (the claim winner)")
	assert.Equal(t, workers-1, losers,
		"every other goroutine must observe ErrPaymentAlreadyInProgress from the claim-conflict branch")
}

// TestCancelPaymentByProvider_SameOrderParallelChargebacksTransitionOnce proves
// that two concurrent CHARGEBACKED callbacks for the SAME provider_payment_id
// serialize on the per-order lock. The first transitions paid→canceled and
// triggers the downgrade path; later callbacks observe Transitioned=false
// (order already canceled) and produce no admin alert, no chargeback metric,
// and no post-commit sync.
func TestCancelPaymentByProvider_SameOrderParallelChargebacksTransitionOnce(t *testing.T) {
	t.Parallel()

	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440145")
	sub := &database.Subscription{ID: 81, TelegramID: 91, PlanID: 99, Status: "active", ExpiresAt: testutil.PtrTime(time.Now().Add(24 * time.Hour))}
	order := &database.Order{ID: 71, SubscriptionID: 81, ProductID: 78, Status: database.OrderStatusPaid, AmountCents: 6900, Currency: "RUB", ProviderPaymentID: providerID.String()}

	started := make(chan struct{})
	release := make(chan struct{})
	// Transitions track real paid-order→canceled actions; LockCASCalls tracks
	// how many goroutines reached the repository-level atomic. Both are
	// observable: the lock-serializes CAS-call counts are >= 1, the
	// "real" Post-CAS transition count must be exactly 1 in any execution.
	var lockCASCalls atomic.Int32
	var transitions atomic.Int32

	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return order, nil
		},
		GetPlanByNameFunc: func(context.Context, string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: database.FreePlanName, IsActive: true}, nil
		},
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		CancelPaidOrderAndDowngradeCASFunc: func(_ context.Context, _ string, _ uuid.UUID, _ time.Time, freePlanID uint, _ database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
			n := lockCASCalls.Add(1)
			if n == 1 {
				close(started)
				<-release
				// Realistic post-transition state: order is canceled and the
				// subscription reset to the free plan.
				order.Status = database.OrderStatusCanceled
				sub.PlanID = freePlanID
				sub.ExpiresAt = nil
				sub.ProductID = nil
				sub.PricePaidCents = 0
				sub.Currency = nil
				transitions.Add(1)
				return &database.ChargebackResult{Order: order, WasPaid: true, Transitioned: true, Downgraded: true}, nil
			}
			// Subsequent callers observe that the order was already canceled
			// before this CAS acquired its subscription lock.
			return &database.ChargebackResult{Order: order, WasPaid: false, Transitioned: false, Downgraded: false}, nil
		},
		GetNodesByPlanIDFunc: func(context.Context, uint) ([]database.Node, error) {
			return nil, nil
		},
	}
	adminBot := testutil.NewBotAPI()
	o := NewOrderService(mock, nil, NewSyncService(mock, nil, nil), fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	const workers = 4
	type outcome struct {
		wasPaid bool
		err     error
	}
	outcomes := make([]outcome, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CHARGEBACKED", json.Number("69.00"), "RUB")
			outcomes[i] = outcome{wasPaid: wasPaid, err: err}
		}()
	}
	close(start)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first CHARGEBACKED did not reach CAS")
	}
	close(release)
	wg.Wait()

	paidReports := 0
	for _, r := range outcomes {
		require.NoError(t, r.err)
		if r.wasPaid {
			paidReports++
		}
	}
	assert.Equal(t, 1, paidReports, "only the first CHARGEBACKED may report wasPaid=true")
	// The lock-serialized path may run the CAS again for every goroutine that
	// reacquires after the first release; the strict invariant is that exactly
	// one goroutine produces the paid→canceled transition. Later callers all
	// observe Transitioned=false and short-circuit.
	assert.Equal(t, int32(1), transitions.Load(),
		"exactly one goroutine may transition paid→canceled")
	assert.GreaterOrEqual(t, lockCASCalls.Load(), int32(1),
		"at least one caller must reach the atomic CAS repository")
	// Exactly one admin alert and it carries the buyer notification text.
	messages := adminBot.GetAllSentMessages()
	require.Len(t, messages, 1, "duplicates must not produce extra buyer alerts")
	assert.Contains(t, messages[0].Text, "Chargeback по платежу")
}

// TestCancelPaymentByProvider_SameOrderParallelPendingCancelsIdempotent asserts
// the dual of the chargeback test: concurrent CANCELED callbacks on a pending
// order must serialize. The first transitions pending→canceled; the rest
// observe a no-op. No admin alerts are expected (no money was ever collected).
func TestCancelPaymentByProvider_SameOrderParallelPendingCancelsIdempotent(t *testing.T) {
	t.Parallel()

	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440146")
	order := &database.Order{ID: 85, SubscriptionID: 95, ProductID: 105, Status: database.OrderStatusPending, AmountCents: 2300, Currency: "RUB", ProviderPaymentID: providerID.String()}

	started := make(chan struct{})
	release := make(chan struct{})
	// lockCASCalls counts every goroutine that reached the atomic, transitions
	// tracks real pending→canceled actions.
	var lockCASCalls atomic.Int32
	var transitions atomic.Int32

	mock := &testutil.DatabaseService{
		GetOrderByProviderPaymentIDFunc: func(context.Context, string, uuid.UUID) (*database.Order, error) {
			return order, nil
		},
		CancelOrderCASFunc: func(_ context.Context, _ string, _ uuid.UUID, _ []database.OrderStatus) (bool, error) {
			n := lockCASCalls.Add(1)
			if n == 1 {
				close(started)
				<-release
				order.Status = database.OrderStatusCanceled
				transitions.Add(1)
				return true, nil
			}
			// Subsequent callers observe that the order was already canceled
			// before this CAS acquired; the conditional UPDATE matches zero rows.
			return false, nil
		},
	}
	adminBot := testutil.NewBotAPI()
	o := NewOrderService(mock, nil, nil, fakePaymentProvider{}, "", &config.Config{TelegramAdminID: 999})
	o.SetAdminBot(adminBot)

	const workers = 4
	type outcome struct {
		wasPaid bool
		err     error
	}
	outcomes := make([]outcome, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, wasPaid, err := o.CancelPaymentByProvider(context.Background(), providerID, "CANCELED", json.Number("23.00"), "RUB")
			outcomes[i] = outcome{wasPaid: wasPaid, err: err}
		}()
	}
	close(start)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first CANCELED did not reach CAS")
	}
	close(release)
	wg.Wait()

	for _, r := range outcomes {
		require.NoError(t, r.err)
		assert.False(t, r.wasPaid, "a plain CANCELED must never report wasPaid, even under contention")
	}
	assert.Equal(t, int32(1), transitions.Load(),
		"exactly one goroutine may transition pending→canceled")
	assert.GreaterOrEqual(t, lockCASCalls.Load(), int32(1),
		"at least one caller must reach the atomic CAS repository")
	assert.Empty(t, adminBot.GetAllSentMessages(), "plain CANCELED is silent for the admin (no money was collected)")
}
