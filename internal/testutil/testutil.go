package testutil

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

var ErrRecordNotFound = gorm.ErrRecordNotFound

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

type TestDatabase struct {
	DB      *gorm.DB
	SQLDB   *sql.DB
	Path    string
	Cleanup func()
}

func NewTestDatabase(t any) (*TestDatabase, error) {
	type testInterface interface {
		TempDir() string
	}

	var tmpDir string
	if ti, ok := t.(testInterface); ok {
		tmpDir = ti.TempDir()
	} else {
		tmpDir = "/tmp"
	}

	dbPath := filepath.Join(tmpDir, "test.db")

	if err := database.Init(dbPath); err != nil {
		return nil, err
	}

	sqlDB, err := database.DB.DB()
	if err != nil {
		_ = database.Close()
		return nil, err
	}

	return &TestDatabase{
		DB:    database.DB,
		SQLDB: sqlDB,
		Path:  dbPath,
		Cleanup: func() {
			_ = database.Close()
		},
	}, nil
}

func NewMockDatabaseService() *MockDatabaseService {
	return &MockDatabaseService{
		Subscriptions: make(map[int64]*database.Subscription),
	}
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

type MockDatabaseService struct {
	mu                                  sync.RWMutex
	Subscriptions                       map[int64]*database.Subscription
	PingFunc                            func(ctx context.Context) error
	GetByTelegramIDFunc                 func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	CreateSubscriptionFunc              func(ctx context.Context, sub *database.Subscription) error
	UpdateSubscriptionFunc              func(ctx context.Context, sub *database.Subscription) error
	DeleteSubscriptionFunc              func(ctx context.Context, telegramID int64) error
	GetLatestSubscriptionsFunc          func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetAllSubscriptionsFunc             func(ctx context.Context) ([]database.Subscription, error)
	CountAllSubscriptionsFunc           func(ctx context.Context) (int64, error)
	CountActiveSubscriptionsFunc        func(ctx context.Context) (int64, error)
	CountExpiredSubscriptionsFunc       func(ctx context.Context) (int64, error)
	GetAllTelegramIDsFunc               func(ctx context.Context) ([]int64, error)
	GetByIDFunc                         func(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDByUsernameFunc         func(ctx context.Context, username string) (int64, error)
	DeleteSubscriptionByIDFunc          func(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDsBatchFunc             func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc         func(ctx context.Context) (int64, error)
	GetOrCreateInviteFunc               func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetInviteByCodeFunc                 func(ctx context.Context, code string) (*database.Invite, error)
	CreateTrialSubscriptionFunc         func(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error)
	GetSubscriptionBySubscriptionIDFunc func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	GetTrialSubscriptionBySubIDFunc     func(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	BindTrialSubscriptionFunc           func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CountTrialRequestsByIPLastHourFunc  func(ctx context.Context, ip string) (int, error)
	CreateTrialRequestFunc              func(ctx context.Context, ip string) error
	CleanupExpiredTrialsFunc            func(ctx context.Context, hours int, xuiClient interface {
		DeleteClient(ctx context.Context, inboundID int, clientID string) error
	}, inboundID int) (int64, error)
	GetPoolStatsFunc         func() (*database.PoolStats, error)
	GetReferralCountFunc     func(ctx context.Context, referrerTGID int64) (int64, error)
	GetAllReferralCountsFunc func(ctx context.Context) (map[int64]int64, error)
}

func (m *MockDatabaseService) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}
	return nil
}

func (m *MockDatabaseService) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
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

func (m *MockDatabaseService) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
	if m.GetByIDFunc != nil {
		return m.GetByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *MockDatabaseService) CreateSubscription(ctx context.Context, sub *database.Subscription) error {
	if m.CreateSubscriptionFunc != nil {
		return m.CreateSubscriptionFunc(ctx, sub)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Subscriptions == nil {
		m.Subscriptions = make(map[int64]*database.Subscription)
	}
	if sub.TelegramID != 0 {
		m.Subscriptions[sub.TelegramID] = sub
	}
	return nil
}

func (m *MockDatabaseService) UpdateSubscription(ctx context.Context, sub *database.Subscription) error {
	if m.UpdateSubscriptionFunc != nil {
		return m.UpdateSubscriptionFunc(ctx, sub)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Subscriptions == nil {
		m.Subscriptions = make(map[int64]*database.Subscription)
	}
	if sub.TelegramID != 0 {
		m.Subscriptions[sub.TelegramID] = sub
	}
	return nil
}

func (m *MockDatabaseService) DeleteSubscription(ctx context.Context, telegramID int64) error {
	if m.DeleteSubscriptionFunc != nil {
		return m.DeleteSubscriptionFunc(ctx, telegramID)
	}
	m.mu.Lock()
	delete(m.Subscriptions, telegramID)
	m.mu.Unlock()
	return nil
}

func (m *MockDatabaseService) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
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

func (m *MockDatabaseService) GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error) {
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

func (m *MockDatabaseService) CountActiveSubscriptions(ctx context.Context) (int64, error) {
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

func (m *MockDatabaseService) CountAllSubscriptions(ctx context.Context) (int64, error) {
	if m.CountAllSubscriptionsFunc != nil {
		return m.CountAllSubscriptionsFunc(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.Subscriptions)), nil
}

func (m *MockDatabaseService) CountExpiredSubscriptions(ctx context.Context) (int64, error) {
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

func (m *MockDatabaseService) GetAllTelegramIDs(ctx context.Context) ([]int64, error) {
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

func (m *MockDatabaseService) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
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

func (m *MockDatabaseService) DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error) {
	if m.DeleteSubscriptionByIDFunc != nil {
		return m.DeleteSubscriptionByIDFunc(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *MockDatabaseService) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
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

func (m *MockDatabaseService) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	if m.GetTotalTelegramIDCountFunc != nil {
		return m.GetTotalTelegramIDCountFunc(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.Subscriptions)), nil
}

func (m *MockDatabaseService) Close() error {
	return nil
}

func (m *MockDatabaseService) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	if m.GetOrCreateInviteFunc != nil {
		return m.GetOrCreateInviteFunc(ctx, referrerTGID, code)
	}
	return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
}

func (m *MockDatabaseService) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	if m.GetInviteByCodeFunc != nil {
		return m.GetInviteByCodeFunc(ctx, code)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *MockDatabaseService) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error) {
	if m.CreateTrialSubscriptionFunc != nil {
		return m.CreateTrialSubscriptionFunc(ctx, inviteCode, subscriptionID, clientID, inboundID, trafficBytes, expiryTime, subURL)
	}
	return &database.Subscription{InviteCode: inviteCode, SubscriptionID: subscriptionID, ClientID: clientID, IsTrial: true}, nil
}

func (m *MockDatabaseService) GetSubscriptionBySubscriptionID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	if m.GetSubscriptionBySubscriptionIDFunc != nil {
		return m.GetSubscriptionBySubscriptionIDFunc(ctx, subscriptionID)
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

func (m *MockDatabaseService) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	if m.GetTrialSubscriptionBySubIDFunc != nil {
		return m.GetTrialSubscriptionBySubIDFunc(ctx, subscriptionID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sub := range m.Subscriptions {
		if sub.SubscriptionID == subscriptionID && sub.IsTrial {
			return sub, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *MockDatabaseService) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	if m.BindTrialSubscriptionFunc != nil {
		return m.BindTrialSubscriptionFunc(ctx, subscriptionID, telegramID, username)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sub := range m.Subscriptions {
		if sub.SubscriptionID == subscriptionID {
			sub.TelegramID = telegramID
			sub.Username = username
			sub.IsTrial = false
			return sub, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *MockDatabaseService) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	if m.CountTrialRequestsByIPLastHourFunc != nil {
		return m.CountTrialRequestsByIPLastHourFunc(ctx, ip)
	}
	return 0, nil
}

func (m *MockDatabaseService) CreateTrialRequest(ctx context.Context, ip string) error {
	if m.CreateTrialRequestFunc != nil {
		return m.CreateTrialRequestFunc(ctx, ip)
	}
	return nil
}

func (m *MockDatabaseService) CleanupExpiredTrials(ctx context.Context, hours int, xuiClient interface {
	DeleteClient(ctx context.Context, inboundID int, clientID string) error
}, inboundID int) (int64, error) {
	if m.CleanupExpiredTrialsFunc != nil {
		return m.CleanupExpiredTrialsFunc(ctx, hours, xuiClient, inboundID)
	}
	return 0, nil
}

func (m *MockDatabaseService) GetPoolStats() (*database.PoolStats, error) {
	if m.GetPoolStatsFunc != nil {
		return m.GetPoolStatsFunc()
	}
	return &database.PoolStats{}, nil
}

func (m *MockDatabaseService) GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error) {
	if m.GetReferralCountFunc != nil {
		return m.GetReferralCountFunc(ctx, referrerTGID)
	}
	return 0, nil
}

func (m *MockDatabaseService) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	if m.GetAllReferralCountsFunc != nil {
		return m.GetAllReferralCountsFunc(ctx)
	}
	return make(map[int64]int64), nil
}

func CreateTestSubscription(telegramID int64, username string, status string, expiry time.Time) *database.Subscription {
	return &database.Subscription{
		TelegramID:      telegramID,
		Username:        username,
		ClientID:        "test-client-id-" + username,
		SubscriptionID:  username,
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      expiry,
		Status:          status,
		SubscriptionURL: "http://localhost/sub/" + username,
	}
}

type MockXUIClient struct {
	mu                      sync.Mutex
	PingFunc                func(ctx context.Context) error
	LoginFunc               func(ctx context.Context) error
	AddClientFunc           func(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithIDFunc     func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error)
	UpdateClientFunc        func(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error
	DeleteClientFunc        func(ctx context.Context, inboundID int, clientID string) error
	GetClientTrafficFunc    func(ctx context.Context, email string) (*xui.ClientTraffic, error)
	GetSubscriptionLinkFunc func(baseURL, subID, subPath string) string
	GetExternalURLFunc      func(host string) string

	// Call tracking
	AddClientCalled       bool
	AddClientWithIDCalled bool
	DeleteClientCalled    bool
	UpdateClientCalled    bool
}

func (m *MockXUIClient) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}
	return nil
}

func (m *MockXUIClient) Login(ctx context.Context) error {
	if m.LoginFunc != nil {
		return m.LoginFunc(ctx)
	}
	return nil
}

func (m *MockXUIClient) AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	m.mu.Lock()
	m.AddClientCalled = true
	m.mu.Unlock()
	if m.AddClientFunc != nil {
		return m.AddClientFunc(ctx, inboundID, email, trafficBytes, expiryTime)
	}
	return &xui.ClientConfig{
		ID:         "test-client-id",
		Email:      email,
		TotalGB:    trafficBytes,
		ExpiryTime: expiryTime.UnixMilli(),
		Enable:     true,
	}, nil
}

func (m *MockXUIClient) AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
	m.mu.Lock()
	m.AddClientWithIDCalled = true
	m.mu.Unlock()
	if m.AddClientWithIDFunc != nil {
		return m.AddClientWithIDFunc(ctx, inboundID, email, clientID, subID, trafficBytes, expiryTime, resetDays)
	}
	return &xui.ClientConfig{
		ID:         clientID,
		Email:      email,
		TotalGB:    trafficBytes,
		ExpiryTime: expiryTime.UnixMilli(),
		Enable:     true,
		SubID:      subID,
	}, nil
}

func (m *MockXUIClient) UpdateClient(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	m.mu.Lock()
	m.UpdateClientCalled = true
	m.mu.Unlock()
	if m.UpdateClientFunc != nil {
		return m.UpdateClientFunc(ctx, inboundID, clientID, email, subID, trafficBytes, expiryTime, tgID, comment)
	}
	return nil
}

func (m *MockXUIClient) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	m.mu.Lock()
	m.DeleteClientCalled = true
	m.mu.Unlock()
	if m.DeleteClientFunc != nil {
		return m.DeleteClientFunc(ctx, inboundID, clientID)
	}
	return nil
}

func (m *MockXUIClient) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
	if m.GetClientTrafficFunc != nil {
		return m.GetClientTrafficFunc(ctx, email)
	}
	return &xui.ClientTraffic{
		Up:   1024 * 1024 * 100,
		Down: 1024 * 1024 * 200,
	}, nil
}

func (m *MockXUIClient) GetSubscriptionLink(baseURL, subID, subPath string) string {
	if m.GetSubscriptionLinkFunc != nil {
		return m.GetSubscriptionLinkFunc(baseURL, subID, subPath)
	}
	return baseURL + "/" + subPath + "/" + subID
}

func (m *MockXUIClient) GetExternalURL(host string) string {
	if m.GetExternalURLFunc != nil {
		return m.GetExternalURLFunc(host)
	}
	return host
}

func NewMockXUIClient() *MockXUIClient {
	return &MockXUIClient{}
}

// MockBotAPI is a mock implementation of the Telegram Bot API for testing.
type MockBotAPI struct {
	mu            sync.RWMutex
	SendCalled    bool
	RequestCalled bool
	LastSentText  string
	LastChatID    int64
	SendCount     int
	SendError     error
	RequestError  error
	LastChattable tgbotapi.Chattable
	// SendFunc allows custom behavior per test. If set, Send calls this instead of default logic.
	SendFunc func(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

func NewMockBotAPI() *MockBotAPI {
	return &MockBotAPI{}
}

func (m *MockBotAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SendCalled = true
	m.SendCount++
	m.LastChattable = c

	// Extract text and chat ID from various message types
	switch v := c.(type) {
	case tgbotapi.MessageConfig:
		m.LastSentText = v.Text
		m.LastChatID = v.ChatID
	case tgbotapi.EditMessageTextConfig:
		m.LastSentText = v.Text
		m.LastChatID = v.ChatID
	case tgbotapi.EditMessageReplyMarkupConfig:
		m.LastChatID = v.ChatID
	case tgbotapi.DeleteMessageConfig:
		m.LastChatID = v.ChatID
	}

	// Use custom send function if provided
	if m.SendFunc != nil {
		return m.SendFunc(c)
	}

	if m.SendError != nil {
		return tgbotapi.Message{}, m.SendError
	}
	return tgbotapi.Message{MessageID: 1}, nil
}

func (m *MockBotAPI) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RequestCalled = true

	if m.RequestError != nil {
		return nil, m.RequestError
	}
	return &tgbotapi.APIResponse{Ok: true}, nil
}

// SendCountSafe returns the number of Send calls (thread-safe).
func (m *MockBotAPI) SendCountSafe() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.SendCount
}

// SendCalledSafe returns whether Send was called (thread-safe).
func (m *MockBotAPI) SendCalledSafe() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.SendCalled
}

// RequestCalledSafe returns whether Request was called (thread-safe).
func (m *MockBotAPI) RequestCalledSafe() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.RequestCalled
}

// LastSentTextSafe returns the last sent text (thread-safe).
func (m *MockBotAPI) LastSentTextSafe() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.LastSentText
}

// LastChattableSafe returns the last sent Chattable (thread-safe).
func (m *MockBotAPI) LastChattableSafe() tgbotapi.Chattable {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.LastChattable
}

func (m *MockBotAPI) GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	ch := make(chan tgbotapi.Update)
	close(ch)
	return ch
}

func (m *MockBotAPI) StopReceivingUpdates() {
	// No-op for mock
}

func (m *MockBotAPI) Self() *tgbotapi.User {
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
