package testutil

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"

	"gorm.io/gorm"
)

var ErrRecordNotFound = gorm.ErrRecordNotFound

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
	mu                            sync.RWMutex
	Subscriptions                 map[int64]*database.Subscription
	GetByTelegramIDFunc           func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	CreateSubscriptionFunc        func(ctx context.Context, sub *database.Subscription) error
	UpdateSubscriptionFunc        func(ctx context.Context, sub *database.Subscription) error
	DeleteSubscriptionFunc        func(ctx context.Context, telegramID int64) error
	GetLatestSubscriptionsFunc    func(ctx context.Context, limit int) ([]database.Subscription, error)
	GetAllSubscriptionsFunc       func(ctx context.Context) ([]database.Subscription, error)
	CountActiveSubscriptionsFunc  func(ctx context.Context) (int64, error)
	CountExpiredSubscriptionsFunc func(ctx context.Context) (int64, error)
	GetAllTelegramIDsFunc         func(ctx context.Context) ([]int64, error)
	GetByIDFunc                   func(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDByUsernameFunc   func(ctx context.Context, username string) (int64, error)
	DeleteSubscriptionByIDFunc    func(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDsBatchFunc       func(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCountFunc   func(ctx context.Context) (int64, error)
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
	m.mu.Lock()
	m.Subscriptions[sub.TelegramID] = sub
	m.mu.Unlock()
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
	m.Subscriptions[sub.TelegramID] = sub
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.Subscriptions)), nil
}

func (m *MockDatabaseService) Close() error {
	return nil
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
	LoginFunc               func(ctx context.Context) error
	AddClientFunc           func(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithIDFunc     func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	DeleteClientFunc        func(ctx context.Context, inboundID int, clientID string) error
	GetClientTrafficFunc    func(ctx context.Context, email string) (*xui.ClientTraffic, error)
	GetSubscriptionLinkFunc func(baseURL, subID, subPath string) string
	GetExternalURLFunc      func(host string) string
}

func (m *MockXUIClient) Login(ctx context.Context) error {
	if m.LoginFunc != nil {
		return m.LoginFunc(ctx)
	}
	return nil
}

func (m *MockXUIClient) AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
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

func (m *MockXUIClient) AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	if m.AddClientWithIDFunc != nil {
		return m.AddClientWithIDFunc(ctx, inboundID, email, clientID, subID, trafficBytes, expiryTime)
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

func (m *MockXUIClient) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
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
