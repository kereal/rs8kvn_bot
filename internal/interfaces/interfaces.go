package interfaces

import (
	"context"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

type Logger interface {
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
	Debug(msg string, fields ...zap.Field)
	Fatal(msg string, fields ...zap.Field)
}

type DatabaseService interface {
	Ping(ctx context.Context) error
	GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error)
	GetByID(ctx context.Context, id uint) (*database.Subscription, error)
	CreateSubscription(ctx context.Context, sub *database.Subscription) error
	UpdateSubscription(ctx context.Context, sub *database.Subscription) error
	DeleteSubscription(ctx context.Context, telegramID int64) error
	GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error)
	GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error)
	CountAllSubscriptions(ctx context.Context) (int64, error)
	CountActiveSubscriptions(ctx context.Context) (int64, error)
	CountExpiredSubscriptions(ctx context.Context) (int64, error)
	GetAllTelegramIDs(ctx context.Context) ([]int64, error)
	GetTelegramIDByUsername(ctx context.Context, username string) (int64, error)
	DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error)
	GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCount(ctx context.Context) (int64, error)
	GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetInviteByCode(ctx context.Context, code string) (*database.Invite, error)
	CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error)
	GetSubscriptionBySubscriptionID(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error)
	CreateTrialRequest(ctx context.Context, ip string) error
	CleanupExpiredTrials(ctx context.Context, hours int, xuiClient interface {
		DeleteClient(ctx context.Context, inboundID int, clientID string) error
	}, inboundID int) (int64, error)
	Close() error
}

type XUIClient interface {
	Ping(ctx context.Context) error
	Login(ctx context.Context) error
	AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error)
	UpdateClient(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error
	DeleteClient(ctx context.Context, inboundID int, clientID string) error
	GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error)
	GetSubscriptionLink(baseURL, subID, subPath string) string
	GetExternalURL(host string) string
}

// BotAPI defines the interface for Telegram Bot API operations
type BotAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}
