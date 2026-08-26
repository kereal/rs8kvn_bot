package service

import (
	"context"
	"errors"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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

// trafficNotifySetup builds a service wired to one active 3x-ui node whose
// traffic and update behavior are controlled by the provided callbacks.
// It captures every sent message and claimed bit so tests can inspect what the
// service actually did. The mock objects are returned too so individual tests
// can swap behaviors (plan without limit, claim=false, send error, node error).
func trafficNotifySetup(t *testing.T, traffic *xui.ClientTraffic, planTrafficLimit int64, updateFn func(ctx context.Context, req xui.ClientRequest) error) (*SubscriptionService, *testutil.BotAPI, *testutil.DatabaseService, *testutil.XUIClient, *[]uint, *[]tgbotapi.MessageConfig) {
	t.Helper()

	var claimedBits []uint
	var sentMsgs []tgbotapi.MessageConfig

	db := &testutil.DatabaseService{
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: planID, TrafficLimit: planTrafficLimit}, nil // bytes; 0 = unlimited
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			return nil, nil
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
	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}

	bot := testutil.NewBotAPI()
	bot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok {
			sentMsgs = append(sentMsgs, mc)
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}
	svc := NewSubscriptionService(db, xuiClients, nil, nodes, &config.Config{})
	svc.SetBot(bot)

	return svc, bot, db, xuiClient, &claimedBits, &sentMsgs
}

func TestProcessTrafficNotifications_NinetyPercent(t *testing.T) {
	t.Parallel()

	// used = 95 GB of 100 GB, client still enabled.
	used := int64(95) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: true}
	svc, bot, _, _, _, sentMsgs := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)

	sub := &database.Subscription{ID: 1, TelegramID: 123456, Username: "testuser", PlanID: 1}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.NoError(t, err)
	require.GreaterOrEqual(t, bot.SendCount, 1, "90% warning must be sent")
	require.True(t, hasPremiumButton(*sentMsgs), "90% warning must offer the Premium menu")

	// Second call: bot.Send still succeeds but claim returns true each time in
	// the mock, so sendOnce proceeds; dedup is enforced by real DB claim logic.
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
}

func TestProcessTrafficNotifications_ExhaustedDisabled(t *testing.T) {
	t.Parallel()

	// used = 101 GB of 100 GB, panel disabled the client.
	used := int64(101) * 1024 * 1024 * 1024
	reenableCalled := false
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: false}
	svc, bot, _, _, _, sentMsgs := trafficNotifySetup(t, traffic, 100*1024*1024*1024, func(ctx context.Context, req xui.ClientRequest) error {
		reenableCalled = true
		return nil
	})

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
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: false, Total: 100 * 1024 * 1024 * 1024, ExpiresAt: 0, Reset: 30}
	svc, bot, _, _, _, sentMsgs := trafficNotifySetup(t, traffic, 100*1024*1024*1024, func(ctx context.Context, req xui.ClientRequest) error {
		gotEnable = req.Enable
		return nil
	})

	sub := &database.Subscription{ID: 3, TelegramID: 123458, Username: "reset", PlanID: 1}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.NoError(t, err)
	require.NotNil(t, gotEnable, "UpdateClient must be called to re-enable the client")
	require.NotNil(t, gotEnable)
	require.True(t, *gotEnable)
	require.GreaterOrEqual(t, bot.SendCount, 1, "reset/come-back notification must be sent")
	require.False(t, hasPremiumButton(*sentMsgs), "reset/come-back must NOT include a Premium sales button")
}

func TestProcessTrafficNotifications_SkippedWhenBelow90(t *testing.T) {
	t.Parallel()

	// used = 10 GB of 100 GB, enabled -> no notification.
	used := int64(10) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: true}
	svc, bot, _, _, _, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)

	sub := &database.Subscription{ID: 4, TelegramID: 123459, Username: "below", PlanID: 1}
	err := svc.ProcessTrafficNotifications(context.Background(), sub)
	require.NoError(t, err)
	require.Equal(t, 0, bot.SendCount, "no notification when usage is below 90%")
}

func TestProcessTrafficNotifications_NilSqlNoop(t *testing.T) {
	t.Parallel()

	svc, _, _, _, _, _ := trafficNotifySetup(t, &xui.ClientTraffic{Up: 1, Enable: true}, 100*1024*1024*1024, nil)
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
	svc, bot, _, _, _, _ := trafficNotifySetup(t, traffic, 0, nil)

	sub := &database.Subscription{ID: 12, TelegramID: 123462, Username: "nolimit", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.Equal(t, 0, bot.SendCount, "plans without a traffic limit must never get quota notifications")
}

func TestProcessTrafficNotifications_NoNodesRespondNoop(t *testing.T) {
	t.Parallel()

	used := int64(95) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: true}
	svc, bot, _, xuiClient, _, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024, nil)

	// Every live node returns an error -> no node snapshot succeeded.
	xuiClient.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return nil, errors.New("panel unreachable")
	}

	sub := &database.Subscription{ID: 13, TelegramID: 123463, Username: "nodown", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.Equal(t, 0, bot.SendCount, "must stay silent when no panel node answered")
}

func TestProcessTrafficNotifications_ReenableUpdateErrorNoNotification(t *testing.T) {
	t.Parallel()

	// Counter reset (used < limit) but client disabled. If re-enabling fails,
	// the come-back message must NOT be sent (the client is still off).
	used := int64(1) * 1024 * 1024 * 1024
	traffic := &xui.ClientTraffic{Up: used, Down: 0, Enable: false, Total: 100 * 1024 * 1024 * 1024}
	svc, bot, _, _, _, _ := trafficNotifySetup(t, traffic, 100*1024*1024*1024,
		func(ctx context.Context, req xui.ClientRequest) error {
			return errors.New("panel update failed")
		})

	sub := &database.Subscription{ID: 14, TelegramID: 123464, Username: "reneferr", PlanID: 1}
	require.NoError(t, svc.ProcessTrafficNotifications(context.Background(), sub))
	require.Equal(t, 0, bot.SendCount, "must not invite the user back when the client could not be re-enabled")
}