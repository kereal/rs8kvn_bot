// Package interfaces defines narrow dependency-injection contracts for the application.
package interfaces

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

var _ DatabaseService = (*database.Service)(nil)
var _ XUIClient = (*xui.Client)(nil)
var _ WebRepository = (*database.Service)(nil)

// SubscriptionNodeCRUD provides basic CRUD operations for subscription nodes.
type SubscriptionNodeCRUD interface {
	GetBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
	CreateSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error
	UpsertSubscriptionNode(ctx context.Context, sn *database.SubscriptionNode) error
	DeleteSubscriptionNode(ctx context.Context, subID, nodeID uint) error
	DeleteSubscriptionNodesBySubscriptionID(ctx context.Context, subID uint) error
	MarkActiveNodesPendingUpdate(ctx context.Context, subID uint, targetNodeIDs []uint) error
}

// SubscriptionNodeStatus manages sync status and retry logic for subscription nodes.
type SubscriptionNodeStatus interface {
	UpdateSubscriptionNodeStatus(ctx context.Context, subID, nodeID uint, status database.SyncStatus) error
	UpdateRetry(ctx context.Context, subID, nodeID uint, retryCount int, retryAt *time.Time, lastErr *string) error
}

// SubscriptionNodeQueries retrieves subscription nodes by various criteria.
type SubscriptionNodeQueries interface {
	GetPendingSync(ctx context.Context) ([]database.SubscriptionNode, error)
	GetPendingBySubscriptionID(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error)
}

// SubscriptionNodeRepository combines all subscription node interfaces.
type SubscriptionNodeRepository interface {
	SubscriptionNodeCRUD
	SubscriptionNodeStatus
	SubscriptionNodeQueries
}

// SubscriptionCRUD provides basic CRUD operations for subscriptions.
// DeleteSubscriptionByID is the only physical deletion (admin /del); all other
// lifecycle changes are status updates via UpdateSubscription.
type SubscriptionCRUD interface {
	CreateSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) error
	UpdateSubscription(ctx context.Context, sub *database.Subscription) error
	DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error)
}

// SubscriptionQueries retrieves subscriptions by various criteria.
type SubscriptionQueries interface {
	GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error)
	GetAnyByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error)
	GetByID(ctx context.Context, id uint) (*database.Subscription, error)
	GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error)
	GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error)
	GetWithPlanAndNodes(ctx context.Context, subscriptionID string) (*database.SubscriptionFull, error)
}

// SubscriptionCounts retrieves subscription statistics.
type SubscriptionCounts interface {
	CountAllSubscriptions(ctx context.Context) (int64, error)
	CountActiveSubscriptions(ctx context.Context) (int64, error)
	CountTrialSubscriptions(ctx context.Context) (int64, error)
	GetTelegramIDByUsername(ctx context.Context, username string) (int64, error)
	GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error)
	GetTotalTelegramIDCount(ctx context.Context) (int64, error)
	GetFilteredTelegramIDCount(ctx context.Context, filter database.BroadcastFilter) (int64, error)
}

// SubscriptionStatus manages subscription lifecycle operations.
type SubscriptionStatus interface {
	GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error)
	ExpireSubscription(ctx context.Context, id uint, freePlanID uint) error
	GetExpiredPaidSubscriptions(ctx context.Context, now time.Time) ([]database.Subscription, error)
}

// SubscriptionJSONFields updates JSON-encoded fields on subscriptions.
type SubscriptionJSONFields interface {
	UpdateDevices(ctx context.Context, id uint, devicesJSON string) error
	UpdateIPs(ctx context.Context, id uint, ipsJSON string) error
}

// SubscriptionReminderRepository provides the atomic reminder workflow and expiry query.
type SubscriptionReminderRepository interface {
	GetSubscriptionsExpiringInRange(ctx context.Context, from, to time.Time) ([]database.Subscription, error)
	ClaimReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error)
	ReleaseReminder(ctx context.Context, id uint, bit int, expiresAt time.Time) error
}

// SubscriptionReminderWindow identifies one expiry reminder touch.
type SubscriptionReminderWindow struct {
	Name     string
	Bit      int
	LeadTime time.Duration
}

// SubscriptionReminderService is the narrow contract the reminder worker needs
// from the subscription service.
type SubscriptionReminderService interface {
	SendExpiryReminder(ctx context.Context, sub *database.Subscription, window SubscriptionReminderWindow) error
}

// SubscriptionLastRequest updates the last_request timestamp on subscriptions.
type SubscriptionLastRequest interface {
	UpdateLastRequest(ctx context.Context, subscriptionID string) error
}

// SubscriptionLookup provides methods for external subscription ID lookups.
type SubscriptionLookup interface {
	GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error)
}

// SubscriptionRepository combines all subscription interfaces.
type SubscriptionRepository interface {
	SubscriptionCRUD
	SubscriptionQueries
	SubscriptionCounts
	SubscriptionStatus
	SubscriptionJSONFields
	SubscriptionReminderRepository
	SubscriptionLastRequest
	SubscriptionLookup
}

// TrialRepository provides operations for trial subscriptions.
type TrialRepository interface {
	CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error)
	GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*database.Subscription, error)
	BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error)
	CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error)
	CreateTrialRequest(ctx context.Context, ip string) error
	CleanupExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error)
	ClaimExpiredTrials(ctx context.Context, hours int) ([]database.Subscription, error)
	DeleteClaimedTrial(ctx context.Context, id uint) error
}

// NodeRepository provides node and plan-node lookup operations.
type NodeRepository interface {
	ListNodes(ctx context.Context) ([]database.Node, error)
	GetNodesByPlanName(ctx context.Context, planName string) ([]database.Node, error)
	GetNodesByPlanID(ctx context.Context, planID uint) ([]database.Node, error)
	GetNodeByID(ctx context.Context, id uint) (*database.Node, error)
	ListEnabled(ctx context.Context) ([]database.Node, error)
}

// InviteRepository provides referral invite and referral-count operations.
type InviteRepository interface {
	GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error)
	GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*database.Invite, error)
	GetInviteByCode(ctx context.Context, code string) (*database.Invite, error)
	GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error)
	GetAllReferralCounts(ctx context.Context) (map[int64]int64, error)
}

// PlanRepository provides plan lookup operations.
type PlanRepository interface {
	GetPlanByName(ctx context.Context, name string) (*database.Plan, error)
	GetPlanByID(ctx context.Context, id uint) (*database.Plan, error)
}

// ProductRepository provides product listing, lookup, and guarded update operations.
type ProductRepository interface {
	ListActiveProducts(ctx context.Context) ([]database.Product, error)
	GetProductByID(ctx context.Context, id uint) (*database.Product, error)
	UpdateProductGuarded(ctx context.Context, product *database.Product) error
}

// OrderRepository provides order lifecycle and payment-intent operations.
type OrderRepository interface {
	GetOrderByID(ctx context.Context, id uint) (*database.Order, error)
	GetOrderByProviderPaymentID(ctx context.Context, provider string, providerPaymentID uuid.UUID) (*database.Order, error)
	FindPendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, now time.Time) (*database.Order, error)
	FindOrCreatePendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, amountCents int64, currency string, now time.Time) (*database.Order, error)
	MarkPaymentCreationUncertain(ctx context.Context, orderID uint, uncertain bool) (bool, error)
	SavePaymentDetails(ctx context.Context, orderID uint, providerPaymentID uuid.UUID, paymentURL string, paymentExpiresAt time.Time) error
	SaveOrderPaymentAmounts(ctx context.Context, orderID uint, callbackAmountCents int64, providerFeeCents *int64) error
	GetPaidOrdersWithoutProviderFee(ctx context.Context, limit int) ([]database.Order, error)
	GetPaidOrdersWithoutProviderFeeAfterID(ctx context.Context, afterID uint, limit int) ([]database.Order, error)
	ConfirmOrderPaidCAS(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *database.Subscription, product *database.Product, applyPlan database.ApplyPlanInTxFn, callbackAmountCents int64) (bool, error)
	CancelOrderCAS(ctx context.Context, provider string, providerPaymentID uuid.UUID, fromStatuses []database.OrderStatus) (bool, error)
	CancelPaidOrderAndDowngradeCAS(ctx context.Context, provider string, providerPaymentID uuid.UUID, now time.Time, freePlanID uint, applyPlan database.ChargebackPlanInTxFn) (*database.ChargebackResult, error)
}

// BroadcastRepository provides CRUD for broadcast cards
// (название, текст, статус, даты, счётчики, JSON-отчёт).
type BroadcastRepository interface {
	CreateBroadcast(ctx context.Context, b *database.Broadcast) error
	GetBroadcast(ctx context.Context, id uint) (*database.Broadcast, error)
	ListBroadcasts(ctx context.Context, limit int) ([]database.Broadcast, error)
	UpdateBroadcast(ctx context.Context, b *database.Broadcast) error
	SnapshotBroadcastRecipients(ctx context.Context, broadcastID uint, filter database.BroadcastFilter) (int64, error)
	ClaimBroadcast(ctx context.Context, id uint, now time.Time) (bool, error)
	CancelBroadcast(ctx context.Context, id uint, now time.Time) (bool, error)
	GetRunnableBroadcasts(ctx context.Context, now time.Time) ([]database.Broadcast, error)
	RecoverStaleBroadcastRecipients(ctx context.Context, broadcastID uint, before time.Time) error
	ClaimBroadcastRecipients(ctx context.Context, broadcastID uint, now time.Time, limit int) ([]database.BroadcastRecipient, error)
	FinishBroadcastRecipient(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, status database.BroadcastRecipientStatus, lastError string, now time.Time) error
	UpdateBroadcastRecipientProgress(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, nextChunk int, now time.Time) error
	ResetBroadcastFailedRecipients(ctx context.Context, id uint, now time.Time) error
	GetBroadcastRecipientsStats(ctx context.Context, broadcastID uint) (total, sent, blocked, unreachable, failed int64, report database.BroadcastDeliveryReport, err error)
}

// DatabaseService is the composed persistence contract used by application services.
type DatabaseService interface {
	SubscriptionNodeRepository
	SubscriptionRepository
	TrialRepository
	InviteRepository
	NodeRepository
	PlanRepository
	ProductRepository
	OrderRepository
	BroadcastRepository
	Ping(ctx context.Context) error
	Close() error
	GetPoolStats() (*database.PoolStats, error)
	Transaction(ctx context.Context, fn func(*gorm.DB) error) error
}

// XUIClientReader contains read-only panel operations.
type XUIClientReader interface {
	Ping(ctx context.Context) error
	GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error)
}

// XUIClientWriter contains panel mutation and lifecycle operations.
type XUIClientWriter interface {
	AddClient(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithID(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error)
	UpdateClient(ctx context.Context, req xui.ClientRequest) error
	DeleteClient(ctx context.Context, email string) error
	ResetTraffic(ctx context.Context, email string) error
	Close() error
}

// XUIClient combines panel read and write operations.
type XUIClient interface {
	XUIClientReader
	XUIClientWriter
}

// BotAPI is the narrow Telegram client contract required by handlers and services.
type BotAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}

// WebRepository is the composed DB seam for the web/HTTP server: subscription
// aggregation handoff (SubscriptionRepository) plus the invite/trial/plan reads
// its landing pages need. It deliberately drops Node/Order/SubscriptionNode/Product.
type WebRepository interface {
	SubscriptionRepository
	InviteRepository
	TrialRepository
	PlanRepository
}

// vpn.Client is defined in internal/vpn/client.go
