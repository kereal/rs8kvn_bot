package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"go.uber.org/zap"
)

// pendingOrderGrace — запас поверх срока платёжной ссылки: sweep не должен
// опережать webhook, который провайдер мог отправить с задержкой.
const pendingOrderGrace = 10 * time.Minute

// reconcilePendingBudget ограничивает один проход реконсиляции: зависший
// провайдер не должен растягивать тик планировщика.
const reconcilePendingBudget = 60 * time.Second

// ReconcilePendingOrders находит pending-заказы, чья платёжная ссылка истекла
// с запасом, и сверяет их с провайдером: CONFIRMED — активирует, явный
// терминальный отказ провайдера — истекает локально. Выборка охватывает
// последние 30 дней относительно границы с запасом; более старые pending-заказы
// требуют отдельной проверки оператором.
func (o *OrderService) ReconcilePendingOrders(ctx context.Context, limit int) error {
	// Верхнего guard по o.payment нет: Stars-pending истекает локально и без
	// Platega-клиента (rollback с отключённым callback), а Platega-ветка
	// skip+warn при o.payment == nil внутри reconcilePendingOrder.
	budgetCtx, cancel := context.WithTimeout(ctx, reconcilePendingBudget)
	defer cancel()

	pageSize := limit
	if pageSize <= 0 {
		pageSize = 50
	}

	before := time.Now().UTC().Add(-pendingOrderGrace)

	var (
		afterID uint
		errs    []error
	)

	for {
		orders, err := o.db.GetStalePendingOrdersAfterID(budgetCtx, before, afterID, pageSize)
		if err != nil {
			return fmt.Errorf("load stale pending orders after id %d: %w", afterID, err)
		}

		if len(orders) == 0 {
			return errors.Join(errs...)
		}

		for i := range orders {
			err = budgetCtx.Err()
			if err != nil {
				return errors.Join(append(errs, err)...)
			}

			reconcileErr := o.reconcilePendingOrder(budgetCtx, &orders[i])
			if reconcileErr != nil {
				errs = append(errs, reconcileErr)
			}
		}

		lastID := orders[len(orders)-1].ID
		if lastID <= afterID {
			// Страница не продвинулась: без этого guard'а цикл вечен (баг
			// выдачи или заглушка репозитория вернули те же строки).
			return errors.Join(append(errs, fmt.Errorf("pending order page did not advance past id %d", afterID))...)
		}

		afterID = lastID
	}
}

// reconcilePendingOrder сверяет один pending-заказ с провайдером.
func (o *OrderService) reconcilePendingOrder(ctx context.Context, order *database.Order) error {
	// Telegram Stars: invoice живёт в Telegram, статус не сверяется с
	// провайдером. Локальное истечение безопасно — invoice не оплачен, а
	// поздний successful_payment активирует expired-заказ (план, раздел 5.3).
	if order.PaymentProvider == database.PaymentProviderTelegramStars {
		return o.expireReconciledOrder(ctx, order, "stars_invoice_not_paid_local_expiry")
	}

	if o.payment == nil {
		// Platega-провайдер не сконфигурирован (Stars-only режим), а заказ —
		// Platega. Паника недопустима: без обращения к провайдеру сверку
		// провести нельзя — skip + warn + метрика, заказ разбирает оператор.
		metrics.PaymentReconcileTotal.WithLabelValues("skipped_provider_unavailable").Inc()
		logger.Warn("pending platega order cannot be reconciled: platega provider is not configured",
			zap.Uint("order_id", order.ID),
			zap.String("provider_payment_id", order.ProviderPaymentID))

		return nil
	}

	providerPaymentID, parseErr := platega.ParseTransactionID(order.ProviderPaymentID)
	if parseErr != nil {
		metrics.PaymentReconcileTotal.WithLabelValues("skipped_no_provider_id").Inc()
		logger.Warn("pending order has no usable provider transaction id; manual reconciliation required",
			zap.Uint("order_id", order.ID),
			zap.String("provider_payment_id", order.ProviderPaymentID),
			zap.Error(parseErr))

		return nil
	}

	statusCtx, cancel := context.WithTimeout(ctx, paymentSyncTimeout)
	defer cancel()

	status, err := o.payment.GetTransactionStatus(statusCtx, providerPaymentID.String())
	if err != nil {
		if errors.Is(err, platega.ErrTransactionNotFound) {
			// Провайдер не знает транзакцию: оплатить её уже нельзя.
			return o.expireReconciledOrder(ctx, order, "provider_transaction_not_found")
		}

		metrics.PaymentReconcileTotal.WithLabelValues("provider_error").Inc()

		return fmt.Errorf("fetch transaction status for order %d: %w", order.ID, err)
	}

	if status == nil {
		metrics.PaymentReconcileTotal.WithLabelValues("provider_error").Inc()

		return fmt.Errorf("fetch transaction status for order %d: empty response", order.ID)
	}

	switch strings.ToUpper(strings.TrimSpace(status.Status)) {
	case "CONFIRMED":
		confirmation, confirmErr := o.ConfirmPayment(ctx, providerPaymentID.String(), fmt.Sprint(order.ID),
			status.PaymentDetails.Amount, status.PaymentDetails.Currency)
		if confirmErr != nil {
			metrics.PaymentReconcileTotal.WithLabelValues("confirm_error").Inc()

			return fmt.Errorf("confirm reconciled order %d: %w", order.ID, confirmErr)
		}

		result := "confirmed_pending"
		if confirmation != nil && confirmation.Activated {
			result = "confirmed_activated"
		}

		metrics.PaymentReconcileTotal.WithLabelValues(result).Inc()
		logger.Info("pending order reconciled as paid",
			zap.Uint("order_id", order.ID),
			zap.String("result", result),
			zap.String("provider_payment_id", providerPaymentID.String()))

		return nil
	case "CANCELED", "CANCELLED", "EXPIRED", "FAILED":
		return o.expireReconciledOrder(ctx, order, "provider_status_"+strings.ToLower(status.Status))
	default:
		// PENDING и прочие нефинальные статусы: заказ остаётся pending, следующий
		// проход проверит его снова.
		metrics.PaymentReconcileTotal.WithLabelValues("still_pending").Inc()

		return nil
	}
}

// expireReconciledOrder истекает pending-заказ по итогам сверки с провайдером.
func (o *OrderService) expireReconciledOrder(ctx context.Context, order *database.Order, reason string) error {
	expired, err := o.db.ExpirePendingOrder(ctx, order.ID)
	if err != nil {
		metrics.PaymentReconcileTotal.WithLabelValues("expire_error").Inc()

		return fmt.Errorf("expire reconciled order %d: %w", order.ID, err)
	}

	if !expired {
		// Заказ успел оплатиться или истечь иначе — ничего не делаем.
		metrics.PaymentReconcileTotal.WithLabelValues("expire_race").Inc()

		return nil
	}

	metrics.PaymentReconcileTotal.WithLabelValues("expired").Inc()
	logger.Info("pending order expired by reconciliation",
		zap.Uint("order_id", order.ID),
		zap.String("reason", reason))

	auditErr := o.db.InsertAuditLog(ctx, database.AuditOrderExpired(order))
	if auditErr != nil {
		logger.Warn("audit log write failed",
			zap.String("event", database.AuditEventOrderExpired),
			zap.Uint("order_id", order.ID),
			zap.Error(auditErr))
	}

	return nil
}

// starsReconcileLimit — размер страницы getStarTransactions за один запрос.
const starsReconcileLimit = 100

// starsReconcileBudget ограничивает один проход recovery-сверки Stars: длинная
// история не должна растягивать тик планировщика. При исчерпании бюджета проход
// завершается без ошибки, offset сохраняет позицию для следующего тика.
//
// Бюджет проверяется между транзакциями, а не отменяет уже начатую обработку:
// одна транзакция (confirm + post-commit sync) может занять до paymentSyncTimeout,
// поэтому фактическая длительность прохода — бюджет плюс одна транзакция.
const starsReconcileBudget = 60 * time.Second

// ReconcileStarsTransactions сверяет историю Star-транзакций Telegram с БД:
// обрабатывает только входящие invoice_payment, вызывая идемпотентный
// ConfirmStarsPayment (CAS по charge ID). Offset хранится в OrderService
// под мьютексом; после рестарта обработка начинается с 0 и повторно
// (безопасно) проходит историю. Ошибка запроса не двигает offset —
// следующий проход повторит страницу.
//
// Проход пагинированный: страницы читаются подряд, пока страница не короче
// starsReconcileLimit или не исчерпан time-budget, поэтому новая оплата не
// ждёт час за страницу из 100 транзакций.
func (o *OrderService) ReconcileStarsTransactions(ctx context.Context) error {
	return o.reconcileStarsTransactions(ctx, starsReconcileBudget)
}

// reconcileStarsTransactions — рабочая часть сверки с явным бюджетом прохода:
// тесты могут передать маленький бюджет и проверить, что проход останавливается
// без ошибки и сохраняет позицию для следующего тика.
func (o *OrderService) reconcileStarsTransactions(ctx context.Context, budget time.Duration) error {
	if o.stars == nil {
		return nil
	}

	o.starsReconcileMu.Lock()
	offset := o.starsOffset
	o.starsReconcileMu.Unlock()

	budgetCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// processed — фактическое число транзакций (включая не-invoice) на пройденных
	// страницах: offset Telegram двигается по размеру страниц, а не по числу
	// обработанных invoice_payment.
	processed := int64(0)

	for budgetCtx.Err() == nil {
		transactions, err := o.stars.GetTransactions(budgetCtx, offset+processed, starsReconcileLimit)
		if err != nil {
			if budgetCtx.Err() != nil {
				break
			}

			return fmt.Errorf("load stars transactions from offset %d: %w", offset+processed, err)
		}

		page := transactions.Transactions

		// pageDone — число транзакций страницы, дошедших до конца обработки.
		// Если бюджет исчерпан посреди страницы, offset двигается ровно на
		// pageDone: непрочитанные транзакции не пропускаются, следующий проход
		// продолжит с них.
		pageDone := 0

		for range page {
			if budgetCtx.Err() != nil {
				break
			}

			tx := page[pageDone]

			if tx.IsIncomingInvoicePayment() {
				telegramID := tx.Source.User.ID
				payload := tx.Source.InvoicePayload

				// Идемпотентность: CAS по charge ID, повторная обработка — no-op.
				_, confirmErr := o.ConfirmStarsPayment(ctx, tx.ID, payload, tx.Amount, stars.Currency, telegramID)
				if confirmErr != nil {
					var mismatch *StarsSemanticMismatchError

					if !errors.As(confirmErr, &mismatch) {
						// DB/infrastructure ошибка: состояние не применено,
						// транзакцию нужно повторить. Прерываем проход, offset не
						// двигается — следующий hourly-тик повторит страницу
						// (CAS идемпотентен).
						return fmt.Errorf("confirm reconciled stars transaction %s: %w", tx.ID, confirmErr)
					}

					// Semantic mismatch ретраем не исправить: charge возвращается
					// автоматически (сбой возврата — payment issue alert). Заказ по
					// payload не найден, order=nil — честный маркер отсутствия заказа.
					//
					// Ошибка проверки audit — инфраструктурная: решение о возврате
					// принять нельзя. Прерываем проход и держим offset — следующий
					// тик повторит транзакцию (R8 плана).
					_, refundErr := o.autoRefundStarsCharge(ctx, nil, telegramID, tx.Amount, stars.Currency, tx.ID, "recovery_semantic_mismatch")
					if refundErr != nil {
						return fmt.Errorf("auto-refund stars charge %s: %w", tx.ID, refundErr)
					}
				}
			}

			pageDone++
		}

		processed += int64(pageDone)

		if pageDone < len(page) {
			// Бюджет исчерпан посреди страницы — сохраняем позицию и выходим
			// без ошибки: следующий проход продолжит с непрочитанной транзакции.
			break
		}

		if len(page) < starsReconcileLimit {
			// Неполная (или пустая) страница — история исчерпана.
			break
		}
	}

	if processed > 0 {
		// Двигаем offset на фактическое число обработанных транзакций:
		// следующий проход продолжает с новой позиции.
		o.starsReconcileMu.Lock()
		o.starsOffset = offset + processed
		o.starsReconcileMu.Unlock()
	}

	return nil
}
