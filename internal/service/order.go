package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

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
	db          interfaces.OrderStore
	subSvc      *SubscriptionService
	syncSvc     *SyncService
	payment     PlategaClient
	botUsername string
	cfg         *config.Config
	adminBot    interfaces.BotAPI
	// stars — клиент Telegram Stars. Нулевой Pointer: Stars недоступен
	// даже при activeProvider()==telegram_stars (fail-closed). Atomic:
	// фоновый ретрай восстановления переключает клиента на живой инстанс,
	// пока обработчики его читают.
	stars atomic.Pointer[stars.Client]

	// paymentLocksMu guards paymentLocks. paymentLocks and the per-lock
	// channels together give each in-flight order (provider payment UUID)
	// its own serialization point, so concurrent webhook callbacks for
	// different orders do NOT block each other.
	paymentLocksMu sync.Mutex
	paymentLocks   map[string]*paymentLock
	// paymentLockTimeout caps how long a single goroutine may wait to acquire
	// a per-order lock. It uses the caller's context deadline if shorter.
	paymentLockTimeout time.Duration
}

// paymentLock is a capacity-1 token channel reused by every concurrent
// caller operating on the same provider payment UUID. The holder owns the
// token; pending waiters sit on the channel until the previous holder
// returns it.
type paymentLock struct {
	ch      chan struct{}
	waiters int
}

// paymentLockTimeoutDefault mirrors the SyncService per-subscription lock
// default. A stuck holder cannot starve other goroutines on this order past
// this ceiling; longer-held work (e.g. post-commit sync) happens AFTER the
// caller releases the token.
const paymentLockTimeoutDefault = 30 * time.Second

// PaymentInfo contains payment details for an order.
type PaymentInfo struct {
	URL       string
	Provider  string
	PaymentID string
	ExpiresAt time.Time
}

// PaymentConfirmation describes the result of a callback transition. Activated
// is true only for the callback that changed an order from pending to paid; repeat
// callbacks return the existing order with Activated=false. IsRenewal records
// whether the paid subscription already had a paid product before activation.
type PaymentConfirmation struct {
	Order     *database.Order
	Activated bool
	IsRenewal bool
	// AutoRefunded — новый charge ID автоматически возвращён (двойная оплата
	// уже оплаченного или отменённого Stars-заказа). Bot handler сообщает
	// покупателю «ошибочная оплата возвращена», а не «деньги списаны».
	AutoRefunded bool
	// DuplicateCharge — входящий charge трактуется как дубль и сервис пытался
	// сделать авто-возврат. AutoRefunded=true означает применённый возврат;
	// DuplicateCharge && !AutoRefunded — возврат не удался, bot-слой показывает
	// покупателю подсказку /paysupport.
	DuplicateCharge bool
	// AlreadyRefunded — charge уже был возвращён ранее (заказ отменён после
	// refund), повторный возврат не выполняется и не нужен: деньги у покупателя.
	// Bot-слой сообщает об уже выполненном возврате, а не о списании.
	AlreadyRefunded bool
}

// StarsRefundResult describes a completed Telegram Stars refund. TelegramID
// identifies the buyer who must receive the refund notification.
type StarsRefundResult struct {
	OrderID     uint
	TelegramID  int64
	AmountCents int64
	Currency    string
	Downgraded  bool
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

// recordPaymentConfirmationMetrics records the shared confirm-operation metrics
// for both payment providers.
func recordPaymentConfirmationMetrics(start time.Time, confirmation *PaymentConfirmation, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}

	metrics.PaymentOperationsTotal.WithLabelValues("confirm", result).Inc()
	metrics.PaymentOperationDuration.WithLabelValues("confirm").Observe(time.Since(start).Seconds())

	if err == nil && confirmation != nil && confirmation.Activated && confirmation.Order != nil {
		recordPaymentAmount("confirmed", confirmation.Order.AmountCents, confirmation.Order.Currency)
		if confirmation.IsRenewal {
			metrics.SubscriptionRenewalsTotal.Inc()
		}
	}
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

// formatAdminPaymentAlert рендерит админ-уведомление о возврате/чарджбэке:
// единый набор полей (тариф, сумма, покупатель, charge/order id, статус доступа)
// для обоих путей. title различает refund и chargeback.
func formatAdminPaymentAlert(title string, sub *database.Subscription, order *database.Order, product *database.Product, downgraded bool) string {
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

	return fmt.Sprintf("%s\n\n"+
		"💎 Тариф: *%s*\n"+
		"💵 Сумма: *%s*\n"+
		"👤 Покупатель: %s\n"+
		"🆔 Telegram ID: `%d`\n"+
		"🔖 Платёж: `%s`\n"+
		"📋 Подписка: #%d\n"+
		"🧾 Заказ: #%d\n"+
		"%s",
		title,
		productName,
		formatMoneyCents(amountCents, currency),
		utils.FormatUserLink(username, telegramID),
		telegramID,
		providerPaymentID,
		subscriptionID,
		orderID,
		access)
}

// NewOrderService wires the order service dependencies explicitly.
func NewOrderService(db interfaces.OrderStore, subSvc *SubscriptionService, syncSvc *SyncService, payment PlategaClient, botUsername string, cfg *config.Config) *OrderService {
	return &OrderService{
		db:                 db,
		subSvc:             subSvc,
		syncSvc:            syncSvc,
		payment:            payment,
		botUsername:        botUsername,
		cfg:                cfg,
		paymentLocks:       make(map[string]*paymentLock),
		paymentLockTimeout: paymentLockTimeoutDefault,
	}
}

// lockPayment acquires a per-order lock so only one webhook handler at a time
// mutates state for a given provider payment UUID. The unlock function
// returns the token to the channel and decrements the waiter count; the
// map entry is reclaimed once no goroutine holds or waits for it.
//
// Acquisition is bounded by the shorter of the caller's context deadline
// and paymentLockTimeout, so a stuck holder cannot starve others on the
// same order indefinitely. Concurrent callbacks for DIFFERENT orders run
// independently and never queue behind each other.
func (o *OrderService) lockPayment(ctx context.Context, providerPaymentID string) (func(), error) {
	o.paymentLocksMu.Lock()

	l, ok := o.paymentLocks[providerPaymentID]
	if !ok {
		l = &paymentLock{ch: make(chan struct{}, 1)}
		l.ch <- struct{}{} // initial token

		o.paymentLocks[providerPaymentID] = l
	}

	l.waiters++
	o.paymentLocksMu.Unlock()

	timeout := o.paymentLockTimeout
	if timeout <= 0 {
		timeout = paymentLockTimeoutDefault
	}

	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem > 0 && rem < timeout {
			timeout = rem
		}
	}

	acquireCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-l.ch:
		var released atomic.Bool

		return func() {
			// Idempotent: callers explicitly release before best-effort post-commit
			// sync, and the deferred release on return would otherwise block on the
			// already-full token channel.
			if !released.CompareAndSwap(false, true) {
				return
			}

			l.ch <- struct{}{}

			o.dropPaymentWaiter(l, providerPaymentID)
		}, nil
	case <-acquireCtx.Done():
		// Never return the token here: the holder still owns it. Only undo our
		// waiter accounting so the map entry can be reclaimed correctly.
		o.dropPaymentWaiter(l, providerPaymentID)
		return nil, fmt.Errorf("lock payment %s: %w", providerPaymentID, acquireCtx.Err())
	}
}

// dropPaymentWaiter decrements the waiter count for a payment lock and
// removes the map entry once no goroutine holds or waits for it. It must
// NOT touch l.ch: only the actual holder returns the token on unlock.
func (o *OrderService) dropPaymentWaiter(l *paymentLock, providerPaymentID string) {
	o.paymentLocksMu.Lock()

	l.waiters--
	if l.waiters == 0 {
		delete(o.paymentLocks, providerPaymentID)
	}
	o.paymentLocksMu.Unlock()
}

func (o *OrderService) applyFreePlanInTx() database.DowngradePlanInTxFn {
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

// loadPaymentAlertContext loads the subscription and product needed for the
// admin chargeback alert. Each load is best-effort: a failure is logged via the
// returned error context but never blocks the chargeback alert. Callers render
// placeholders for any nil field, so alerts always go out.
func (o *OrderService) loadPaymentAlertContext(ctx context.Context, order *database.Order) (*database.Subscription, *database.Product) {
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

func (o *OrderService) syncDowngradeAfterCommit(ctx context.Context, subscriptionID uint) {
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

// SetSyncService wires post-commit VPN synchronization after startup.
func (o *OrderService) SetSyncService(syncSvc *SyncService) { o.syncSvc = syncSvc }

// SetBotUsername sets the username used to build provider return and failure URLs.
func (o *OrderService) SetBotUsername(username string) { o.botUsername = strings.TrimSpace(username) }

// SetAdminBot wires the Telegram client used for best-effort operational alerts.
func (o *OrderService) SetAdminBot(bot interfaces.BotAPI) { o.adminBot = bot }

// SetStarsClient подключает клиент Telegram Stars. nil — Stars недоступен.
func (o *OrderService) SetStarsClient(c *stars.Client) { o.stars.Store(c) }

// activeProvider возвращает активного платёжного провайдера из конфига.
// Пустое значение означает Platega (обратная совместимость дефолта).
func (o *OrderService) activeProvider() string {
	if o.cfg == nil {
		return database.PaymentProviderPlatega
	}

	provider := strings.TrimSpace(o.cfg.PaymentProvider)
	if provider == "" {
		return database.PaymentProviderPlatega
	}

	return provider
}

// notifyRequestIssue enriches an early RequestPayment failure with whichever
// product/order context is available and routes it through the common alert path.
func (o *OrderService) notifyRequestIssue(ctx context.Context, event, reason, action string, telegramID int64, product *database.Product, order *database.Order) {
	issue := PaymentIssue{Event: event, Reason: reason, Action: action, TelegramID: telegramID}
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

	_, err := o.adminBot.Send(tgbotapi.NewMessage(o.cfg.TelegramAdminID, text))
	if err != nil {
		logger.Warn("failed to notify admin about payment event", zap.Error(err))
	}
}

// notifyAdminMarkdown отправляет админу Markdown-алерт best-effort: guard тот
// же, что у notifyAdmin; доставка никогда не влияет на платёжный флоу.
func (o *OrderService) notifyAdminMarkdown(ctx context.Context, text string, orderID uint) {
	if o.adminBot == nil || o.cfg == nil || o.cfg.TelegramAdminID <= 0 {
		return
	}

	msg := tgbotapi.NewMessage(o.cfg.TelegramAdminID, text)
	msg.ParseMode = "Markdown"

	_, err := o.adminBot.Send(msg)
	if err != nil {
		logger.Warn("failed to notify admin about payment event",
			zap.Uint("order_id", orderID),
			zap.Error(err))
	}
}

// notifyAdminPaid sends a best-effort Markdown notification about a confirmed
// payment to the configured administrator: purchase/renewal marker, tariff,
// amount, buyer link and order context. A delivery failure never fails the
// payment flow and is only logged.
func (o *OrderService) notifyAdminPaid(ctx context.Context, sub *database.Subscription, order *database.Order, product *database.Product, isRenewal bool) {
	o.notifyAdminMarkdown(ctx, formatAdminPaidAlert(sub, order, product, isRenewal), order.ID)
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

// formatMoneyCents renders an amount in cents as a readable value with a
// thousands separator and currency symbol/code. Whole amounts drop the
// fractional part ("2 300 ₽"); fractional amounts keep two digits ("2 300,50 ₽").
// XTR делегируется в utils.FormatStarsAmount: 1 Star = 1 minor unit, делить на 100 нельзя.
func formatMoneyCents(amountCents int64, currency string) string {
	if strings.EqualFold(strings.TrimSpace(currency), stars.Currency) {
		return utils.FormatStarsAmount(amountCents)
	}

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

// BuildPaidUserNotification produces the subscription presentation used by the bot
// "✅ Оплата подтверждена" screen. It returns the target Telegram ID and message
// text; delivery is intentionally left to the webhook layer so a send failure
// cannot roll back a paid order.
func (o *OrderService) BuildPaidUserNotification(ctx context.Context, order *database.Order) (int64, string, error) {
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

	text := fmt.Sprintf("🎉 *Добро пожаловать в Premium!*\n\n%s\n\n%s", PremiumBenefitsText, FormatSubscriptionMessage("✅ *Оплата подтверждена!*", "", traffic, SubscriptionURL(o.cfg, sub.SubscriptionID)))

	return sub.TelegramID, text, nil
}
