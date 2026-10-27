package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncService_RevokedPendingProvisioningIsRemoved(t *testing.T) {
	t.Parallel()

	for _, status := range []database.SyncStatus{database.SyncStatusPendingAdd, database.SyncStatusPendingUpdate} {
		for _, background := range []bool{false, true} {
			name := string(status) + "/immediate"
			if background {
				name = string(status) + "/background"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				ctx := context.Background()
				db, err := testutil.NewTestDatabaseService(t)
				require.NoError(t, err)
				node := &database.Node{Name: "revoked-node", Type: database.NodeType3xUI, IsActive: true, Host: "http://revoked", InboundIDs: `[1]`}
				require.NoError(t, db.GetDB().WithContext(ctx).Create(node).Error)
				plan := &database.Plan{Name: "revoked-plan", DevicesLimit: 1}
				require.NoError(t, db.GetDB().WithContext(ctx).Create(plan).Error)
				sub := &database.Subscription{
					TelegramID: 9201, Username: "revoked-user", ClientID: "revoked-client",
					SubscriptionID: "revoked-sub", Status: string(database.SubscriptionStatusRevoked), PlanID: plan.ID,
				}
				require.NoError(t, db.CreateSubscription(ctx, sub, ""))
				due := time.Now().UTC().Add(-time.Minute)
				require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{
					SubscriptionID: sub.ID, NodeID: node.ID, Status: status, RetryCount: 4, RetryAt: &due,
				}))

				deleteErr := errors.New("VPN node temporarily unavailable")
				client := &mockVPNClient{deleteError: deleteErr}
				// Removal must use the current DB node even without a runtime snapshot.
				svc := NewSyncService(db, map[uint]vpn.Client{node.ID: client}, nil)
				sync := func() error {
					if background {
						return svc.SyncPendingNodes(ctx)
					}
					return svc.SyncSubscription(ctx, sub.ID)
				}
				err = sync()
				if background {
					require.ErrorIs(t, err, deleteErr)
				} else {
					require.NoError(t, err, "immediate external sync remains best-effort")
				}
				assert.True(t, client.deleteCalled)
				assert.Equal(t, sub.ClientID, client.deleteProvision.ClientID)
				assert.False(t, client.createCalled)
				assert.False(t, client.updateCalled)
				assert.False(t, client.resetTrafficCalled)

				bindings, err := db.GetBySubscriptionID(ctx, sub.ID)
				require.NoError(t, err)
				require.Len(t, bindings, 1)
				assert.Equal(t, database.SyncStatusPendingRemove, bindings[0].Status)
				assert.Equal(t, 1, bindings[0].RetryCount, "removal starts a fresh retry sequence")
				require.NotNil(t, bindings[0].RetryAt)
				require.NotNil(t, bindings[0].LastError)
				assert.Contains(t, *bindings[0].LastError, deleteErr.Error())

				// Make the persisted deletion due and let the next scan finish it.
				require.NoError(t, db.UpdateRetry(ctx, sub.ID, node.ID, bindings[0].RetryCount, &due, bindings[0].LastError))
				client.deleteError = nil
				client.deleteCalled = false
				require.NoError(t, sync())
				assert.True(t, client.deleteCalled)
				assert.False(t, client.createCalled)
				assert.False(t, client.updateCalled)
				bindings, err = db.GetBySubscriptionID(ctx, sub.ID)
				require.NoError(t, err)
				assert.Empty(t, bindings)
				stored, err := db.GetByID(ctx, sub.ID)
				require.NoError(t, err)
				assert.Equal(t, string(database.SubscriptionStatusRevoked), stored.Status)
			})
		}
	}
}

func TestSyncService_RevokedRemovalQueueFailure(t *testing.T) {
	t.Parallel()

	for _, queueErr := range []error{errors.New("DB write failed"), context.Canceled, context.DeadlineExceeded} {
		t.Run(queueErr.Error(), func(t *testing.T) {
			t.Parallel()

			var queued, retried, deleted []uint
			db := testutil.NewDatabaseService()
			db.UpdateSubscriptionNodeStatusFunc = func(_ context.Context, _ uint, nodeID uint, status database.SyncStatus) error {
				assert.Equal(t, database.SyncStatusPendingRemove, status)
				queued = append(queued, nodeID)
				if nodeID == 1 {
					return queueErr
				}
				return nil
			}
			db.UpdateRetryFunc = func(_ context.Context, _, nodeID uint, _ int, _ *time.Time, _ *string) error {
				retried = append(retried, nodeID)
				return nil
			}
			db.GetNodeByIDFunc = func(_ context.Context, nodeID uint) (*database.Node, error) {
				assert.Equal(t, uint(2), nodeID, "a failed queue write must prevent external sync")
				return &database.Node{ID: nodeID, IsActive: true}, nil
			}
			db.DeleteSubscriptionNodeFunc = func(_ context.Context, _, nodeID uint) error {
				deleted = append(deleted, nodeID)
				return nil
			}
			failedClient, nextClient := &mockVPNClient{}, &mockVPNClient{}
			svc := NewSyncService(db, map[uint]vpn.Client{1: failedClient, 2: nextClient}, []database.Node{
				{ID: 1, Type: database.NodeType3xUI}, {ID: 2, Type: database.NodeType3xUI},
			})
			sub := &database.Subscription{ID: 42, Status: string(database.SubscriptionStatusRevoked)}
			err := svc.syncNodes(context.Background(), sub, []database.SubscriptionNode{
				{SubscriptionID: sub.ID, NodeID: 1, Status: database.SyncStatusPendingAdd},
				{SubscriptionID: sub.ID, NodeID: 2, Status: database.SyncStatusPendingUpdate},
			})
			require.ErrorIs(t, err, queueErr)
			assert.False(t, failedClient.createCalled)
			assert.False(t, failedClient.updateCalled)
			assert.False(t, failedClient.deleteCalled)
			assert.False(t, nextClient.createCalled)
			assert.False(t, nextClient.updateCalled)
			if errors.Is(queueErr, context.Canceled) || errors.Is(queueErr, context.DeadlineExceeded) {
				assert.Equal(t, []uint{1}, queued)
				assert.Empty(t, retried)
				assert.Empty(t, deleted)
				assert.False(t, nextClient.deleteCalled)
			} else {
				assert.Equal(t, []uint{1, 2}, queued)
				assert.Equal(t, []uint{1}, retried)
				assert.Equal(t, []uint{2}, deleted)
				assert.True(t, nextClient.deleteCalled)
			}
		})
	}
}
