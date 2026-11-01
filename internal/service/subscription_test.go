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
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	err := testutil.InitLogger(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func TestSubscriptionService_Create_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.ServiceStoreFake{
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
	clients := NodeClients{}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Subscription)
	assert.NotEmpty(t, result.Subscription.ClientID)
	assert.NotEmpty(t, result.Subscription.SubscriptionID)
	assert.Equal(t, int64(123456), result.Subscription.TelegramID)
	assert.Equal(t, "testuser", result.Subscription.Username)
}

func TestSubscriptionService_Create_NoNodesDBFirst(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.ServiceStoreFake{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, database.ErrSubscriptionNotFound
		},
		GetAnyByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, database.ErrSubscriptionNotFound
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
	clients := NodeClients{}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	// DB-first, no nodes: Create succeeds, and the 3x-ui layer is never touched.
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestSubscriptionService_Create_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.ServiceStoreFake{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			return errors.New("database error")
		},
	}
	sources := []database.Node{}
	clients := NodeClients{}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	// The very first DB write decides the fate of the whole operation: no VPN
	// client was created, so there is nothing to roll back.
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "create subscription")
}

// TestSubscriptionService_Create_DBError_AfterRowCreationNoOrphanClient
// verifies the DB-first contract: the subscription row is persisted by the
// transaction, then a DB error while preparing node bindings must surface; and
// critically no VPN panel client may be created/left orphaned (the sync layer
// is never reached because node readiness failed).
func TestSubscriptionService_Create_DBError_AfterRowCreationNoOrphanClient(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.ServiceStoreFake{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			sub.ID = 1

			return nil
		},
		GetNodesByPlanIDFunc: func(ctx context.Context, planID uint) ([]database.Node, error) {
			return []database.Node{{ID: 1, Name: "db-err-node", IsActive: true, InboundIDs: `[1]`, Host: "http://node"}}, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, errors.New("database error")
		},
	}

	vpnClient := &mockVPNClient{}
	nodes := []database.Node{{ID: 1, Name: "db-err-node", IsActive: true, InboundIDs: `[1]`, Host: "http://node"}}
	clients := NodeClients{1: {Provision: vpnClient}}

	svc := NewSubscriptionService(db, clients, nodes, cfg)
	svc.SetSyncService(NewSyncService(db, clients, nodes))

	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	// The DB write landed, but the node-binding preparation fails afterwards.
	// The error must surface with a nil result, and — because provisioning never
	// reached the panel — no VPN client may be created (nothing to roll back, no
	// orphan client left on the panel).
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "load subscription nodes")
	assert.False(t, vpnClient.createCalled, "no VPN client may be provisioned when the node-binding preparation fails")
	assert.False(t, vpnClient.deleteCalled, "no rollback delete is needed: the panel was never touched")
}

func TestSubscriptionService_Create_PropagatesInviteCodeToDB(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	var (
		gotSub        *database.Subscription
		gotInviteCode string
	)

	db := &testutil.ServiceStoreFake{
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
	clients := NodeClients{}

	svc := NewSubscriptionService(db, clients, sources, cfg)
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

	db := &testutil.ServiceStoreFake{
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
	clients := NodeClients{}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "", gotInviteCode)
	assert.Equal(t, int64(0), result.ReferrerTGID)
}

func TestSubscriptionService_Create_FillsFallbackUsername(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	var gotSub *database.Subscription

	db := &testutil.ServiceStoreFake{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			gotSub = sub
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

	svc := NewSubscriptionService(db, nil, nil, cfg)
	result, err := svc.Create(context.Background(), 123456, "", "")

	assert.NoError(t, err)
	require.NotNil(t, gotSub)
	assert.Equal(t, "tgId_123456", gotSub.Username)
	assert.Equal(t, "tgId_123456", result.Subscription.Username)
}

func TestSubscriptionService_GetWithTraffic_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		PlanID:     1,
		CreatedAt:  time.Now(),
		ExpiresAt:  testutil.PtrTime(time.Now().Add(7 * 24 * time.Hour)),
	}

	db := &testutil.ServiceStoreFake{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: 1, TrafficLimit: 100 * 1024 * 1024 * 1024}, nil
		},
	}

	xuiClient := &testutil.XUIClient{
		GetClientTrafficFunc: func(ctx context.Context, username string) (*xui.ClientTraffic, error) {
			return &xui.ClientTraffic{Up: 1024 * 1024 * 1024, Down: 2 * 1024 * 1024 * 1024}, nil
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	clients := NodeClients{1: {Reader: xuiClient}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
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

	db := &testutil.ServiceStoreFake{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
	}

	xuiClient := &testutil.XUIClient{
		GetClientTrafficFunc: func(ctx context.Context, username string) (*xui.ClientTraffic, error) {
			return nil, errors.New("connection refused")
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	clients := NodeClients{1: {Reader: xuiClient}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
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

	db := &testutil.ServiceStoreFake{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: name, TrafficLimit: 1073741824}, nil
		},
	}

	var gotResetDays = -1

	xuiClient := &testutil.XUIClient{
		AddClientWithIDFunc: func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
			gotResetDays = req.ResetDays
			return &xui.ClientConfig{ID: req.ClientID, SubID: req.SubID}, nil
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	clients := NodeClients{1: {Provision: vpn.NewThreeXUIClient(xuiClient, []int{1}), Reader: xuiClient}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.SubID)
	assert.NotEmpty(t, result.ClientID)
	assert.Equal(t, 0, gotResetDays, "trial must not auto-renew (resetDays=0)")
}

func TestSubscriptionService_CreateTrial_XUIError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TrialDurationHours: 3,
	}

	db := &testutil.ServiceStoreFake{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: "trial", TrafficLimit: 1073741824}, nil
		},
		GetNodesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Node, error) {
			return []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}, nil
		},
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
			inviteVal := inviteCode
			return &database.Subscription{InviteCode: &inviteVal, SubscriptionID: subscriptionID, ClientID: clientID}, nil
		},
	}
	xuiClient := &testutil.XUIClient{
		AddClientWithIDFunc: func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
			return nil, errors.New("xui error")
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	clients := NodeClients{1: {Provision: vpn.NewThreeXUIClient(xuiClient, []int{1}), Reader: xuiClient}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
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
	db := &testutil.ServiceStoreFake{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: name, TrafficLimit: 1073741824}, nil
		},
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
			return nil, errors.New("db error")
		},
	}
	xuiClient := &testutil.XUIClient{
		AddClientWithIDFunc: func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: req.ClientID, SubID: req.SubID}, nil
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
	clients := NodeClients{1: {Provision: vpn.NewThreeXUIClient(xuiClient, []int{1}), Reader: xuiClient}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
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

	db := &testutil.ServiceStoreFake{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: name, TrafficLimit: 1073741824}, nil
		},
		GetNodesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Node, error) {
			return nil, nil
		},
	}
	xuiClient := &testutil.XUIClient{
		AddClientWithIDFunc: func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: req.ClientID, SubID: req.SubID}, nil
		},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"},
	}
	clients := NodeClients{1: {Provision: vpn.NewThreeXUIClient(xuiClient, []int{1}), Reader: xuiClient}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no linked nodes")
}

// TestSubscriptionService_ReconcileOrphanedClients_RevokesFullyDeprovisioned verifies
// a subscription with only pending_remove node bindings (deprovisioning that did
// not complete in the delete flow) is REVOKED (policy: subscriptions are never
// deleted except via admin /del), while:
//   - a subscription with a live (active) binding is kept,
//   - a subscription with NO subscription_nodes (e.g. a trial) is kept.
func TestSubscriptionService_ReconcileOrphanedClients_RevokesFullyDeprovisioned(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	withLive := &database.Subscription{ID: 10, TelegramID: 100, Username: "liveuser", Status: "active"}
	orphan := &database.Subscription{ID: 20, TelegramID: 200, Username: "orphanuser", Status: "active"}
	trial := &database.Subscription{ID: 30, TelegramID: 0, SubscriptionID: "trial123", Status: "active"}
	inactive := &database.Subscription{ID: 40, TelegramID: 400, Username: "bad", Status: "revoked"}

	updated := make(map[uint]string)
	db := &testutil.ServiceStoreFake{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return []database.Subscription{*withLive, *orphan, *trial, *inactive}, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			switch subscriptionID {
			case withLive.ID:
				return []database.SubscriptionNode{
					{SubscriptionID: withLive.ID, NodeID: 1, Status: database.SyncStatusActive},
				}, nil
			case orphan.ID:
				return []database.SubscriptionNode{
					{SubscriptionID: orphan.ID, NodeID: 1, Status: database.SyncStatusPendingRemove},
					{SubscriptionID: orphan.ID, NodeID: 2, Status: database.SyncStatusPendingRemove},
				}, nil
			default:
				// trial (no subscription_nodes) and others.
				return nil, nil
			}
		},
		UpdateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			updated[sub.ID] = sub.Status
			return nil
		},
	}
	svc := NewSubscriptionService(db, nil, nil, cfg)

	invoked := []int64{}

	svc.SetInvalidateFunc(func(id int64) { invoked = append(invoked, id) })

	count, err := svc.ReconcileOrphanedClients(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Equal(t, string(database.SubscriptionStatusRevoked), updated[orphan.ID])
	assert.NotContains(t, updated, withLive.ID)
	assert.NotContains(t, updated, trial.ID)
	assert.Equal(t, []int64{orphan.TelegramID}, invoked)
}

// TestSubscriptionService_ReconcileOrphanedClients_NoActive verifies that an
// all-revoked set produces no revocations and no error.
func TestSubscriptionService_ReconcileOrphanedClients_NoActive(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.ServiceStoreFake{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return []database.Subscription{{ID: 1, Status: "revoked"}}, nil
		},
	}
	svc := NewSubscriptionService(db, nil, nil, cfg)

	count, err := svc.ReconcileOrphanedClients(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

// TestSubscriptionService_ReconcileOrphanedClients_UpdateError verifies that a
// failed status update on an orphaned subscription is logged and skipped (the
// row stays active for the next run), while the scan continues with the
// remaining subscriptions.
func TestSubscriptionService_ReconcileOrphanedClients_UpdateError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	orphan := &database.Subscription{ID: 20, TelegramID: 200, Username: "orphanuser", Status: "active"}
	withLive := &database.Subscription{ID: 10, TelegramID: 100, Username: "liveuser", Status: "active"}

	db := &testutil.ServiceStoreFake{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return []database.Subscription{*orphan, *withLive}, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			switch subscriptionID {
			case orphan.ID:
				return []database.SubscriptionNode{
					{SubscriptionID: orphan.ID, NodeID: 1, Status: database.SyncStatusPendingRemove},
				}, nil
			case withLive.ID:
				return []database.SubscriptionNode{
					{SubscriptionID: withLive.ID, NodeID: 1, Status: database.SyncStatusActive},
				}, nil
			default:
				return nil, nil
			}
		},
		UpdateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			return errors.New("db write failed")
		},
	}
	svc := NewSubscriptionService(db, nil, nil, cfg)

	count, err := svc.ReconcileOrphanedClients(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 0, count, "failed revoke must not count as processed")
}

// TestSubscriptionService_ReconcileOrphanedClients_InvalidatesBySubID verifies
// that a successfully revoked orphan also invalidates the subserver cache keyed
// by subscription_id (so the stale config is dropped and /sub/:id serves 404).
func TestSubscriptionService_ReconcileOrphanedClients_InvalidatesBySubID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	orphan := &database.Subscription{ID: 20, TelegramID: 200, Username: "orphanuser", SubscriptionID: "sub-orphan", Status: "active"}

	db := &testutil.ServiceStoreFake{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return []database.Subscription{*orphan}, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{
				{SubscriptionID: orphan.ID, NodeID: 1, Status: database.SyncStatusPendingRemove},
			}, nil
		},
		UpdateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			assert.Equal(t, string(database.SubscriptionStatusRevoked), sub.Status)
			return nil
		},
	}
	svc := NewSubscriptionService(db, nil, nil, cfg)

	invokedSubIDs := []string{}

	svc.SetInvalidateBySubIDFunc(func(subID string) { invokedSubIDs = append(invokedSubIDs, subID) })

	count, err := svc.ReconcileOrphanedClients(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Equal(t, []string{"sub-orphan"}, invokedSubIDs)
}

// TestSubscriptionService_BindTrial_SingleNode_ErrorPropagated verifies the
// single-node trial contract: BindTrial only touches nodes[0] (the node where
// CreateTrial provisioned the client). A failure on that node must be returned
// to the caller, not swallowed, and other trial nodes must not be contacted.
func TestSubscriptionService_BindTrial_SingleNode_ErrorPropagated(t *testing.T) {
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

	db := &testutil.ServiceStoreFake{
		GetTrialSubscriptionBySubIDFunc: func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
			return bound, nil
		},
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

	xui1Calls := 0
	xui2Calls := 0

	var gotReq vpn.SubscriptionProvision

	xui1 := &testutil.XUIClient{
		UpdateClientFunc: func(ctx context.Context, req xui.ClientRequest) error {
			xui1Calls++
			gotReq = vpn.SubscriptionProvision{
				ClientID:     req.ClientID,
				CurrentEmail: req.CurrentEmail,
				Username:     req.Email,
				SubID:        req.SubID,
				TrafficBytes: req.TrafficBytes,
				ExpiryTime:   req.ExpiryTime,
				ResetDays:    req.ResetDays,
				TgID:         req.TgID,
				Comment:      req.Comment,
			}

			return errors.New("source 1 unreachable")
		},
	}
	xui2 := &testutil.XUIClient{
		UpdateClientFunc: func(ctx context.Context, req xui.ClientRequest) error {
			xui2Calls++
			return nil
		},
	}

	clients := NodeClients{
		1: {Provision: vpn.NewThreeXUIClient(xui1, []int{1}), Reader: xui1},
		2: {Provision: vpn.NewThreeXUIClient(xui2, []int{1}), Reader: xui2},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"},
		{ID: 2, IsActive: true, Host: "http://x2", InboundIDs: "[1]"},
	}

	svc := NewSubscriptionService(db, clients, sources, cfg)

	got, err := svc.BindTrial(context.Background(), "trial-sub-1", 123456, "testuser")
	require.Error(t, err, "BindTrial must propagate UpdateClient failure on nodes[0]")
	assert.Contains(t, err.Error(), "update trial client on node 1")
	// The DB binding has not happened because panel sync failed first.
	assert.Nil(t, got)
	assert.Equal(t, 1, xui1Calls, "only nodes[0] must be contacted")
	assert.Equal(t, 0, xui2Calls, "nodes[1] must not be contacted (single-node trial contract)")
	assert.Equal(t, "trial_trial-sub-1", gotReq.CurrentEmail, "CurrentEmail must be trial_ + subscriptionID")
	assert.Equal(t, "testuser", gotReq.Username, "Username must be the resolved XUIEmail (username)")
}

func TestSubscriptionService_BindTrial_ReferrerErrorStopsProvisioning(t *testing.T) {
	t.Parallel()

	bound := &database.Subscription{
		ID:             42,
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "client-xyz",
		SubscriptionID: "trial-sub-1",
		Status:         "active",
		PlanID:         2,
		InviteCode:     testutil.PtrString("REFER123"),
	}
	updateCalls := 0
	bindCalls := 0

	db := &testutil.ServiceStoreFake{
		GetTrialSubscriptionBySubIDFunc: func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
			return bound, nil
		},
		BindTrialSubscriptionFunc: func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
			bindCalls++
			return bound, nil
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: database.FreePlanName, TrafficLimit: 1024}, nil
		},
		GetInviteByCodeFunc: func(ctx context.Context, code string) (*database.Invite, error) {
			return nil, context.Canceled
		},
	}

	client := &testutil.XUIClient{
		UpdateClientFunc: func(ctx context.Context, req xui.ClientRequest) error {
			updateCalls++
			return nil
		},
	}
	node := database.Node{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"}
	svc := NewSubscriptionService(db, NodeClients{1: {Provision: vpn.NewThreeXUIClient(client, []int{1})}}, []database.Node{node}, &config.Config{})

	got, err := svc.BindTrial(context.Background(), "trial-sub-1", 123456, "testuser")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, got)
	assert.Zero(t, bindCalls, "referrer lookup errors must not commit the trial binding")
	assert.Zero(t, updateCalls, "referrer lookup errors must not send an empty Comment to the panel")
}

// TestSubscriptionService_BindTrial_SingleNode_Success verifies the happy path
// of the single-node trial contract: exactly one UpdateClient on nodes[0].
func TestSubscriptionService_BindTrial_SingleNode_Success(t *testing.T) {
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

	db := &testutil.ServiceStoreFake{
		GetTrialSubscriptionBySubIDFunc: func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
			return bound, nil
		},
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

	xui1Calls := 0
	xui2Calls := 0

	var gotReq vpn.SubscriptionProvision

	xui1 := &testutil.XUIClient{
		UpdateClientFunc: func(ctx context.Context, req xui.ClientRequest) error {
			xui1Calls++
			gotReq = vpn.SubscriptionProvision{
				ClientID:     req.ClientID,
				CurrentEmail: req.CurrentEmail,
				Username:     req.Email,
				SubID:        req.SubID,
				TrafficBytes: req.TrafficBytes,
				ExpiryTime:   req.ExpiryTime,
				ResetDays:    req.ResetDays,
				TgID:         req.TgID,
				Comment:      req.Comment,
			}

			return nil
		},
	}
	xui2 := &testutil.XUIClient{
		UpdateClientFunc: func(ctx context.Context, req xui.ClientRequest) error {
			xui2Calls++
			return nil
		},
	}

	clients := NodeClients{
		1: {Provision: vpn.NewThreeXUIClient(xui1, []int{1}), Reader: xui1},
		2: {Provision: vpn.NewThreeXUIClient(xui2, []int{1}), Reader: xui2},
	}
	sources := []database.Node{
		{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"},
		{ID: 2, IsActive: true, Host: "http://x2", InboundIDs: "[1]"},
	}

	svc := NewSubscriptionService(db, clients, sources, cfg)

	got, err := svc.BindTrial(context.Background(), "trial-sub-1", 123456, "testuser")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, uint(42), got.ID)
	assert.Equal(t, 1, xui1Calls, "exactly one UpdateClient on nodes[0]")
	assert.Equal(t, 0, xui2Calls, "nodes[1] must not be contacted")
	assert.Equal(t, "trial_trial-sub-1", gotReq.CurrentEmail, "CurrentEmail must be trial_ + subscriptionID")
	assert.Equal(t, "testuser", gotReq.Username, "Username must be the resolved XUIEmail (username)")
}

// TestSubscriptionService_BindTrial_DBBindFailureRollsBack verifies the
// two-phase contract: after the panel update succeeds, a failed DB bind rolls
// the panel client back to its anonymous trial identity when the row is still
// unbound (TelegramID < 0).
func TestSubscriptionService_BindTrial_DBBindFailureRollsBack(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 3}

	trialExpiresAt := time.Now().UTC().Add(3 * time.Hour)
	unboundTrial := &database.Subscription{
		ID:             42,
		TelegramID:     -5,
		ClientID:       "client-xyz",
		SubscriptionID: "trial-sub-1",
		Status:         "active",
		PlanID:         2,
		ExpiresAt:      &trialExpiresAt,
	}

	lookupCalls := 0
	db := &testutil.ServiceStoreFake{
		GetTrialSubscriptionBySubIDFunc: func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
			lookupCalls++
			// First call: load the trial. Second call: verify it is still unbound.
			return unboundTrial, nil
		},
		BindTrialSubscriptionFunc: func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
			return nil, errors.New("bind race lost")
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			switch name {
			case database.TrialPlanName:
				return &database.Plan{ID: 1, Name: database.TrialPlanName, TrafficLimit: 512 * 1024 * 1024}, nil
			default:
				return &database.Plan{ID: 2, Name: database.FreePlanName, TrafficLimit: 1024 * 1024 * 1024}, nil
			}
		},
		GetNodesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Node, error) {
			if planName == database.TrialPlanName {
				return []database.Node{{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"}}, nil
			}
			return nil, nil
		},
	}

	var provisions []vpn.SubscriptionProvision
	xui1 := &testutil.XUIClient{
		UpdateClientFunc: func(ctx context.Context, req xui.ClientRequest) error {
			provisions = append(provisions, vpn.SubscriptionProvision{
				ClientID:     req.ClientID,
				CurrentEmail: req.CurrentEmail,
				Username:     req.Email,
				SubID:        req.SubID,
				TrafficBytes: req.TrafficBytes,
				ExpiryTime:   req.ExpiryTime,
				ResetDays:    req.ResetDays,
				TgID:         req.TgID,
				Comment:      req.Comment,
			})
			return nil
		},
	}
	clients := NodeClients{1: {Provision: vpn.NewThreeXUIClient(xui1, []int{1})}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)

	got, err := svc.BindTrial(context.Background(), "trial-sub-1", 123456, "testuser")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bind trial subscription")
	assert.Nil(t, got)
	assert.Equal(t, 2, lookupCalls, "trial must be re-read after the bind failure to check state")
	require.Len(t, provisions, 2, "bound update plus rollback update")
	assert.Equal(t, "trial_trial-sub-1", provisions[0].CurrentEmail, "first call binds the user identity")
	assert.Equal(t, "testuser", provisions[0].Username)
	assert.Equal(t, "testuser", provisions[1].CurrentEmail, "rollback renames the bound email back to the anonymous trial identity")
	assert.Equal(t, "trial_trial-sub-1", provisions[1].Username)
	assert.Equal(t, int64(512*1024*1024), provisions[1].TrafficBytes, "rollback must restore the trial plan traffic limit")
	assert.Equal(t, trialExpiresAt, provisions[1].ExpiryTime, "rollback must restore the original trial expiry")
}

// TestSubscriptionService_BindTrial_DBBindFailureCheckErrorSkipsRollback verifies
// the conservative branch: when the post-failure state check itself fails, the
// panel update is left intact (another binder may have won the race) and no
// rollback is attempted.
func TestSubscriptionService_BindTrial_DBBindFailureCheckErrorSkipsRollback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 3}

	trial := &database.Subscription{
		ID:             42,
		TelegramID:     -5,
		ClientID:       "client-xyz",
		SubscriptionID: "trial-sub-1",
		Status:         "active",
		PlanID:         2,
	}

	lookupCalls := 0
	db := &testutil.ServiceStoreFake{
		GetTrialSubscriptionBySubIDFunc: func(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
			lookupCalls++
			if lookupCalls > 1 {
				return nil, errors.New("db unreachable")
			}
			return trial, nil
		},
		BindTrialSubscriptionFunc: func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
			return nil, errors.New("bind race lost")
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: database.FreePlanName, TrafficLimit: 1024 * 1024 * 1024}, nil
		},
		GetNodesByPlanNameFunc: func(ctx context.Context, planName string) ([]database.Node, error) {
			if planName == database.TrialPlanName {
				return []database.Node{{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"}}, nil
			}
			return nil, nil
		},
	}

	updateCalls := 0
	xui1 := &testutil.XUIClient{
		UpdateClientFunc: func(ctx context.Context, req xui.ClientRequest) error {
			updateCalls++
			return nil
		},
	}
	clients := NodeClients{1: {Provision: vpn.NewThreeXUIClient(xui1, []int{1})}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x1", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)

	got, err := svc.BindTrial(context.Background(), "trial-sub-1", 123456, "testuser")
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, 1, updateCalls, "no rollback when the state check fails: do not undo another binder's work")
}

func TestReferrerComment(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("no invite code returns empty", func(t *testing.T) {
		db := &testutil.ServiceStoreFake{}
		comment, err := referrerComment(ctx, db, &database.Subscription{})
		require.NoError(t, err)
		assert.Empty(t, comment)
	})

	t.Run("unknown invite returns empty without error", func(t *testing.T) {
		db := &testutil.ServiceStoreFake{
			GetInviteByCodeFunc: func(ctx context.Context, code string) (*database.Invite, error) {
				return nil, database.ErrInviteNotFound
			},
		}
		sub := &database.Subscription{InviteCode: testutil.PtrString("REFER123")}
		comment, err := referrerComment(ctx, db, sub)
		require.NoError(t, err)
		assert.Empty(t, comment)
	})

	t.Run("unknown referrer returns empty without error", func(t *testing.T) {
		db := &testutil.ServiceStoreFake{
			GetInviteByCodeFunc: func(ctx context.Context, code string) (*database.Invite, error) {
				return &database.Invite{Code: code, ReferrerTGID: 555}, nil
			},
			GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return nil, database.ErrSubscriptionNotFound
			},
		}
		sub := &database.Subscription{InviteCode: testutil.PtrString("REFER123")}
		comment, err := referrerComment(ctx, db, sub)
		require.NoError(t, err)
		assert.Empty(t, comment)
	})

	t.Run("context error is wrapped", func(t *testing.T) {
		db := &testutil.ServiceStoreFake{
			GetInviteByCodeFunc: func(ctx context.Context, code string) (*database.Invite, error) {
				return nil, context.DeadlineExceeded
			},
		}
		sub := &database.Subscription{InviteCode: testutil.PtrString("REFER123")}
		comment, err := referrerComment(ctx, db, sub)
		assert.Empty(t, comment)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Contains(t, err.Error(), "load invite by code")
	})

	t.Run("referrer error is wrapped", func(t *testing.T) {
		loadErr := errors.New("database unavailable")
		db := &testutil.ServiceStoreFake{
			GetInviteByCodeFunc: func(ctx context.Context, code string) (*database.Invite, error) {
				return &database.Invite{Code: code, ReferrerTGID: 555}, nil
			},
			GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return nil, loadErr
			},
		}
		sub := &database.Subscription{InviteCode: testutil.PtrString("REFER123")}
		comment, err := referrerComment(ctx, db, sub)
		assert.Empty(t, comment)
		require.Error(t, err)
		assert.ErrorIs(t, err, loadErr)
		assert.Contains(t, err.Error(), "load referrer subscription")
	})

	t.Run("referrer found formats comment", func(t *testing.T) {
		db := &testutil.ServiceStoreFake{
			GetInviteByCodeFunc: func(ctx context.Context, code string) (*database.Invite, error) {
				return &database.Invite{Code: code, ReferrerTGID: 555}, nil
			},
			GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return &database.Subscription{TelegramID: telegramID, Username: "referrer"}, nil
			},
		}
		sub := &database.Subscription{InviteCode: testutil.PtrString("REFER123")}
		comment, err := referrerComment(ctx, db, sub)
		require.NoError(t, err)
		assert.Equal(t, "from: @referrer", comment)
	})
}

// ==================== DeleteByID Tests ====================

func TestSubscriptionService_DeleteByID_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 7, TelegramID: 42, Username: "u", ClientID: "c1", SubscriptionID: "s1"}, nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			assert.Equal(t, uint(7), id)
			return &database.Subscription{ID: 7, TelegramID: 42, Username: "u", ClientID: "c1", SubscriptionID: "s1"}, nil
		},
	}
	xuiClient := &testutil.XUIClient{
		DeleteClientFunc: func(ctx context.Context, email string) error {
			assert.NotEmpty(t, email)
			return nil
		},
	}
	clients := NodeClients{1: {Reader: xuiClient}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	deleted, err := svc.DeleteByID(context.Background(), 7)

	assert.NoError(t, err)
	assert.NotNil(t, deleted)
	assert.Equal(t, uint(7), deleted.ID)
}

func TestSubscriptionService_DeleteByID_GetError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	clients := NodeClients{1: {Reader: &testutil.XUIClient{}}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	deleted, err := svc.DeleteByID(context.Background(), 1)

	assert.Error(t, err)
	assert.Nil(t, deleted)
	assert.Contains(t, err.Error(), "get subscription")
}

func TestSubscriptionService_DeleteByID_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, ClientID: "c", SubscriptionID: "s", TelegramID: 1, Username: "u"}, nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return nil, errors.New("db fail")
		},
	}
	clients := NodeClients{1: {Reader: &testutil.XUIClient{}}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	deleted, err := svc.DeleteByID(context.Background(), 1)

	assert.Error(t, err)
	assert.Nil(t, deleted)
	assert.Contains(t, err.Error(), "db delete")
}

// TestSubscriptionService_DeleteByID_MarkRevokedBeforeDBDelete verifies audit #2
// fix: the subscription is marked revoked (Phase 1) BEFORE the physical DB delete
// (Phase 3). If the DB delete fails, the subscription is already revoked so /sub/{id}
// returns 404 and the user/admin no longer sees it as active. The deprovision
// (Phase 2) is skipped when syncService is nil.
func TestSubscriptionService_DeleteByID_MarkRevokedBeforeDBDelete(t *testing.T) {
	t.Parallel()

	var (
		updatedStatus      string
		updateBeforeDelete bool
	)

	deleteCalled := false

	cfg := &config.Config{}
	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, ClientID: "c", SubscriptionID: "s", TelegramID: 1, Username: "u", Status: "active"}, nil
		},
		UpdateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			updatedStatus = sub.Status
			// Record that Update happened before Delete by checking the flag.
			if !deleteCalled {
				updateBeforeDelete = true
			}

			return nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			deleteCalled = true
			return nil, errors.New("db delete failed")
		},
	}
	clients := NodeClients{1: {Reader: &testutil.XUIClient{}}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	_, err := svc.DeleteByID(context.Background(), 1)

	// DB delete failed → error returned to caller.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db delete")

	// Phase 1 happened: subscription was marked revoked before the delete attempt.
	assert.True(t, updateBeforeDelete, "UpdateSubscription (mark revoked) must happen before DeleteSubscriptionByID")
	assert.Equal(t, "revoked", updatedStatus, "subscription must be marked revoked before deprovision/delete")
}

// TestSubscriptionService_DeleteByID_SyncFailureKeepsRetryQueue verifies that
// a failed external deprovision leaves the revoked subscription and its
// pending_remove row for the background worker instead of destroying the retry
// queue with a physical delete.
func TestSubscriptionService_DeleteByID_SyncFailureKeepsRetryQueue(t *testing.T) {
	t.Parallel()

	var deleteByIDCalled bool
	// Node binding used by MarkAllForRemoval / SyncSubscription.
	nodes := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}
	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, ClientID: "c", SubscriptionID: "s", TelegramID: 1, Username: "u", Status: "active"}, nil
		},
		// An existing active node binding so MarkAllForRemoval flips it to
		// pending_remove, and the subsequent SyncSubscription reaches the VPN
		// client's DeleteSubscription call.
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{
				{SubscriptionID: subscriptionID, NodeID: 1, Status: database.SyncStatusActive},
			}, nil
		},
		// SyncSubscription reads the pending_remove node and dispatches the
		// delete to the VPN client (which fails below).
		GetPendingBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{
				{SubscriptionID: subscriptionID, NodeID: 1, Status: database.SyncStatusPendingRemove},
			}, nil
		},
		UpdateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			return nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			deleteByIDCalled = true
			return &database.Subscription{ID: id, ClientID: "c", SubscriptionID: "s"}, nil
		},
	}

	// VPN client fails on delete → SyncSubscription reaches DeleteSubscription
	// and receives the configured "vpn delete failed" error (best-effort).
	clients := NodeClients{1: {Provision: &mockVPNClient{deleteError: errors.New("vpn delete failed")}}}

	svc := NewSubscriptionService(db, clients, nodes, &config.Config{})
	svc.SetSyncService(NewSyncService(db, clients, nodes))

	deleted, err := svc.DeleteByID(context.Background(), 1)

	// Physical delete must not run while the pending_remove retry row remains.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deprovision incomplete")
	assert.False(t, deleteByIDCalled, "physical delete must wait for successful deprovision")
	assert.Nil(t, deleted)
}

// ==================== InvalidateSubscription Tests ====================

func TestSubscriptionService_InvalidateSubscription_CallsCallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	clients := NodeClients{1: {Reader: &testutil.XUIClient{}}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(&testutil.ServiceStoreFake{}, clients, sources, cfg)

	var captured int64

	svc.SetInvalidateFunc(func(telegramID int64) {
		captured = telegramID
	})

	svc.InvalidateSubscription(context.Background(), 123)
	assert.Equal(t, int64(123), captured)
}

func TestSubscriptionService_InvalidateSubscription_NoCallback(t *testing.T) {
	t.Parallel()

	clients := NodeClients{1: {Reader: &testutil.XUIClient{}}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(&testutil.ServiceStoreFake{}, clients, sources, &config.Config{})

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
	db := &testutil.ServiceStoreFake{
		CleanupExpiredTrialsFunc: func(ctx context.Context, hours int) ([]database.Subscription, error) {
			assert.Equal(t, 24, hours)
			return expired, nil
		},
	}
	xuiClient := &testutil.XUIClient{
		DeleteClientFunc: func(ctx context.Context, email string) error {
			assert.True(t, strings.HasPrefix(email, "trial_"))
			return nil
		},
	}
	clients := NodeClients{1: {Provision: vpn.NewThreeXUIClient(xuiClient, []int{1}), Reader: xuiClient}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	n, err := svc.CleanupExpiredTrials(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

func TestSubscriptionService_CleanupExpiredTrials_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 24}
	db := &testutil.ServiceStoreFake{
		CleanupExpiredTrialsFunc: func(ctx context.Context, hours int) ([]database.Subscription, error) {
			return nil, errors.New("db fail")
		},
	}
	clients := NodeClients{1: {Reader: &testutil.XUIClient{}}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	n, err := svc.CleanupExpiredTrials(context.Background())

	assert.Error(t, err)
	assert.Equal(t, int64(0), n)
}

// TestSubscriptionService_CleanupExpiredTrials_DeprovisionFailureKeepsRow
// verifies the two-phase cleanup: when external deprovision fails, the claimed
// trial row is NOT deleted and the failure is surfaced as an aggregate error so
// the scheduler retries on the next pass.
func TestSubscriptionService_CleanupExpiredTrials_DeprovisionFailureKeepsRow(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 24}
	expired := []database.Subscription{
		{ID: 1, ClientID: "c1", SubscriptionID: "exp1", TelegramID: 0},
	}
	deleteCalled := false
	db := &testutil.ServiceStoreFake{
		ClaimExpiredTrialsFunc: func(ctx context.Context, hours int) ([]database.Subscription, error) {
			return expired, nil
		},
		DeleteClaimedTrialFunc: func(ctx context.Context, id uint) error {
			deleteCalled = true
			return nil
		},
	}

	xuiClient := &testutil.XUIClient{
		DeleteClientFunc: func(ctx context.Context, email string) error {
			return errors.New("panel unreachable")
		},
	}
	clients := NodeClients{1: {Provision: vpn.NewThreeXUIClient(xuiClient, []int{1})}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}

	svc := NewSubscriptionService(db, clients, sources, cfg)
	n, err := svc.CleanupExpiredTrials(context.Background())

	assert.Error(t, err, "deprovision failure must be surfaced")
	assert.Contains(t, err.Error(), "cleanup trial exp1")
	assert.Equal(t, int64(0), n)
	assert.False(t, deleteCalled, "the row must stay claimed for the next retry")
}

// TestSubscriptionService_ReconcileOrphanedClients_RecreatesMissingQueue
// verifies the recovery branch: an active non-trial subscription without any
// node bindings gets its pending_add queue recreated instead of being revoked.
func TestSubscriptionService_ReconcileOrphanedClients_RecreatesMissingQueue(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	sub := &database.Subscription{ID: 10, TelegramID: 100, Username: "paiduser", SubscriptionID: "sub-paid", Status: "active", PlanID: 5}

	upserted := make([]database.SubscriptionNode, 0)
	updated := make(map[uint]string)

	db := &testutil.ServiceStoreFake{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return []database.Subscription{*sub}, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil // queue was lost
		},
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: 5, Name: "paid-plan"}, nil
		},
		GetNodesByPlanIDFunc: func(ctx context.Context, planID uint) ([]database.Node, error) {
			return []database.Node{{ID: 1, IsActive: true, Host: "http://n1", InboundIDs: "[1]"}}, nil
		},
		UpsertSubscriptionNodeFunc: func(ctx context.Context, sn *database.SubscriptionNode) error {
			upserted = append(upserted, *sn)
			return nil
		},
		UpdateSubscriptionFunc: func(ctx context.Context, s *database.Subscription) error {
			updated[s.ID] = s.Status
			return nil
		},
	}
	svc := NewSubscriptionService(db, nil, nil, cfg)

	count, err := svc.ReconcileOrphanedClients(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 0, count, "a recoverable subscription must not be revoked")
	require.Len(t, upserted, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, upserted[0].Status)
	assert.Equal(t, sub.ID, upserted[0].SubscriptionID)
	assert.Empty(t, updated, "the subscription status must not change")
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
	db := &testutil.ServiceStoreFake{
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

	clients := NodeClients{1: {Reader: &testutil.XUIClient{}}}
	sources := []database.Node{{ID: 1, IsActive: true, Host: "http://x", InboundIDs: "[1]"}}
	svc := NewSubscriptionService(db, clients, sources, &config.Config{})

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

func TestSubscriptionService_Create_ReanimatesRevoked_ReturnsCleanupError(t *testing.T) {
	t.Parallel()

	cleanupErr := errors.New("subscription nodes database unavailable")
	existing := &database.Subscription{
		ID:             42,
		TelegramID:     777778,
		ClientID:       "client-revoked-error",
		SubscriptionID: "sub-revoked-error",
		PlanID:         1,
		Status:         string(database.SubscriptionStatusRevoked),
	}
	updateCalled := false

	db := &testutil.ServiceStoreFake{
		GetByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return nil, database.ErrSubscriptionNotFound
		},
		GetAnyByTelegramIDFunc: func(context.Context, int64) (*database.Subscription, error) {
			return existing, nil
		},
		GetPlanByNameFunc: func(context.Context, string) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: database.FreePlanName}, nil
		},
		DeleteSubscriptionNodesBySubscriptionIDFunc: func(context.Context, uint) error {
			return cleanupErr
		},
		UpdateSubscriptionFunc: func(context.Context, *database.Subscription) error {
			updateCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, nil, nil, &config.Config{})
	result, err := svc.Create(context.Background(), existing.TelegramID, "", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, cleanupErr)
	assert.Nil(t, result)
	assert.False(t, updateCalled, "failed cleanup must not activate the subscription")
}

func TestSubscriptionService_DeleteByID_KeepsRetryQueueOnDeprovisionFailure(t *testing.T) {
	t.Parallel()

	const (
		subscriptionID uint = 77
		nodeID         uint = 9
	)
	deprovisionErr := errors.New("vpn node unavailable")
	sub := &database.Subscription{
		ID:             subscriptionID,
		TelegramID:     777779,
		Username:       "delete-retry-user",
		ClientID:       "delete-retry-client",
		SubscriptionID: "delete-retry-sub",
		Status:         string(database.SubscriptionStatusActive),
	}
	pending := []database.SubscriptionNode{{
		SubscriptionID: subscriptionID,
		NodeID:         nodeID,
		Status:         database.SyncStatusPendingRemove,
	}}
	deleteClient := &mockVPNClient{deleteError: deprovisionErr}
	hardDeleteCalled := false

	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		GetNodeByIDFunc: func(context.Context, uint) (*database.Node, error) {
			return &database.Node{ID: nodeID, IsActive: true}, nil
		},
		UpdateSubscriptionFunc: func(_ context.Context, updated *database.Subscription) error {
			assert.Equal(t, database.SubscriptionStatusRevoked, database.SubscriptionStatus(updated.Status))
			return nil
		},
		GetBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return pending, nil
		},
		GetPendingBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return pending, nil
		},
		UpdateRetryFunc: func(context.Context, uint, uint, int, *time.Time, *string) error {
			return nil
		},
		DeleteSubscriptionByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			hardDeleteCalled = true
			return sub, nil
		},
	}

	syncSvc := NewSyncService(db, NodeClients{nodeID: {Provision: deleteClient}}, []database.Node{{
		ID:       nodeID,
		Type:     database.NodeType3xUI,
		IsActive: true,
	}})
	svc := NewSubscriptionService(db, nil, nil, &config.Config{})
	svc.SetSyncService(syncSvc)

	deleted, err := svc.DeleteByID(context.Background(), subscriptionID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deprovision incomplete")
	assert.Nil(t, deleted)
	assert.True(t, deleteClient.deleteCalled)
	assert.False(t, hardDeleteCalled, "subscription must remain for background retry")
}

// TestSubscriptionService_Create_ReanimatesRevoked verifies the bug fix: when a
// prior delete left the subscription in "revoked" state (partial delete failure),
// Create must reanimate that single row instead of inserting a duplicate — which
// would otherwise hit the telegram_id UNIQUE constraint and permanently block the
// user from re-subscribing.
func TestSubscriptionService_Create_ReanimatesRevoked(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	// Simulate a partially-failed delete: a row left in "revoked" with a
	// stale subscription node binding (pending_remove from the failed deprovision).
	revoked := &database.Subscription{
		TelegramID:     777777,
		Username:       "revoked_user",
		ClientID:       "client-revoked",
		SubscriptionID: "sub-revoked",
		PlanID:         1,
		Status:         "revoked",
	}
	require.NoError(t, db.CreateSubscription(ctx, revoked, ""))

	// The test DSN enables SQLite foreign-key enforcement, so the stale binding
	// below must reference an existing node row.
	staleNode := &database.Node{ID: 1, Name: "stale-node", Host: "http://stale", APIToken: "token", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(staleNode).Error)
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
		SubscriptionID: revoked.ID,
		NodeID:         staleNode.ID,
		Status:         database.SyncStatusPendingRemove,
	}))

	cfg := &config.Config{}
	sources := []database.Node{}
	clients := NodeClients{}
	svc := NewSubscriptionService(db, clients, sources, cfg)

	result, err := svc.Create(ctx, 777777, "revoked_user", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	// Exactly one row for this telegram_id, now active — no duplicate.
	all, err := db.GetAllSubscriptions(ctx)
	require.NoError(t, err)

	var matching []database.Subscription

	for _, s := range all {
		if s.TelegramID == 777777 {
			matching = append(matching, s)
		}
	}

	require.Len(t, matching, 1, "must not create a second row")
	assert.Equal(t, "active", matching[0].Status)

	freePlan, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	assert.Equal(t, freePlan.ID, matching[0].PlanID)
	assert.Nil(t, matching[0].ExpiresAt)
	assert.Equal(t, revoked.ID, matching[0].ID, "same row reanimated, not a new one")

	// Stale node bindings were wiped so re-provision starts clean.
	nodes, err := db.GetBySubscriptionID(ctx, revoked.ID)
	require.NoError(t, err)
	assert.Empty(t, nodes, "stale pending_remove node must be cleared")
}

func TestSubscriptionService_AdminSetPlan_ReconcilesNodesAndInvalidatesCache(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	// The free plan is seeded by migrations; the premium plan is created here.
	freePlan, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	premiumPlan := &database.Plan{Name: "premium-admin-setplan", DevicesLimit: 2, TrafficLimit: 1024}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(premiumPlan).Error)

	nodeA := &database.Node{Name: "setplan-a", Host: "http://a", APIToken: "a", InboundIDs: `[1]`, Type: database.NodeType3xUI}
	nodeB := &database.Node{Name: "setplan-b", Host: "http://b", APIToken: "b", InboundIDs: `[1]`, Type: database.NodeType3xUI}
	nodeC := &database.Node{Name: "setplan-c", Host: "http://c", APIToken: "c", InboundIDs: `[1]`, Type: database.NodeType3xUI}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(nodeA).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(nodeB).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(nodeC).Error)

	// Old (free) plan → nodeA + nodeC; new (premium) plan → nodeA + nodeB.
	// nodeA is shared (must be pending_update), nodeB is new (pending_add),
	// nodeC leaves the plan (pending_remove).
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: freePlan.ID, NodeID: nodeA.ID}).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: freePlan.ID, NodeID: nodeC.ID}).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: premiumPlan.ID, NodeID: nodeA.ID}).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: premiumPlan.ID, NodeID: nodeB.ID}).Error)

	sub := &database.Subscription{
		TelegramID:     999802,
		Username:       "adminsetplan",
		ClientID:       "c-adminsetplan",
		SubscriptionID: "s-adminsetplan",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         freePlan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(5 * 24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: nodeA.ID, Status: database.SyncStatusActive}))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: nodeC.ID, Status: database.SyncStatusActive}))

	var invalidatedTGIDs []int64
	var invalidatedSubIDs []string

	svc := NewSubscriptionService(db, nil, nil, &config.Config{})
	svc.SetInvalidateFunc(func(id int64) { invalidatedTGIDs = append(invalidatedTGIDs, id) })
	svc.SetInvalidateBySubIDFunc(func(id string) { invalidatedSubIDs = append(invalidatedSubIDs, id) })
	// No VPN clients wired: SyncSubscription treats nodes as unavailable and
	// keeps them pending (best-effort), leaving the reconciled state visible.
	svc.SetSyncService(NewSyncService(db, NodeClients{}, nil))

	updated, err := svc.AdminSetPlan(ctx, sub.ID, premiumPlan.ID, 30)
	require.NoError(t, err)

	assert.Equal(t, premiumPlan.ID, updated.PlanID)
	assert.Equal(t, string(database.SubscriptionStatusActive), updated.Status)
	require.NotNil(t, updated.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(30*24*time.Hour), *updated.ExpiresAt, time.Minute)

	// Cache invalidated by both keys.
	assert.Contains(t, invalidatedTGIDs, sub.TelegramID)
	assert.Contains(t, invalidatedSubIDs, sub.SubscriptionID)

	// Bindings reconciled per plan membership.
	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 3)

	byNode := make(map[uint]database.SyncStatus, len(rows))
	for _, r := range rows {
		byNode[r.NodeID] = r.Status
	}
	assert.Equal(t, database.SyncStatusPendingUpdate, byNode[nodeA.ID], "shared node must get pending_update")
	assert.Equal(t, database.SyncStatusPendingAdd, byNode[nodeB.ID], "new plan node must get pending_add")
	assert.Equal(t, database.SyncStatusPendingRemove, byNode[nodeC.ID], "leaving node must get pending_remove")
}

func TestSubscriptionService_AdminSetPlan_ExpirySemantics(t *testing.T) {
	t.Parallel()

	nearFuture := time.Now().Add(10 * 24 * time.Hour).Truncate(time.Second)
	past := time.Now().Add(-24 * time.Hour)

	tests := []struct {
		name      string
		planName  string
		days      int
		existing  *time.Time
		wantNil   bool
		wantDelta time.Duration // 0 means "preserve existing"
	}{
		{name: "explicit days", planName: "premium", days: 30, existing: testutil.PtrTime(nearFuture), wantDelta: 30 * 24 * time.Hour},
		{name: "future expiry preserved", planName: "premium", days: 0, existing: testutil.PtrTime(nearFuture), wantDelta: 0},
		{name: "nil expiry gets default", planName: "premium", days: 0, existing: nil, wantDelta: 30 * 24 * time.Hour},
		{name: "past expiry gets default", planName: "premium", days: 0, existing: testutil.PtrTime(past), wantDelta: 30 * 24 * time.Hour},
		{name: "free plan clears expiry", planName: database.FreePlanName, days: 30, existing: testutil.PtrTime(nearFuture), wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()

			sub := &database.Subscription{
				ID:             42,
				TelegramID:     999803,
				Username:       "expiry-semantics",
				ClientID:       "c-expiry",
				SubscriptionID: "s-expiry",
				Status:         string(database.SubscriptionStatusActive),
				PlanID:         1,
				ExpiresAt:      tt.existing,
			}

			var captured *database.Subscription

			db := &testutil.ServiceStoreFake{
				GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
					return sub, nil
				},
				GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
					return &database.Plan{ID: 2, Name: tt.planName}, nil
				},
				UpdateSubscriptionFunc: func(_ context.Context, updated *database.Subscription) error {
					captured = updated
					return nil
				},
			}

			svc := NewSubscriptionService(db, nil, nil, &config.Config{})

			updated, err := svc.AdminSetPlan(context.Background(), sub.ID, 2, tt.days)
			require.NoError(t, err)
			require.NotNil(t, captured, "UpdateSubscription must be called")

			assert.Equal(t, uint(2), updated.PlanID)
			assert.Equal(t, string(database.SubscriptionStatusActive), updated.Status)
			assert.Equal(t, 0, updated.RemindersSent, "reminder bitmask must reset for the new expiry")

			if tt.wantNil {
				assert.Nil(t, captured.ExpiresAt, "free plan must clear expiry")
			} else if tt.wantDelta == 0 {
				require.NotNil(t, captured.ExpiresAt)
				assert.Equal(t, tt.existing, captured.ExpiresAt, "future expiry must be preserved")
			} else {
				require.NotNil(t, captured.ExpiresAt)
				assert.WithinDuration(t, now.Add(tt.wantDelta), *captured.ExpiresAt, time.Minute)
			}
		})
	}
}

func TestSubscriptionService_AdminSetPlan_Errors(t *testing.T) {
	t.Parallel()

	sub := &database.Subscription{
		ID:             42,
		TelegramID:     999804,
		Username:       "adminsetplan-errors",
		ClientID:       "c-errors",
		SubscriptionID: "s-errors",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         1,
	}

	t.Run("subscription not found", func(t *testing.T) {
		db := &testutil.ServiceStoreFake{
			GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
				return nil, database.ErrSubscriptionNotFound
			},
		}
		svc := NewSubscriptionService(db, nil, nil, &config.Config{})

		_, err := svc.AdminSetPlan(context.Background(), 42, 2, 30)
		require.Error(t, err)
		assert.ErrorIs(t, err, database.ErrSubscriptionNotFound)
		assert.Contains(t, err.Error(), "load subscription")
	})

	t.Run("plan not found", func(t *testing.T) {
		db := &testutil.ServiceStoreFake{
			GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
				return sub, nil
			},
			GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
				return nil, database.ErrPlanNotFound
			},
		}
		svc := NewSubscriptionService(db, nil, nil, &config.Config{})

		_, err := svc.AdminSetPlan(context.Background(), 42, 2, 30)
		require.Error(t, err)
		assert.ErrorIs(t, err, database.ErrPlanNotFound)
		assert.Contains(t, err.Error(), "load plan")
	})

	t.Run("apply plan failure surfaces to caller", func(t *testing.T) {
		applyErr := errors.New("reconcile plan nodes database unavailable")

		db := &testutil.ServiceStoreFake{
			GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
				return sub, nil
			},
			GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
				return &database.Plan{ID: 2, Name: "premium-errors"}, nil
			},
			UpdateSubscriptionFunc: func(context.Context, *database.Subscription) error {
				return nil
			},
			GetNodesByPlanIDFunc: func(context.Context, uint) ([]database.Node, error) {
				return nil, applyErr
			},
		}

		svc := NewSubscriptionService(db, nil, nil, &config.Config{})
		svc.SetSyncService(NewSyncService(db, nil, nil))

		_, err := svc.AdminSetPlan(context.Background(), 42, 2, 30)
		require.Error(t, err)
		assert.ErrorIs(t, err, applyErr, "DB-setup phase failure must surface, not be swallowed")
	})
}

func TestSubscriptionService_AdminSetPlan_FreePlanClearsPaidState(t *testing.T) {
	t.Parallel()

	productID := uint(7)
	currency := "RUB"
	started := time.Now().Add(-48 * time.Hour)
	expiry := time.Now().Add(30 * 24 * time.Hour)

	sub := &database.Subscription{
		ID:             42,
		TelegramID:     999805,
		Username:       "adminsetplan-free",
		ClientID:       "c-free",
		SubscriptionID: "s-free",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         1,
		ExpiresAt:      &expiry,
		ProductID:      &productID,
		StartedAt:      &started,
		PricePaidCents: 2300,
		Currency:       &currency,
	}

	var captured *database.Subscription

	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: database.FreePlanName}, nil
		},
		UpdateSubscriptionFunc: func(_ context.Context, updated *database.Subscription) error {
			captured = updated
			return nil
		},
	}

	svc := NewSubscriptionService(db, nil, nil, &config.Config{})

	updated, err := svc.AdminSetPlan(context.Background(), sub.ID, 2, 30)
	require.NoError(t, err)
	require.NotNil(t, captured, "UpdateSubscription must be called")

	assert.Equal(t, uint(2), updated.PlanID)
	assert.Equal(t, string(database.SubscriptionStatusActive), updated.Status)
	assert.Nil(t, captured.ExpiresAt, "free plan must clear expiry")
	assert.Nil(t, captured.ProductID, "free plan must clear product reference")
	assert.Nil(t, captured.StartedAt, "free plan must clear started_at")
	assert.Zero(t, captured.PricePaidCents, "free plan must clear paid amount")
	assert.Nil(t, captured.Currency, "free plan must clear currency")
}

func TestSubscriptionService_AdminSetPlan_DaysTooLarge(t *testing.T) {
	t.Parallel()

	sub := &database.Subscription{
		ID:             42,
		TelegramID:     999806,
		Username:       "adminsetplan-days",
		ClientID:       "c-days",
		SubscriptionID: "s-days",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         1,
	}

	updateCalled := false

	db := &testutil.ServiceStoreFake{
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: "premium-days"}, nil
		},
		UpdateSubscriptionFunc: func(context.Context, *database.Subscription) error {
			updateCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, nil, nil, &config.Config{})

	_, err := svc.AdminSetPlan(context.Background(), sub.ID, 2, AdminSetPlanMaxDays+1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
	assert.False(t, updateCalled, "oversized duration must fail before any DB write")
}

func TestFormatSubscriptionMessage_UsesCanonicalFields(t *testing.T) {
	traffic := &TrafficInfo{
		PlanName:           "Premium",
		LimitGB:            100,
		UsedGB:             12.5,
		Percentage:         12.5,
		ProgressBar:        "████",
		CreatedAtFormatted: "01.01.2026 12:00",
		ExpiresAtFormatted: "31.01.2026 12:00",
		ResetInfo:          "через 5 дн.",
		Available:          true,
	}

	got := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", traffic, "https://example.test/sub/abc")

	assert.Contains(t, got, "Статус: *активна*")
	assert.Contains(t, got, "Тариф: *Premium*")
	assert.Contains(t, got, "12.50 из 100 Гб (12%)")
	assert.Contains(t, got, "████")
	assert.Contains(t, got, "01.01.2026 12:00")
	assert.Contains(t, got, "31.01.2026 12:00")
	assert.Contains(t, got, "через 5 дн.")
	assert.Contains(t, got, "`https://example.test/sub/abc`")
}

func TestFormatSubscriptionMessage_DefaultsUnlimitedAndStatus(t *testing.T) {
	got := FormatSubscriptionMessage("Heading", "", &TrafficInfo{PlanName: "Free"}, "")

	assert.Contains(t, got, "Статус: *активна*")
	assert.Contains(t, got, "Тариф: *Free*")
	assert.Contains(t, got, "Трафик: неограничен")
	assert.Contains(t, got, "Сброс: нет")
}

func TestFormatSubscriptionMessage_EscapesMarkdownInPlanName(t *testing.T) {
	tests := []struct {
		name             string
		planName         string
		expectedInOutput string
	}{
		{
			name:             "underscore in plan name",
			planName:         "Premium_Pro",
			expectedInOutput: `Тариф: *Premium\_Pro*`,
		},
		{
			name:             "asterisk in plan name",
			planName:         "Plan*VIP",
			expectedInOutput: `Тариф: *Plan\*VIP*`,
		},
		{
			name:             "backtick in plan name",
			planName:         "Plan`Special",
			expectedInOutput: "Тариф: *Plan\\`Special*",
		},
		{
			name:             "opening bracket in plan name",
			planName:         "Plan[Elite]",
			expectedInOutput: `Тариф: *Plan\[Elite]*`,
		},
		{
			name:             "multiple special chars",
			planName:         "Pro_*Test*_Plan",
			expectedInOutput: `Тариф: *Pro\_\*Test\*\_Plan*`,
		},
		{
			name:             "all special chars",
			planName:         "_*`[VIP",
			expectedInOutput: "Тариф: *\\_\\*\\`\\[VIP*",
		},
		{
			name:             "normal plan name unchanged",
			planName:         "Premium Pro 2024",
			expectedInOutput: `Тариф: *Premium Pro 2024*`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			traffic := &TrafficInfo{
				PlanName:           tc.planName,
				CreatedAtFormatted: "01.01.2026",
				ExpiresAtFormatted: "31.01.2026",
			}

			got := FormatSubscriptionMessage("Test", "активна", traffic, "https://example.com/sub/test")

			assert.Contains(t, got, tc.expectedInOutput,
				"Plan name should be properly escaped in the output message")
		})
	}
}

// referenceNow is a fixed Saturday (2026-10-31, 15:30 UTC) used as "now" so
// the countdown math is deterministic. The Weekday assertion guards against a
// silently wrong calendar assumption.
func referenceNow(t *testing.T) time.Time {
	t.Helper()

	now := time.Date(2026, 10, 31, 15, 30, 0, 0, time.UTC)
	assert.Equal(t, time.Saturday, now.Weekday(), "reference date must stay a Saturday")

	return now
}

func TestDescribeTrafficReset(t *testing.T) {
	t.Parallel()

	now := referenceNow(t)

	tests := []struct {
		name     string
		profile  xui.ClientConfig
		wantText string
		wantDays int
	}{
		{
			name:     "never",
			profile:  xui.ClientConfig{TrafficReset: "never"},
			wantText: "не сбрасывается",
		},
		{
			name:     "empty period is never",
			profile:  xui.ClientConfig{},
			wantText: "не сбрасывается",
		},
		{
			name:     "monthly day 1 lands on next month first",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 1},
			wantText: "через 1 дн.",
			wantDays: 1,
		},
		{
			name:     "monthly day passed this month rolls to next month",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 30},
			wantText: "через 30 дн.",
			wantDays: 30,
		},
		{
			// The reset fires at midnight: a matching day today has already
			// passed, so the countdown rolls forward. Nov has no 31st.
			name:     "monthly day today already fired rolls forward",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 31},
			wantText: "через 61 дн.",
			wantDays: 61,
		},
		{
			name:     "monthly day 0 normalizes to 1 like the panel",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 0},
			wantText: "через 1 дн.",
			wantDays: 1,
		},
		{
			name:     "monthly day out of range is unknown",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 32},
			wantText: "неизвестно",
		},
		{
			name:     "monthly negative day is unknown",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: -3},
			wantText: "неизвестно",
		},
		{
			name:     "daily is tomorrow",
			profile:  xui.ClientConfig{TrafficReset: "daily"},
			wantText: "через 1 дн.",
			wantDays: 1,
		},
		{
			name:     "weekly 1 is next Monday",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 1},
			wantText: "через 2 дн.",
			wantDays: 2,
		},
		{
			name:     "weekly 7 is next Sunday",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 7},
			wantText: "через 1 дн.",
			wantDays: 1,
		},
		{
			// referenceNow is a Saturday: the weekday reset already fired at
			// midnight, so the cycle rolls to the next Saturday.
			name:     "weekly on the current weekday rolls to next week",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 6},
			wantText: "через 7 дн.",
			wantDays: 7,
		},
		{
			name:     "weekly without weekday is unknown",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 0},
			wantText: "неизвестно",
		},
		{
			name:     "weekly weekday out of range is unknown",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 8},
			wantText: "неизвестно",
		},
		{
			name:     "hourly is not modeled",
			profile:  xui.ClientConfig{TrafficReset: "hourly"},
			wantText: "неизвестно",
		},
		{
			name:     "unrecognized period is unknown",
			profile:  xui.ClientConfig{TrafficReset: "yearly", TrafficResetDay: 1},
			wantText: "неизвестно",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := describeTrafficReset(now, tt.profile)

			assert.Equal(t, tt.wantText, got.Text)
			assert.Equal(t, tt.wantDays, got.Days)
		})
	}
}

func TestNextMonthlyReset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		now  time.Time
		day  int
		want time.Time
	}{
		{
			name: "day later this month",
			now:  time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
			day:  20,
			want: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "day earlier this month rolls to next month",
			now:  time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
			day:  1,
			want: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "day missing in current month skips to a month that has it",
			now:  time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
			day:  31,
			want: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "february rollover with day 31 lands on march 31",
			now:  time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC),
			day:  31,
			want: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "day today already fired rolls to next month",
			now:  time.Date(2026, 3, 5, 23, 0, 0, 0, time.UTC),
			day:  5,
			want: time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := nextMonthlyReset(tt.now, tt.day)
			require.False(t, got.IsZero())

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNextWeeklyReset(t *testing.T) {
	t.Parallel()

	saturday := referenceNow(t)

	tests := []struct {
		name    string
		now     time.Time
		weekday int
		want    time.Time
	}{
		{
			name:    "monday from saturday",
			now:     saturday,
			weekday: 1,
			want:    time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "sunday from saturday",
			now:     saturday,
			weekday: 7,
			want:    time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "same weekday already fired rolls to next week",
			now:     saturday,
			weekday: 6,
			want:    time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "saturday from sunday",
			now:     time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
			weekday: 6,
			want:    time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := nextWeeklyReset(tt.now, tt.weekday)

			assert.Equal(t, tt.want, got)
		})
	}
}
