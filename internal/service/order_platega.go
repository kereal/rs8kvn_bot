package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// PlategaClient is the minimal outbound payment contract consumed by OrderService
// for creating a provider transaction and reading back its details. The status
// call is used best-effort to persist the provider fee, which the callback does
// not carry.
type PlategaClient interface {
	CreateTransaction(ctx context.Context, req platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error)
	GetTransactionStatus(ctx context.Context, transactionID string) (*platega.TransactionStatusResponse, error)
}

// paymentSyncTimeout bounds the best-effort post-commit VPN sync. It prevents a
// stuck node from keeping the webhook handler open indefinitely; the sync worker
// retries any remaining pending node operations later.
const paymentSyncTimeout = 20 * time.Second

// feeSyncBudget bounds one whole provider-fee sync pass. Without it, a hung
// provider could stall the scheduler for limit×paymentSyncTimeout (up to ~16
// minutes) and delay the node sync that shares the same worker tick.
const feeSyncBudget = 60 * time.Second

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

	_, err := o.adminBot.Send(msg)
	if err != nil {
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
	// В Stars-режиме Platega-клиент отсутствует: o.payment == nil — не ошибка.
	if o.payment == nil && o.activeProvider() != database.PaymentProviderTelegramStars {
		return nil, nil, ErrPaymentDisabled
	}

	if telegramID <= 0 || product == nil || product.ID == 0 {
		return nil, nil, errors.New("invalid paid product request")
	}

	canonical, err := o.db.GetProductByID(ctx, product.ID)
	if err != nil {
		o.notifyRequestIssue(ctx, "load_product_failed", err.Error(), "retry after database recovery", telegramID, product, nil)
		return nil, nil, fmt.Errorf("load canonical product: %w", err)
	}

	if canonical == nil {
		err := errors.New("load canonical product: product is nil")
		o.notifyRequestIssue(ctx, "load_product_failed", err.Error(), "verify the product record", telegramID, product, nil)

		return nil, nil, err
	}

	// Platega не умеет Stars (XTR): проверка должна происходить до reuse любого
	// pending intent и до создания транзакции, иначе цена может быть разделена
	// на 100 либо возвращён старый некорректный link.
	if o.activeProvider() != database.PaymentProviderTelegramStars &&
		strings.EqualFold(strings.TrimSpace(canonical.Currency), stars.Currency) {
		return nil, nil, ErrCurrencyMismatch
	}

	now := time.Now().UTC()
	// Resolve the subscription before looking up the intent. Intents belong to
	// a concrete subscription, not merely to a Telegram ID; this matters when
	// historical data contains more than one subscription for one user.
	var sub *database.Subscription

	sub, err = o.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		if !errors.Is(err, database.ErrSubscriptionNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
			o.notifyRequestIssue(ctx, "load_subscription_failed", err.Error(), "retry after database recovery", telegramID, canonical, nil)
			return nil, nil, fmt.Errorf("load payment subscription: %w", err)
		}

		if !canonical.IsActive || canonical.PriceCents <= 0 {
			return nil, nil, errors.New("invalid paid product request")
		}

		plan, planErr := o.db.GetPlanByID(ctx, canonical.PlanID)
		if planErr != nil {
			o.notifyRequestIssue(ctx, "load_plan_failed", planErr.Error(), "retry after database recovery", telegramID, canonical, nil)
			return nil, nil, fmt.Errorf("load product plan: %w", planErr)
		}

		if plan == nil || !plan.IsActive {
			return nil, nil, errors.New("product plan is inactive")
		}

		if o.subSvc == nil {
			err := errors.New("subscription service is not configured")
			o.notifyRequestIssue(ctx, "subscription_service_not_ready", err.Error(), "wire SubscriptionService before enabling payments", telegramID, canonical, nil)

			return nil, nil, err
		}

		sub, err = o.subSvc.GetOrCreateSubscription(ctx, telegramID, username, "")
		if err != nil {
			o.notifyRequestIssue(ctx, "create_subscription_failed", err.Error(), "retry after subscription service recovery", telegramID, canonical, nil)
			return nil, nil, fmt.Errorf("get or create subscription: %w", err)
		}
	}

	// Telegram Stars: ранний return до всех Platega-проверок — reuse ссылки,
	// MarkPaymentCreationUncertain и CreateTransaction в Stars-режиме неприменимы.
	if o.activeProvider() == database.PaymentProviderTelegramStars {
		return o.requestStarsPayment(ctx, telegramID, canonical, sub)
	}

	// Return a still-valid link before validating the current product flags: an
	// operator may deactivate a product while an already-created payment link is
	// still usable. Expiry is terminalized by the repository lookup.
	order, err := o.db.FindPendingPaymentOrder(ctx, sub.ID, canonical.ID, database.PaymentProviderPlatega, now)
	if err != nil {
		o.notifyRequestIssue(ctx, "find_pending_order_failed", err.Error(), "retry after database recovery", telegramID, canonical, nil)
		return nil, nil, fmt.Errorf("find pending payment order: %w", err)
	}

	if order != nil && order.Status == database.OrderStatusExpired {
		order = nil
	}

	if order != nil && order.Status != database.OrderStatusPending {
		return nil, order, ErrPaymentAlreadyInProgress
	}

	var created bool

	if order == nil {
		if !canonical.IsActive || canonical.PriceCents <= 0 {
			return nil, nil, errors.New("invalid paid product request")
		}

		plan, planErr := o.db.GetPlanByID(ctx, canonical.PlanID)
		if planErr != nil {
			o.notifyRequestIssue(ctx, "load_plan_failed", planErr.Error(), "retry after database recovery", telegramID, canonical, nil)
			return nil, nil, fmt.Errorf("load product plan: %w", planErr)
		}

		if plan == nil || !plan.IsActive {
			return nil, nil, errors.New("product plan is inactive")
		}
		// The repository transaction expires an old link and creates/reuses the
		// single pending intent under the partial unique index. This is the only
		// creation path, so concurrent requests cannot create duplicate intents.
		// reuse (created=false) возвращает существующий intent без новой
		// транзакции провайдера и без повторного order_created аудита.
		order, created, err = o.db.FindOrCreatePendingPaymentOrder(ctx, sub.ID, canonical.ID, database.PaymentProviderPlatega, canonical.PriceCents, canonical.Currency, nil, now)
		if err != nil {
			o.notifyRequestIssue(ctx, "create_pending_order_failed", err.Error(), "retry after database recovery", telegramID, canonical, nil)
			return nil, nil, fmt.Errorf("find or create pending payment order: %w", err)
		}

		if created {
			o.logAuditOrderCreated(ctx, order)
		} else {
			// Существующий intent (включая конкурентную перезагрузку): новую
			// транзакцию провайдера не создаём — победитель уже определён.
			return nil, order, ErrPaymentAlreadyInProgress
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
			zap.String("provider", database.PaymentProviderPlatega),
			zap.String("provider_payment_id", paymentID.String()),
			zap.Int64("amount_cents", order.AmountCents),
			zap.String("currency", order.Currency))

		return &PaymentInfo{URL: order.PaymentURL, Provider: database.PaymentProviderPlatega, PaymentID: paymentID.String(), ExpiresAt: *order.PaymentExpiresAt}, order, nil
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
		o.notifyRequestIssue(ctx, "load_plan_failed", planErr.Error(), "retry after database recovery", telegramID, canonical, order)
		return nil, order, fmt.Errorf("load product plan: %w", planErr)
	}

	if plan == nil || !plan.IsActive {
		return nil, order, errors.New("product plan is inactive")
	}

	claimed, err := o.db.MarkPaymentCreationUncertain(ctx, order.ID, true)
	if err != nil {
		o.notifyRequestIssue(ctx, "claim_payment_creation_failed", err.Error(), "retry after database recovery", telegramID, canonical, order)
		return nil, order, fmt.Errorf("mark payment creation uncertain: %w", err)
	}

	if !claimed {
		latest, loadErr := o.db.GetOrderByID(ctx, order.ID)
		if loadErr != nil {
			o.notifyRequestIssue(ctx, "reload_pending_order_failed", loadErr.Error(), "retry after database recovery", telegramID, canonical, order)
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

			return &PaymentInfo{URL: latest.PaymentURL, Provider: database.PaymentProviderPlatega, PaymentID: paymentID.String(), ExpiresAt: *latest.PaymentExpiresAt}, latest, nil
		}

		return nil, latest, ErrPaymentAlreadyInProgress
	}

	base := "https://t.me/" + strings.TrimPrefix(o.botUsername, "@")

	response, err := o.payment.CreateTransaction(ctx, platega.CreateTransactionRequest{AmountCents: canonical.PriceCents, Currency: canonical.Currency, Description: canonical.Name, ReturnURL: base, FailedURL: base, Payload: fmt.Sprint(order.ID), UserID: fmt.Sprint(telegramID), UserName: username})
	if err != nil {
		if errors.Is(err, platega.ErrBadRequest) || errors.Is(err, platega.ErrAuth) {
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "provider_create_rejected", Reason: err.Error(), Action: "inspect Platega credentials and request fields before retrying", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: order.ProviderPaymentID, PaymentURL: order.PaymentURL})

			_, clearErr := o.db.MarkPaymentCreationUncertain(ctx, order.ID, false)
			if clearErr != nil {
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
		return nil, order, fmt.Errorf("%w: transactionId must be UUID v4: %w", platega.ErrProvider, err)
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

	err = o.db.SavePaymentDetails(ctx, order.ID, providerPaymentID.String(), url, expiresAt)
	if err != nil {
		logger.Warn("provider transaction created but payment details were not saved; manual reconciliation required", zap.Uint("order_id", order.ID), zap.String("provider_payment_id", providerPaymentID.String()), zap.Error(err))
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "payment_details_save_failed", Reason: err.Error(), Action: "attach or refund provider transaction manually", OrderID: order.ID, TelegramID: telegramID, ProductID: canonical.ID, ProductName: canonical.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID.String(), PaymentURL: url})

		return nil, order, fmt.Errorf("save payment details: %w", err)
	}

	order.ProviderPaymentID, order.PaymentURL = providerPaymentID.String(), url
	order.PaymentExpiresAt, order.PaymentCreationUncertain = &expiresAt, false
	logger.Info("Payment link created",
		zap.Uint("order_id", order.ID),
		zap.Uint("product_id", canonical.ID),
		zap.String("provider", database.PaymentProviderPlatega),
		zap.String("provider_payment_id", providerPaymentID.String()),
		zap.Int64("amount_cents", order.AmountCents),
		zap.String("currency", order.Currency))

	return &PaymentInfo{URL: url, Provider: database.PaymentProviderPlatega, PaymentID: providerPaymentID.String(), ExpiresAt: expiresAt}, order, nil
}

// orderByCallbackPayload находит заказ по payload провайдера, когда id
// транзакции ещё не сохранён, и привязывает этот id. Заказ с уже записанным
// другим id не перехватывается: payload мог устареть.
func (o *OrderService) orderByCallbackPayload(
	ctx context.Context, payload string, providerPaymentID string,
) (*database.Order, error) {
	raw := strings.TrimSpace(payload)
	if raw == "" {
		return nil, database.ErrOrderNotFound
	}

	orderID, convErr := strconv.ParseUint(raw, 10, 64)
	if convErr != nil {
		return nil, database.ErrOrderNotFound
	}

	order, loadErr := o.db.GetOrderByID(ctx, uint(orderID))
	if loadErr != nil {
		return nil, loadErr
	}

	if strings.TrimSpace(order.ProviderPaymentID) != "" {
		return nil, database.ErrOrderNotFound
	}

	attached, attachErr := o.db.AttachProviderPaymentID(ctx, order.ID, database.PaymentProviderPlatega, providerPaymentID)
	if attachErr != nil {
		return nil, attachErr
	}

	if !attached {
		// Параллельный callback успел привязать транзакцию.
		return nil, database.ErrOrderNotFound
	}

	logger.Info("payment callback matched order by payload after missing provider id",
		zap.Uint("order_id", order.ID),
		zap.String("provider_payment_id", providerPaymentID))

	order.ProviderPaymentID = providerPaymentID
	order.PaymentProvider = database.PaymentProviderPlatega

	return order, nil
}

// logAuditOrderCreated пишет событие order_created best-effort: ошибка журнала
// не влияет на выдачу платёжной ссылки.
func (o *OrderService) logAuditOrderCreated(ctx context.Context, order *database.Order) {
	err := o.db.InsertAuditLog(ctx, database.AuditOrderCreated(order))
	if err != nil {
		logger.Warn("audit log write failed",
			zap.String("event", database.AuditEventOrderCreated),
			zap.Uint("order_id", order.ID),
			zap.Error(err))
	}
}

// ConfirmPayment validates a provider callback and atomically applies the paid
// transition plus subscription plan changes. Only the successful pending-to-paid
// CAS caller performs post-commit synchronization and reports Activated=true.
func (o *OrderService) ConfirmPayment(
	ctx context.Context, providerPaymentID string, payload string, amount json.Number, currency string,
) (*PaymentConfirmation, error) {
	start := time.Now()
	confirmation, err := o.confirmPayment(ctx, providerPaymentID, payload, amount, currency)

	recordPaymentConfirmationMetrics(start, confirmation, err)

	return confirmation, err
}

func (o *OrderService) confirmPayment(
	ctx context.Context, providerPaymentID string, payload string, amount json.Number, currency string,
) (*PaymentConfirmation, error) {
	if o.payment == nil {
		return nil, ErrPaymentDisabled
	}
	// Per-order serialization: webhook callbacks for the same provider payment
	// UUID run sequentially; callbacks for different orders run in parallel.
	//
	// detachCtx ensures that a client disconnect (aborted webhook HTTP
	// connection) does not cancel lock acquisition or the initial order
	// lookup. The payment processing that follows uses the caller's ctx,
	// so DB-level timeouts still apply.
	detachCtx := context.WithoutCancel(ctx)
	unlock, err := o.lockPayment(detachCtx, providerPaymentID)
	if err != nil {
		logger.Warn("could not acquire per-order payment lock", zap.String("provider_payment_id", providerPaymentID), zap.Error(err))
		return nil, fmt.Errorf("acquire payment lock: %w", err)
	}
	defer unlock()

	if strings.TrimSpace(providerPaymentID) == "" {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "invalid_provider_id", Reason: "provider payment ID is empty", Action: "reject callback and inspect provider payload", CallbackStatus: "CONFIRMED"})
		return nil, errors.New("invalid provider payment ID")
	}

	order, err := o.db.GetOrderByProviderPaymentID(detachCtx, database.PaymentProviderPlatega, providerPaymentID)
	if err != nil && (errors.Is(err, database.ErrOrderNotFound) || errors.Is(err, gorm.ErrRecordNotFound)) {
		// Окно между CreateTransaction и SavePaymentDetails: провайдер может
		// подтвердить платёж раньше, чем мы сохранили id транзакции. Провайдер
		// возвращает payload, переданный при создании (= order.ID), поэтому
		// заказ находится по нему, а id привязывается CAS.
		order, err = o.orderByCallbackPayload(detachCtx, payload, providerPaymentID)
	}

	if err != nil {
		if errors.Is(err, database.ErrOrderNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("payment callback for unknown order", zap.String("provider_payment_id", providerPaymentID))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "unknown_provider_id", Reason: "callback references an order that does not exist", Action: "verify the provider transaction and reconcile manually", ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})

			return &PaymentConfirmation{Activated: false}, nil
		}

		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_failed", Reason: err.Error(), Action: "retry callback after database recovery", ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})

		return nil, fmt.Errorf("find payment order: %w", err)
	}

	if order.Currency != currency {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_currency_mismatch", Reason: fmt.Sprintf("callback currency %q does not match order currency %q", currency, order.Currency), Action: "reject callback and investigate provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})
		return nil, ErrCurrencyMismatch
	}

	cents, err := platega.ParseCallbackAmount(amount)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_amount_invalid", Reason: err.Error(), Action: "reject callback and inspect provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED", Payload: amount.String()})
		return nil, fmt.Errorf("parse callback amount: %w", err)
	}

	// Platega may include its payment-method commission in the callback amount.
	// The order amount is the merchant's requested price, so underpayment is
	// rejected while a provider-calculated amount above it is accepted.
	if cents < order.AmountCents {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_amount_mismatch", Reason: fmt.Sprintf("callback amount %d cents is below order amount %d cents", cents, order.AmountCents), Action: "reject callback and investigate provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: cents, Currency: currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})
		return nil, ErrAmountMismatch
	}

	if cents > order.AmountCents {
		logger.Info("payment callback includes provider fee", zap.Uint("order_id", order.ID), zap.Int64("order_amount_cents", order.AmountCents), zap.Int64("callback_amount_cents", cents), zap.Int64("provider_fee_cents", cents-order.AmountCents), zap.String("currency", currency), zap.String("provider_payment_id", providerPaymentID))
	}

	if order.Status == database.OrderStatusPaid {
		return &PaymentConfirmation{Order: order}, nil
	}

	now := time.Now().UTC()
	if order.Status == database.OrderStatusExpired || order.Status == database.OrderStatusCanceled {
		// PaymentExpiresAt is the real provider-link deadline and must stay
		// unchanged: extending it would make RequestPayment reuse a link that
		// Platega has already invalidated. The provider can nevertheless deliver
		// a CONFIRMED webhook several minutes after the customer paid near that
		// deadline. Therefore an already-expired order receives a five-minute
		// settlement grace period, measured from the original provider deadline.
		// This grace affects only acceptance of this callback; it does not extend
		// the payment link and does not allow a canceled order to be resurrected.
		withinSettlementGrace := order.Status == database.OrderStatusExpired &&
			order.PaymentExpiresAt != nil &&
			!now.After(order.PaymentExpiresAt.Add(5*time.Minute))

		if !withinSettlementGrace {
			logger.Warn("late payment callback ignored", zap.Uint("order_id", order.ID), zap.String("provider_payment_id", providerPaymentID), zap.String("order_status", string(order.Status)))

			telegramID := int64(0)

			sub, subErr := o.db.GetByID(ctx, order.SubscriptionID)
			if subErr == nil && sub != nil {
				telegramID = sub.TelegramID
			}

			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "late_confirmed_callback", Reason: fmt.Sprintf("callback received for order status %s", order.Status), Action: "verify payment and refund or activate manually", OrderID: order.ID, TelegramID: telegramID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})

			return &PaymentConfirmation{Order: order}, nil
		}

		logger.Info("accepting expired payment callback inside settlement grace", zap.Uint("order_id", order.ID), zap.String("provider_payment_id", providerPaymentID), zap.Time("provider_link_expires_at", *order.PaymentExpiresAt), zap.Time("settlement_deadline", order.PaymentExpiresAt.Add(5*time.Minute)))
	}

	if order.Status != database.OrderStatusPending && order.Status != database.OrderStatusExpired {
		return nil, ErrInvalidPaymentTransition
	}

	if o.syncSvc == nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "payment_sync_not_ready", Reason: "post-commit synchronization service is not configured", Action: "wire SyncService before enabling payment callbacks", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})
		return nil, ErrPaymentSyncNotReady
	}

	product, err := o.db.GetProductByID(ctx, order.ProductID)
	if err != nil {
		if errors.Is(err, database.ErrProductNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			// The product row was removed after the order was created (e.g. by a
			// direct SQL cleanup). Activation is impossible and the money was
			// taken, so acknowledge the callback (200) and alert for manual
			// refund/reconciliation instead of returning 5xx forever.
			logger.Warn("payment callback for deleted product", zap.Uint("order_id", order.ID), zap.Uint("product_id", order.ProductID), zap.String("provider_payment_id", providerPaymentID))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_product_failed", Reason: "ordered product no longer exists", Action: "verify payment and refund or activate manually", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})

			return &PaymentConfirmation{Activated: false}, nil
		}

		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_product_failed", Reason: err.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})

		return nil, fmt.Errorf("get ordered product: %w", err)
	}

	sub, err := o.db.GetByID(ctx, order.SubscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			// The subscription was deleted (or revoked and removed) after the
			// order was created. The money was taken, so acknowledge the callback
			// (200) and alert for manual refund/reconciliation instead of
			// returning 5xx forever while Platega retries.
			logger.Warn("payment callback for deleted subscription", zap.Uint("order_id", order.ID), zap.Uint("subscription_id", order.SubscriptionID), zap.String("provider_payment_id", providerPaymentID))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_subscription_failed", Reason: "ordered subscription no longer exists", Action: "verify payment and refund or reconcile manually", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})

			return &PaymentConfirmation{Activated: false}, nil
		}

		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_subscription_failed", Reason: err.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})

		return nil, fmt.Errorf("get ordered subscription: %w", err)
	}
	// A renewal extends an already-paid subscription; a first purchase activates
	// a free-plan one. Captured before CAS mutates sub in place.
	isRenewal := sub.PricePaidCents > 0 || sub.ProductID != nil
	var applyPlan database.ApplyPlanInTxFn
	if o.syncSvc != nil {
		applyPlan = o.syncSvc.ApplyPlanToSubscriptionInTx
	}

	activated, err := o.db.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, applyPlan, cents, "", nil)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "confirm_payment_failed", Reason: err.Error(), Action: "retry callback; order must remain pending if DB setup rolled back", OrderID: order.ID, TelegramID: sub.TelegramID, ProductID: order.ProductID, ProductName: product.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: "CONFIRMED"})
		return nil, err
	}

	if activated {
		mirrorOrderFromSubscriptionAfterCAS(order, sub, now)
	}

	return o.confirmPaidOrderTail(ctx, order, sub, product, isRenewal, activated, unlock)
}

// confirmPaidOrderTail выполняет действия после успешного подтверждения оплаты:
// освобождает per-order lock, инвалидирует кэш, шлёт админ-алерт и синхронизирует
// подписку с VPN-нодами. Вызывается обеими ветками подтверждения (Platega, Stars).
// unlock — разблокировщик per-order lock; передаётся явно, чтобы вызывающая сторона
// могла удерживать его до последнего момента (Platega: unlock до sync, Stars: тоже).
func (o *OrderService) confirmPaidOrderTail(
	ctx context.Context, order *database.Order, sub *database.Subscription, product *database.Product,
	isRenewal, activated bool, unlock func(),
) (*PaymentConfirmation, error) {
	unlock()

	if activated {
		if o.subSvc != nil && sub.SubscriptionID != "" {
			o.subSvc.InvalidateBySubID(ctx, sub.SubscriptionID)
		}

		o.notifyAdminPaid(ctx, sub, order, product, isRenewal)
	}

	if activated && o.syncSvc != nil {
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), paymentSyncTimeout)
		defer cancel()

		err := o.syncSvc.SyncSubscription(syncCtx, sub.ID)
		if err != nil {
			logger.Warn("payment post-commit sync failed", zap.Uint("subscription_id", sub.ID), zap.Error(err))
		}
	}

	return &PaymentConfirmation{Order: order, Activated: activated, IsRenewal: isRenewal}, nil
}

// SyncProviderFees retries best-effort provider fee lookups for paid orders.
// The nullable provider_fee_cents column is the durable retry queue, so payment
// callbacks do not wait for a second provider request and a later worker run can
// recover from provider, network, or database failures.
func (o *OrderService) SyncProviderFees(ctx context.Context, limit int) error {
	if o.payment == nil {
		return nil
	}

	// One shared deadline for the whole pass: a provider that hangs on every
	// request must not extend one scheduler tick beyond feeSyncBudget. Each
	// per-order lookup keeps its own shorter paymentSyncTimeout.
	budgetCtx, cancel := context.WithTimeout(ctx, feeSyncBudget)
	defer cancel()

	pageSize := limit
	if pageSize <= 0 {
		pageSize = 50
	}

	var (
		afterID uint
		errs    []error
	)

	for {
		orders, err := o.db.GetPaidOrdersWithoutProviderFeeAfterID(budgetCtx, afterID, pageSize)
		if err != nil {
			return fmt.Errorf("load paid orders without provider fee after id %d: %w", afterID, err)
		}
		if len(orders) == 0 {
			break
		}

		for _, order := range orders {
			err = budgetCtx.Err()
			if err != nil {
				return err
			}

			// Комиссия провайдера — только Platega: Stars-заказы не имеют
			// комиссии и не могут запрашиваться через Platega API.
			if order.PaymentProvider != database.PaymentProviderPlatega {
				logger.Warn("skipping provider fee sync for non-Platega order",
					zap.Uint("order_id", order.ID),
					zap.String("payment_provider", order.PaymentProvider))
				continue
			}

			providerPaymentID, parseErr := platega.ParseTransactionID(order.ProviderPaymentID)
			if parseErr != nil {
				wrappedErr := fmt.Errorf("order %d: parse provider payment ID: %w", order.ID, parseErr)
				logger.Warn("failed to parse provider payment ID for fee sync", zap.Uint("order_id", order.ID), zap.String("provider_payment_id", order.ProviderPaymentID), zap.Error(wrappedErr))
				errs = append(errs, wrappedErr)
				continue
			}

			callbackAmountCents := order.AmountCents
			if order.CallbackAmountCents != nil {
				callbackAmountCents = *order.CallbackAmountCents
			}
			err = o.persistProviderFee(budgetCtx, order.ID, providerPaymentID.String(), callbackAmountCents)
			if err != nil {
				wrappedErr := fmt.Errorf("order %d: %w", order.ID, err)
				logger.Warn("provider fee sync failed", zap.Uint("order_id", order.ID), zap.Error(wrappedErr))
				errs = append(errs, wrappedErr)
			}
		}

		afterID = orders[len(orders)-1].ID
	}

	return errors.Join(errs...)
}

func (o *OrderService) persistProviderFee(ctx context.Context, orderID uint, providerPaymentID string, callbackAmountCents int64) error {
	feeCtx, cancel := context.WithTimeout(ctx, paymentSyncTimeout)
	defer cancel()

	status, err := o.payment.GetTransactionStatus(feeCtx, providerPaymentID)
	if err != nil {
		if errors.Is(err, platega.ErrTransactionNotFound) {
			// Транзакция удалена у провайдера — комиссию уже не получить.
			// Пишем 0 как терминальный маркер, чтобы заказ не оставался в
			// очереди ретрая (provider_fee_cents IS NULL) вечно.
			logger.Warn("provider transaction not found; provider fee recorded as unavailable",
				zap.Uint("order_id", orderID),
				zap.String("provider_payment_id", providerPaymentID))
			feeUnavailable := int64(0)

			return o.db.SaveOrderPaymentAmounts(feeCtx, orderID, callbackAmountCents, &feeUnavailable)
		}

		return fmt.Errorf("fetch provider fee: %w", err)
	}
	if status == nil {
		return errors.New("fetch provider fee: empty transaction status")
	}
	if status.Status != "CONFIRMED" {
		// Не-финальный статус транзакции: комиссию пока не записываем,
		// NULL-маркер оставляет заказ в очереди ретрая следующего тика.
		logger.Warn("provider transaction is not confirmed; fee sync deferred",
			zap.Uint("order_id", orderID),
			zap.String("provider_payment_id", providerPaymentID),
			zap.String("transaction_status", status.Status))

		return nil
	}

	feeCents, err := status.CommissionCents()
	if err != nil {
		return fmt.Errorf("parse provider fee: %w", err)
	}

	err = o.db.SaveOrderPaymentAmounts(feeCtx, orderID, callbackAmountCents, &feeCents)
	if err != nil {
		return fmt.Errorf("save provider fee: %w", err)
	}

	return nil
}

// CancelPaymentByProvider validates a cancellation or chargeback callback and
// applies an idempotent order transition. wasPaid is true only when a
// previously-paid order is transitioned by a CHARGEBACKED callback — a chargeback
// on a still-pending order (money never collected) reports wasPaid=false.
// A chargeback on a paid order automatically downgrades the subscription to the
// free plan (best-effort, after the payment lock is released) unless another
// paid order for the same subscription still exists — in that case access is
// preserved for manual review. Returns (nil, false, nil) for an idempotent no-op.
func (o *OrderService) CancelPaymentByProvider(ctx context.Context, providerPaymentID string, status string, amount json.Number, currency string) (order *database.Order, wasPaid bool, err error) {
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

	if strings.TrimSpace(providerPaymentID) == "" {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "invalid_provider_id", Reason: "provider payment ID is empty", Action: "reject callback and inspect provider payload", CallbackStatus: status})
		return nil, false, errors.New("invalid provider payment ID")
	}
	// Per-order serialization: confirm/cancel of one provider payment cannot
	// interleave with itself, but multiple distinct payments run in parallel.
	//
	// detachCtx ensures that a client disconnect (aborted webhook HTTP
	// connection) does not cancel lock acquisition or the initial order
	// lookup.
	detachCtx := context.WithoutCancel(ctx)
	unlock, err := o.lockPayment(detachCtx, providerPaymentID)
	if err != nil {
		logger.Warn("could not acquire per-order payment lock", zap.String("provider_payment_id", providerPaymentID), zap.Error(err))
		return nil, false, fmt.Errorf("acquire payment lock: %w", err)
	}
	defer unlock()

	order, err = o.db.GetOrderByProviderPaymentID(detachCtx, database.PaymentProviderPlatega, providerPaymentID)
	if err != nil {
		if errors.Is(err, database.ErrOrderNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("payment cancellation callback for unknown order", zap.String("provider_payment_id", providerPaymentID), zap.String("status", status))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "unknown_provider_id", Reason: "cancellation callback references an order that does not exist", Action: "verify the provider transaction and reconcile manually", ProviderID: providerPaymentID, CallbackStatus: status})

			return nil, false, nil
		}

		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_failed", Reason: err.Error(), Action: "retry callback after database recovery", ProviderID: providerPaymentID, CallbackStatus: status})

		return nil, false, fmt.Errorf("find payment order for cancellation: %w", err)
	}

	if order.Currency != currency {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_currency_mismatch", Reason: fmt.Sprintf("callback currency %q does not match order currency %q", currency, order.Currency), Action: "reject callback and investigate provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: currency, ProviderID: providerPaymentID, CallbackStatus: status})
		return nil, false, ErrCurrencyMismatch
	}

	cents, err := platega.ParseCallbackAmount(amount)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_amount_invalid", Reason: err.Error(), Action: "reject callback and inspect provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: currency, ProviderID: providerPaymentID, CallbackStatus: status, Payload: amount.String()})
		return nil, false, fmt.Errorf("parse cancellation amount: %w", err)
	}

	// Cancellation/chargeback callbacks carry the same provider-total amount
	// as CONFIRMED callbacks and may therefore include the payment commission.
	// Only an amount below the order price is rejected.
	if cents < order.AmountCents {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "callback_amount_mismatch", Reason: fmt.Sprintf("callback amount %d cents is below order amount %d cents", cents, order.AmountCents), Action: "reject callback and investigate provider payload", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: cents, Currency: currency, ProviderID: providerPaymentID, CallbackStatus: status})
		return nil, false, ErrAmountMismatch
	}

	if status != "CANCELED" && status != "CHARGEBACKED" {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "invalid_payment_status", Reason: fmt.Sprintf("unsupported cancellation status %q", status), Action: "ignore callback and verify provider status", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: status})
		return nil, false, ErrInvalidPaymentTransition
	}

	isChargeback := status == "CHARGEBACKED"
	// wasPaid is computed post-CAS from result.WasPaid (the BEFORE-transition
	// state of the order). A chargeback on a still-pending order has not yet
	// collected any money and reports wasPaid=false.
	from := []database.OrderStatus{database.OrderStatusPending}
	if isChargeback {
		from = append(from, database.OrderStatusPaid)
	}

	if isChargeback {
		if o.syncSvc == nil {
			err := fmt.Errorf("%w: SyncService is not configured", ErrChargebackAtomicPathNotReady)
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "cancel_payment_failed", Reason: err.Error(), Action: "wire SyncService before enabling chargebacks", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: status})

			return nil, false, err
		}

		freePlan, planErr := o.db.GetPlanByName(ctx, database.FreePlanName)
		if planErr != nil {
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "cancel_payment_failed", Reason: planErr.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: status})
			return nil, false, fmt.Errorf("resolve free plan for chargeback: %w", planErr)
		}

		result, txErr := o.db.CancelPaidOrderAndDowngradeCAS(ctx, database.PaymentProviderPlatega, providerPaymentID, time.Now().UTC(), freePlan.ID, o.chargebackPlanInTx(), database.AuditChargebackReceived)
		if txErr != nil {
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "cancel_payment_failed", Reason: txErr.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: status})
			return nil, false, fmt.Errorf("cancel payment: %w", txErr)
		}

		if result == nil || !result.Transitioned {
			logger.Warn("payment cancellation callback was a no-op", zap.String("provider_payment_id", providerPaymentID), zap.String("status", status))
			return nil, false, nil
		}

		order = result.Order
		wasPaid = result.WasPaid

		var (
			chargebackSub     *database.Subscription
			chargebackProduct *database.Product
		)

		if wasPaid {
			recordPaymentAmount("chargeback", order.AmountCents, order.Currency)
			chargebackSub, chargebackProduct = o.loadChargebackAlertContext(ctx, order)
		}

		unlock()

		if wasPaid {
			o.notifyAdminChargeback(ctx, chargebackSub, order, chargebackProduct, result.Downgraded)
		}

		if result.Downgraded {
			o.syncChargebackAfterCommit(ctx, order.SubscriptionID)
		}

		return order, wasPaid, nil
	}

	transitioned, err := o.db.CancelOrderCAS(ctx, database.PaymentProviderPlatega, providerPaymentID, from)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "cancel_payment_failed", Reason: err.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: providerPaymentID, CallbackStatus: status})
		return nil, false, fmt.Errorf("cancel payment: %w", err)
	}

	if !transitioned {
		logger.Warn("payment cancellation callback was a no-op", zap.String("provider_payment_id", providerPaymentID), zap.String("status", status))
		return nil, false, nil
	}
	// CancelOrderCAS transitioned the row from one of the allowed "from" states
	// to canceled. The only field changed in the DB is Status; mirror it in
	// memory so downstream callers do not need a second SELECT.
	order.Status = database.OrderStatusCanceled

	unlock()

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

// mirrorOrderFromSubscriptionAfterCAS keeps the order snapshot consistent with
// the subscription state the CAS just wrote. The CAS itself only mutates the
// caller-supplied subscription pointer; the order snapshot has to be updated
// separately so downstream notification (admin paid alert, user confirmation,
// subserver cache invalidation) reads a coherent picture.
//
// The nil guard on sub.ExpiresAt keeps the helper safe against fakes or future
// CAS implementations that activate without setting an expiry (e.g. free plans
// past paid activation).
func mirrorOrderFromSubscriptionAfterCAS(order *database.Order, sub *database.Subscription, now time.Time) {
	if sub != nil && sub.ExpiresAt != nil {
		newExpiry := *sub.ExpiresAt
		order.ExpiresAt = &newExpiry
	}

	order.Status = database.OrderStatusPaid
	paidAt, activatedAt := now, now
	order.PaidAt = &paidAt
	order.ActivatedAt = &activatedAt
}

// loadChargebackAlertContext loads the subscription and product needed for the
// admin chargeback alert. Each load is best-effort: a failure is logged via the
// returned error context but never blocks the chargeback alert. Callers render
// placeholders for any nil field, so alerts always go out.
func (o *OrderService) loadChargebackAlertContext(ctx context.Context, order *database.Order) (*database.Subscription, *database.Product) {
	var (
		sub     *database.Subscription
		product *database.Product
	)

	loaded, subErr := o.db.GetByID(ctx, order.SubscriptionID)
	if subErr == nil && loaded != nil {
		sub = loaded
	} else if subErr != nil {
		logger.Debug("chargeback admin alert: subscription lookup failed", zap.Uint("order_id", order.ID), zap.Error(subErr))
	}

	if order.ProductID != 0 {
		loaded, pErr := o.db.GetProductByID(ctx, order.ProductID)
		if pErr == nil && loaded != nil {
			product = loaded
		} else if pErr != nil {
			logger.Debug("chargeback admin alert: product lookup failed", zap.Uint("order_id", order.ID), zap.Uint("product_id", order.ProductID), zap.Error(pErr))
		}
	}

	return sub, product
}

func (o *OrderService) syncChargebackAfterCommit(ctx context.Context, subscriptionID uint) {
	if o.syncSvc == nil {
		return
	}

	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), paymentSyncTimeout)
	defer cancel()

	err := o.syncSvc.SyncSubscription(dctx, subscriptionID)
	if err != nil {
		logger.Warn("chargeback post-commit sync failed", zap.Uint("subscription_id", subscriptionID), zap.Error(err))
	}
}
