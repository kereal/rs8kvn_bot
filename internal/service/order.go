package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"gorm.io/gorm"

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
	ErrPaymentCreationUncertain = errors.New("payment creation requires manual reconciliation")
	ErrPaymentAlreadyInProgress = errors.New("payment is already in progress")
	ErrPaymentSyncNotReady      = errors.New("payment synchronization is not ready")
)

// OrderService handles order creation and activation flows.
type OrderService struct {
	db          interfaces.DatabaseService
	subSvc      *SubscriptionService
	syncSvc     *SyncService
	payment     PaymentProvider
	botUsername string
	cfg         *config.Config
	adminBot    interfaces.BotAPI
	paymentMu   sync.Mutex
}

// PaymentInfo contains payment details for an order.
type PaymentInfo struct {
	URL       string
	Provider  string
	PaymentID uuid.UUID
	ExpiresAt time.Time
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

// SetAdminBot wires the Telegram client used for best-effort operational alerts.
func (o *OrderService) SetAdminBot(bot interfaces.BotAPI) { o.adminBot = bot }

func (o *OrderService) notifyAdmin(ctx context.Context, text string) {
	if o.adminBot == nil || o.cfg == nil || o.cfg.TelegramAdminID <= 0 {
		return
	}
	if _, err := o.adminBot.Send(tgbotapi.NewMessage(o.cfg.TelegramAdminID, text)); err != nil {
		logger.Warn("failed to notify admin about payment event", zap.Error(err))
	}
}

func (o *OrderService) RequestPayment(ctx context.Context, telegramID int64, username string, product *database.Product) (*PaymentInfo, *database.Order, error) {
	if o.payment == nil {
		return nil, nil, ErrPaymentDisabled
	}
	if telegramID <= 0 || product == nil || product.ID == 0 {
		return nil, nil, errors.New("invalid paid product request")
	}

	canonical, err := o.db.GetProductByID(ctx, product.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("load canonical product: %w", err)
	}
	if canonical == nil {
		return nil, nil, errors.New("load canonical product: product is nil")
	}
	now := time.Now().UTC()
	// Resolve the subscription before looking up the intent. Intents belong to
	// a concrete subscription, not merely to a Telegram ID; this matters when
	// historical data contains more than one subscription for one user.
	var sub *database.Subscription
	sub, err = o.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		if !errors.Is(err, database.ErrSubscriptionNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("load payment subscription: %w", err)
		}
		if !canonical.IsActive || canonical.PriceCents <= 0 {
			return nil, nil, errors.New("invalid paid product request")
		}
		plan, planErr := o.db.GetPlanByID(ctx, canonical.PlanID)
		if planErr != nil {
			return nil, nil, fmt.Errorf("load product plan: %w", planErr)
		}
		if plan == nil || !plan.IsActive {
			return nil, nil, errors.New("product plan is inactive")
		}
		if o.subSvc == nil {
			return nil, nil, errors.New("subscription service is not configured")
		}
		sub, err = o.subSvc.GetOrCreateSubscription(ctx, telegramID, username, "")
		if err != nil {
			return nil, nil, fmt.Errorf("get or create subscription: %w", err)
		}
	}

	// Return a still-valid link before validating the current product flags: an
	// operator may deactivate a product while an already-created payment link is
	// still usable. Expiry is terminalized by the repository lookup.
	order, err := o.db.FindPendingPaymentOrder(ctx, sub.ID, canonical.ID, now)
	if err != nil {
		return nil, nil, fmt.Errorf("find pending payment order: %w", err)
	}
	if order != nil && order.Status == database.OrderStatusExpired {
		order = nil
	}
	if order != nil && order.Status != database.OrderStatusPending {
		return nil, order, ErrPaymentAlreadyInProgress
	}
	if order == nil {
		if !canonical.IsActive || canonical.PriceCents <= 0 {
			return nil, nil, errors.New("invalid paid product request")
		}
		plan, planErr := o.db.GetPlanByID(ctx, canonical.PlanID)
		if planErr != nil {
			return nil, nil, fmt.Errorf("load product plan: %w", planErr)
		}
		if plan == nil || !plan.IsActive {
			return nil, nil, errors.New("product plan is inactive")
		}
		// The repository transaction expires an old link and creates/reuses the
		// single pending intent under the partial unique index. This is the only
		// creation path, so concurrent requests cannot create duplicate intents.
		order, err = o.db.FindOrCreatePendingPaymentOrder(ctx, sub.ID, canonical.ID, canonical.PriceCents, canonical.Currency, now)
		if err != nil {
			return nil, nil, fmt.Errorf("find or create pending payment order: %w", err)
		}
	}
	if order.PaymentCreationUncertain {
		logger.Warn("payment creation requires manual reconciliation", zap.Uint("order_id", order.ID), zap.Uint("product_id", canonical.ID))
		o.notifyAdmin(ctx, fmt.Sprintf("⚠️ Payment requires manual reconciliation\n\nOrder ID: %d\nTelegram ID: %d\nProduct ID: %d\nAmount: %d cents\nCurrency: %s\nReason: previous provider request has an uncertain outcome", order.ID, telegramID, canonical.ID, order.AmountCents, order.Currency))
		return nil, order, ErrPaymentCreationUncertain
	}
	if strings.TrimSpace(order.ProviderPaymentID) != "" && order.PaymentURL != "" && order.PaymentExpiresAt != nil && now.Before(*order.PaymentExpiresAt) {
		paymentID, parseErr := platega.ParseTransactionID(order.ProviderPaymentID)
		if parseErr != nil {
			o.notifyAdmin(ctx, fmt.Sprintf("🚨 Stored provider transaction ID is invalid\n\nOrder ID: %d\nProduct ID: %d\nProvider transaction ID: %s\nError: %v", order.ID, canonical.ID, order.ProviderPaymentID, parseErr))
			return nil, order, fmt.Errorf("parse stored payment ID: %w", parseErr)
		}
		return &PaymentInfo{URL: order.PaymentURL, Provider: "platega", PaymentID: paymentID, ExpiresAt: *order.PaymentExpiresAt}, order, nil
	}
	if strings.TrimSpace(order.ProviderPaymentID) != "" {
		return nil, order, ErrPaymentAlreadyInProgress
	}
	// Creating a new provider transaction is a new purchase attempt. Do not
	// start it for a product/plan that was deactivated after the pending intent
	// was created; only an already-saved, still-valid link may be returned above.
	if !canonical.IsActive || canonical.PriceCents <= 0 {
		return nil, order, errors.New("invalid paid product request")
	}
	plan, planErr := o.db.GetPlanByID(ctx, canonical.PlanID)
	if planErr != nil {
		return nil, order, fmt.Errorf("load product plan: %w", planErr)
	}
	if plan == nil || !plan.IsActive {
		return nil, order, errors.New("product plan is inactive")
	}
	claimed, err := o.db.MarkPaymentCreationUncertain(ctx, order.ID, true)
	if err != nil {
		return nil, order, fmt.Errorf("mark payment creation uncertain: %w", err)
	}
	if !claimed {
		latest, loadErr := o.db.GetOrderByID(ctx, order.ID)
		if loadErr != nil {
			return nil, order, fmt.Errorf("reload payment order after concurrent claim: %w", loadErr)
		}
		if latest.PaymentCreationUncertain {
			return nil, latest, ErrPaymentCreationUncertain
		}
		if strings.TrimSpace(latest.ProviderPaymentID) != "" && latest.PaymentURL != "" && latest.PaymentExpiresAt != nil && now.Before(*latest.PaymentExpiresAt) {
			paymentID, parseErr := platega.ParseTransactionID(latest.ProviderPaymentID)
			if parseErr != nil {
				return nil, latest, fmt.Errorf("parse stored payment ID: %w", parseErr)
			}
			return &PaymentInfo{URL: latest.PaymentURL, Provider: "platega", PaymentID: paymentID, ExpiresAt: *latest.PaymentExpiresAt}, latest, nil
		}
		return nil, latest, ErrPaymentAlreadyInProgress
	}
	base := "https://t.me/" + strings.TrimPrefix(o.botUsername, "@")
	response, err := o.payment.CreateTransaction(ctx, platega.CreateTransactionRequest{AmountCents: canonical.PriceCents, Currency: canonical.Currency, Description: canonical.Name, ReturnURL: base, FailedURL: base, Payload: fmt.Sprint(order.ID), UserID: fmt.Sprint(telegramID), UserName: username})
	if err != nil {
		if errors.Is(err, platega.ErrBadRequest) || errors.Is(err, platega.ErrAuth) {
			if _, clearErr := o.db.MarkPaymentCreationUncertain(ctx, order.ID, false); clearErr != nil {
				return nil, order, fmt.Errorf("clear payment creation uncertainty: %w", clearErr)
			}
		} else {
			o.notifyAdmin(ctx, fmt.Sprintf("🚨 Payment creation outcome is uncertain\n\nOrder ID: %d\nTelegram ID: %d\nProduct ID: %d\nAmount: %d cents\nCurrency: %s\nError: %v\nAction: find the provider transaction and attach/refund it manually", order.ID, telegramID, canonical.ID, order.AmountCents, order.Currency, err))
		}
		return nil, order, fmt.Errorf("create payment transaction: %w", err)
	}
	if response == nil {
		o.notifyAdmin(ctx, fmt.Sprintf("⚠️ Payment provider returned an empty response\n\nOrder ID: %d\nTelegram ID: %d\nProduct ID: %d\nAmount: %d cents\nCurrency: %s", order.ID, telegramID, canonical.ID, order.AmountCents, order.Currency))
		return nil, order, fmt.Errorf("%w: empty response", platega.ErrProvider)
	}
	transactionID, err := platega.ParseTransactionID(response.TransactionID)
	if err != nil {
		o.notifyAdmin(ctx, fmt.Sprintf("🚨 Payment provider returned an invalid transaction ID\n\nOrder ID: %d\nTelegram ID: %d\nProduct ID: %d\nTransaction ID: %s\nError: %v", order.ID, telegramID, canonical.ID, response.TransactionID, err))
		return nil, order, fmt.Errorf("%w: transactionId must be UUID v4: %v", platega.ErrProvider, err)
	}
	providerPaymentID := transactionID
	url := strings.TrimSpace(response.URL)
	if url == "" {
		url = strings.TrimSpace(response.Redirect)
	}
	if url == "" {
		err := fmt.Errorf("%w: response has no payment URL", platega.ErrProvider)
		o.notifyAdmin(ctx, fmt.Sprintf("🚨 Payment provider response is incomplete\n\nOrder ID: %d\nTelegram ID: %d\nProduct ID: %d\nProvider transaction ID: %s\nError: %v\nAction: verify the provider transaction manually", order.ID, telegramID, canonical.ID, response.TransactionID, err))
		return nil, order, err
	}
	expiresIn, err := platega.ParseExpiresIn(response.ExpiresIn)
	if err != nil {
		o.notifyAdmin(ctx, fmt.Sprintf("🚨 Payment provider response is incomplete\n\nOrder ID: %d\nTelegram ID: %d\nProduct ID: %d\nProvider transaction ID: %s\nError: %v\nAction: verify the provider transaction manually", order.ID, telegramID, canonical.ID, response.TransactionID, err))
		return nil, order, fmt.Errorf("parse payment expiry: %w", err)
	}
	expiresAt := time.Now().UTC().Add(expiresIn)
	if err := o.db.SavePaymentDetails(ctx, order.ID, providerPaymentID, url, expiresAt); err != nil {
		logger.Warn("provider transaction created but payment details were not saved; manual reconciliation required", zap.Uint("order_id", order.ID), zap.String("provider_payment_id", providerPaymentID.String()), zap.Error(err))
		o.notifyAdmin(ctx, fmt.Sprintf("🚨 Payment created at provider but details were not saved\n\nOrder ID: %d\nTelegram ID: %d\nProduct ID: %d\nAmount: %d cents\nCurrency: %s\nProvider transaction ID: %s\nPayment URL: %s\nError: %v", order.ID, telegramID, canonical.ID, order.AmountCents, order.Currency, providerPaymentID.String(), url, err))
		return nil, order, fmt.Errorf("save payment details: %w", err)
	}
	order.ProviderPaymentID, order.PaymentURL = providerPaymentID.String(), url
	order.PaymentExpiresAt, order.PaymentCreationUncertain = &expiresAt, false
	return &PaymentInfo{URL: url, Provider: "platega", PaymentID: providerPaymentID, ExpiresAt: expiresAt}, order, nil
}

func (o *OrderService) ConfirmPayment(ctx context.Context, providerPaymentID uuid.UUID, amount json.Number, currency string) (*PaymentConfirmation, error) {
	if o.payment == nil {
		return nil, ErrPaymentDisabled
	}
	o.paymentMu.Lock()
	defer o.paymentMu.Unlock()
	if providerPaymentID == uuid.Nil {
		return nil, errors.New("invalid provider payment UUID")
	}
	order, err := o.db.GetOrderByProviderPaymentID(ctx, "platega", providerPaymentID)
	if err != nil {
		if errors.Is(err, database.ErrOrderNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("payment callback for unknown order", zap.String("provider_payment_id", providerPaymentID.String()))
			return &PaymentConfirmation{Activated: false}, nil
		}
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
	if order.Status == database.OrderStatusPaid {
		return &PaymentConfirmation{Order: order}, nil
	}
	if order.Status == database.OrderStatusExpired || order.Status == database.OrderStatusCanceled {
		logger.Warn("late payment callback ignored", zap.Uint("order_id", order.ID), zap.String("provider_payment_id", providerPaymentID.String()), zap.String("order_status", string(order.Status)))
		telegramID := int64(0)
		if sub, subErr := o.db.GetByID(ctx, order.SubscriptionID); subErr == nil && sub != nil {
			telegramID = sub.TelegramID
		}
		o.notifyAdmin(ctx, fmt.Sprintf("🚨 Late confirmed payment requires manual review\n\nOrder ID: %d\nTelegram ID: %d\nSubscription DB ID: %d\nProduct ID: %d\nAmount: %d cents\nCurrency: %s\nProvider transaction ID: %s\nOrder status: %s\nAction: verify payment and refund or activate manually", order.ID, telegramID, order.SubscriptionID, order.ProductID, order.AmountCents, order.Currency, providerPaymentID, order.Status))
		return &PaymentConfirmation{Order: order}, nil
	}
	if order.Status != database.OrderStatusPending {
		return nil, ErrInvalidPaymentTransition
	}
	if o.syncSvc == nil {
		return nil, ErrPaymentSyncNotReady
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
	var applyPlan database.ApplyPlanInTxFn
	if o.syncSvc != nil {
		applyPlan = o.syncSvc.ApplyPlanToSubscriptionInTx
	}
	activated, err := o.db.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, newExpiry, product, applyPlan)
	if err != nil {
		return nil, err
	}
	if activated {
		sub.PlanID = product.PlanID
		sub.Status = string(database.SubscriptionStatusActive)
		sub.ProductID = &product.ID
		if sub.ExpiresAt != nil {
			newExpiry = *sub.ExpiresAt
		} else {
			sub.ExpiresAt = &newExpiry
		}
	}
	if activated && o.syncSvc != nil {
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		if err := o.syncSvc.SyncSubscription(syncCtx, sub.ID); err != nil {
			logger.Warn("payment post-commit sync failed", zap.Uint("subscription_id", sub.ID), zap.Error(err))
		}
	}
	if activated {
		order.Status, order.PaidAt, order.ActivatedAt, order.ExpiresAt = database.OrderStatusPaid, &now, &now, &newExpiry
	}
	return &PaymentConfirmation{Order: order, Activated: activated}, nil
}

// CancelPaymentByProvider applies provider cancellation/chargeback idempotently.	// It returns the transitioned order and wasPaid=true when a previously-paid
// order receives a CHARGEBACKED status. The webhook deliberately does not
// call an automatic subscription downgrade; that status requires manual review.
// Returns (nil, false, nil) for an idempotent no-op.
func (o *OrderService) CancelPaymentByProvider(ctx context.Context, providerPaymentID uuid.UUID, status string, amount json.Number, currency string) (*database.Order, bool, error) {
	if o.payment == nil {
		return nil, false, ErrPaymentDisabled
	}
	if providerPaymentID == uuid.Nil {
		return nil, false, errors.New("invalid provider payment UUID")
	}
	o.paymentMu.Lock()
	defer o.paymentMu.Unlock()

	order, err := o.db.GetOrderByProviderPaymentID(ctx, "platega", providerPaymentID)
	if err != nil {
		if errors.Is(err, database.ErrOrderNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("payment cancellation callback for unknown order", zap.String("provider_payment_id", providerPaymentID.String()), zap.String("status", status))
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("find payment order for cancellation: %w", err)
	}
	if order.Currency != currency {
		return nil, false, ErrCurrencyMismatch
	}
	cents, err := platega.ParseCallbackAmount(amount)
	if err != nil {
		return nil, false, fmt.Errorf("parse cancellation amount: %w", err)
	}
	if cents != order.AmountCents {
		return nil, false, ErrAmountMismatch
	}

	if status != "CANCELED" && status != "CHARGEBACKED" {
		return nil, false, ErrInvalidPaymentTransition
	}
	isChargeback := status == "CHARGEBACKED"
	from := []database.OrderStatus{database.OrderStatusPending}
	if isChargeback {
		from = append(from, database.OrderStatusPaid)
	}
	transitioned, err := o.db.CancelOrderCAS(ctx, "platega", providerPaymentID, from)
	if err != nil {
		return nil, false, fmt.Errorf("cancel payment: %w", err)
	}
	if !transitioned {
		logger.Warn("payment cancellation callback was a no-op", zap.String("provider_payment_id", providerPaymentID.String()), zap.String("status", status))
		return nil, false, nil
	}
	order, err = o.db.GetOrderByProviderPaymentID(ctx, "platega", providerPaymentID)
	if err != nil {
		return nil, false, fmt.Errorf("load canceled order: %w", err)
	}
	if isChargeback {
		telegramID := int64(0)
		if sub, subErr := o.db.GetByID(ctx, order.SubscriptionID); subErr == nil && sub != nil {
			telegramID = sub.TelegramID
		}
		o.notifyAdmin(ctx, fmt.Sprintf("🚨 Chargeback requires manual review\n\nOrder ID: %d\nTelegram ID: %d\nSubscription DB ID: %d\nProduct ID: %d\nAmount: %d cents\nCurrency: %s\nProvider transaction ID: %s\nAction: verify access revocation and refund/manual reconciliation", order.ID, telegramID, order.SubscriptionID, order.ProductID, order.AmountCents, order.Currency, providerPaymentID))
	}
	return order, isChargeback, nil
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
	if sub.TelegramID <= 0 {
		logger.Warn("paid subscription has invalid telegram id; skipping notification",
			zap.Uint("order_id", order.ID),
			zap.Uint("subscription_id", order.SubscriptionID),
			zap.Int64("telegram_id", sub.TelegramID))
		return 0, "", nil
	}
	_, traffic, err := o.subSvc.GetWithTraffic(ctx, sub.TelegramID)
	if err != nil {
		return 0, "", fmt.Errorf("load paid subscription traffic: %w", err)
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
