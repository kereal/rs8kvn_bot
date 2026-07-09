package testutil

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"gorm.io/gorm"
)

// Per-slice fakes (3A). Each implements exactly one sub-interface from
// internal/interfaces, so a test can depend on — and stub — only the slice it
// needs, instead of the full DatabaseService. The flat DatabaseService fake is
// kept for backward compatibility with the existing &DatabaseService{...}
// composite-literal tests; these slice fakes are opt-in for new tests.
//
// Note: Go does not allow setting an embedded (promoted) field inside a
// composite literal, so DatabaseService is NOT composed via struct embedding —
// that would break the ~40 &DatabaseService{ Func: ... } literals in the suite.
// Instead the slices live here as standalone, composable fakes.

// --- SubscriptionRepository ---

type SubscriptionRepositoryFake struct {
	GetByTelegramIDFunc             func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	CreateSubscriptionFunc          func(ctx context.Context, sub *database.Subscription, inviteCode string) error
	UpdateSubscriptionFunc          func(ctx context.Context, sub *database.Subscription) error
	DeleteSubscriptionFunc          func(ctx context.Context, telegramID int64) error
	DeleteSubscriptionByIDFunc      func(ctx context.Context, id uint) (*database.Subscription, error)
	GetLatestSubscriptionsFunc      func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetAllSubscriptionsFunc         func(ctx context.Context) ([]database.Subscription, error)
	CountAllSubscriptionsFunc       func(ctx context.Context) (int64, error)
	CountActiveSubscriptionsFunc    func(ctx context.Context) (int64, error)
	CountExpiredSubscriptionsFunc   func(ctx context.Context) (int64, error)
	GetAllTelegramIDsFunc           func(ctx context.Context) ([]int64, error)
	GetTelegramIDByUsernameFunc     func(ctx context.Context, username string) (int64, error)
	GetTelegramIDsBatchFunc         func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc     func(ctx context.Context) (int64, error)
	GetSubscriptionStatusFunc       func(ctx context.Context, subscriptionID string) (string, time.Time, error)
	ExpireSubscriptionFunc          func(ctx context.Context, id uint, freePlanID uint) error
	GetExpiredPaidSubscriptionsFunc func(ctx context.Context, now time.Time) ([]database.Subscription, error)
	UpdateDevicesFunc               func(ctx context.Context, id uint, devicesJSON string) error
	UpdateIPsFunc                   func(ctx context.Context, id uint, ipsJSON string) error
	UpdateLastRequestFunc           func(ctx context.Context, subscriptionID string) error
	GetSubscriptionFunc             func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	GetWithPlanAndNodesFunc         func(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error)
}

func NewSubscriptionRepository() *SubscriptionRepositoryFake { return &SubscriptionRepositoryFake{} }

func (m *SubscriptionRepositoryFake) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	if m.GetByTelegramIDFunc != nil {
		return m.GetByTelegramIDFunc(ctx, telegramID)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *SubscriptionRepositoryFake) CreateSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) error {
	if m.CreateSubscriptionFunc != nil {
		return m.CreateSubscriptionFunc(ctx, sub, inviteCode)
	}
	return nil
}
func (m *SubscriptionRepositoryFake) UpdateSubscription(ctx context.Context, sub *database.Subscription) error {
	if m.UpdateSubscriptionFunc != nil {
		return m.UpdateSubscriptionFunc(ctx, sub)
	}
	return nil
}
func (m *SubscriptionRepositoryFake) DeleteSubscription(ctx context.Context, telegramID int64) error {
	if m.DeleteSubscriptionFunc != nil {
		return m.DeleteSubscriptionFunc(ctx, telegramID)
	}
	return nil
}
func (m *SubscriptionRepositoryFake) DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error) {
	if m.DeleteSubscriptionByIDFunc != nil {
		return m.DeleteSubscriptionByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *SubscriptionRepositoryFake) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	if m.GetLatestSubscriptionsFunc != nil {
		return m.GetLatestSubscriptionsFunc(ctx, limit)
	}
	return nil, nil
}
func (m *SubscriptionRepositoryFake) GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error) {
	if m.GetAllSubscriptionsFunc != nil {
		return m.GetAllSubscriptionsFunc(ctx)
	}
	return nil, nil
}
func (m *SubscriptionRepositoryFake) CountAllSubscriptions(ctx context.Context) (int64, error) {
	if m.CountAllSubscriptionsFunc != nil {
		return m.CountAllSubscriptionsFunc(ctx)
	}
	return 0, nil
}
func (m *SubscriptionRepositoryFake) CountActiveSubscriptions(ctx context.Context) (int64, error) {
	if m.CountActiveSubscriptionsFunc != nil {
		return m.CountActiveSubscriptionsFunc(ctx)
	}
	return 0, nil
}
func (m *SubscriptionRepositoryFake) CountExpiredSubscriptions(ctx context.Context) (int64, error) {
	if m.CountExpiredSubscriptionsFunc != nil {
		return m.CountExpiredSubscriptionsFunc(ctx)
	}
	return 0, nil
}
func (m *SubscriptionRepositoryFake) GetAllTelegramIDs(ctx context.Context) ([]int64, error) {
	if m.GetAllTelegramIDsFunc != nil {
		return m.GetAllTelegramIDsFunc(ctx)
	}
	return nil, nil
}
func (m *SubscriptionRepositoryFake) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	if m.GetTelegramIDByUsernameFunc != nil {
		return m.GetTelegramIDByUsernameFunc(ctx, username)
	}
	return 0, gorm.ErrRecordNotFound
}
func (m *SubscriptionRepositoryFake) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	if m.GetTelegramIDsBatchFunc != nil {
		return m.GetTelegramIDsBatchFunc(ctx, offset, limit)
	}
	return nil, nil
}
func (m *SubscriptionRepositoryFake) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	if m.GetTotalTelegramIDCountFunc != nil {
		return m.GetTotalTelegramIDCountFunc(ctx)
	}
	return 0, nil
}
func (m *SubscriptionRepositoryFake) GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error) {
	if m.GetSubscriptionStatusFunc != nil {
		return m.GetSubscriptionStatusFunc(ctx, subscriptionID)
	}
	return "", time.Time{}, gorm.ErrRecordNotFound
}
func (m *SubscriptionRepositoryFake) ExpireSubscription(ctx context.Context, id uint, freePlanID uint) error {
	if m.ExpireSubscriptionFunc != nil {
		return m.ExpireSubscriptionFunc(ctx, id, freePlanID)
	}
	return nil
}
func (m *SubscriptionRepositoryFake) GetExpiredPaidSubscriptions(ctx context.Context, now time.Time) ([]database.Subscription, error) {
	if m.GetExpiredPaidSubscriptionsFunc != nil {
		return m.GetExpiredPaidSubscriptionsFunc(ctx, now)
	}
	return nil, nil
}
func (m *SubscriptionRepositoryFake) UpdateDevices(ctx context.Context, id uint, devicesJSON string) error {
	if m.UpdateDevicesFunc != nil {
		return m.UpdateDevicesFunc(ctx, id, devicesJSON)
	}
	return nil
}
func (m *SubscriptionRepositoryFake) UpdateIPs(ctx context.Context, id uint, ipsJSON string) error {
	if m.UpdateIPsFunc != nil {
		return m.UpdateIPsFunc(ctx, id, ipsJSON)
	}
	return nil
}
func (m *SubscriptionRepositoryFake) UpdateLastRequest(ctx context.Context, subscriptionID string) error {
	if m.UpdateLastRequestFunc != nil {
		return m.UpdateLastRequestFunc(ctx, subscriptionID)
	}
	return nil
}
func (m *SubscriptionRepositoryFake) GetSubscription(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	if m.GetSubscriptionFunc != nil {
		return m.GetSubscriptionFunc(ctx, subscriptionID)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *SubscriptionRepositoryFake) GetWithPlanAndNodes(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error) {
	if m.GetWithPlanAndNodesFunc != nil {
		return m.GetWithPlanAndNodesFunc(ctx, subscriptionID)
	}
	return nil, gorm.ErrRecordNotFound
}

// --- SubscriptionNodeRepository ---

type SubscriptionNodeRepositoryFake struct {
	GetBySubscriptionIDFunc                     func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetByNodeIDFunc                             func(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error)
	CreateSubscriptionNodeFunc                  func(ctx context.Context, sn *database.SubscriptionNode) error
	UpsertSubscriptionNodeFunc                  func(ctx context.Context, sn *database.SubscriptionNode) error
	DeleteSubscriptionNodeFunc                  func(ctx context.Context, subID, nodeID uint) error
	DeleteSubscriptionNodesBySubscriptionIDFunc func(ctx context.Context, subID uint) error
	MarkActiveNodesPendingUpdateFunc            func(ctx context.Context, subID uint, targetNodeIDs []uint) error
	UpdateSubscriptionNodeStatusFunc            func(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error
	UpdateRetryFunc                             func(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error
	GetPendingSyncFunc                          func(ctx context.Context) ([]database.SubscriptionNode, error)
	GetPendingBySubscriptionIDFunc              func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetPendingByNodeIDFunc                      func(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error)
}

func NewSubscriptionNodeRepository() *SubscriptionNodeRepositoryFake {
	return &SubscriptionNodeRepositoryFake{}
}

func (m *SubscriptionNodeRepositoryFake) GetBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	if m.GetBySubscriptionIDFunc != nil {
		return m.GetBySubscriptionIDFunc(ctx, subscriptionID)
	}
	return nil, nil
}
func (m *SubscriptionNodeRepositoryFake) GetByNodeID(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error) {
	if m.GetByNodeIDFunc != nil {
		return m.GetByNodeIDFunc(ctx, nodeID)
	}
	return nil, nil
}
func (m *SubscriptionNodeRepositoryFake) CreateSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error {
	if m.CreateSubscriptionNodeFunc != nil {
		return m.CreateSubscriptionNodeFunc(ctx, sn)
	}
	return nil
}
func (m *SubscriptionNodeRepositoryFake) UpsertSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error {
	if m.UpsertSubscriptionNodeFunc != nil {
		return m.UpsertSubscriptionNodeFunc(ctx, sn)
	}
	return nil
}
func (m *SubscriptionNodeRepositoryFake) DeleteSubscriptionNode(ctx context.Context, subID, nodeID uint) error {
	if m.DeleteSubscriptionNodeFunc != nil {
		return m.DeleteSubscriptionNodeFunc(ctx, subID, nodeID)
	}
	return nil
}
func (m *SubscriptionNodeRepositoryFake) DeleteSubscriptionNodesBySubscriptionID(ctx context.Context, subID uint) error {
	if m.DeleteSubscriptionNodesBySubscriptionIDFunc != nil {
		return m.DeleteSubscriptionNodesBySubscriptionIDFunc(ctx, subID)
	}
	return nil
}
func (m *SubscriptionNodeRepositoryFake) MarkActiveNodesPendingUpdate(ctx context.Context, subID uint, targetNodeIDs []uint) error {
	if m.MarkActiveNodesPendingUpdateFunc != nil {
		return m.MarkActiveNodesPendingUpdateFunc(ctx, subID, targetNodeIDs)
	}
	return nil
}
func (m *SubscriptionNodeRepositoryFake) UpdateSubscriptionNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error {
	if m.UpdateSubscriptionNodeStatusFunc != nil {
		return m.UpdateSubscriptionNodeStatusFunc(ctx, subID, nodeID, status)
	}
	return nil
}
func (m *SubscriptionNodeRepositoryFake) UpdateRetry(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error {
	if m.UpdateRetryFunc != nil {
		return m.UpdateRetryFunc(ctx, subID, nodeID, retryCount, retryAt, lastErr)
	}
	return nil
}
func (m *SubscriptionNodeRepositoryFake) GetPendingSync(ctx context.Context) ([]database.SubscriptionNode, error) {
	if m.GetPendingSyncFunc != nil {
		return m.GetPendingSyncFunc(ctx)
	}
	return nil, nil
}
func (m *SubscriptionNodeRepositoryFake) GetPendingBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	if m.GetPendingBySubscriptionIDFunc != nil {
		return m.GetPendingBySubscriptionIDFunc(ctx, subscriptionID)
	}
	return nil, nil
}
func (m *SubscriptionNodeRepositoryFake) GetPendingByNodeID(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error) {
	if m.GetPendingByNodeIDFunc != nil {
		return m.GetPendingByNodeIDFunc(ctx, nodeID)
	}
	return nil, nil
}

// --- TrialRepository ---

type TrialRepositoryFake struct {
	CreateTrialSubscriptionFunc        func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error)
	GetTrialSubscriptionBySubIDFunc    func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	BindTrialSubscriptionFunc          func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CountTrialRequestsByIPLastHourFunc func(ctx context.Context, ip string) (int, error)
	CreateTrialRequestFunc             func(ctx context.Context, ip string) error
	CleanupExpiredTrialsFunc           func(ctx context.Context, hours int) ([]database.Subscription, error)
}

func NewTrialRepository() *TrialRepositoryFake { return &TrialRepositoryFake{} }

func (m *TrialRepositoryFake) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
	if m.CreateTrialSubscriptionFunc != nil {
		return m.CreateTrialSubscriptionFunc(ctx, inviteCode, subscriptionID, clientID, expiryTime)
	}
	return &database.Subscription{
		SubscriptionID: subscriptionID,
		ClientID:       clientID,
		Status:         "active",
		ExpiresAt:      &expiryTime,
	}, nil
}
func (m *TrialRepositoryFake) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	if m.GetTrialSubscriptionBySubIDFunc != nil {
		return m.GetTrialSubscriptionBySubIDFunc(ctx, subscriptionID)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *TrialRepositoryFake) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	if m.BindTrialSubscriptionFunc != nil {
		return m.BindTrialSubscriptionFunc(ctx, subscriptionID, telegramID, username)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *TrialRepositoryFake) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	if m.CountTrialRequestsByIPLastHourFunc != nil {
		return m.CountTrialRequestsByIPLastHourFunc(ctx, ip)
	}
	return 0, nil
}
func (m *TrialRepositoryFake) CreateTrialRequest(ctx context.Context, ip string) error {
	if m.CreateTrialRequestFunc != nil {
		return m.CreateTrialRequestFunc(ctx, ip)
	}
	return nil
}
func (m *TrialRepositoryFake) CleanupExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error) {
	if m.CleanupExpiredTrialsFunc != nil {
		return m.CleanupExpiredTrialsFunc(ctx, hours)
	}
	return nil, nil
}

// --- NodeRepository ---

type NodeRepositoryFake struct {
	ListNodesFunc          func(ctx context.Context) ([]database.Node, error)
	GetNodesByPlanNameFunc func(ctx context.Context, planName string) ([]database.Node, error)
	GetNodesByPlanIDFunc   func(ctx context.Context, planID uint) ([]database.Node, error)
	GetNodeByIDFunc        func(ctx context.Context, id uint) (*database.Node, error)
	ListEnabledFunc        func(ctx context.Context) ([]database.Node, error)
}

func NewNodeRepository() *NodeRepositoryFake { return &NodeRepositoryFake{} }

func (m *NodeRepositoryFake) ListNodes(ctx context.Context) ([]database.Node, error) {
	if m.ListNodesFunc != nil {
		return m.ListNodesFunc(ctx)
	}
	return nil, nil
}
func (m *NodeRepositoryFake) GetNodesByPlanName(ctx context.Context, planName string) ([]database.Node, error) {
	if m.GetNodesByPlanNameFunc != nil {
		return m.GetNodesByPlanNameFunc(ctx, planName)
	}
	return nil, nil
}
func (m *NodeRepositoryFake) GetNodesByPlanID(ctx context.Context, planID uint) ([]database.Node, error) {
	if m.GetNodesByPlanIDFunc != nil {
		return m.GetNodesByPlanIDFunc(ctx, planID)
	}
	return nil, nil
}
func (m *NodeRepositoryFake) GetNodeByID(ctx context.Context, id uint) (*database.Node, error) {
	if m.GetNodeByIDFunc != nil {
		return m.GetNodeByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *NodeRepositoryFake) ListEnabled(ctx context.Context) ([]database.Node, error) {
	if m.ListEnabledFunc != nil {
		return m.ListEnabledFunc(ctx)
	}
	return nil, nil
}

// --- InviteRepository ---

type InviteRepositoryFake struct {
	GetOrCreateInviteFunc    func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetInviteByCodeFunc      func(ctx context.Context, code string) (*database.Invite, error)
	GetInviteByReferrerFunc  func(ctx context.Context, referrerTGID int64) (*database.Invite, error)
	GetReferralCountFunc     func(ctx context.Context, referrerTGID int64) (int64, error)
	GetAllReferralCountsFunc func(ctx context.Context) (map[int64]int64, error)
}

func NewInviteRepository() *InviteRepositoryFake { return &InviteRepositoryFake{} }

func (m *InviteRepositoryFake) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	if m.GetOrCreateInviteFunc != nil {
		return m.GetOrCreateInviteFunc(ctx, referrerTGID, code)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *InviteRepositoryFake) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	if m.GetInviteByCodeFunc != nil {
		return m.GetInviteByCodeFunc(ctx, code)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *InviteRepositoryFake) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error) {
	if m.GetInviteByReferrerFunc != nil {
		return m.GetInviteByReferrerFunc(ctx, referrerTGID)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *InviteRepositoryFake) GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error) {
	if m.GetReferralCountFunc != nil {
		return m.GetReferralCountFunc(ctx, referrerTGID)
	}
	return 0, nil
}
func (m *InviteRepositoryFake) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	if m.GetAllReferralCountsFunc != nil {
		return m.GetAllReferralCountsFunc(ctx)
	}
	return nil, nil
}

// --- PlanRepository ---

type PlanRepositoryFake struct {
	GetPlanByNameFunc func(ctx context.Context, name string) (*database.Plan, error)
	GetPlanByIDFunc   func(ctx context.Context, planID uint) (*database.Plan, error)
}

func NewPlanRepository() *PlanRepositoryFake { return &PlanRepositoryFake{} }

func (m *PlanRepositoryFake) GetPlanByName(ctx context.Context, name string) (*database.Plan, error) {
	if m.GetPlanByNameFunc != nil {
		return m.GetPlanByNameFunc(ctx, name)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *PlanRepositoryFake) GetPlanByID(ctx context.Context, planID uint) (*database.Plan, error) {
	if m.GetPlanByIDFunc != nil {
		return m.GetPlanByIDFunc(ctx, planID)
	}
	return nil, gorm.ErrRecordNotFound
}

// --- ProductRepository ---

type ProductRepositoryFake struct {
	GetActiveByPlanIDFunc func(ctx context.Context, planID uint) ([]database.Product, error)
	GetProductByIDFunc    func(ctx context.Context, id uint) (*database.Product, error)
}

func NewProductRepository() *ProductRepositoryFake { return &ProductRepositoryFake{} }

func (m *ProductRepositoryFake) GetActiveByPlanID(ctx context.Context, planID uint) ([]database.Product, error) {
	if m.GetActiveByPlanIDFunc != nil {
		return m.GetActiveByPlanIDFunc(ctx, planID)
	}
	return nil, nil
}
func (m *ProductRepositoryFake) GetProductByID(ctx context.Context, id uint) (*database.Product, error) {
	if m.GetProductByIDFunc != nil {
		return m.GetProductByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}

// --- OrderRepository ---

type OrderRepositoryFake struct {
	CreateOrderFunc               func(ctx context.Context, order *database.Order) error
	GetOrderByIDFunc              func(ctx context.Context, id uint) (*database.Order, error)
	GetOrdersBySubscriptionIDFunc func(ctx context.Context, subscriptionID uint) ([]database.Order, error)
	UpdateOrderStatusFunc         func(ctx context.Context, id uint, status database.OrderStatus) error
	UpdateOrderPaidStatusFunc     func(ctx context.Context, id uint) error
	UpdateOrderActivatedAtFunc    func(ctx context.Context, id uint, activatedAt, expiresAt time.Time) error
}

func NewOrderRepository() *OrderRepositoryFake { return &OrderRepositoryFake{} }

func (m *OrderRepositoryFake) CreateOrder(ctx context.Context, order *database.Order) error {
	if m.CreateOrderFunc != nil {
		return m.CreateOrderFunc(ctx, order)
	}
	return nil
}
func (m *OrderRepositoryFake) GetOrderByID(ctx context.Context, id uint) (*database.Order, error) {
	if m.GetOrderByIDFunc != nil {
		return m.GetOrderByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *OrderRepositoryFake) GetOrdersBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.Order, error) {
	if m.GetOrdersBySubscriptionIDFunc != nil {
		return m.GetOrdersBySubscriptionIDFunc(ctx, subscriptionID)
	}
	return nil, nil
}
func (m *OrderRepositoryFake) UpdateOrderStatus(ctx context.Context, id uint, status database.OrderStatus) error {
	if m.UpdateOrderStatusFunc != nil {
		return m.UpdateOrderStatusFunc(ctx, id, status)
	}
	return nil
}
func (m *OrderRepositoryFake) UpdateOrderPaidStatus(ctx context.Context, id uint) error {
	if m.UpdateOrderPaidStatusFunc != nil {
		return m.UpdateOrderPaidStatusFunc(ctx, id)
	}
	return nil
}
func (m *OrderRepositoryFake) UpdateOrderActivatedAt(ctx context.Context, id uint, activatedAt, expiresAt time.Time) error {
	if m.UpdateOrderActivatedAtFunc != nil {
		return m.UpdateOrderActivatedAtFunc(ctx, id, activatedAt, expiresAt)
	}
	return nil
}
