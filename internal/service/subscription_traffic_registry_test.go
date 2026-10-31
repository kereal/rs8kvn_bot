package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trafficRegistryHarness wires a SubscriptionService with one readable node
// and any number of opaque (reader-less) nodes. trafficCalls counts
// GetClientTraffic invocations; profileCalls counts GetClientByEmail.
type trafficRegistryHarness struct {
	svc          *SubscriptionService
	trafficCalls int
	profileCalls int
	readerErr    error
	traffic      *xui.ClientTraffic
	profile      *xui.ClientRecord
	profileErr   error
	bindings     map[uint]database.SyncStatus
}

func newTrafficRegistryHarness(t *testing.T, h *trafficRegistryHarness) func(int) *SubscriptionService {
	t.Helper()

	return func(readableNodeID int) *SubscriptionService {
		reader := &testutil.XUIClient{
			GetClientTrafficFunc: func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
				h.trafficCalls++

				if h.readerErr != nil {
					return nil, h.readerErr
				}

				return h.traffic, nil
			},
			GetClientByEmailFunc: func(ctx context.Context, email string) (*xui.ClientRecord, error) {
				h.profileCalls++

				if h.profileErr != nil {
					return nil, h.profileErr
				}

				return h.profile, nil
			},
		}

		clients := NodeClients{}
		if readableNodeID > 0 {
			clients[uint(readableNodeID)] = NodeAccess{Reader: reader, Type: database.NodeType3xUI}
		}

		// Every bound node exists as an active runtime node; only readableNodeID
		// carries a reader (the rest model proxman/fetch-style opaque nodes).
		nodes := make([]database.Node, 0, len(h.bindings))
		for nodeID := range h.bindings {
			nodes = append(nodes, database.Node{
				ID:         nodeID,
				Type:       database.NodeType3xUI,
				IsActive:   true,
				Host:       fmt.Sprintf("http://node%d", nodeID),
				InboundIDs: "[1]",
			})
		}

		db := &testutil.ServiceStoreFake{
			GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
				bindings := make([]database.SubscriptionNode, 0, len(h.bindings))
				for nodeID, status := range h.bindings {
					bindings = append(bindings, database.SubscriptionNode{NodeID: nodeID, Status: status})
				}

				return bindings, nil
			},
		}

		svc := NewSubscriptionService(db, clients, nodes, &config.Config{})
		h.svc = svc

		return svc
	}
}

func TestGetWithTraffic_SkipsNodesWithoutReader(t *testing.T) {
	t.Parallel()

	h := &trafficRegistryHarness{
		traffic: &xui.ClientTraffic{Up: 1024, Down: 2048},
		profile: &xui.ClientRecord{Client: xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 1}},
		bindings: map[uint]database.SyncStatus{
			1: database.SyncStatusActive,
			2: database.SyncStatusActive,
		},
	}
	svc := newTrafficRegistryHarness(t, h)(1)

	sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
	db := svc.db.(*testutil.ServiceStoreFake)
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 1, TrafficLimit: 10 * 1024 * 1024 * 1024}, nil
	}

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.Equal(t, 1, h.trafficCalls, "only the node with a reader may be polled")
	assert.InDelta(t, 3.0/1024/1024, info.UsedGB, 1e-9)
	assert.Equal(t, "через 1 дн.", info.ResetInfo)
}

func TestGetWithTraffic_ReaderErrorDoesNotBreakAggregation(t *testing.T) {
	t.Parallel()

	h := &trafficRegistryHarness{
		readerErr: errors.New("connection refused"),
		bindings:  map[uint]database.SyncStatus{1: database.SyncStatusActive},
	}
	svc := newTrafficRegistryHarness(t, h)(1)

	sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
	db := svc.db.(*testutil.ServiceStoreFake)
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 1, TrafficLimit: 10 * 1024 * 1024 * 1024}, nil
	}

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.Zero(t, info.UsedGB)
	assert.Equal(t, "неизвестно", info.ResetInfo, "no panel data means the reset date is unknown, not absent")
	assert.Zero(t, h.profileCalls, "profile must not be read without a successful traffic source")
}

func TestGetWithTraffic_ResetLineScenarios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		profile      *xui.ClientRecord
		profileErr   error
		wantReset    string
		wantProfiles int
	}{
		{
			name:         "monthly profile shows real reset countdown",
			profile:      &xui.ClientRecord{Client: xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 1}},
			wantReset:    "через 1 дн.",
			wantProfiles: 1,
		},
		{
			name:         "never profile",
			profile:      &xui.ClientRecord{Client: xui.ClientConfig{TrafficReset: "never"}},
			wantReset:    "не сбрасывается",
			wantProfiles: 1,
		},
		{
			name:         "trial profile (never written by bot) is no reset",
			profile:      &xui.ClientRecord{Client: xui.ClientConfig{}},
			wantReset:    "не сбрасывается",
			wantProfiles: 1,
		},
		{
			name:         "panel failure keeps the card with unknown reset",
			profileErr:   errors.New("panel down"),
			wantReset:    "неизвестно",
			wantProfiles: 1,
		},
		{
			name:         "client removed from panel has nothing to reset",
			profileErr:   xui.ErrClientNotFound,
			wantReset:    "не сбрасывается",
			wantProfiles: 1,
		},
		{
			name:         "nil profile without error is unknown",
			profile:      nil,
			wantReset:    "неизвестно",
			wantProfiles: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := &trafficRegistryHarness{
				traffic:    &xui.ClientTraffic{Up: 1},
				profile:    tt.profile,
				profileErr: tt.profileErr,
				bindings:   map[uint]database.SyncStatus{1: database.SyncStatusActive},
			}
			svc := newTrafficRegistryHarness(t, h)(1)

			sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
			db := svc.db.(*testutil.ServiceStoreFake)
			db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
			db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
				return &database.Plan{ID: 1, TrafficLimit: 10 * 1024 * 1024 * 1024}, nil
			}

			_, info, err := svc.GetWithTraffic(context.Background(), 42)
			require.NoError(t, err)

			assert.Equal(t, tt.wantReset, info.ResetInfo)
			assert.Equal(t, tt.wantProfiles, h.profileCalls)
		})
	}
}

func TestGetWithTraffic_NoLimitSkipsProfileRead(t *testing.T) {
	t.Parallel()

	h := &trafficRegistryHarness{bindings: map[uint]database.SyncStatus{1: database.SyncStatusActive}}
	svc := newTrafficRegistryHarness(t, h)(1)

	sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
	db := svc.db.(*testutil.ServiceStoreFake)
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 1, TrafficLimit: 0}, nil
	}

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.Equal(t, "не сбрасывается", info.ResetInfo)
	assert.Zero(t, h.trafficCalls)
	assert.Zero(t, h.profileCalls, "no traffic limit: the panel profile is irrelevant and must not be fetched")
}

func TestGetWithTraffic_InactiveBindingNotPolled(t *testing.T) {
	t.Parallel()

	h := &trafficRegistryHarness{
		traffic:  &xui.ClientTraffic{Up: 1024},
		profile:  &xui.ClientRecord{Client: xui.ClientConfig{TrafficReset: "never"}},
		bindings: map[uint]database.SyncStatus{1: database.SyncStatusPendingAdd},
	}
	svc := newTrafficRegistryHarness(t, h)(1)

	sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
	db := svc.db.(*testutil.ServiceStoreFake)
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 1, TrafficLimit: 10 * 1024 * 1024 * 1024}, nil
	}

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.Zero(t, h.trafficCalls, "pending_add binding is not polled")
	assert.Equal(t, "неизвестно", info.ResetInfo)
}

// TestGetWithTraffic_NoReadersOnActiveBindings shows the honest card when all
// active bindings sit on reader-less nodes (proxman/fetch): no polling, no
// misleading 0 ГБ — Available=false carries the «недоступен» rendering.
func TestGetWithTraffic_NoReadersOnActiveBindings(t *testing.T) {
	t.Parallel()

	h := &trafficRegistryHarness{
		// readableNodeID=0: no node in the registry carries a reader.
		bindings: map[uint]database.SyncStatus{1: database.SyncStatusActive, 2: database.SyncStatusActive},
	}
	svc := newTrafficRegistryHarness(t, h)(0)

	sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
	db := svc.db.(*testutil.ServiceStoreFake)
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 1, TrafficLimit: 10 * 1024 * 1024 * 1024}, nil
	}

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.Zero(t, h.trafficCalls, "no readers means no polling")
	assert.False(t, info.Available, "no reader on any active node: traffic is not measured")
	assert.Zero(t, info.UsedGB)
	assert.Equal(t, "неизвестно", info.ResetInfo)

	msg := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", info, "https://example.test/sub/abc")
	assert.Contains(t, msg, "📊 Трафик: недоступен")
	assert.NotContains(t, msg, "0.00 из 10 Гб")
}

// TestGetWithTraffic_PartialReadersSumReadableOnly checks a mixed fleet: the
// sum covers the readable nodes and the figure is measured (Available=true).
func TestGetWithTraffic_PartialReadersSumReadableOnly(t *testing.T) {
	t.Parallel()

	h := &trafficRegistryHarness{
		traffic:  &xui.ClientTraffic{Up: 1024, Down: 2048},
		bindings: map[uint]database.SyncStatus{1: database.SyncStatusActive, 2: database.SyncStatusActive},
	}
	svc := newTrafficRegistryHarness(t, h)(1)

	sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
	db := svc.db.(*testutil.ServiceStoreFake)
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 1, TrafficLimit: 10 * 1024 * 1024 * 1024}, nil
	}

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.Equal(t, 1, h.trafficCalls, "only readable nodes are polled")
	assert.True(t, info.Available)
	assert.InDelta(t, 3.0/1024/1024, info.UsedGB, 1e-9)
}

// TestFormatSubscriptionMessage_TrafficUnavailable pins the honest rendering:
// a measured figure keeps the classic line, an unmeasurable one (no readers)
// shows «недоступен» without a progress bar.
func TestFormatSubscriptionMessage_TrafficUnavailable(t *testing.T) {
	t.Parallel()

	unavailable := &TrafficInfo{
		LimitGB:            100,
		ResetInfo:          "неизвестно",
		CreatedAtFormatted: "01.10.2026 12:00",
		ExpiresAtFormatted: "31.10.2026 12:00",
		PlanName:           "Free",
	}

	got := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", unavailable, "https://example.test/sub/abc")
	assert.Contains(t, got, "📊 Трафик: недоступен\n")
	assert.NotContains(t, got, "Гб")
	assert.NotContains(t, got, "█")

	// Measured traffic is unchanged by the Available flag rendering.
	measured := &TrafficInfo{
		UsedGB:             2.5,
		LimitGB:            100,
		Percentage:         2.5,
		ProgressBar:        "[█░░░]",
		ResetInfo:          "через 12 дн.",
		CreatedAtFormatted: "01.10.2026 12:00",
		ExpiresAtFormatted: "31.10.2026 12:00",
		PlanName:           "Free",
	}
	measured.Available = true

	got = FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", measured, "https://example.test/sub/abc")
	assert.Contains(t, got, "📊 Трафик: 2.50 из 100 Гб (2%)")
}

// TestFormatSubscriptionMessage_Golden pins the full card text: only the
// «🔄 Сброс» line may carry the new reset values, every other line must stay
// byte-identical.
func TestFormatSubscriptionMessage_Golden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		traffic *TrafficInfo
		want    string
	}{
		{
			name: "reset countdown",
			traffic: &TrafficInfo{
				UsedGB:             2.5,
				LimitGB:            100,
				Percentage:         2.5,
				ProgressBar:        "[█░░░]",
				ResetInfo:          "через 12 дн.",
				CreatedAtFormatted: "01.10.2026 12:00",
				ExpiresAtFormatted: "31.10.2026 12:00",
				PlanName:           "Free",
				Available:          true,
			},
			want: "📋 *Ваша подписка*\n\n" +
				"✌️ Статус: *активна*\n" +
				"💡 Тариф: *Free*\n" +
				"📊 Трафик: 2.50 из 100 Гб (2%)\n" +
				"[█░░░]\n\n" +
				"📅 Создана: 01.10.2026 12:00\n" +
				"⏰ Истекает: 31.10.2026 12:00\n" +
				"🔄 Сброс: через 12 дн.\n\n" +
				"🔗 Ссылка (для Happ)\n" +
				"`https://example.test/sub/abc`",
		},
		{
			name: "no reset",
			traffic: &TrafficInfo{
				UsedGB:             2.5,
				LimitGB:            100,
				Percentage:         2.5,
				ProgressBar:        "[█░░░]",
				ResetInfo:          "не сбрасывается",
				CreatedAtFormatted: "01.10.2026 12:00",
				ExpiresAtFormatted: "31.10.2026 12:00",
				PlanName:           "Free",
				Available:          true,
			},
			want: "📋 *Ваша подписка*\n\n" +
				"✌️ Статус: *активна*\n" +
				"💡 Тариф: *Free*\n" +
				"📊 Трафик: 2.50 из 100 Гб (2%)\n" +
				"[█░░░]\n\n" +
				"📅 Создана: 01.10.2026 12:00\n" +
				"⏰ Истекает: 31.10.2026 12:00\n" +
				"🔄 Сброс: не сбрасывается\n\n" +
				"🔗 Ссылка (для Happ)\n" +
				"`https://example.test/sub/abc`",
		},
		{
			name: "unknown reset",
			traffic: &TrafficInfo{
				UsedGB:             2.5,
				LimitGB:            100,
				Percentage:         2.5,
				ProgressBar:        "[█░░░]",
				ResetInfo:          "неизвестно",
				CreatedAtFormatted: "01.10.2026 12:00",
				ExpiresAtFormatted: "31.10.2026 12:00",
				PlanName:           "Free",
				Available:          true,
			},
			want: "📋 *Ваша подписка*\n\n" +
				"✌️ Статус: *активна*\n" +
				"💡 Тариф: *Free*\n" +
				"📊 Трафик: 2.50 из 100 Гб (2%)\n" +
				"[█░░░]\n\n" +
				"📅 Создана: 01.10.2026 12:00\n" +
				"⏰ Истекает: 31.10.2026 12:00\n" +
				"🔄 Сброс: неизвестно\n\n" +
				"🔗 Ссылка (для Happ)\n" +
				"`https://example.test/sub/abc`",
		},
		{
			name: "today reset",
			traffic: &TrafficInfo{
				UsedGB:             2.5,
				LimitGB:            100,
				Percentage:         2.5,
				ProgressBar:        "[█░░░]",
				ResetInfo:          "сегодня",
				CreatedAtFormatted: "01.10.2026 12:00",
				ExpiresAtFormatted: "31.10.2026 12:00",
				PlanName:           "Free",
				Available:          true,
			},
			want: "📋 *Ваша подписка*\n\n" +
				"✌️ Статус: *активна*\n" +
				"💡 Тариф: *Free*\n" +
				"📊 Трафик: 2.50 из 100 Гб (2%)\n" +
				"[█░░░]\n\n" +
				"📅 Создана: 01.10.2026 12:00\n" +
				"⏰ Истекает: 31.10.2026 12:00\n" +
				"🔄 Сброс: сегодня\n\n" +
				"🔗 Ссылка (для Happ)\n" +
				"`https://example.test/sub/abc`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", tt.traffic, "https://example.test/sub/abc")

			assert.Equal(t, tt.want, got)
		})
	}
}

// TestRegistryReaderIsSingleCapabilityCheck pins R3: the only reader
// capability check lives on NodeClients.Reader, and unknown IDs as well as
// reader-less node types report no capability.
func TestRegistryReaderIsSingleCapabilityCheck(t *testing.T) {
	t.Parallel()

	registry := NodeClients{
		1: {Provision: &mockVPNClient{}, Reader: &testutil.XUIClient{}, Type: database.NodeType3xUI},
		2: {Type: database.NodeTypeProxman},
	}

	_, ok := registry.Reader(1)
	assert.True(t, ok, "3x-ui node has a reader")

	_, ok = registry.Reader(2)
	assert.False(t, ok, "proxman node has no reader")

	_, ok = registry.Reader(3)
	assert.False(t, ok, "unknown node has no reader")

	_, ok = registry.Provision(1)
	assert.True(t, ok)

	_, ok = registry.Provision(2)
	assert.False(t, ok, "node without provision slot reports no capability")

	_, ok = registry.Provision(3)
	assert.False(t, ok, "unknown node reports no capability")
}
