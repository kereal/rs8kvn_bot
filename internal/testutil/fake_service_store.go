package testutil

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"gorm.io/gorm"
)

// ServiceStoreFake is the in-memory fake for service-package consumers: it implements
// interfaces.SubscriptionStore, interfaces.SyncStore and interfaces.OrderStore, the
// three seams service tests wire (order tests build SubscriptionService, SyncService
// and OrderService over one instance).

type ServiceStoreFake struct {
	mu                                          sync.RWMutex
	Subscriptions                               map[int64]*database.Subscription
	SubscriptionsByID                           map[uint]*database.Subscription
	Orders                                      map[uint]*database.Order
	Broadcasts                                  map[uint]*database.Broadcast
	AuditLogs                                   []database.AuditLog
	BindTrialSubscriptionFunc                   func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CancelOrderCASFunc                          func(ctx context.Context, provider string, providerPaymentID uuid.UUID, fromStatuses []database.OrderStatus) (bool, error)
	CancelPaidOrderAndDowngradeCASFunc          func(ctx context.Context, provider string, providerPaymentID uuid.UUID, now time.Time, freePlanID uint, applyPlan database.ChargebackPlanInTxFn) (*database.ChargebackResult, error)
	ClaimExpiredTrialsFunc                      func(ctx context.Context, hours int) ([]database.Subscription, error)
	ClaimReminderFunc                           func(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error)
	ClaimTrafficReminderFunc                    func(ctx context.Context, id uint, bit int) (bool, error)
	CleanupExpiredTrialsFunc                    func(ctx context.Context, hours int) ([]database.Subscription, error)
	ConfirmOrderPaidCASFunc                     func(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *database.Subscription, product *database.Product, applyPlan database.ApplyPlanInTxFn, callbackAmountCents int64) (bool, error)
	CountActiveSubscriptionsFunc                func(ctx context.Context) (int64, error)
	CountFreeSubscriptionsFunc                  func(ctx context.Context) (int64, error)
	CountPremiumSubscriptionsFunc               func(ctx context.Context) (int64, error)
	CountTrialSubscriptionsFunc                 func(ctx context.Context) (int64, error)
	CreateSubscriptionFunc                      func(ctx context.Context, sub *database.Subscription, inviteCode string) error
	CreateTrialSubscriptionFunc                 func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error)
	DeleteClaimedTrialFunc                      func(ctx context.Context, id uint) error
	DeleteSubscriptionByIDFunc                  func(ctx context.Context, id uint) (*database.Subscription, error)
	DeleteSubscriptionNodeFunc                  func(ctx context.Context, subID, nodeID uint) error
	DeleteSubscriptionNodesBySubscriptionIDFunc func(ctx context.Context, subID uint) error
	ExpireSubscriptionFunc                      func(ctx context.Context, id uint, freePlanID uint) error
	FindOrCreatePendingPaymentOrderFunc         func(ctx context.Context, subscriptionID, productID uint, amountCents int64, currency string, now time.Time) (*database.Order, error)
	FindPendingPaymentOrderFunc                 func(ctx context.Context, subscriptionID, productID uint, now time.Time) (*database.Order, error)
	GetAllSubscriptionsFunc                     func(ctx context.Context) ([]database.Subscription, error)
	GetAnyByTelegramIDFunc                      func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	GetBroadcastRecipientsStatsFunc             func(ctx context.Context, broadcastID uint) (total, sent, blocked, unreachable, failed int64, report database.BroadcastDeliveryReport, err error)
	GetByIDFunc                                 func(ctx context.Context, id uint) (*database.Subscription, error)
	GetBySubscriptionIDFunc                     func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetByTelegramIDFunc                         func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	GetInviteByCodeFunc                         func(ctx context.Context, code string) (*database.Invite, error)
	GetNodeByIDFunc                             func(ctx context.Context, id uint) (*database.Node, error)
	GetNodesByPlanIDFunc                        func(ctx context.Context, planID uint) ([]database.Node, error)
	GetNodesByPlanNameFunc                      func(ctx context.Context, planName string) ([]database.Node, error)
	GetOrderByIDFunc                            func(ctx context.Context, id uint) (*database.Order, error)
	GetOrderByProviderPaymentIDFunc             func(ctx context.Context, provider string, providerPaymentID uuid.UUID) (*database.Order, error)
	GetPaidOrdersWithoutProviderFeeAfterIDFunc  func(ctx context.Context, afterID uint, limit int) ([]database.Order, error)
	GetPaidOrdersWithoutProviderFeeFunc         func(ctx context.Context, limit int) ([]database.Order, error)
	GetPendingBySubscriptionIDFunc              func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetPendingSyncFunc                          func(ctx context.Context) ([]database.SubscriptionNode, error)
	GetPlanByIDFunc                             func(ctx context.Context, planID uint) (*database.Plan, error)
	GetPlanByNameFunc                           func(ctx context.Context, name string) (*database.Plan, error)
	GetProductByIDFunc                          func(ctx context.Context, id uint) (*database.Product, error)
	GetTrialSubscriptionBySubIDFunc             func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	InsertAuditLogFunc                          func(ctx context.Context, e *database.AuditLog) error
	MarkActiveNodesPendingUpdateFunc            func(ctx context.Context, subID uint, targetNodeIDs []uint) error
	MarkPaymentCreationUncertainFunc            func(ctx context.Context, orderID uint, uncertain bool) (bool, error)
	ReleaseReminderFunc                         func(ctx context.Context, id uint, bit int, expiresAt time.Time) error
	ReleaseTrafficReminderFunc                  func(ctx context.Context, id uint, bit int) error
	SaveOrderPaymentAmountsFunc                 func(ctx context.Context, orderID uint, callbackAmountCents int64, providerFeeCents *int64) error
	SavePaymentDetailsFunc                      func(ctx context.Context, orderID uint, providerPaymentID uuid.UUID, paymentURL string, paymentExpiresAt time.Time) error
	UpdateRetryFunc                             func(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error
	UpdateSubscriptionFunc                      func(ctx context.Context, sub *database.Subscription) error
	UpdateSubscriptionNodeStatusFunc            func(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error
	UpsertSubscriptionNodeFunc                  func(ctx context.Context, sn *database.SubscriptionNode) error
}

// NewServiceStoreFake returns an empty ServiceStoreFake with initialised storage.
func NewServiceStoreFake() *ServiceStoreFake {
	return &ServiceStoreFake{
		Subscriptions:     make(map[int64]*database.Subscription),
		SubscriptionsByID: make(map[uint]*database.Subscription),
		Orders:            make(map[uint]*database.Order),
		Broadcasts:        make(map[uint]*database.Broadcast),
	}
}

var (
	_ interfaces.SubscriptionStore = (*ServiceStoreFake)(nil)
	_ interfaces.SyncStore         = (*ServiceStoreFake)(nil)
	_ interfaces.OrderStore        = (*ServiceStoreFake)(nil)
)

func (m *ServiceStoreFake) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	if m.BindTrialSubscriptionFunc != nil {
		return m.BindTrialSubscriptionFunc(ctx, subscriptionID, telegramID, username)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, sub := range m.Subscriptions {
		if sub.SubscriptionID == subscriptionID {
			sub.TelegramID = telegramID
			sub.Username = username
			sub.PlanID = 0

			return sub, nil
		}
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) CancelOrderCAS(ctx context.Context, provider string, providerPaymentID uuid.UUID, fromStatuses []database.OrderStatus) (bool, error) {
	if m.CancelOrderCASFunc != nil {
		return m.CancelOrderCASFunc(ctx, provider, providerPaymentID, fromStatuses)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, order := range m.Orders {
		if order.PaymentProvider == provider && order.ProviderPaymentID == providerPaymentID.String() {
			if slices.Contains(fromStatuses, order.Status) {
				order.Status = database.OrderStatusCanceled
				return true, nil
			}
		}
	}

	return false, nil
}

func (m *ServiceStoreFake) CancelPaidOrderAndDowngradeCAS(ctx context.Context, provider string, providerPaymentID uuid.UUID, now time.Time, freePlanID uint, applyPlan database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
	if m.CancelPaidOrderAndDowngradeCASFunc != nil {
		return m.CancelPaidOrderAndDowngradeCASFunc(ctx, provider, providerPaymentID, now, freePlanID, applyPlan)
	}

	return nil, errors.New("mock atomic chargeback requires CancelPaidOrderAndDowngradeCASFunc")
}

func (m *ServiceStoreFake) ClaimExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error) {
	if m.ClaimExpiredTrialsFunc != nil {
		return m.ClaimExpiredTrialsFunc(ctx, hours)
	}
	if m.CleanupExpiredTrialsFunc != nil {
		return m.CleanupExpiredTrialsFunc(ctx, hours)
	}

	return nil, nil
}

// ClaimReminder delegates to ClaimReminderFunc; the real implementation is a
// transactional CAS over the reminders bitmask that the fake does not mirror.
func (m *ServiceStoreFake) ClaimReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error) {
	if m.ClaimReminderFunc != nil {
		return m.ClaimReminderFunc(ctx, id, bit, expiresAt)
	}

	return false, errors.New("fake: ClaimReminderFunc is not set")
}

// ClaimTrafficReminder delegates to ClaimTrafficReminderFunc; the real
// implementation is a transactional CAS over the traffic-reminder bitmask that
// the fake does not mirror.
func (m *ServiceStoreFake) ClaimTrafficReminder(ctx context.Context, id uint, bit int) (bool, error) {
	if m.ClaimTrafficReminderFunc != nil {
		return m.ClaimTrafficReminderFunc(ctx, id, bit)
	}

	return false, errors.New("fake: ClaimTrafficReminderFunc is not set")
}

func (m *ServiceStoreFake) CleanupExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error) {
	if m.CleanupExpiredTrialsFunc != nil {
		return m.CleanupExpiredTrialsFunc(ctx, hours)
	}

	return nil, nil
}

func (m *ServiceStoreFake) ConfirmOrderPaidCAS(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *database.Subscription, product *database.Product, applyPlan database.ApplyPlanInTxFn, callbackAmountCents int64) (bool, error) {
	if m.ConfirmOrderPaidCASFunc != nil {
		return m.ConfirmOrderPaidCASFunc(ctx, orderID, paidAt, activatedAt, sub, product, applyPlan, callbackAmountCents)
	}

	// The real implementation is a transactional CAS computing the new expiry
	// (CalculatePaymentExpiry); the fake deliberately does not mirror that
	// domain logic — tests set the Func or use a real test database.
	return false, errors.New("fake: ConfirmOrderPaidCASFunc is not set")
}

func (m *ServiceStoreFake) CountActiveSubscriptions(ctx context.Context) (int64, error) {
	if m.CountActiveSubscriptionsFunc != nil {
		return m.CountActiveSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var count int64

	for _, sub := range m.Subscriptions {
		if sub.Status == string(database.SubscriptionStatusActive) && !sub.IsExpired() {
			count++
		}
	}

	return count, nil
}

func (m *ServiceStoreFake) CountFreeSubscriptions(ctx context.Context) (int64, error) {
	if m.CountFreeSubscriptionsFunc != nil {
		return m.CountFreeSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var count int64
	for _, sub := range m.Subscriptions {
		if sub.Status == string(database.SubscriptionStatusActive) &&
			sub.TelegramID > 0 && sub.ProductID == nil && sub.PricePaidCents == 0 {
			count++
		}
	}

	return count, nil
}

func (m *ServiceStoreFake) CountPremiumSubscriptions(ctx context.Context) (int64, error) {
	if m.CountPremiumSubscriptionsFunc != nil {
		return m.CountPremiumSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var count int64
	for _, sub := range m.Subscriptions {
		if sub.Status == string(database.SubscriptionStatusActive) &&
			(sub.ProductID != nil || sub.PricePaidCents > 0) {
			count++
		}
	}

	return count, nil
}

func (m *ServiceStoreFake) CountTrialSubscriptions(ctx context.Context) (int64, error) {
	if m.CountTrialSubscriptionsFunc != nil {
		return m.CountTrialSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var count int64

	for _, sub := range m.Subscriptions {
		if sub.TelegramID < 0 {
			count++
		}
	}

	return count, nil
}

func (m *ServiceStoreFake) CreateSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) error {
	if m.CreateSubscriptionFunc != nil {
		return m.CreateSubscriptionFunc(ctx, sub, inviteCode)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Subscriptions == nil {
		m.Subscriptions = make(map[int64]*database.Subscription)
	}

	if m.SubscriptionsByID == nil {
		m.SubscriptionsByID = make(map[uint]*database.Subscription)
	}

	stored := *sub
	if sub.TelegramID > 0 {
		m.Subscriptions[sub.TelegramID] = &stored
	}

	if sub.ID > 0 {
		m.SubscriptionsByID[sub.ID] = &stored
	}

	return nil
}

func (m *ServiceStoreFake) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
	if m.CreateTrialSubscriptionFunc != nil {
		return m.CreateTrialSubscriptionFunc(ctx, inviteCode, subscriptionID, clientID, expiryTime)
	}

	trialPlan, err := m.GetPlanByName(ctx, database.TrialPlanName)
	if err != nil {
		return nil, err
	}

	inviteVal := inviteCode

	return &database.Subscription{InviteCode: &inviteVal, SubscriptionID: subscriptionID, ClientID: clientID, PlanID: trialPlan.ID}, nil
}

func (m *ServiceStoreFake) DeleteClaimedTrial(ctx context.Context, id uint) error {
	if m.DeleteClaimedTrialFunc != nil {
		return m.DeleteClaimedTrialFunc(ctx, id)
	}

	return nil
}

func (m *ServiceStoreFake) DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error) {
	if m.DeleteSubscriptionByIDFunc != nil {
		return m.DeleteSubscriptionByIDFunc(ctx, id)
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) DeleteSubscriptionNode(ctx context.Context, subID, nodeID uint) error {
	if m.DeleteSubscriptionNodeFunc != nil {
		return m.DeleteSubscriptionNodeFunc(ctx, subID, nodeID)
	}

	return nil
}

func (m *ServiceStoreFake) DeleteSubscriptionNodesBySubscriptionID(ctx context.Context, subID uint) error {
	if m.DeleteSubscriptionNodesBySubscriptionIDFunc != nil {
		return m.DeleteSubscriptionNodesBySubscriptionIDFunc(ctx, subID)
	}

	return nil
}

func (m *ServiceStoreFake) ExpireSubscription(ctx context.Context, id uint, freePlanID uint) error {
	return nil
}

func (m *ServiceStoreFake) FindOrCreatePendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, amountCents int64, currency string, now time.Time) (*database.Order, error) {
	if m.FindOrCreatePendingPaymentOrderFunc != nil {
		return m.FindOrCreatePendingPaymentOrderFunc(ctx, subscriptionID, productID, amountCents, currency, now)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, order := range m.Orders {
		if order.SubscriptionID == subscriptionID && order.ProductID == productID && order.PaymentProvider == "platega" && order.Status == database.OrderStatusPending {
			if order.PaymentCreationUncertain {
				return order, nil
			}

			if order.PaymentExpiresAt != nil && !now.Before(*order.PaymentExpiresAt) {
				order.Status = database.OrderStatusExpired
				continue
			}

			return order, nil
		}
	}

	if m.Orders == nil {
		m.Orders = make(map[uint]*database.Order)
	}

	order := &database.Order{ID: uint(len(m.Orders) + 1), SubscriptionID: subscriptionID, ProductID: productID, Status: database.OrderStatusPending, AmountCents: amountCents, Currency: currency, PaymentProvider: "platega", CreatedAt: now} // #nosec G115 -- slice length is non-negative
	m.Orders[order.ID] = order

	return order, nil
}

func (m *ServiceStoreFake) FindPendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, now time.Time) (*database.Order, error) {
	if m.FindPendingPaymentOrderFunc != nil {
		return m.FindPendingPaymentOrderFunc(ctx, subscriptionID, productID, now)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, order := range m.Orders {
		if order.SubscriptionID == subscriptionID && order.ProductID == productID && order.PaymentProvider == "platega" && order.Status == database.OrderStatusPending {
			if order.PaymentExpiresAt != nil && !now.Before(*order.PaymentExpiresAt) {
				order.Status = database.OrderStatusExpired
				continue
			}

			return order, nil
		}
	}

	return nil, nil
}

func (m *ServiceStoreFake) GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error) {
	if m.GetAllSubscriptionsFunc != nil {
		return m.GetAllSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []database.Subscription
	for _, sub := range m.Subscriptions {
		result = append(result, *sub)
	}

	return result, nil
}

func (m *ServiceStoreFake) GetAnyByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	if m.GetAnyByTelegramIDFunc != nil {
		return m.GetAnyByTelegramIDFunc(ctx, telegramID)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if sub, ok := m.Subscriptions[telegramID]; ok {
		return sub, nil
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetBroadcastRecipientsStats(ctx context.Context, broadcastID uint) (total, sent, blocked, unreachable, failed int64, report database.BroadcastDeliveryReport, err error) {
	if m.GetBroadcastRecipientsStatsFunc != nil {
		return m.GetBroadcastRecipientsStatsFunc(ctx, broadcastID)
	}

	// The real implementation aggregates the broadcast recipients snapshot in
	// SQL; the fake does not mirror that reporting logic — tests set the Func
	// or use a real test database.
	return 0, 0, 0, 0, 0, report, errors.New("fake: GetBroadcastRecipientsStatsFunc is not set")
}

func (m *ServiceStoreFake) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
	if m.GetByIDFunc != nil {
		return m.GetByIDFunc(ctx, id)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if sub, ok := m.SubscriptionsByID[id]; ok {
		copy := *sub
		return &copy, nil
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	if m.GetBySubscriptionIDFunc != nil {
		return m.GetBySubscriptionIDFunc(ctx, subscriptionID)
	}

	return nil, nil
}

func (m *ServiceStoreFake) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	if m.GetByTelegramIDFunc != nil {
		return m.GetByTelegramIDFunc(ctx, telegramID)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if sub, ok := m.Subscriptions[telegramID]; ok {
		return sub, nil
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	if m.GetInviteByCodeFunc != nil {
		return m.GetInviteByCodeFunc(ctx, code)
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetNodeByID(ctx context.Context, id uint) (*database.Node, error) {
	if m.GetNodeByIDFunc != nil {
		return m.GetNodeByIDFunc(ctx, id)
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetNodesByPlanID(ctx context.Context, planID uint) ([]database.Node, error) {
	if m.GetNodesByPlanIDFunc != nil {
		return m.GetNodesByPlanIDFunc(ctx, planID)
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetNodesByPlanName(ctx context.Context, planName string) ([]database.Node, error) {
	if m.GetNodesByPlanNameFunc != nil {
		return m.GetNodesByPlanNameFunc(ctx, planName)
	}

	if planName == database.TrialPlanName {
		inboundIDs, err := json.Marshal([]int{1})
		if err != nil {
			return nil, err
		}

		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: string(inboundIDs)}}, nil
	}

	return nil, nil
}

func (m *ServiceStoreFake) GetOrderByID(ctx context.Context, id uint) (*database.Order, error) {
	if m.GetOrderByIDFunc != nil {
		return m.GetOrderByIDFunc(ctx, id)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if order, ok := m.Orders[id]; ok {
		return order, nil
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetOrderByProviderPaymentID(ctx context.Context, provider string, providerPaymentID uuid.UUID) (*database.Order, error) {
	if m.GetOrderByProviderPaymentIDFunc != nil {
		return m.GetOrderByProviderPaymentIDFunc(ctx, provider, providerPaymentID)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, order := range m.Orders {
		if order.PaymentProvider == provider && order.ProviderPaymentID == providerPaymentID.String() {
			return order, nil
		}
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetPaidOrdersWithoutProviderFee(ctx context.Context, limit int) ([]database.Order, error) {
	return m.GetPaidOrdersWithoutProviderFeeAfterID(ctx, 0, limit)
}

func (m *ServiceStoreFake) GetPaidOrdersWithoutProviderFeeAfterID(ctx context.Context, afterID uint, limit int) ([]database.Order, error) {
	if m.GetPaidOrdersWithoutProviderFeeAfterIDFunc != nil {
		return m.GetPaidOrdersWithoutProviderFeeAfterIDFunc(ctx, afterID, limit)
	}

	if m.GetPaidOrdersWithoutProviderFeeFunc != nil {
		orders, err := m.GetPaidOrdersWithoutProviderFeeFunc(ctx, limit)
		if err != nil {
			return nil, err
		}

		filtered := orders[:0]
		for _, order := range orders {
			if order.ID > afterID {
				filtered = append(filtered, order)
			}
		}

		return filtered, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	orders := make([]database.Order, 0)
	for _, order := range m.Orders {
		if order == nil || order.ID <= afterID || order.Status != database.OrderStatusPaid || order.PaymentProvider != "platega" || order.ProviderPaymentID == "" || order.ProviderFeeCents != nil {
			continue
		}
		orders = append(orders, *order)
	}
	// Map iteration order is random; sort by ID so callers observe the same
	// deterministic order as the production query (ORDER BY id ASC).
	slices.SortFunc(orders, func(a, b database.Order) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	if limit > 0 && len(orders) > limit {
		orders = orders[:limit]
	}

	return orders, nil
}

func (m *ServiceStoreFake) GetPendingBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	if m.GetPendingBySubscriptionIDFunc != nil {
		return m.GetPendingBySubscriptionIDFunc(ctx, subscriptionID)
	}

	return nil, nil
}

func (m *ServiceStoreFake) GetPendingSync(ctx context.Context) ([]database.SubscriptionNode, error) {
	if m.GetPendingSyncFunc != nil {
		return m.GetPendingSyncFunc(ctx)
	}

	return nil, nil
}

func (m *ServiceStoreFake) GetPlanByID(ctx context.Context, planID uint) (*database.Plan, error) {
	if m.GetPlanByIDFunc != nil {
		return m.GetPlanByIDFunc(ctx, planID)
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetPlanByName(ctx context.Context, name string) (*database.Plan, error) {
	if m.GetPlanByNameFunc != nil {
		return m.GetPlanByNameFunc(ctx, name)
	}

	if name == database.FreePlanName {
		return &database.Plan{ID: 2, Name: name, DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}

	if name == database.TrialPlanName {
		return &database.Plan{ID: 1, Name: name, DevicesLimit: 1, TrafficLimit: 1073741824}, nil
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetProductByID(ctx context.Context, id uint) (*database.Product, error) {
	if m.GetProductByIDFunc != nil {
		return m.GetProductByIDFunc(ctx, id)
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *ServiceStoreFake) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	if m.GetTrialSubscriptionBySubIDFunc != nil {
		return m.GetTrialSubscriptionBySubIDFunc(ctx, subscriptionID)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	trialPlan, err := m.GetPlanByName(ctx, database.TrialPlanName)
	if err != nil {
		return nil, err
	}

	for _, sub := range m.Subscriptions {
		if sub.SubscriptionID == subscriptionID && sub.PlanID == trialPlan.ID {
			return sub, nil
		}
	}

	// Lightweight command-handler tests often configure only the bind seam. A
	// synthetic trial keeps that fake compatible with the service's pre-bind
	// referral validation while preserving the normal not-found default.
	if m.BindTrialSubscriptionFunc != nil {
		inviteCode := subscriptionID
		return &database.Subscription{SubscriptionID: subscriptionID, InviteCode: &inviteCode, PlanID: trialPlan.ID}, nil
	}

	return nil, gorm.ErrRecordNotFound
}

// InsertAuditLog records an audit event in the in-memory fake. Tests assert on
// m.AuditLogs to verify which events a flow produced.
func (m *ServiceStoreFake) InsertAuditLog(ctx context.Context, e *database.AuditLog) error {
	if m.InsertAuditLogFunc != nil {
		return m.InsertAuditLogFunc(ctx, e)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e == nil {
		return nil
	}

	// #nosec G115 -- в тестовом фейке ID всегда положительный
	stored := *e
	stored.ID = uint(len(m.AuditLogs) + 1)
	m.AuditLogs = append(m.AuditLogs, stored)

	return nil
}

func (m *ServiceStoreFake) MarkActiveNodesPendingUpdate(ctx context.Context, subID uint, targetNodeIDs []uint) error {
	if m.MarkActiveNodesPendingUpdateFunc != nil {
		return m.MarkActiveNodesPendingUpdateFunc(ctx, subID, targetNodeIDs)
	}

	return nil
}

func (m *ServiceStoreFake) MarkPaymentCreationUncertain(ctx context.Context, orderID uint, uncertain bool) (bool, error) {
	if m.MarkPaymentCreationUncertainFunc != nil {
		return m.MarkPaymentCreationUncertainFunc(ctx, orderID, uncertain)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.Orders[orderID]
	if !ok || order.Status != database.OrderStatusPending || order.PaymentCreationUncertain == uncertain || (uncertain && order.ProviderPaymentID != "") {
		return false, nil
	}

	order.PaymentCreationUncertain = uncertain

	return true, nil
}

// ReleaseReminder delegates to ReleaseReminderFunc; the real implementation is
// a transactional CAS over the reminders bitmask that the fake does not mirror.
func (m *ServiceStoreFake) ReleaseReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) error {
	if m.ReleaseReminderFunc != nil {
		return m.ReleaseReminderFunc(ctx, id, bit, expiresAt)
	}

	return errors.New("fake: ReleaseReminderFunc is not set")
}

// ReleaseTrafficReminder delegates to ReleaseTrafficReminderFunc; the real
// implementation is a transactional CAS over the traffic-reminder bitmask that
// the fake does not mirror.
func (m *ServiceStoreFake) ReleaseTrafficReminder(ctx context.Context, id uint, bit int) error {
	if m.ReleaseTrafficReminderFunc != nil {
		return m.ReleaseTrafficReminderFunc(ctx, id, bit)
	}

	return errors.New("fake: ReleaseTrafficReminderFunc is not set")
}

func (m *ServiceStoreFake) SaveOrderPaymentAmounts(ctx context.Context, orderID uint, callbackAmountCents int64, providerFeeCents *int64) error {
	if m.SaveOrderPaymentAmountsFunc != nil {
		return m.SaveOrderPaymentAmountsFunc(ctx, orderID, callbackAmountCents, providerFeeCents)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.Orders[orderID]
	if !ok {
		// Production Updates() on a missing row succeeds with no error
		// (RowsAffected 0); mirror that instead of surfacing ErrRecordNotFound.
		return nil
	}

	order.CallbackAmountCents = &callbackAmountCents
	if providerFeeCents != nil {
		order.ProviderFeeCents = providerFeeCents
	}

	return nil
}

func (m *ServiceStoreFake) SavePaymentDetails(ctx context.Context, orderID uint, providerPaymentID uuid.UUID, paymentURL string, paymentExpiresAt time.Time) error {
	if m.SavePaymentDetailsFunc != nil {
		return m.SavePaymentDetailsFunc(ctx, orderID, providerPaymentID, paymentURL, paymentExpiresAt)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.Orders[orderID]
	if !ok || order.Status != database.OrderStatusPending {
		return gorm.ErrRecordNotFound
	}

	order.ProviderPaymentID, order.PaymentURL, order.PaymentExpiresAt, order.PaymentCreationUncertain = providerPaymentID.String(), paymentURL, &paymentExpiresAt, false

	return nil
}

func (m *ServiceStoreFake) UpdateRetry(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error {
	if m.UpdateRetryFunc != nil {
		return m.UpdateRetryFunc(ctx, subID, nodeID, retryCount, retryAt, lastErr)
	}

	return nil
}

func (m *ServiceStoreFake) UpdateSubscription(ctx context.Context, sub *database.Subscription) error {
	if m.UpdateSubscriptionFunc != nil {
		return m.UpdateSubscriptionFunc(ctx, sub)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Subscriptions == nil {
		m.Subscriptions = make(map[int64]*database.Subscription)
	}

	stored := *sub
	if sub.TelegramID > 0 {
		m.Subscriptions[sub.TelegramID] = &stored
	}

	if sub.ID > 0 {
		if m.SubscriptionsByID == nil {
			m.SubscriptionsByID = make(map[uint]*database.Subscription)
		}

		m.SubscriptionsByID[sub.ID] = &stored
	}

	return nil
}

func (m *ServiceStoreFake) UpdateSubscriptionNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error {
	if m.UpdateSubscriptionNodeStatusFunc != nil {
		return m.UpdateSubscriptionNodeStatusFunc(ctx, subID, nodeID, status)
	}

	return nil
}

func (m *ServiceStoreFake) UpsertSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error {
	if m.UpsertSubscriptionNodeFunc != nil {
		return m.UpsertSubscriptionNodeFunc(ctx, sn)
	}

	return nil
}

// AuditLogsByEvent returns the recorded audit entries with the given event;
// test assertion helper, not part of any consumer interface.
func (m *ServiceStoreFake) AuditLogsByEvent(event string) []database.AuditLog {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []database.AuditLog
	for _, e := range m.AuditLogs {
		if e.Event == event {
			out = append(out, e)
		}
	}

	return out
}
