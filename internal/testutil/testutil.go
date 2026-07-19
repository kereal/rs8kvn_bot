package testutil

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

var ErrRecordNotFound = gorm.ErrRecordNotFound

func PtrString(v string) *string {
	return &v
}

func PtrInt64(v int64) *int64 {
	return &v
}

func PtrUint(v uint) *uint {
	return &v
}

func PtrTime(t time.Time) *time.Time {
	return &t
}

const (
	DefaultTelegramID = int64(123456789)
	DefaultUsername   = "testuser"
	DefaultTrafficGB  = 100
	AdminTelegramID   = int64(999999)
)

func InitLogger(t any) error {
	_, err := logger.Init("", "error")
	return err
}

func NewTestDatabaseService(t any) (*database.Service, error) {
	type testInterface interface {
		TempDir() string
	}

	var tmpDir string
	if ti, ok := t.(testInterface); ok {
		tmpDir = ti.TempDir()
	} else {
		tmpDir = "/tmp"
	}

	dbPath := filepath.Join(tmpDir, "test_service.db")
	return database.NewService(dbPath)
}

func NewDatabaseService() *DatabaseService {
	return &DatabaseService{
		Subscriptions: make(map[int64]*database.Subscription),
	}
}

type DatabaseService struct {
	mu                                          sync.RWMutex
	Subscriptions                               map[int64]*database.Subscription
	Products                                    map[uint]*database.Product
	Orders                                      map[uint]*database.Order
	OrdersBySubscriptionID                      map[uint][]database.Order
	PingFunc                                    func(ctx context.Context) error
	GetByTelegramIDFunc                         func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	CreateSubscriptionFunc                      func(ctx context.Context, sub *database.Subscription, inviteCode string) error
	UpdateSubscriptionFunc                      func(ctx context.Context, sub *database.Subscription) error
	DeleteSubscriptionFunc                      func(ctx context.Context, telegramID int64) error
	GetLatestSubscriptionsFunc                  func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetAllSubscriptionsFunc                     func(ctx context.Context) ([]database.Subscription, error)
	CountAllSubscriptionsFunc                   func(ctx context.Context) (int64, error)
	CountActiveSubscriptionsFunc                func(ctx context.Context) (int64, error)
	CountTrialSubscriptionsFunc                 func(ctx context.Context) (int64, error)
	CountExpiredSubscriptionsFunc               func(ctx context.Context) (int64, error)
	GetAllTelegramIDsFunc                       func(ctx context.Context) ([]int64, error)
	GetByIDFunc                                 func(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDByUsernameFunc                 func(ctx context.Context, username string) (int64, error)
	DeleteSubscriptionByIDFunc                  func(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDsBatchFunc                     func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc                 func(ctx context.Context) (int64, error)
	GetOrCreateInviteFunc                       func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetInviteByReferrerFunc                     func(ctx context.Context, referrerTGID int64) (*database.Invite, error)
	GetInviteByCodeFunc                         func(ctx context.Context, code string) (*database.Invite, error)
	GetReferralCountFunc                        func(ctx context.Context, referrerTGID int64) (int64, error)
	GetAllReferralCountsFunc                    func(ctx context.Context) (map[int64]int64, error)
	CreateTrialSubscriptionFunc                 func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error)
	ListNodesFunc                               func(ctx context.Context) ([]database.Node, error)
	GetNodesByPlanNameFunc                      func(ctx context.Context, planName string) ([]database.Node, error)
	GetPlansBySourceIDFunc                      func(ctx context.Context, sourceID uint) ([]database.Plan, error)
	GetPlanByNameFunc                           func(ctx context.Context, name string) (*database.Plan, error)
	GetPlanByIDFunc                             func(ctx context.Context, planID uint) (*database.Plan, error)
	GetAllPlansFunc                             func(ctx context.Context) ([]database.Plan, error)
	CreatePlanFunc                              func(ctx context.Context, plan *database.Plan) error
	UpdatePlanFunc                              func(ctx context.Context, plan *database.Plan) error
	DeletePlanFunc                              func(ctx context.Context, planID uint) error
	AddSourceToPlanFunc                         func(ctx context.Context, planID, sourceID uint) error
	RemoveSourceFromPlanFunc                    func(ctx context.Context, planID, sourceID uint) error
	SeedDefaultDataFunc                         func(ctx context.Context) error
	GetActiveByPlanIDFunc                       func(ctx context.Context, planID uint) ([]database.Product, error)
	GetProductByIDFunc                          func(ctx context.Context, id uint) (*database.Product, error)
	GetNodeByIDFunc                             func(ctx context.Context, id uint) (*database.Node, error)
	ListEnabledFunc                             func(ctx context.Context) ([]database.Node, error)
	GetNodesByPlanIDFunc                        func(ctx context.Context, planID uint) ([]database.Node, error)
	CreateSubscriptionNodeFunc                  func(ctx context.Context, sn *database.SubscriptionNode) error
	UpdateSubscriptionNodeStatusFunc            func(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error
	UpsertSubscriptionNodeFunc                  func(ctx context.Context, sn *database.SubscriptionNode) error
	DeleteSubscriptionNodeFunc                  func(ctx context.Context, subID, nodeID uint) error
	DeleteSubscriptionNodesBySubscriptionIDFunc func(ctx context.Context, subID uint) error
	MarkActiveNodesPendingUpdateFunc            func(ctx context.Context, subID uint, targetNodeIDs []uint) error
	UpdateRetryFunc                             func(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error
	GetBySubscriptionIDFunc                     func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetByNodeIDFunc                             func(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error)
	GetPendingSyncFunc                          func(ctx context.Context) ([]database.SubscriptionNode, error)
	GetPendingBySubscriptionIDFunc              func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetPendingByNodeIDFunc                      func(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error)
	CreateOrderFunc                             func(ctx context.Context, order *database.Order) error
	GetOrderByIDFunc                            func(ctx context.Context, id uint) (*database.Order, error)
	GetOrdersBySubscriptionIDFunc               func(ctx context.Context, subscriptionID uint) ([]database.Order, error)
	UpdateOrderStatusFunc                       func(ctx context.Context, id uint, status database.OrderStatus) error
	UpdateOrderPaidStatusFunc                   func(ctx context.Context, id uint) error
	UpdateOrderActivatedAtFunc                  func(ctx context.Context, id uint, activatedAt, expiresAt time.Time) error
	TransactionFunc                             func(ctx context.Context, fn func(*gorm.DB) error) error
	GetSubscriptionFunc                         func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	GetTrialSubscriptionBySubIDFunc             func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	BindTrialSubscriptionFunc                   func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CountTrialRequestsByIPLastHourFunc          func(ctx context.Context, ip string) (int, error)
	CreateTrialRequestFunc                      func(ctx context.Context, ip string) error
	CleanupExpiredTrialsFunc                    func(ctx context.Context, hours int) ([]database.Subscription, error)
	GetPoolStatsFunc                            func() (*database.PoolStats, error)
	GetWithPlanAndNodesFunc                     func(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error)
	GetSubscriptionStatusFunc                   func(ctx context.Context, subscriptionID string) (string, time.Time, error)
	UpdateDevicesFunc                           func(ctx context.Context, id uint, devicesJSON string) error
	UpdateIPsFunc                               func(ctx context.Context, id uint, ipsJSON string) error
	UpdateLastRequestFunc                       func(ctx context.Context, subscriptionID string) error
}

func (m *DatabaseService) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}
	return nil
}

func (m *DatabaseService) Transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	if m.TransactionFunc != nil {
		return m.TransactionFunc(ctx, fn)
	}
	return errors.New("mock transaction not configured")
}

func (m *DatabaseService) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
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

func (m *DatabaseService) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
	if m.GetByIDFunc != nil {
		return m.GetByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) CreateSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) error {
	if m.CreateSubscriptionFunc != nil {
		return m.CreateSubscriptionFunc(ctx, sub, inviteCode)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Subscriptions == nil {
		m.Subscriptions = make(map[int64]*database.Subscription)
	}
	if sub.TelegramID > 0 {
		stored := *sub
		m.Subscriptions[sub.TelegramID] = &stored
	}
	return nil
}

func (m *DatabaseService) CreateSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error {
	if m.CreateSubscriptionNodeFunc != nil {
		return m.CreateSubscriptionNodeFunc(ctx, sn)
	}
	return nil
}

func (m *DatabaseService) UpdateSubscriptionNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error {
	if m.UpdateSubscriptionNodeStatusFunc != nil {
		return m.UpdateSubscriptionNodeStatusFunc(ctx, subID, nodeID, status)
	}
	return nil
}

func (m *DatabaseService) UpsertSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error {
	if m.UpsertSubscriptionNodeFunc != nil {
		return m.UpsertSubscriptionNodeFunc(ctx, sn)
	}
	return nil
}

func (m *DatabaseService) DeleteSubscriptionNode(ctx context.Context, subID, nodeID uint) error {
	if m.DeleteSubscriptionNodeFunc != nil {
		return m.DeleteSubscriptionNodeFunc(ctx, subID, nodeID)
	}
	return nil
}

func (m *DatabaseService) DeleteSubscriptionNodesBySubscriptionID(ctx context.Context, subID uint) error {
	if m.DeleteSubscriptionNodesBySubscriptionIDFunc != nil {
		return m.DeleteSubscriptionNodesBySubscriptionIDFunc(ctx, subID)
	}
	return nil
}

func (m *DatabaseService) MarkActiveNodesPendingUpdate(ctx context.Context, subID uint, targetNodeIDs []uint) error {
	if m.MarkActiveNodesPendingUpdateFunc != nil {
		return m.MarkActiveNodesPendingUpdateFunc(ctx, subID, targetNodeIDs)
	}
	return nil
}

func (m *DatabaseService) UpdateRetry(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error {
	if m.UpdateRetryFunc != nil {
		return m.UpdateRetryFunc(ctx, subID, nodeID, retryCount, retryAt, lastErr)
	}
	return nil
}

func (m *DatabaseService) GetBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	if m.GetBySubscriptionIDFunc != nil {
		return m.GetBySubscriptionIDFunc(ctx, subscriptionID)
	}
	return nil, nil
}

func (m *DatabaseService) GetByNodeID(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error) {
	if m.GetByNodeIDFunc != nil {
		return m.GetByNodeIDFunc(ctx, nodeID)
	}
	return nil, nil
}

func (m *DatabaseService) GetPendingSync(ctx context.Context) ([]database.SubscriptionNode, error) {
	if m.GetPendingSyncFunc != nil {
		return m.GetPendingSyncFunc(ctx)
	}
	return nil, nil
}

func (m *DatabaseService) GetPendingBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
	if m.GetPendingBySubscriptionIDFunc != nil {
		return m.GetPendingBySubscriptionIDFunc(ctx, subscriptionID)
	}
	return nil, nil
}

func (m *DatabaseService) GetPendingByNodeID(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error) {
	if m.GetPendingByNodeIDFunc != nil {
		return m.GetPendingByNodeIDFunc(ctx, nodeID)
	}
	return nil, nil
}

func (m *DatabaseService) GetNodeByID(ctx context.Context, id uint) (*database.Node, error) {
	if m.GetNodeByIDFunc != nil {
		return m.GetNodeByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) GetNodesByPlanID(ctx context.Context, planID uint) ([]database.Node, error) {
	if m.GetNodesByPlanIDFunc != nil {
		return m.GetNodesByPlanIDFunc(ctx, planID)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) ListEnabled(ctx context.Context) ([]database.Node, error) {
	if m.ListEnabledFunc != nil {
		return m.ListEnabledFunc(ctx)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) GetProductByID(ctx context.Context, id uint) (*database.Product, error) {
	if m.GetProductByIDFunc != nil {
		return m.GetProductByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) UpdateSubscription(ctx context.Context, sub *database.Subscription) error {
	if m.UpdateSubscriptionFunc != nil {
		return m.UpdateSubscriptionFunc(ctx, sub)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Subscriptions == nil {
		m.Subscriptions = make(map[int64]*database.Subscription)
	}
	if sub.TelegramID > 0 {
		m.Subscriptions[sub.TelegramID] = sub
	}
	return nil
}

func (m *DatabaseService) DeleteSubscription(ctx context.Context, telegramID int64) error {
	if m.DeleteSubscriptionFunc != nil {
		return m.DeleteSubscriptionFunc(ctx, telegramID)
	}
	m.mu.Lock()
	delete(m.Subscriptions, telegramID)
	m.mu.Unlock()
	return nil
}

func (m *DatabaseService) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	if m.GetLatestSubscriptionsFunc != nil {
		return m.GetLatestSubscriptionsFunc(ctx, limit)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []database.Subscription
	for _, sub := range m.Subscriptions {
		if sub.Status == "active" {
			result = append(result, *sub)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *DatabaseService) GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error) {
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

func (m *DatabaseService) CountActiveSubscriptions(ctx context.Context) (int64, error) {
	if m.CountActiveSubscriptionsFunc != nil {
		return m.CountActiveSubscriptionsFunc(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var count int64
	for _, sub := range m.Subscriptions {
		if sub.Status == "active" && !sub.IsExpired() {
			count++
		}
	}
	return count, nil
}

func (m *DatabaseService) CountTrialSubscriptions(ctx context.Context) (int64, error) {
	if m.CountTrialSubscriptionsFunc != nil {
		return m.CountTrialSubscriptionsFunc(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var count int64
	for _, sub := range m.Subscriptions {
		if sub.TelegramID == 0 {
			count++
		}
	}
	return count, nil
}

func (m *DatabaseService) CountAllSubscriptions(ctx context.Context) (int64, error) {
	if m.CountAllSubscriptionsFunc != nil {
		return m.CountAllSubscriptionsFunc(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.Subscriptions)), nil
}

func (m *DatabaseService) CountExpiredSubscriptions(ctx context.Context) (int64, error) {
	if m.CountExpiredSubscriptionsFunc != nil {
		return m.CountExpiredSubscriptionsFunc(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var count int64
	for _, sub := range m.Subscriptions {
		if sub.Status == "active" && sub.IsExpired() {
			count++
		}
	}
	return count, nil
}

func (m *DatabaseService) GetAllTelegramIDs(ctx context.Context) ([]int64, error) {
	if m.GetAllTelegramIDsFunc != nil {
		return m.GetAllTelegramIDsFunc(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []int64
	for id := range m.Subscriptions {
		result = append(result, id)
	}
	return result, nil
}

func (m *DatabaseService) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	if m.GetTelegramIDByUsernameFunc != nil {
		return m.GetTelegramIDByUsernameFunc(ctx, username)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sub := range m.Subscriptions {
		if sub.Username == username {
			return sub.TelegramID, nil
		}
	}
	return 0, gorm.ErrRecordNotFound
}

func (m *DatabaseService) DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error) {
	if m.DeleteSubscriptionByIDFunc != nil {
		return m.DeleteSubscriptionByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	if m.GetTelegramIDsBatchFunc != nil {
		return m.GetTelegramIDsBatchFunc(ctx, offset, limit)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var ids []int64
	for id := range m.Subscriptions {
		ids = append(ids, id)
	}
	if offset >= len(ids) {
		return []int64{}, nil
	}
	end := offset + limit
	if end > len(ids) {
		end = len(ids)
	}
	return ids[offset:end], nil
}

func (m *DatabaseService) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	if m.GetTotalTelegramIDCountFunc != nil {
		return m.GetTotalTelegramIDCountFunc(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.Subscriptions)), nil
}

func (m *DatabaseService) Close() error {
	return nil
}

func (m *DatabaseService) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	if m.GetOrCreateInviteFunc != nil {
		return m.GetOrCreateInviteFunc(ctx, referrerTGID, code)
	}
	return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
}

func (m *DatabaseService) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	if m.GetInviteByCodeFunc != nil {
		return m.GetInviteByCodeFunc(ctx, code)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error) {
	if m.GetInviteByReferrerFunc != nil {
		return m.GetInviteByReferrerFunc(ctx, referrerTGID)
	}
	return nil, database.ErrInviteNotFound
}

func (m *DatabaseService) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
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

func (m *DatabaseService) ListNodes(ctx context.Context) ([]database.Node, error) {
	if m.ListNodesFunc != nil {
		return m.ListNodesFunc(ctx)
	}
	return []database.Node{
		{ID: 1, Name: "default", IsActive: true, Host: "http://localhost:2053", APIToken: "test-token", InboundIDs: `[1]`, SubscriptionURL: "http://example.com/sub/"},
	}, nil
}

func (m *DatabaseService) SeedDefaultData(ctx context.Context) error {
	if m.SeedDefaultDataFunc != nil {
		return m.SeedDefaultDataFunc(ctx)
	}
	return nil
}

func (m *DatabaseService) GetPlanByName(ctx context.Context, name string) (*database.Plan, error) {
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

func (m *DatabaseService) GetPlanByID(ctx context.Context, planID uint) (*database.Plan, error) {
	if m.GetPlanByIDFunc != nil {
		return m.GetPlanByIDFunc(ctx, planID)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) GetAllPlans(ctx context.Context) ([]database.Plan, error) {
	if m.GetAllPlansFunc != nil {
		return m.GetAllPlansFunc(ctx)
	}
	return nil, nil
}

func (m *DatabaseService) CreatePlan(ctx context.Context, plan *database.Plan) error {
	if m.CreatePlanFunc != nil {
		return m.CreatePlanFunc(ctx, plan)
	}
	return nil
}

func (m *DatabaseService) UpdatePlan(ctx context.Context, plan *database.Plan) error {
	if m.UpdatePlanFunc != nil {
		return m.UpdatePlanFunc(ctx, plan)
	}
	return nil
}

func (m *DatabaseService) DeletePlan(ctx context.Context, planID uint) error {
	if m.DeletePlanFunc != nil {
		return m.DeletePlanFunc(ctx, planID)
	}
	return nil
}

func (m *DatabaseService) GetNodesByPlanName(ctx context.Context, planName string) ([]database.Node, error) {
	if m.GetNodesByPlanNameFunc != nil {
		return m.GetNodesByPlanNameFunc(ctx, planName)
	}
	if planName == database.TrialPlanName {
		inboundIDs, _ := json.Marshal([]int{1})
		return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: string(inboundIDs)}}, nil
	}
	return nil, nil
}

func (m *DatabaseService) GetPlansBySourceID(ctx context.Context, sourceID uint) ([]database.Plan, error) {
	if m.GetPlansBySourceIDFunc != nil {
		return m.GetPlansBySourceIDFunc(ctx, sourceID)
	}
	return nil, nil
}

func (m *DatabaseService) AddSourceToPlan(ctx context.Context, planID, sourceID uint) error {
	if m.AddSourceToPlanFunc != nil {
		return m.AddSourceToPlanFunc(ctx, planID, sourceID)
	}
	return nil
}

func (m *DatabaseService) RemoveSourceFromPlan(ctx context.Context, planID, sourceID uint) error {
	if m.RemoveSourceFromPlanFunc != nil {
		return m.RemoveSourceFromPlanFunc(ctx, planID, sourceID)
	}
	return nil
}

func (m *DatabaseService) GetSubscription(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	if m.GetSubscriptionFunc != nil {
		return m.GetSubscriptionFunc(ctx, subscriptionID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sub := range m.Subscriptions {
		if sub.SubscriptionID == subscriptionID {
			return sub, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
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
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
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

func (m *DatabaseService) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	if m.CountTrialRequestsByIPLastHourFunc != nil {
		return m.CountTrialRequestsByIPLastHourFunc(ctx, ip)
	}
	return 0, nil
}

func (m *DatabaseService) CreateTrialRequest(ctx context.Context, ip string) error {
	if m.CreateTrialRequestFunc != nil {
		return m.CreateTrialRequestFunc(ctx, ip)
	}
	return nil
}

func (m *DatabaseService) CleanupExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error) {
	if m.CleanupExpiredTrialsFunc != nil {
		return m.CleanupExpiredTrialsFunc(ctx, hours)
	}
	return nil, nil
}

func (m *DatabaseService) GetPoolStats() (*database.PoolStats, error) {
	if m.GetPoolStatsFunc != nil {
		return m.GetPoolStatsFunc()
	}
	return &database.PoolStats{}, nil
}

func (m *DatabaseService) GetWithPlanAndNodes(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error) {
	if m.GetWithPlanAndNodesFunc != nil {
		return m.GetWithPlanAndNodesFunc(ctx, subscriptionID)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error) {
	if m.GetSubscriptionStatusFunc != nil {
		return m.GetSubscriptionStatusFunc(ctx, subscriptionID)
	}
	return "", time.Time{}, gorm.ErrRecordNotFound
}

func (m *DatabaseService) UpdateDevices(ctx context.Context, id uint, devicesJSON string) error {
	if m.UpdateDevicesFunc != nil {
		return m.UpdateDevicesFunc(ctx, id, devicesJSON)
	}
	return nil
}

func (m *DatabaseService) UpdateIPs(ctx context.Context, id uint, ipsJSON string) error {
	return nil
}

func (m *DatabaseService) UpdateLastRequest(ctx context.Context, subscriptionID string) error {
	if m.UpdateLastRequestFunc != nil {
		return m.UpdateLastRequestFunc(ctx, subscriptionID)
	}
	return nil
}

func (m *DatabaseService) ExpireSubscription(ctx context.Context, id uint, freePlanID uint) error {
	return nil
}

func (m *DatabaseService) GetExpiredPaidSubscriptions(ctx context.Context, now time.Time) ([]database.Subscription, error) {
	return nil, nil
}

func (m *DatabaseService) GetActiveByPlanID(ctx context.Context, planID uint) ([]database.Product, error) {
	if m.GetActiveByPlanIDFunc != nil {
		return m.GetActiveByPlanIDFunc(ctx, planID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, product := range m.Products {
		if product.PlanID == planID && product.IsActive {
			return []database.Product{*product}, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) CreateOrder(ctx context.Context, order *database.Order) error {
	if m.CreateOrderFunc != nil {
		return m.CreateOrderFunc(ctx, order)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Orders == nil {
		m.Orders = make(map[uint]*database.Order)
		m.OrdersBySubscriptionID = make(map[uint][]database.Order)
	}
	if order.ID == 0 {
		order.ID = uint(len(m.Orders) + 1) //nolint:gosec // test helper: map size is tiny, no overflow risk
	}
	stored := *order
	m.Orders[order.ID] = &stored
	m.OrdersBySubscriptionID[order.SubscriptionID] = append(m.OrdersBySubscriptionID[order.SubscriptionID], stored)
	return nil
}

func (m *DatabaseService) GetOrderByID(ctx context.Context, id uint) (*database.Order, error) {
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

func (m *DatabaseService) GetOrdersBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.Order, error) {
	if m.GetOrdersBySubscriptionIDFunc != nil {
		return m.GetOrdersBySubscriptionIDFunc(ctx, subscriptionID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if orders, ok := m.OrdersBySubscriptionID[subscriptionID]; ok {
		return orders, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *DatabaseService) UpdateOrderStatus(ctx context.Context, id uint, status database.OrderStatus) error {
	if m.UpdateOrderStatusFunc != nil {
		return m.UpdateOrderStatusFunc(ctx, id, status)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if order, ok := m.Orders[id]; ok {
		order.Status = status
		return nil
	}
	return gorm.ErrRecordNotFound
}

func (m *DatabaseService) UpdateOrderPaidStatus(ctx context.Context, id uint) error {
	if m.UpdateOrderPaidStatusFunc != nil {
		return m.UpdateOrderPaidStatusFunc(ctx, id)
	}
	return nil
}

func (m *DatabaseService) UpdateOrderActivatedAt(ctx context.Context, id uint, activatedAt, expiresAt time.Time) error {
	if m.UpdateOrderActivatedAtFunc != nil {
		return m.UpdateOrderActivatedAtFunc(ctx, id, activatedAt, expiresAt)
	}
	return nil
}

func (m *DatabaseService) GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error) {
	if m.GetReferralCountFunc != nil {
		return m.GetReferralCountFunc(ctx, referrerTGID)
	}
	return 0, nil
}

func (m *DatabaseService) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	if m.GetAllReferralCountsFunc != nil {
		return m.GetAllReferralCountsFunc(ctx)
	}
	return make(map[int64]int64), nil
}

func CreateTestSubscription(telegramID int64, username string, status string, expiry *time.Time) *database.Subscription {
	return &database.Subscription{
		TelegramID:     telegramID,
		Username:       username,
		ClientID:       "test-client-id-" + username,
		SubscriptionID: username,
		ExpiresAt:      expiry,
		Status:         status,
	}
}

type XUIClient struct {
	mu                      sync.Mutex
	PingFunc                func(ctx context.Context) error
	AddClientFunc           func(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithIDFunc     func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error)
	UpdateClientFunc        func(ctx context.Context, req xui.ClientRequest) error
	DeleteClientFunc        func(ctx context.Context, email string) error
	GetClientTrafficFunc    func(ctx context.Context, email string) (*xui.ClientTraffic, error)
	GetSubscriptionLinkFunc func(host, subID, subPath string) string
	GetExternalURLFunc      func(host string) string

	// Call tracking
	AddClientCalled       bool
	AddClientWithIDCalled bool
	DeleteClientCalled    bool
	UpdateClientCalled    bool
}

func (m *XUIClient) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}
	return nil
}

func (m *XUIClient) AddClient(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	m.mu.Lock()
	m.AddClientCalled = true
	m.mu.Unlock()
	if m.AddClientFunc != nil {
		return m.AddClientFunc(ctx, inboundIDs, email, trafficBytes, expiryTime)
	}
	return &xui.ClientConfig{
		ID:        "test-client-id",
		Email:     email,
		TotalGB:   trafficBytes,
		ExpiresAt: expiryTime.UnixMilli(),
		Enable:    true,
	}, nil
}

func (m *XUIClient) AddClientWithID(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
	m.mu.Lock()
	m.AddClientWithIDCalled = true
	m.mu.Unlock()
	if m.AddClientWithIDFunc != nil {
		return m.AddClientWithIDFunc(ctx, req)
	}
	return &xui.ClientConfig{
		ID:        req.ClientID,
		Email:     req.Email,
		TotalGB:   req.TrafficBytes,
		ExpiresAt: req.ExpiryTime.UnixMilli(),
		Enable:    true,
		SubID:     req.SubID,
	}, nil
}

func (m *XUIClient) UpdateClient(ctx context.Context, req xui.ClientRequest) error {
	m.mu.Lock()
	m.UpdateClientCalled = true
	m.mu.Unlock()
	if m.UpdateClientFunc != nil {
		return m.UpdateClientFunc(ctx, req)
	}
	return nil
}

func (m *XUIClient) DeleteClient(ctx context.Context, email string) error {
	m.mu.Lock()
	m.DeleteClientCalled = true
	m.mu.Unlock()
	if m.DeleteClientFunc != nil {
		return m.DeleteClientFunc(ctx, email)
	}
	return nil
}

func (m *XUIClient) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
	if m.GetClientTrafficFunc != nil {
		return m.GetClientTrafficFunc(ctx, email)
	}
	return &xui.ClientTraffic{
		Up:   1024 * 1024 * 100,
		Down: 1024 * 1024 * 200,
	}, nil
}

func (m *XUIClient) GetSubscriptionLink(host, subID, subPath string) string {
	if m.GetSubscriptionLinkFunc != nil {
		return m.GetSubscriptionLinkFunc(host, subID, subPath)
	}
	return host + "/" + subPath + "/" + subID
}

func (m *XUIClient) GetExternalURL(host string) string {
	if m.GetExternalURLFunc != nil {
		return m.GetExternalURLFunc(host)
	}
	return host
}

func (m *XUIClient) Close() error {
	return nil
}

func NewXUIClient() *XUIClient {
	return &XUIClient{}
}

// BotAPI is a mock implementation of the Telegram Bot API for testing.
type BotAPI struct {
	mu              sync.RWMutex
	sendCalled      bool
	requestCalled   bool
	LastSentText    string
	LastChatID      int64
	SendCount       int
	SendError       error
	RequestError    error
	LastChattable   tgbotapi.Chattable
	SendFunc        func(c tgbotapi.Chattable) (tgbotapi.Message, error)
	AllSentMessages []SentMessage
	// DeletedMessageIDs captures the message ids passed to DeleteMessage.
	DeletedMessageIDs []int
}

// SentMessage represents a captured message
type SentMessage struct {
	ChatID int64
	Text   string
}

func NewBotAPI() *BotAPI {
	return &BotAPI{}
}

func (m *BotAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCalled = true
	m.SendCount++
	m.LastChattable = c

	var msg SentMessage

	// Extract text and chat ID from various message types
	switch v := c.(type) {
	case tgbotapi.MessageConfig:
		m.LastSentText = v.Text
		m.LastChatID = v.ChatID
		msg = SentMessage{ChatID: v.ChatID, Text: v.Text}
	case tgbotapi.EditMessageTextConfig:
		m.LastSentText = v.Text
		m.LastChatID = v.ChatID
		msg = SentMessage{ChatID: v.ChatID, Text: v.Text}
	case tgbotapi.EditMessageReplyMarkupConfig:
		m.LastChatID = v.ChatID
	case tgbotapi.DeleteMessageConfig:
		m.LastChatID = v.ChatID
	}

	m.AllSentMessages = append(m.AllSentMessages, msg)

	// Use custom send function if provided
	if m.SendFunc != nil {
		return m.SendFunc(c)
	}

	if m.SendError != nil {
		return tgbotapi.Message{}, m.SendError
	}
	return tgbotapi.Message{MessageID: 1}, nil
}

func (m *BotAPI) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestCalled = true

	if dm, ok := c.(tgbotapi.DeleteMessageConfig); ok {
		m.DeletedMessageIDs = append(m.DeletedMessageIDs, dm.MessageID)
	}

	if m.RequestError != nil {
		return nil, m.RequestError
	}
	return &tgbotapi.APIResponse{Ok: true}, nil
}

// SendCountSafe returns the number of Send calls (thread-safe).
func (m *BotAPI) SendCountSafe() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.SendCount
}

// SendCalledSafe returns whether Send was called (thread-safe).
func (m *BotAPI) SendCalledSafe() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sendCalled
}

// RequestCalledSafe returns whether Request was called (thread-safe).
func (m *BotAPI) RequestCalledSafe() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.requestCalled
}

// DeletedMessageIDsSafe returns the captured DeleteMessage ids (thread-safe).
func (m *BotAPI) DeletedMessageIDsSafe() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]int, len(m.DeletedMessageIDs))
	copy(out, m.DeletedMessageIDs)
	return out
}

// LastSentTextSafe returns the last sent text (thread-safe).
func (m *BotAPI) LastSentTextSafe() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.LastSentText
}

// LastChattableSafe returns the last sent Chattable (thread-safe).
func (m *BotAPI) LastChattableSafe() tgbotapi.Chattable {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.LastChattable
}

// GetAllSentMessages returns all captured messages (thread-safe).
func (m *BotAPI) GetAllSentMessages() []SentMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SentMessage, len(m.AllSentMessages))
	copy(out, m.AllSentMessages)
	return out
}

// SetSendCalled sets the sendCalled flag (thread-safe).
func (m *BotAPI) SetSendCalled(b bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCalled = b
}

// SetRequestCalled sets the requestCalled flag (thread-safe).
func (m *BotAPI) SetRequestCalled(b bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestCalled = b
}

// SetSendCount sets the send count (thread-safe).
func (m *BotAPI) SetSendCount(c int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SendCount = c
}

func (m *BotAPI) GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	ch := make(chan tgbotapi.Update)
	close(ch)
	return ch
}

func (m *BotAPI) StopReceivingUpdates() {
	// No-op for mock
}

func (m *BotAPI) Self() *tgbotapi.User {
	return &tgbotapi.User{
		ID:                      123456789,
		FirstName:               "TestBot",
		UserName:                "testbot",
		IsBot:                   true,
		CanJoinGroups:           false,
		CanReadAllGroupMessages: false,
		SupportsInlineQueries:   false,
	}
}

// Setenv sets an environment variable and returns a cleanup function.
func Setenv(t testing.TB, key, value string) func() {
	t.Helper()
	prev, ok := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("failed to set env %s: %v", key, err)
	}
	return func() {
		if ok {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	}
}
