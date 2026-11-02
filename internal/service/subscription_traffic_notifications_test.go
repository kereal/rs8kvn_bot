package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hasPremiumButton reports whether any of the captured message replies contains
// a button with callback `buy_premium_list` (the premium-zone entry point).
func hasPremiumButton(msgs []tgbotapi.MessageConfig) bool {
	for _, m := range msgs {
		kb, ok := m.ReplyMarkup.(*tgbotapi.InlineKeyboardMarkup)
		if !ok {
			continue
		}
		for _, row := range kb.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData != nil && *btn.CallbackData == "buy_premium_list" {
					return true
				}
			}
		}
	}
	return false
}

// stubSubAndPlan wires a fixed subscription (ID 7, TelegramID 42) and a plan
// with the given traffic limit into the harness' fake store. Call before
// GetWithTraffic; the returned value is unused after stubbing.
func (h *trafficRegistryHarness) stubSubAndPlan(svc *SubscriptionService, trafficLimit int64) *database.Subscription {
	sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
	db := svc.db.(*testutil.ServiceStoreFake)
	db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 1, TrafficLimit: trafficLimit}, nil
	}

	return sub
}

// trafficNotifySetup builds a service wired to one active 3x-ui node whose
// traffic and update behavior are controlled by the provided callbacks.
// It captures every sent message and claimed bit so tests can inspect what the
// service actually did. The mock objects are returned too so individual tests
// can swap behaviors (plan without limit, claim=false, send error, node error).
func trafficNotifySetup(t *testing.T, traffic *xui.ClientTraffic, planTrafficLimit int64, updateFn func(ctx context.Context, req xui.ClientRequest) error) (*SubscriptionService, *testutil.BotAPI, *testutil.ServiceStoreFake, *testutil.XUIClient, *[]uint, *[]tgbotapi.MessageConfig) {
	t.Helper()

	var claimedBits []uint
	var sentMsgs []tgbotapi.MessageConfig

	db := &testutil.ServiceStoreFake{
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: planID, TrafficLimit: planTrafficLimit}, nil // bytes; 0 = unlimited
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{{NodeID: 1, Status: database.SyncStatusActive}}, nil
		},
		ClaimTrafficReminderFunc: func(ctx context.Context, id uint, bit int) (bool, error) {
			claimedBits = append(claimedBits, uint(bit))
			return true, nil
		},
		ReleaseTrafficReminderFunc: func(ctx context.Context, id uint, bit int) error {
			return nil
		},
	}

	xuiClient := &testutil.XUIClient{
		GetClientTrafficFunc: func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
			return traffic, nil
		},
		UpdateClientFunc: func(ctx context.Context, req xui.ClientRequest) error {
			if updateFn != nil {
				return updateFn(ctx, req)
			}
			return nil
		},
	}

	nodes := []database.Node{{ID: 1, IsActive: true, Host: "http://node1", InboundIDs: "[1]"}}
	clients := NodeClients{1: {Reader: xuiClient}}

	bot := testutil.NewBotAPI()
	bot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok {
			sentMsgs = append(sentMsgs, mc)
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}
	svc := NewSubscriptionService(db, clients, nodes, &config.Config{})
	svc.SetBot(bot)

	return svc, bot, db, xuiClient, &claimedBits, &sentMsgs
}

func TestProcessTrafficNotifications_NinetyPercent(t *testing.T) {
	t.Parallel()

	// used = 95 GB of 100 GB, client still enabled.
	used := int64(95) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: true}
	svc, bot, db, xuiClient, claimedBits, sentMsgs := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)
	_ = db
	_ = xuiClient
	_ = claimedBits

	sub := &database.Subscription{ID: 1, TelegramID: 123456, Username: "testuser", PlanID: 1}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.NoError(t, err)
	require.GreaterOrEqual(t, bot.SendCount, 1, "90% warning must be sent")
	require.True(t, hasPremiumButton(*sentMsgs), "90% warning must offer the Premium menu")
}

func TestProcessTrafficNotifications_ExhaustedDisabled(t *testing.T) {
	t.Parallel()

	// used = 101 GB of 100 GB, panel disabled the client.
	used := int64(101) * 1024 * 1024 * 1024
	reenableCalled := false
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: false}
	svc, bot, db, xuiClient, claimedBits, sentMsgs := trafficNotifySetup(t, traffic, 100*1024*1024*1024, func(ctx context.Context, req xui.ClientRequest) error {
		reenableCalled = true
		return nil
	})
	_ = db
	_ = xuiClient
	_ = claimedBits

	sub := &database.Subscription{ID: 2, TelegramID: 123457, Username: "exhausted", PlanID: 1}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.NoError(t, err)
	require.GreaterOrEqual(t, bot.SendCount, 1, "exhausted notification must be sent")
	require.True(t, hasPremiumButton(*sentMsgs), "exhausted notification must offer the Premium menu")
	require.False(t, reenableCalled, "must NOT re-enable a client whose quota is still exceeded")
}

func TestProcessTrafficNotifications_ResetAndReenable(t *testing.T) {
	t.Parallel()

	// Counter was reset (used = 1 GB < 100 GB) but the client is still disabled
	// by the panel -> the worker must re-enable it and invite the user back.
	used := int64(1) * 1024 * 1024 * 1024
	var gotEnable *bool
	var gotRequest xui.ClientRequest
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: false, Total: 100 * 1024 * 1024 * 1024, ExpiresAt: 0, Reset: 30, UUID: "reset-client"}
	svc, bot, db, xuiClient, _, sentMsgs := trafficNotifySetup(t, traffic, 100*1024*1024*1024, func(ctx context.Context, req xui.ClientRequest) error {
		gotEnable = req.Enable
		gotRequest = req
		return nil
	})
	// The panel's traffic endpoint does not return the auto-renew profile, so
	// the worker must read it back from the client card and forward it,
	// otherwise the re-enable update resets trafficReset to never.
	xuiClient.GetClientByEmailFunc = func(ctx context.Context, email string) (*xui.ClientRecord, error) {
		return &xui.ClientRecord{Client: xui.ClientConfig{Email: email, Reset: 30, ResetDay: 5, ResetMax: 3, TrafficReset: "monthly", TrafficResetDay: 1}, InboundIDs: []int{1}}, nil
	}
	db.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{TrafficRemindersSent: database.TrafficBitExhausted}, nil
	}

	sub := &database.Subscription{ID: 3, TelegramID: 123458, Username: "reset", PlanID: 1, TrafficRemindersSent: database.TrafficBitExhausted}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.NoError(t, err)
	require.NotNil(t, gotEnable, "UpdateClient must be called to re-enable the client")
	require.NotNil(t, gotEnable)
	require.True(t, *gotEnable)
	require.GreaterOrEqual(t, bot.SendCount, 1, "reset/come-back notification must be sent")
	require.False(t, hasPremiumButton(*sentMsgs), "reset/come-back must NOT include a Premium sales button")

	// Re-enabling a free client must preserve its v3.7.0 auto-renew profile,
	// otherwise the panel would receive zero/empty values and overwrite it.
	assert.Equal(t, 30, gotRequest.ResetDays)
	assert.Equal(t, 5, gotRequest.ResetDay, "ResetDay must be propagated from inbound profile, not zero")
	assert.Equal(t, 3, gotRequest.ResetMax, "ResetMax must be propagated from inbound profile, not zero")
	assert.Equal(t, "monthly", gotRequest.TrafficReset)
	assert.Equal(t, 1, gotRequest.TrafficResetDay)
}
func TestProcessTrafficReset_ReenablesWithoutNotificationBit(t *testing.T) {
	t.Parallel()

	traffic := &xui.ClientTraffic{
		Up: 100 * 1024 * 1024 * 1024, Enable: false, Total: 100 * 1024 * 1024 * 1024,
		Reset: 30, UUID: "reset-without-notification",
	}
	svc, bot, db, xuiClient, claimedBits, sentMsgs := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)
	_ = db
	updated := false
	xuiClient.UpdateClientFunc = func(context.Context, xui.ClientRequest) error {
		updated = true
		return nil
	}
	xuiClient.GetClientByEmailFunc = func(context.Context, string) (*xui.ClientRecord, error) {
		return &xui.ClientRecord{Client: xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 1}, InboundIDs: []int{1}}, nil
	}

	// Phase 1: quota exceeded in recovery mode, no notification bit saved yet.
	// The worker must record TrafficBitExhausted so later reset recovery can
	// recognize this subscription — without sending any message.
	sub := &database.Subscription{ID: 30, TelegramID: 123490, Username: "reset-no-bit", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficReset(context.Background(), sub))
	assert.Equal(t, []uint{uint(database.TrafficBitExhausted)}, *claimedBits,
		"recovery mode must record the exhausted bit without sending a notification")
	assert.Equal(t, 0, bot.SendCount, "recovery mode must not send Telegram messages")
	assert.False(t, updated, "recovery mode must not re-enable while the quota is still exceeded")

	// Phase 2: the panel reset the counter (used < limit) but left the client
	// disabled. With the saved exhausted bit the recovery pass must re-enable
	// the client — still without any Telegram messages.
	traffic.Up = int64(1) * 1024 * 1024 * 1024
	sub.TrafficRemindersSent = database.TrafficBitExhausted
	require.NoError(t, svc.ProcessTrafficReset(context.Background(), sub))
	assert.True(t, updated, "recovery mode must re-enable a reset client using the recorded exhaustion state")
	assert.Equal(t, 0, bot.SendCount, "recovery mode must not send Telegram messages")
	assert.Zero(t, *sentMsgs, "no notification of any kind must be sent in recovery mode")
}

func TestProcessTrafficNotifications_SkippedWhenBelow90(t *testing.T) {
	t.Parallel()

	// used = 10 GB of 100 GB, enabled -> no notification.
	used := int64(10) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: true}
	svc, bot, db, xuiClient, claimedBits, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)
	_ = db
	_ = xuiClient
	_ = claimedBits

	sub := &database.Subscription{ID: 4, TelegramID: 123459, Username: "below", PlanID: 1}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.NoError(t, err)
	require.Equal(t, 0, bot.SendCount, "no notification when usage is below 90%")
}

func TestProcessTrafficNotifications_NilSqlNoop(t *testing.T) {
	t.Parallel()

	svc, _, _, xuiClient, claimedBits, sentMsgs := trafficNotifySetup(t, &xui.ClientTraffic{Up: 1, Enable: true}, 100*1024*1024*1024, nil)
	_ = xuiClient
	_ = claimedBits
	_ = sentMsgs
	err := svc.ProcessTrafficNotifications(context.Background(), nil)
	require.NoError(t, err)

	// TelegramID <= 0 -> noop.
	bad := &database.Subscription{ID: 5, TelegramID: 0, Username: "anon", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), bad))
}

func TestProcessTrafficNotifications_DedupClaimedBit(t *testing.T) {
	t.Parallel()

	// used >= limit so the exhausted path claims the bit; the mock DB returns
	// false on claim (bit already set) -> sendOnce must be a silent no-op.
	used := int64(200) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: false}
	svc, bot, db, _, claimedBits, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)

	db.ClaimTrafficReminderFunc = func(ctx context.Context, id uint, bit int) (bool, error) {
		return false, nil // already sent -> no duplicate message
	}

	sub := &database.Subscription{ID: 10, TelegramID: 123460, Username: "dedup", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.Equal(t, 0, bot.SendCount, "must NOT send a duplicate notification when the bit is already set")
	require.Len(t, *claimedBits, 0, "claim returning false should not have incremented attempted claims")
}

func TestProcessTrafficNotifications_SendErrorReleasesClaim(t *testing.T) {
	t.Parallel()

	used := int64(95) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: true}
	svc, bot, db, _, claimedBits, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)

	released := false
	db.ReleaseTrafficReminderFunc = func(ctx context.Context, id uint, bit int) error {
		released = true
		return nil
	}
	bot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		return tgbotapi.Message{}, errors.New("telegram timeout")
	}

	sub := &database.Subscription{ID: 11, TelegramID: 123461, Username: "senderr", PlanID: 1}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.Error(t, err, "send failure must surface so the worker can retry next cycle")
	require.True(t, released, "send failure must release the claimed bit for a future retry")
	require.Equal(t, []uint{uint(database.TrafficBit90)}, *claimedBits, "the 90% bit must be the one claimed and then released")
}

func TestProcessTrafficNotifications_PlanWithoutLimitNoop(t *testing.T) {
	t.Parallel()

	// Plan has traffic_limit == 0 (free premium/unlimited) -> no traffic handling.
	used := int64(200) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: false}
	svc, bot, db, xuiClient, claimedBits, _ := trafficNotifySetup(t, traffic, 0, nil)
	_ = db
	_ = xuiClient
	_ = claimedBits

	sub := &database.Subscription{ID: 12, TelegramID: 123462, Username: "nolimit", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.Equal(t, 0, bot.SendCount, "plans without a traffic limit must never get quota notifications")
}

func TestProcessTrafficNotifications_NoNodesRespondNoop(t *testing.T) {
	t.Parallel()

	used := int64(95) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: true}
	svc, bot, db, xuiClient, claimedBits, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)
	_ = db
	_ = claimedBits

	// Every live node returns an error -> no node snapshot succeeded.
	xuiClient.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return nil, errors.New("panel unreachable")
	}

	sub := &database.Subscription{ID: 13, TelegramID: 123463, Username: "nodown", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.Equal(t, 0, bot.SendCount, "must stay silent when no panel node answered")
}

func TestProcessTrafficNotifications_NodeBindingErrorSkipsWithoutUpdate(t *testing.T) {
	t.Parallel()

	used := int64(1) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Enable: false, Total: 100 * 1024 * 1024 * 1024}
	svc, bot, db, xuiClient, _, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)
	updated := false
	xuiClient.UpdateClientFunc = func(ctx context.Context, req xui.ClientRequest) error {
		updated = true
		return nil
	}
	db.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return nil, errors.New("database unavailable")
	}

	sub := &database.Subscription{ID: 15, TelegramID: 123465, Username: "binding-error", PlanID: 1}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.Error(t, err, "binding lookup errors must be surfaced to the worker")
	require.False(t, updated, "must not update a client when node bindings cannot be loaded")
	require.Equal(t, 0, bot.SendCount, "must not notify when node bindings cannot be loaded")
}

// TestProcessTrafficNotifications_DisabledWithoutExhaustedBitStaysDisabled:
// the counter is below the limit but the panel disabled the client. The skip
// comes from the exhausted-bit guard (TrafficRemindersSent&Exhausted==0):
// only a previously exhausted subscription may be re-enabled, so this pass
// neither re-enables nor notifies.
func TestProcessTrafficNotifications_DisabledWithoutExhaustedBitStaysDisabled(t *testing.T) {
	t.Parallel()

	used := int64(1) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Enable: false, Total: 100 * 1024 * 1024 * 1024, UUID: "client"}
	var updated bool
	db := &testutil.ServiceStoreFake{
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{TrafficLimit: 100 * 1024 * 1024 * 1024}, nil
		},
		GetBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{
				{NodeID: 1, Status: database.SyncStatusActive},
				{NodeID: 2, Status: database.SyncStatusActive},
			}, nil
		},
	}
	client1 := &testutil.XUIClient{
		GetClientTrafficFunc: func(context.Context, string) (*xui.ClientTraffic, error) { return traffic, nil },
		UpdateClientFunc:     func(context.Context, xui.ClientRequest) error { updated = true; return nil },
	}
	client2 := &testutil.XUIClient{
		GetClientTrafficFunc: func(context.Context, string) (*xui.ClientTraffic, error) { return nil, errors.New("node unavailable") },
	}
	bot := testutil.NewBotAPI()
	svc := NewSubscriptionService(db, NodeClients{1: {Reader: client1}, 2: {Reader: client2}}, []database.Node{
		{ID: 1, IsActive: true, Host: "http://node1", InboundIDs: "[1]"},
		{ID: 2, IsActive: true, Host: "http://node2", InboundIDs: "[2]"},
	}, &config.Config{})
	svc.SetBot(bot)

	sub := &database.Subscription{ID: 16, TelegramID: 123466, Username: "partial", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.False(t, updated, "no exhausted bit: the client must not be re-enabled")
	require.Equal(t, 0, bot.SendCount, "no exhausted bit: the pass must not notify")
}

func TestProcessTrafficNotifications_RejectsInvalidTraffic(t *testing.T) {
	t.Parallel()

	traffic := &xui.ClientTraffic{Up: -1, Down: 0, Enable: true}
	svc, bot, db, xuiClient, claimedBits, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)
	_ = db
	_ = xuiClient
	_ = claimedBits
	sub := &database.Subscription{ID: 17, TelegramID: 123467, Username: "invalid", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.Equal(t, 0, bot.SendCount)
}

func TestProcessTrafficNotifications_ReenableUpdateErrorNoNotification(t *testing.T) {
	t.Parallel()

	// Counter reset (used < limit) but client disabled. If re-enabling fails,
	// the come-back message must NOT be sent (the client is still off).
	used := int64(1) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: false, Total: 100 * 1024 * 1024 * 1024, UUID: "reset-client"}
	svc, bot, db, xuiClient, claimedBits, sentMsgs := trafficNotifySetup(t, traffic, 100*1024*1024*1024,
		func(ctx context.Context, req xui.ClientRequest) error {
			return errors.New("panel update failed")
		})
	_ = db
	_ = xuiClient
	_ = claimedBits
	_ = sentMsgs

	sub := &database.Subscription{ID: 14, TelegramID: 123464, Username: "reneferr", PlanID: 1, TrafficRemindersSent: database.TrafficBitExhausted}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.Equal(t, 0, bot.SendCount, "must not invite the user back when the client could not be re-enabled")
}

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

// TestGetWithTraffic_SkipsNodesWithoutReader checks that only the node with a
// reader is polled and the reset line is derived from the panel profile. The
// expected countdown is computed for the same monthly profile, so the test is
// calendar-independent (a day-1 monthly reset is «сегодня» on the 1st and
// «через 1 дн.» the day before).
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

	h.stubSubAndPlan(svc, 10*1024*1024*1024)

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.Equal(t, 1, h.trafficCalls, "only the node with a reader may be polled")
	assert.InDelta(t, 3.0/1024/1024, info.UsedGB, 1e-9)
	assert.True(t, info.Available, "a successful read keeps the figure measured")
	assert.Equal(t, 10, info.LimitGB, "LimitGB derives from the plan traffic limit")
	wantReset := describeTrafficReset(time.Now(), xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 1}).Text
	assert.Equal(t, wantReset, info.ResetInfo)
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
			// The expectation is computed for the same profile, so the case is
			// calendar-independent (a day-1 monthly reset is «сегодня» on the
			// 1st, «через 1 дн.» the day before).
			name:         "monthly profile shows real reset countdown",
			profile:      &xui.ClientRecord{Client: xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 1}},
			wantReset:    describeTrafficReset(time.Now(), xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 1}).Text,
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

	h.stubSubAndPlan(svc, 0)

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

	h.stubSubAndPlan(svc, 10*1024*1024*1024)

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

	h.stubSubAndPlan(svc, 10*1024*1024*1024)

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.Zero(t, h.trafficCalls, "no readers means no polling")
	assert.False(t, info.Available, "no reader on any active node: traffic is not measured")
	assert.Zero(t, info.UsedGB)
	assert.Equal(t, "неизвестно", info.ResetInfo)

	msg := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", info, "https://example.test/sub/abc")
	assert.Contains(t, msg, "📊 Трафик: измерение недоступно")
	assert.NotContains(t, msg, "0.00 из 10 Гб")
}

// TestGetWithTraffic_AllReadsFailShowsUnavailable checks that when readers
// exist but every panel read fails, the card is honest: Available=false and
// «измерение недоступно» instead of a misleading 0.00.
func TestGetWithTraffic_AllReadsFailShowsUnavailable(t *testing.T) {
	t.Parallel()

	h := &trafficRegistryHarness{
		readerErr: errors.New("connection refused"),
		bindings:  map[uint]database.SyncStatus{1: database.SyncStatusActive},
	}
	svc := newTrafficRegistryHarness(t, h)(1)

	h.stubSubAndPlan(svc, 10*1024*1024*1024)

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.False(t, info.Available, "all reads failed: traffic is not measured")
	assert.Zero(t, info.UsedGB)
	assert.Equal(t, "неизвестно", info.ResetInfo, "no panel data means the reset date is unknown, not absent")
	assert.Zero(t, h.profileCalls, "profile must not be read without a successful traffic source")

	msg := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", info, "https://example.test/sub/abc")
	assert.Contains(t, msg, "📊 Трафик: измерение недоступно")
	assert.NotContains(t, msg, "0.00 из 10 Гб")
	assert.NotContains(t, msg, "█")
}

// TestGetWithTraffic_PartialReadFailureStillMeasured checks a mixed fleet with
// a failing reader: one successful read is enough to keep the figure measured
// (Available=true) with the classic rendering.
func TestGetWithTraffic_PartialReadFailureStillMeasured(t *testing.T) {
	t.Parallel()

	h := &trafficRegistryHarness{
		traffic:  &xui.ClientTraffic{Up: 1024, Down: 2048},
		bindings: map[uint]database.SyncStatus{1: database.SyncStatusActive, 2: database.SyncStatusActive},
	}
	svc := newTrafficRegistryHarness(t, h)(1)

	// Вторая активная нода тоже с читателем, но панель недоступна: хотя бы
	// одно успешное чтение делает цифру измеренной.
	failReader := &testutil.XUIClient{
		GetClientTrafficFunc: func(context.Context, string) (*xui.ClientTraffic, error) {
			return nil, errors.New("panel unreachable")
		},
	}
	h.svc.clients[2] = NodeAccess{Reader: failReader, Type: database.NodeType3xUI}

	h.stubSubAndPlan(svc, 10*1024*1024*1024)

	_, info, err := svc.GetWithTraffic(context.Background(), 42)
	require.NoError(t, err)

	assert.True(t, info.Available, "at least one successful read keeps the figure measured")
	assert.InDelta(t, 3.0/1024/1024, info.UsedGB, 1e-9)

	msg := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", info, "https://example.test/sub/abc")
	assert.Contains(t, msg, "📊 Трафик: 0.00 из 10 Гб (0%)")
	assert.Contains(t, msg, "⬜")
}

// TestGetWithTraffic_PanelLimitOverridesPlanDisplay checks the manual panel
// limit: when traffic.Total diverges from the plan by more than 1%, the panel
// value is shown to the user; within 1% the plan limit stays. Notification
// logic keeps reading the plan — only the displayed limit changes.
func TestGetWithTraffic_PanelLimitOverridesPlanDisplay(t *testing.T) {
	t.Parallel()

	gib := int64(1024 * 1024 * 1024)
	tests := []struct {
		name       string
		panelTotal int64
		planBytes  int64
		wantLimit  int
	}{
		{
			name:       "panel limit diverges from plan: panel wins",
			panelTotal: 20 * gib,
			planBytes:  10 * gib,
			wantLimit:  20,
		},
		{
			name:       "panel limit within 1% of plan: plan stays",
			panelTotal: 10 * gib,
			planBytes:  10 * gib,
			wantLimit:  10,
		},
		{
			name:       "small panel divergence stays under 1%: plan stays",
			panelTotal: 10*gib + gib/200,
			planBytes:  10 * gib,
			wantLimit:  10,
		},
		{
			name:       "sub-GiB panel total: plan stays",
			panelTotal: gib / 2,
			planBytes:  10 * gib,
			wantLimit:  10,
		},
		{
			name:       "panel total missing: plan stays",
			panelTotal: 0,
			planBytes:  10 * gib,
			wantLimit:  10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := &trafficRegistryHarness{
				traffic:  &xui.ClientTraffic{Up: 1, Total: tt.panelTotal},
				profile:  &xui.ClientRecord{Client: xui.ClientConfig{TrafficReset: "never"}},
				bindings: map[uint]database.SyncStatus{1: database.SyncStatusActive},
			}
			svc := newTrafficRegistryHarness(t, h)(1)

			sub := &database.Subscription{ID: 7, TelegramID: 42, Username: "user", PlanID: 1}
			db := svc.db.(*testutil.ServiceStoreFake)
			db.GetByTelegramIDFunc = func(context.Context, int64) (*database.Subscription, error) { return sub, nil }
			db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
				return &database.Plan{ID: 1, TrafficLimit: tt.planBytes}, nil
			}

			_, info, err := svc.GetWithTraffic(context.Background(), 42)
			require.NoError(t, err)

			assert.Equal(t, tt.wantLimit, info.LimitGB)
			assert.True(t, info.Available)
		})
	}
}

// TestFormatSubscriptionMessage_TrafficUnavailable pins the honest rendering:
// a measured figure keeps the classic line, an unmeasurable one (no readers or
// all reads failed) shows «измерение недоступно» without a progress bar.
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
	assert.Contains(t, got, "📊 Трафик: измерение недоступно\n")
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

// TestProcessTrafficNotifications_NilBotIsSilentNoop: with the notification
// worker disabled (no bot wired), the pass is a silent noop — no reminder bit
// is claimed, so the real notification worker can later fire on schedule.
func TestProcessTrafficNotifications_NilBotIsSilentNoop(t *testing.T) {
	t.Parallel()

	var claimedBits []uint

	db := &testutil.ServiceStoreFake{
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 1, TrafficLimit: 100 * 1024 * 1024 * 1024}, nil
		},
		GetBySubscriptionIDFunc: func(context.Context, uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{{NodeID: 1, Status: database.SyncStatusActive}}, nil
		},
		ClaimTrafficReminderFunc: func(ctx context.Context, id uint, bit int) (bool, error) {
			claimedBits = append(claimedBits, uint(bit))

			return true, nil
		},
	}
	client := &testutil.XUIClient{
		GetClientTrafficFunc: func(context.Context, string) (*xui.ClientTraffic, error) {
			return &xui.ClientTraffic{Up: 95 * 1024 * 1024 * 1024, Down: 0, Enable: true}, nil
		},
	}

	// No SetBot: notifications are disabled for this service instance.
	svc := NewSubscriptionService(db, NodeClients{1: {Reader: client}}, []database.Node{{ID: 1, IsActive: true, Host: "http://node1", InboundIDs: "[1]"}}, &config.Config{})

	sub := &database.Subscription{ID: 30, TelegramID: 123480, Username: "nobot", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	assert.Empty(t, claimedBits, "nil bot: no reminder bit may be claimed")
}

// trafficNotifySetupTwoNodes wires a service to two active 3x-ui nodes with
// independent readers so tests can exercise partial node failure. Claimed bits
// and sent messages are captured for assertions.
func trafficNotifySetupTwoNodes(t *testing.T, limit int64, client1, client2 *testutil.XUIClient) (*SubscriptionService, *testutil.BotAPI, *testutil.ServiceStoreFake, *[]uint, *[]tgbotapi.MessageConfig) {
	t.Helper()

	var claimedBits []uint
	var sentMsgs []tgbotapi.MessageConfig

	db := &testutil.ServiceStoreFake{
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: planID, TrafficLimit: limit}, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return []database.SubscriptionNode{
				{NodeID: 1, Status: database.SyncStatusActive},
				{NodeID: 2, Status: database.SyncStatusActive},
			}, nil
		},
		ClaimTrafficReminderFunc: func(ctx context.Context, id uint, bit int) (bool, error) {
			claimedBits = append(claimedBits, uint(bit))
			return true, nil
		},
		ReleaseTrafficReminderFunc: func(ctx context.Context, id uint, bit int) error {
			return nil
		},
	}

	bot := testutil.NewBotAPI()
	bot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok {
			sentMsgs = append(sentMsgs, mc)
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	svc := NewSubscriptionService(db, NodeClients{1: {Reader: client1}, 2: {Reader: client2}}, []database.Node{
		{ID: 1, IsActive: true, Host: "http://node1", InboundIDs: "[1]"},
		{ID: 2, IsActive: true, Host: "http://node2", InboundIDs: "[2]"},
	}, &config.Config{})
	svc.SetBot(bot)

	return svc, bot, db, &claimedBits, &sentMsgs
}

// TestProcessTrafficNotifications_ClientNotFoundOnOneNodeStillDecides checks
// that a failed read on one node (missing client or transport error) does not
// abort the pass: the exhausted decision fires from the surviving node's
// snapshot instead of being lost to the failed one.
func TestProcessTrafficNotifications_ClientNotFoundOnOneNodeStillDecides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		node1Err error
	}{
		{"client removed from panel", xui.ErrClientNotFound},
		{"transport error", errors.New("connection refused")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Node 1 fails with tt.node1Err; node 2 reports exhausted quota.
			used := int64(101) * 1024 * 1024 * 1024
			client1 := &testutil.XUIClient{
				GetClientTrafficFunc: func(context.Context, string) (*xui.ClientTraffic, error) {
					return nil, tt.node1Err
				},
			}
			client2 := &testutil.XUIClient{
				GetClientTrafficFunc: func(context.Context, string) (*xui.ClientTraffic, error) {
					return &xui.ClientTraffic{Up: used, Down: 0, Enable: false}, nil
				},
			}
			svc, bot, _, claimedBits, sentMsgs := trafficNotifySetupTwoNodes(t, 100*1024*1024*1024, client1, client2)

			sub := &database.Subscription{ID: 20, TelegramID: 123470, Username: "ghost-node", PlanID: 1}
			require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
			require.Equal(t, []uint{uint(database.TrafficBitExhausted)}, *claimedBits,
				"exhausted decision must fire from the healthy node despite the failed read on node 1")
			require.GreaterOrEqual(t, bot.SendCount, 1, "exhausted notification must be sent")
			require.True(t, hasPremiumButton(*sentMsgs), "exhausted notification must offer the Premium menu")
		})
	}
}

func TestProcessTrafficNotifications_AllNodesFailedSkipsSubscription(t *testing.T) {
	t.Parallel()

	// First subscription: both nodes fail -> its pass is skipped silently.
	// The next subscription, polled through the same nodes that now answer,
	// must still be processed by the worker loop.
	client1 := &testutil.XUIClient{
		GetClientTrafficFunc: func(_ context.Context, email string) (*xui.ClientTraffic, error) {
			if email == "first" {
				return nil, errors.New("node down")
			}
			return &xui.ClientTraffic{Up: 95 * 1024 * 1024 * 1024, Enable: true}, nil
		},
	}
	client2 := &testutil.XUIClient{
		GetClientTrafficFunc: func(_ context.Context, email string) (*xui.ClientTraffic, error) {
			if email == "first" {
				return nil, xui.ErrClientNotFound
			}
			return &xui.ClientTraffic{Up: 1 * 1024 * 1024 * 1024, Enable: true}, nil
		},
	}
	svc, bot, _, claimedBits, _ := trafficNotifySetupTwoNodes(t, 100*1024*1024*1024, client1, client2)

	first := &database.Subscription{ID: 21, TelegramID: 123471, Username: "first", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), first),
		"an all-nodes-failed pass must be skipped, not reported as an error")
	require.Empty(t, *claimedBits, "a skipped subscription must not claim any notification bits")
	require.Equal(t, 0, bot.SendCount, "a skipped subscription must not send messages")

	second := &database.Subscription{ID: 22, TelegramID: 123472, Username: "second", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), second))
	require.Equal(t, []uint{uint(database.TrafficBit90)}, *claimedBits,
		"the next subscription must be processed normally after a skipped one")
	require.Equal(t, 1, bot.SendCount, "the next subscription must get its 90% warning")
}

func TestProcessTrafficNotifications_PartialPreservesWarning(t *testing.T) {
	svc, bot, db, client, claimed, messages := trafficNotifySetup(t, &xui.ClientTraffic{Up: 50, Enable: true}, 100, nil)
	db.GetBySubscriptionIDFunc = func(context.Context, uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{{NodeID: 1, Status: database.SyncStatusActive}, {NodeID: 2, Status: database.SyncStatusActive}}, nil
	}
	svc.nodes = append(svc.nodes, database.Node{ID: 2, IsActive: true, Host: "http://node2"})
	svc.clients[2] = NodeAccess{Reader: client}
	svc.clients[1] = NodeAccess{Reader: &testutil.XUIClient{GetClientTrafficFunc: func(context.Context, string) (*xui.ClientTraffic, error) { return nil, errors.New("offline") }}}
	released := false
	db.ReleaseTrafficReminderFunc = func(context.Context, uint, int) error { released = true; return nil }
	sub := &database.Subscription{ID: 1, TelegramID: 42, PlanID: 1, TrafficRemindersSent: database.TrafficBit90}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	assert.False(t, released, "partial traffic must not clear the warning")
	assert.Empty(t, *claimed)
	assert.Empty(t, *messages)
	assert.Zero(t, bot.SendCount)
}
