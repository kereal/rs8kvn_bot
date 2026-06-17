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
	"github.com/kereal/rs8kvn_bot/internal/webhook"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"go.uber.org/zap"
)

type SubscriptionService struct {
	db           interfaces.DatabaseService
	xuiClients   map[uint]interfaces.XUIClient
	nodes        []database.Node
	cfg          *config.Config
	globalSubURL string
	webhook      webhook.WebhookSender
	invalidate   func(telegramID int64)
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

// NewSubscriptionService creates a SubscriptionService configured with the given database, XUI clients map, sources, configuration, global subscription URL prefix, and optional webhook sender.
func NewSubscriptionService(db interfaces.DatabaseService, xuiClients map[uint]interfaces.XUIClient, nodes []database.Node, cfg *config.Config, globalSubURL string, webhookSender webhook.WebhookSender) *SubscriptionService {
	return &SubscriptionService{
		db:           db,
		xuiClients:   xuiClients,
		nodes:        nodes,
		cfg:          cfg,
		globalSubURL: globalSubURL,
		webhook:      webhookSender,
	}
}

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
// sub.InviteCode / sub.ReferredBy. The resolved ReferrerTGID (0 if unset) is
// returned in CreateResult so callers can update aggregate referral state.
func (s *SubscriptionService) Create(ctx context.Context, chatID int64, username, inviteCode string) (*CreateResult, error) {

	plan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve free plan: %w", err)
	}

	trafficBytes := plan.TrafficLimit

	expiryTime := time.Now().AddDate(0, 0, config.SubscriptionResetDay)
	resetday := config.SubscriptionResetDay

	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

	email := XUIEmail(username, chatID)

	var firstClient *xui.ClientConfig
	var firstErr error
	nodes := s.activeNodes()
	for _, node := range nodes {
		client, ok := s.xuiClients[node.ID]
		if !ok {
			continue
		}
		inboundIDs := node.ResolveInboundIDs()
		c, err := client.AddClientWithID(xui.WithTgID(ctx, chatID), inboundIDs, email, clientID, subID, trafficBytes, expiryTime, resetday)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			logger.Warn("failed to add client on node",
				zap.Uint("node_id", node.ID),
				zap.Error(err))
			continue
		}
		if firstClient == nil {
			firstClient = c
		}
	}

	if firstClient == nil {
		return nil, fmt.Errorf("failed to create client on any node: %w", firstErr)
	}

	sub := &database.Subscription{
		TelegramID:     chatID,
		Username:       username,
		ClientID:       firstClient.ID,
		SubscriptionID: firstClient.SubID,
		ExpiresAt:      expiryTime,
		PlanID:         plan.ID,
		Status:         "active",
	}

	if firstClient.SubID == "" {
		s.deleteClientFromAllNodes(ctx, email)
		return nil, fmt.Errorf("xui client returned empty subscription id")
	}

	if err := s.db.CreateSubscription(ctx, sub, inviteCode); err != nil {
		s.deleteClientFromAllNodes(ctx, email)
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	subscriptionURL := s.cfg.SubURL(firstClient.SubID)
	return &CreateResult{
		Subscription:    sub,
		SubscriptionURL: subscriptionURL,
		ReferrerTGID:    sub.ReferredBy,
	}, nil
}

func (s *SubscriptionService) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	return s.db.GetByTelegramID(ctx, telegramID)
}

func (s *SubscriptionService) Delete(ctx context.Context, telegramID int64) error {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return err
	}

	if err := s.db.DeleteSubscription(ctx, telegramID); err != nil {
		return fmt.Errorf("db delete: %w", err)
	}

	email := XUIEmail(sub.Username, telegramID)
	s.deleteClientFromAllNodes(ctx, email)

	if s.webhook != nil {
		eventID, _ := utils.GenerateUUID()
		s.webhook.SendAsync(ctx, webhook.Event{
			EventID:        "evt-" + eventID,
			Event:          webhook.EventSubscriptionExpired,
			ClientID:       sub.ClientID,
			Email:          email,
			SubscriptionID: sub.SubscriptionID,
		})
	}

	return nil
}

// DeleteByID deletes a subscription by database ID.
// Used by admin /del command.
func (s *SubscriptionService) DeleteByID(ctx context.Context, id uint) (*database.Subscription, error) {
	sub, err := s.db.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}

	clientID := sub.ClientID
	subscriptionID := sub.SubscriptionID

	deleted, err := s.db.DeleteSubscriptionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("db delete: %w", err)
	}

	email := XUIEmail(deleted.Username, deleted.TelegramID)
	s.deleteClientFromAllNodes(ctx, email)

	if s.webhook != nil {
		eventID, _ := utils.GenerateUUID()
		s.webhook.SendAsync(ctx, webhook.Event{
			EventID:        "evt-" + eventID,
			Event:          webhook.EventSubscriptionExpired,
			ClientID:       clientID,
			Email:          email,
			SubscriptionID: subscriptionID,
		})
	}

	return deleted, nil
}

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
}

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

	email := XUIEmail(sub.Username, sub.TelegramID)

	// обходим серверы
	var totalUp, totalDown int64
	var anySuccess bool
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
		anySuccess = true
	}

	// не получилось опросить серверы
	if !anySuccess {
		return sub, &TrafficInfo{
			UsedGB:  0,
			LimitGB: limitGB,
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

	// Calculate reset time
	var resetTime time.Time
	if sub.ExpiresAt.IsZero() {
		resetTime = sub.CreatedAt.AddDate(0, 0, config.SubscriptionResetDay)
	} else {
		resetTime = sub.ExpiresAt
	}
	daysUntilReset := utils.DaysUntilReset(time.Now(), resetTime)

	// Reset info string
	var resetInfo string
	switch {
	case daysUntilReset < 0:
		resetInfo = "🔄 Сброс: отключен"
	case daysUntilReset == 0:
		resetInfo = "🔄 Сброс: сегодня"
	default:
		resetInfo = fmt.Sprintf("🔄 Сброс: через %d дн.", daysUntilReset)
	}

	return sub, &TrafficInfo{
		UsedGB:             usedGB,
		LimitGB:            limitGB,
		Percentage:         percentage,
		ProgressBar:        progressBar,
		DaysUntilReset:     daysUntilReset,
		ResetInfo:          resetInfo,
		CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
		ExpiresAtFormatted: utils.FormatDateRu(sub.ExpiresAt),
	}, nil
}

// daysUntilReset calculates the number of days until the next traffic reset.

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
	if _, err = client.AddClientWithID(ctx, inboundIDs, email, clientID, subID, trafficBytes, expiryTime, 0); err != nil {
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
func (s *SubscriptionService) BindTrial(ctx context.Context, subscriptionID string, chatID int64, username string) (*database.Subscription, error) {
	sub, err := s.db.BindTrialSubscription(ctx, subscriptionID, chatID, username)
	if err != nil {
		return nil, fmt.Errorf("bind trial subscription: %w", err)
	}

	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve free plan: %w", err)
	}
	trafficBytes := freePlan.TrafficLimit

	expiryTime := time.UnixMilli(0)

	var comment string
	if invite, err := s.db.GetInviteByCode(ctx, sub.InviteCode); err == nil {
		if referrerSub, err := s.db.GetByTelegramID(ctx, invite.ReferrerTGID); err == nil {
			comment = fmt.Sprintf("from: @%s", referrerSub.Username)
		}
	}

	currentEmail := "trial_" + subscriptionID
	email := XUIEmail(username, chatID)

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
		if err := client.UpdateClient(ctx, inboundIDs, currentEmail, sub.ClientID, email, sub.SubscriptionID, trafficBytes, expiryTime, chatID, comment); err != nil {
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
func (s *SubscriptionService) InvalidateSubscription(ctx context.Context, telegramID int64) error {
	if s.invalidate != nil {
		s.invalidate(telegramID)
	}
	// No error needed; cache invalidation is best-effort.
	return nil
}

// ReconcileOrphanedClients scans all active subscriptions and removes those whose
// client no longer exists in the XUI panel. It returns the number of removed subscriptions.
// This is a best-effort background cleanup; errors are logged but do not stop the scan.
func (s *SubscriptionService) ReconcileOrphanedClients(ctx context.Context) (int, error) {
	type activeOnly struct {
		ID             uint
		TelegramID      int64
		Username        string
		SubscriptionID  string
		ClientID        string
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
		if sub.TelegramID == 0 {
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
				if s.invalidate != nil && sub.TelegramID != 0 {
					s.invalidate(sub.TelegramID)
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
			s.deleteClientFromAllNodes(ctx, email)
		}
	}

	return int64(len(subs)), nil
}

// GetTotalTelegramIDCount returns the total number of unique Telegram IDs.
func (s *SubscriptionService) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	return s.db.GetTotalTelegramIDCount(ctx)
}

// GetTelegramIDsBatch returns a batch of Telegram IDs for pagination.
func (s *SubscriptionService) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	return s.db.GetTelegramIDsBatch(ctx, offset, limit)
}

// GetAllReferralCounts returns referral counts for all users.
func (s *SubscriptionService) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	return s.db.GetAllReferralCounts(ctx)
}
