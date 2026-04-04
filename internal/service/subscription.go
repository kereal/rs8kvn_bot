package service

import (
	"context"
	"fmt"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/xui"

	"rs8kvn_bot/internal/utils"
)

type SubscriptionService struct {
	db  interfaces.DatabaseService
	xui interfaces.XUIClient
	cfg *config.Config
}

type CreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
}

func NewSubscriptionService(db interfaces.DatabaseService, xui interfaces.XUIClient, cfg *config.Config) *SubscriptionService {
	return &SubscriptionService{
		db:  db,
		xui: xui,
		cfg: cfg,
	}
}

func (s *SubscriptionService) Create(ctx context.Context, chatID int64, username string) (*CreateResult, error) {
	trafficBytes := int64(s.cfg.TrafficLimitGB) * 1024 * 1024 * 1024

	// Ensure XUI session is valid before making API calls
	if err := s.xui.Login(ctx); err != nil {
		return nil, fmt.Errorf("xui login: %w", err)
	}

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
		ExpiryTime:      time.Time{},
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

	if err := s.xui.DeleteClient(ctx, s.cfg.XUIInboundID, sub.ClientID); err != nil {
		return fmt.Errorf("xui delete: %w", err)
	}

	if err := s.db.DeleteSubscription(ctx, telegramID); err != nil {
		return fmt.Errorf("db delete: %w", err)
	}

	return nil
}

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

	if err := s.xui.Login(ctx); err != nil {
		return nil, fmt.Errorf("xui login: %w", err)
	}

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
