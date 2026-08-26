// Package testutil provides reusable fakes and fixtures for repository and bot tests.
package testutil

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
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
		Subscriptions:     make(map[int64]*database.Subscription),
		SubscriptionsByID: make(map[uint]*database.Subscription),
		Broadcasts:        make(map[uint]*database.Broadcast),
	}
}

type DatabaseService struct {
	mu                sync.RWMutex
	Subscriptions     map[int64]*database.Subscription
	SubscriptionsByID map[uint]*database.Subscription
	Products          map[uint]*database.Product
	Orders            map[uint]*database.Order
	Broadcasts        map[uint]*database.Broadcast

	PingFunc                                    func(ctx context.Context) error
	GetByTelegramIDFunc                         func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	GetAnyByTelegramIDFunc                      func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	CreateSubscriptionFunc                      func(ctx context.Context, sub *database.Subscription, inviteCode string) error
	UpdateSubscriptionFunc                      func(ctx context.Context, sub *database.Subscription) error
	GetLatestSubscriptionsFunc                  func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetAllSubscriptionsFunc                     func(ctx context.Context) ([]database.Subscription, error)
	CountAllSubscriptionsFunc                   func(ctx context.Context) (int64, error)
	CountActiveSubscriptionsFunc                func(ctx context.Context) (int64, error)
	CountTrialSubscriptionsFunc                 func(ctx context.Context) (int64, error)
	GetByIDFunc                                 func(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDByUsernameFunc                 func(ctx context.Context, username string) (int64, error)
	DeleteSubscriptionByIDFunc                  func(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDsBatchFunc                     func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc                 func(ctx context.Context) (int64, error)
	GetFilteredTelegramIDCountFunc              func(ctx context.Context, filter database.BroadcastFilter) (int64, error)
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
	ListActiveProductsFunc                      func(ctx context.Context) ([]database.Product, error)
	GetProductByIDFunc                          func(ctx context.Context, id uint) (*database.Product, error)
	UpdateProductGuardedFunc                    func(ctx context.Context, product *database.Product) error
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
	GetPendingSyncFunc                          func(ctx context.Context) ([]database.SubscriptionNode, error)
	GetPendingBySubscriptionIDFunc              func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetOrderByIDFunc                            func(ctx context.Context, id uint) (*database.Order, error)
	GetOrderByProviderPaymentIDFunc             func(ctx context.Context, provider string, providerPaymentID uuid.UUID) (*database.Order, error)
	FindPendingPaymentOrderFunc                 func(ctx context.Context, subscriptionID, productID uint, now time.Time) (*database.Order, error)
	FindOrCreatePendingPaymentOrderFunc         func(ctx context.Context, subscriptionID, productID uint, amountCents int64, currency string, now time.Time) (*database.Order, error)
	MarkPaymentCreationUncertainFunc            func(ctx context.Context, orderID uint, uncertain bool) (bool, error)
	SavePaymentDetailsFunc                      func(ctx context.Context, orderID uint, providerPaymentID uuid.UUID, paymentURL string, paymentExpiresAt time.Time) error
	SaveOrderPaymentAmountsFunc                 func(ctx context.Context, orderID uint, callbackAmountCents int64, providerFeeCents *int64) error
	GetPaidOrdersWithoutProviderFeeFunc         func(ctx context.Context, limit int) ([]database.Order, error)
	GetPaidOrdersWithoutProviderFeeAfterIDFunc  func(ctx context.Context, afterID uint, limit int) ([]database.Order, error)
	ConfirmOrderPaidCASFunc                     func(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *database.Subscription, product *database.Product, applyPlan database.ApplyPlanInTxFn, callbackAmountCents int64) (bool, error)
	CancelOrderCASFunc                          func(ctx context.Context, provider string, providerPaymentID uuid.UUID, fromStatuses []database.OrderStatus) (bool, error)
	CancelPaidOrderAndDowngradeCASFunc          func(ctx context.Context, provider string, providerPaymentID uuid.UUID, now time.Time, freePlanID uint, applyPlan database.ChargebackPlanInTxFn) (*database.ChargebackResult, error)
	TransactionFunc                             func(ctx context.Context, fn func(*gorm.DB) error) error
	GetSubscriptionFunc                         func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	GetTrialSubscriptionBySubIDFunc             func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	BindTrialSubscriptionFunc                   func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CountTrialRequestsByIPLastHourFunc          func(ctx context.Context, ip string) (int, error)
	CreateTrialRequestFunc                      func(ctx context.Context, ip string) error
	CleanupExpiredTrialsFunc                    func(ctx context.Context, hours int) ([]database.Subscription, error)
	ClaimExpiredTrialsFunc                      func(ctx context.Context, hours int) ([]database.Subscription, error)
	DeleteClaimedTrialFunc                      func(ctx context.Context, id uint) error
	GetPoolStatsFunc                            func() (*database.PoolStats, error)
	GetWithPlanAndNodesFunc                     func(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error)
	GetSubscriptionStatusFunc                   func(ctx context.Context, subscriptionID string) (string, time.Time, error)
	UpdateDevicesFunc                           func(ctx context.Context, id uint, devicesJSON string) error
	UpdateIPsFunc                               func(ctx context.Context, id uint, ipsJSON string) error
	UpdateLastRequestFunc                       func(ctx context.Context, subscriptionID string) error

	GetSubscriptionsExpiringInRangeFunc func(ctx context.Context, from, to time.Time) ([]database.Subscription, error)
	ClaimReminderFunc                   func(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error)
	ReleaseReminderFunc                 func(ctx context.Context, id uint, bit int, expiresAt time.Time) error

	CreateBroadcastFunc                 func(ctx context.Context, b *database.Broadcast) error
	GetBroadcastFunc                    func(ctx context.Context, id uint) (*database.Broadcast, error)
	ListBroadcastsFunc                  func(ctx context.Context, limit int) ([]database.Broadcast, error)
	UpdateBroadcastFunc                 func(ctx context.Context, b *database.Broadcast) error
	SnapshotBroadcastRecipientsFunc     func(ctx context.Context, broadcastID uint, filter database.BroadcastFilter) (int64, error)
	ClaimBroadcastFunc                  func(ctx context.Context, id uint, now time.Time) (bool, error)
	CancelBroadcastFunc                 func(ctx context.Context, id uint, now time.Time) (bool, error)
	GetRunnableBroadcastsFunc           func(ctx context.Context, now time.Time) ([]database.Broadcast, error)
	RecoverStaleBroadcastRecipientsFunc func(ctx context.Context, broadcastID uint, before time.Time) error
	ClaimBroadcastRecipientsFunc        func(ctx context.Context, broadcastID uint, now time.Time, limit int) ([]database.BroadcastRecipient, error)
	FinishBroadcastRecipientFunc        func(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, status database.BroadcastRecipientStatus, lastError string, now time.Time) error
	UpdateBroadcastRecipientProgressFunc func(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, nextChunk int, now time.Time) error
	ResetBroadcastFailedRecipientsFunc  func(ctx context.Context, id uint, now time.Time) error
	GetBroadcastRecipientsStatsFunc     func(ctx context.Context, broadcastID uint) (total, sent, blocked, unreachable, failed int64, report database.BroadcastDeliveryReport, err error)
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

func (m *DatabaseService) GetAnyByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
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

func (m *DatabaseService) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
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

func (m *DatabaseService) CreateSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) error {
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

func (m *DatabaseService) UpdateProductGuarded(ctx context.Context, product *database.Product) error {
	if m.UpdateProductGuardedFunc != nil {
		return m.UpdateProductGuardedFunc(ctx, product)
	}

	return nil
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

func (m *DatabaseService) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	if m.GetLatestSubscriptionsFunc != nil {
		return m.GetLatestSubscriptionsFunc(ctx, limit)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []database.Subscription

	for _, sub := range m.Subscriptions {
		if sub.Status == string(database.SubscriptionStatusActive) {
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
		if sub.Status == string(database.SubscriptionStatusActive) && !sub.IsExpired() {
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
		if sub.TelegramID < 0 {
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

	end := min(offset+limit, len(ids))

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

func (m *DatabaseService) GetFilteredTelegramIDCount(ctx context.Context, filter database.BroadcastFilter) (int64, error) {
	if m.GetFilteredTelegramIDCountFunc != nil {
		return m.GetFilteredTelegramIDCountFunc(ctx, filter)
	}

	return m.GetTotalTelegramIDCount(ctx)
}

func (m *DatabaseService) CreateBroadcast(ctx context.Context, b *database.Broadcast) error {
	if m.CreateBroadcastFunc != nil {
		return m.CreateBroadcastFunc(ctx, b)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Broadcasts == nil {
		m.Broadcasts = make(map[uint]*database.Broadcast)
	}

	if b.ID == 0 {
		// #nosec G115 -- map length is non-negative
		b.ID = uint(len(m.Broadcasts) + 1)
	}

	stored := *b
	m.Broadcasts[b.ID] = &stored

	return nil
}

func (m *DatabaseService) GetBroadcast(ctx context.Context, id uint) (*database.Broadcast, error) {
	if m.GetBroadcastFunc != nil {
		return m.GetBroadcastFunc(ctx, id)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if b, ok := m.Broadcasts[id]; ok {
		copy := *b
		return &copy, nil
	}

	return nil, database.ErrBroadcastNotFound
}

func (m *DatabaseService) ListBroadcasts(ctx context.Context, limit int) ([]database.Broadcast, error) {
	if m.ListBroadcastsFunc != nil {
		return m.ListBroadcastsFunc(ctx, limit)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]uint, 0, len(m.Broadcasts))
	for id := range m.Broadcasts {
		ids = append(ids, id)
	}
	// Same order as production: newest first.
	slices.SortFunc(ids, func(a, b uint) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		default:
			return 0
		}
	})

	// Negative limit means "no limit", matching the production GORM query
	// (LIMIT is omitted when limit < 0, returning all broadcasts).
	capacity := len(ids)
	if limit >= 0 && limit < capacity {
		capacity = limit
	}
	out := make([]database.Broadcast, 0, capacity)
	for _, id := range ids {
		if limit >= 0 && len(out) >= limit {
			break
		}
		out = append(out, *m.Broadcasts[id])
	}

	return out, nil
}

func (m *DatabaseService) UpdateBroadcast(ctx context.Context, b *database.Broadcast) error {
	if m.UpdateBroadcastFunc != nil {
		return m.UpdateBroadcastFunc(ctx, b)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.Broadcasts[b.ID]
	if !ok {
		return database.ErrBroadcastNotFound
	}

	// Merge semantics mirror production: only mutable lifecycle/stats fields
	// are updated; name/filters/message_text/planned_at/started_at are never
	// overwritten — started_at is owned exclusively by ClaimBroadcast.
	existing.Status = b.Status
	existing.FinishedAt = b.FinishedAt
	existing.RecipientsTotal = b.RecipientsTotal
	existing.SentCount = b.SentCount
	existing.BlockedCount = b.BlockedCount
	existing.UnreachableCount = b.UnreachableCount
	existing.FailedCount = b.FailedCount
	existing.LastError = b.LastError
	existing.RetryAt = b.RetryAt
	existing.RetryCount = b.RetryCount
	existing.DeliveryReport = b.DeliveryReport
	if b.RecipientsState != "" {
		existing.RecipientsState = b.RecipientsState
	}
	existing.UpdatedAt = b.UpdatedAt

	return nil
}

type fakeBroadcastRecipientState struct {
	Snapshot   bool                          `json:"snapshot"`
	Recipients []database.BroadcastRecipient `json:"recipients"`
}

func fakeBroadcastState(b *database.Broadcast) (fakeBroadcastRecipientState, error) {
	state := fakeBroadcastRecipientState{Recipients: []database.BroadcastRecipient{}}
	if b.RecipientsState == "" || b.RecipientsState == "{}" {
		return state, nil
	}
	err := json.Unmarshal([]byte(b.RecipientsState), &state)
	if err != nil {
		return state, err
	}
	if state.Recipients == nil {
		state.Recipients = []database.BroadcastRecipient{}
	}
	for i := range state.Recipients {
		state.Recipients[i].BroadcastID = b.ID
	}
	return state, nil
}

func setFakeBroadcastState(b *database.Broadcast, state fakeBroadcastRecipientState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	b.RecipientsState = string(data)
	return nil
}

func (m *DatabaseService) SnapshotBroadcastRecipients(ctx context.Context, broadcastID uint, filter database.BroadcastFilter) (int64, error) {
	if m.SnapshotBroadcastRecipientsFunc != nil {
		return m.SnapshotBroadcastRecipientsFunc(ctx, broadcastID, filter)
	}
	// Default fallback: unfiltered audience. Tests that exercise filters set
	// SnapshotBroadcastRecipientsFunc instead.
	ids, err := m.GetTelegramIDsBatch(ctx, 0, int(^uint(0)>>1))
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[broadcastID]
	if !ok {
		return 0, database.ErrBroadcastNotFound
	}
	state, err := fakeBroadcastState(b)
	if err != nil {
		return 0, err
	}
	if state.Snapshot {
		return int64(len(state.Recipients)), nil
	}
	state.Snapshot = true
	now := time.Now().UTC()
	for i, telegramID := range ids {
		state.Recipients = append(state.Recipients, database.BroadcastRecipient{ID: uint(i + 1), BroadcastID: broadcastID, TelegramID: telegramID, Status: database.BroadcastRecipientPending, UpdatedAt: now})
	}
	err = setFakeBroadcastState(b, state)
	if err != nil {
		return 0, err
	}
	return int64(len(state.Recipients)), nil
}

func (m *DatabaseService) ClaimBroadcast(ctx context.Context, id uint, now time.Time) (bool, error) {
	if m.ClaimBroadcastFunc != nil {
		return m.ClaimBroadcastFunc(ctx, id, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[id]
	if !ok {
		return false, database.ErrBroadcastNotFound
	}
	if b.Status != string(database.BroadcastStatusScheduled) {
		return false, nil
	}
	b.Status = string(database.BroadcastStatusRunning)
	b.StartedAt = &now
	return true, nil
}

func (m *DatabaseService) CancelBroadcast(ctx context.Context, id uint, now time.Time) (bool, error) {
	if m.CancelBroadcastFunc != nil {
		return m.CancelBroadcastFunc(ctx, id, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[id]
	if !ok {
		return false, database.ErrBroadcastNotFound
	}
	if b.Status != string(database.BroadcastStatusScheduled) && b.Status != string(database.BroadcastStatusRunning) {
		return false, nil
	}
	// Mirrors production: sending leases are left in place so in-flight
	// deliveries can still record their terminal outcome.
	b.Status = string(database.BroadcastStatusCanceled)
	b.FinishedAt = &now
	return true, nil
}

func (m *DatabaseService) GetRunnableBroadcasts(ctx context.Context, now time.Time) ([]database.Broadcast, error) {
	if m.GetRunnableBroadcastsFunc != nil {
		return m.GetRunnableBroadcastsFunc(ctx, now)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]database.Broadcast, 0)
	for _, b := range m.Broadcasts {
		if (b.Status == string(database.BroadcastStatusScheduled) || b.Status == string(database.BroadcastStatusRunning)) &&
			(b.RetryAt == nil || !b.RetryAt.After(now)) &&
			(b.PlannedAt == nil || !b.PlannedAt.After(now)) {
			out = append(out, *b)
		}
	}
	return out, nil
}

func (m *DatabaseService) RecoverStaleBroadcastRecipients(ctx context.Context, broadcastID uint, before time.Time) error {
	if m.RecoverStaleBroadcastRecipientsFunc != nil {
		return m.RecoverStaleBroadcastRecipientsFunc(ctx, broadcastID, before)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[broadcastID]
	if !ok {
		return database.ErrBroadcastNotFound
	}
	state, err := fakeBroadcastState(b)
	if err != nil {
		return err
	}
	for i := range state.Recipients {
		if state.Recipients[i].Status == database.BroadcastRecipientSending && state.Recipients[i].UpdatedAt.Before(before) {
			state.Recipients[i].Status = database.BroadcastRecipientPending
			state.Recipients[i].UpdatedAt = time.Now().UTC()
		}
	}
	return setFakeBroadcastState(b, state)
}

func (m *DatabaseService) ClaimBroadcastRecipients(ctx context.Context, broadcastID uint, now time.Time, limit int) ([]database.BroadcastRecipient, error) {
	if m.ClaimBroadcastRecipientsFunc != nil {
		return m.ClaimBroadcastRecipientsFunc(ctx, broadcastID, now, limit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[broadcastID]
	if !ok {
		return nil, database.ErrBroadcastNotFound
	}
	state, err := fakeBroadcastState(b)
	if err != nil {
		return nil, err
	}
	out := make([]database.BroadcastRecipient, 0, limit)
	for i := range state.Recipients {
		if state.Recipients[i].Status == database.BroadcastRecipientPending && len(out) < limit {
			state.Recipients[i].Status = database.BroadcastRecipientSending
			state.Recipients[i].Attempts++
			state.Recipients[i].UpdatedAt = now
			out = append(out, state.Recipients[i])
		}
	}
	err = setFakeBroadcastState(b, state)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (m *DatabaseService) FinishBroadcastRecipient(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, status database.BroadcastRecipientStatus, lastError string, now time.Time) error {
	if m.FinishBroadcastRecipientFunc != nil {
		return m.FinishBroadcastRecipientFunc(ctx, broadcastID, id, expectedAttempts, status, lastError, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[broadcastID]
	if !ok {
		return database.ErrBroadcastNotFound
	}
	state, err := fakeBroadcastState(b)
	if err != nil {
		return err
	}
	for i := range state.Recipients {
		if state.Recipients[i].ID == id {
			if state.Recipients[i].Status != database.BroadcastRecipientSending || state.Recipients[i].Attempts != expectedAttempts {
				return database.ErrBroadcastRecipientStale
			}
			state.Recipients[i].Status, state.Recipients[i].LastError, state.Recipients[i].UpdatedAt = status, lastError, now
			return setFakeBroadcastState(b, state)
		}
	}
	return database.ErrBroadcastRecipientNotFound
}

func (m *DatabaseService) UpdateBroadcastRecipientProgress(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, nextChunk int, now time.Time) error {
	if m.UpdateBroadcastRecipientProgressFunc != nil {
		return m.UpdateBroadcastRecipientProgressFunc(ctx, broadcastID, id, expectedAttempts, nextChunk, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[broadcastID]
	if !ok {
		return database.ErrBroadcastNotFound
	}
	state, err := fakeBroadcastState(b)
	if err != nil {
		return err
	}
	for i := range state.Recipients {
		if state.Recipients[i].ID == id {
			if state.Recipients[i].Status != database.BroadcastRecipientSending || state.Recipients[i].Attempts != expectedAttempts {
				return database.ErrBroadcastRecipientStale
			}
			state.Recipients[i].NextChunk = nextChunk
			state.Recipients[i].UpdatedAt = now
			return setFakeBroadcastState(b, state)
		}
	}
	return database.ErrBroadcastRecipientNotFound
}

func (m *DatabaseService) ResetBroadcastFailedRecipients(ctx context.Context, id uint, now time.Time) error {
	if m.ResetBroadcastFailedRecipientsFunc != nil {
		return m.ResetBroadcastFailedRecipientsFunc(ctx, id, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Broadcasts[id]
	if !ok {
		return database.ErrBroadcastNotFound
	}
	state, err := fakeBroadcastState(b)
	if err != nil {
		return err
	}
	for i := range state.Recipients {
		if state.Recipients[i].Status == database.BroadcastRecipientFailed || state.Recipients[i].Status == database.BroadcastRecipientSending {
			state.Recipients[i].Status, state.Recipients[i].LastError, state.Recipients[i].UpdatedAt = database.BroadcastRecipientPending, "", now
		}
	}
	b.LastError = ""
	b.RetryAt = nil
	b.RetryCount = 0
	if b.Status == string(database.BroadcastStatusCompleted) || b.Status == string(database.BroadcastStatusFailed) || b.Status == string(database.BroadcastStatusCanceled) {
		b.Status = string(database.BroadcastStatusRunning)
		b.FinishedAt = nil
	}
	return setFakeBroadcastState(b, state)
}

func (m *DatabaseService) GetBroadcastRecipientsStats(ctx context.Context, broadcastID uint) (total, sent, blocked, unreachable, failed int64, report database.BroadcastDeliveryReport, err error) {
	if m.GetBroadcastRecipientsStatsFunc != nil {
		return m.GetBroadcastRecipientsStatsFunc(ctx, broadcastID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.Broadcasts[broadcastID]
	if !ok {
		return 0, 0, 0, 0, 0, report, database.ErrBroadcastNotFound
	}
	state, err := fakeBroadcastState(b)
	if err != nil {
		return 0, 0, 0, 0, 0, report, err
	}
	for _, recipient := range state.Recipients {
		total++
		switch recipient.Status {
		case database.BroadcastRecipientSent:
			sent++
			report.Delivered = append(report.Delivered, recipient.TelegramID)
		case database.BroadcastRecipientBlocked:
			blocked++
			report.Blocked = append(report.Blocked, recipient.TelegramID)
		case database.BroadcastRecipientUnreachable:
			unreachable++
			report.Unreachable = append(report.Unreachable, recipient.TelegramID)
		case database.BroadcastRecipientFailed:
			failed++
			report.Errors = append(report.Errors, database.BroadcastSendError{TelegramID: recipient.TelegramID, Error: recipient.LastError})
		case database.BroadcastRecipientPending, database.BroadcastRecipientSending:
			report.NotProcessed = append(report.NotProcessed, recipient.TelegramID)
		}
	}
	return total, sent, blocked, unreachable, failed, report, nil
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
		inboundIDs, err := json.Marshal([]int{1})
		if err != nil {
			return nil, err
		}

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

	// Lightweight command-handler tests often configure only the bind seam. A
	// synthetic trial keeps that fake compatible with the service's pre-bind
	// referral validation while preserving the normal not-found default.
	if m.BindTrialSubscriptionFunc != nil {
		inviteCode := subscriptionID
		return &database.Subscription{SubscriptionID: subscriptionID, InviteCode: &inviteCode, PlanID: trialPlan.ID}, nil
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

func (m *DatabaseService) ClaimExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error) {
	if m.ClaimExpiredTrialsFunc != nil {
		return m.ClaimExpiredTrialsFunc(ctx, hours)
	}
	if m.CleanupExpiredTrialsFunc != nil {
		return m.CleanupExpiredTrialsFunc(ctx, hours)
	}

	return nil, nil
}

func (m *DatabaseService) DeleteClaimedTrial(ctx context.Context, id uint) error {
	if m.DeleteClaimedTrialFunc != nil {
		return m.DeleteClaimedTrialFunc(ctx, id)
	}

	return nil
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
	if m.UpdateIPsFunc != nil {
		return m.UpdateIPsFunc(ctx, id, ipsJSON)
	}

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

func (m *DatabaseService) GetSubscriptionsExpiringInRange(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
	if m.GetSubscriptionsExpiringInRangeFunc != nil {
		return m.GetSubscriptionsExpiringInRangeFunc(ctx, from, to)
	}

	return nil, nil
}

// ClaimReminder atomically claims a reminder bit in the stateful fake.
func (m *DatabaseService) ClaimReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error) {
	if m.ClaimReminderFunc != nil {
		return m.ClaimReminderFunc(ctx, id, bit, expiresAt)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.SubscriptionsByID[id]
	if !ok {
		return false, gorm.ErrRecordNotFound
	}

	if sub.ExpiresAt == nil || !sub.ExpiresAt.Equal(expiresAt) || sub.RemindersSent&bit != 0 {
		return false, nil
	}

	sub.RemindersSent |= bit
	if sub.TelegramID > 0 {
		if current, exists := m.Subscriptions[sub.TelegramID]; exists {
			current.RemindersSent = sub.RemindersSent
		}
	}

	return true, nil
}

// ReleaseReminder releases a reminder claim after a failed send.
func (m *DatabaseService) ReleaseReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) error {
	if m.ReleaseReminderFunc != nil {
		return m.ReleaseReminderFunc(ctx, id, bit, expiresAt)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.SubscriptionsByID[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}

	if sub.ExpiresAt == nil || !sub.ExpiresAt.Equal(expiresAt) {
		return nil
	}

	sub.RemindersSent &^= bit
	if sub.TelegramID > 0 {
		if current, exists := m.Subscriptions[sub.TelegramID]; exists {
			current.RemindersSent = sub.RemindersSent
		}
	}

	return nil
}
func (m *DatabaseService) ListActiveProducts(ctx context.Context) ([]database.Product, error) {
	if m.ListActiveProductsFunc != nil {
		return m.ListActiveProductsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	products := make([]database.Product, 0, len(m.Products))
	for _, product := range m.Products {
		if product.IsActive && product.PriceCents > 0 {
			products = append(products, *product)
		}
	}

	return products, nil
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

func (m *DatabaseService) GetOrderByProviderPaymentID(ctx context.Context, provider string, providerPaymentID uuid.UUID) (*database.Order, error) {
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

func (m *DatabaseService) FindPendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, now time.Time) (*database.Order, error) {
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

func (m *DatabaseService) FindOrCreatePendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, amountCents int64, currency string, now time.Time) (*database.Order, error) {
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

func (m *DatabaseService) MarkPaymentCreationUncertain(ctx context.Context, orderID uint, uncertain bool) (bool, error) {
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

func (m *DatabaseService) SavePaymentDetails(ctx context.Context, orderID uint, providerPaymentID uuid.UUID, paymentURL string, paymentExpiresAt time.Time) error {
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

func (m *DatabaseService) SaveOrderPaymentAmounts(ctx context.Context, orderID uint, callbackAmountCents int64, providerFeeCents *int64) error {
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

func (m *DatabaseService) GetPaidOrdersWithoutProviderFee(ctx context.Context, limit int) ([]database.Order, error) {
	return m.GetPaidOrdersWithoutProviderFeeAfterID(ctx, 0, limit)
}

func (m *DatabaseService) GetPaidOrdersWithoutProviderFeeAfterID(ctx context.Context, afterID uint, limit int) ([]database.Order, error) {
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

func (m *DatabaseService) ConfirmOrderPaidCAS(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *database.Subscription, product *database.Product, applyPlan database.ApplyPlanInTxFn, callbackAmountCents int64) (bool, error) {
	if m.ConfirmOrderPaidCASFunc != nil {
		return m.ConfirmOrderPaidCASFunc(ctx, orderID, paidAt, activatedAt, sub, product, applyPlan, callbackAmountCents)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.Orders[orderID]
	if !ok || (order.Status != database.OrderStatusPending && order.Status != database.OrderStatusExpired) {
		return false, nil
	}

	if applyPlan != nil {
		// The in-memory fake has no transaction handle. Require tests that cover
		// transaction-scoped plan reconciliation to provide ConfirmOrderPaidCASFunc;
		// silently invoking the callback with nil would hide a production bug.
		// Validate before mutating any state: in production an applyPlan failure
		// rolls back the whole transaction, so the mock must not leave partial
		// order/subscription mutations behind when it returns this error.
		return false, errors.New("mock ConfirmOrderPaidCAS requires ConfirmOrderPaidCASFunc when applyPlan is set")
	}

	newExpiry := database.CalculatePaymentExpiry(activatedAt, sub, product)

	order.Status, order.PaidAt, order.ActivatedAt, order.ExpiresAt = database.OrderStatusPaid, &paidAt, &activatedAt, &newExpiry
	order.CallbackAmountCents = &callbackAmountCents
	if sub != nil {
		sub.ExpiresAt = &newExpiry
	}

	return true, nil
}

func (m *DatabaseService) CancelPaidOrderAndDowngradeCAS(ctx context.Context, provider string, providerPaymentID uuid.UUID, now time.Time, freePlanID uint, applyPlan database.ChargebackPlanInTxFn) (*database.ChargebackResult, error) {
	if m.CancelPaidOrderAndDowngradeCASFunc != nil {
		return m.CancelPaidOrderAndDowngradeCASFunc(ctx, provider, providerPaymentID, now, freePlanID, applyPlan)
	}

	return nil, errors.New("mock atomic chargeback requires CancelPaidOrderAndDowngradeCASFunc")
}

func (m *DatabaseService) CancelOrderCAS(ctx context.Context, provider string, providerPaymentID uuid.UUID, fromStatuses []database.OrderStatus) (bool, error) {
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
	// PlanID 2 is the seeded free plan: NewService seeds trial then free
	// (ids 1, 2), and the in-memory DatabaseService mock mirrors this. The test
	// DSN enables SQLite foreign-key enforcement, so a subscription must
	// reference an existing plan to be insertable into a real database.
	return &database.Subscription{
		TelegramID:     telegramID,
		Username:       username,
		ClientID:       "test-client-id-" + username,
		SubscriptionID: username,
		ExpiresAt:      expiry,
		Status:         status,
		PlanID:         2,
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
	ResetTrafficCalled    bool
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

func (m *XUIClient) ResetTraffic(_ context.Context, _ string) error {
	m.mu.Lock()
	m.ResetTrafficCalled = true
	m.mu.Unlock()

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
	LastRequest     tgbotapi.Chattable
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
	m.LastRequest = c

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

// LastRequestSafe returns the last Request Chattable (thread-safe).
func (m *BotAPI) LastRequestSafe() tgbotapi.Chattable {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.LastRequest
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
