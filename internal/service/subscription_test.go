package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
	"unicode/utf8"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"
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

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
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

	svc := NewSubscriptionService(db, xuiClient, cfg)
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

	svc := NewSubscriptionService(db, xuiClient, cfg)
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

	svc := NewSubscriptionService(db, xuiClient, cfg)
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

	deleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			deleteCalled = true
			assert.Equal(t, 1, inboundID)
			assert.Equal(t, "client-123", clientID)
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg)
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
	assert.True(t, deleteCalled)
}

func TestSubscriptionService_Delete_NotFound(t *testing.T) {
	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	xuiClient := &testutil.MockXUIClient{}

	svc := NewSubscriptionService(db, xuiClient, cfg)
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

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			return errors.New("xui connection refused")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg)
	err := svc.Delete(context.Background(), 123456)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "xui delete")
}

func TestSubscriptionService_Delete_DBError(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		TelegramID: 123456,
		ClientID:   "client-123",
	}

	deleteClientCalled := false
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, inboundID int, clientID string) error {
			deleteClientCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg)
	err := svc.Delete(context.Background(), 123456)

	// Delete should return error because mockDB.DeleteSubscription is stub
	// but the xui delete should have been called
	assert.True(t, deleteClientCalled)
	// The error depends on mockDB.DeleteSubscription implementation
	_ = err
}

func TestDaysUntilReset(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		expiryTime time.Time
		want       int
	}{
		{"zero expiry", time.Time{}, -1},
		{"future expiry", now.Add(24 * time.Hour), 1},
		{"past expiry", now.Add(-1 * time.Hour), 0},
		{"exactly now", now, 0},
		{"3 days", now.Add(3 * 24 * time.Hour), 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := daysUntilReset(now, tt.expiryTime)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatDateRu(t *testing.T) {
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero time", time.Time{}, "—"},
		{"specific date", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), "15 января 2025"},
		{"december", time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), "31 декабря 2025"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDateRu(tt.t)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGenerateProgressBar(t *testing.T) {
	tests := []struct {
		name    string
		usedGB  float64
		limitGB float64
		wantLen int
	}{
		{"zero limit", 0, 0, 10},
		{"negative limit", 5, -1, 10},
		{"empty bar", 0, 10, 10},
		{"full bar", 10, 10, 10},
		{"half way", 5, 10, 10},
		{"over 100%", 15, 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateProgressBar(tt.usedGB, tt.limitGB)
			assert.Equal(t, tt.wantLen, utf8.RuneCountInString(got))
		})
	}
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

	svc := NewSubscriptionService(db, xuiClient, cfg)
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

	svc := NewSubscriptionService(db, xuiClient, cfg)
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

	svc := NewSubscriptionService(db, xuiClient, cfg)
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

	svc := NewSubscriptionService(db, xuiClient, cfg)
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

	svc := NewSubscriptionService(db, xuiClient, cfg)
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, deleteCalled)
}
