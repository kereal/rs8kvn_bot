package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDeactivationDatabase builds a mock database with one completed broadcast
// whose recipients are all blocked.
func newDeactivationDatabase(telegramIDs []int64) *testutil.DatabaseService {
	recipients := make([]string, 0, len(telegramIDs))
	for i, id := range telegramIDs {
		recipients = append(recipients, fmt.Sprintf(`{"id":%d,"telegram_id":%d,"status":"blocked","attempts":1}`, i+1, id))
	}

	broadcast := database.Broadcast{
		ID:               1,
		Name:             "Тестовая рассылка",
		MessageText:      "Привет",
		Status:           string(database.BroadcastStatusCompleted),
		BlockedCount:     int64(len(telegramIDs)),
		UnreachableCount: 1,
		RecipientsState:  `{"snapshot":true,"recipients":[` + strings.Join(recipients, ",") + `]}`,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}

	return &testutil.DatabaseService{
		Broadcasts: map[uint]*database.Broadcast{1: &broadcast},
	}
}

// TestDeactivateBlockedBroadcastRecipients_RevokesAndDeprovisions covers the
// full revocation lifecycle: the subscription is marked revoked, the audit event
// is written, and — the part that was previously missing — the VPN client is
// actually removed from the node (binding -> pending_remove -> panel delete),
// so an already-imported profile stops working instead of only the URL 404ing.
func TestDeactivateBlockedBroadcastRecipients_RevokesAndDeprovisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	node := database.Node{
		ID:         1,
		Name:       "node-1",
		IsActive:   true,
		Host:       "http://node-1",
		InboundIDs: "[1]",
		Type:       database.NodeType3xUI,
	}

	sub := &database.Subscription{
		ID:             10,
		TelegramID:     123456789,
		Username:       "blocked_user",
		ClientID:       "client-1",
		SubscriptionID: "sub-1",
		Status:         string(database.SubscriptionStatusActive),
		CreatedAt:      time.Now().UTC(),
	}

	m := newDeactivationDatabase([]int64{sub.TelegramID})
	m.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		if telegramID == sub.TelegramID {
			return sub, nil
		}

		return nil, database.ErrSubscriptionNotFound
	}
	m.UpdateSubscriptionFunc = func(ctx context.Context, s *database.Subscription) error {
		sub.Status = s.Status

		return nil
	}
	m.GetNodeByIDFunc = func(ctx context.Context, id uint) (*database.Node, error) {
		return &node, nil
	}

	var markedPendingRemoval bool

	m.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{{SubscriptionID: subscriptionID, NodeID: node.ID, Status: database.SyncStatusActive}}, nil
	}
	m.UpdateSubscriptionNodeStatusFunc = func(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error {
		if status == database.SyncStatusPendingRemove {
			markedPendingRemoval = true
		}

		return nil
	}
	m.GetPendingBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{{SubscriptionID: subscriptionID, NodeID: node.ID, Status: database.SyncStatusPendingRemove}}, nil
	}
	m.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}

	var bindingRemoved bool

	m.DeleteSubscriptionNodeFunc = func(ctx context.Context, subID, nodeID uint) error {
		bindingRemoved = true

		return nil
	}

	client := &mockVPNClient{}
	vpnClients := map[uint]vpn.Client{node.ID: client}

	svc := NewSubscriptionService(m, nil, vpnClients, []database.Node{node}, &config.Config{})
	svc.SetSyncService(NewSyncService(m, vpnClients, []database.Node{node}))

	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Targets)
	assert.Equal(t, 1, result.Revoked)
	assert.Zero(t, result.Failed)
	assert.Equal(t, string(database.SubscriptionStatusRevoked), sub.Status)
	require.Len(t, m.AuditLogs, 1)
	assert.Equal(t, database.AuditEventSubscriptionRevoked, m.AuditLogs[0].Event)
	assert.True(t, markedPendingRemoval, "binding must be queued as pending_remove so the VPN worker can retry")
	assert.True(t, client.deleteCalled, "VPN client must be deleted from the node, not just revoked in the DB")
	assert.True(t, bindingRemoved, "successful removal must clear the pending_remove binding")
}

// TestDeactivateBlockedBroadcastRecipients_ReportsPartialFailure verifies a
// partial run is neither silently reported as success nor aborts the remaining
// recipients.
func TestDeactivateBlockedBroadcastRecipients_ReportsPartialFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	okSub := &database.Subscription{
		ID:             11,
		TelegramID:     111,
		ClientID:       "client-ok",
		SubscriptionID: "sub-ok",
		Status:         string(database.SubscriptionStatusActive),
	}
	brokenSub := &database.Subscription{
		ID:             12,
		TelegramID:     222,
		ClientID:       "client-broken",
		SubscriptionID: "sub-broken",
		Status:         string(database.SubscriptionStatusActive),
	}

	subs := map[int64]*database.Subscription{okSub.TelegramID: okSub, brokenSub.TelegramID: brokenSub}

	m := newDeactivationDatabase([]int64{okSub.TelegramID, brokenSub.TelegramID})
	m.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		if sub, ok := subs[telegramID]; ok {
			return sub, nil
		}

		return nil, database.ErrSubscriptionNotFound
	}
	m.UpdateSubscriptionFunc = func(ctx context.Context, s *database.Subscription) error {
		if s.ID == brokenSub.ID {
			return errors.New("database is locked")
		}

		subs[s.TelegramID].Status = s.Status

		return nil
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})

	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database is locked")
	require.NotNil(t, result)
	assert.Equal(t, 2, result.Targets)
	assert.Equal(t, 1, result.Revoked)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, string(database.SubscriptionStatusRevoked), okSub.Status)
	assert.Equal(t, string(database.SubscriptionStatusActive), brokenSub.Status, "failed write must not mutate the in-memory state")
	assert.Len(t, m.AuditLogs, 1, "only the actually revoked subscription is audited")
}

func TestDeactivateBlockedBroadcastRecipients_RetriesFailedDeprovision(t *testing.T) {
	t.Parallel()

	for _, failOnRead := range []bool{true, false} {
		t.Run(fmt.Sprintf("failOnRead=%t", failOnRead), func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			sub := &database.Subscription{ID: 10, TelegramID: 123, Status: string(database.SubscriptionStatusActive)}
			bindings := []database.SubscriptionNode{
				{SubscriptionID: sub.ID, NodeID: 1, Status: database.SyncStatusActive},
				{SubscriptionID: sub.ID, NodeID: 2, Status: database.SyncStatusPendingUpdate},
			}
			queueErr := errors.New("database is locked")
			failQueue := true
			m := newDeactivationDatabase([]int64{sub.TelegramID})
			m.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
				if sub.Status != string(database.SubscriptionStatusActive) {
					return nil, database.ErrSubscriptionNotFound
				}
				return sub, nil
			}
			m.GetAnyByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
			statusWrites := 0
			m.UpdateSubscriptionFunc = func(context.Context, *database.Subscription) error {
				statusWrites++
				return nil
			}
			m.GetBySubscriptionIDFunc = func(context.Context, uint) ([]database.SubscriptionNode, error) {
				if failQueue && failOnRead {
					return nil, queueErr
				}
				return bindings, nil
			}
			m.UpdateSubscriptionNodeStatusFunc = func(_ context.Context, _ uint, nodeID uint, status database.SyncStatus) error {
				if failQueue && nodeID == 2 {
					return queueErr
				}
				bindings[nodeID-1].Status = status
				return nil
			}
			// Leave queued removals to the background worker.
			m.GetPendingBySubscriptionIDFunc = func(context.Context, uint) ([]database.SubscriptionNode, error) { return nil, nil }
			svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})
			svc.SetSyncService(NewSyncService(m, nil, nil))

			result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
			require.ErrorIs(t, err, queueErr)
			require.Equal(t, &DeactivationResult{Targets: 1, Revoked: 1, Failed: 1}, result)
			require.Equal(t, string(database.SubscriptionStatusRevoked), sub.Status)

			failQueue = false
			result, err = svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, &DeactivationResult{Targets: 1}, result)
			for _, binding := range bindings {
				require.Equal(t, database.SyncStatusPendingRemove, binding.Status)
			}
			require.Equal(t, 1, statusWrites, "retry must not revoke again")
			require.Len(t, m.AuditLogs, 1, "retry must not duplicate the revocation audit")

			bindings = nil
			result, err = svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, &DeactivationResult{}, result, "completed removals must be skipped")
		})
	}
}

// TestDeactivateBlockedBroadcastRecipients_StatsError covers the early-return
// branch: a failure reading the broadcast stats must abort the run with a
// wrapped error and nil result, before any recipient is touched.
func TestDeactivateBlockedBroadcastRecipients_StatsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	m := &testutil.DatabaseService{
		GetBroadcastRecipientsStatsFunc: func(context.Context, uint) (int64, int64, int64, int64, int64, database.BroadcastDeliveryReport, error) {
			return 0, 0, 0, 0, 0, database.BroadcastDeliveryReport{}, errors.New("stats db down")
		},
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})
	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get broadcast stats")
	assert.Nil(t, result)
}

// TestDeactivateBlockedBroadcastRecipients_NoTargets covers the empty-target
// branch: a completed broadcast with zero blocked/unreachable recipients must
// return an empty DeactivationResult with no error and no side effects.
func TestDeactivateBlockedBroadcastRecipients_NoTargets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	m := &testutil.DatabaseService{
		GetBroadcastRecipientsStatsFunc: func(context.Context, uint) (int64, int64, int64, int64, int64, database.BroadcastDeliveryReport, error) {
			return 3, 3, 0, 0, 0, database.BroadcastDeliveryReport{}, nil
		},
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})
	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, &DeactivationResult{}, result)
	assert.Empty(t, m.AuditLogs, "no recipient must be touched, no audit event written")
}

// TestDeactivateBlockedBroadcastRecipients_SkipsTelegramWithoutSubscription
// covers the continue-path: a blocked recipient with no subscription at all
// must be skipped (not counted as failed, no audit event).
func TestDeactivateBlockedBroadcastRecipients_SkipsTelegramWithoutSubscription(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	m := newDeactivationDatabase([]int64{111})
	m.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	m.GetAnyByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})
	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, &DeactivationResult{}, result)
	assert.Empty(t, m.AuditLogs)
}

// TestDeactivateBlockedBroadcastRecipients_SubscriptionReadErrorCountsFailed
// covers the non-NotFound subscription read error: the recipient is counted as
// failed and the run continues (other recipients still processed).
func TestDeactivateBlockedBroadcastRecipients_SubscriptionReadErrorCountsFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	m := newDeactivationDatabase([]int64{111})
	m.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) {
		return nil, errors.New("db read error")
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})
	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.Error(t, err)
	require.NotNil(t, result)
	assert.Contains(t, err.Error(), "get subscription for telegram")
	assert.Equal(t, 1, result.Failed)
	assert.Zero(t, result.Targets)
	assert.Zero(t, result.Revoked)
	assert.Empty(t, m.AuditLogs)
}

// TestDeactivateBlockedBroadcastRecipients_UnreachableAdressees covers the
// unreachable-report path: recipients marked unreachable in the broadcast state
// feed the same target set as blocked ones and are deactivated.
func TestDeactivateBlockedBroadcastRecipients_UnreachableAdressees(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	sub := &database.Subscription{
		ID:             10,
		TelegramID:     123456789,
		ClientID:       "client-unre",
		SubscriptionID: "sub-unre",
		Status:         string(database.SubscriptionStatusActive),
	}

	broadcast := database.Broadcast{
		ID:               1,
		Name:             "unreachable-test",
		MessageText:      "hi",
		Status:           string(database.BroadcastStatusCompleted),
		BlockedCount:     0,
		UnreachableCount: 1,
		RecipientsState:  `{"snapshot":true,"recipients":[{"id":1,"telegram_id":123456789,"status":"unreachable","attempts":1}]}`,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}

	m := &testutil.DatabaseService{
		Broadcasts: map[uint]*database.Broadcast{1: &broadcast},
	}
	m.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		if telegramID == sub.TelegramID {
			return sub, nil
		}

		return nil, database.ErrSubscriptionNotFound
	}
	m.UpdateSubscriptionFunc = func(ctx context.Context, s *database.Subscription) error {
		sub.Status = s.Status

		return nil
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})
	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Targets)
	assert.Equal(t, 1, result.Revoked)
	assert.Equal(t, string(database.SubscriptionStatusRevoked), sub.Status)
	require.Len(t, m.AuditLogs, 1)
	assert.Equal(t, database.AuditEventSubscriptionRevoked, m.AuditLogs[0].Event)
	assert.Equal(t, database.EntitySubscription, m.AuditLogs[0].EntityType)
}

// TestDeactivateBlockedBroadcastRecipients_ImmediateSyncCapped covers the
// maxImmediateSyncs cap: with more than 25 queued removals, exactly the first
// 25 are synced immediately (VPN client actually deleted), the rest are left
// pending_remove for the background SyncPendingNodes worker.
func TestDeactivateBlockedBroadcastRecipients_ImmediateSyncCapped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const nTargets = 30

	node := database.Node{
		ID:         1,
		Name:       "node-1",
		IsActive:   true,
		Host:       "http://node-1",
		InboundIDs: "[1]",
		Type:       database.NodeType3xUI,
	}

	subs := make([]*database.Subscription, 0, nTargets)
	telegramIDs := make([]int64, 0, nTargets)
	for i := range nTargets {
		telegramIDs = append(telegramIDs, int64(1000+i))
		subs = append(subs, &database.Subscription{
			ID:             uint(100 + i),
			TelegramID:     int64(1000 + i),
			ClientID:       fmt.Sprintf("client-%d", i),
			SubscriptionID: fmt.Sprintf("sub-%d", i),
			Status:         string(database.SubscriptionStatusActive),
		})
	}

	subsByTelegram := make(map[int64]*database.Subscription, nTargets)
	subsByID := make(map[uint]*database.Subscription, nTargets)
	for _, sub := range subs {
		subsByTelegram[sub.TelegramID] = sub
		subsByID[sub.ID] = sub
	}

	immediateSyncCount := 0

	m := newDeactivationDatabase(telegramIDs)
	m.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		if sub, ok := subsByTelegram[telegramID]; ok {
			return sub, nil
		}

		return nil, database.ErrSubscriptionNotFound
	}
	m.UpdateSubscriptionFunc = func(ctx context.Context, s *database.Subscription) error {
		subsByID[s.ID].Status = s.Status

		return nil
	}
	m.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{{SubscriptionID: subscriptionID, NodeID: node.ID, Status: database.SyncStatusActive}}, nil
	}
	m.UpdateSubscriptionNodeStatusFunc = func(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error {
		return nil
	}
	m.GetPendingBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{{SubscriptionID: subscriptionID, NodeID: node.ID, Status: database.SyncStatusPendingRemove}}, nil
	}
	m.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		if sub, ok := subsByID[id]; ok {
			return sub, nil
		}

		return nil, database.ErrSubscriptionNotFound
	}
	m.GetNodeByIDFunc = func(ctx context.Context, id uint) (*database.Node, error) {
		return &node, nil
	}
	m.DeleteSubscriptionNodeFunc = func(ctx context.Context, subID, nodeID uint) error {
		immediateSyncCount++
		return nil
	}

	client := &mockVPNClient{}
	vpnClients := map[uint]vpn.Client{node.ID: client}

	svc := NewSubscriptionService(m, nil, vpnClients, []database.Node{node}, &config.Config{})
	svc.SetSyncService(NewSyncService(m, vpnClients, []database.Node{node}))

	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, nTargets, result.Targets)
	assert.Equal(t, nTargets, result.Revoked)
	assert.Zero(t, result.Failed)
	require.Len(t, m.AuditLogs, nTargets, "every revoked recipient writes an audit event")
	assert.Equal(t, 25, immediateSyncCount, "exactly the cap (25) removals are synced immediately, the rest are left for background SyncPendingNodes")
	assert.True(t, client.deleteCalled, "the cap only limits immediate syncs; at least one immediate delete must have run")
}

// TestDeactivateBlockedBroadcastRecipients_InvalidatesSubscriberCache covers the
// InvalidateBySubID side effect: every exited subscription with a SubID must
// have its subserver cache entry invalidated (featured as hook).
func TestDeactivateBlockedBroadcastRecipients_InvalidatesSubscriberCache(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	sub := &database.Subscription{
		ID:             10,
		TelegramID:     123456789,
		ClientID:       "client-inval",
		SubscriptionID: "sub-inval",
		Status:         string(database.SubscriptionStatusActive),
	}

	var invalidated []string

	m := newDeactivationDatabase([]int64{sub.TelegramID})
	m.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		if telegramID == sub.TelegramID {
			return sub, nil
		}

		return nil, database.ErrSubscriptionNotFound
	}
	m.UpdateSubscriptionFunc = func(ctx context.Context, s *database.Subscription) error {
		sub.Status = s.Status

		return nil
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})
	svc.SetInvalidateBySubIDFunc(func(subID string) {
		invalidated = append(invalidated, subID)
	})

	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Revoked)
	assert.Equal(t, []string{"sub-inval"}, invalidated, "the revoked subscription's cache must be invalidated")
}

// TestDeactivateBlockedBroadcastRecipients_RefreshesMetrics verifies the metric
// side effect of a successful run: after at least one subscription is revoked
// the active-subscription gauge is refreshed.
func TestDeactivateBlockedBroadcastRecipients_RefreshesMetrics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	sub := &database.Subscription{
		ID:             10,
		TelegramID:     123456789,
		ClientID:       "client-metric",
		SubscriptionID: "sub-metric",
		Status:         string(database.SubscriptionStatusActive),
	}

	m := newDeactivationDatabase([]int64{sub.TelegramID})
	m.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		if telegramID == sub.TelegramID {
			return sub, nil
		}

		return nil, database.ErrSubscriptionNotFound
	}
	m.UpdateSubscriptionFunc = func(ctx context.Context, s *database.Subscription) error {
		sub.Status = s.Status

		return nil
	}

	var countCalls uint

	m.CountActiveSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		countCalls++

		return 1, nil
	}
	m.CountPremiumSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 0, nil
	}
	m.CountFreeSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 1, nil
	}
	m.CountTrialSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 0, nil
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})

	result, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Revoked)
	assert.Equal(t, uint(1), countCalls, "active-subscription metric must be refreshed after a revocation run")
}
