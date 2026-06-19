package interfaces

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var _ DatabaseService = (*database.Service)(nil)
var _ XUIClient = (*xui.Client)(nil)

type SubscriptionNodeRepository interface {
	GetBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetByNodeID(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error)
	GetPendingSync(ctx context.Context) ([]database.SubscriptionNode, error)
	GetPendingBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	GetPendingByNodeID(ctx context.Context, nodeID uint) ([]database.SubscriptionNode, error)
	CreateSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error
	UpdateSubscriptionNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error
	UpsertSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error
	DeleteSubscriptionNode(ctx context.Context, subID, nodeID uint) error
	UpdateRetry(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error
}

type SubscriptionRepository interface {
	GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error)
	GetByID(ctx context.Context, id uint) (*database.Subscription, error)
	CreateSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) error
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
	GetSubscriptionWithPlanAndNodes(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error)
	GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error)
	UpdateSubscriptionDevices(ctx context.Context, id uint, devicesJSON string) error
	UpdateSubscriptionIPs(ctx context.Context, id uint, ipsJSON string) error
	ExpireSubscription(ctx context.Context, id uint, freePlanID uint) error
	GetExpiredPaidSubscriptions(ctx context.Context) ([]database.Subscription, error)
}

type TrialRepository interface {
	CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error)
	GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error)
	CreateTrialRequest(ctx context.Context, ip string) error
	CleanupExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error)
}

type NodeRepository interface {
	ListNodes(ctx context.Context) ([]database.Node, error)
	GetNodesByPlanName(ctx context.Context, planName string) ([]database.Node, error)
	GetNodesByPlanID(ctx context.Context, planID uint) ([]database.Node, error)
	GetNodeByID(ctx context.Context, id uint) (*database.Node, error)
	ListEnabled(ctx context.Context) ([]database.Node, error)
	IsNodesEmpty(ctx context.Context) (bool, error)
	SeedDefaultNode(ctx context.Context, name, host, apiToken string, inboundIDs []int, subscriptionURL string) error
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
	GetPlanByID(ctx context.Context, id uint) (*database.Plan, error)
}

type ProductRepository interface {
	GetActiveByPlanID(ctx context.Context, planID uint) ([]database.Product, error)
	GetProductByID(ctx context.Context, id uint) (*database.Product, error)
}

type OrderRepository interface {
	CreateOrder(ctx context.Context, order *database.Order) error
	GetOrderByID(ctx context.Context, id uint) (*database.Order, error)
	GetOrdersBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.Order, error)
	UpdateOrderStatus(ctx context.Context, id uint, status database.OrderStatus) error
	UpdateOrderPaidStatus(ctx context.Context, id uint) error
	UpdateOrderActivatedAt(ctx context.Context, id uint, activatedAt, expiresAt time.Time) error
}

type DatabaseService interface {
	SubscriptionNodeRepository
	SubscriptionRepository
	TrialRepository
	InviteRepository
	NodeRepository
	PlanRepository
	ProductRepository
	OrderRepository
	Ping(ctx context.Context) error
	Close() error
	GetPoolStats() (*database.PoolStats, error)
}

type XUIClient interface {
	Ping(ctx context.Context) error
	AddClient(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithID(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error)
	UpdateClient(ctx context.Context, inboundIDs []int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error
	DeleteClient(ctx context.Context, email string) error
	GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error)
	Close() error
}

type BotAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}

type VPNClient interface {
	CreateSubscription(ctx context.Context, uuid, username string) error
	DeleteSubscription(ctx context.Context, uuid, username string) error
}
