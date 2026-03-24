package interfaces

import (
	"context"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"
)

type DatabaseService interface {
	GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error)
	GetByID(ctx context.Context, id uint) (*database.Subscription, error)
	CreateSubscription(ctx context.Context, sub *database.Subscription) error
	UpdateSubscription(ctx context.Context, sub *database.Subscription) error
	DeleteSubscription(ctx context.Context, telegramID int64) error
	GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error)
	GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error)
	CountActiveSubscriptions(ctx context.Context) (int64, error)
	CountExpiredSubscriptions(ctx context.Context) (int64, error)
	GetAllTelegramIDs(ctx context.Context) ([]int64, error)
	GetTelegramIDByUsername(ctx context.Context, username string) (int64, error)
	DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error)
	Close() error
}

type XUIClient interface {
	Login(ctx context.Context) error
	AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	DeleteClient(ctx context.Context, inboundID int, clientID string) error
	GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error)
	GetSubscriptionLink(baseURL, subID, subPath string) string
	GetExternalURL(host string) string
}
