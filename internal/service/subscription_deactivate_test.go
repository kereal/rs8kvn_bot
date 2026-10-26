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
