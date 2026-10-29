package scheduler

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"

	"go.uber.org/zap"
)

// trafficWorkerInterval is how often the worker polls panels about traffic.
// The user chose 60 minutes: proactive warnings and re-enabling a client after
// a reset within an hour is an acceptable trade-off against panel load.
const trafficWorkerInterval = 60 * time.Minute

// SubscriptionTrafficWorker inspects subscriptions on traffic-limited plans,
// sends quota notifications, and re-enables clients whose traffic was reset.
type SubscriptionTrafficWorker struct {
	db     interfaces.SubscriptionTrafficRepository
	subSvc interfaces.SubscriptionTrafficService
}

// NewSubscriptionTrafficWorker creates a new SubscriptionTrafficWorker.
func NewSubscriptionTrafficWorker(db interfaces.SubscriptionTrafficRepository, subSvc interfaces.SubscriptionTrafficService) *SubscriptionTrafficWorker {
	return &SubscriptionTrafficWorker{db: db, subSvc: subSvc}
}

// Run starts the periodic traffic-notification loop. It blocks until ctx ends.
func (w *SubscriptionTrafficWorker) Run(ctx context.Context) {
	w.run(ctx, true)
}

// RunRecovery starts the periodic access-recovery loop without sending
// Telegram notifications. It is used when user notifications are disabled.
func (w *SubscriptionTrafficWorker) RunRecovery(ctx context.Context) {
	w.run(ctx, false)
}

func (w *SubscriptionTrafficWorker) run(ctx context.Context, sendNotifications bool) {
	logger.Info("Subscription traffic worker started",
		zap.String("interval", trafficWorkerInterval.String()),
		zap.Bool("notifications", sendNotifications))

	w.processMode(ctx, sendNotifications)

	ticker := time.NewTicker(trafficWorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.processMode(ctx, sendNotifications)
		case <-ctx.Done():
			logger.Info("Subscription traffic worker stopped",
				zap.Bool("notifications", sendNotifications))
			return
		}
	}
}

func (w *SubscriptionTrafficWorker) process(ctx context.Context) {
	w.processMode(ctx, true)
}

func (w *SubscriptionTrafficWorker) processResets(ctx context.Context) {
	w.processMode(ctx, false)
}

func (w *SubscriptionTrafficWorker) processMode(ctx context.Context, sendNotifications bool) {
	if sendNotifications {
		metrics.TrafficNotificationRunsTotal.Inc()
	}

	targets, err := w.db.GetActiveSubscriptionsWithTrafficLimit(ctx)
	if err != nil {
		logger.Error("Failed to query subscriptions with traffic limit", zap.Error(err))
		return
	}

	for _, target := range targets {
		sub := target.Subscription
		if sub.TelegramID <= 0 {
			continue
		}

		if sendNotifications {
			err = w.subSvc.ProcessTrafficNotifications(ctx, &sub)
		} else {
			err = w.subSvc.ProcessTrafficReset(ctx, &sub)
		}
		if err != nil {
			logger.Warn("Traffic subscription processing failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Int64("telegram_id", sub.TelegramID),
				zap.Error(err))
		}

		if ctx.Err() != nil {
			return
		}
	}
}
