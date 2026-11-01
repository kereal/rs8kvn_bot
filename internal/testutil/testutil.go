// Package testutil provides reusable fakes and fixtures for repository and bot tests.
package testutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

var ErrRecordNotFound = gorm.ErrRecordNotFound

func PtrString(v string) *string {
	return new(v)
}

func PtrInt64(v int64) *int64 {
	return new(v)
}

func PtrTime(t time.Time) *time.Time {
	return new(t)
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

func CreateTestSubscription(telegramID int64, username string, status string, expiry *time.Time) *database.Subscription {
	// PlanID 2 is the seeded free plan: NewService seeds trial then free
	// (ids 1, 2), and per-consumer fakes mirror this via their storage maps. The test
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
	GetClientByEmailFunc    func(ctx context.Context, email string) (*xui.ClientRecord, error)
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

func (m *XUIClient) GetClientByEmail(ctx context.Context, email string) (*xui.ClientRecord, error) {
	if m.GetClientByEmailFunc != nil {
		return m.GetClientByEmailFunc(ctx, email)
	}

	return nil, xui.ErrClientNotFound
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
	if m.ExpireSubscriptionFunc != nil {
		return m.ExpireSubscriptionFunc(ctx, id, freePlanID)
	}

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

var (
	_ interfaces.ReferralCountSource = (*ReferralCountSourceFake)(nil)
	_ interfaces.ReferralInviteStore = (*ReferralInviteStoreFake)(nil)
	_ interfaces.AuditLogWriter      = (*AuditLogWriterFake)(nil)
)

// ReferralCountSourceFake is a plain in-memory fake for the
// bot.ReferralCache seam: a map of telegram ID to referral count.
type ReferralCountSourceFake struct {
	mu     sync.RWMutex
	Counts map[int64]int64
}

// NewReferralCountSourceFake returns an empty ReferralCountSourceFake.
func NewReferralCountSourceFake() *ReferralCountSourceFake {
	return &ReferralCountSourceFake{Counts: make(map[int64]int64)}
}

func (f *ReferralCountSourceFake) GetAllReferralCounts(context.Context) (map[int64]int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	counts := make(map[int64]int64, len(f.Counts))
	maps.Copy(counts, f.Counts)

	return counts, nil
}

// ReferralInviteStoreFake is a plain in-memory fake for the
// bot.ReferralHandler seam: one invite per referrer plus a code index.
type ReferralInviteStoreFake struct {
	mu      sync.RWMutex
	Invites map[int64]*database.Invite // keyed by referrer TG ID
}

// NewReferralInviteStoreFake returns an empty ReferralInviteStoreFake.
func NewReferralInviteStoreFake() *ReferralInviteStoreFake {
	return &ReferralInviteStoreFake{Invites: make(map[int64]*database.Invite)}
}

func (f *ReferralInviteStoreFake) GetOrCreateInvite(_ context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if invite, ok := f.Invites[referrerTGID]; ok {
		return invite, nil
	}

	invite := &database.Invite{ReferrerTGID: referrerTGID, Code: code}
	f.Invites[referrerTGID] = invite

	return invite, nil
}

func (f *ReferralInviteStoreFake) GetInviteByReferrer(_ context.Context, referrerTGID int64) (*database.Invite, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	invite, ok := f.Invites[referrerTGID]
	if !ok {
		// Handlers branch on this sentinel to offer invite creation; a plain
		// record-not-found would hide the distinction.
		return nil, database.ErrInviteNotFound
	}

	return invite, nil
}

// AuditLogWriterFake is a plain in-memory fake for audit-log seams: it
// records every inserted entry for assertions.
type AuditLogWriterFake struct {
	mu        sync.Mutex
	AuditLogs []database.AuditLog
}

func (f *AuditLogWriterFake) InsertAuditLog(_ context.Context, e *database.AuditLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.AuditLogs = append(f.AuditLogs, *e)

	return nil
}

// SubscriptionRepositoryFake is the in-memory fake for the already-narrowed read seams:
// it implements interfaces.WebRepository (SubscriptionRepository + Invite/Trial/Plan
// reads) and therefore satisfies the subserver, web and scheduler expire-worker seams
// structurally. The embedded ServiceStoreFake carries the subscription storage.

type SubscriptionRepositoryFake struct {
	*ServiceStoreFake

	Plans                                      map[uint]*database.Plan
	CountAllSubscriptionsFunc                  func(ctx context.Context) (int64, error)
	CountTrialRequestsByIPLastHourFunc         func(ctx context.Context, ip string) (int, error)
	CreateTrialRequestFunc                     func(ctx context.Context, ip string) error
	GetActiveSubscriptionsWithTrafficLimitFunc func(ctx context.Context) ([]database.SubscriptionTrafficTarget, error)
	GetAllReferralCountsFunc                   func(ctx context.Context) (map[int64]int64, error)
	GetExpiredPaidSubscriptionsFunc            func(ctx context.Context, now time.Time) ([]database.Subscription, error)
	GetFilteredTelegramIDCountFunc             func(ctx context.Context, filter database.BroadcastFilter) (int64, error)
	GetInviteByReferrerFunc                    func(ctx context.Context, referrerTGID int64) (*database.Invite, error)
	GetLatestSubscriptionsFunc                 func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetOrCreateInviteFunc                      func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetReferralCountFunc                       func(ctx context.Context, referrerTGID int64) (int64, error)
	GetSubscriptionStatusFunc                  func(ctx context.Context, subscriptionID string) (string, time.Time, error)
	GetSubscriptionsExpiringInRangeFunc        func(ctx context.Context, from, to time.Time) ([]database.Subscription, error)
	GetTelegramIDByUsernameFunc                func(ctx context.Context, username string) (int64, error)
	GetTelegramIDsBatchFunc                    func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc                func(ctx context.Context) (int64, error)
	GetWithPlanAndNodesFunc                    func(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error)
	UpdateDevicesFunc                          func(ctx context.Context, id uint, devicesJSON string) error
	UpdateIPsFunc                              func(ctx context.Context, id uint, ipsJSON string) error
	UpdateLastRequestFunc                      func(ctx context.Context, subscriptionID string) error
}

// NewSubscriptionRepositoryFake returns an empty SubscriptionRepositoryFake; the embedded ServiceStoreFake
// provides the shared subscription storage.
func NewSubscriptionRepositoryFake() *SubscriptionRepositoryFake {
	return &SubscriptionRepositoryFake{
		ServiceStoreFake: NewServiceStoreFake(),
		Plans:            make(map[uint]*database.Plan),
	}
}

var _ interfaces.WebRepository = (*SubscriptionRepositoryFake)(nil)

func (m *SubscriptionRepositoryFake) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
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

func (m *SubscriptionRepositoryFake) GetWithPlanAndNodes(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error) {
	if m.GetWithPlanAndNodesFunc != nil {
		return m.GetWithPlanAndNodesFunc(ctx, subscriptionID)
	}

	return nil, gorm.ErrRecordNotFound
}

func (m *SubscriptionRepositoryFake) CountAllSubscriptions(ctx context.Context) (int64, error) {
	if m.CountAllSubscriptionsFunc != nil {
		return m.CountAllSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.Subscriptions)), nil
}

func (m *SubscriptionRepositoryFake) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
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

func (m *SubscriptionRepositoryFake) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	if m.GetTelegramIDsBatchFunc != nil {
		return m.GetTelegramIDsBatchFunc(ctx, offset, limit)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var ids []int64
	for id := range m.Subscriptions {
		ids = append(ids, id)
	}

	// The production query orders by telegram_id; sorting keeps batches stable
	// so pagination skips and repeats nothing.
	slices.Sort(ids)

	if offset >= len(ids) {
		return []int64{}, nil
	}

	end := min(offset+limit, len(ids))

	return ids[offset:end], nil
}

func (m *SubscriptionRepositoryFake) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	if m.GetTotalTelegramIDCountFunc != nil {
		return m.GetTotalTelegramIDCountFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.Subscriptions)), nil
}

func (m *SubscriptionRepositoryFake) GetFilteredTelegramIDCount(ctx context.Context, filter database.BroadcastFilter) (int64, error) {
	if m.GetFilteredTelegramIDCountFunc != nil {
		return m.GetFilteredTelegramIDCountFunc(ctx, filter)
	}

	return m.GetTotalTelegramIDCount(ctx)
}

func (m *SubscriptionRepositoryFake) GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error) {
	if m.GetSubscriptionStatusFunc != nil {
		return m.GetSubscriptionStatusFunc(ctx, subscriptionID)
	}

	return "", time.Time{}, gorm.ErrRecordNotFound
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

func (m *SubscriptionRepositoryFake) GetSubscriptionsExpiringInRange(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
	if m.GetSubscriptionsExpiringInRangeFunc != nil {
		return m.GetSubscriptionsExpiringInRangeFunc(ctx, from, to)
	}

	return nil, nil
}

// GetActiveSubscriptionsWithTrafficLimit returns active subscriptions with
// their respective positive plan traffic limits from the shared fake.
func (m *SubscriptionRepositoryFake) GetActiveSubscriptionsWithTrafficLimit(ctx context.Context) ([]database.SubscriptionTrafficTarget, error) {
	if m.GetActiveSubscriptionsWithTrafficLimitFunc != nil {
		return m.GetActiveSubscriptionsWithTrafficLimitFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []database.SubscriptionTrafficTarget
	for _, sub := range m.Subscriptions {
		if sub.Status != string(database.SubscriptionStatusActive) {
			continue
		}
		plan, ok := m.Plans[sub.PlanID]
		if !ok {
			plan, _ = m.GetPlanByID(ctx, sub.PlanID)
		}
		if plan == nil || plan.TrafficLimit <= 0 {
			continue
		}
		out = append(out, database.SubscriptionTrafficTarget{Subscription: *sub, TrafficLimit: plan.TrafficLimit})
	}

	return out, nil
}

func (m *SubscriptionRepositoryFake) UpdateLastRequest(ctx context.Context, subscriptionID string) error {
	if m.UpdateLastRequestFunc != nil {
		return m.UpdateLastRequestFunc(ctx, subscriptionID)
	}

	return nil
}

func (m *SubscriptionRepositoryFake) GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error) {
	if m.GetReferralCountFunc != nil {
		return m.GetReferralCountFunc(ctx, referrerTGID)
	}

	return 0, nil
}

func (m *SubscriptionRepositoryFake) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	if m.GetOrCreateInviteFunc != nil {
		return m.GetOrCreateInviteFunc(ctx, referrerTGID, code)
	}

	return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
}

func (m *SubscriptionRepositoryFake) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error) {
	if m.GetInviteByReferrerFunc != nil {
		return m.GetInviteByReferrerFunc(ctx, referrerTGID)
	}

	return nil, database.ErrInviteNotFound
}

func (m *SubscriptionRepositoryFake) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	if m.GetAllReferralCountsFunc != nil {
		return m.GetAllReferralCountsFunc(ctx)
	}

	return make(map[int64]int64), nil
}

func (m *SubscriptionRepositoryFake) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	if m.CountTrialRequestsByIPLastHourFunc != nil {
		return m.CountTrialRequestsByIPLastHourFunc(ctx, ip)
	}

	return 0, nil
}

func (m *SubscriptionRepositoryFake) CreateTrialRequest(ctx context.Context, ip string) error {
	if m.CreateTrialRequestFunc != nil {
		return m.CreateTrialRequestFunc(ctx, ip)
	}

	return nil
}

// BotStoreFake is the in-memory fake for bot-package tests. Bot tests wire the handler
// together with SubscriptionService/OrderService over a single instance, so it embeds
// ServiceStoreFake (interfaces.SubscriptionStore + SyncStore + OrderStore) and adds the
// interfaces.BotStore surface (broadcast management, menu lookups, referral reads,
// audit writes).

type BotStoreFake struct {
	*ServiceStoreFake

	Products                             map[uint]*database.Product
	CancelBroadcastFunc                  func(ctx context.Context, id uint, now time.Time) (bool, error)
	ClaimBroadcastFunc                   func(ctx context.Context, id uint, now time.Time) (bool, error)
	ClaimBroadcastRecipientsFunc         func(ctx context.Context, broadcastID uint, now time.Time, limit int) ([]database.BroadcastRecipient, error)
	CountAllSubscriptionsFunc            func(ctx context.Context) (int64, error)
	CreateBroadcastFunc                  func(ctx context.Context, b *database.Broadcast) error
	FinishBroadcastRecipientFunc         func(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, status database.BroadcastRecipientStatus, lastError string, now time.Time) error
	GetAllReferralCountsFunc             func(ctx context.Context) (map[int64]int64, error)
	GetBroadcastFunc                     func(ctx context.Context, id uint) (*database.Broadcast, error)
	GetFilteredTelegramIDCountFunc       func(ctx context.Context, filter database.BroadcastFilter) (int64, error)
	GetInviteByReferrerFunc              func(ctx context.Context, referrerTGID int64) (*database.Invite, error)
	GetLatestSubscriptionsFunc           func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetOrCreateInviteFunc                func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetRunnableBroadcastsFunc            func(ctx context.Context, now time.Time) ([]database.Broadcast, error)
	GetTelegramIDByUsernameFunc          func(ctx context.Context, username string) (int64, error)
	GetTelegramIDsBatchFunc              func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc          func(ctx context.Context) (int64, error)
	ListActiveProductsFunc               func(ctx context.Context) ([]database.Product, error)
	ListBroadcastsFunc                   func(ctx context.Context, limit int) ([]database.Broadcast, error)
	RecoverStaleBroadcastRecipientsFunc  func(ctx context.Context, broadcastID uint, before time.Time) error
	ResetBroadcastFailedRecipientsFunc   func(ctx context.Context, id uint, now time.Time) error
	SnapshotBroadcastRecipientsFunc      func(ctx context.Context, broadcastID uint, filter database.BroadcastFilter) (int64, error)
	UpdateBroadcastFunc                  func(ctx context.Context, b *database.Broadcast) error
	UpdateBroadcastRecipientProgressFunc func(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, nextChunk int, now time.Time) error
}

// NewBotStoreFake returns an empty BotStoreFake; the embedded ServiceStoreFake
// provides the shared subscription storage.
func NewBotStoreFake() *BotStoreFake {
	return &BotStoreFake{
		ServiceStoreFake: NewServiceStoreFake(),
		Products:         make(map[uint]*database.Product),
	}
}

// errBotStoreFakeFuncNotSet is the explicit error a nil Func-field default
// produces, so tests fail loudly instead of silently passing on missing wiring.
func errBotStoreFakeFuncNotSet(name string) error {
	return fmt.Errorf("fake: %s is not set", name)
}

var (
	_ interfaces.BotStore          = (*BotStoreFake)(nil)
	_ interfaces.SubscriptionStore = (*BotStoreFake)(nil)
	_ interfaces.OrderStore        = (*BotStoreFake)(nil)
)

func (m *BotStoreFake) CancelBroadcast(ctx context.Context, id uint, now time.Time) (bool, error) {
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

func (m *BotStoreFake) ClaimBroadcast(ctx context.Context, id uint, now time.Time) (bool, error) {
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

func (m *BotStoreFake) ClaimBroadcastRecipients(ctx context.Context, broadcastID uint, now time.Time, limit int) ([]database.BroadcastRecipient, error) {
	if m.ClaimBroadcastRecipientsFunc != nil {
		return m.ClaimBroadcastRecipientsFunc(ctx, broadcastID, now, limit)
	}
	// The recipient lease state machine mirrors production CAS logic; tests that
	// need it run against the real database (testutil.NewTestDatabaseService).
	return nil, errBotStoreFakeFuncNotSet("ClaimBroadcastRecipientsFunc")
}

func (m *BotStoreFake) CountAllSubscriptions(ctx context.Context) (int64, error) {
	if m.CountAllSubscriptionsFunc != nil {
		return m.CountAllSubscriptionsFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.Subscriptions)), nil
}

func (m *BotStoreFake) CreateBroadcast(ctx context.Context, b *database.Broadcast) error {
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

func (m *BotStoreFake) FinishBroadcastRecipient(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, status database.BroadcastRecipientStatus, lastError string, now time.Time) error {
	if m.FinishBroadcastRecipientFunc != nil {
		return m.FinishBroadcastRecipientFunc(ctx, broadcastID, id, expectedAttempts, status, lastError, now)
	}
	// Lease CAS mirrors production logic; tests that need it run on real SQLite.
	return errBotStoreFakeFuncNotSet("FinishBroadcastRecipientFunc")
}

func (m *BotStoreFake) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	if m.GetAllReferralCountsFunc != nil {
		return m.GetAllReferralCountsFunc(ctx)
	}

	return make(map[int64]int64), nil
}

func (m *BotStoreFake) GetBroadcast(ctx context.Context, id uint) (*database.Broadcast, error) {
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

func (m *BotStoreFake) GetFilteredTelegramIDCount(ctx context.Context, filter database.BroadcastFilter) (int64, error) {
	if m.GetFilteredTelegramIDCountFunc != nil {
		return m.GetFilteredTelegramIDCountFunc(ctx, filter)
	}

	return m.GetTotalTelegramIDCount(ctx)
}

func (m *BotStoreFake) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error) {
	if m.GetInviteByReferrerFunc != nil {
		return m.GetInviteByReferrerFunc(ctx, referrerTGID)
	}

	return nil, database.ErrInviteNotFound
}

func (m *BotStoreFake) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
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

func (m *BotStoreFake) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	if m.GetOrCreateInviteFunc != nil {
		return m.GetOrCreateInviteFunc(ctx, referrerTGID, code)
	}

	return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
}

func (m *BotStoreFake) GetRunnableBroadcasts(ctx context.Context, now time.Time) ([]database.Broadcast, error) {
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

func (m *BotStoreFake) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
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

func (m *BotStoreFake) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	if m.GetTelegramIDsBatchFunc != nil {
		return m.GetTelegramIDsBatchFunc(ctx, offset, limit)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var ids []int64
	for id := range m.Subscriptions {
		ids = append(ids, id)
	}

	// The production query orders by telegram_id; sorting keeps batches stable
	// so pagination skips and repeats nothing.
	slices.Sort(ids)

	if offset >= len(ids) {
		return []int64{}, nil
	}

	end := min(offset+limit, len(ids))

	return ids[offset:end], nil
}

func (m *BotStoreFake) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	if m.GetTotalTelegramIDCountFunc != nil {
		return m.GetTotalTelegramIDCountFunc(ctx)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.Subscriptions)), nil
}

func (m *BotStoreFake) ListActiveProducts(ctx context.Context) ([]database.Product, error) {
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

func (m *BotStoreFake) ListBroadcasts(ctx context.Context, limit int) ([]database.Broadcast, error) {
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

func (m *BotStoreFake) RecoverStaleBroadcastRecipients(ctx context.Context, broadcastID uint, before time.Time) error {
	if m.RecoverStaleBroadcastRecipientsFunc != nil {
		return m.RecoverStaleBroadcastRecipientsFunc(ctx, broadcastID, before)
	}
	// Stale-lease recovery mirrors production state logic; tests that need it
	// run on real SQLite.
	return errBotStoreFakeFuncNotSet("RecoverStaleBroadcastRecipientsFunc")
}

func (m *BotStoreFake) ResetBroadcastFailedRecipients(ctx context.Context, id uint, now time.Time) error {
	if m.ResetBroadcastFailedRecipientsFunc != nil {
		return m.ResetBroadcastFailedRecipientsFunc(ctx, id, now)
	}
	// Retry reset rewrites recipient lease state; tests that need it run on
	// real SQLite.
	return errBotStoreFakeFuncNotSet("ResetBroadcastFailedRecipientsFunc")
}

func (m *BotStoreFake) SnapshotBroadcastRecipients(ctx context.Context, broadcastID uint, filter database.BroadcastFilter) (int64, error) {
	if m.SnapshotBroadcastRecipientsFunc != nil {
		return m.SnapshotBroadcastRecipientsFunc(ctx, broadcastID, filter)
	}
	// Audience snapshot materialization mirrors production logic; tests that
	// need it run on real SQLite or set SnapshotBroadcastRecipientsFunc.
	return 0, errBotStoreFakeFuncNotSet("SnapshotBroadcastRecipientsFunc")
}

func (m *BotStoreFake) UpdateBroadcast(ctx context.Context, b *database.Broadcast) error {
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

func (m *BotStoreFake) UpdateBroadcastRecipientProgress(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, nextChunk int, now time.Time) error {
	if m.UpdateBroadcastRecipientProgressFunc != nil {
		return m.UpdateBroadcastRecipientProgressFunc(ctx, broadcastID, id, expectedAttempts, nextChunk, now)
	}
	// Lease CAS mirrors production logic; tests that need it run on real SQLite.
	return errBotStoreFakeFuncNotSet("UpdateBroadcastRecipientProgressFunc")
}
