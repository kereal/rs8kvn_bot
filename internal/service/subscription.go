// Package service contains subscription, payment, and synchronization business logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SubscriptionService coordinates subscription persistence, VPN provisioning,
// cache invalidation, metrics, and referral/trial lifecycle operations.
type SubscriptionService struct {
	db                interfaces.DatabaseService
	reminderRepo      interfaces.SubscriptionReminderRepository
	xuiClients        map[uint]interfaces.XUIClient
	vpnClients        map[uint]vpn.Client
	nodes             []database.Node
	cfg               *config.Config
	invalidate        func(telegramID int64)
	invalidateBySubID func(subID string)
	syncService       *SyncService
	bot               interfaces.BotAPI
}

// CreateResult contains the subscription and referral information produced by
// creating or recovering a user subscription.
type CreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
	ReferrerTGID    int64
}

// XUIEmail returns an email suitable for use as XUI client email.
func XUIEmail(username string, telegramID int64) string {
	if utils.IsRealUsername(username) {
		return username
	}

	return fmt.Sprintf("tgId_%d", telegramID)
}

// NewSubscriptionService creates a SubscriptionService configured with the given
// database, XUI/VPN clients, nodes, and application configuration.
func NewSubscriptionService(db interfaces.DatabaseService, xuiClients map[uint]interfaces.XUIClient, vpnClients map[uint]vpn.Client, nodes []database.Node, cfg *config.Config) *SubscriptionService {
	return &SubscriptionService{
		db:           db,
		reminderRepo: db,
		xuiClients:   xuiClients,
		vpnClients:   vpnClients,
		nodes:        nodes,
		cfg:          cfg,
	}
}

// SetBot links the Telegram bot client to the subscription service.
func (s *SubscriptionService) SetBot(bot interfaces.BotAPI) {
	s.bot = bot
}

// SetSyncService links the subscription service to the sync module.
func (s *SubscriptionService) SetSyncService(svc *SyncService) {
	s.syncService = svc
}

// activeNodes returns nodes that are active and have a host configured.
func (s *SubscriptionService) activeNodes() []database.Node {
	var result []database.Node

	for _, node := range s.nodes {
		if node.IsActive && node.Host != "" {
			result = append(result, node)
		}
	}

	return result
}

// trialNodes returns nodes linked to the trial plan.
// Returns an error if the trial plan has no linked nodes (fail-fast).
func (s *SubscriptionService) trialNodes(ctx context.Context) ([]database.Node, error) {
	nodes, err := s.db.GetNodesByPlanName(ctx, database.TrialPlanName)
	if err != nil {
		return nil, fmt.Errorf("load trial nodes: %w", err)
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("trial plan has no linked nodes")
	}

	return nodes, nil
}

// Create provisions a new free-plan subscription. inviteCode, when non-empty,
// is resolved atomically inside the DB transaction and persisted in
// sub.InviteCode / sub.ReferredBy. The resolved ReferrerTGID (nil if unset) is
// returned in CreateResult so callers can update aggregate referral state.
// VPN node access is provisioned asynchronously via the sync module.
func (s *SubscriptionService) Create(ctx context.Context, telegramID int64, username, inviteCode string) (*CreateResult, error) {
	username = XUIEmail(username, telegramID)

	existing, err := s.db.GetByTelegramID(ctx, telegramID)
	if err == nil {
		err = s.ensureSubscriptionNodes(ctx, existing)
		if err != nil {
			return nil, fmt.Errorf("ensure subscription nodes: %w", err)
		}

		referrerID := int64(0)
		if existing.ReferredBy != nil {
			referrerID = *existing.ReferredBy
		}

		return &CreateResult{
			Subscription:    existing,
			SubscriptionURL: s.cfg.SubURL(existing.SubscriptionID),
			ReferrerTGID:    referrerID,
		}, nil
	}

	if !errors.Is(err, database.ErrSubscriptionNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup subscription: %w", err)
	}

	// No active subscription. If a non-active one exists (e.g. left "revoked"
	// after a partially-failed delete), reanimate it instead of inserting a
	// duplicate row that would violate telegram_id uniqueness.
	existingAny, anyErr := s.db.GetAnyByTelegramID(ctx, telegramID)
	if anyErr == nil {
		reanimated, reErr := s.reanimateRevokedSubscription(ctx, existingAny, inviteCode)
		if reErr != nil {
			return nil, fmt.Errorf("reanimate subscription: %w", reErr)
		}

		referrerID := int64(0)
		if reanimated.ReferredBy != nil {
			referrerID = *reanimated.ReferredBy
		}

		return &CreateResult{
			Subscription:    reanimated,
			SubscriptionURL: s.cfg.SubURL(reanimated.SubscriptionID),
			ReferrerTGID:    referrerID,
		}, nil
	}

	if !errors.Is(anyErr, database.ErrSubscriptionNotFound) && !errors.Is(anyErr, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup subscription (any status): %w", anyErr)
	}

	plan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve free plan: %w", err)
	}

	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}

	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

	sub := &database.Subscription{
		TelegramID:     telegramID,
		Username:       username,
		ClientID:       clientID,
		SubscriptionID: subID,
		PlanID:         plan.ID,
		Status:         string(database.SubscriptionStatusActive),
	}

	err = s.db.CreateSubscription(ctx, sub, inviteCode)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	err = s.ensureSubscriptionNodes(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("ensure subscription nodes: %w", err)
	}

	referrerID := int64(0)
	if sub.ReferredBy != nil {
		referrerID = *sub.ReferredBy
	}

	subscriptionURL := s.cfg.SubURL(subID)
	result := &CreateResult{
		Subscription:    sub,
		SubscriptionURL: subscriptionURL,
		ReferrerTGID:    referrerID,
	}

	metrics.SubscriptionCreatesTotal.Inc()

	s.RefreshActiveSubscriptionsMetric(ctx)

	return result, nil
}

// reanimateRevokedSubscription recovers a subscription left in a non-active state
// (e.g. "revoked") after a partially-failed delete, turning it back into an
// active free-plan subscription. This avoids creating a second row for the same
// telegram_id (which would violate the telegram_id uniqueness expectation and
// could otherwise permanently block the user from re-subscribing).
//
// The existing subscription_nodes are wiped so ensureSubscriptionNodes can rebuild
// them as pending_add — leftovers from the failed delete (pending_remove) would
// otherwise make the next sync attempt to deprovision instead of re-provision.
func (s *SubscriptionService) reanimateRevokedSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) (*database.Subscription, error) {
	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("resolve free plan: %w", err)
	}

	if inviteCode != "" {
		inv, err := s.db.GetInviteByCode(ctx, inviteCode)
		if err == nil {
			inviteVal := inviteCode
			sub.InviteCode = &inviteVal
			referredBy := inv.ReferrerTGID
			sub.ReferredBy = &referredBy
		} else if !errors.Is(err, database.ErrInviteNotFound) {
			return nil, fmt.Errorf("resolve invite: %w", err)
		}
	} else {
		sub.InviteCode = nil
		sub.ReferredBy = nil
	}

	// Keep the row revoked until stale node bindings have been cleared
	// successfully; otherwise an active subscription could be returned without
	// a working VPN binding.
	sub.PlanID = freePlan.ID
	sub.Status = string(database.SubscriptionStatusActive)
	sub.ExpiresAt = nil
	sub.ProductID = nil
	sub.StartedAt = nil
	sub.PricePaidCents = 0
	sub.Currency = nil
	sub.Devices = "[]"
	sub.Ips = "[]"

	// This is a DB-setup prerequisite of the user-initiated operation. Do not
	// continue with an active subscription when cleanup fails.
	err = s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, sub.ID)
	if err != nil {
		return nil, fmt.Errorf("clear subscription nodes during reanimation: %w", err)
	}

	err = s.db.UpdateSubscription(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("reanimate subscription: %w", err)
	}

	err = s.ensureSubscriptionNodes(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("ensure subscription nodes: %w", err)
	}

	metrics.SubscriptionCreatesTotal.Inc()

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// DowngradeToFreePlan resets a subscription to the free plan and deprovisions
// premium VPN access. Used when a paid order is chargebacked: the user loses
// premium access but retains the free tier. Returns the updated subscription.
//
// With the sync service wired, premium node bindings are first transitioned to
// pending_remove and free-plan bindings to pending_add via ApplyPlanToSubscription,
// then SyncSubscription physically deletes the VPN clients from the premium
// panels. Without the sync service (tests / before wiring) the node bindings are
// rebuilt in place and the background worker reconciles panels later.
func (s *SubscriptionService) DowngradeToFreePlan(ctx context.Context, sub *database.Subscription) (*database.Subscription, error) {
	if sub == nil {
		return nil, errors.New("downgrade: subscription is nil")
	}

	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("downgrade: resolve free plan: %w", err)
	}

	// Reset subscription to free-plan defaults.
	sub.PlanID = freePlan.ID
	sub.Status = string(database.SubscriptionStatusActive)
	sub.ExpiresAt = nil
	sub.ProductID = nil
	sub.StartedAt = nil
	sub.PricePaidCents = 0
	sub.Currency = nil

	err = s.db.UpdateSubscription(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("downgrade: update subscription: %w", err)
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	if s.syncService != nil {
		// The subscription now points at the free plan, so ApplyPlanToSubscription
		// reconciles the bindings against it: premium nodes become pending_remove
		// and missing free nodes pending_add. SyncSubscription then physically
		// removes the premium clients from the panels (best-effort; the background
		// worker retries any failed removals).
		err = s.syncService.ApplyPlanToSubscription(ctx, sub.ID)
		if err != nil {
			return nil, fmt.Errorf("downgrade: apply plan: %w", err)
		}

		err = s.syncService.SyncSubscription(ctx, sub.ID)
		if err != nil {
			logger.Warn("downgrade: sync subscription failed (will retry)",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(err))
		}

		s.RefreshActiveSubscriptionsMetric(ctx)

		return sub, nil
	}

	// Fallback without the sync service (tests / before wiring): rebuild the
	// node bindings; physical panel cleanup is left to the background workers.
	err = s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, sub.ID)
	if err != nil {
		logger.Warn("downgrade: failed to clear subscription nodes",
			zap.Uint("subscription_id", sub.ID),
			zap.Error(err))
	}

	err = s.ensureSubscriptionNodes(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("downgrade: ensure subscription nodes: %w", err)
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// revokeAndDeprovisionThenDelete runs the two-phase subscription teardown used by
// DeleteByID: mark revoked → deprovision VPN access (best-effort; background
// sync reconciles on failure) → physically delete the DB row + subscription nodes →
// invalidate the cache. The resolved sub is the single input, so the lifecycle contract
// lives in exactly one place. Returns the deleted subscription (nil on error).
func (s *SubscriptionService) revokeAndDeprovisionThenDelete(ctx context.Context, sub *database.Subscription) (*database.Subscription, error) {
	// Phase 1: mark revoked before any external effect.
	sub.Status = string(database.SubscriptionStatusRevoked)

	err := s.db.UpdateSubscription(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("mark revoked: %w", err)
	}

	// Phase 2: deprovision VPN access (best-effort; background sync reconciles on failure).
	if s.syncService != nil {
		err = s.syncService.MarkAllForRemoval(ctx, sub.ID)
		if err != nil {
			return nil, fmt.Errorf("deprovision mark failed: %w", err)
		}

		err = s.syncService.SyncSubscription(ctx, sub.ID)
		if err != nil {
			logger.Warn("deprovision sync failed; subscription remains revoked, background sync will retry",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(err))
		}
	}

	// SyncSubscription intentionally treats external node failures as best-effort
	// and may return nil after scheduling a retry. Inspect the durable DB state
	// before deleting the subscription: pending bindings are the retry queue.
	nodes, nodesErr := s.db.GetBySubscriptionID(ctx, sub.ID)
	if nodesErr != nil {
		return nil, fmt.Errorf("deprovision: verify node bindings: %w", nodesErr)
	}
	if len(nodes) > 0 {
		return nil, fmt.Errorf("deprovision incomplete: %d node bindings remain", len(nodes))
	}

	// Phase 3: remove the now-empty binding set before the parent row. This keeps
	// the operation valid even when SQLite foreign-key enforcement is enabled and
	// prevents a failed cleanup from destroying the retry queue.
	err = s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, sub.ID)
	if err != nil {
		return nil, fmt.Errorf("delete subscription nodes: %w", err)
	}

	_, err = s.db.DeleteSubscriptionByID(ctx, sub.ID)
	if err != nil {
		return nil, fmt.Errorf("db delete: %w", err)
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.InvalidateBySubID(ctx, sub.SubscriptionID)
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// DeleteByID is the ONLY real (hard) subscription deletion in the product. It is
// used by the admin /del command. Two-phase teardown is owned by
// revokeAndDeprovisionThenDelete. Everything else only changes subscription status.
func (s *SubscriptionService) DeleteByID(ctx context.Context, id uint) (*database.Subscription, error) {
	sub, err := s.db.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}

	return s.revokeAndDeprovisionThenDelete(ctx, sub)
}

// adminSetPlanDefaultDays is the default expiry window (in days) applied when an
// admin changes a plan without an explicit duration and the subscription has no
// future expiry to preserve.
const adminSetPlanDefaultDays = 30

// AdminSetPlanMaxDays is the upper bound for the explicit duration in /setplan.
// It guards against typos (e.g. an extra zero) silently extending a
// subscription for centuries.
const AdminSetPlanMaxDays = 3650

// AdminSetPlan changes a subscription's plan through the service layer
// (admin override, no payment involved). It mirrors the payment activation
// flow: update the subscription row (plan, status, expiry, reminder state),
// materialize DB sync prerequisites via ApplyPlanToSubscription
// (pending_add/pending_update/pending_remove), then best-effort
// SyncSubscription to push changes to VPN panels. Cache is invalidated and
// metrics refreshed. Returns the updated subscription.
//
// days, when > 0, sets ExpiresAt = now + days. When 0, an existing future
// expiry is preserved; otherwise the subscription is given a fresh
// adminSetPlanDefaultDays window. Setting the free plan clears ExpiresAt
// (бессрочная), mirroring DowngradeToFreePlan.
func (s *SubscriptionService) AdminSetPlan(ctx context.Context, subscriptionID, planID uint, days int) (*database.Subscription, error) {
	if days > AdminSetPlanMaxDays {
		return nil, fmt.Errorf("admin set plan: days %d exceeds maximum %d", days, AdminSetPlanMaxDays)
	}

	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("admin set plan: load subscription: %w", err)
	}

	plan, err := s.db.GetPlanByID(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("admin set plan: load plan: %w", err)
	}

	now := time.Now().UTC()
	if plan.Name == database.FreePlanName {
		// Free plan: clear expiry and paid state, mirroring DowngradeToFreePlan.
		// Leaving ProductID/PricePaidCents behind would keep the subscription
		// looking "paid" (renewal vs first-purchase detection in the payment
		// flow) while it is on the free tier.
		sub.ExpiresAt = nil
		sub.ProductID = nil
		sub.StartedAt = nil
		sub.PricePaidCents = 0
		sub.Currency = nil
	} else {
		switch {
		case days > 0:
			expiry := now.AddDate(0, 0, days)
			sub.ExpiresAt = &expiry
		case sub.ExpiresAt == nil || !sub.ExpiresAt.After(now):
			expiry := now.AddDate(0, 0, adminSetPlanDefaultDays)
			sub.ExpiresAt = &expiry
		}

		if sub.StartedAt == nil {
			started := now
			sub.StartedAt = &started
		}
	}

	sub.PlanID = plan.ID
	sub.Status = string(database.SubscriptionStatusActive)

	// Reset the reminder bitmask so the expiry-reminder cycle restarts for the
	// new expiry (mirrors ConfirmOrderPaidCAS).
	sub.RemindersSent = 0

	err = s.db.UpdateSubscription(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("admin set plan: update subscription: %w", err)
	}

	// DB-setup phase: structural prerequisites for the background worker. These
	// must fail loudly — the row is already committed and ApplyPlanToSubscription
	// is idempotent, so a retry converges.
	if s.syncService != nil {
		err = s.syncService.ApplyPlanToSubscription(ctx, sub.ID)
		if err != nil {
			return nil, fmt.Errorf("admin set plan: apply plan: %w", err)
		}

		// External-sync phase is best-effort: SyncPendingNodes retries on failure.
		if err = s.syncService.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("admin set plan: external sync failed; background will retry",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("plan_id", plan.ID),
				zap.Error(err))
		}
	}

	if s.invalidate != nil && sub.TelegramID > 0 {
		s.invalidate(sub.TelegramID)
	}
	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// deleteClientFromAllNodes removes the VPN subscription from all active nodes.
// Uses vpnClients (supports 3x-ui and proxman) — the legacy xuiClients map
// covers only 3x-ui nodes and must not be used here.
func (s *SubscriptionService) deleteClientFromAllNodes(ctx context.Context, provision vpn.SubscriptionProvision) {
	for _, node := range s.nodes {
		if !node.IsActive {
			continue
		}

		client, ok := s.vpnClients[node.ID]
		if !ok {
			continue
		}

		err := client.DeleteSubscription(ctx, provision)
		if err != nil {
			logger.Warn("failed to delete VPN subscription on node",
				zap.String("username", provision.Username),
				zap.Uint("node_id", node.ID),
				zap.Error(err))
		}
	}
}

// TrialCreateResult holds the outcome of a trial creation, including the
// generated identifiers and public subscription URL.
type TrialCreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
	SubID           string
	ClientID        string
}

// CreateTrial provisions a new anonymous trial subscription.
// It resolves the trial plan, picks the first trial node,
// creates a client on that node via XUI, and persists the subscription
// in the database with a negative telegram_id (unactivated).
func (s *SubscriptionService) CreateTrial(ctx context.Context, inviteCode string) (*TrialCreateResult, error) {
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}

	trialPlan, err := s.db.GetPlanByName(ctx, database.TrialPlanName)
	if err != nil {
		return nil, fmt.Errorf("resolve trial plan: %w", err)
	}

	trafficBytes := trialPlan.TrafficLimit
	expiryTime := time.Now().UTC().Add(time.Duration(s.cfg.TrialDurationHours) * time.Hour)
	email := "trial_" + subID
	// Trials must not auto-renew. resetDays=0 disables the 3x-ui auto-renew,
	// which (reset>0 + expiryTime>0) would otherwise reset traffic and extend
	// expiry by SubscriptionResetDay every cycle, making the short trial expire.
	resetDays := 0

	trialNodes, err := s.trialNodes(ctx)
	if err != nil {
		return nil, err
	}

	node := trialNodes[0]

	client, ok := s.vpnClients[node.ID]
	if !ok {
		return nil, fmt.Errorf("vpn client not found for trial node %d", node.ID)
	}
	// Trial provisioned on a 3x-ui-style node (email == client id). For
	// proxman/fetch nodes the CreateSubscription is a no-op, so the trial
	// still works there without a 3x-ui panel.
	provision := vpn.SubscriptionProvision{
		ClientID:     clientID,
		Username:     email,
		SubID:        subID,
		TrafficBytes: trafficBytes,
		ExpiryTime:   expiryTime,
		ResetDays:    resetDays,
	}

	err = client.CreateSubscription(ctx, provision)
	if err != nil {
		return nil, fmt.Errorf("add trial client on node %d: %w", node.ID, err)
	}

	sub, err := s.db.CreateTrialSubscription(ctx, inviteCode, subID, clientID, expiryTime)
	if err != nil {
		delErr := client.DeleteSubscription(ctx, provision)
		if delErr != nil {
			logger.Warn("failed to rollback trial client on node",
				zap.Uint("node_id", node.ID),
				zap.Error(delErr))
		}

		return nil, fmt.Errorf("create trial subscription: %w", err)
	}

	subURL := s.cfg.SubURL(subID)
	result := &TrialCreateResult{
		Subscription:    sub,
		SubscriptionURL: subURL,
		SubID:           subID,
		ClientID:        clientID,
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	return result, nil
}

// BindTrial binds a trial subscription to a Telegram user.
// It updates the trial in the database, then upgrades the client in the
// 3x-ui panel with proper traffic limits and expiry settings.
func (s *SubscriptionService) BindTrial(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	username = XUIEmail(username, telegramID)

	sub, err := s.db.BindTrialSubscription(ctx, subscriptionID, telegramID, username)
	if err != nil {
		return nil, fmt.Errorf("bind trial subscription: %w", err)
	}

	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve free plan: %w", err)
	}

	trafficBytes := freePlan.TrafficLimit
	// Trials must never auto-renew. Keep resetDays=0 even though the free
	// plan carries a traffic limit (which would otherwise enable reset=-1 → 30).
	resetDays := 0

	expiryTime := time.UnixMilli(0)

	var comment string

	if sub.InviteCode != nil {
		invite, err := s.db.GetInviteByCode(ctx, *sub.InviteCode)
		if err == nil {
			referrerSub, err := s.db.GetByTelegramID(ctx, invite.ReferrerTGID)
			if err == nil {
				comment = fmt.Sprintf("from: @%s", referrerSub.Username)
			}
		}
	}

	currentEmail := "trial_" + subscriptionID
	email := XUIEmail(username, telegramID)

	nodes, err := s.trialNodes(ctx)
	if err != nil {
		return sub, fmt.Errorf("load trial nodes: %w", err)
	}

	if len(nodes) == 0 {
		return sub, fmt.Errorf("no trial nodes configured")
	}
	// Trial is intentionally single-node (provisioned on nodes[0] by CreateTrial).
	// Only update the node where the client actually exists.
	node := nodes[0]

	client, ok := s.vpnClients[node.ID]
	if !ok {
		return sub, fmt.Errorf("vpn client not found for trial node %d", node.ID)
	}
	// Rename the anonymous trial client (email "trial_<subID>") to the bound
	// user's identity, lift traffic limits to the free plan, and bind the
	// Telegram id. proxman/fetch nodes treat this as a no-op.
	err = client.UpdateSubscription(ctx, vpn.SubscriptionProvision{
		ClientID:     sub.ClientID,
		CurrentEmail: currentEmail,
		Username:     email,
		SubID:        sub.SubscriptionID,
		TrafficBytes: trafficBytes,
		ExpiryTime:   expiryTime,
		ResetDays:    resetDays,
		TgID:         telegramID,
		Comment:      comment,
	})
	if err != nil {
		return sub, fmt.Errorf("update trial client on node %d: %w", node.ID, err)
	}

	return sub, nil
}

// RefreshActiveSubscriptionsMetric updates the active_subscriptions and trial_subscriptions gauges.
func (s *SubscriptionService) RefreshActiveSubscriptionsMetric(ctx context.Context) {
	count, err := s.db.CountActiveSubscriptions(ctx)
	if err != nil {
		logger.Warn("failed to refresh active subscriptions metric", zap.Error(err))
		return
	}

	metrics.ActiveSubscriptions.Set(float64(count))

	trialCount, err := s.db.CountTrialSubscriptions(ctx)
	if err != nil {
		logger.Warn("failed to refresh trial subscriptions metric", zap.Error(err))
		return
	}

	metrics.TrialSubscriptions.Set(float64(trialCount))
}

// SetInvalidateFunc sets the cache invalidation callback.
func (s *SubscriptionService) SetInvalidateFunc(fn func(telegramID int64)) {
	s.invalidate = fn
}

// InvalidateSubscription clears cached subscription data for the given Telegram ID.
// It is safe to call from any goroutine.
func (s *SubscriptionService) InvalidateSubscription(ctx context.Context, telegramID int64) {
	if s.invalidate != nil {
		s.invalidate(telegramID)
	}
}

// SetInvalidateBySubIDFunc sets the cache invalidation callback keyed by subscription ID.
// Used for trial and other subscriptions where TelegramID may be unavailable.
func (s *SubscriptionService) SetInvalidateBySubIDFunc(fn func(subID string)) {
	s.invalidateBySubID = fn
}

// InvalidateBySubID clears cached subscription data for the given subscription ID.
// It is safe to call from any goroutine.
func (s *SubscriptionService) InvalidateBySubID(ctx context.Context, subID string) {
	if s.invalidateBySubID != nil {
		s.invalidateBySubID(subID)
	}
}

// ReconcileOrphanedClients scans all active subscriptions and REVOKES (not
// deletes — subscriptions are never physically removed except via admin /del)
// those that are no longer provisioned on any VPN node.
//
// It uses the subscription_nodes table — the source of truth for node
// provisioning — instead of querying each node's panel directly. This works for
// every node type (3x-ui, proxman, fetch) and fixes the previous bug where
// subscriptions on proxman/fetch nodes were falsely deleted because the legacy
// xuiClients map only covers 3x-ui nodes.
//
// A subscription is orphaned when it has subscription_nodes rows but none are in
// a live state (active/pending_add/pending_update) — i.e. every binding is
// pending_remove (deprovisioning did not complete in the delete flow). Such a
// subscription would otherwise stay "active" forever while its VPN clients are
// already gone; revoking it makes the subserver return 404 and excludes it from
// broadcasts and metrics.
//
// Subscriptions without any subscription_nodes (trial subscriptions, which are
// cleaned up by their own expiry-based mechanism, or brand-new subscriptions
// still being provisioned) are left untouched to avoid races and to keep the
// trial lifecycle separate from node-level orphan cleanup.
//
// This is a best-effort background cleanup; errors are logged but do not stop the scan.
func (s *SubscriptionService) ReconcileOrphanedClients(ctx context.Context) (int, error) {
	start := time.Now()

	rows, err := s.db.GetAllSubscriptions(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch subscriptions: %w", err)
	}

	activeSubs := make([]database.Subscription, 0, len(rows))
	for _, sub := range rows {
		if sub.Status == string(database.SubscriptionStatusActive) {
			activeSubs = append(activeSubs, sub)
		}
	}

	revoked := 0

	for _, sub := range activeSubs {
		subNodes, nodeErr := s.db.GetBySubscriptionID(ctx, sub.ID)
		if nodeErr != nil {
			logger.Warn("failed to load subscription nodes for orphan reconciliation",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(nodeErr))

			continue
		}

		// No node bindings: trial subscription (cleaned up by expiry) or a
		// subscription still being provisioned. Never treat as orphan here.
		if len(subNodes) == 0 {
			continue
		}

		hasLiveNode := false

		for _, sn := range subNodes {
			if sn.Status == database.SyncStatusActive ||
				sn.Status == database.SyncStatusPendingAdd ||
				sn.Status == database.SyncStatusPendingUpdate {
				hasLiveNode = true
				break
			}
		}

		if hasLiveNode {
			continue
		}

		// Every node binding is pending_remove (or an unexpected state):
		// the subscription is fully deprovisioned but the DB row remains.
		// Policy: never delete — revoke instead (subserver then serves 404).
		sub.Status = string(database.SubscriptionStatusRevoked)

		updateErr := s.db.UpdateSubscription(ctx, &sub)
		if updateErr != nil {
			logger.Warn("failed to revoke orphaned subscription",
				zap.Error(updateErr),
				zap.Uint("id", sub.ID),
				zap.Int64("telegram_id", sub.TelegramID),
				zap.String("subscription_id", sub.SubscriptionID))
		} else {
			revoked++

			logger.Info("revoked orphaned subscription (no live node bindings)",
				zap.Uint("id", sub.ID),
				zap.Int64("telegram_id", sub.TelegramID),
				zap.String("username", sub.Username), zap.String("subscription_id", sub.SubscriptionID))

			if s.invalidate != nil && sub.TelegramID > 0 {
				s.invalidate(sub.TelegramID)
			}

			if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
				s.invalidateBySubID(sub.SubscriptionID)
			}

			metrics.OrphanedClientsRevokedTotal.Inc()
		}

		if ctx.Err() != nil {
			return revoked, ctx.Err()
		}
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	metrics.ReconcileOrphanedDuration.Observe(time.Since(start).Seconds())

	return revoked, nil
}

// CleanupExpiredTrials deletes expired trial subscriptions from the database
// and deprovisions their VPN clients.
//
// The DB cleanup (database.CleanupExpiredTrials) removes rows with
// `RETURNING id, client_id, subscription_id` — the status column is NOT
// returned, so sub.Status is empty and the sync-based branch below is
// unreachable: deprovision always runs through deleteClientFromAllNodes even
// when a sync service is wired. Do not "fix" this by adding status to the
// RETURNING clause: the sync path needs the subscription row to still exist
// (SyncSubscription loads it by ID), but the row is already deleted here,
// which would leave the trial clients orphaned on the panels.
func (s *SubscriptionService) CleanupExpiredTrials(ctx context.Context) (int64, error) {
	subs, err := s.db.CleanupExpiredTrials(ctx, s.cfg.TrialDurationHours)
	if err != nil {
		return 0, err
	}

	var successCount int64

	for _, sub := range subs {
		if sub.SubscriptionID == "" {
			continue
		}

		if sub.Status == string(database.SubscriptionStatusActive) && s.syncService != nil {
			markErr := s.syncService.MarkAllForRemoval(ctx, sub.ID)
			if markErr != nil {
				logger.Warn("cleanup trial: mark for removal failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Error(markErr))

				continue
			}

			syncErr := s.syncService.SyncSubscription(ctx, sub.ID)
			if syncErr != nil {
				logger.Warn("cleanup trial: sync failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Error(syncErr))

				continue
			}

			successCount++
		} else {
			// Fallback: syncService is nil (only possible in tests or before
			// SetSyncService is called). Use direct deprovision so VPN clients
			// are still cleaned up.
			s.deleteClientFromAllNodes(ctx, vpn.SubscriptionProvision{
				ClientID: sub.ClientID,
				Username: "trial_" + sub.SubscriptionID,
				SubID:    sub.SubscriptionID,
			})

			successCount++
		}

		if s.invalidateBySubID != nil {
			s.invalidateBySubID(sub.SubscriptionID)
		}
	}

	return successCount, nil
}

// GetOrCreateSubscription returns an existing subscription or creates a new free-plan one with sync.
func (s *SubscriptionService) GetOrCreateSubscription(ctx context.Context, telegramID int64, username, inviteCode string) (*database.Subscription, error) {
	username = XUIEmail(username, telegramID)

	existing, err := s.db.GetByTelegramID(ctx, telegramID)
	if err == nil {
		err = s.ensureSubscriptionNodes(ctx, existing)
		if err != nil {
			return nil, fmt.Errorf("repair subscription nodes: %w", err)
		}

		return existing, nil
	}

	if !errors.Is(err, database.ErrSubscriptionNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup subscription: %w", err)
	}
	// No active subscription. If a non-active one exists (e.g. left "revoked"
	// after a partially-failed delete), reanimate it instead of inserting a
	// duplicate row that would violate telegram_id uniqueness.
	existingAny, anyErr := s.db.GetAnyByTelegramID(ctx, telegramID)
	if anyErr == nil {
		return s.reanimateRevokedSubscription(ctx, existingAny, inviteCode)
	}

	if !errors.Is(anyErr, database.ErrSubscriptionNotFound) && !errors.Is(anyErr, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup subscription (any status): %w", anyErr)
	}

	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("resolve free plan: %w", err)
	}

	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}

	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

	sub := &database.Subscription{
		TelegramID:     telegramID,
		Username:       username,
		ClientID:       clientID,
		SubscriptionID: subID,
		PlanID:         freePlan.ID,
		Status:         string(database.SubscriptionStatusActive),
	}

	err = s.db.CreateSubscription(ctx, sub, inviteCode)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	err = s.ensureSubscriptionNodes(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("ensure subscription nodes: %w", err)
	}

	metrics.SubscriptionCreatesTotal.Inc()

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// ensureSubscriptionNodes creates pending_add records for plan nodes missing from subscription_nodes, then triggers sync.
// This is the single entry point for provisioning VPN node access when a subscription is created or changed.
func (s *SubscriptionService) ensureSubscriptionNodes(ctx context.Context, sub *database.Subscription) error {
	if sub == nil {
		return fmt.Errorf("nil subscription")
	}

	nodes, err := s.db.GetNodesByPlanID(ctx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("load plan nodes: %w", err)
	}

	existing, err := s.db.GetBySubscriptionID(ctx, sub.ID)
	if err != nil {
		return fmt.Errorf("load subscription nodes: %w", err)
	}

	existingByNodeID := make(map[uint]database.SubscriptionNode, len(existing))
	for _, sn := range existing {
		existingByNodeID[sn.NodeID] = sn
	}

	createdAny := false

	for _, node := range nodes {
		if !node.IsActive {
			continue
		}

		if _, ok := existingByNodeID[node.ID]; ok {
			continue
		}

		err = s.db.UpsertSubscriptionNode(ctx, &database.SubscriptionNode{
			SubscriptionID: sub.ID,
			NodeID:         node.ID,
			Status:         database.SyncStatusPendingAdd,
		})
		if err != nil {
			return fmt.Errorf("upsert subscription node %d: %w", node.ID, err)
		}

		createdAny = true
	}

	if createdAny && s.syncService != nil {
		syncErr := s.syncService.SyncSubscription(ctx, sub.ID)
		if syncErr != nil {
			logger.Warn("initial sync failed for subscription",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(syncErr))
		}
	}

	return nil
}

type expiryRepository interface {
	ExpireSubscriptionWithPlanCAS(ctx context.Context, subscriptionID, planID uint, applyPlan database.ExpireSubscriptionPlanInTxFn) error
}

// ExpireSubscription downgrades the subscription to the Free plan and syncs node removals.
func (s *SubscriptionService) ExpireSubscription(ctx context.Context, subscriptionID uint) error {
	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("get subscription: %w", err)
	}

	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return fmt.Errorf("resolve free plan: %w", err)
	}

	if repo, ok := s.db.(expiryRepository); ok && s.syncService != nil {
		err = repo.ExpireSubscriptionWithPlanCAS(ctx, sub.ID, freePlan.ID, func(ctx context.Context, tx *gorm.DB, subscriptionID, planID uint) error {
			return s.syncService.ApplyPlanToSubscriptionInTx(ctx, tx, subscriptionID, planID)
		})
		if err != nil {
			return fmt.Errorf("expire subscription: %w", err)
		}
	} else {
		err = s.db.ExpireSubscription(ctx, sub.ID, freePlan.ID)
		if err != nil {
			return fmt.Errorf("expire subscription: %w", err)
		}
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	if s.syncService != nil {
		if _, atomicPath := s.db.(expiryRepository); !atomicPath {
			err = s.syncService.ApplyPlanToSubscription(ctx, sub.ID)
			if err != nil {
				return fmt.Errorf("expire subscription: apply plan: %w", err)
			}
		}

		err = s.syncService.SyncSubscription(ctx, sub.ID)
		if err != nil {
			logger.Warn("sync subscription failed (will retry)", zap.Error(err))
		}
	}

	return nil
}
