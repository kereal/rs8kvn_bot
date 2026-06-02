package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	if err := testutil.InitLogger(m); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestSubscriptionService_Create_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrafficLimitGB: 100,
	}

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "client-123", result.Subscription.ClientID)
	assert.Equal(t, "sub-456", result.Subscription.SubscriptionID)
}

func TestSubscriptionService_Create_XUIError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrafficLimitGB: 100,
	}

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return nil, errors.New("connection refused")
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestSubscriptionService_Create_DBError_RollbackSuccess(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrafficLimitGB: 100,
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
		DeleteClientFunc: func(ctx context.Context, email string) error {
			deleteCalled = true
			assert.Equal(t, "testuser", email)
			return nil
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, deleteCalled)
	assert.Contains(t, err.Error(), "create subscription")
}

func TestSubscriptionService_Create_DBError_RollbackFailed(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrafficLimitGB: 100,
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
		DeleteClientFunc: func(ctx context.Context, email string) error {
			return errors.New("rollback failed")
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "create subscription")
}

func TestSubscriptionService_GetByTelegramID_Success(t *testing.T) {
	t.Parallel()

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
	svc := NewSubscriptionService(db, nil, nil, cfg, "", &webhook.NoopSender{})
	result, err := svc.GetByTelegramID(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestSubscriptionService_GetByTelegramID_NotFound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	svc := NewSubscriptionService(db, nil, nil, cfg, "", &webhook.NoopSender{})
	result, err := svc.GetByTelegramID(context.Background(), 999999)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not found")
}

func TestSubscriptionService_Delete_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

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
		DeleteClientFunc: func(ctx context.Context, email string) error {
			xuiDeleteCalled = true
			assert.Equal(t, "testuser", email)
			return nil
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
	assert.True(t, xuiDeleteCalled)
}

func TestSubscriptionService_Delete_NotFound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	svc := NewSubscriptionService(db, nil, nil, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 999999)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSubscriptionService_Delete_XUIError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

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
		DeleteClientFunc: func(ctx context.Context, email string) error {
			xuiDeleteCalled = true
			assert.Equal(t, "tgId_123456", email)
			return errors.New("xui connection refused")
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	// XUI errors are best-effort — Delete should succeed even if XUI cleanup fails
	assert.NoError(t, err)
	assert.True(t, xuiDeleteCalled, "XUI DeleteClient should still be called")
}

func TestSubscriptionService_Delete_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

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
	svc := NewSubscriptionService(db, nil, nil, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	// DB errors should still be returned since DB is deleted first
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db delete")
	// XUI DeleteClient should NOT be called because DB delete failed first
	assert.False(t, xuiDeleteCalled, "XUI DeleteClient should not be called when DB delete fails")
}

func TestSubscriptionService_Delete_UsesCorrectEmail(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		ClientID:   "client-456",
	}

	var receivedEmail string
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, email string) error {
			receivedEmail = email
			return nil
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, "testuser", receivedEmail, "DeleteClient should receive XUIEmail(username, id) when username is real")
}

func TestSubscriptionService_Delete_FallsBackToTgIdEmail(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	// No real username → email must be tgId_ format
	sub := &database.Subscription{
		TelegramID: 123456,
		ClientID:   "client-789",
		// Username empty on purpose
	}

	var receivedEmail string
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, email string) error {
			receivedEmail = email
			return nil
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, "tgId_123456", receivedEmail, "DeleteClient should receive tgId_ email when no real username")
}

func TestCalcTrialTraffic(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	cfg := &config.Config{
		TrafficLimitGB: 100,
	}

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		CreatedAt:  time.Now(),
		ExpiryTime: time.Now().Add(7 * 24 * time.Hour),
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
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	resultSub, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.NotNil(t, resultSub)
	assert.NotNil(t, traffic)
	assert.Equal(t, 100, traffic.LimitGB)
}

func TestSubscriptionService_GetWithTraffic_XUIErrorFallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrafficLimitGB: 100,
	}

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		CreatedAt:  time.Now(),
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
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	resultSub, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.NotNil(t, resultSub)
	assert.NotNil(t, traffic)
	assert.Equal(t, float64(0), traffic.UsedGB)
}

func TestSubscriptionService_CreateTrial_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrialDurationHours: 3,
	}

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.SubID)
	assert.NotEmpty(t, result.ClientID)
}

func TestSubscriptionService_CreateTrial_XUIError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrialDurationHours: 3,
	}

	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: "trial", DevicesLimit: 1, TrafficLimit: 1073741824, Duration: 3}, nil
		},
		GetSourcesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Source, error) {
			return []database.Source{{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1}}, nil
		},
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
			return &database.Subscription{InviteCode: inviteCode, SubscriptionID: subscriptionID, ClientID: clientID}, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return nil, errors.New("xui error")
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "xui error")
}

func TestSubscriptionService_CreateTrial_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrialDurationHours: 3,
	}

	deleteCalled := false
	db := &testutil.MockDatabaseService{
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
			return nil, errors.New("db error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
		},
		DeleteClientFunc: func(ctx context.Context, email string) error {
			deleteCalled = true
			assert.True(t, strings.HasPrefix(email, "trial_"), "trial rollback must use trial_ email")
			return nil
		},
	}
	sources := []database.Source{
		{ID: 1, Active: true,  XUIHost: "http://localhost:2053", XUIInboundID: 1},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, deleteCalled)
}

func TestSubscriptionService_ReconcileOrphanedClients_RemovesMissing(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	normal := &database.Subscription{ID: 10, TelegramID: 100, Username: "realuser", Status: "active"}
	trial := &database.Subscription{ID: 20, TelegramID: 0, SubscriptionID: "orphan123", Status: "active", PlanID: 1}
	good := &database.Subscription{ID: 30, TelegramID: 300, Username: "gooduser", Status: "active"}
	inactive := &database.Subscription{ID: 40, TelegramID: 400, Username: "bad", Status: "revoked"}

	deleted := []uint{}
	db := &testutil.MockDatabaseService{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return []database.Subscription{*normal, *trial, *good, *inactive}, nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			deleted = append(deleted, id)
			return &database.Subscription{ID: id}, nil
		},
	}

	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
			if email == "gooduser" {
				return &xui.ClientTraffic{}, nil
			}
			return nil, fmt.Errorf("client not found")
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}
	sources := []database.Source{{ID: 1, Active: true, XUIHost: "http://localhost:2053", XUIInboundID: 1}}
	svc := NewSubscriptionService(db, xuiClients, sources, cfg, "", &webhook.NoopSender{})

	invoked := []int64{}
	svc.SetInvalidateFunc(func(id int64) { invoked = append(invoked, id) })

	count, err := svc.ReconcileOrphanedClients(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.ElementsMatch(t, []uint{10, 20}, deleted)
	assert.Equal(t, []int64{100}, invoked)
}

func TestSubscriptionService_ReconcileOrphanedClients_NoActive(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.MockDatabaseService{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return []database.Subscription{{ID: 1, Status: "revoked"}}, nil
		},
	}
	svc := NewSubscriptionService(db, nil, nil, cfg, "", &webhook.NoopSender{})

	count, err := svc.ReconcileOrphanedClients(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}
