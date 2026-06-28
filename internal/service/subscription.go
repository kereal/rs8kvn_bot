package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	return &CreateResult{
		Subscription:    sub,
		SubscriptionURL: subscriptionURL,
		ReferrerTGID:    referrerID,
	}, nil
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

// Delete removes a subscription by Telegram ID via sync module.
// VPN client removal is performed via sync. Orphaned clients are cleaned up by ReconcileOrphanedClients.
func (s *SubscriptionService) Delete(ctx context.Context, telegramID int64) error {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return err
	}

	if s.syncService != nil {
		if err := s.syncService.MarkAllForRemoval(ctx, sub.ID); err != nil {
			logger.Warn("mark all for removal failed", zap.Error(err))
		}
		if err := s.syncService.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("sync subscription failed (will retry)", zap.Error(err))
		}
	}

	if err := s.db.DeleteSubscription(ctx, telegramID); err != nil {
		return fmt.Errorf("db delete: %w", err)
	}

	if err := s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, sub.ID); err != nil {
		return fmt.Errorf("delete subscription nodes: %w", err)
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.InvalidateBySubID(ctx, sub.SubscriptionID)
	}

	return nil
}

// DeleteByID deletes a subscription by database ID via sync module.
// Used by admin /del command.
func (s *SubscriptionService) DeleteByID(ctx context.Context, id uint) (*database.Subscription, error) {
	sub, err := s.db.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}

	if s.syncService != nil {
		if err := s.syncService.MarkAllForRemoval(ctx, sub.ID); err != nil {
			logger.Warn("mark all for removal failed", zap.Error(err))
		}
		if err := s.syncService.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("sync subscription failed (will retry)", zap.Error(err))
		}
	}

	deleted, err := s.db.DeleteSubscriptionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("db delete: %w", err)
	}

	if err := s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, deleted.ID); err != nil {
		return nil, fmt.Errorf("delete subscription nodes: %w", err)
	}

	if s.invalidateBySubID != nil && deleted.SubscriptionID != "" {
		s.InvalidateBySubID(ctx, deleted.SubscriptionID)
	}

	return deleted, nil
}

// deleteClientFromAllNodes removes the client email from all active XUI nodes.
func (s *SubscriptionService) deleteClientFromAllNodes(ctx context.Context, email string) {
	for _, node := range s.nodes {
		if !node.IsActive || node.Host == "" {
			continue
		}
		client, ok := s.xuiClients[node.ID]
		if !ok {
			continue
		}
		if err := client.DeleteClient(ctx, email); err != nil {
			logger.Warn("failed to delete XUI client on source",
				zap.String("email", email),
				zap.Uint("node_id", node.ID),
				zap.Error(err))
		}
	}
}

type TrafficInfo struct {
	UsedGB             float64
	LimitGB            int
	Percentage         float64
	ProgressBar        string
	DaysUntilReset     int
	ResetInfo          string
	CreatedAtFormatted string
	ExpiresAtFormatted string
	PlanName           string
}

// PlanTrafficLimitGB returns the traffic limit in GB for the user's current plan.
func (s *SubscriptionService) PlanTrafficLimitGB(ctx context.Context, telegramID int64) int {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil || sub == nil {
		return 0
	}
	plan, planErr := s.db.GetPlanByID(ctx, sub.PlanID)
	if planErr != nil {
		return 0
	}
	return int(float64(plan.TrafficLimit) / 1024 / 1024 / 1024)
}

// Получаем данные подписки, содержащие информацию о трафике
func (s *SubscriptionService) GetWithTraffic(ctx context.Context, telegramID int64) (*database.Subscription, *TrafficInfo, error) {

	// получили подписку
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, nil, err
	}

	limitGB := s.PlanTrafficLimitGB(ctx, telegramID)

	// Получаем название тарифного плана (product.name если есть product_id, иначе plan.name)
	plan, planErr := s.db.GetPlanByID(ctx, sub.PlanID)
	var planName string
	if planErr == nil && plan != nil {
		planName = plan.Name
	}

	if sub.ProductID != nil && *sub.ProductID != 0 {
		product, productErr := s.db.GetProductByID(ctx, *sub.ProductID)
		if productErr == nil && product != nil && product.Name != "" {
			planName = product.Name
		}
	}

	// Если лимит трафика нулевой — не опрашиваем серверы
	if limitGB == 0 {
		return sub, &TrafficInfo{
			UsedGB:             0,
			LimitGB:            0,
			PlanName:           planName,
			CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
			ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
		}, nil
	}

	email := XUIEmail(sub.Username, sub.TelegramID)

	// обходим серверы
	var totalUp, totalDown int64
	var anySuccess bool
	var panelResetExpiry int64
	var panelResetDays int
	for _, node := range s.activeNodes() {
		client, ok := s.xuiClients[node.ID]
		if !ok {
			continue
		}
		traffic, err := client.GetClientTraffic(ctx, email)
		if err != nil {
			logger.Debug("GetClientTraffic failed on source",
				zap.Uint("node_id", node.ID),
				zap.Error(err))
			continue
		}
		totalUp += traffic.Up
		totalDown += traffic.Down
		panelResetExpiry = traffic.ExpiresAt
		panelResetDays = traffic.Reset
		anySuccess = true
	}

	// не получилось опросить серверы
	if !anySuccess {
		return sub, &TrafficInfo{
			UsedGB:   0,
			LimitGB:  limitGB,
			PlanName: planName,
		}, nil
	}

	usedGB := float64(totalUp+totalDown) / 1024 / 1024 / 1024
	percentage := 0.0

	if limitGB > 0 {
		percentage = (usedGB / float64(limitGB)) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	// Progress bar
	progressBar := utils.GenerateProgressBar(usedGB, float64(limitGB))

	// Calculate reset time from panel: expiryTime + reset days
	var resetInfo string
	var daysUntilReset int
	if panelResetExpiry > 0 && panelResetDays > 0 {
		resetTime := time.UnixMilli(panelResetExpiry)
		daysUntilReset = utils.DaysUntilReset(time.Now(), resetTime)
		var resetText string
		switch {
		case daysUntilReset < 0:
			resetText = "отключен"
		case daysUntilReset == 0:
			resetText = "сегодня"
		default:
			resetText = fmt.Sprintf("через %d дн.", daysUntilReset)
		}
		resetInfo = resetText
	}

	return sub, &TrafficInfo{
		UsedGB:             usedGB,
		LimitGB:            limitGB,
		Percentage:         percentage,
		ProgressBar:        progressBar,
		DaysUntilReset:     daysUntilReset,
		ResetInfo:          resetInfo,
		CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
		ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
		PlanName:           planName,
	}, nil
}

// daysUntilReset calculates the number of days until the next traffic reset.

// formatExpiresAt formats ExpiresAt for display. NULL = "бессрочно".
func formatExpiresAt(t *time.Time) string {
	if t == nil {
		return "бессрочно"
	}
	return utils.FormatDateRu(*t)
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
	resetDays := 0
	if trafficBytes > 0 {
		resetDays = -1
	}

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
	if _, err = client.AddClientWithID(ctx, inboundIDs, email, clientID, subID, trafficBytes, expiryTime, resetDays); err != nil {
		return nil, fmt.Errorf("add trial client on node %d: %w", node.ID, err)
	}

	sub, err := s.db.CreateTrialSubscription(ctx, inviteCode, subID, clientID, expiryTime)
	if err != nil {
		s.deleteClientFromAllNodes(ctx, email)
		return nil, fmt.Errorf("create trial subscription: %w", err)
	}

	subURL := s.cfg.SubURL(subID)
	return &TrialCreateResult{
		Subscription:    sub,
		SubscriptionURL: subURL,
		SubID:           subID,
		ClientID:        clientID,
	}, nil
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
	sub, err := s.db.BindTrialSubscription(ctx, subscriptionID, telegramID, username)
	if err != nil {
		return nil, fmt.Errorf("bind trial subscription: %w", err)
	}

	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve free plan: %w", err)
	}
	trafficBytes := freePlan.TrafficLimit
	resetDays := 0
	if trafficBytes > 0 {
		resetDays = -1
	}

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
	for _, node := range nodes {
		client, ok := s.xuiClients[node.ID]
		if !ok {
			continue
		}
		inboundIDs := node.ResolveInboundIDs()
		if err := client.UpdateClient(ctx, inboundIDs, currentEmail, sub.ClientID, email, sub.SubscriptionID, trafficBytes, expiryTime, resetDays, telegramID, comment); err != nil {
			logger.Warn("UpdateClient failed on trial node",
				zap.Uint("node_id", node.ID),
				zap.Error(err))
			continue
		}
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

// ReconcileOrphanedClients scans all active subscriptions and removes those whose
// client no longer exists in the XUI panel. It returns the number of removed subscriptions.
// This is a best-effort background cleanup; errors are logged but do not stop the scan.
func (s *SubscriptionService) ReconcileOrphanedClients(ctx context.Context) (int, error) {
	type activeOnly struct {
		ID             uint
		TelegramID     int64
		Username       string
		SubscriptionID string
		ClientID       string
	}

	rows, err := s.db.GetAllSubscriptions(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch subscriptions: %w", err)
	}

	activeSubs := make([]activeOnly, 0, len(rows))
	for _, sub := range rows {
		if sub.Status == "active" {
			activeSubs = append(activeSubs, activeOnly{
				ID:             sub.ID,
				TelegramID:     sub.TelegramID,
				Username:       sub.Username,
				SubscriptionID: sub.SubscriptionID,
				ClientID:       sub.ClientID,
			})
		}
	}

	removed := 0
	for _, sub := range activeSubs {
		var xuiEmail string
		if sub.TelegramID < 0 {
			if sub.SubscriptionID == "" {
				continue
			}
			xuiEmail = "trial_" + sub.SubscriptionID
		} else {
			xuiEmail = XUIEmail(sub.Username, sub.TelegramID)
		}
		if xuiEmail == "" {
			continue
		}

		pendingNodes, pendErr := s.db.GetPendingBySubscriptionID(ctx, sub.ID)
		if pendErr == nil && len(pendingNodes) > 0 {
			continue
		}
		if pendErr != nil {
			logger.Warn("failed to check pending nodes for orphan reconciliation",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(pendErr))
		}

		notFoundOnAll := true
		for _, node := range s.activeNodes() {
			client, ok := s.xuiClients[node.ID]
			if !ok {
				continue
			}
			_, err := client.GetClientTraffic(ctx, xuiEmail)
			if err == nil {
				notFoundOnAll = false
				break
			}
			if errors.Is(err, xui.ErrClientNotFound) {
				continue
			}
			errMsg := strings.ToLower(err.Error())
			if !strings.Contains(errMsg, "client not found") {
				notFoundOnAll = false
				logger.Debug("Error checking XUI client, skipping",
					zap.Error(err),
					zap.Int64("telegram_id", sub.TelegramID))
				break
			}
		}

		if notFoundOnAll {
			if _, delErr := s.db.DeleteSubscriptionByID(ctx, sub.ID); delErr != nil {
				logger.Warn("Failed to delete orphaned subscription",
					zap.Error(delErr),
					zap.Uint("id", sub.ID),
					zap.Int64("telegram_id", sub.TelegramID),
					zap.String("subscription_id", sub.SubscriptionID))
			} else {
				removed++
				logger.Info("Removed orphaned subscription (XUI client missing on all nodes)",
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
		}

		if ctx.Err() != nil {
			return removed, ctx.Err()
		}
	}
	return removed, nil
}

// CleanupExpiredTrials deletes expired trial subscriptions from the database
// and removes their clients from all XUI sources.
func (s *SubscriptionService) CleanupExpiredTrials(ctx context.Context) (int64, error) {
	subs, err := s.db.CleanupExpiredTrials(ctx, s.cfg.TrialDurationHours)
	if err != nil {
		return 0, err
	}

	for _, sub := range subs {
		if sub.SubscriptionID != "" {
			email := "trial_" + sub.SubscriptionID
			if sub.Status == "active" && s.syncService != nil {
				s.syncService.MarkAllForRemoval(ctx, sub.ID)
				s.syncService.SyncSubscription(ctx, sub.ID)
			} else {
				s.deleteClientFromAllNodes(ctx, email)
			}
			if s.invalidateBySubID != nil {
				s.invalidateBySubID(sub.SubscriptionID)
			}
		}
	}

	return int64(len(subs)), nil
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
	existing, err := s.db.GetByTelegramID(ctx, telegramID)
	if err == nil {
		if err := s.ensureSubscriptionNodes(ctx, existing); err != nil {
			return nil, fmt.Errorf("repair subscription nodes: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, database.ErrSubscriptionNotFound) {
		return nil, fmt.Errorf("lookup subscription: %w", err)
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

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	if s.syncService != nil {
		if planChanged {
			newNodes, _ := s.db.GetNodesByPlanID(ctx, sub.PlanID)
			var newNodeIDs []uint
			for _, n := range newNodes {
				newNodeIDs = append(newNodeIDs, n.ID)
			}
			if err := s.db.MarkActiveNodesPendingUpdate(ctx, sub.ID, newNodeIDs); err != nil {
				logger.Warn("mark active nodes pending update failed", zap.Error(err))
			}

			if err := s.syncService.ReconcilePlanNodes(ctx, sub.ID); err != nil {
				logger.Warn("reconcile plan nodes failed (will retry)", zap.Error(err))
			}
		}

		if err := s.syncService.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("sync subscription failed (will retry)", zap.Error(err))
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
		newNodes, _ := s.db.GetNodesByPlanID(ctx, freePlan.ID)
		var newNodeIDs []uint
		for _, n := range newNodes {
			newNodeIDs = append(newNodeIDs, n.ID)
		}
		if err := s.db.MarkActiveNodesPendingUpdate(ctx, sub.ID, newNodeIDs); err != nil {
			logger.Warn("mark active nodes pending update failed", zap.Error(err))
		}

		if err := s.syncService.ReconcilePlanNodes(ctx, sub.ID); err != nil {
			logger.Warn("reconcile plan nodes failed (will retry)", zap.Error(err))
		}
		if err := s.syncService.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("sync subscription failed (will retry)", zap.Error(err))
		}
	}

	return nil
}
