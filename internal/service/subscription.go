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
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type SubscriptionService struct {
	db                interfaces.DatabaseService
	xuiClients        map[uint]interfaces.XUIClient
	vpnClients        map[uint]vpn.Client
	nodes             []database.Node
	cfg               *config.Config
	invalidate        func(telegramID int64)
	invalidateBySubID func(subID string)
	syncService       *SyncService
}

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

// NewSubscriptionService creates a SubscriptionService configured with the given database, XUI clients map, VPN clients map, nodes, and configuration.
func NewSubscriptionService(db interfaces.DatabaseService, xuiClients map[uint]interfaces.XUIClient, vpnClients map[uint]vpn.Client, nodes []database.Node, cfg *config.Config) *SubscriptionService {
	return &SubscriptionService{
		db:         db,
		xuiClients: xuiClients,
		vpnClients: vpnClients,
		nodes:      nodes,
		cfg:        cfg,
	}
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
		if err := s.ensureSubscriptionNodes(ctx, existing); err != nil {
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
		Status:         "active",
	}

	if err := s.db.CreateSubscription(ctx, sub, inviteCode); err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	if err := s.ensureSubscriptionNodes(ctx, sub); err != nil {
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

func calculateProductExpiry(now time.Time, currentPlanID uint, currentExpiry *time.Time, product *database.Product) time.Time {
	base := now
	if product != nil && currentPlanID == product.PlanID && currentExpiry != nil && currentExpiry.After(now) {
		base = *currentExpiry
	}
	return base.AddDate(0, 0, product.DurationDays)
}

// GetByTelegramID retrieves a subscription by Telegram user ID.
func (s *SubscriptionService) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	return s.db.GetByTelegramID(ctx, telegramID)
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

	sub.PlanID = freePlan.ID
	sub.Status = "active"
	sub.ExpiresAt = nil
	sub.ProductID = nil
	sub.StartedAt = nil
	sub.PricePaidCents = 0
	sub.Currency = nil
	sub.Devices = "[]"
	sub.Ips = "[]"

	if err := s.db.UpdateSubscription(ctx, sub); err != nil {
		return nil, fmt.Errorf("reanimate subscription: %w", err)
	}

	// Wipe stale node bindings; ensureSubscriptionNodes rebuilds them as pending_add.
	if err := s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, sub.ID); err != nil {
		logger.Warn("failed to clear subscription nodes during reanimation",
			zap.Uint("subscription_id", sub.ID),
			zap.Error(err))
	}

	if err := s.ensureSubscriptionNodes(ctx, sub); err != nil {
		return nil, fmt.Errorf("ensure subscription nodes: %w", err)
	}

	metrics.SubscriptionCreatesTotal.Inc()
	s.RefreshActiveSubscriptionsMetric(ctx)
	return sub, nil
}

// revokeAndDeprovisionThenDelete runs the two-phase subscription teardown shared by
// Delete and DeleteByID: mark revoked → deprovision VPN access (best-effort; background
// sync reconciles on failure) → physically delete the DB row + subscription nodes →
// invalidate the cache. The resolved sub is the single input, so the lifecycle contract
// lives in exactly one place. Returns the deleted subscription (nil on error).
func (s *SubscriptionService) revokeAndDeprovisionThenDelete(ctx context.Context, sub *database.Subscription) (*database.Subscription, error) {
	// Phase 1: mark revoked before any external effect.
	sub.Status = "revoked"
	if err := s.db.UpdateSubscription(ctx, sub); err != nil {
		return nil, fmt.Errorf("mark revoked: %w", err)
	}

	// Phase 2: deprovision VPN access (best-effort; background sync reconciles on failure).
	if s.syncService != nil {
		if err := s.syncService.MarkAllForRemoval(ctx, sub.ID); err != nil {
			return nil, fmt.Errorf("deprovision mark failed: %w", err)
		}
		if err := s.syncService.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("deprovision sync failed; subscription remains revoked, background sync will retry",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(err))
		}
	}

	// Phase 3: physical delete.
	// If this fails, the row is already revoked; background reconciliation cleans up.
	// Unified on DeleteSubscriptionByID(sub.ID): both entry points already resolved sub,
	// so the primary key is known and the telegramID/id divergence in the old copies is gone.
	if _, err := s.db.DeleteSubscriptionByID(ctx, sub.ID); err != nil {
		return nil, fmt.Errorf("db delete: %w", err)
	}
	if err := s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, sub.ID); err != nil {
		// Non-fatal: SyncPendingNodes will purge orphan nodes on next run.
		logger.Warn("failed to delete subscription nodes after subscription delete",
			zap.Uint("subscription_id", sub.ID),
			zap.Error(err))
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.InvalidateBySubID(ctx, sub.SubscriptionID)
	}

	s.RefreshActiveSubscriptionsMetric(ctx)
	return sub, nil
}

// Delete removes a subscription by Telegram ID. Two-phase teardown is owned by
// revokeAndDeprovisionThenDelete; if deprovision fails, the subscription stays
// revoked and ReconcileOrphanedClients/SyncPendingNodes finish removal in the background.
func (s *SubscriptionService) Delete(ctx context.Context, telegramID int64) error {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return err
	}
	_, err = s.revokeAndDeprovisionThenDelete(ctx, sub)
	return err
}

// DeleteByID deletes a subscription by database ID. Used by admin /del command.
// Two-phase teardown is owned by revokeAndDeprovisionThenDelete.
func (s *SubscriptionService) DeleteByID(ctx context.Context, id uint) (*database.Subscription, error) {
	sub, err := s.db.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return s.revokeAndDeprovisionThenDelete(ctx, sub)
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
		if err := client.DeleteSubscription(ctx, provision); err != nil {
			logger.Warn("failed to delete VPN subscription on node",
				zap.String("username", provision.Username),
				zap.Uint("node_id", node.ID),
				zap.Error(err))
		}
	}
}

// TrialCreateResult holds the outcome of a trial creation.
type TrialCreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
	SubID           string
	ClientID        string
}

// CreateTrial provisions a new anonymous trial subscription.
// It resolves the trial plan, picks the first trial node,
// creates a client on that node via XUI, and persists the subscription
// in the database with telegram_id = 0 (unactivated).
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
	expiryTime := time.Now().Add(time.Duration(s.cfg.TrialDurationHours) * time.Hour)
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
	client, ok := s.xuiClients[node.ID]
	if !ok {
		return nil, fmt.Errorf("xui client not found for node %d", node.ID)
	}
	inboundIDs := node.ResolveInboundIDs()
	if _, err = client.AddClientWithID(ctx, xui.ClientRequest{
		InboundIDs:   inboundIDs,
		Email:        email,
		ClientID:     clientID,
		SubID:        subID,
		TrafficBytes: trafficBytes,
		ExpiryTime:   expiryTime,
		ResetDays:    resetDays,
	}); err != nil {
		return nil, fmt.Errorf("add trial client on node %d: %w", node.ID, err)
	}

	sub, err := s.db.CreateTrialSubscription(ctx, inviteCode, subID, clientID, expiryTime)
	if err != nil {
		s.deleteClientFromAllNodes(ctx, vpn.SubscriptionProvision{
			ClientID: clientID,
			Username: email,
			SubID:    subID,
		})
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

// GetByID retrieves a subscription by database ID.
func (s *SubscriptionService) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return s.db.GetByID(ctx, id)
}

// GetOrCreateInvite gets an existing invite or creates a new one for the given referrer.
func (s *SubscriptionService) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	return s.db.GetOrCreateInvite(ctx, referrerTGID, code)
}

// GetInviteByCode retrieves an invite by its code.
func (s *SubscriptionService) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	return s.db.GetInviteByCode(ctx, code)
}

// BindTrialSubscription binds a trial subscription to a Telegram user.
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
		if invite, err := s.db.GetInviteByCode(ctx, *sub.InviteCode); err == nil {
			if referrerSub, err := s.db.GetByTelegramID(ctx, invite.ReferrerTGID); err == nil {
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
	// Trial is intentionally single-node (provisioned on nodes[0] by CreateTrial).
	// Only update the node where the client actually exists.
	node := nodes[0]
	client, ok := s.xuiClients[node.ID]
	if !ok {
		return sub, fmt.Errorf("xui client not found for trial node %d", node.ID)
	}
	inboundIDs := node.ResolveInboundIDs()
	if err := client.UpdateClient(ctx, xui.ClientRequest{
		InboundIDs:   inboundIDs,
		CurrentEmail: currentEmail,
		ClientID:     sub.ClientID,
		Email:        email,
		SubID:        sub.SubscriptionID,
		TrafficBytes: trafficBytes,
		ExpiryTime:   expiryTime,
		ResetDays:    resetDays,
		TgID:         telegramID,
		Comment:      comment,
	}); err != nil {
		return sub, fmt.Errorf("update trial client on node %d: %w", node.ID, err)
	}

	return sub, nil
}

// CountAll returns the total number of subscriptions.
func (s *SubscriptionService) CountAll(ctx context.Context) (int64, error) {
	return s.db.CountAllSubscriptions(ctx)
}

// CountActive returns the number of active subscriptions.
func (s *SubscriptionService) CountActive(ctx context.Context) (int64, error) {
	return s.db.CountActiveSubscriptions(ctx)
}

// RefreshActiveSubscriptionsMetric updates the active_subscriptions and trial_subscriptions gauges.
func (s *SubscriptionService) RefreshActiveSubscriptionsMetric(ctx context.Context) {
	count, err := s.CountActive(ctx)
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

// GetLatest returns the most recent subscriptions up to the given limit.
func (s *SubscriptionService) GetLatest(ctx context.Context, limit int) ([]database.Subscription, error) {
	return s.db.GetLatestSubscriptions(ctx, limit)
}

// GetTelegramIDByUsername looks up a Telegram ID by username.
func (s *SubscriptionService) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	return s.db.GetTelegramIDByUsername(ctx, username)
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

// ReconcileOrphanedClients scans all active subscriptions and removes those that
// are no longer provisioned on any VPN node.
//
// It uses the subscription_nodes table — the source of truth for node
// provisioning — instead of querying each node's panel directly. This works for
// every node type (3x-ui, proxman, fetch) and fixes the previous bug where
// subscriptions on proxman/fetch nodes were falsely deleted because the legacy
// xuiClients map only covers 3x-ui nodes.
//
// A subscription is orphaned when it has subscription_nodes rows but none are in
// a live state (active/pending_add/pending_update) — i.e. every binding is
// pending_remove (deprovisioning did not complete in the delete flow).
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
		if sub.Status == "active" {
			activeSubs = append(activeSubs, sub)
		}
	}

	removed := 0
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
		if _, delErr := s.db.DeleteSubscriptionByID(ctx, sub.ID); delErr != nil {
			logger.Warn("failed to delete orphaned subscription",
				zap.Error(delErr),
				zap.Uint("id", sub.ID),
				zap.Int64("telegram_id", sub.TelegramID),
				zap.String("subscription_id", sub.SubscriptionID))
		} else {
			removed++
			logger.Info("removed orphaned subscription (no live node bindings)",
				zap.Uint("id", sub.ID),
				zap.Int64("telegram_id", sub.TelegramID),
				zap.String("username", sub.Username),
				zap.String("subscription_id", sub.SubscriptionID))
			if s.invalidate != nil && sub.TelegramID > 0 {
				s.invalidate(sub.TelegramID)
			}
			if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
				s.invalidateBySubID(sub.SubscriptionID)
			}
			metrics.OrphanedClientsRemovedTotal.Inc()
		}

		if ctx.Err() != nil {
			return removed, ctx.Err()
		}
	}
	s.RefreshActiveSubscriptionsMetric(ctx)
	metrics.ReconcileOrphanedDuration.Observe(time.Since(start).Seconds())
	return removed, nil
}

// CleanupExpiredTrials deletes expired trial subscriptions from the database
// and removes their clients from all XUI sources.
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
		if sub.Status == "active" && s.syncService != nil {
			if markErr := s.syncService.MarkAllForRemoval(ctx, sub.ID); markErr != nil {
				logger.Warn("cleanup trial: mark for removal failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Error(markErr))
				continue
			}
			if syncErr := s.syncService.SyncSubscription(ctx, sub.ID); syncErr != nil {
				logger.Warn("cleanup trial: sync failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Error(syncErr))
				continue
			}
			successCount++
		} else {
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

// GetTotalTelegramIDCount returns the count of unique Telegram IDs for active subscriptions eligible for broadcast.
func (s *SubscriptionService) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	return s.db.GetTotalTelegramIDCount(ctx)
}

// GetTelegramIDsBatch returns a batch of Telegram IDs for active subscriptions eligible for broadcast.
func (s *SubscriptionService) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	return s.db.GetTelegramIDsBatch(ctx, offset, limit)
}

// GetAllReferralCounts returns referral counts for all users.
func (s *SubscriptionService) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	return s.db.GetAllReferralCounts(ctx)
}

// GetOrCreateSubscription returns an existing subscription or creates a new free-plan one with sync.
func (s *SubscriptionService) GetOrCreateSubscription(ctx context.Context, telegramID int64, username, inviteCode string) (*database.Subscription, error) {
	username = XUIEmail(username, telegramID)

	existing, err := s.db.GetByTelegramID(ctx, telegramID)
	if err == nil {
		if err := s.ensureSubscriptionNodes(ctx, existing); err != nil {
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
		Status:         "active",
	}

	if err := s.db.CreateSubscription(ctx, sub, inviteCode); err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	if err := s.ensureSubscriptionNodes(ctx, sub); err != nil {
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

	// 1. Load active 3x-ui nodes linked to the subscription's plan
	nodes, err := s.db.GetNodesByPlanID(ctx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("load plan nodes: %w", err)
	}

	// 2. Load existing subscription_nodes to avoid duplicates
	existing, err := s.db.GetBySubscriptionID(ctx, sub.ID)
	if err != nil {
		return fmt.Errorf("load subscription nodes: %w", err)
	}

	existingByNodeID := make(map[uint]database.SubscriptionNode, len(existing))
	for _, sn := range existing {
		existingByNodeID[sn.NodeID] = sn
	}

	// 3. Create pending_add for each active plan node not yet in subscription_nodes
	createdAny := false
	for _, node := range nodes {
		if !node.IsActive {
			continue
		}
		if _, ok := existingByNodeID[node.ID]; ok {
			continue
		}
		if err := s.db.UpsertSubscriptionNode(ctx, &database.SubscriptionNode{
			SubscriptionID: sub.ID,
			NodeID:         node.ID,
			Status:         database.SyncStatusPendingAdd,
		}); err != nil {
			return fmt.Errorf("upsert subscription node %d: %w", node.ID, err)
		}
		createdAny = true
	}

	// 4. Best-effort sync: trigger immediate VPN provisioning
	if createdAny && s.syncService != nil {
		if syncErr := s.syncService.SyncSubscription(ctx, sub.ID); syncErr != nil {
			logger.Warn("initial sync failed for subscription",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(syncErr))
		}
	}

	return nil
}

// RenewSubscription extends the subscription expiry, creates a paid order, recalculates nodes, and syncs.
func (s *SubscriptionService) RenewSubscription(ctx context.Context, telegramID int64, product *database.Product) (*database.Order, error) {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	planChanged := sub.PlanID != product.PlanID
	newExpiry := calculateProductExpiry(now, sub.PlanID, sub.ExpiresAt, product)
	oldExpiry := sub.ExpiresAt

	sub.PlanID = product.PlanID
	sub.ProductID = &product.ID
	sub.ExpiresAt = &newExpiry
	sub.PricePaidCents = product.PriceCents
	sub.Currency = &product.Currency
	sub.StartedAt = &now
	sub.UpdatedAt = now

	order := &database.Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         database.OrderStatusPaid,
		AmountCents:    product.PriceCents,
		Currency:       product.Currency,
		PaidAt:         &now,
		ActivatedAt:    &now,
		ExpiresAt:      &newExpiry,
	}
	if err := s.db.Transaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Subscription{}).
			Where("id = ?", sub.ID).
			Select("telegram_id", "username", "client_id", "subscription_id", "expires_at", "status", "invite_code", "plan_id", "referred_by", "devices", "ips", "product_id", "started_at", "price_paid_cents", "currency", "updated_at").
			Updates(sub).Error; err != nil {
			return fmt.Errorf("update subscription: %w", err)
		}

		if err := tx.Create(order).Error; err != nil {
			return fmt.Errorf("create order: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	metrics.SubscriptionRenewalsTotal.Inc()

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	if s.syncService != nil {
		if planChanged || (oldExpiry == nil || !oldExpiry.Equal(newExpiry)) {
			if err := s.syncService.ApplyPlanToSubscription(ctx, sub.ID); err != nil {
				return order, fmt.Errorf("renew subscription: apply plan: %w", err)
			}
		}

		if err := s.syncService.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("renew subscription: post-commit sync failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("product_id", product.ID),
				zap.Error(err))
			return order, nil
		}
	}

	return order, nil
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

	if err := s.db.ExpireSubscription(ctx, sub.ID, freePlan.ID); err != nil {
		return fmt.Errorf("expire subscription: %w", err)
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	if s.syncService != nil {
		if err := s.syncService.ApplyPlanToSubscription(ctx, sub.ID); err != nil {
			logger.Warn("expire subscription: apply plan failed (will retry)",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(err))
		}
		if err := s.syncService.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("sync subscription failed (will retry)", zap.Error(err))
		}
	}

	return nil
}
