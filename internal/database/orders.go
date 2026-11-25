package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetOrderByID retrieves an order by its ID.
func (s *Service) GetOrderByID(ctx context.Context, id uint) (*Order, error) {
	var order Order

	result := s.db.WithContext(ctx).First(&order, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrOrderNotFound
		}

		return nil, fmt.Errorf("failed to get order: %w", result.Error)
	}

	return &order, nil
}

// GetOrderByProviderPaymentID finds an order by provider and external payment ID.
// Not-found results are normalized to ErrOrderNotFound for service-layer handling.
func (s *Service) GetOrderByProviderPaymentID(ctx context.Context, provider string, providerPaymentID string) (*Order, error) {
	var order Order

	result := s.db.WithContext(ctx).Where("payment_provider = ? AND provider_payment_id = ?", provider, providerPaymentID).First(&order)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get order by provider payment id: %w", result.Error)
	}

	return &order, nil
}

// HasOrdersByProvider сообщает, есть ли в базе хотя бы один заказ указанного
// провайдера. Стартовая проверка решает, нужен ли платёжный Telegram-инстанс
// Telegram Stars: без активного Stars-провайдера и без старых Stars-заказов
// (возвраты `/refund`) он не поднимается вовсе.
func (s *Service) HasOrdersByProvider(ctx context.Context, provider string) (bool, error) {
	var order Order

	result := s.db.WithContext(ctx).Select("id").Where("payment_provider = ?", provider).Limit(1).Find(&order)
	if result.Error != nil {
		return false, fmt.Errorf("failed to check orders by provider %s: %w", provider, result.Error)
	}

	return result.RowsAffected > 0, nil
}

const stalePendingOrderLookback = 30 * 24 * time.Hour

// GetStalePendingOrdersAfterID возвращает pending-заказы с истёкшим сроком
// платёжной ссылки (before - 30 дней <= payment_expires_at < before), начиная после afterID.
// Заказы с неопределённым созданием транзакции пропускаются: у провайдера
// может быть транзакция без сохранённого id, и такой заказ нельзя ни
// подтвердить, ни истечь автоматически.
func (s *Service) GetStalePendingOrdersAfterID(
	ctx context.Context, before time.Time, afterID uint, limit int,
) ([]Order, error) {
	var orders []Order

	result := s.db.WithContext(ctx).
		Where("status = ? AND payment_creation_uncertain = ?", OrderStatusPending, false).
		Where("payment_expires_at IS NOT NULL AND payment_expires_at >= ? AND payment_expires_at < ?", before.Add(-stalePendingOrderLookback), before).
		Where("id > ?", afterID).
		Order("id ASC").
		Limit(limit).
		Find(&orders)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to load stale pending orders: %w", result.Error)
	}

	return orders, nil
}

// ExpirePendingOrder переводит pending-заказ в expired. CAS по статусу:
// конкурентный callback, успевший оплатить заказ, не перезаписывается.
func (s *Service) ExpirePendingOrder(ctx context.Context, orderID uint) (bool, error) {
	result := s.db.WithContext(ctx).
		Model(&Order{}).
		Where("id = ? AND status = ?", orderID, OrderStatusPending).
		Update("status", OrderStatusExpired)
	if result.Error != nil {
		return false, fmt.Errorf("expire pending order %d: %w", orderID, result.Error)
	}

	return result.RowsAffected > 0, nil
}

// AttachProviderPaymentID привязывает id транзакции провайдера к заказу, у
// которого он ещё не записан. Это окно между CreateTransaction и
// SavePaymentDetails: callback провайдера может обогнать сохранение. CAS по
// пустому provider_payment_id не даёт перехватить уже подтверждённый заказ.
func (s *Service) AttachProviderPaymentID(
	ctx context.Context, orderID uint, provider string, providerPaymentID string,
) (bool, error) {
	result := s.db.WithContext(ctx).
		Model(&Order{}).
		Where("id = ? AND (provider_payment_id IS NULL OR provider_payment_id = '')", orderID).
		Updates(map[string]any{
			"provider_payment_id": providerPaymentID,
			"payment_provider":    provider,
		})
	if result.Error != nil {
		return false, fmt.Errorf("attach provider payment id: %w", result.Error)
	}

	return result.RowsAffected > 0, nil
}

// FindPendingPaymentOrder returns the current pending payment intent. An
// expired intent is terminalized, but no replacement is created by this method.
func (s *Service) FindPendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, provider string, now time.Time) (*Order, error) {
	var order Order

	result := s.db.WithContext(ctx).
		Where("subscription_id = ? AND product_id = ? AND payment_provider = ? AND status = ?", subscriptionID, productID, provider, OrderStatusPending).
		Order("id ASC").First(&order)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	if result.Error != nil {
		return nil, fmt.Errorf("find pending payment order: %w", result.Error)
	}

	if order.PaymentExpiresAt != nil && !now.Before(*order.PaymentExpiresAt) {
		result := s.db.WithContext(ctx).Model(&Order{}).
			Where("id = ? AND status = ?", order.ID, OrderStatusPending).
			Update("status", OrderStatusExpired)
		if result.Error != nil {
			return nil, fmt.Errorf("expire payment order: %w", result.Error)
		}

		if result.RowsAffected == 1 {
			order.Status = OrderStatusExpired

			// order_expired: время жизни платёжной ссылки истекло. Best-effort:
			// сбой журнала не должен ломать выдачу новой ссылки.
			_ = s.insertAuditLogBestEffort(ctx, AuditOrderExpired(&order))

			return &order, nil
		}
		// Another worker may have confirmed/canceled this order between the
		// lookup and the conditional update. Return the current row so the
		// caller cannot mistake the race for an absent intent and create a
		// second payment attempt for the same purchase.
		var current Order

		err := s.db.WithContext(ctx).First(&current, order.ID).Error
		if err != nil {
			return nil, fmt.Errorf("reload payment order after expiry race: %w", err)
		}

		return &current, nil
	}

	return &order, nil
}

// FindOrCreatePendingPaymentOrder returns the single pending payment intent for
// a subscription/product pair, expiring it first when its local payment-link
// deadline is at or before now. An intent with uncertain provider creation is
// reused even after that deadline. The partial unique index is the final
// concurrency guard; a losing creator reloads the winning pending intent.
// created is true only when a NEW pending order was inserted by this call;
// any reuse of an existing intent (including a concurrent reload) reports
// created=false. Expiry and replacement are transactional; expiry audit is
// best-effort after commit. Database errors are returned, including a failed
// concurrent reload.
func (s *Service) FindOrCreatePendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, provider string, amountCents int64, currency string, paymentExpiresAt *time.Time, now time.Time) (*Order, bool, error) {
	// In-process guard for providers the partial unique index 034 does not
	// cover (it filters payment_provider='platega'). One process, one SQLite
	// file — a mutex is enough to keep concurrent requests from creating
	// duplicate intents for Stars before the provider id exists.
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	var order Order
	// created фиксируется только успешным INSERT нового pending; reuse,
	// конкурентная перезагрузка и повторное чтение uncertain-intent дают false.
	created := false
	// expiredOrder фиксируется внутри tx, но аудит пишется ПОСЛЕ коммита:
	// best-effort INSERT из замыкания на SQLite дедлочится на write-lock
	// незакоммиченной транзакции, а при откате tx истечение отменилось бы —
	// записывать его в журнал было бы ложью.
	var expiredOrder *Order

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("subscription_id = ? AND product_id = ? AND payment_provider = ? AND status = ?", subscriptionID, productID, provider, OrderStatusPending).
			Order("id ASC").First(&order)
		if result.Error == nil {
			if order.PaymentCreationUncertain {
				return nil
			}

			if order.PaymentExpiresAt != nil && !now.Before(*order.PaymentExpiresAt) {
				err := tx.Model(&Order{}).Where("id = ? AND status = ?", order.ID, OrderStatusPending).
					Update("status", OrderStatusExpired).Error
				if err != nil {
					return fmt.Errorf("expire payment order: %w", err)
				}

				order.Status = OrderStatusExpired
				expired := order
				expiredOrder = &expired
				order = Order{}
			} else {
				return nil
			}
		} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("find pending payment order: %w", result.Error)
		}

		order = Order{
			SubscriptionID:   subscriptionID,
			ProductID:        productID,
			Status:           OrderStatusPending,
			AmountCents:      amountCents,
			Currency:         currency,
			PaymentProvider:  provider,
			PaymentExpiresAt: paymentExpiresAt,
			CreatedAt:        now,
		}

		err := tx.Create(&order).Error
		if err != nil {
			return fmt.Errorf("create pending payment order: %w", err)
		}

		created = true

		return nil
	})
	// Аудит order_expired — только после УСПЕШНОЙ фиксации транзакции
	// (см. expiredOrder выше): при откате tx истечение отменилось, и событие
	// в журнале было бы ложью.
	if err == nil && expiredOrder != nil {
		_ = s.insertAuditLogBestEffort(ctx, AuditOrderExpired(expiredOrder))
	}

	if err != nil {
		// A concurrent creator can win the partial unique-index race. The
		// transaction is rolled back, so read the winner and continue with it.
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "unique constraint") || strings.Contains(message, "idx_orders_pending_subscription_product_unique") {
			result := s.db.WithContext(ctx).
				Where("subscription_id = ? AND product_id = ? AND payment_provider = ? AND status = ?", subscriptionID, productID, provider, OrderStatusPending).
				Order("id ASC").First(&order)
			if result.Error == nil {
				return &order, false, nil
			}

			return nil, false, fmt.Errorf("reload concurrent pending payment order: %w", result.Error)
		}

		return nil, false, err
	}

	return &order, created, nil
}

// MarkPaymentCreationUncertain atomically marks whether an outbound provider
// request has an outcome that cannot safely be retried automatically.
func (s *Service) MarkPaymentCreationUncertain(ctx context.Context, orderID uint, uncertain bool) (bool, error) {
	query := s.db.WithContext(ctx).Model(&Order{}).Where("id = ? AND status = ?", orderID, OrderStatusPending)
	if uncertain {
		query = query.Where("payment_creation_uncertain = ? AND (provider_payment_id IS NULL OR provider_payment_id = '')", false)
	} else {
		query = query.Where("payment_creation_uncertain = ?", true)
	}

	result := query.Update("payment_creation_uncertain", uncertain)
	if result.Error != nil {
		return false, fmt.Errorf("mark payment creation uncertainty: %w", result.Error)
	}

	if claimed := result.RowsAffected == 1; claimed {
		if uncertain {
			// payment_creation_uncertain: исход запроса к провайдеру неизвестен,
			// требуется ручная сверка. Сигнальное событие для расследований.
			var order Order

			loadErr := s.db.WithContext(ctx).First(&order, orderID).Error
			if loadErr == nil {
				_ = s.insertAuditLogBestEffort(ctx, AuditPaymentCreationUncertain(&order))
			}
		}
	}

	return result.RowsAffected == 1, nil
}

// SavePaymentDetails stores the provider ID, link and local link deadline in a
// single update and clears the uncertainty flag.
func (s *Service) SavePaymentDetails(ctx context.Context, orderID uint, providerPaymentID string, paymentURL string, paymentExpiresAt time.Time) error {
	result := s.db.WithContext(ctx).Model(&Order{}).
		Where("id = ? AND status = ?", orderID, OrderStatusPending).
		Updates(map[string]any{
			"provider_payment_id":        providerPaymentID,
			"payment_url":                paymentURL,
			"payment_expires_at":         paymentExpiresAt.UTC(),
			"payment_creation_uncertain": false,
		})
	if result.Error != nil {
		return fmt.Errorf("save payment details: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("order %d is not pending: %w", orderID, ErrOrderNotFound)
	}

	return nil
}

// SaveOrderPaymentAmounts persists the actual amounts known only at callback
// time: what was really charged from the customer (callbackAmountCents,
// including any customer-paid provider fee) and, when the provider API
// answered, the provider fee. A nil providerFeeCents leaves the stored fee
// value untouched, so the first call can save the callback amount and a later
// best-effort call can add the fee without disturbing it.
func (s *Service) SaveOrderPaymentAmounts(ctx context.Context, orderID uint, callbackAmountCents int64, providerFeeCents *int64) error {
	// The callback amount is always written; the provider fee is added only
	// when supplied so a nil value cannot overwrite previously stored
	// commission data with NULL.
	updates := map[string]any{
		"callback_amount_cents": callbackAmountCents,
	}
	if providerFeeCents != nil {
		updates["provider_fee_cents"] = *providerFeeCents
	}

	err := s.db.WithContext(ctx).Model(&Order{}).Where("id = ?", orderID).Updates(updates).Error
	if err != nil {
		return fmt.Errorf("save order payment amounts: %w", err)
	}

	return nil
}

// GetPaidOrdersWithoutProviderFeeAfterID returns the next page of fee-sync
// candidates after afterID. Paging lets the worker continue past deferred
// orders without requiring a retry-state column.
func (s *Service) GetPaidOrdersWithoutProviderFeeAfterID(ctx context.Context, afterID uint, limit int) ([]Order, error) {
	var orders []Order

	query := s.db.WithContext(ctx).
		Where("status = ? AND payment_provider = ? AND provider_payment_id IS NOT NULL AND provider_payment_id <> '' AND provider_fee_cents IS NULL", OrderStatusPaid, PaymentProviderPlatega).
		Order("id ASC")
	if afterID > 0 {
		query = query.Where("id > ?", afterID)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}

	err := query.Find(&orders).Error
	if err != nil {
		return nil, fmt.Errorf("get paid orders without provider fee after id %d: %w", afterID, err)
	}

	return orders, nil
}

// ApplyPlanInTxFn is invoked inside the transaction that confirms an order.
// It must materialize all DB prerequisites (pending_add / pending_remove rows)
// needed by the background sync worker using the supplied tx handle.
// Returning an error aborts the transaction and rolls back the payment
// confirmation; returning nil commits it.
type ApplyPlanInTxFn func(ctx context.Context, tx *gorm.DB, subscriptionID uint, planID uint) error

// ConfirmOrderPaidCAS atomically marks an order paid and updates its subscription.
// The order and subscription expiry are both computed inside the transaction from
// the current subscription state, so the caller does not need to pre-calculate
// (and cannot pass a stale) expiry value. The OrderService may call this method
// for an expired order when its provider callback is still inside the short
// settlement grace period; the service validates that time window before
// entering this transaction. If applyPlan is non-nil, the CAS succeeds, and the
// plan changes, it is called with the same tx used to write the subscription.
// Payment and renewal audit events are written in that transaction as well.
// Returns true only when the transition commits, or false with no error if the
// order is no longer pending or expired. Missing rows return wrapped
// ErrSubscriptionNotFound or ErrOrderNotFound; database, audit, and applyPlan
// errors roll back the transaction and reach the caller. The supplied sub is
// updated in place before applyPlan and may remain changed after a rollback.
// Callers MUST NOT share that Subscription object between concurrent calls:
// the whole struct is written at the end of the transaction while another call
// may be reading its fields (ID, expiry) from the start.
func (s *Service) ConfirmOrderPaidCAS(ctx context.Context, p ConfirmPaidOrderParams) (bool, error) {
	var activated bool

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize every entitlement-changing payment transition on the
		// subscription row before touching its order. Chargeback acquires this
		// same lock first, so its active-coverage decision cannot be made from a
		// snapshot that races a concurrent confirmation.
		var currentSub Subscription

		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&currentSub, p.Subscription.ID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load subscription for payment: %w", ErrSubscriptionNotFound)
			}

			return fmt.Errorf("load subscription for payment: %w", err)
		}

		newExpiry := CalculatePaymentExpiry(p.ActivatedAt, &currentSub, p.Product)

		// An expired order is accepted here only for the provider callback grace
		// path documented above. The service layer checks the grace deadline;
		// keeping both statuses in the same conditional update preserves the
		// pending/expired -> paid transition as one atomic compare-and-swap.
		var order Order

		err = tx.First(&order, p.OrderID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load order for payment: %w", ErrOrderNotFound)
			}

			return fmt.Errorf("load order for payment: %w", err)
		}

		// Фактический id транзакции, который ляжет в БД: переданный charge ID
		// (Stars) либо уже сохранённый (Platega сохраняет его заранее).
		// Обрабатываем один раз: и снапшот заказа для аудита, и SQL-обновление
		// должны получить одно и то же значение.
		effectiveProviderPaymentID := p.ProviderPaymentID
		if strings.TrimSpace(effectiveProviderPaymentID) == "" {
			effectiveProviderPaymentID = order.ProviderPaymentID
		}
		order.ProviderPaymentID = effectiveProviderPaymentID

		updates := map[string]any{
			"status":                OrderStatusPaid,
			"paid_at":               p.PaidAt,
			"activated_at":          p.ActivatedAt,
			"expires_at":            newExpiry,
			"callback_amount_cents": p.CallbackAmountCents,
			// Пустое/пробельное значение не трогает сохранённое (Platega сохраняет
			// provider_payment_id через SavePaymentDetails заранее); непустой id
			// заменяет прежний. Значение уже нормализовано выше и совпадает со
			// снапшотом аудита.
			"provider_payment_id": gorm.Expr("COALESCE(NULLIF(?, ''), provider_payment_id)", effectiveProviderPaymentID),
		}
		if p.ProviderFeeCents != nil {
			updates["provider_fee_cents"] = *p.ProviderFeeCents
		}

		result := tx.Model(&Order{}).Where("id = ? AND status IN ?", p.OrderID, []OrderStatus{OrderStatusPending, OrderStatusExpired}).Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("confirm order: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return nil
		}

		result = tx.Model(&Subscription{}).Where("id = ?", p.Subscription.ID).Updates(map[string]any{
			"plan_id": p.Product.PlanID, "status": string(SubscriptionStatusActive),
			"expires_at": newExpiry, "product_id": p.Product.ID, "started_at": p.ActivatedAt,
			"price_paid_cents": p.Product.PriceCents, "currency": p.Product.Currency, "reminders_sent": 0,
		})
		if result.Error != nil {
			return fmt.Errorf("update subscription after payment: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf("update subscription after payment: %w", ErrSubscriptionNotFound)
		}

		// Состояние, фактически сохранённое апдейтом выше: единственный
		// достоверный источник new_state для аудита. currentSub — состояние ДО.
		productID := p.Product.ID
		startedAt := p.ActivatedAt
		expiryCopy := newExpiry
		currency := p.Product.Currency
		renewedSub := currentSub
		renewedSub.PlanID = p.Product.PlanID
		renewedSub.Status = string(SubscriptionStatusActive)
		renewedSub.ProductID = &productID
		renewedSub.ExpiresAt = &expiryCopy
		renewedSub.StartedAt = &startedAt
		renewedSub.PricePaidCents = p.Product.PriceCents
		renewedSub.Currency = &currency
		renewedSub.RemindersSent = 0

		// Аудит пишется в той же транзакции: событие не может потеряться при
		// сбое между изменением подписки и записью журнала. Откат транзакции
		// откатывает и событие — журнал отражает только зафиксированные факты.
		err = InsertAuditLogInTx(tx, AuditPaymentConfirmed(&order, &renewedSub, p.Product, newExpiry))
		if err != nil {
			return fmt.Errorf("audit payment confirmation: %w", err)
		}

		// Событие в таймлайне самой подписки: payment_confirmed адресован заказу,
		// а subscription_renewed показывает продление/активацию в истории подписки.
		err = InsertAuditLogInTx(tx, AuditSubscriptionRenewed(&currentSub, &renewedSub, p.Product, order.PaymentProvider))
		if err != nil {
			return fmt.Errorf("audit subscription renewal: %w", err)
		}

		// Caller получает то же состояние, что зафиксировано в БД.
		*p.Subscription = renewedSub

		// Plan reconciliation runs only when the purchased plan actually differs
		// from the subscription's current one. A same-plan renewal extends the
		// expiry but must NOT recreate pending_update rows: that path re-syncs
		// the VPN client and resets its traffic counter, which would wipe the
		// user's remaining quota mid-period. Renewals only touch the DB; the
		// panel keeps serving the client (its own 30-day auto-renew continues).
		if p.ApplyPlan != nil && currentSub.PlanID != p.Product.PlanID {
			err := p.ApplyPlan(ctx, tx, p.Subscription.ID, p.Product.PlanID)
			if err != nil {
				return fmt.Errorf("apply plan after payment: %w", err)
			}
		}

		activated = true

		return nil
	})
	if err != nil {
		return false, err
	}

	return activated, nil
}

// CalculatePaymentExpiry is the single source of truth for payment expiry
// computation shared by the order CAS and the service layer. When the current
// plan matches the purchased product's plan and the existing expiry is still in
// the future, the new expiry extends the existing one; otherwise it starts from
// the activation time. A nil product yields the base time unchanged.
func CalculatePaymentExpiry(now time.Time, sub *Subscription, product *Product) time.Time {
	base := now
	if sub != nil && product != nil && sub.PlanID == product.PlanID && sub.ExpiresAt != nil && sub.ExpiresAt.After(now) {
		base = *sub.ExpiresAt
	}

	if product == nil {
		return base
	}

	return base.AddDate(0, 0, product.DurationDays)
}

// ConfirmPaidOrderParams — параметры CAS-подтверждения оплаты (один tx:
// paid + charge id + fee + продление). Пустые строковые/nil-поля означают
// «не перезаписывать» (см. документацию метода).
type ConfirmPaidOrderParams struct {
	OrderID             uint
	PaidAt              time.Time
	ActivatedAt         time.Time
	Subscription        *Subscription
	Product             *Product
	ApplyPlan           ApplyPlanInTxFn
	CallbackAmountCents int64
	ProviderPaymentID   string // пусто = не перезаписывать
	ProviderFeeCents    *int64 // nil = не перезаписывать
}

// CancelPaidOrderParams — параметры CAS-отмены оплаченного заказа с downgrade.
type CancelPaidOrderParams struct {
	Provider          string
	ProviderPaymentID string
	Now               time.Time
	FreePlanID        uint
	ApplyPlan         DowngradePlanInTxFn // бывший ChargebackPlanInTxFn
	Audit             func(*Order, *Subscription, bool) *AuditLog
}

// DowngradePlanInTxFn materializes the DB-side VPN sync prerequisites while the
// downgrade transaction is open. It must not perform external network calls.
type DowngradePlanInTxFn func(ctx context.Context, tx *gorm.DB, subscriptionID uint, freePlanID uint) error

// CancelPaidOrderAndDowngradeCAS atomically records a paid-order chargeback (or
// refund, per auditFn) and, when no other currently-valid paid order covers the
// subscription, resets the subscription to the free plan and materializes its
// pending sync state. The auditFn event is journaled exactly once in the same
// transaction for every successful paid→canceled transition — including the
// branches without downgrade (subscription not active, or another active paid
// order exists) — with the downgraded flag reflecting the actual decision.
// A pending→canceled transition journals only order_canceled, never the
// chargeback/refund event.
//
// The order status, entitlement update, and pending node transitions share one
// transaction. A concurrent confirmation therefore cannot commit an entitlement
// after this chargeback's decision without first observing the serialized DB
// state. An auditFn error rolls back the whole transaction. External VPN calls
// must happen after this method returns successfully.
func (s *Service) CancelPaidOrderAndDowngradeCAS(ctx context.Context, p CancelPaidOrderParams) (*ChargebackResult, error) {
	var result ChargebackResult

	// nil → прежнее чарджбэк-событие: событие по умолчанию не должно зависеть
	// от того, передал ли caller явный конструктор.
	auditEvent := p.Audit
	if auditEvent == nil {
		auditEvent = AuditChargebackReceived
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order Order

		query := tx.Where("payment_provider = ? AND provider_payment_id = ?", p.Provider, p.ProviderPaymentID).First(&order)
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return nil
		}

		if query.Error != nil {
			return fmt.Errorf("find chargeback order: %w", query.Error)
		}

		// Use the same subscription lock order as ConfirmOrderPaidCAS. The lock
		// is acquired before changing this order or evaluating other paid orders,
		// which makes the chargeback coverage decision linearizable per user.
		var currentSub Subscription

		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&currentSub, order.SubscriptionID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load subscription for chargeback: %w", ErrSubscriptionNotFound)
			}

			return fmt.Errorf("load subscription for chargeback: %w", err)
		}

		// Re-read the p.Provider order after the subscription lock. A concurrent
		// confirmation may have changed it after the initial lookup.
		err = tx.First(&order, order.ID).Error
		if err != nil {
			return fmt.Errorf("reload chargeback order: %w", err)
		}

		result.Order = &order
		result.WasPaid = order.Status == OrderStatusPaid

		fromStatus := OrderStatusPending
		if result.WasPaid {
			fromStatus = OrderStatusPaid
		}

		updated := tx.Model(&Order{}).
			Where("id = ? AND status = ?", order.ID, fromStatus).
			Update("status", OrderStatusCanceled)
		if updated.Error != nil {
			return fmt.Errorf("cancel paid order: %w", updated.Error)
		}

		if updated.RowsAffected == 0 {
			return nil
		}

		result.Transitioned = true

		// Actor события заказа наследуется от платёжного события: admin refund
		// wrapper меняет actor на admin, chargeback остаётся payment_provider.
		// downgraded flag здесь ещё не известен — берём только actor.
		actorEvent := auditEvent(&order, &currentSub, false)
		if actorEvent == nil {
			return errors.New("audit order event is nil")
		}

		canceledEvent := AuditOrderCanceled(&order, actorEvent.ActorType)
		canceledEvent.ActorID = actorEvent.ActorID

		err = InsertAuditLogInTx(tx, canceledEvent)
		if err != nil {
			return fmt.Errorf("audit order cancellation: %w", err)
		}

		order.Status = OrderStatusCanceled
		if !result.WasPaid {
			// pending→canceled получает только order_canceled; событие
			// чарджбэка/возврата для него не журналируется.
			result.Order = &order
			return nil
		}

		downgraded := false

		// A chargeback must never resurrect a subscription that was already
		// revoked/canceled by another lifecycle flow. The order transition is
		// still committed, but only active subscriptions may be downgraded to
		// the active free plan below.
		if currentSub.Status == string(SubscriptionStatusActive) {
			var activePaid int64

			// Count the paid orders that still cover this subscription. The
			// order being charged back was already flipped to canceled above,
			// so it no longer counts: if it was the only paid order,
			// activePaid == 0 and the subscription is downgraded to the free
			// plan; if another active paid order exists, activePaid > 0 and
			// access is preserved.
			err = tx.Model(&Order{}).
				Where("subscription_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", order.SubscriptionID, OrderStatusPaid, p.Now).
				Count(&activePaid).Error
			if err != nil {
				return fmt.Errorf("check active paid coverage: %w", err)
			}

			if activePaid == 0 {
				updated = tx.Model(&Subscription{}).Where("id = ?", order.SubscriptionID).Updates(map[string]any{
					"status":           string(SubscriptionStatusActive),
					"expires_at":       nil,
					"plan_id":          p.FreePlanID,
					"product_id":       nil,
					"started_at":       nil,
					"price_paid_cents": 0,
					"currency":         nil,
				})
				if updated.Error != nil {
					return fmt.Errorf("downgrade subscription after chargeback: %w", updated.Error)
				}

				if updated.RowsAffected == 0 {
					return fmt.Errorf("downgrade subscription after chargeback: %w", ErrSubscriptionNotFound)
				}

				if p.ApplyPlan != nil {
					err := p.ApplyPlan(ctx, tx, order.SubscriptionID, p.FreePlanID)
					if err != nil {
						return fmt.Errorf("apply free plan after chargeback: %w", err)
					}
				}

				downgraded = true
			}
		}

		// Чарджбэк/возврат журналируется в той же транзакции ровно один раз для
		// любого успешного paid→canceled перехода — даже когда downgrade не
		// потребовался. Ошибка аудита откатывает отмену и downgrade.
		orderEvent := auditEvent(&order, &currentSub, downgraded)
		if orderEvent == nil {
			return errors.New("audit chargeback event is nil")
		}

		err = InsertAuditLogInTx(tx, orderEvent)
		if err != nil {
			return fmt.Errorf("audit chargeback: %w", err)
		}

		// Timeline адресуемой подписки: downgrade фиксируется событием
		// downgraded_to_free, actor наследуется от события заказа
		// (admin refund → admin, chargeback → payment_provider).
		if downgraded {
			reason := "chargeback_received"
			if orderEvent.Event == AuditEventPaymentRefunded {
				reason = "payment_refunded"
			}

			timeline := AuditDowngradedToFree(&currentSub, reason)
			timeline.ActorType = orderEvent.ActorType
			timeline.ActorID = orderEvent.ActorID
			if strings.TrimSpace(order.ProviderPaymentID) != "" {
				var timelineDetails map[string]any
				jsonErr := json.Unmarshal([]byte(timeline.Details), &timelineDetails)
				if jsonErr == nil {
					timelineDetails["provider_payment_id"] = order.ProviderPaymentID
					timeline.Details = marshalAuditDetails(timelineDetails)
				}
			}

			err = InsertAuditLogInTx(tx, timeline)
			if err != nil {
				return fmt.Errorf("audit downgrade to free: %w", err)
			}
		}

		result.Downgraded = downgraded
		result.Order = &order

		return nil
	})
	if err != nil {
		return nil, err
	}

	if result.Order == nil {
		return nil, nil
	}

	return &result, nil
}

// CancelOrderCAS cancels an order only from one of the supplied states. It
// returns false without error when the callback is an idempotent no-op.
func (s *Service) CancelOrderCAS(ctx context.Context, provider string, providerPaymentID string, fromStatuses []OrderStatus) (bool, error) {
	result := s.db.WithContext(ctx).Model(&Order{}).Where("payment_provider = ? AND provider_payment_id = ? AND status IN ?", provider, providerPaymentID, fromStatuses).Update("status", OrderStatusCanceled)
	if result.Error != nil {
		return false, fmt.Errorf("cancel order: %w", result.Error)
	}

	if result.RowsAffected > 0 {
		// order_canceled (не-чарджбэк ветка: провайдер отменил оплату до/после
		// попытки). Чарджбэк-ветка пишет свой аудит в tx CancelPaidOrderAndDowngradeCAS.
		order, loadErr := s.GetOrderByProviderPaymentID(ctx, provider, providerPaymentID)
		if loadErr == nil {
			_ = s.insertAuditLogBestEffort(ctx, AuditOrderCanceled(order, ActorPaymentProvider))
		}
	}

	return result.RowsAffected > 0, nil
}

// GetOrdersBySubscriptionID returns orders for the given subscription.
func (s *Service) GetOrdersBySubscriptionID(ctx context.Context, subscriptionID uint) ([]Order, error) {
	var orders []Order

	result := s.db.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		Order("created_at DESC").
		Find(&orders)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list orders: %w", result.Error)
	}

	return orders, nil
}
