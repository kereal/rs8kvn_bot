package testutil

import (
	"context"
	"database/sql"
	"path/filepath"
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
		database.Close()
		return nil, err
	}

	return &TestDatabase{
		DB:    database.DB,
		SQLDB: sqlDB,
		Path:  dbPath,
		Cleanup: func() {
			database.Close()
		},
	}, nil
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
	Subscriptions              map[int64]*database.Subscription
	GetByTelegramIDFunc        func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	CreateSubscriptionFunc     func(ctx context.Context, sub *database.Subscription) error
	GetLatestSubscriptionsFunc func(ctx context.Context, limit int) ([]database.Subscription, error)
	DeleteSubscriptionFunc     func(ctx context.Context, telegramID int64) error
}

func (m *MockDatabaseService) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	if m.GetByTelegramIDFunc != nil {
		return m.GetByTelegramIDFunc(ctx, telegramID)
	}
	if sub, ok := m.Subscriptions[telegramID]; ok {
		return sub, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *MockDatabaseService) CreateSubscription(ctx context.Context, sub *database.Subscription) error {
	if m.CreateSubscriptionFunc != nil {
		return m.CreateSubscriptionFunc(ctx, sub)
	}
	m.Subscriptions[sub.TelegramID] = sub
	return nil
}

func (m *MockDatabaseService) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	if m.GetLatestSubscriptionsFunc != nil {
		return m.GetLatestSubscriptionsFunc(ctx, limit)
	}
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

func (m *MockDatabaseService) DeleteSubscription(ctx context.Context, telegramID int64) error {
	if m.DeleteSubscriptionFunc != nil {
		return m.DeleteSubscriptionFunc(ctx, telegramID)
	}
	delete(m.Subscriptions, telegramID)
	return nil
}

func CreateTestSubscription(telegramID int64, username string, status string, expiry time.Time) *database.Subscription {
	return &database.Subscription{
		TelegramID:      telegramID,
		Username:        username,
		ClientID:        "test-client-id-" + username,
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      expiry,
		Status:          status,
		SubscriptionURL: "http://localhost/sub/" + username,
	}
}

type MockXUIClient struct {
	LoginFunc            func(ctx context.Context) error
	AddClientWithIDFunc  func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	DeleteClientFunc     func(ctx context.Context, inboundID int, clientID string) error
	GetClientTrafficFunc func(ctx context.Context, email string) (*xui.ClientTraffic, error)
}

func (m *MockXUIClient) Login(ctx context.Context) error {
	if m.LoginFunc != nil {
		return m.LoginFunc(ctx)
	}
	return nil
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

func NewMockXUIClient() *MockXUIClient {
	return &MockXUIClient{}
}

func (m *MockXUIClient) GetSubscriptionLink(baseURL, subID, subPath string) string {
	return baseURL + "/" + subPath + "/" + subID
}

func (m *MockXUIClient) GetHost() string {
	return "http://localhost:2053"
}

func (m *MockXUIClient) GetClient(email string) (map[string]interface{}, error) {
	return nil, nil
}
