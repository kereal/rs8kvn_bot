package service

import (
	"context"
	"errors"
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

// TestSyncService_buildNodeProvision covers the single provision assembly in a
// table: paid create (product default), trial (never), update with a panel
// profile (preserved) and the readerless fallback (default).
func TestSyncService_buildNodeProvision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	expiresAt := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)

	paidPlan := &database.Plan{Name: "prov-build-paid", DevicesLimit: 1, TrafficLimit: 4096}
	trialPlan := &database.Plan{Name: database.TrialPlanName, DevicesLimit: 1, TrafficLimit: 1024}
	// Free plan: no traffic limit — no profile and no expiry are written.
	freePlan := &database.Plan{Name: "prov-build-free", DevicesLimit: 1, TrafficLimit: 0}

	panelProfile := xui.ClientConfig{
		Reset:           7,
		ResetDay:        3,
		ResetMax:        2,
		TrafficReset:    "weekly",
		TrafficResetDay: 5,
	}

	// panelAdminEdits simulate manual admin edits in the panel: reset interval
	// 7 days, weekly reset on Friday, capped renewals. The update path must
	// carry them into the provision untouched.
	withReader := func(profile xui.ClientConfig) NodeClients {
		reader := &testutil.XUIClient{}
		reader.GetClientByEmailFunc = func(_ context.Context, _ string) (*xui.ClientRecord, error) {
			return &xui.ClientRecord{Client: profile}, nil
		}

		return NodeClients{1: {Reader: reader}}
	}

	tests := []struct {
		name    string
		plan    *database.Plan
		clients NodeClients
		policy  profilePolicy
		check   func(t *testing.T, provision vpn.SubscriptionProvision)
	}{
		{
			name:    "paid_create_writes_default_profile",
			plan:    paidPlan,
			clients: NodeClients{1: {}},
			policy:  profileDefault,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, config.SubscriptionResetDay, provision.ResetDays)
				assert.Equal(t, 1, provision.TrafficResetDay)
				assert.Equal(t, 0, provision.ResetMax)
				assert.Equal(t, "monthly", provision.TrafficReset)
			},
		},
		{
			name:    "trial_create_writes_never_profile",
			plan:    trialPlan,
			clients: NodeClients{1: {}},
			policy:  profileDefault,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, 0, provision.ResetDays, "trial must not auto-renew")
				assert.Equal(t, "never", provision.TrafficReset, "trial quota must not renew")
				assert.Equal(t, 0, provision.ResetMax)
			},
		},
		{
			name:    "trial_update_overrides_panel_profile_with_never",
			plan:    trialPlan,
			clients: withReader(panelProfile),
			policy:  profilePreserve,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, 0, provision.ResetDays, "trial must not inherit the panel reset interval")
				assert.Equal(t, "never", provision.TrafficReset, "trial must never keep a panel reset cycle")
			},
		},
		{
			name:    "paid_update_preserves_panel_profile",
			plan:    paidPlan,
			clients: withReader(panelProfile),
			policy:  profilePreserve,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, 7, provision.ResetDays)
				assert.Equal(t, 3, provision.ResetDay)
				assert.Equal(t, 2, provision.ResetMax)
				assert.Equal(t, "weekly", provision.TrafficReset)
				assert.Equal(t, 5, provision.TrafficResetDay)
			},
		},
		{
			name:    "paid_update_without_reader_falls_back_to_default",
			plan:    paidPlan,
			clients: NodeClients{1: {}},
			policy:  profilePreserve,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Equal(t, config.SubscriptionResetDay, provision.ResetDays)
				assert.Equal(t, "monthly", provision.TrafficReset)
			},
		},
		{
			name:    "free_plan_gets_no_profile_and_no_expiry",
			plan:    freePlan,
			clients: withReader(panelProfile),
			policy:  profilePreserve,
			check: func(t *testing.T, provision vpn.SubscriptionProvision) {
				assert.Zero(t, provision.ResetDays)
				assert.Empty(t, provision.TrafficReset)
				assert.True(t, provision.ExpiryTime.IsZero())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testutil.NewServiceStoreFake()
			db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
				return tt.plan, nil
			}

			sub := &database.Subscription{
				TelegramID:     4242,
				Username:       "provuser",
				ClientID:       "c-prov",
				SubscriptionID: "s-prov",
				Status:         "active",
				PlanID:         tt.plan.ID,
				ExpiresAt:      testutil.PtrTime(expiresAt),
			}

			svc := NewSyncService(db, tt.clients, nil)

			provision, err := svc.buildNodeProvision(ctx, sub, 1, tt.policy)
			require.NoError(t, err)

			// Common client fields are identical for both paths.
			assert.Equal(t, sub.ClientID, provision.ClientID)
			assert.Equal(t, sub.Username, provision.Username)
			assert.Equal(t, sub.SubscriptionID, provision.SubID)
			assert.Equal(t, tt.plan.TrafficLimit, provision.TrafficBytes)
			assert.Equal(t, sub.TelegramID, provision.TgID)
			if tt.plan.TrafficLimit > 0 {
				assert.Equal(t, expiresAt, provision.ExpiryTime, "existing expiry must be carried as-is")
			}

			tt.check(t, provision)
		})
	}
}

// TestSyncService_buildNodeProvision_AddUpdateParity checks that both paths
// build the identical client (golden comparison of the whole provision) when
// only the profile policy differs and the panel profile equals the default.
func TestSyncService_buildNodeProvision_AddUpdateParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := testutil.NewServiceStoreFake()

	expiresAt := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Minute)

	plan := &database.Plan{Name: "prov-parity", DevicesLimit: 2, TrafficLimit: 8192}
	db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
		return plan, nil
	}

	sub := &database.Subscription{
		TelegramID:     4243,
		Username:       "parityuser",
		ClientID:       "c-parity",
		SubscriptionID: "s-parity",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(expiresAt),
	}

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(_ context.Context, _ string) (*xui.ClientRecord, error) {
		return &xui.ClientRecord{Client: xui.ClientConfig{
			Reset:           config.SubscriptionResetDay,
			ResetMax:        0,
			TrafficReset:    "monthly",
			TrafficResetDay: 1,
		}}, nil
	}

	svc := NewSyncService(db, NodeClients{1: {Reader: reader}}, nil)

	addProvision, err := svc.buildNodeProvision(ctx, sub, 1, profileDefault)
	require.NoError(t, err)

	updateProvision, err := svc.buildNodeProvision(ctx, sub, 1, profilePreserve)
	require.NoError(t, err)

	assert.Equal(t, addProvision, updateProvision,
		"add and update must build the identical client when the panel profile matches the default")
}

// TestSyncService_buildNodeProvision_DefaultExpiryWhenNone checks the fallback
// expiry for a subscription without ExpiresAt: now (minute-truncated) plus
// config.SubscriptionResetDay.
func TestSyncService_buildNodeProvision_DefaultExpiryWhenNone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := testutil.NewServiceStoreFake()

	plan := &database.Plan{Name: "prov-default-expiry", DevicesLimit: 1, TrafficLimit: 1024}
	db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
		return plan, nil
	}

	sub := &database.Subscription{
		TelegramID:     4244,
		Username:       "noexpiry",
		ClientID:       "c-noexpiry",
		SubscriptionID: "s-noexpiry",
		Status:         "active",
		PlanID:         plan.ID,
	}

	svc := NewSyncService(db, NodeClients{1: {}}, nil)

	provision, err := svc.buildNodeProvision(ctx, sub, 1, profileDefault)
	require.NoError(t, err)

	want := time.Now().UTC().Truncate(time.Minute).AddDate(0, 0, config.SubscriptionResetDay)
	assert.True(t, provision.ExpiryTime.Equal(want),
		"fallback expiry must be minute-truncated now plus SubscriptionResetDay")
}

// TestSyncService_buildNodeProvision_UpdateMissingClient checks that the
// preserve policy fails with the sentinel vpn.ErrSubscriptionNotFound when the
// client is gone from the panel — without falling back to a default profile or
// a create.
func TestSyncService_buildNodeProvision_UpdateMissingClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := testutil.NewServiceStoreFake()

	plan := &database.Plan{Name: "prov-missing", DevicesLimit: 1, TrafficLimit: 1024}
	db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
		return plan, nil
	}

	sub := &database.Subscription{
		TelegramID:     4245,
		Username:       "missinguser",
		ClientID:       "c-missing",
		SubscriptionID: "s-missing",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().UTC().Add(24 * time.Hour)),
	}

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(_ context.Context, _ string) (*xui.ClientRecord, error) {
		return nil, xui.ErrClientNotFound
	}

	svc := NewSyncService(db, NodeClients{1: {Reader: reader}}, nil)

	_, err := svc.buildNodeProvision(ctx, sub, 1, profilePreserve)
	require.Error(t, err)
	assert.ErrorIs(t, err, vpn.ErrSubscriptionNotFound,
		"a missing panel client must surface the sentinel instead of a default profile")
	assert.NotErrorIs(t, err, vpn.ErrSubscriptionAlreadyExists)
}

// TestSyncService_buildNodeProvision_UpdateProfileReadError checks that a
// panel read failure (other than "not found") fails the build: an update must
// not silently overwrite manual admin edits with the default profile.
func TestSyncService_buildNodeProvision_UpdateProfileReadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := testutil.NewServiceStoreFake()

	plan := &database.Plan{Name: "prov-read-err", DevicesLimit: 1, TrafficLimit: 1024}
	db.GetPlanByIDFunc = func(_ context.Context, _ uint) (*database.Plan, error) {
		return plan, nil
	}

	sub := &database.Subscription{
		TelegramID:     4246,
		Username:       "readerruser",
		ClientID:       "c-readerr",
		SubscriptionID: "s-readerr",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().UTC().Add(24 * time.Hour)),
	}

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(_ context.Context, _ string) (*xui.ClientRecord, error) {
		return nil, errors.New("panel unreachable")
	}

	svc := NewSyncService(db, NodeClients{1: {Reader: reader}}, nil)

	_, err := svc.buildNodeProvision(ctx, sub, 1, profilePreserve)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read panel profile")
}

// TestSyncService_SyncSubscription_PendingUpdate_PreservesPanelProfile runs the
// full update path end-to-end: a plan change must not erase non-default admin
// edits of the auto-renew profile on the panel.
func TestSyncService_SyncSubscription_PendingUpdate_PreservesPanelProfile(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "prov-e2e-preserve", IsActive: true, DevicesLimit: 1, TrafficLimit: 2048}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node := &database.Node{Name: "prov-preserve-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://pp", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	expiresAt := time.Now().UTC().Add(12 * time.Hour).Truncate(time.Minute)
	sub := &database.Subscription{TelegramID: 994002, Username: "preserve-user", ClientID: "preserve-client", SubscriptionID: "preserve-sub", Status: "active", PlanID: plan.ID, ExpiresAt: &expiresAt}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate}))

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(_ context.Context, email string) (*xui.ClientRecord, error) {
		require.Equal(t, XUIEmail(sub.Username, sub.TelegramID), email)

		return &xui.ClientRecord{Client: xui.ClientConfig{
			Reset:           14,
			ResetDay:        20,
			ResetMax:        5,
			TrafficReset:    "daily",
			TrafficResetDay: 15,
		}}, nil
	}

	mockVPN := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: mockVPN, Reader: reader}}, []database.Node{*node})
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	require.True(t, mockVPN.updateCalled)
	assert.Equal(t, 14, mockVPN.updateProvision.ResetDays, "admin-edited reset interval must survive the update")
	assert.Equal(t, 20, mockVPN.updateProvision.ResetDay)
	assert.Equal(t, 5, mockVPN.updateProvision.ResetMax)
	assert.Equal(t, "daily", mockVPN.updateProvision.TrafficReset, "admin-edited reset period must survive the update")
	assert.Equal(t, 15, mockVPN.updateProvision.TrafficResetDay)
	assert.Equal(t, plan.TrafficLimit, mockVPN.updateProvision.TrafficBytes)
	assert.Equal(t, expiresAt, mockVPN.updateProvision.ExpiryTime)

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
}

// TestSyncService_SyncSubscription_PendingUpdate_MissingClientFallsBackToCreate
// checks the full update path when the client is gone from the panel: the item
// is re-queued as pending_add (create path) in the same pass — no update
// attempted, no retry ladder, next worker pass provisions the client.
func TestSyncService_SyncSubscription_PendingUpdate_MissingClientFallsBackToCreate(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "prov-e2e-missing", IsActive: true, DevicesLimit: 1, TrafficLimit: 2048}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node := &database.Node{Name: "prov-missing-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://pm", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &database.Subscription{TelegramID: 994003, Username: "gone-user", ClientID: "gone-client", SubscriptionID: "gone-sub", Status: "active", PlanID: plan.ID, ExpiresAt: testutil.PtrTime(time.Now().UTC().Add(12 * time.Hour))}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate}))

	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(context.Context, string) (*xui.ClientRecord, error) {
		return nil, xui.ErrClientNotFound
	}

	mockVPN := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: mockVPN, Reader: reader}}, []database.Node{*node})

	// SyncSubscription swallows per-node failures by contract (best-effort
	// external sync); the outcome is observable in the node status.
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))
	assert.False(t, mockVPN.updateCalled, "update must not run without the panel profile")
	assert.False(t, mockVPN.createCalled, "create runs on the next pass, not in the same one")

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, rows[0].Status, "the item must be re-queued for create")
	assert.Zero(t, rows[0].RetryCount, "the status transition resets retry metadata")
	assert.Nil(t, rows[0].RetryAt)
	assert.Nil(t, rows[0].LastError)
}

// TestSyncService_SyncSubscription_PendingUpdate_MissingClientThenCreate
// runs the fallback end-to-end: after the pending_add re-queue the next pass
// creates the client and marks the node active.
func TestSyncService_SyncSubscription_PendingUpdate_MissingClientThenCreate(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan := &database.Plan{Name: "prov-e2e-fallback", IsActive: true, DevicesLimit: 1, TrafficLimit: 2048}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)

	node := &database.Node{Name: "prov-fallback-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://pf", APIToken: "t", InboundIDs: `[1]`}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: plan.ID, NodeID: node.ID}).Error)

	sub := &database.Subscription{TelegramID: 994004, Username: "fb-user", ClientID: "fb-client", SubscriptionID: "fb-sub", Status: "active", PlanID: plan.ID, ExpiresAt: testutil.PtrTime(time.Now().UTC().Add(12 * time.Hour))}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusPendingUpdate}))

	clientGone := true
	reader := &testutil.XUIClient{}
	reader.GetClientByEmailFunc = func(context.Context, string) (*xui.ClientRecord, error) {
		if clientGone {
			return nil, xui.ErrClientNotFound
		}

		return &xui.ClientRecord{Client: xui.ClientConfig{Reset: 30, TrafficReset: "monthly", TrafficResetDay: 1}}, nil
	}

	mockVPN := &mockVPNClient{}
	svc := NewSyncService(db, NodeClients{node.ID: {Provision: mockVPN, Reader: reader}}, []database.Node{*node})

	// Pass 1: update discovers the missing client, re-queues pending_add.
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	rows, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, database.SyncStatusPendingAdd, rows[0].Status)

	// Pass 2: the create path provisions the client fresh.
	clientGone = false
	require.NoError(t, svc.SyncSubscription(ctx, sub.ID))

	assert.True(t, mockVPN.createCalled, "the re-queued node must be provisioned by the create path")

	rows, err = db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.SyncStatusActive, rows[0].Status)
	assert.Zero(t, rows[0].RetryCount)
}
