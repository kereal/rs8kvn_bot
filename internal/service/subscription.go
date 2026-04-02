package service

import (
	"context"
	"fmt"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"

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

	clientID := utils.GenerateUUID()
	subID := utils.GenerateSubID()

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
		if rollbackErr := s.xui.DeleteClient(ctx, s.cfg.XUIInboundID, client.ID); rollbackErr != nil {
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
