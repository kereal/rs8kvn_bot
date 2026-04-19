package service

import (
	"context"
	"fmt"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	"go.uber.org/zap"
)

// WebhookSender interface for sending webhook events (mockable for tests).
type WebhookSender interface {
	SendAsync(event Event)
}

// Event represents a webhook event (re-exported from webhook package).
type Event = webhook.Event

type SubscriptionService struct {
	db      interfaces.DatabaseService
	xui     interfaces.XUIClient
	cfg     *config.Config
	webhook WebhookSender
}

type CreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
}

// NewSubscriptionService creates a SubscriptionService configured with the given database, XUI client, configuration, and optional webhook sender.
// If webhookSender is nil, webhook delivery will be disabled for the service.
func NewSubscriptionService(db interfaces.DatabaseService, xui interfaces.XUIClient, cfg *config.Config, webhookSender WebhookSender) *SubscriptionService {
	return &SubscriptionService{
		db:      db,
		xui:     xui,
		cfg:     cfg,
		webhook: webhookSender,
	}
}

func (s *SubscriptionService) Create(ctx context.Context, chatID int64, username string) (*CreateResult, error) {
	trafficBytes := int64(s.cfg.TrafficLimitGB) * 1024 * 1024 * 1024

	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

	// Calculate expiry time for auto-reset (now + reset days)
	expiryTime := time.Now().Add(time.Duration(config.SubscriptionResetDay) * 24 * time.Hour)

	client, err := s.xui.AddClientWithID(ctx, s.cfg.XUIInboundID, username, clientID, subID, trafficBytes, expiryTime, config.SubscriptionResetDay)
	if err != nil {
		return nil, fmt.Errorf("xui add client: %w", err)
	}

	subscriptionURL := s.xui.GetSubscriptionLink(s.xui.GetExternalURL(s.cfg.XUIHost), client.SubID, s.cfg.XUISubPath)

	sub := &database.Subscription{
		TelegramID:      chatID,
		Username:        username,
		ClientID:        client.ID,
		SubscriptionID:  client.SubID,
		InboundID:       s.cfg.XUIInboundID,
		TrafficLimit:    trafficBytes,
		ExpiryTime:      expiryTime,
		Status:          "active",
		SubscriptionURL: subscriptionURL,
	}

	if err := s.db.CreateSubscription(ctx, sub); err != nil {
		// Retry rollback with backoff to ensure client is deleted from XUI
		rollbackErr := xui.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			return s.xui.DeleteClient(ctx, s.cfg.XUIInboundID, client.ID)
		})
		if rollbackErr != nil {
			return nil, fmt.Errorf("create subscription: %w (rollback failed: %w)", err, rollbackErr)
		}
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	// Send webhook notification (async)
	if s.webhook != nil {
		eventID, _ := utils.GenerateUUID()
		s.webhook.SendAsync(Event{
			EventID:           "evt-" + eventID,
			Event:             webhook.EventSubscriptionActivated,
			UserID:            sub.ClientID,
			Email:             sub.Username,
			SubscriptionID: sub.SubscriptionID,
			Plan:              sub.Plan,
		})
	}

	return &CreateResult{
		Subscription:    sub,
		SubscriptionURL: subscriptionURL,
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

	// Store subscription data before deletion for webhook
	clientID := sub.ClientID
	inboundID := sub.InboundID
	username := sub.Username
	subscriptionID := sub.SubscriptionID
	plan := sub.Plan

	if inboundID == 0 {
		inboundID = s.cfg.XUIInboundID
	}

	// Delete from database first — if this fails, the XUI client remains
	// intact and the operation can be retried. Reversing the order (XUI
	// first) would leave an orphaned DB record with no XUI client on failure.
	if err := s.db.DeleteSubscription(ctx, telegramID); err != nil {
		return fmt.Errorf("db delete: %w", err)
	}

	// Best-effort XUI cleanup: log but don't fail if XUI delete errors.
	// The DB record is already gone; an orphaned XUI client is less critical
	// than an orphaned DB record and can be cleaned up manually.
	if err := s.xui.DeleteClient(ctx, inboundID, clientID); err != nil {
		logger.Error("Failed to delete XUI client (orphaned client may remain)",
			zap.Int("inboundID", inboundID),
			zap.String("clientID", clientID),
			zap.Error(err))
	}

	// Send webhook notification (async)
	if s.webhook != nil {
		eventID, _ := utils.GenerateUUID()
		s.webhook.SendAsync(Event{
			EventID:           "evt-" + eventID,
			Event:             webhook.EventSubscriptionExpired,
			UserID:            clientID,
			Email:             username,
			SubscriptionID: subscriptionID,
			Plan:              plan,
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

	// Store data before deletion
	clientID := sub.ClientID
	inboundID := sub.InboundID
	username := sub.Username
	subscriptionID := sub.SubscriptionID
	plan := sub.Plan

	// Delete from database first — same rationale as Delete():
	// DB-first avoids orphaned DB records when XUI deletion succeeds
	// but DB deletion fails.
	deleted, err := s.db.DeleteSubscriptionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("db delete: %w", err)
	}

	// Best-effort XUI cleanup
	if err := s.xui.DeleteClient(ctx, inboundID, clientID); err != nil {
		_ = inboundID // captured for potential future cleanup job
	}

	// Send webhook notification (async)
	if s.webhook != nil {
		eventID, _ := utils.GenerateUUID()
		s.webhook.SendAsync(Event{
			EventID:           "evt-" + eventID,
			Event:             webhook.EventSubscriptionExpired,
			UserID:            clientID,
			Email:             username,
			SubscriptionID: subscriptionID,
			Plan:              plan,
		})
	}

	return deleted, nil
}

type TrafficInfo struct {
	UsedGB              float64
	LimitGB             int
	Percentage          float64
	ProgressBar         string
	DaysUntilReset      int
	ResetInfo           string
	CreatedAtFormatted  string
	ExpiryTimeFormatted string
}

func (s *SubscriptionService) GetWithTraffic(ctx context.Context, telegramID int64) (*database.Subscription, *TrafficInfo, error) {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, nil, err
	}

	// Get traffic from XUI
	traffic, err := s.xui.GetClientTraffic(ctx, sub.Username)
	if err != nil {
		//nolint:nilerr // Intentionally return zero traffic when XUI fails - better UX than error
		// Return subscription with zero traffic instead of failing
		return sub, &TrafficInfo{
			UsedGB:  0,
			LimitGB: s.cfg.TrafficLimitGB,
		}, nil
	}

	usedGB := float64(traffic.Up+traffic.Down) / 1024 / 1024 / 1024
	percentage := 0.0
	limitGB := float64(s.cfg.TrafficLimitGB)
	if limitGB > 0 {
		percentage = (usedGB / limitGB) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	// Progress bar
	progressBar := utils.GenerateProgressBar(usedGB, limitGB)

	// Calculate reset time
	var resetTime time.Time
	if sub.ExpiryTime.IsZero() {
		resetTime = sub.CreatedAt.AddDate(0, 0, config.SubscriptionResetDay)
	} else {
		resetTime = sub.ExpiryTime
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
		UsedGB:              usedGB,
		LimitGB:             s.cfg.TrafficLimitGB,
		Percentage:          percentage,
		ProgressBar:         progressBar,
		DaysUntilReset:      daysUntilReset,
		ResetInfo:           resetInfo,
		CreatedAtFormatted:  utils.FormatDateRu(sub.CreatedAt),
		ExpiryTimeFormatted: utils.FormatDateRu(sub.ExpiryTime),
	}, nil
}

// daysUntilReset calculates the number of days until the next traffic reset.

type TrialCreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
	SubID           string
	ClientID        string
}

func (s *SubscriptionService) CreateTrial(ctx context.Context, inviteCode string) (*TrialCreateResult, error) {
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}
	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}

	trafficBytes := calcTrialTraffic(s.cfg.TrialDurationHours)
	expiryTime := time.Now().Add(time.Duration(s.cfg.TrialDurationHours) * time.Hour)

	_, err = s.xui.AddClientWithID(ctx, s.cfg.XUIInboundID, "trial_"+subID, clientID, subID, trafficBytes, expiryTime, 0)
	if err != nil {
		return nil, fmt.Errorf("xui add client: %w", err)
	}

	subURL := s.xui.GetSubscriptionLink(s.xui.GetExternalURL(s.cfg.XUIHost), subID, s.cfg.XUISubPath)

	sub, err := s.db.CreateTrialSubscription(ctx, inviteCode, subID, clientID, s.cfg.XUIInboundID, trafficBytes, expiryTime, subURL)
	if err != nil {
		if rollbackErr := s.xui.DeleteClient(ctx, s.cfg.XUIInboundID, clientID); rollbackErr != nil {
			return nil, fmt.Errorf("create trial subscription: %w (rollback failed: %w)", err, rollbackErr)
		}
		return nil, fmt.Errorf("create trial subscription: %w", err)
	}

	return &TrialCreateResult{
		Subscription:    sub,
		SubscriptionURL: subURL,
		SubID:           subID,
		ClientID:        clientID,
	}, nil
}

// calcTrialTraffic calculates trial traffic allocation based on trial duration.
// Formula: trialHours * 1GiB / 12, where 12 = (24*365)/(30*24) = hours in year / hours in month.
// This gives a proportional share of monthly traffic (100 GiB). Minimum 1 GiB.
func calcTrialTraffic(trialHours int) int64 {
	const (
		gib        = 1024 * 1024 * 1024
		minTraffic = gib
		// hoursInYear / hoursInMonth ≈ 12.17, integer division gives 12
		trafficDivisor = (24 * 365) / (30 * 24)
	)

	trafficBytes := int64(trialHours) * gib / trafficDivisor
	if trafficBytes < minTraffic {
		trafficBytes = minTraffic
	}
	return trafficBytes
}
