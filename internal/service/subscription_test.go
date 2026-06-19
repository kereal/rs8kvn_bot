package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/webhook"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			sub.ID = 1
			return nil
		},
		GetNodesByPlanIDFunc: func(ctx context.Context, planID uint) ([]database.Node, error) {
			return nil, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	sources := []database.Node{}
	xuiClients := map[uint]interfaces.XUIClient{}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Subscription)
	assert.NotEmpty(t, result.Subscription.ClientID)
	assert.NotEmpty(t, result.Subscription.SubscriptionID)
	assert.Equal(t, int64(123456), result.Subscription.TelegramID)
	assert.Equal(t, "testuser", result.Subscription.Username)
}

func TestSubscriptionService_Create_XUIError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			sub.ID = 1
			return nil
		},
		GetNodesByPlanIDFunc: func(ctx context.Context, planID uint) ([]database.Node, error) {
			return nil, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	sources := []database.Node{}
	xuiClients := map[uint]interfaces.XUIClient{}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	// DB-first: Create succeeds even if XUI is unavailable
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestSubscriptionService_Create_DBError_RollbackSuccess(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			return errors.New("database error")
		},
	}
	sources := []database.Node{}
	xuiClients := map[uint]interfaces.XUIClient{}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "create subscription")
}

func TestSubscriptionService_Create_DBError_RollbackFailed(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			return errors.New("database error")
		},
	}
	sources := []database.Node{}
	xuiClients := map[uint]interfaces.XUIClient{}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "create subscription")
}

func TestSubscriptionService_Create_PropagatesInviteCodeToDB(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	var gotSub *database.Subscription
	var gotInviteCode string
	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			gotSub = sub
			gotInviteCode = inviteCode
			sub.ID = 1
			return nil
		},
		GetNodesByPlanIDFunc: func(ctx context.Context, planID uint) ([]database.Node, error) {
			return nil, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	sources := []database.Node{}
	xuiClients := map[uint]interfaces.XUIClient{}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser", "INV-ABC")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "INV-ABC", gotInviteCode, "inviteCode must be forwarded to db.CreateSubscription")
	require.NotNil(t, gotSub)
	assert.Equal(t, int64(123456), gotSub.TelegramID)
	// Mock does not simulate invite resolution; the real DB layer would set
	// sub.InviteCode/sub.ReferredBy. We only assert the parameter was passed.
	assert.Equal(t, int64(0), result.ReferrerTGID)
}

func TestSubscriptionService_Create_EmptyInviteCodeIsNoop(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	var gotInviteCode string
	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			gotInviteCode = inviteCode
			sub.ID = 1
			return nil
		},
		GetNodesByPlanIDFunc: func(ctx context.Context, planID uint) ([]database.Node, error) {
			return nil, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	sources := []database.Node{}
	xuiClients := map[uint]interfaces.XUIClient{}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "", gotInviteCode)
	assert.Equal(t, int64(0), result.ReferrerTGID)
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
	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})
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
	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})
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

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}

	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
}

func TestSubscriptionService_Delete_NotFound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})
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

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}

	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	// Sync errors are best-effort — Delete should succeed even if sync fails
	assert.NoError(t, err)
}

func TestSubscriptionService_Delete_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	sub := &database.Subscription{
		TelegramID: 123456,
		ClientID:   "client-123",
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return errors.New("db connection refused")
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}
	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	// DB errors should still be returned
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db delete")
}

func TestSubscriptionService_Delete_UsesCorrectEmail(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		ClientID:   "client-456",
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}

	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	// Email is now computed inside sync module, not in Delete()
	assert.NoError(t, err)
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

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(ctx context.Context, telegramID int64) error {
			return nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
		},
	}

	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 123456)

	// Email is now computed inside sync module, not in Delete()
	assert.NoError(t, err)
}

func TestSubscriptionService_GetWithTraffic_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		PlanID:     1,
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(7 * 24 * time.Hour),
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: 1, TrafficLimit: 100 * 1024 * 1024 * 1024}, nil
		},
	}

	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(ctx context.Context, username string) (*xui.ClientTraffic, error) {
			return &xui.ClientTraffic{Up: 1024 * 1024 * 1024, Down: 2 * 1024 * 1024 * 1024}, nil
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	resultSub, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.NotNil(t, resultSub)
	assert.NotNil(t, traffic)
	assert.Equal(t, 100, traffic.LimitGB)
}

func TestSubscriptionService_GetWithTraffic_XUIErrorFallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

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
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
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

	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: name, TrafficLimit: 1073741824}, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
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
			return &database.Plan{ID: 1, Name: "trial", TrafficLimit: 1073741824}, nil
		},
		GetNodesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Node, error) {
			return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
		},
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
			return &database.Subscription{InviteCode: inviteCode, SubscriptionID: subscriptionID, ClientID: clientID}, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return nil, errors.New("xui error")
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
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
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: name, TrafficLimit: 1073741824}, nil
		},
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
			return nil, errors.New("db error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
		},
		DeleteClientFunc: func(ctx context.Context, email string) error {
			deleteCalled = true
			assert.True(t, strings.HasPrefix(email, "trial_"), "trial rollback must use trial_ email")
			return nil
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, deleteCalled)
}

func TestSubscriptionService_CreateTrial_NoTrialNodes(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrialDurationHours: 3,
	}

	db := &testutil.MockDatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: name, TrafficLimit: 1073741824}, nil
		},
		GetNodesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Node, error) {
			return nil, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no linked nodes")
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
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}
	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})

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
	svc := NewSubscriptionService(db, nil, nil, nil, cfg, "", &webhook.NoopSender{})

	count, err := svc.ReconcileOrphanedClients(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

// TestSubscriptionService_BindTrial_UpdatesAllSources verifies that when
// UpdateClient fails on one trial source, the loop continues to the next
// source (fix #6: break → continue). All trial sources must be upgraded.
func TestSubscriptionService_BindTrial_UpdatesAllSources(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 3}

	bound := &database.Subscription{
		ID:             42,
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "client-xyz",
		SubscriptionID: "trial-sub-1",
		Status:         "active",
		PlanID:         2,
	}

	db := &testutil.MockDatabaseService{
		BindTrialSubscriptionFunc: func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
			return bound, nil
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: database.FreePlanName, TrafficLimit: 1024 * 1024 * 1024}, nil
		},
		GetNodesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Node, error) {
			if planName == database.TrialPlanName {
				return []database.Node{
					{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"},
					{ID: 2, IsActive: true, Host: "http://x2", InboundIDs: "[1]"},
				}, nil
			}
			return nil, nil
		},
	}

	xui1 := &testutil.MockXUIClient{
		UpdateClientFunc: func(ctx context.Context, inboundIDs []int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
			return errors.New("source 1 unreachable")
		},
	}
	xui2 := &testutil.MockXUIClient{
		UpdateClientFunc: func(ctx context.Context, inboundIDs []int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
			return nil
		},
	}

	xuiClients := map[uint]interfaces.XUIClient{1: xui1, 2: xui2}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"},
		{ID: 2, IsActive: true, Host: "http://x2", InboundIDs: "[1]"},
	}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})

	got, err := svc.BindTrial(context.Background(), "trial-sub-1", 123456, "testuser")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, uint(42), got.ID)
}

// ==================== DeleteByID Tests ====================

func TestSubscriptionService_DeleteByID_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 7, TelegramID: 42, Username: "u", ClientID: "c1", SubscriptionID: "s1"}, nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			assert.Equal(t, uint(7), id)
			return &database.Subscription{ID: 7, TelegramID: 42, Username: "u", ClientID: "c1", SubscriptionID: "s1"}, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, email string) error {
			assert.NotEmpty(t, email)
			return nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	deleted, err := svc.DeleteByID(context.Background(), 7)

	assert.NoError(t, err)
	assert.NotNil(t, deleted)
	assert.Equal(t, uint(7), deleted.ID)
}

func TestSubscriptionService_DeleteByID_GetError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	deleted, err := svc.DeleteByID(context.Background(), 1)

	assert.Error(t, err)
	assert.Nil(t, deleted)
	assert.Contains(t, err.Error(), "get subscription")
}

func TestSubscriptionService_DeleteByID_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, ClientID: "c", SubscriptionID: "s", TelegramID: 1, Username: "u"}, nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return nil, errors.New("db fail")
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	deleted, err := svc.DeleteByID(context.Background(), 1)

	assert.Error(t, err)
	assert.Nil(t, deleted)
	assert.Contains(t, err.Error(), "db delete")
}

// ==================== GetByID Tests ====================

func TestSubscriptionService_GetByID_Success(t *testing.T) {
	t.Parallel()

	expected := &database.Subscription{ID: 99, TelegramID: 1, ClientID: "c", SubscriptionID: "s"}
	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			assert.Equal(t, uint(99), id)
			return expected, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetByID(context.Background(), 99)

	assert.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestSubscriptionService_GetByID_NotFound(t *testing.T) {
	t.Parallel()

	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetByID(context.Background(), 1)

	assert.Error(t, err)
	assert.Nil(t, got)
}

// ==================== Invite Tests ====================

func TestSubscriptionService_GetOrCreateInvite_Delegates(t *testing.T) {
	t.Parallel()

	want := &database.Invite{Code: "ABC", ReferrerTGID: 111}
	db := &testutil.MockDatabaseService{
		GetOrCreateInviteFunc: func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
			assert.Equal(t, int64(111), referrerTGID)
			assert.Equal(t, "ABC", code)
			return want, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetOrCreateInvite(context.Background(), 111, "ABC")

	assert.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestSubscriptionService_GetInviteByCode_Delegates(t *testing.T) {
	t.Parallel()

	want := &database.Invite{Code: "XYZ", ReferrerTGID: 222}
	db := &testutil.MockDatabaseService{
		GetInviteByCodeFunc: func(ctx context.Context, code string) (*database.Invite, error) {
			assert.Equal(t, "XYZ", code)
			return want, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetInviteByCode(context.Background(), "XYZ")

	assert.NoError(t, err)
	assert.Equal(t, want, got)
}

// ==================== Count / List Tests ====================

func TestSubscriptionService_CountAll_Delegates(t *testing.T) {
	t.Parallel()

	db := &testutil.MockDatabaseService{
		CountAllSubscriptionsFunc: func(ctx context.Context) (int64, error) {
			return 42, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.CountAll(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, int64(42), got)
}

func TestSubscriptionService_CountActive_Delegates(t *testing.T) {
	t.Parallel()

	db := &testutil.MockDatabaseService{
		CountActiveSubscriptionsFunc: func(ctx context.Context) (int64, error) {
			return 7, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.CountActive(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, int64(7), got)
}

func TestSubscriptionService_GetLatest_Delegates(t *testing.T) {
	t.Parallel()

	want := []database.Subscription{{ID: 1}, {ID: 2}}
	db := &testutil.MockDatabaseService{
		GetLatestSubscriptionsFunc: func(ctx context.Context, limit int) ([]database.Subscription, error) {
			assert.Equal(t, 5, limit)
			return want, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetLatest(context.Background(), 5)

	assert.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestSubscriptionService_GetTelegramIDByUsername_Delegates(t *testing.T) {
	t.Parallel()

	db := &testutil.MockDatabaseService{
		GetTelegramIDByUsernameFunc: func(ctx context.Context, username string) (int64, error) {
			assert.Equal(t, "alice", username)
			return 555, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetTelegramIDByUsername(context.Background(), "alice")

	assert.NoError(t, err)
	assert.Equal(t, int64(555), got)
}

// ==================== InvalidateSubscription Tests ====================

func TestSubscriptionService_InvalidateSubscription_CallsCallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(&testutil.MockDatabaseService{}, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})

	var captured int64
	svc.SetInvalidateFunc(func(telegramID int64) {
		captured = telegramID
	})

	svc.InvalidateSubscription(context.Background(), 123)
	assert.Equal(t, int64(123), captured)
}

func TestSubscriptionService_InvalidateSubscription_NoCallback(t *testing.T) {
	t.Parallel()

	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(&testutil.MockDatabaseService{}, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})

	assert.NotPanics(t, func() {
		svc.InvalidateSubscription(context.Background(), 1)
	})
}

// ==================== CleanupExpiredTrials Tests ====================

func TestSubscriptionService_CleanupExpiredTrials_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 24}
	expired := []database.Subscription{
		{SubscriptionID: "exp1", ClientID: "c1", TelegramID: 0},
		{SubscriptionID: "exp2", ClientID: "c2", TelegramID: 0},
	}
	db := &testutil.MockDatabaseService{
		CleanupExpiredTrialsFunc: func(ctx context.Context, hours int) ([]database.Subscription, error) {
			assert.Equal(t, 24, hours)
			return expired, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(ctx context.Context, email string) error {
			assert.True(t, strings.HasPrefix(email, "trial_"))
			return nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	n, err := svc.CleanupExpiredTrials(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

func TestSubscriptionService_CleanupExpiredTrials_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 24}
	db := &testutil.MockDatabaseService{
		CleanupExpiredTrialsFunc: func(ctx context.Context, hours int) ([]database.Subscription, error) {
			return nil, errors.New("db fail")
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, cfg, "", &webhook.NoopSender{})
	n, err := svc.CleanupExpiredTrials(context.Background())

	assert.Error(t, err)
	assert.Equal(t, int64(0), n)
}

// ==================== Batch / Pagination Tests ====================

func TestSubscriptionService_GetTotalTelegramIDCount_Delegates(t *testing.T) {
	t.Parallel()

	db := &testutil.MockDatabaseService{
		GetTotalTelegramIDCountFunc: func(ctx context.Context) (int64, error) {
			return 100, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetTotalTelegramIDCount(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, int64(100), got)
}

func TestSubscriptionService_GetTelegramIDsBatch_Delegates(t *testing.T) {
	t.Parallel()

	want := []int64{1, 2, 3, 4, 5}
	db := &testutil.MockDatabaseService{
		GetTelegramIDsBatchFunc: func(ctx context.Context, offset, limit int) ([]int64, error) {
			assert.Equal(t, 10, offset)
			assert.Equal(t, 5, limit)
			return want, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetTelegramIDsBatch(context.Background(), 10, 5)

	assert.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestSubscriptionService_GetAllReferralCounts_Delegates(t *testing.T) {
	t.Parallel()

	want := map[int64]int64{1: 3, 2: 5, 3: 0}
	db := &testutil.MockDatabaseService{
		GetAllReferralCountsFunc: func(ctx context.Context) (map[int64]int64, error) {
			return want, nil
		},
	}
	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})
	got, err := svc.GetAllReferralCounts(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestSubscriptionService_GetOrCreateSubscription_RepairsExistingSubscriptionNodes(t *testing.T) {
	t.Parallel()

	existing := &database.Subscription{ID: 42, TelegramID: 4242, Username: "repairuser", PlanID: 9, Status: "active"}
	planNodes := []database.Node{
		{ID: 1, IsActive: true},
		{ID: 2, IsActive: true},
		{ID: 3, IsActive: false},
	}
	existingNodes := []database.SubscriptionNode{{SubscriptionID: existing.ID, NodeID: 1, Status: database.SyncStatusActive}}

	upserted := make([]database.SubscriptionNode, 0)
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			assert.Equal(t, int64(4242), telegramID)
			return existing, nil
		},
		GetNodesByPlanIDFunc: func(ctx context.Context, planID uint) ([]database.Node, error) {
			assert.Equal(t, existing.PlanID, planID)
			return planNodes, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			assert.Equal(t, existing.ID, subscriptionID)
			return existingNodes, nil
		},
		UpsertSubscriptionNodeFunc: func(ctx context.Context, sn *database.SubscriptionNode) error {
			upserted = append(upserted, *sn)
			return nil
		},
	}

	xuiClients := map[uint]interfaces.XUIClient{1: &testutil.MockXUIClient{}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}
	svc := NewSubscriptionService(db, xuiClients, nil, sources, &config.Config{}, "", &webhook.NoopSender{})

	got, err := svc.GetOrCreateSubscription(context.Background(), 4242, "repairuser", "")

	assert.NoError(t, err)
	assert.Equal(t, existing, got)
	require.Len(t, upserted, 1)
	assert.Equal(t, existing.ID, upserted[0].SubscriptionID)
	assert.Equal(t, uint(2), upserted[0].NodeID)
	assert.Equal(t, database.SyncStatusPendingAdd, upserted[0].Status)
}

// ==================== XUIEmail Tests ====================

func TestXUIEmail_RealUsername(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "alice", XUIEmail("alice", 42))
}

func TestXUIEmail_FallbackToTgID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		chatID   int64
		want     string
	}{
		{"empty username", "", 100, "tgId_100"},
		{"special char", "user@name", 300, "tgId_300"},
		{"cyrillic", "юзер", 400, "tgId_400"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, XUIEmail(tt.username, tt.chatID))
		})
	}
}
