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

type SubscriptionRepository interface {
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
	GetSubscriptionBySubscriptionID(ctx context.Context, subscriptionID string) (*database.Subscription, error)
}

type TrialRepository interface {
	CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error)
	GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error)
	CreateTrialRequest(ctx context.Context, ip string) error
	CleanupExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error)
}

type SourceRepository interface {
	ListSources(ctx context.Context) ([]database.Source, error)
	GetSourcesByPlanName(ctx context.Context, planName string) ([]database.Source, error)
	IsSourcesEmpty(ctx context.Context) (bool, error)
	SeedDefaultSource(ctx context.Context, name, xuiHost, xuiAPIToken string, xuiInboundID int, subURL string) error
}

type InviteRepository interface {
	GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error)
	GetInviteByCode(ctx context.Context, code string) (*database.Invite, error)
	GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error)
	GetAllReferralCounts(ctx context.Context) (map[int64]int64, error)
}

type PlanRepository interface {
	GetPlanByName(ctx context.Context, name string) (*database.Plan, error)
}

type DatabaseService interface {
	SubscriptionRepository
	TrialRepository
	InviteRepository
	SourceRepository
	PlanRepository
	Ping(ctx context.Context) error
	Close() error
	GetPoolStats() (*database.PoolStats, error)
}

type XUIClient interface {
	Ping(ctx context.Context) error
	AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error)
	UpdateClient(ctx context.Context, inboundID int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error
	DeleteClient(ctx context.Context, email string) error
	GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error)
	Close() error
}

// BotAPI defines the interface for Telegram Bot API operations
type BotAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}
