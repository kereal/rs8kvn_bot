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

// trafficQuotaWarningDenominator defines the
// fixed 90% threshold without floating-point rounding at the boundary.
const (
	trafficQuotaWarningDenominator int64 = 10
	maxInt64                             = int64(^uint64(0) >> 1)
)

// trafficSnapshot carries one node's live traffic reading.
type trafficSnapshot struct {
	node    readerTarget
	traffic xui.ClientTraffic
}

// trafficNotifyCandidates returns the readable panel nodes a subscription is
// actually provisioned on. Mirrors GetWithTraffic: only nodes with an active
// binding are polled so other plans' nodes and pending_* states are excluded.
func (s *SubscriptionService) trafficNotifyCandidates(ctx context.Context, sub *database.Subscription) ([]readerTarget, error) {
	activeSubNodeIDs, err := s.activeBindingNodeIDs(ctx, sub.ID)
	if err != nil {
		return nil, fmt.Errorf("traffic notifications: load subscription nodes: %w", err)
	}

	return s.activeReaders(activeSubNodeIDs, XUIEmail(sub.Username, sub.TelegramID)), nil
}

// ProcessTrafficNotifications inspects live traffic, sends quota notifications,
// and re-enables clients whose exhausted quota has been reset.
func (s *SubscriptionService) ProcessTrafficNotifications(ctx context.Context, sub *database.Subscription) error {
	if sub == nil || sub.TelegramID <= 0 || s.bot == nil {
		return nil
	}

	return s.processTraffic(ctx, sub, true)
}

// ProcessTrafficReset inspects live traffic and re-enables clients whose
// exhausted quota has been reset, without sending user notifications. It is
// used when notification workers are disabled so automatic access recovery
// remains independent from Telegram messaging.
func (s *SubscriptionService) ProcessTrafficReset(ctx context.Context, sub *database.Subscription) error {
	if sub == nil || sub.TelegramID <= 0 {
		return nil
	}

	return s.processTraffic(ctx, sub, false)
}

func (s *SubscriptionService) processTraffic(ctx context.Context, sub *database.Subscription, sendNotifications bool) error {
	plan, err := s.db.GetPlanByID(ctx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("traffic notifications: load plan: %w", err)
	}
	if plan == nil || plan.TrafficLimit <= 0 {
		return nil
	}
	limit := plan.TrafficLimit

	nodes, err := s.trafficNotifyCandidates(ctx, sub)
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		return nil
	}

	partial := false
	snapshots := make([]trafficSnapshot, 0, len(nodes))
	for _, n := range nodes {
		traffic, tErr := n.reader.GetClientTraffic(ctx, n.email)
		if tErr != nil {
			// A client absent on one node is an expected panel state, not a
			// node failure: skip that node and keep the pass alive on the rest.
			if errors.Is(tErr, xui.ErrClientNotFound) {
				logger.Info("traffic notifications: client not found on node; skipping node",
					zap.Uint("subscription_id", sub.ID),
					zap.Uint("node_id", n.nodeID),
					zap.String("email", n.email))
				continue
			}
			partial = true
			logger.Warn("traffic notifications: GetClientTraffic failed on node; skipping node",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID),
				zap.Error(tErr))
			continue
		}
		if traffic == nil {
			partial = true
			logger.Warn("traffic notifications: source returned empty traffic snapshot; skipping node",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID))
			continue
		}
		if traffic.Up < 0 || traffic.Down < 0 {
			partial = true
			logger.Warn("traffic notifications: source returned negative traffic; skipping node",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID),
				zap.Int64("up", traffic.Up),
				zap.Int64("down", traffic.Down))
			continue
		}
		if traffic.Up > maxInt64-traffic.Down {
			partial = true
			logger.Warn("traffic notifications: source traffic overflows int64; skipping node",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID))
			continue
		}
		snapshots = append(snapshots, trafficSnapshot{node: n, traffic: *traffic})
	}

	// Decide on successful snapshots only. If no node answered, skip this
	// subscription pass (the worker continues with the next subscription)
	// instead of failing the whole traffic-control loop.
	if len(snapshots) == 0 {
		logger.Warn("traffic notifications: no readable node snapshots; skipping subscription pass",
			zap.Uint("subscription_id", sub.ID))
		return nil
	}

	var used int64
	var anyEnabled bool
	for _, snapshot := range snapshots {
		if used > maxInt64-(snapshot.traffic.Up+snapshot.traffic.Down) {
			logger.Warn("traffic notifications: aggregated traffic overflows int64",
				zap.Uint("subscription_id", sub.ID))
			return nil
		}
		used += snapshot.traffic.Up + snapshot.traffic.Down
		anyEnabled = anyEnabled || snapshot.traffic.Enable
	}

	// ceil(90% of limit), computed without multiplying potentially large values.
	warningReached := used >= limit-limit/trafficQuotaWarningDenominator
	if partial && used < limit {
		if sendNotifications && warningReached {
			return s.notifyNinety(ctx, sub)
		}
		return nil
	}

	switch {
	case used >= limit:
		// Quota exceeded: the panel disables the client. Warn once and do NOT
		// re-enable (the user should buy Premium).
		if !sendNotifications {
			// Recovery mode: still mark the exhaustion via the existing claim
			// mechanism (no message is sent), so the post-reset pass can
			// recognize this subscription as previously exhausted and
			// re-enable it once the counter resets.
			_, cErr := s.trafficRepo.ClaimTrafficReminder(ctx, sub.ID, database.TrafficBitExhausted)
			if cErr != nil {
				logger.Warn("traffic notifications: failed to record exhausted state in recovery mode",
					zap.Uint("subscription_id", sub.ID),
					zap.Error(cErr))
			}
			return nil
		}
		return s.notifyExhausted(ctx, sub)
	case !anyEnabled:
		// Only a previously exhausted subscription may be re-enabled after reset.
		// This prevents the recovery pass from overriding a deliberate manual
		// disablement of an otherwise healthy client.
		if sub.TrafficRemindersSent&database.TrafficBitExhausted == 0 {
			return nil
		}
		// Traffic counter was reset (used < limit) but the panel left the client
		// disabled. Re-enable it before optionally notifying the user.
		if !s.reenableResetClients(ctx, sub, snapshots) {
			return nil
		}

		// Forget the exhausted and 90% bits so future cycles can fire again.
		rErr := s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, database.TrafficBitExhausted)
		if rErr != nil {
			logger.Warn("traffic notifications: failed to release exhausted bit", zap.Uint("subscription_id", sub.ID), zap.Error(rErr))
		}
		rErr = s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, database.TrafficBit90)
		if rErr != nil {
			logger.Warn("traffic notifications: failed to release 90 bit", zap.Uint("subscription_id", sub.ID), zap.Error(rErr))
		}

		if !sendNotifications {
			return nil
		}
		return s.sendOnce(ctx, sub, database.TrafficBitReset, "reset", func() (string, tgbotapi.InlineKeyboardMarkup, error) {
			text := utils.EscapeMarkdownV2(
				"✅ *Твой трафик сброшен — ты снова в сети!*\n\n" +
					"Возвращайся и продолжай пользоваться.\n\n🔗 " +
					s.cfg.SubURL(sub.SubscriptionID))
			// Empty keyboard: the reset message is a simple "come back", not a sales CTA.
			return text, tgbotapi.InlineKeyboardMarkup{}, nil
		})

	case sendNotifications && warningReached:
		// Still enabled and approaching the cap: warm the user once.
		return s.notifyNinety(ctx, sub)
	case sendNotifications:
		// Below 90%: release the 90% bit so it can fire again if use climbs back.
		return s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, database.TrafficBit90)
	default:
		return nil
	}
}

// reenableResetClients re-enables disabled clients whose traffic has been reset.
// It returns true when at least one panel update succeeds.
func (s *SubscriptionService) reenableResetClients(ctx context.Context, sub *database.Subscription, snapshots []trafficSnapshot) bool {
	reenabled := false

	for _, snapshot := range snapshots {
		n := snapshot.node
		traffic := snapshot.traffic
		if traffic.Enable {
			continue
		}
		// Re-enabling is a panel mutation: the read slot must also carry the
		// writer capability (the 3x-ui client does; a read-only adapter would
		// be skipped rather than fail).
		writer, canWrite := n.reader.(interfaces.XUIClientWriter)
		if !canWrite {
			logger.Warn("traffic notifications: node reader does not support panel writes",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID))
			continue
		}
		if traffic.UUID == "" || traffic.ExpiresAt < 0 || traffic.ExpiresAt > 0 && time.UnixMilli(traffic.ExpiresAt).Before(time.Now()) {
			logger.Warn("traffic notifications: refusing to re-enable invalid or expired client",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID))
			continue
		}

		// The panel's traffic endpoint does not return the auto-renew profile
		// (resetDay/resetMax/trafficReset/trafficResetDay) — it lives in the
		// client card from GET /panel/api/clients/get/{email}. Without it,
		// UpdateClient would overwrite the profile with "never" and the
		// monthly traffic reset would stop firing. Read it back best-effort
		// before re-enabling; inbound IDs come from the same response.
		profile := &xui.ClientConfig{}
		record, pErr := n.reader.GetClientByEmail(ctx, n.email)
		switch {
		case errors.Is(pErr, xui.ErrClientNotFound):
			// Client fully removed on the panel — keep empty profile.
		case pErr != nil:
			logger.Warn("traffic notifications: failed to load client auto-renew profile; skipping update to retry later",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID),
				zap.String("email", n.email),
				zap.Error(pErr))
			continue
		case record == nil:
			logger.Warn("traffic notifications: client record was nil without error; skipping update to retry later",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", n.nodeID),
				zap.String("email", n.email))
			continue
		default:
			profile = &record.Client
		}

		// Effective inbound set is unchanged from the previous behaviour:
		// the update goes out with the node's configured inbound list. When
		// the panel reports inboundIds, prefer them; when the panel returns
		// none, fall back to n.inbound instead of sending an empty list
		// silently — with both empty UpdateClient rejects the request and
		// the node is skipped with a Warn, as before.
		inboundIDs := n.inbound
		if record != nil && len(record.InboundIDs) > 0 {
			inboundIDs = record.InboundIDs
		}

		uErr := writer.UpdateClient(ctx, xui.ClientRequest{
			InboundIDs:      inboundIDs,
			Email:           n.email,
			CurrentEmail:    n.email,
			ClientID:        traffic.UUID,
			SubID:           traffic.SubID,
			TrafficBytes:    traffic.Total,
			ExpiryTime:      time.UnixMilli(traffic.ExpiresAt),
			ResetDays:       traffic.Reset,
			ResetDay:        profile.ResetDay,
			ResetMax:        profile.ResetMax,
			TrafficReset:    profile.TrafficReset,
			TrafficResetDay: profile.TrafficResetDay,
			TgID:            sub.TelegramID,
			Enable:          new(true),
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

	return reenabled
}

// notifyExhausted sends the "quota exceeded" notification at most once.
func (s *SubscriptionService) notifyExhausted(ctx context.Context, sub *database.Subscription) error {
	return s.sendOnce(ctx, sub, database.TrafficBitExhausted, "exhausted", func() (string, tgbotapi.InlineKeyboardMarkup, error) {
		text := utils.EscapeMarkdownV2(
			"🚫 *Доступ приостановлен*\n\n" +
				"Ты использовал весь бесплатный лимит (100%), и твой VPN отключён.\n\n" +
				"Возобнови подключение на Premium — там безлимит, больше серверов и никаких ограничений.\n\n" +
				"Выбрать тариф 👇")
		return text, premiumKeyboard(), nil
	})
}

// notifyNinety sends the "almost exhausted" warning at most once.
func (s *SubscriptionService) notifyNinety(ctx context.Context, sub *database.Subscription) error {
	return s.sendOnce(ctx, sub, database.TrafficBit90, "ninety", func() (string, tgbotapi.InlineKeyboardMarkup, error) {
		text := utils.EscapeMarkdownV2(
			"⚠️ *Осталось меньше 10% трафика*\n\n" +
				"Ты использовал почти весь бесплатный лимит (90%).\n\n" +
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
		rErr := s.trafficRepo.ReleaseTrafficReminder(ctx, sub.ID, bit)
		if rErr != nil {
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

	// Аудит уведомления best-effort.
	auditErr := s.db.InsertAuditLog(ctx, database.AuditTrafficNotified(sub, kind))
	if auditErr != nil {
		logger.Warn("audit log write failed",
			zap.String("event", database.AuditEventTrafficNotified),
			zap.Uint("subscription_id", sub.ID),
			zap.Error(auditErr))
	}

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
