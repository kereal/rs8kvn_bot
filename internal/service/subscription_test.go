package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
}

func TestSubscriptionService_Create_Success(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
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

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return nil, errors.New("connection refused")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
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
	db := &testutil.MockDatabaseService{
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			return errors.New("database error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			deleteCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
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

	db := &testutil.MockDatabaseService{
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			return errors.New("database error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			return errors.New("rollback failed")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "rollback failed")
}

func TestSubscriptionService_GetByTelegramID_Success(t *testing.T) {
	cfg := &config.Config{}
	expected := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		Status:     "active",
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			assert.Equal(t, int64(123456), telegramID)
			return expected, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.GetByTelegramID(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestSubscriptionService_GetByTelegramID_NotFound(t *testing.T) {
	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	xuiClient := &testutil.MockXUIClient{}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.GetByTelegramID(context.Background(), 999999)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not found")
}

func TestSubscriptionService_Delete_Success(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		ClientID:   "client-123",
	}

	xuiDeleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			xuiDeleteCalled = true
			assert.Equal(t, 1, inboundID)
			assert.Equal(t, "client-123", clientID)
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
	assert.True(t, xuiDeleteCalled)
}

func TestSubscriptionService_Delete_NotFound(t *testing.T) {
	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	xuiClient := &testutil.MockXUIClient{}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 999999)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSubscriptionService_Delete_XUIError(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		TelegramID: 123456,
		ClientID:   "client-123",
	}

	xuiDeleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			xuiDeleteCalled = true
			return errors.New("xui connection refused")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	// XUI errors are best-effort — Delete should succeed even if XUI cleanup fails
	assert.NoError(t, err)
	assert.True(t, xuiDeleteCalled, "XUI DeleteClient should still be called")
}

func TestSubscriptionService_Delete_DBError(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		TelegramID: 123456,
		ClientID:   "client-123",
	}

	xuiDeleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return errors.New("db connection refused")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			xuiDeleteCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	// DB errors should still be returned since DB is deleted first
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db delete")
	// XUI DeleteClient should NOT be called because DB delete failed first
	assert.False(t, xuiDeleteCalled, "XUI DeleteClient should not be called when DB delete fails")
}

func TestSubscriptionService_Delete_UsesSubscriptionInboundID(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	// Subscription has a different InboundID than the config default
	sub := &database.Subscription{
		TelegramID: 123456,
		ClientID:   "client-456",
		InboundID:  5, // different from cfg.XUIInboundID
	}

	var receivedInboundID int
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			receivedInboundID = inboundID
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, sub.InboundID, receivedInboundID, "DeleteClient should be called with subscription's InboundID, not cfg.XUIInboundID")
}

func TestSubscriptionService_Delete_FallsBackToConfigInboundID(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	// Subscription has zero InboundID (e.g., migrated from old data)
	sub := &database.Subscription{
		TelegramID: 123456,
		ClientID:   "client-789",
		InboundID:  0, // zero — should fall back to cfg.XUIInboundID
	}

	var receivedInboundID int
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			receivedInboundID = inboundID
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, cfg.XUIInboundID, receivedInboundID, "DeleteClient should fall back to cfg.XUIInboundID when sub.InboundID is zero")
}

func TestCalcTrialTraffic(t *testing.T) {
	tests := []struct {
		name       string
		trialHours int
		minWant    int64
	}{
		{"1 hour", 1, 1024 * 1024 * 1024},
		{"3 hours", 3, 3 * 1024 * 1024 * 1024 / 12},
		{"24 hours", 24, 24 * 1024 * 1024 * 1024 / 12},
		{"100 hours", 100, 100 * 1024 * 1024 * 1024 / 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcTrialTraffic(tt.trialHours)
			assert.GreaterOrEqual(t, got, tt.minWant)
		})
	}
}

func TestSubscriptionService_GetWithTraffic_Success(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
	}

	sub := &database.Subscription{
		TelegramID:   123456,
		Username:     "testuser",
		TrafficLimit: 100 * 1024 * 1024 * 1024,
		CreatedAt:    time.Now(),
		ExpiryTime:   time.Now().Add(7 * 24 * time.Hour),
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
	}

	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(ctx context.Context, username string) (*xui.ClientTraffic, error) {
			return &xui.ClientTraffic{Up: 1024 * 1024 * 1024, Down: 2 * 1024 * 1024 * 1024}, nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	resultSub, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.NotNil(t, resultSub)
	assert.NotNil(t, traffic)
	assert.Equal(t, 100, traffic.LimitGB)
}

func TestSubscriptionService_GetWithTraffic_XUIErrorFallback(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
	}

	sub := &database.Subscription{
		TelegramID:   123456,
		Username:     "testuser",
		TrafficLimit: 100 * 1024 * 1024 * 1024,
		CreatedAt:    time.Now(),
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
	}

	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(ctx context.Context, username string) (*xui.ClientTraffic, error) {
			return nil, errors.New("connection refused")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	resultSub, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.NotNil(t, resultSub)
	assert.NotNil(t, traffic)
	assert.Equal(t, float64(0), traffic.UsedGB)
}

func TestSubscriptionService_CreateTrial_Success(t *testing.T) {
	cfg := &config.Config{
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
		TrialDurationHours: 3,
	}

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
		},
		GetSubscriptionLinkFunc: func(host, subID, subPath string) string {
			return host + "/" + subPath + "/" + subID
		},
		GetExternalURLFunc: func(host string) string {
			return host
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.SubID)
	assert.NotEmpty(t, result.ClientID)
}

func TestSubscriptionService_CreateTrial_XUIError(t *testing.T) {
	cfg := &config.Config{
		XUIInboundID:       1,
		TrialDurationHours: 3,
	}

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return nil, errors.New("xui error")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "xui add client")
}

func TestSubscriptionService_CreateTrial_DBError(t *testing.T) {
	cfg := &config.Config{
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
		TrialDurationHours: 3,
	}

	deleteCalled := false
	db := &testutil.MockDatabaseService{
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error) {
			return nil, errors.New("db error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
		},
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			deleteCalled = true
			return nil
		},
		GetSubscriptionLinkFunc: func(host, subID, subPath string) string {
			return host + "/" + subPath + "/" + subID
		},
		GetExternalURLFunc: func(host string) string {
			return host
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, deleteCalled)
}
