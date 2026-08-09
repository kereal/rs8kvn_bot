package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"

	"go.uber.org/zap"
)

// PaymentProvider is the minimal outbound payment contract consumed by OrderService.
type PaymentProvider interface {
	CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error)
}

var (
	ErrPaymentDisabled          = errors.New("payment is disabled")
	ErrAmountMismatch           = errors.New("payment amount mismatch")
	ErrCurrencyMismatch         = errors.New("payment currency mismatch")
	ErrInvalidPaymentTransition = errors.New("invalid payment transition")
)

// OrderService handles order creation and activation flows.
type OrderService struct {
	db          interfaces.DatabaseService
	subSvc      *SubscriptionService
	syncSvc     *SyncService
	payment     PaymentProvider
	botUsername string
	cfg         *config.Config
}

// PaymentInfo contains payment details for an order.
type PaymentInfo struct {
	URL       string
	Provider  string
	PaymentID string
}

type PaymentConfirmation struct {
	Order     *database.Order
	Activated bool
}

// NewOrderService keeps the existing three-argument call contract; optional arguments are payment provider and bot username.
func NewOrderService(db interfaces.DatabaseService, subSvc *SubscriptionService, syncSvc *SyncService, options ...interface{}) *OrderService {
	o := &OrderService{db: db, subSvc: subSvc, syncSvc: syncSvc}
	if len(options) > 0 {
		o.payment, _ = options[0].(PaymentProvider)
	}
	if len(options) > 1 {
		o.botUsername, _ = options[1].(string)
	}
	if len(options) > 2 {
		o.cfg, _ = options[2].(*config.Config)
	}
	return o
}

// SetSyncService wires post-commit VPN synchronization after startup.
func (o *OrderService) SetSyncService(syncSvc *SyncService) { o.syncSvc = syncSvc }

// SetConfig wires URL presentation configuration.
func (o *OrderService) SetConfig(cfg *config.Config) { o.cfg = cfg }

func (o *OrderService) SetBotUsername(username string) { o.botUsername = strings.TrimSpace(username) }

func (o *OrderService) RequestPayment(ctx context.Context, telegramID int64, username string, product *database.Product) (*PaymentInfo, *database.Order, error) {
	if o.payment == nil {
		return nil, nil, ErrPaymentDisabled
	}
	if telegramID <= 0 || product == nil || !product.IsActive || product.PriceCents <= 0 {
		return nil, nil, errors.New("invalid paid product request")
	}
	sub, err := o.subSvc.GetOrCreateSubscription(ctx, telegramID, username, "")
	if err != nil {
		return nil, nil, fmt.Errorf("get or create subscription: %w", err)
	}
	order := &database.Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: database.OrderStatusPending, AmountCents: product.PriceCents, Currency: product.Currency, PaymentProvider: "platega"}
	if err := o.db.CreateOrder(ctx, order); err != nil {
		return nil, nil, err
	}
	base := "https://t.me/" + strings.TrimPrefix(o.botUsername, "@")
	response, err := o.payment.CreateTransaction(ctx, platega.CreateTransactionRequest{AmountCents: product.PriceCents, Currency: product.Currency, Description: product.Name, ReturnURL: base, FailedURL: base, Payload: fmt.Sprint(order.ID), UserID: fmt.Sprint(telegramID), UserName: username})
	if err != nil {
		return nil, nil, fmt.Errorf("create payment transaction: %w", err)
	}
	if response == nil || strings.TrimSpace(response.TransactionID) == "" {
		return nil, nil, errors.New("payment provider returned empty transaction id")
	}
	if err := o.db.UpdateOrderProviderPaymentID(ctx, order.ID, response.TransactionID); err != nil {
		return nil, nil, err
	}
	order.ProviderPaymentID = response.TransactionID
	url := response.URL
	if url == "" {
		url = response.Redirect
	}
	return &PaymentInfo{URL: url, Provider: "platega", PaymentID: response.TransactionID}, order, nil
}

func (o *OrderService) ConfirmPayment(ctx context.Context, providerPaymentID string, amount json.Number, currency string) (*PaymentConfirmation, error) {
	if o.payment == nil {
		return nil, ErrPaymentDisabled
	}
	order, err := o.db.GetOrderByProviderPaymentID(ctx, "platega", providerPaymentID)
	if err != nil {
		return nil, fmt.Errorf("find payment order: %w", err)
	}
	if order.Currency != currency {
		return nil, ErrCurrencyMismatch
	}
	cents, err := platega.ParseCallbackAmount(amount)
	if err != nil {
		return nil, fmt.Errorf("parse callback amount: %w", err)
	}
	if cents != order.AmountCents {
		return nil, ErrAmountMismatch
	}
	if order.Status != database.OrderStatusPending {
		if order.Status == database.OrderStatusPaid {
			return &PaymentConfirmation{Order: order}, nil
		}
		return nil, ErrInvalidPaymentTransition
	}
	product, err := o.db.GetProductByID(ctx, order.ProductID)
	if err != nil {
		return nil, fmt.Errorf("get ordered product: %w", err)
	}
	sub, err := o.db.GetByID(ctx, order.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("get ordered subscription: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	newExpiry := calculateProductExpiry(now, sub.PlanID, sub.ExpiresAt, product)
	activated, err := o.db.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, newExpiry, product)
	if err != nil {
		return nil, err
	}
	if activated && o.syncSvc != nil {
		if err := o.syncSvc.SyncSubscription(ctx, sub.ID); err != nil {
			logger.Warn("payment post-commit sync failed", zap.Uint("subscription_id", sub.ID), zap.Error(err))
		}
	}
	if activated {
		order.Status, order.PaidAt, order.ActivatedAt, order.ExpiresAt = database.OrderStatusPaid, &now, &now, &newExpiry
	}
	return &PaymentConfirmation{Order: order, Activated: activated}, nil
}

// CancelPaymentByProvider applies provider cancellation/chargeback idempotently.
func (o *OrderService) CancelPaymentByProvider(ctx context.Context, providerPaymentID, status string) error {
	if o.payment == nil {
		return ErrPaymentDisabled
	}
	from := []database.OrderStatus{database.OrderStatusPending}
	if strings.EqualFold(status, "CHARGEBACKED") {
		from = append(from, database.OrderStatusPaid)
	}
	_, err := o.db.CancelOrderCAS(ctx, "platega", providerPaymentID, from)
	if err != nil {
		return fmt.Errorf("cancel payment: %w", err)
	}
	return nil
}

// NotifyPaidUser builds the same subscription presentation used by the bot screen.
func (o *OrderService) NotifyPaidUser(ctx context.Context, order *database.Order) (int64, string, error) {
	if order == nil {
		return 0, "", errors.New("order is nil")
	}
	sub, err := o.db.GetByID(ctx, order.SubscriptionID)
	if err != nil {
		return 0, "", fmt.Errorf("load paid subscription: %w", err)
	}
	if sub == nil {
		return 0, "", errors.New("paid subscription is nil")
	}
	_, traffic, err := o.subSvc.GetWithTraffic(ctx, sub.TelegramID)
	if err != nil {
		return 0, "", fmt.Errorf("load paid subscription traffic: %w", err)
	}
	if sub.TelegramID <= 0 {
		return 0, "", fmt.Errorf("paid subscription has invalid telegram id")
	}
	trafficInfo := "неограничен"
	progress := ""
	if traffic.LimitGB > 0 {
		trafficInfo = fmt.Sprintf("%.2f из %d Гб (%.0f%%)", traffic.UsedGB, traffic.LimitGB, traffic.Percentage)
		progress = "\n" + traffic.ProgressBar
	}
	resetInfo := traffic.ResetInfo
	if resetInfo == "" {
		resetInfo = "нет"
	}
	subURL := ""
	if o.cfg != nil {
		subURL = o.cfg.SubURL(sub.SubscriptionID)
	}
	text := fmt.Sprintf("✅ *Оплата подтверждена!*\n\n💡 Тариф: *%s*\n📊 Трафик: %s%s\n\n📅 Создана: %s\n⏰ Истекает: %s\n🔄 Сброс: %s\n\n🔗 Ссылка\n`%s`", traffic.PlanName, trafficInfo, progress, traffic.CreatedAtFormatted, traffic.ExpiresAtFormatted, resetInfo, subURL)
	return sub.TelegramID, text, nil
}
