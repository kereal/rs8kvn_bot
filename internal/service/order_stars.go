package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ErrStarsRefundStatePending — sentinel split-brain сценария Stars refund:
// Telegram деньги вернул, локальное состояние заказа (free-plan/CAS) — нет.
// errors.Is(err, ErrStarsRefundStatePending) работает через Unwrap (Join).
var ErrStarsRefundStatePending = errors.New("stars refund: provider refund applied, order state update failed")

// StarsRefundStateError сигнализирует, что Stars уже возвращены Telegram'ом,
// а состояние заказа требует reconciliation. Обнаруживается errors.As(&T);
// повторный вызов RefundStarsOrder для того же charge ID — идемпотентный
// no-op (ErrAlreadyRefunded) и безопасен.
type StarsRefundStateError struct {
	OrderID        uint
	SubscriptionID uint
	TelegramID     int64
	ChargeID       string
	Err            error
}

func (e *StarsRefundStateError) Error() string {
	return fmt.Sprintf("stars refund order %d (charge %s): %v: Stars возвращены, состояние заказа требует reconciliation", e.OrderID, e.ChargeID, e.Err)
}

// Unwrap сохраняет и sentinel (errors.Is ErrStarsRefundStatePending), и
// исходную причину (errors.Is на free-plan/CAS ошибку).
func (e *StarsRefundStateError) Unwrap() []error {
	return []error{ErrStarsRefundStatePending, e.Err}
}

// StarsSemanticMismatchError — typed semantic mismatch транзакции
// getStarTransactions: payload ссылается на удалённый/неподходящий заказ
// (not found, чужая валюта/сумма/владелец, недопустимый переход). Retry
// reconciliation состояние не изменит — деньгу нужно вернуть автоматически.
// Не является DB/infrastructure ошибкой.
type StarsSemanticMismatchError struct {
	ChargeID   string
	Payload    string
	TelegramID int64
	Err        error
}

func (e *StarsSemanticMismatchError) Error() string {
	return fmt.Sprintf("stars transaction %s (payload %q) does not match any order: %v", e.ChargeID, e.Payload, e.Err)
}

func (e *StarsSemanticMismatchError) Unwrap() error { return e.Err }

// requestStarsPayment обрабатывает покупку через Telegram Stars.
// Вызывается из requestPayment после резолва подписки, до Platega-логики.
// Ренты в Stars не отправляются: валюта обязана быть XTR с положительной ценой.
func (o *OrderService) requestStarsPayment(ctx context.Context, telegramID int64, canonical *database.Product, sub *database.Subscription) (*PaymentInfo, *database.Order, error) {
	if o.stars == nil {
		return nil, nil, ErrPaymentDisabled
	}

	if !strings.EqualFold(canonical.Currency, stars.Currency) || canonical.PriceCents <= 0 {
		return nil, nil, errors.New("invalid paid product request")
	}

	if !canonical.IsActive {
		return nil, nil, errors.New("invalid paid product request")
	}

	plan, err := o.db.GetPlanByID(ctx, canonical.PlanID)
	if err != nil {
		o.notifyRequestIssue(ctx, "stars_load_plan_failed", err.Error(), "retry after database recovery", telegramID, canonical, nil)

		return nil, nil, fmt.Errorf("load stars product plan: %w", err)
	}

	if plan == nil || !plan.IsActive {
		return nil, nil, errors.New("product plan is inactive")
	}

	now := time.Now().UTC()

	// TTL для Stars-invoice. Устанавливается атомарно внутри той же tx, что и INSERT,
	// чтобы pending с payment_expires_at IS NULL не блокировал покупку навсегда.
	ttl := config.DefaultStarsInvoiceTTL
	if o.cfg != nil && o.cfg.StarsInvoiceTTL > 0 {
		ttl = o.cfg.StarsInvoiceTTL
	}

	ttlAt := now.Add(ttl)

	order, created, err := o.db.FindOrCreatePendingPaymentOrder(ctx, sub.ID, canonical.ID, database.PaymentProviderTelegramStars, canonical.PriceCents, canonical.Currency, &ttlAt, now)
	if err != nil {
		o.notifyRequestIssue(ctx, "stars_create_pending_failed", err.Error(), "retry after database recovery", telegramID, canonical, nil)
		return nil, nil, fmt.Errorf("find or create stars pending order: %w", err)
	}

	// reuse — всегда ошибка «уже идёт оплата»: победителя определяет
	// репозиторий (created flag), а не эвристика по CreatedAt.
	if !created {
		return nil, order, ErrPaymentAlreadyInProgress
	}

	o.logAuditOrderCreated(ctx, order)

	var expiresAt time.Time
	if order.PaymentExpiresAt != nil {
		expiresAt = *order.PaymentExpiresAt
	}

	return &PaymentInfo{Provider: database.PaymentProviderTelegramStars, ExpiresAt: expiresAt}, order, nil
}

// ValidateStarsPreCheckout проверяет pre_checkout_query без побочных эффектов:
// nil — счёт можно оплачивать, ошибка — OK=false с этой причиной (§5.2).
func (o *OrderService) ValidateStarsPreCheckout(ctx context.Context, payload string, telegramID int64, amount int64, currency string) error {
	order, err := o.validateStarsPayload(ctx, payload, telegramID, amount, currency)
	if err != nil {
		return err
	}

	// Повторная оплата того же invoice не пройдёт: paid/canceled отклоняются.
	// ConfirmStarsPayment статус проверяет отдельно (paid → no-op, canceled → alert).
	if order.Status != database.OrderStatusPending && order.Status != database.OrderStatusExpired {
		return fmt.Errorf("stars order %d has status %s: %w", order.ID, order.Status, ErrInvalidPaymentTransition)
	}

	return nil
}

// validateStarsPayload — общая валидация payload successful_payment/pre_checkout:
// заказ по payload, provider/currency/amount/владелец, статус ∈ {pending, expired}.
func (o *OrderService) validateStarsPayload(ctx context.Context, payload string, telegramID int64, amount int64, currency string) (*database.Order, error) {
	orderID, convErr := strconv.ParseUint(strings.TrimSpace(payload), 10, 64)
	if convErr != nil {
		return nil, database.ErrOrderNotFound
	}

	order, err := o.db.GetOrderByID(ctx, uint(orderID))
	if err != nil {
		return nil, fmt.Errorf("load stars order %d: %w", orderID, err)
	}

	if order.PaymentProvider != database.PaymentProviderTelegramStars {
		// Не-Stars charge для не-Stars заказа — несентинельная ошибка данных,
		// ретраем не исправится: recovery-сверка классифицирует её как semantic
		// mismatch (деньги возвращаются автоматически), иначе страница
		// getStarTransactions залипает навсегда (см. ReconcileStarsTransactions).
		return nil, fmt.Errorf("order %d is not a Telegram Stars order: %w", order.ID, database.ErrOrderNotFound)
	}

	if !strings.EqualFold(order.Currency, stars.Currency) || !strings.EqualFold(currency, stars.Currency) {
		return nil, ErrCurrencyMismatch
	}

	// XTR не масштабируется ×100: сумма сравнивается с точным равенством (D5).
	if amount != order.AmountCents {
		return nil, ErrAmountMismatch
	}

	sub, err := o.db.GetByID(ctx, order.SubscriptionID)
	if err != nil && !errors.Is(err, database.ErrSubscriptionNotFound) {
		return nil, fmt.Errorf("load stars order subscription %d: %w", order.SubscriptionID, err)
	}

	// Подписка физически удалена (admin /del): несентинельная ошибка данных
	// залипла бы страницу recovery-сверки — классифицируем как mismatch.
	if err != nil || sub == nil {
		return nil, fmt.Errorf("load stars order subscription %d: subscription is nil: %w", order.SubscriptionID, database.ErrSubscriptionNotFound)
	}

	if sub.TelegramID != telegramID {
		return nil, fmt.Errorf("stars order %d belongs to a different telegram user: %w", order.ID, database.ErrOrderNotFound)
	}

	return order, nil
}

// ConfirmStarsPayment обрабатывает successful_payment: те же проверки, что и
// pre-checkout, затем один CAS (status=paid + charge id + fee=0 в одной tx).
// Повторная доставка — идемпотентный no-op; canceled — alert без изменения БД.
func (o *OrderService) ConfirmStarsPayment(ctx context.Context, chargeID string, payload string, amount int64, currency string, telegramID int64) (*PaymentConfirmation, error) {
	start := time.Now()
	confirmation, err := o.confirmStarsPayment(ctx, chargeID, payload, amount, currency, telegramID)

	recordPaymentConfirmationMetrics(start, confirmation, err)

	return confirmation, err
}

func (o *OrderService) confirmStarsPayment(ctx context.Context, chargeID string, payload string, amount int64, currency string, telegramID int64) (*PaymentConfirmation, error) {
	if o.stars == nil {
		return nil, ErrPaymentDisabled
	}

	if strings.TrimSpace(chargeID) == "" {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "invalid_provider_id", Reason: "stars payment charge ID is empty", Action: "reject callback and inspect Telegram payload", CallbackStatus: "successful_payment"})
		return nil, errors.New("invalid stars payment charge ID")
	}

	// Per-order serialization по charge ID, detachCtx — как в confirmPayment:
	// разрыв webhook-соединения не отменяет lock и первичный lookup.
	detachCtx := context.WithoutCancel(ctx)
	unlock, err := o.lockPayment(detachCtx, chargeID)
	if err != nil {
		logger.Warn("could not acquire per-order payment lock", zap.String("provider_payment_id", chargeID), zap.Error(err))
		return nil, fmt.Errorf("acquire payment lock: %w", err)
	}
	defer unlock()

	order, err := o.validateStarsPayload(detachCtx, payload, telegramID, amount, currency)
	if err != nil {
		// Деньги списаны, но заказ не подошёл: нужен ручной разбор/возврат.
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_payment_unmatched", Reason: err.Error(), Action: "verify the payment and refund or reconcile manually", ProviderID: chargeID, CallbackStatus: "successful_payment", Payload: payload})

		// Semantic mismatch (заказ не найден/не Stars/чужая валюта/сумма/
		// владелец/удалённая подписка/переход) не исправится ретраем — recovery
		// помечает его typed error'ом и возвращает charge автоматически.
		// DB/infrastructure ошибки идут как есть.
		if errors.Is(err, database.ErrOrderNotFound) || errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, ErrAmountMismatch) || errors.Is(err, ErrCurrencyMismatch) || errors.Is(err, ErrInvalidPaymentTransition) {
			return nil, &StarsSemanticMismatchError{ChargeID: chargeID, Payload: payload, TelegramID: telegramID, Err: err}
		}

		return nil, err
	}

	if order.Status == database.OrderStatusPaid {
		if strings.TrimSpace(order.ProviderPaymentID) == chargeID {
			// Повторная доставка successful_payment: без второго продления.
			return &PaymentConfirmation{Order: order}, nil
		}

		// Новая транзакция Telegram для уже оплаченного заказа: деньги за ту же
		// покупку списаны дважды — новый charge автоматически возвращается,
		// entitlement второй раз не меняется.
		autoRefunded, refundErr := o.autoRefundStarsCharge(ctx, order, telegramID, order.AmountCents, order.Currency, chargeID, "paid_order_new_charge")
		if refundErr != nil {
			// Журнал аудита недоступен — решение о возврате не принято.
			// Ошибка наружу: recovery повторит транзакцию и вернёт charge.
			return nil, refundErr
		}

		return &PaymentConfirmation{Order: order, AutoRefunded: autoRefunded, DuplicateCharge: true}, nil
	}

	if order.Status == database.OrderStatusCanceled {
		// Заказ отменён и уже возвращён по этому же charge: повторная доставка
		// successful_payment — идемпотентный no-op без повторного возврата.
		// Деньги у покупателя (refund уже применён) — сообщаем об этом, а не
		// о «списании по отменённому заказу».
		if strings.TrimSpace(order.ProviderPaymentID) == chargeID {
			return &PaymentConfirmation{Order: order, Activated: false, AlreadyRefunded: true}, nil
		}

		canceledSub, subErr := o.db.GetByID(ctx, order.SubscriptionID)
		canceledTelegramID := telegramID
		if subErr == nil && canceledSub != nil {
			canceledTelegramID = canceledSub.TelegramID
		}

		// Заказ отменён, но Telegram списал Stars новым charge: деньги
		// возвращаются автоматически. При сбое возврата — alert для ручного
		// возврата.
		autoRefunded, refundErr := o.autoRefundStarsCharge(ctx, order, canceledTelegramID, order.AmountCents, order.Currency, chargeID, "canceled_order_new_charge")
		if refundErr != nil {
			return nil, refundErr
		}

		return &PaymentConfirmation{Order: order, Activated: false, AutoRefunded: autoRefunded, DuplicateCharge: true}, nil
	}

	now := time.Now().UTC()

	if o.syncSvc == nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "payment_sync_not_ready", Reason: "post-commit synchronization service is not configured", Action: "wire SyncService before enabling payment callbacks", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})
		return nil, ErrPaymentSyncNotReady
	}

	product, err := o.db.GetProductByID(ctx, order.ProductID)
	if err != nil {
		if errors.Is(err, database.ErrProductNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("stars payment for deleted product", zap.Uint("order_id", order.ID), zap.Uint("product_id", order.ProductID), zap.String("provider_payment_id", chargeID))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_product_failed", Reason: "ordered product no longer exists", Action: "verify payment and refund or activate manually", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})
			// Товар не существует — услугу оказать нельзя: деньги возвращаем
			// автоматически (заказ pending без charge ID, /refund его не найдёт).
			// Это не дубль оплаты: charge единственный. Если возврат не удался —
			// bot-слой отправляет покупателю в /paysupport (Order == nil).
			autoRefunded, refundErr := o.autoRefundStarsCharge(ctx, order, telegramID, order.AmountCents, order.Currency, chargeID, "product_deleted")
			if refundErr != nil {
				return nil, refundErr
			}

			return &PaymentConfirmation{Activated: false, AutoRefunded: autoRefunded}, nil
		}

		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_product_failed", Reason: err.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})
		return nil, fmt.Errorf("get ordered product: %w", err)
	}

	sub, err := o.db.GetByID(ctx, order.SubscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("stars payment for deleted subscription", zap.Uint("order_id", order.ID), zap.Uint("subscription_id", order.SubscriptionID), zap.String("provider_payment_id", chargeID))
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_subscription_failed", Reason: "ordered subscription no longer exists", Action: "verify payment and refund or reconcile manually", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})
			// Подписка удалена (admin /del) — активировать нечего: деньги
			// возвращаем автоматически (заказ pending без charge ID, /refund
			// его не найдёт). Это не дубль оплаты: charge единственный.
			autoRefunded, refundErr := o.autoRefundStarsCharge(ctx, order, telegramID, order.AmountCents, order.Currency, chargeID, "subscription_deleted")
			if refundErr != nil {
				return nil, refundErr
			}

			return &PaymentConfirmation{Activated: false, AutoRefunded: autoRefunded}, nil
		}

		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "load_order_subscription_failed", Reason: err.Error(), Action: "retry callback after database recovery", OrderID: order.ID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})
		return nil, fmt.Errorf("get ordered subscription: %w", err)
	}

	isRenewal := sub.PricePaidCents > 0 || sub.ProductID != nil

	var applyPlan database.ApplyPlanInTxFn
	if o.syncSvc != nil {
		applyPlan = o.syncSvc.ApplyPlanToSubscriptionInTx
	}

	providerFee := int64(0)
	activated, err := o.db.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, applyPlan, amount, chargeID, &providerFee)
	if err != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "confirm_payment_failed", Reason: err.Error(), Action: "retry callback; order must remain pending if DB setup rolled back", OrderID: order.ID, TelegramID: sub.TelegramID, ProductID: order.ProductID, ProductName: product.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})
		return nil, err
	}

	if !activated {
		// CAS-гонка: заказ ушёл из pending между чтением и CAS — его оплатил
		// другой charge (или отменил /refund). Проигравший charge возвращаем
		// сразу: деньги списаны без активации, и без этого он бы висел до
		// recovery-сверки с ложным «оплата по отменённому заказу».
		current, rerr := o.db.GetOrderByID(ctx, order.ID)
		if rerr != nil {
			// Ошибка перечитывания: статус заказа неизвестен, возврат небезопасен.
			o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_confirm_reread_failed", Reason: rerr.Error(), Action: "retry callback after database recovery; the charge was not refunded", OrderID: order.ID, TelegramID: sub.TelegramID, ProductID: order.ProductID, ProductName: product.Name, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})

			return nil, fmt.Errorf("reload stars order %d after lost CAS race: %w", order.ID, rerr)
		}

		if current.Status == database.OrderStatusPaid || current.Status == database.OrderStatusCanceled {
			autoRefunded, refundErr := o.autoRefundStarsCharge(ctx, current, sub.TelegramID, current.AmountCents, current.Currency, chargeID, "cas_loser")
			if refundErr != nil {
				return nil, refundErr
			}

			return &PaymentConfirmation{Order: current, Activated: false, AutoRefunded: autoRefunded, DuplicateCharge: true}, nil
		}

		// Неожиданный статус (например expired) — конфликт перехода: молчаливое
		// «успешное» подтверждение здесь скрывало бы потерю оплаты.
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_confirm_unexpected_status", Reason: fmt.Sprintf("order %d has status %s after lost CAS race", current.ID, current.Status), Action: "verify the payment and reconcile the order manually", OrderID: current.ID, TelegramID: sub.TelegramID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})

		return nil, fmt.Errorf("stars order %d has unexpected status %s after lost CAS race: %w", current.ID, current.Status, ErrInvalidPaymentTransition)
	}

	if activated {
		// Снапшот заказа должен содержать фактический charge ID, записанный CAS'ом.
		order.ProviderPaymentID = chargeID
		mirrorOrderFromSubscriptionAfterCAS(order, sub, now)
	}

	return o.confirmPaidOrderTail(ctx, order, sub, product, isRenewal, activated, unlock)
}

// autoRefundStarsCharge возвращает новый charge ID для уже оплаченного или
// отменённого Stars-заказа (двойная оплата той же покупки). Entitlement не
// меняется. Возвращает true если refund применён (успех или ErrAlreadyRefunded).
// order может быть nil (recovery-сверка по charge ID) — тогда аудит пишется
// на system-сущность. reason уточняет ветку дубля для админ-алерта и аудита.
//
// Второе значение — ошибка инфраструктуры: журнал аудита недоступен, поэтому
// решение о возврате принять нельзя. Вызывающий обязан вернуть ошибку
// наружу, иначе recovery посчитает транзакцию обработанной и charge уже
// исполненного заказа останется без возврата (R8 плана).
func (o *OrderService) autoRefundStarsCharge(ctx context.Context, order *database.Order, telegramID int64, amountCents int64, currency string, chargeID string, reason string) (bool, error) {
	orderID := uint(0)
	if order != nil {
		orderID = order.ID
	}

	// Recovery после рестарта стартует с offset=0 и видит уже исполненный заказ
	// как unmatched: возврат такого charge вернул бы деньги за оплаченную услугу.
	// Проверяем audit: если charge уже подтверждался — деньги не возвращаем.
	confirmed, auditErr := o.db.HasPaymentConfirmedByProviderPaymentID(ctx, database.PaymentProviderTelegramStars, chargeID)
	if auditErr != nil {
		// Fail-closed: при недоступности журнала решение о возврате принять нельзя.
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_charge_audit_check_failed", Reason: auditErr.Error(), Action: "retry the duplicate-charge decision after database recovery", OrderID: orderID, TelegramID: telegramID, AmountCents: amountCents, Currency: currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})

		return false, fmt.Errorf("check payment confirmed audit for charge %s: %w", chargeID, auditErr)
	}

	if confirmed {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_charge_already_confirmed", Reason: "charge was already confirmed for an executed order", Action: "do not refund; verify the payment manually if money was taken twice", OrderID: orderID, TelegramID: telegramID, AmountCents: amountCents, Currency: currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})

		return false, nil
	}

	refundErr := o.stars.Refund(ctx, telegramID, chargeID)
	if refundErr == nil || errors.Is(refundErr, stars.ErrAlreadyRefunded) {
		// Возврат применён — фиксируем его в audit_log best-effort: ошибка
		// журнала не роняет бизнес-операцию.
		o.logAuditAutoRefund(ctx, order, telegramID, amountCents, currency, chargeID, reason)

		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_duplicate_charge_refunded", OrderID: orderID, TelegramID: telegramID, AmountCents: amountCents, Currency: currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})

		return true, nil
	}

	o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_duplicate_charge_refund_failed", Reason: refundErr.Error(), Action: "refund this duplicate charge manually via Bot API refundStarPayment using provider_payment_id", OrderID: orderID, TelegramID: telegramID, AmountCents: amountCents, Currency: currency, ProviderID: chargeID, CallbackStatus: "successful_payment"})

	return false, nil
}

// logAuditAutoRefund пишет событие payment_auto_refunded best-effort.
func (o *OrderService) logAuditAutoRefund(ctx context.Context, order *database.Order, telegramID int64, amountCents int64, currency string, chargeID string, reason string) {
	err := o.db.InsertAuditLog(ctx, database.AuditPaymentAutoRefunded(order, telegramID, amountCents, currency, chargeID, reason))
	if err != nil {
		logger.Warn("audit log write failed",
			zap.String("event", database.AuditEventPaymentAutoRefunded),
			zap.String("provider_payment_id", chargeID),
			zap.Error(err))
	}
}

// RefundStarsOrder возвращает Stars-платёж: сначала Telegram refund, затем
// CAS paid→canceled + downgrade с audit payment_refunded в той же tx (§5.4).
// Непустой результат означает применённый refund и содержит данные покупателя
// для уведомления; nil означает идемпотентный no-op.
func (o *OrderService) RefundStarsOrder(ctx context.Context, orderID uint) (*StarsRefundResult, error) {
	if o.stars == nil {
		return nil, ErrPaymentDisabled
	}

	order, err := o.db.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("load stars order %d for refund: %w", orderID, err)
	}

	if order.PaymentProvider != database.PaymentProviderTelegramStars {
		return nil, fmt.Errorf("order %d is not a Telegram Stars order", orderID)
	}

	if order.Status == database.OrderStatusCanceled {
		// Идемпотентность по БД: заказ уже отменён — телеграм не дёргаем.
		return nil, nil
	}

	if order.Status != database.OrderStatusPaid {
		return nil, fmt.Errorf("order %d cannot be refunded from status %s", orderID, order.Status)
	}

	chargeID := strings.TrimSpace(order.ProviderPaymentID)
	if chargeID == "" {
		return nil, fmt.Errorf("order %d has no provider payment id", orderID)
	}

	refundAmountCents := order.AmountCents
	refundCurrency := order.Currency

	sub, err := o.db.GetByID(ctx, order.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("load subscription for stars refund: %w", err)
	}

	if sub == nil {
		return nil, fmt.Errorf("load subscription for stars refund: subscription %d is nil", order.SubscriptionID)
	}

	// Free-plan грузится ДО возврата Telegram: если план недоступен, деньги
	// ещё не возвращены и split-brain не возникает — обычная ошибка, БД не трогаем.
	freePlan, planErr := o.db.GetPlanByName(ctx, database.FreePlanName)
	if planErr != nil {
		return nil, fmt.Errorf("load free plan for stars refund: %w", planErr)
	}

	if freePlan == nil {
		return nil, fmt.Errorf("load free plan for stars refund: plan %q is nil", database.FreePlanName)
	}

	refundErr := o.stars.Refund(ctx, sub.TelegramID, chargeID)
	if refundErr != nil && !errors.Is(refundErr, stars.ErrAlreadyRefunded) {
		// Telegram не вернул деньги: БД не трогаем, доступ сохраняется.
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_refund_failed", Reason: refundErr.Error(), Action: "check the bot Stars balance and retry /refund", OrderID: order.ID, TelegramID: sub.TelegramID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID})
		return nil, fmt.Errorf("stars refund order %d: %w", orderID, refundErr)
	}

	auditEvent := database.AuditPaymentRefunded
	if adminID := adminTelegramIDFromCtx(ctx); adminID > 0 {
		auditEvent = func(order *database.Order, sub *database.Subscription, downgraded bool) *database.AuditLog {
			event := database.AuditPaymentRefunded(order, sub, downgraded)
			actorID := strconv.FormatInt(adminID, 10)
			event.ActorType = database.ActorAdmin
			event.ActorID = &actorID

			return event
		}
	}

	result, casErr := o.db.CancelPaidOrderAndDowngradeCAS(ctx, database.PaymentProviderTelegramStars, chargeID, time.Now().UTC(), freePlan.ID, o.chargebackPlanInTx(), auditEvent)
	if casErr != nil {
		o.NotifyPaymentIssue(ctx, PaymentIssue{Event: "stars_refund_state_failed", Reason: casErr.Error(), Action: "reconcile order and subscription manually by charge id: money is already refunded", OrderID: order.ID, TelegramID: sub.TelegramID, ProductID: order.ProductID, SubscriptionID: order.SubscriptionID, AmountCents: order.AmountCents, Currency: order.Currency, ProviderID: chargeID})

		// Split-brain: Telegram деньги вернул, CAS в БД не применился.
		return nil, &StarsRefundStateError{OrderID: order.ID, SubscriptionID: order.SubscriptionID, TelegramID: sub.TelegramID, ChargeID: chargeID, Err: casErr}
	}

	if result == nil || !result.Transitioned {
		// Конкурентный refund/chargeback уже отменил заказ — идемпотентный no-op.
		return nil, nil
	}

	order = result.Order

	if result.Downgraded {
		// Кэш подписки инвалидируется до VPN-синка: клиенты не должны получать
		// оплаченный формат из кэша после возврата денег (образец — ветка
		// успешной оплаты в order_platega.go).
		if o.subSvc != nil && sub.SubscriptionID != "" {
			o.subSvc.InvalidateBySubID(ctx, sub.SubscriptionID)
		}

		o.syncChargebackAfterCommit(ctx, order.SubscriptionID)
	}

	refundSub, refundProduct := o.loadChargebackAlertContext(ctx, order)
	o.notifyAdminRefund(ctx, refundSub, order, refundProduct, result.Downgraded)

	return &StarsRefundResult{
		OrderID:     order.ID,
		TelegramID:  sub.TelegramID,
		AmountCents: refundAmountCents,
		Currency:    refundCurrency,
		Downgraded:  result.Downgraded,
	}, nil
}

// notifyAdminRefund отправляет админу best-effort refund-алерт по Stars-заказу.
func (o *OrderService) notifyAdminRefund(ctx context.Context, sub *database.Subscription, order *database.Order, product *database.Product, downgraded bool) {
	if o.adminBot == nil || o.cfg == nil || o.cfg.TelegramAdminID <= 0 {
		return
	}

	msg := tgbotapi.NewMessage(o.cfg.TelegramAdminID, formatAdminRefundAlert(sub, order, product, downgraded))
	msg.ParseMode = "Markdown"

	_, err := o.adminBot.Send(msg)
	if err != nil {
		logger.Warn("failed to notify admin about stars refund", zap.Uint("order_id", order.ID), zap.Error(err))
	}
}

// formatAdminRefundAlert рендерит админ-уведомление о возврате Stars-платежа
// через общий форматтер возврата/чарджбэка.
func formatAdminRefundAlert(sub *database.Subscription, order *database.Order, product *database.Product, downgraded bool) string {
	return formatAdminPaymentAlert("↩️ *Возврат Stars-платежа*", sub, order, product, downgraded)
}
