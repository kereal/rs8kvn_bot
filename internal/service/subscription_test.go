package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
)

type mockDB struct {
	CreateSubscriptionFunc func(ctx context.Context, sub *database.Subscription) error
	GetByTelegramIDFunc    func(ctx context.Context, telegramID int64) (*database.Subscription, error)
}

func (m *mockDB) Ping(ctx context.Context) error { return nil }
func (m *mockDB) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	if m.GetByTelegramIDFunc != nil {
		return m.GetByTelegramIDFunc(ctx, telegramID)
	}
	return nil, errors.New("not found")
}
func (m *mockDB) CreateSubscription(ctx context.Context, sub *database.Subscription) error {
	if m.CreateSubscriptionFunc != nil {
		return m.CreateSubscriptionFunc(ctx, sub)
	}
	return nil
}
func (m *mockDB) UpdateSubscription(ctx context.Context, sub *database.Subscription) error {
	return nil
}
func (m *mockDB) DeleteSubscription(ctx context.Context, telegramID int64) error { return nil }
func (m *mockDB) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDB) DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDB) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	return nil, nil
}
func (m *mockDB) GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error) {
	return nil, nil
}
func (m *mockDB) CountAllSubscriptions(ctx context.Context) (int64, error)     { return 0, nil }
func (m *mockDB) CountActiveSubscriptions(ctx context.Context) (int64, error)  { return 0, nil }
func (m *mockDB) CountExpiredSubscriptions(ctx context.Context) (int64, error) { return 0, nil }
func (m *mockDB) GetAllTelegramIDs(ctx context.Context) ([]int64, error)       { return nil, nil }
func (m *mockDB) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	return 0, nil
}
func (m *mockDB) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	return nil, nil
}
func (m *mockDB) GetTotalTelegramIDCount(ctx context.Context) (int64, error) { return 0, nil }
func (m *mockDB) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	return nil, nil
}
func (m *mockDB) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	return nil, nil
}
func (m *mockDB) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDB) GetSubscriptionBySubscriptionID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDB) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDB) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDB) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	return 0, nil
}
func (m *mockDB) CreateTrialRequest(ctx context.Context, ip string) error { return nil }
func (m *mockDB) CleanupExpiredTrials(ctx context.Context, hours int, xuiClient interface {
	DeleteClient(ctx context.Context, inboundID int, clientID string) error
}, inboundID int) (int64, error) {
	return 0, nil
}
func (m *mockDB) Close() error                               { return nil }
func (m *mockDB) GetPoolStats() (*database.PoolStats, error) { return nil, nil }

type mockXUI struct {
	AddClientWithIDFunc func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error)
	DeleteClientFunc    func(ctx context.Context, inboundID int, clientID string) error
	GetSubLinkFunc      func(baseURL, subID, subPath string) string
	GetExtURLFunc       func(host string) string
}

func (m *mockXUI) Ping(ctx context.Context) error  { return nil }
func (m *mockXUI) Login(ctx context.Context) error { return nil }
func (m *mockXUI) AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	return nil, nil
}
func (m *mockXUI) AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
	if m.AddClientWithIDFunc != nil {
		return m.AddClientWithIDFunc(ctx, inboundID, email, clientID, subID, trafficBytes, expiryTime, resetDays)
	}
	return &xui.ClientConfig{ID: "default-id", SubID: "default-sub"}, nil
}
func (m *mockXUI) UpdateClient(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	return nil
}
func (m *mockXUI) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	if m.DeleteClientFunc != nil {
		return m.DeleteClientFunc(ctx, inboundID, clientID)
	}
	return nil
}
func (m *mockXUI) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
	return nil, nil
}
func (m *mockXUI) GetSubscriptionLink(baseURL, subID, subPath string) string {
	if m.GetSubLinkFunc != nil {
		return m.GetSubLinkFunc(baseURL, subID, subPath)
	}
	return baseURL + "/" + subPath + "/" + subID
}
func (m *mockXUI) GetExternalURL(host string) string {
	if m.GetExtURLFunc != nil {
		return m.GetExtURLFunc(host)
	}
	return host
}

func TestSubscriptionService_Create_Success(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	db := &mockDB{}
	xuiClient := &mockXUI{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "client-123", result.Subscription.ClientID)
	assert.Equal(t, "sub-456", result.Subscription.SubscriptionID)
	assert.Equal(t, int64(100*1024*1024*1024), result.Subscription.TrafficLimit)
}

func TestSubscriptionService_Create_XUIError(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	db := &mockDB{}
	xuiClient := &mockXUI{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return nil, errors.New("connection refused")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "xui add client")
}

func TestSubscriptionService_Create_DBError_RollbackSuccess(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	deleteCalled := false
	db := &mockDB{
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			return errors.New("database error")
		},
	}
	xuiClient := &mockXUI{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			deleteCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, deleteCalled)
	assert.Contains(t, err.Error(), "create subscription")
}

func TestSubscriptionService_Create_DBError_RollbackFailed(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	db := &mockDB{
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			return errors.New("database error")
		},
	}
	xuiClient := &mockXUI{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			return errors.New("rollback failed")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "rollback failed")
}
