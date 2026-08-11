// Package service contains subscription, payment, and synchronization business logic.
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
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"gorm.io/gorm"

	"go.uber.org/zap"
)

// PaymentProvider is the minimal outbound payment contract consumed by OrderService
// for creating a provider transaction.
type PaymentProvider interface {
	CreateTransaction(context.Context, platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error)
}

// paymentSyncTimeout bounds the best-effort post-commit VPN sync. It prevents a
// stuck node from keeping the webhook handler open indefinitely; the sync worker
// retries any remaining pending node operations later.
const paymentSyncTimeout = 20 * time.Second

// Sentinel errors returned for expected payment states and configuration failures.
// Callers should use errors.Is when they need to distinguish these cases.
var (
	ErrPaymentDisabled              = errors.New("payment is disabled")
	ErrAmountMismatch               = errors.New("payment amount mismatch")
	ErrCurrencyMismatch             = errors.New("payment currency mismatch")
	ErrInvalidPaymentTransition     = errors.New("invalid payment transition")
	ErrPaymentCreationUncertain     = errors.New("payment creation requires manual reconciliation")
	ErrPaymentAlreadyInProgress     = errors.New("payment is already in progress")
	ErrPaymentSyncNotReady          = errors.New("payment synchronization is not ready")
	ErrChargebackAtomicPathNotReady = errors.New("atomic chargeback path is not ready")
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

// PaymentConfirmation describes the result of a callback transition. Activated
// is true only for the callback that changed an order from pending to paid; repeat
// callbacks return the existing order with Activated=false.
type PaymentConfirmation struct {
	Order     *database.Order
	Activated bool
}

// PaymentIssue contains the operational context of a payment integration problem.
// Optional fields remain empty when the provider failed before an order existed.
type PaymentIssue struct {
	Event          string
	Reason         string
	Action         string
	OrderID        uint
	TelegramID     int64
	ProductID      uint
	ProductName    string
	SubscriptionID uint
	PlanID         uint
	AmountCents    int64
	Currency       string
	ProviderID     string
	PaymentURL     string
	CallbackStatus string
	Payload        string
	PaymentMethod  *int
}

// NotifyPaymentIssue logs a structured payment issue and sends the same context
// to the configured administrator. Sending the alert is best-effort, but a
// failure to send it is logged by notifyAdmin.
func (o *OrderService) NotifyPaymentIssue(ctx context.Context, issue PaymentIssue) {
	metrics.PaymentIssuesTotal.WithLabelValues(issue.Event).Inc()
	logger.Warn("payment integration issue",
		zap.String("event", issue.Event),
		zap.String("reason", issue.Reason),
		zap.String("action", issue.Action),
		zap.Uint("order_id", issue.OrderID),
		zap.Int64("telegram_id", issue.TelegramID),
		zap.Uint("product_id", issue.ProductID),
		zap.String("product_name", issue.ProductName),
		zap.Uint("subscription_id", issue.SubscriptionID),
		zap.Uint("plan_id", issue.PlanID),
		zap.Int64("amount_cents", issue.AmountCents),
		zap.String("currency", issue.Currency),
		zap.String("provider_payment_id", issue.ProviderID),
		zap.String("payment_url", issue.PaymentURL),
		zap.String("callback_status", issue.CallbackStatus),
		zap.String("payload", issue.Payload),
		zap.String("payment_method", paymentMethodLogValue(issue.PaymentMethod)),
	)
	o.notifyAdmin(ctx, formatPaymentIssue(issue))
}

// paymentMethodLogValue preserves the distinction between an omitted payment
// method and the valid numeric value zero in structured logs.
func paymentMethodLogValue(method *int) string {
	if method == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *method)
}

// recordPaymentAmount adds a successful monetary transition to the Prometheus
// counter. Currency is normalized because it is a low-cardinality label, while
// invalid or non-positive amounts are ignored rather than corrupting a counter.
func recordPaymentAmount(operation string, amountCents int64, currency string) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if operation == "" || amountCents <= 0 || currency == "" {
		return
	}
	metrics.PaymentAmountCentsTotal.WithLabelValues(operation, currency).Add(float64(amountCents))
}

// formatPaymentIssue renders a Telegram-safe operational alert. Provider-controlled
// fields are truncated both individually and as a whole to stay below Telegram's
// message limit while preserving valid UTF-8.
func formatPaymentIssue(issue PaymentIssue) string {
	method := "-"
	if issue.PaymentMethod != nil {
		method = fmt.Sprintf("%d", *issue.PaymentMethod)
	}
	event := issue.Event
	switch issue.Event {
	case "late_confirmed_callback":
		event = "Late confirmed payment"
	case "provider_create_uncertain":
		event = "Provider outcome is uncertain"
	case "chargeback":
		event = "Chargeback requires manual review"
	}
	message := fmt.Sprintf("🚨 Payment integration issue\n\nEvent: %s\nReason: %s\nAction: %s\n\nOrder ID: %d\nTelegram ID: %d\nProduct ID: %d\nProduct: %s\nSubscription DB ID: %d\nPlan ID: %d\nAmount: %d cents\nCurrency: %s\nProvider transaction ID: %s\nPayment URL: %s\nCallback status: %s\nPayload: %s\nPayment method: %s", truncatePaymentField(event), truncatePaymentField(issue.Reason), truncatePaymentField(issue.Action), issue.OrderID, issue.TelegramID, issue.ProductID, truncatePaymentField(issue.ProductName), issue.SubscriptionID, issue.PlanID, issue.AmountCents, truncatePaymentField(issue.Currency), truncatePaymentField(issue.ProviderID), truncatePaymentField(issue.PaymentURL), truncatePaymentField(issue.CallbackStatus), truncatePaymentField(issue.Payload), method)
	return truncatePaymentMessage(message)
}

const (
	// Keep individual untrusted fields bounded before assembling the alert.
	maxPaymentAlertFieldLength = 700
	// Leave headroom below Telegram's 4096-character message limit.
	maxPaymentAlertMessageLength = 3900
)

// truncatePaymentField bounds one provider-controlled field by Unicode code points.
func truncatePaymentField(value string) string {
	runes := []rune(value)
	if len(runes) <= maxPaymentAlertFieldLength {
		return value
	}
	return string(runes[:maxPaymentAlertFieldLength]) + "… [truncated]"
}

// truncatePaymentMessage applies the final Telegram message-size limit without
// splitting a UTF-8 sequence.
func truncatePaymentMessage(value string) string {
	runes := []rune(value)
	if len(runes) <= maxPaymentAlertMessageLength {
		return value
	}
	return string(runes[:maxPaymentAlertMessageLength]) + "… [truncated]"
}

// NewOrderService wires the order service dependencies explicitly.
func NewOrderService(db interfaces.DatabaseService, subSvc *SubscriptionService, syncSvc *SyncService, payment PaymentProvider, botUsername string, cfg *config.Config) *OrderService {
	return &OrderService{
		db:          db,
		subSvc:      subSvc,
		syncSvc:     syncSvc,
		payment:     payment,
		botUsername: botUsername,
		cfg:         cfg,
	}
}

// SetSyncService wires post-commit VPN synchronization after startup.
func (o *OrderService) SetSyncService(syncSvc *SyncService) { o.syncSvc = syncSvc }

// SetBotUsername sets the username used to build provider return and failure URLs.
func (o *OrderService) SetBotUsername(username string) { o.botUsername = strings.TrimSpace(username) }

// SetAdminBot wires the Telegram client used for best-effort operational alerts.
func (o *OrderService) SetAdminBot(bot interfaces.BotAPI) { o.adminBot = bot }

// notifyRequestIssue enriches an early RequestPayment failure with whichever
// product/order context is available and routes it through the common alert path.
func (o *OrderService) notifyRequestIssue(ctx context.Context, event, reason, action string, telegramID uint64, product *database.Product, order *database.Order) {
	issue := PaymentIssue{Event: event, Reason: reason, Action: action, TelegramID: int64(telegramID)}
	if product != nil {
		issue.ProductID = product.ID
		issue.ProductName = product.Name
		issue.AmountCents = product.PriceCents
		issue.Currency = product.Currency
		issue.PlanID = product.PlanID
	}
	if order != nil {
		issue.OrderID = order.ID
		issue.SubscriptionID = order.SubscriptionID
		issue.AmountCents = order.AmountCents
		issue.Currency = order.Currency
		issue.ProviderID = order.ProviderPaymentID
		issue.PaymentURL = order.PaymentURL
	}
	o.NotifyPaymentIssue(ctx, issue)
}

// notifyAdmin sends a best-effort Telegram alert. Payment processing never fails
// solely because the operational alert could not be delivered.
func (o *OrderService) notifyAdmin(ctx context.Context, text string) {
	if o.adminBot == nil || o.cfg == nil || o.cfg.TelegramAdminID <= 0 {
		return
	}
	if _, err := o.adminBot.Send(tgbotapi.NewMessage(o.cfg.TelegramAdminID, text)); err != nil {
		logger.Warn("failed to notify admin about payment event", zap.Error(err))
	}
}

// notifyAdminPaid sends a best-effort Markdown notification about a confirmed
// payment to the configured administrator: purchase/renewal marker, tariff,
// amount, buyer link and order context. A delivery failure never fails the
// payment flow and is only logged.
func (o *OrderService) notifyAdminPaid(ctx context.Context, sub *database.Subscription, order *database.Order, product *database.Product, isRenewal bool) {
	if o.adminBot == nil || o.cfg == nil || o.cfg.TelegramAdminID <= 0 {
		return
	}
	msg := tgbotapi.NewMessage(o.cfg.TelegramAdminID, formatAdminPaidAlert(sub, order, product, isRenewal))
	msg.ParseMode = "Markdown"
	if _, err := o.adminBot.Send(msg); err != nil {
		logger.Warn("failed to notify admin about confirmed payment",
			zap.Uint("order_id", order.ID),
			zap.Error(err))
	}
}

// formatAdminPaidAlert renders the admin notification for a confirmed payment.
// isRenewal distinguishes a renewal of an already-paid subscription from a
// first purchase. The message uses Telegram Markdown, so user-controlled fields
// (product name, username) are escaped; identifiers are safe numeric/alpha values.
func formatAdminPaidAlert(sub *database.Subscription, order *database.Order, product *database.Product, isRenewal bool) string {
	title := "🆕 *Покупка подтверждена*"
	if isRenewal {
		title = "🔄 *Продление подтверждено*"
	}
	productName := "—"
	if product != nil && strings.TrimSpace(product.Name) != "" {
		productName = utils.EscapeMarkdown(product.Name)
	}
	expiry := "—"
	if sub != nil && sub.ExpiresAt != nil {
		expiry = utils.FormatDateRu(*sub.ExpiresAt)
	}
	telegramID, subscriptionID, username := int64(0), uint(0), ""
	if sub != nil {
		telegramID, subscriptionID, username = sub.TelegramID, sub.ID, sub.Username
	}
	orderID, amountCents, currency, providerPaymentID := uint(0), int64(0), "", ""
	if order != nil {
		orderID, amountCents, currency, providerPaymentID = order.ID, order.AmountCents, order.Currency, order.ProviderPaymentID
	}
	return fmt.Sprintf(title+"\n\n"+
		"💎 Тариф: *%s*\n"+
		"💵 Сумма: *%s*\n"+
		"👤 Покупатель: %s\n"+
		"🆔 Telegram ID: `%d`\n"+
		"🔖 Платёж: `%s`\n"+
		"📋 Подписка: #%d\n"+
		"🧾 Заказ: #%d\n"+
		"📅 Действует до: %s",
		productName,
		formatMoneyCents(amountCents, currency),
		utils.FormatUserLink(username, telegramID),
		telegramID,
		providerPaymentID,
		subscriptionID,
		orderID,
		expiry)
}

// notifyAdminChargeback sends a best-effort Markdown notification about a
// provider chargeback on a paid order: tariff, amount, buyer link and whether
// the subscription was downgraded to the free plan. Delivery failure is logged
// and never affects the callback result.
func (o *OrderService) notifyAdminChargeback(ctx context.Context, sub *database.Subscription, order *database.Order, product *database.Product, downgraded bool) {
	if o.adminBot == nil || o.cfg == nil || o.cfg.TelegramAdminID <= 0 {
		return
	}
	msg := tgbotapi.NewMessage(o.cfg.TelegramAdminID, formatAdminChargebackAlert(sub, order, product, downgraded))
	msg.ParseMode = "Markdown"
	if _, err := o.adminBot.Send(msg); err != nil {
		logger.Warn("failed to notify admin about chargeback",
			zap.Uint("order_id", order.ID),
			zap.Error(err))
	}
}

// formatAdminChargebackAlert renders the admin notification for a chargeback on
// a paid order. The message uses Telegram Markdown, so user-controlled fields
// (product name, username) are escaped.
func formatAdminChargebackAlert(sub *database.Subscription, order *database.Order, product *database.Product, downgraded bool) string {
	productName := "—"
	if product != nil && strings.TrimSpace(product.Name) != "" {
		productName = utils.EscapeMarkdown(product.Name)
	}
	telegramID, subscriptionID, username := int64(0), uint(0), ""
	if sub != nil {
		telegramID, subscriptionID, username = sub.TelegramID, sub.ID, sub.Username
	}
	orderID, amountCents, currency, providerPaymentID := uint(0), int64(0), "", ""
	if order != nil {
		orderID, amountCents, currency, providerPaymentID = order.ID, order.AmountCents, order.Currency, order.ProviderPaymentID
	}
	access := "⬇️ Доступ: *понижен до бесплатного*"
	if !downgraded {
		access = "ℹ️ Доступ: *сохранён* (есть другой оплаченный заказ)"
	}
	return fmt.Sprintf("🚫 *Chargeback по платежу*\n\n"+
		"💎 Тариф: *%s*\n"+
		"💵 Сумма: *%s*\n"+
		"👤 Покупатель: %s\n"+
		"🆔 Telegram ID: `%d`\n"+
		"🔖 Платёж: `%s`\n"+
		"📋 Подписка: #%d\n"+
		"🧾 Заказ: #%d\n"+
		"%s",
		productName,
		formatMoneyCents(amountCents, currency),
		utils.FormatUserLink(username, telegramID),
		telegramID,
		providerPaymentID,
		subscriptionID,
		orderID,
		access)
}

// formatMoneyCents renders an amount in cents as a readable value with a
// thousands separator and currency symbol/code. Whole amounts drop the
// fractional part ("2 300 ₽"); fractional amounts keep two digits ("2 300,50 ₽").
func formatMoneyCents(amountCents int64, currency string) string {
	negative := amountCents < 0
	if negative {
		amountCents = -amountCents
	}
	value := fmt.Sprintf("%d", amountCents/100)
	// Insert a space as the thousands separator (Russian locale convention).
	grouped := ""
	for i := 0; i < len(value); i++ {
		if i > 0 && (len(value)-i)%3 == 0 {
			grouped += " "
		}
		grouped += string(value[i])
	}
	if kopeks := amountCents % 100; kopeks != 0 {
		grouped += fmt.Sprintf(",%02d", kopeks)
	}
	if negative {
		grouped = "-" + grouped
	}
	if strings.EqualFold(currency, "RUB") {
		return grouped + " ₽"
	}
	if symbol := strings.ToUpper(strings.TrimSpace(currency)); symbol != "" {
		return grouped + " " + symbol
	}
	return grouped
}

// RequestPayment validates the current product and plan, reuses a valid pending
// intent when possible, and otherwise claims one intent before contacting Platega.
// An error with an uncertain provider outcome leaves the intent marked uncertain
// so a duplicate charge is not accidentally created by a retry.
func (o *OrderService) RequestPayment(ctx context.Context, telegramID int64, username string, product *database.Product) (*PaymentInfo, *database.Order, error) {
	start := time.Now()
	info, order, err := o.requestPayment(ctx, telegramID, username, product)
	result := "success"
	if err != nil {
		result = "error"
	}
	metrics.PaymentOperationsTotal.WithLabelValues("request", result).Inc()
	metrics.PaymentOperationDuration.WithLabelValues("request").Observe(time.Since(start).Seconds())
	return info, order, err
}

func (o *OrderService) requestPayment(ctx context.Context, telegramID int64, username string, product *database.Product) (*PaymentInfo, *database.Order, error) {
	if o.payment == nil {
		return nil, nil, ErrPaymentDisabled
	}
	if telegramID <= 0 || product == nil || product.ID == 0 {
		return nil, nil, errors.New("invalid paid product request")
	}

	canonical, err := o.db.GetProductByID(ctx, product.ID)
	if err != nil {
		o.notifyRequestIssue(ctx, "load_product_failed", err.Error(), "retry after database recovery", uint64(telegramID), product, nil)
		return nil, nil, fmt.Errorf("load canonical product: %w", err)
	}
	if canonical == nil {
		err := errors.New("load canonical product: product is nil")
		o.notifyRequestIssue(ctx, "load_product_failed", err.Error(), "verify the product record", uint64(telegramID), product, nil)
		return nil, nil, err
	}
	now := time.Now().UTC()
	// Resolve the subscription before looking up the intent. Intents belong to
	// a concrete subscription, not merely to a Telegram ID; this matters when
	// historical data contains more than one subscription for one user.
	var sub *database.Subscription
	sub, err = o.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		if !errors.Is(err, database.ErrSubscriptionNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
			o.notifyRequestIssue(ctx, "load_subscription_failed", err.Error(), "retry after database recovery", uint64(telegramID), canonical, nil)
			return nil, nil, fmt.Errorf("load payment subscription: %w", err)
		}
		if !canonical.IsActive || canonical.PriceCents <= 0 {
			return nil, nil, errors.New("invalid paid product request")
		}
		plan, planErr := o.db.GetPlanByID(ctx, canonical.PlanID)
		if planErr != nil {
			o.notifyRequestIssue(ctx, "load_plan_failed", planErr.Error(), "retry after database recovery", uint64(telegramID), canonical, nil)
			return nil, nil, fmt.Errorf("load product plan: %w", planErr)
		}
		if plan == nil || !plan.IsActive {
			return nil, nil, errors.New("product plan is inactive")
		}
		if o.subSvc == nil {
			err := errors.New("subscription service is not configured")
			o.notifyRequestIssue(ctx, "subscription_service_not_ready", err.Error(), "wire SubscriptionService before enabling payments", uint64(telegramID), canonical, nil)
			return nil, nil, err
		}
		sub, err = o.subSvc.GetOrCreateSubscription(ctx, telegramID, username, "")
		if err != nil {
			o.notifyRequestIssue(ctx, "create_subscription_failed", err.Error(), "retry after subscription service recovery", uint64(telegramID), canonical, nil)
			return nil, nil, fmt.Errorf("get or create subscription: %w", err)
		}
	}

	// Return a still-valid link before validating the current product flags: an
	// operator may deactivate a product while an already-created payment link is
	// still usable. Expiry is terminalized by the repository lookup.
	order, err := o.db.FindPendingPaymentOrder(ctx, sub.ID, canonical.ID, now)
	if err != nil {
		o.notifyRequestIssue(ctx, "find_pending_order_failed", err.Error(), "retry after database recovery", uint64(telegramID), canonical, nil)
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
			o.notifyRequestIssue(ctx, "load_plan_failed", planErr.Error(), "retry after database recovery", uint64(telegramID), canonical, nil)
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
			o.notifyRequestIssue(ctx, "create_pending_order_failed", err.Error(), "retry after database recovery", uint64(telegramID), canonical, nil)
			return nil, nil, fmt.Errorf("find or create pending payment order: %w", err)
		}
	}
	if order.PaymentCreationUncertain {
		logger.Warn("payment creation requires manual reconciliation", zap.Uint("order_id", order.ID), zap.Uint("product_id", canonical.ID))
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "payment_creation_uncertain", Reason: "previous provider request has an uncertain outcome", Action: "reconcile provider transaction manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: order.ProviderPaymentID, PaymentURL: order.PaymentURL})
		return nil, order, ErrPaymentCreationUncertain
	}
	if strings.TrimSpace(order.ProviderPaymentID) != "" && order.PaymentURL != "" && order.PaymentExpiresAt != nil && now.Before(*order.PaymentExpiresAt) {
		paymentID, parseErr := platega.ParseTransactionID(order.ProviderPaymentID)
		if parseErr != nil {
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stored_provider_id_invalid", Reason: parseErr.Error(), Action: "reconcile order manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: order.ProviderPaymentID, PaymentURL: order.PaymentURL})
			return nil, order, fmt.Errorf("parse stored payment ID: %w", parseErr)
		}
		logger.Info("Payment link reused",
			zap.Uint("order_id", order.ID),
			zap.Uint("product_id", canonical.ID),
			zap.String("provider", "platega"),
			zap.String("provider_payment_id", paymentID.String()),
			zap.Int64("amount_cents", order.AmountCents),
			zap.String("currency", order.Currency))
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
		o.notifyRequestIssue(ctx, "load_plan_failed", planErr.Error(), "retry after database recovery", uint64(telegramID), canonical, order)
		return nil, order, fmt.Errorf("load product plan: %w", planErr)
	}
	if plan == nil || !plan.IsActive {
		return nil, order, errors.New("product plan is inactive")
	}
	claimed, err := o.db.MarkPaymentCreationUncertain(ctx, order.ID, true)
	if err != nil {
		o.notifyRequestIssue(ctx, "claim_payment_creation_failed", err.Error(), "retry after database recovery", uint64(telegramID), canonical, order)
		return nil, order, fmt.Errorf("mark payment creation uncertain: %w", err)
	}
	if !claimed {
		latest, loadErr := o.db.GetOrderByID(ctx, order.ID)
		if loadErr != nil {
			o.notifyRequestIssue(ctx, "reload_pending_order_failed", loadErr.Error(), "retry after database recovery", uint64(telegramID), canonical, order)
			return nil, order, fmt.Errorf("reload payment order after concurrent claim: %w", loadErr)
		}
		if latest.PaymentCreationUncertain {
			// The intent was claimed by a concurrent RequestPayment in this race:
			// that caller is already contacting the provider. This is not the
			// manual-reconciliation case (a stale uncertain flag would have been
			// caught by the earlier check), so report the payment as already in
			// progress instead of alarming the user.
			return nil, latest, ErrPaymentAlreadyInProgress
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
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "provider_create_rejected", Reason: err.Error(), Action: "inspect Platega credentials and request fields before retrying", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: order.ProviderPaymentID, PaymentURL: order.PaymentURL})
			if _, clearErr := o.db.MarkPaymentCreationUncertain(ctx, order.ID, false); clearErr != nil {
				o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "clear_payment_uncertainty_failed", Reason: clearErr.Error(), Action: "clear the pending intent flag manually before retrying", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: order.ProviderPaymentID, PaymentURL: order.PaymentURL})
				return nil, order, fmt.Errorf("clear payment creation uncertainty: %w", clearErr)
			}
		} else {
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "provider_create_uncertain", Reason: err.Error(), Action: "find the provider transaction and attach or refund it manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: order.ProviderPaymentID, PaymentURL: order.PaymentURL})
		}
		return nil, order, fmt.Errorf("create payment transaction: %w", err)
	}
	if response == nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "provider_empty_response", Reason: "provider returned an empty response", Action: "verify provider transaction manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency})
		return nil, order, fmt.Errorf("%w: empty response", platega.ErrProvider)
	}
	transactionID, err := platega.ParseTransactionID(response.TransactionID)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "provider_invalid_transaction_id", Reason: err.Error(), Action: "verify provider response manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: response.TransactionID})
		return nil, order, fmt.Errorf("%w: transactionId must be UUID v4: %v", platega.ErrProvider, err)
	}
	providerPaymentID := transactionID
	url := strings.TrimSpace(response.URL)
	if url == "" {
		url = strings.TrimSpace(response.Redirect)
	}
	if url == "" {
		err := fmt.Errorf("%w: response has no payment URL", platega.ErrProvider)
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "provider_incomplete_response", Reason: err.Error(), Action: "verify provider transaction manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: response.TransactionID, PaymentURL: url})
		return nil, order, err
	}
	expiresIn, err := platega.ParseExpiresIn(response.ExpiresIn)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "provider_incomplete_response", Reason: err.Error(), Action: "verify provider transaction manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: response.TransactionID, PaymentURL: url})
		return nil, order, fmt.Errorf("parse payment expiry: %w", err)
	}
	expiresAt := time.Now().UTC().Add(expiresIn)
	if err := o.db.SavePaymentDetails(ctx, order.ID, providerPaymentID, url, expiresAt); err != nil {
		logger.Warn("provider transaction created but payment details were not saved; manual reconciliation required", zap.Uint("order_id", order.ID), zap.String("provider_payment_id", providerPaymentID.String()), zap.Error(err))
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "payment_details_save_failed", Reason: err.Error(), Action: "attach or refund provider transaction manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), PaymentURL: url})
		return nil, order, fmt.Errorf("save payment details: %w", err)
	}
	order.ProviderPaymentID, order.PaymentURL = providerPaymentID.String(), url
	order.PaymentExpiresAt, order.PaymentCreationUncertain = &expiresAt, false
	logger.Info("Payment link created",
		zap.Uint("order_id", order.ID),
		zap.Uint("product_id", canonical.ID),
		zap.String("provider", "platega"),
		zap.String("provider_payment_id", providerPaymentID.String()),
		zap.Int64("amount_cents", order.AmountCents),
		zap.String("currency", order.Currency))
	return &PaymentInfo{URL: url, Provider: "platega", PaymentID: providerPaymentID, ExpiresAt: expiresAt}, order, nil
}

// ConfirmPayment validates a provider callback and atomically applies the paid
// transition plus subscription plan changes. Only the successful pending-to-paid
// CAS caller performs post-commit synchronization and reports Activated=true.
func (o *OrderService) ConfirmPayment(ctx context.Context, providerPaymentID uuid.UUID, amount json.Number, currency string) (*PaymentConfirmation, error) {
	start := time.Now()
	confirmation, err := o.confirmPayment(ctx, providerPaymentID, amount, currency)
	result := "success"
	if err != nil {
		result = "error"
	}
	metrics.PaymentOperationsTotal.WithLabelValues("confirm", result).Inc()
	metrics.PaymentOperationDuration.WithLabelValues("confirm").Observe(time.Since(start).Seconds())
	if err == nil && confirmation != nil && confirmation.Activated && confirmation.Order != nil {
		recordPaymentAmount("confirmed", confirmation.Order.AmountCents, confirmation.Order.Currency)
	}
	return confirmation, err
}

func (o *OrderService) confirmPayment(ctx context.Context, providerPaymentID uuid.UUID, amount json.Number, currency string) (*PaymentConfirmation, error) {
	if o.payment == nil {
		return nil, ErrPaymentDisabled
	}
	o.paymentMu.Lock()
	paymentLocked := true
	defer func() {
		if paymentLocked {
			o.paymentMu.Unlock()
		}
	}()
	if providerPaymentID == uuid.Nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "invalid_provider_id", Reason: "provider payment UUID is nil", Action: "reject callback and inspect provider payload", CallbackStatus: "CONFIRMED"})
		return nil, errors.New("invalid provider payment UUID")
	}
	order, err := o.db.GetOrderByProviderPaymentID(ctx, "platega", providerPaymentID)
	if err != nil {
		if errors.Is(err, database.ErrOrderNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("payment callback for unknown order", zap.String("provider_payment_id", providerPaymentID.String()))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "unknown_provider_id", Reason: "callback references an order that does not exist", Action: "verify the provider transaction and reconcile manually", ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
			return &PaymentConfirmation{Activated: false}, nil
		}
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_failed", Reason: err.Error(), Action: "retry callback after database recovery", ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
		return nil, fmt.Errorf("find payment order: %w", err)
	}
	if order.Currency != currency {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_currency_mismatch", Reason: fmt.Sprintf("callback currency %q does not match order currency %q", currency, order.Currency), Action: "reject callback and investigate provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
		return nil, ErrCurrencyMismatch
	}
	cents, err := platega.ParseCallbackAmount(amount)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_amount_invalid", Reason: err.Error(), Action: "reject callback and inspect provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED", Payload: amount.String()})
		return nil, fmt.Errorf("parse callback amount: %w", err)
	}
	if cents != order.AmountCents {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_amount_mismatch", Reason: fmt.Sprintf("callback amount %d cents does not match order amount %d cents", cents, order.AmountCents), Action: "reject callback and investigate provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: cents, Currency: currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
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
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "late_confirmed_callback", Reason: fmt.Sprintf("callback received for order status %s", order.Status), Action: "verify payment and refund or activate manually", OrderID: order.ID, TelegramID: telegramID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
		return &PaymentConfirmation{Order: order}, nil
	}
	if order.Status != database.OrderStatusPending {
		return nil, ErrInvalidPaymentTransition
	}
	if o.syncSvc == nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "payment_sync_not_ready", Reason: "post-commit synchronization service is not configured", Action: "wire SyncService before enabling payment callbacks", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
		return nil, ErrPaymentSyncNotReady
	}
	product, err := o.db.GetProductByID(ctx, order.ProductID)
	if err != nil {
		if errors.Is(err, database.ErrProductNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			// The product row was removed after the order was created (e.g. by a
			// direct SQL cleanup). Activation is impossible and the money was
			// taken, so acknowledge the callback (200) and alert for manual
			// refund/reconciliation instead of returning 5xx forever.
			logger.Warn("payment callback for deleted product", zap.Uint("order_id", order.ID), zap.Uint("product_id", order.ProductID), zap.String("provider_payment_id", providerPaymentID.String()))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_product_failed", Reason: "ordered product no longer exists", Action: "verify payment and refund or activate manually", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
			return &PaymentConfirmation{Activated: false}, nil
		}
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_product_failed", Reason: err.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
		return nil, fmt.Errorf("get ordered product: %w", err)
	}
	sub, err := o.db.GetByID(ctx, order.SubscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			// The subscription was deleted (or revoked and removed) after the
			// order was created. The money was taken, so acknowledge the callback
			// (200) and alert for manual refund/reconciliation instead of
			// returning 5xx forever while Platega retries.
			logger.Warn("payment callback for deleted subscription", zap.Uint("order_id", order.ID), zap.Uint("subscription_id", order.SubscriptionID), zap.String("provider_payment_id", providerPaymentID.String()))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_subscription_failed", Reason: "ordered subscription no longer exists", Action: "verify payment and refund or reconcile manually", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
			return &PaymentConfirmation{Activated: false}, nil
		}
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_subscription_failed", Reason: err.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
		return nil, fmt.Errorf("get ordered subscription: %w", err)
	}
	// A renewal extends an already-paid subscription; a first purchase activates
	// a free-plan one. Captured before CAS mutates sub in place.
	isRenewal := sub.PricePaidCents > 0 || sub.ProductID != nil
	now := time.Now().UTC()
	var applyPlan database.ApplyPlanInTxFn
	if o.syncSvc != nil {
		applyPlan = o.syncSvc.ApplyPlanToSubscriptionInTx
	}
	activated, err := o.db.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, applyPlan)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "confirm_payment_failed", Reason: err.Error(), Action: "retry callback; order must remain pending if DB setup rolled back", OrderID: order.ID, TelegramID: sub.TelegramID, ProductID: order.ProductID, ProductName: product.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: "CONFIRMED"})
		return nil, err
	}
	if activated {
		// The CAS already populated sub.ExpiresAt from the current subscription
		// state inside the transaction; mirror that value on the order snapshot.
		// The nil guard keeps the service robust if a future CAS/fake stops
		// mutating the caller-supplied subscription pointer.
		if sub.ExpiresAt != nil {
			newExpiry := *sub.ExpiresAt
			order.ExpiresAt = &newExpiry
		}
		order.Status, order.PaidAt, order.ActivatedAt = database.OrderStatusPaid, &now, &now
	}
	// The database transition and in-memory result are complete. Do not hold the
	// process-wide payment lock while contacting VPN nodes; that external call is
	// best-effort and may take up to paymentSyncTimeout.
	o.paymentMu.Unlock()
	paymentLocked = false
	if activated {
		o.notifyAdminPaid(ctx, sub, order, product, isRenewal)
	}
	if activated && o.syncSvc != nil {
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), paymentSyncTimeout)
		defer cancel()
		if err := o.syncSvc.SyncSubscription(syncCtx, sub.ID); err != nil {
			logger.Warn("payment post-commit sync failed", zap.Uint("subscription_id", sub.ID), zap.Error(err))
		}
	}
	return &PaymentConfirmation{Order: order, Activated: activated}, nil
}

// CancelPaymentByProvider validates a cancellation or chargeback callback and
// applies an idempotent order transition. wasPaid is true only when a
// previously-paid order is transitioned by a CHARGEBACKED callback — a chargeback
// on a still-pending order (money never collected) reports wasPaid=false.
// A chargeback on a paid order automatically downgrades the subscription to the
// free plan (best-effort, after the payment lock is released) unless another
// paid order for the same subscription still exists — in that case access is
// preserved for manual review. Returns (nil, false, nil) for an idempotent no-op.
func (o *OrderService) CancelPaymentByProvider(ctx context.Context, providerPaymentID uuid.UUID, status string, amount json.Number, currency string) (order *database.Order, wasPaid bool, err error) {
	start := time.Now()
	defer func() {
		result := "success"
		if err != nil {
			result = "error"
		}
		metrics.PaymentOperationsTotal.WithLabelValues("cancel", result).Inc()
		metrics.PaymentOperationDuration.WithLabelValues("cancel").Observe(time.Since(start).Seconds())
	}()
	if o.payment == nil {
		return nil, false, ErrPaymentDisabled
	}
	if providerPaymentID == uuid.Nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "invalid_provider_id", Reason: "provider payment UUID is nil", Action: "reject callback and inspect provider payload", CallbackStatus: status})
		return nil, false, errors.New("invalid provider payment UUID")
	}
	o.paymentMu.Lock()
	paymentLocked := true
	defer func() {
		if paymentLocked {
			o.paymentMu.Unlock()
		}
	}()

	order, err = o.db.GetOrderByProviderPaymentID(ctx, "platega", providerPaymentID)
	if err != nil {
		if errors.Is(err, database.ErrOrderNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("payment cancellation callback for unknown order", zap.String("provider_payment_id", providerPaymentID.String()), zap.String("status", status))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "unknown_provider_id", Reason: "cancellation callback references an order that does not exist", Action: "verify the provider transaction and reconcile manually", ProviderID: providerPaymentID.String(), CallbackStatus: status})
			return nil, false, nil
		}
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_failed", Reason: err.Error(), Action: "retry callback after database recovery", ProviderID: providerPaymentID.String(), CallbackStatus: status})
		return nil, false, fmt.Errorf("find payment order for cancellation: %w", err)
	}
	if order.Currency != currency {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_currency_mismatch", Reason: fmt.Sprintf("callback currency %q does not match order currency %q", currency, order.Currency), Action: "reject callback and investigate provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: currency, ProviderID: providerPaymentID.String(), CallbackStatus: status})
		return nil, false, ErrCurrencyMismatch
	}
	cents, err := platega.ParseCallbackAmount(amount)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_amount_invalid", Reason: err.Error(), Action: "reject callback and inspect provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: currency, ProviderID: providerPaymentID.String(), CallbackStatus: status, Payload: amount.String()})
		return nil, false, fmt.Errorf("parse cancellation amount: %w", err)
	}
	if cents != order.AmountCents {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_amount_mismatch", Reason: fmt.Sprintf("callback amount %d cents does not match order amount %d cents", cents, order.AmountCents), Action: "reject callback and investigate provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: cents, Currency: currency, ProviderID: providerPaymentID.String(), CallbackStatus: status})
		return nil, false, ErrAmountMismatch
	}

	if status != "CANCELED" && status != "CHARGEBACKED" {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "invalid_payment_status", Reason: fmt.Sprintf("unsupported cancellation status %q", status), Action: "ignore callback and verify provider status", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: status})
		return nil, false, ErrInvalidPaymentTransition
	}
	isChargeback := status == "CHARGEBACKED"
	// wasPaid must reflect the order state BEFORE the transition: a chargeback on
	// a pending order has collected no money yet and must not report wasPaid=true.
	wasPaid = isChargeback && order.Status == database.OrderStatusPaid
	from := []database.OrderStatus{database.OrderStatusPending}
	if isChargeback {
		from = append(from, database.OrderStatusPaid)
	}
	if isChargeback {
		if o.syncSvc == nil {
			err := fmt.Errorf("%w: SyncService is not configured", ErrChargebackAtomicPathNotReady)
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "cancel_payment_failed", Reason: err.Error(), Action: "wire SyncService before enabling chargebacks", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: status})
			return nil, false, err
		}
		freePlan, planErr := o.db.GetPlanByName(ctx, database.FreePlanName)
		if planErr != nil {
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "cancel_payment_failed", Reason: planErr.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: status})
			return nil, false, fmt.Errorf("resolve free plan for chargeback: %w", planErr)
		}
		result, txErr := o.db.CancelPaidOrderAndDowngradeCAS(ctx, "platega", providerPaymentID, time.Now().UTC(), freePlan.ID, o.chargebackPlanInTx())
		if txErr != nil {
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "cancel_payment_failed", Reason: txErr.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: status})
			return nil, false, fmt.Errorf("cancel payment: %w", txErr)
		}
		if result == nil || !result.Transitioned {
			logger.Warn("payment cancellation callback was a no-op", zap.String("provider_payment_id", providerPaymentID.String()), zap.String("status", status))
			return nil, false, nil
		}
		order = result.Order
		wasPaid = result.WasPaid
		var chargebackSub *database.Subscription
		var chargebackProduct *database.Product
		if wasPaid {
			recordPaymentAmount("chargeback", order.AmountCents, order.Currency)
			telegramID := int64(0)
			if loaded, subErr := o.db.GetByID(ctx, order.SubscriptionID); subErr == nil && loaded != nil {
				telegramID = loaded.TelegramID
				chargebackSub = loaded
			}
			if order.ProductID != 0 {
				if loaded, pErr := o.db.GetProductByID(ctx, order.ProductID); pErr == nil && loaded != nil {
					chargebackProduct = loaded
				}
			}
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "chargeback", Reason: "provider reported CHARGEBACKED", Action: "verify the refund; a paid order is downgraded to free automatically", OrderID: order.ID, TelegramID: telegramID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: status})
		}
		o.paymentMu.Unlock()
		paymentLocked = false
		if wasPaid {
			o.notifyAdminChargeback(ctx, chargebackSub, order, chargebackProduct, result.Downgraded)
		}
		if result.Downgraded {
			o.syncChargebackAfterCommit(ctx, order.SubscriptionID)
		}
		return order, wasPaid, nil
	}

	transitioned, err := o.db.CancelOrderCAS(ctx, "platega", providerPaymentID, from)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "cancel_payment_failed", Reason: err.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), CallbackStatus: status})
		return nil, false, fmt.Errorf("cancel payment: %w", err)
	}
	if !transitioned {
		logger.Warn("payment cancellation callback was a no-op", zap.String("provider_payment_id", providerPaymentID.String()), zap.String("status", status))
		return nil, false, nil
	}
	order, err = o.db.GetOrderByProviderPaymentID(ctx, "platega", providerPaymentID)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_canceled_order_failed", Reason: err.Error(), Action: "retry callback after database recovery", ProviderID: providerPaymentID.String(), CallbackStatus: status})
		return nil, false, fmt.Errorf("load canceled order: %w", err)
	}
	o.paymentMu.Unlock()
	paymentLocked = false
	return order, wasPaid, nil
}

func (o *OrderService) chargebackPlanInTx() database.ChargebackPlanInTxFn {
	if o.syncSvc == nil {
		return nil
	}
	return func(ctx context.Context, tx *gorm.DB, subscriptionID, freePlanID uint) error {
		return o.syncSvc.ApplyPlanToSubscriptionInTx(ctx, tx, subscriptionID, freePlanID)
	}
}

func (o *OrderService) syncChargebackAfterCommit(ctx context.Context, subscriptionID uint) {
	if o.syncSvc == nil {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), paymentSyncTimeout)
	defer cancel()
	if err := o.syncSvc.SyncSubscription(dctx, subscriptionID); err != nil {
		logger.Warn("chargeback post-commit sync failed", zap.Uint("subscription_id", subscriptionID), zap.Error(err))
	}
}

// NotifyPaidUser builds the same subscription presentation used by the bot screen.
// It returns the target Telegram ID and message text; delivery is intentionally
// left to the webhook layer so a send failure cannot roll back a paid order.
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
	if o.subSvc == nil {
		return 0, "", errors.New("subscription service is not configured")
	}
	_, traffic, err := o.subSvc.GetWithTraffic(ctx, sub.TelegramID)
	if err != nil {
		return 0, "", fmt.Errorf("load paid subscription traffic: %w", err)
	}
	text := FormatSubscriptionMessage("✅ *Оплата подтверждена!*", "", traffic, SubscriptionURL(o.cfg, sub.SubscriptionID))
	return sub.TelegramID, text, nil
}
