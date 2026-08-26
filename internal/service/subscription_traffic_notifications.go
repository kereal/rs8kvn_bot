package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// trafficQuotaWarningFraction is the fraction of the plan's traffic limit that
// triggers the "almost exhausted" warning.
const trafficQuotaWarningFraction = 0.9

// trafficNode is a concrete live panel client a subscription is provisioned on.
type trafficNode struct {
	nodeID  uint
	inbound []int
	client  interfaces.XUIClient
	email   string
}

// trafficNotifyCandidates returns the active 3x-ui nodes a subscription is
// actually provisioned on. Mirrors GetWithTraffic: only nodes with an active
// binding are polled so other plans' nodes and pending_* states are excluded.
func (s *SubscriptionService) trafficNotifyCandidates(ctx context.Context, sub *database.Subscription) ([]trafficNode, error) {
	subNodes, err := s.db.GetBySubscriptionID(ctx, sub.ID)
	if err != nil {
		logger.Warn("traffic notifications: failed to load subscription nodes, falling back to all active nodes",
			zap.Uint("subscription_id", sub.ID),
			zap.Error(err))

		subNodes = nil
	}

	activeSubNodeIDs := make(map[uint]struct{}, len(subNodes))
	for _, sn := range subNodes {
		if sn.Status == database.SyncStatusActive {
			activeSubNodeIDs[sn.NodeID] = struct{}{}
		}
	}

	email := XUIEmail(sub.Username, sub.TelegramID)

	var nodes []trafficNode
	for _, node := range s.activeNodes() {
		if len(activeSubNodeIDs) > 0 {
			if _, ok := activeSubNodeIDs[node.ID]; !ok {
				continue
			}
		}

		client, ok := s.xuiClients[node.ID]
		if !ok {
			continue
		}

		nodes = append(nodes, trafficNode{
			nodeID:  node.ID,
			inbound: node.ResolveInboundIDs(),
			client:  client,
			email:   email,
		})
	}

	return nodes, nil
}

// ProcessTrafficNotifications inspects a subscription's live traffic on its
// active 3x-ui nodes and, once per condition (bitmask-guarded):
//  1. warns when >= 90% of the plan's traffic limit is used,
//  2. notifies when the limit is exceeded (the panel disables the client),
//  3. re-enables a disabled client whose traffic has since been reset, and
//     notifies the user to come back.
//
// It never sends more than one message per subscription per condition and only
// touches subscriptions on plans with a non-zero traffic limit. Best-effort:
// per-node failures are logged and processing continues.
func (s *SubscriptionService) ProcessTrafficNotifications(ctx context.Context, sub *database.Subscription) error {
	if sub == nil || sub.TelegramID <= 0 || s.bot == nil {
		return nil
	}

	plan, err := s.db.GetPlanByID(ctx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("traffic notifications: load plan: %w", err)
	}
	if plan == nil || plan.TrafficLimit <= 0 {
		return nil
	}
	limit := float64(plan.TrafficLimit)

	nodes, err := s.trafficNotifyCandidates(ctx, sub)
	if err != nil {
		return err
	}

	var (
		totalUp, totalDown int64
		anyEnabled         bool
		anySuccess         bool
	)

	for _, n := range nodes {
		traffic, tErr := n.client.GetClientTraffic(ctx, n.email)
		if tErr != nil {
			logger.Debug("traffic notifications: GetClientTraffic failed on source",
				zap.Uint("node_id", n.nodeID),
				zap.Error(tErr))
			continue
		}

		anySuccess = true
		totalUp += traffic.Up
		totalDown += traffic.Down
		if traffic.Enable {
			anyEnabled = true
		}
	}

	if !anySuccess {
		return nil
	}

	used := float64(totalUp + totalDown)

	switch {
	case used >= limit:
		// Quota exceeded: the panel disables the client. Warn once and do NOT
		// re-enable (the user should buy Premium).
		return s.notifyExhausted(ctx, sub)
	case !anyEnabled:
		// Traffic counter was reset (used < limit) but the panel left the client
		// disabled. Re-enable it and invite the user back.
		return s.reenableAndNotify(ctx, sub, nodes)
	case used >= trafficQuotaWarningFraction*limit:
		// Still enabled and approaching the cap: warm the user once.
		return s.notifyNinety(ctx, sub)
	default:
		// Below 90%: release the 90% bit so it can fire again if use climbs back.
		return s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, database.TrafficBit90)
	}
}

// reenableAndNotify re-enables disabled clients whose traffic has been reset and
// sends a single "come back" notification.
func (s *SubscriptionService) reenableAndNotify(ctx context.Context, sub *database.Subscription, nodes []trafficNode) error {
	reenabled := false

	for _, n := range nodes {
		traffic, tErr := n.client.GetClientTraffic(ctx, n.email)
		if tErr != nil {
			logger.Debug("traffic notifications: re-enable snapshot failed", zap.Uint("node_id", n.nodeID), zap.Error(tErr))
			continue
		}
		if traffic.Enable {
			continue
		}

		uErr := n.client.UpdateClient(ctx, xui.ClientRequest{
			InboundIDs:   n.inbound,
			Email:        n.email,
			CurrentEmail: n.email,
			ClientID:     traffic.UUID,
			SubID:        traffic.SubID,
			TrafficBytes: traffic.Total,
			ExpiryTime:   time.UnixMilli(traffic.ExpiresAt),
			ResetDays:    traffic.Reset,
			TgID:         sub.TelegramID,
			Enable:       boolPtr(true),
		})
		if uErr != nil {
			logger.Warn("traffic notifications: failed to re-enable client",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID),
				zap.Error(uErr))
			continue
		}

		logger.Info("traffic client re-enabled",
			zap.Uint("subscription_id", sub.ID),
			zap.Uint("node_id", n.nodeID))
		reenabled = true
	}

	if !reenabled {
		return nil
	}

	// Forget the "exhausted" and "90%" bits so future cycles can fire again.
	if rErr := s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, database.TrafficBitExhausted); rErr != nil {
		logger.Warn("traffic notifications: failed to release exhausted bit", zap.Uint("subscription_id", sub.ID), zap.Error(rErr))
	}
	if rErr := s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, database.TrafficBit90); rErr != nil {
		logger.Warn("traffic notifications: failed to release 90 bit", zap.Uint("subscription_id", sub.ID), zap.Error(rErr))
	}

	// The user's quota was exhausted and reset; we re-enabled the client. Invite
	// them back — without pushing a purchase (unlike the warning/exhausted cases).
	return s.sendOnce(ctx, sub, database.TrafficBitReset, "reset", func() (string, tgbotapi.InlineKeyboardMarkup, error) {
		text := utils.EscapeMarkdownV2(
			"✅ *Твой трафик сброшен — ты снова в сети!*\n\n" +
				"Возвращайся и продолжай пользоваться.\n\n🔗 " +
				s.cfg.SubURL(sub.SubscriptionID))
		// Empty keyboard: the reset message is a simple "come back", not a sales CTA.
		return text, tgbotapi.InlineKeyboardMarkup{}, nil
	})
}

// notifyExhausted sends the "quota exceeded" notification at most once.
func (s *SubscriptionService) notifyExhausted(ctx context.Context, sub *database.Subscription) error {
	return s.sendOnce(ctx, sub, database.TrafficBitExhausted, "exhausted", func() (string, tgbotapi.InlineKeyboardMarkup, error) {
		text := utils.EscapeMarkdownV2(
			"🚫 *Доступ приостановлен*\n\n" +
				"Ты использовал весь бесплатный лимит (100%%), и твой VPN отключён.\n\n" +
				"Возобнови подключение на Premium — там безлимит, больше серверов и никаких ограничений.\n\n" +
				"Выбрать тариф 👇")
		return text, premiumKeyboard(), nil
	})
}

// notifyNinety sends the "almost exhausted" warning at most once.
func (s *SubscriptionService) notifyNinety(ctx context.Context, sub *database.Subscription) error {
	return s.sendOnce(ctx, sub, database.TrafficBit90, "ninety", func() (string, tgbotapi.InlineKeyboardMarkup, error) {
		text := utils.EscapeMarkdownV2(
			"⚠️ *Осталось меньше 10%% трафика*\n\n" +
				"Ты использовал почти весь бесплатный лимит (90%%).\n\n" +
				"Не прерывай VPN — на Premium безлимит, больше серверов и никаких ограничений.\n\n" +
				"Выбрать тариф 👇")
		return text, premiumKeyboard(), nil
	})
}

// sendOnce claims the traffic-notification bit; if already sent, it is a no-op.
// On send failure the bit is released so the next cycle retries.
func (s *SubscriptionService) sendOnce(ctx context.Context, sub *database.Subscription, bit int, kind string, textFn func() (string, tgbotapi.InlineKeyboardMarkup, error)) error {
	claimed, err := s.trafficRepo.ClaimTrafficReminder(ctx, sub.ID, bit)
	if err != nil {
		metrics.TrafficNotificationsTotal.WithLabelValues(kind, "error").Inc()
		return fmt.Errorf("claim traffic reminder: %w", err)
	}
	if !claimed {
		return nil
	}

	text, kb, err := textFn()
	if err != nil {
		if rErr := s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, bit); rErr != nil {
			err = errors.Join(err, rErr)
		}
		metrics.TrafficNotificationsTotal.WithLabelValues(kind, "error").Inc()
		return fmt.Errorf("build traffic notification (%s): %w", kind, err)
	}

	msg := tgbotapi.NewMessage(sub.TelegramID, text)
	if len(kb.InlineKeyboard) > 0 {
		msg.ReplyMarkup = &kb
	}
	msg.ParseMode = tgbotapi.ModeMarkdownV2

	_, err = s.bot.Send(msg)
	if err != nil {
		relErr := s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, bit)
		if relErr != nil {
			err = errors.Join(err, relErr)
		}
		metrics.TrafficNotificationsTotal.WithLabelValues(kind, "error").Inc()
		return fmt.Errorf("send traffic notification (%s): %w", kind, err)
	}

	metrics.TrafficNotificationsTotal.WithLabelValues(kind, "success").Inc()

	logger.Info("traffic notification sent",
		zap.String("kind", kind),
		zap.Uint("subscription_id", sub.ID),
		zap.Int64("telegram_id", sub.TelegramID))

	return nil
}

// premiumKeyboard returns the inline keyboard that opens the paid-tier picker.
// The single "buy_premium_list" callback is a ready-made, static entry: tapping
// it opens the Premium menu where the user chooses a duration (30/60 days and
// a price) and pays. No product/price data has to be pulled into this service.
func premiumKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💎 Перейти на Premium", "buy_premium_list"),
		),
	)
}

func boolPtr(v bool) *bool { return &v }