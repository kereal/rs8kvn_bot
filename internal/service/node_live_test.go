package service

import (
	"context"
	"errors"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nodeLiveHarness собирает SubscriptionService с одной читаемой нодой и стабом
// магазина: активные привязки + подписки владельцев.
type nodeLiveHarness struct {
	reader  *testutil.XUIClient
	db      *testutil.ServiceStoreFake
	emails  []string
	pingErr error
}

func newNodeLiveHarness(t *testing.T, h *nodeLiveHarness) *SubscriptionService {
	t.Helper()

	h.emails = nil

	h.reader = &testutil.XUIClient{
		PingFunc: func(ctx context.Context) error {
			return h.pingErr
		},
		GetClientTrafficFunc: func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
			h.emails = append(h.emails, email)

			return &xui.ClientTraffic{Up: 100, Down: 50, Enable: true, LastOnline: 111}, nil
		},
	}

	clients := NodeClients{7: NodeAccess{Reader: h.reader, Type: database.NodeType3xUI}}
	nodes := []database.Node{{ID: 7, Type: database.NodeType3xUI, IsActive: true, Host: "http://node7"}}

	db := &testutil.ServiceStoreFake{
		GetActiveByNodeIDFunc: func(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{
				{SubscriptionID: 1, NodeID: nodeID, Status: database.SyncStatusActive},
				{SubscriptionID: 2, NodeID: nodeID, Status: database.SyncStatusActive},
			}, nil
		},
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			if id == 2 {
				return nil, errors.New("gone")
			}

			return &database.Subscription{ID: id, TelegramID: 42, Username: "alice"}, nil
		},
	}

	return NewSubscriptionService(db, clients, nodes, &config.Config{})
}

func TestGetNodeLive_AggregatesClients(t *testing.T) {
	t.Parallel()

	h := &nodeLiveHarness{}
	svc := newNodeLiveHarness(t, h)

	info, err := svc.GetNodeLive(context.Background(), 7)
	require.NoError(t, err)
	assert.True(t, info.Available)
	assert.Empty(t, info.PingError)
	assert.Equal(t, []string{XUIEmail("alice", 42)}, h.emails, "несуществующая подписка пропущена")
	assert.Equal(t, 1, info.Clients)
	assert.Equal(t, int64(150), info.TrafficBytes)
	assert.Equal(t, int64(111), info.LastOnline)
}

func TestGetNodeLive_PingFailure(t *testing.T) {
	t.Parallel()

	h := &nodeLiveHarness{pingErr: errors.New("connection refused")}
	svc := newNodeLiveHarness(t, h)

	info, err := svc.GetNodeLive(context.Background(), 7)
	require.NoError(t, err)
	assert.False(t, info.Available)
	assert.Equal(t, "connection refused", info.PingError)
	assert.Nil(t, h.emails, "при недоступной панели клиенты не опрашиваются")
}

func TestGetNodeLive_NodeWithoutReader(t *testing.T) {
	t.Parallel()

	h := &nodeLiveHarness{}
	svc := newNodeLiveHarness(t, h)

	info, err := svc.GetNodeLive(context.Background(), 404)
	require.NoError(t, err)
	assert.False(t, info.Available)
	assert.NotEmpty(t, info.PingError)
}
