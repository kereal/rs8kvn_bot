package scheduler

import (
	"context"
	"errors"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

// SubscriptionSyncWorker periodically syncs pending VPN subscription nodes.
type SubscriptionSyncWorker struct {
	syncSvc  *service.SyncService
	orderSvc *service.OrderService
	subSvc   *service.SubscriptionService
	db       interfaces.AuditLogWriter
	// lastReconcile — время последней реконсиляции зависших pending-заказов:
	// сверка с провайдером идёт раз в час, а не на каждом 5-минутном тике.
	lastReconcile time.Time
}

// orderReconcileInterval — период сверки потерянных webhook'ов с провайдером.
const orderReconcileInterval = time.Hour

// NewSubscriptionSyncWorker creates a new SubscriptionSyncWorker. The optional
// order service enables durable provider-fee retries without changing existing
// test and embedding call sites.
func NewSubscriptionSyncWorker(syncSvc *service.SyncService, orderSvc ...*service.OrderService) *SubscriptionSyncWorker {
	worker := &SubscriptionSyncWorker{syncSvc: syncSvc}
	if len(orderSvc) > 0 {
		worker.orderSvc = orderSvc[0]
	}

	return worker
}

// SetSubscriptionService подключает сервис подписок для периодического
// обновления гейджей. Гейджи обновлялись только на мутациях, поэтому после
// рестарта показывали ноль до первого изменения, а в тихое время — прошлые
// сутки. Опционально: без сервиса воркер работает как раньше.
func (w *SubscriptionSyncWorker) SetSubscriptionService(svc *service.SubscriptionService) {
	w.subSvc = svc
}

// SetAuditDB подключает хранилище журнала аудита. Опционально: без него
// воркер не пишет события (существующие test/embedding вызовы не ломаются).
func (w *SubscriptionSyncWorker) SetAuditDB(db interfaces.AuditLogWriter) {
	w.db = db
}

// Run starts the periodic sync loop. It blocks until ctx is cancelled.
func (w *SubscriptionSyncWorker) Run(ctx context.Context) {
	logger.Info("Subscription sync worker started", zap.String("interval", "5m"))

	w.process(ctx)

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.process(ctx)
		case <-ctx.Done():
			logger.Info("Subscription sync worker stopped")
			return
		}
	}
}

func (w *SubscriptionSyncWorker) process(ctx context.Context) {
	start := time.Now()

	// Гейджи состояния подписок обновляются в том же проходе: отдельный
	// горутин с таймером здесь ничего не даёт, кроме лишнего состояния.
	if w.subSvc != nil {
		w.subSvc.RefreshActiveSubscriptionsMetric(ctx)
	}

	err := w.syncSvc.SyncPendingNodes(ctx)
	if err != nil {
		metrics.SubscriptionSyncTotal.WithLabelValues("degraded").Inc()
		logger.Warn("Subscription sync failed", zap.Error(err))

		// Сводное событие аудита за прогон (не per-item): деградация воркера
		// должна быть видна в журнале для последующих расследований. Best-effort.
		if w.db != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			auditErr := w.db.InsertAuditLog(ctx, database.AuditSyncRunDegraded(0, err))
			if auditErr != nil {
				logger.Warn("audit log write failed",
					zap.String("event", database.AuditEventSyncRunDegraded),
					zap.Error(auditErr))
			}
		}
	} else {
		metrics.SubscriptionSyncTotal.WithLabelValues("ok").Inc()
		logger.Debug("Subscription sync completed")
	}

	if w.orderSvc != nil {
		err = w.orderSvc.SyncProviderFees(ctx, 50)
		if err != nil {
			logger.Warn("Provider fee sync failed", zap.Error(err))
		}

		// Потерянный webhook иначе оставляет заказ в pending навсегда: деньги
		// списаны, доступа нет. Сверяем не чаще раза в час.
		if time.Since(w.lastReconcile) >= orderReconcileInterval {
			w.lastReconcile = time.Now()

			reconcileErr := w.orderSvc.ReconcilePendingOrders(ctx, 50)
			if reconcileErr != nil {
				logger.Warn("Pending order reconciliation failed", zap.Error(reconcileErr))
			}
		}
	}

	metrics.SubscriptionSyncDuration.Observe(time.Since(start).Seconds())
}
