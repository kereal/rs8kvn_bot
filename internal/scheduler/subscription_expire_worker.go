package scheduler

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

// SubscriptionExpireWorker handles expiration of paid subscriptions that have passed their expiry date.
type SubscriptionExpireWorker struct {
	db     interfaces.SubscriptionRepository
	subSvc *service.SubscriptionService
}

// NewSubscriptionExpireWorker creates a new SubscriptionExpireWorker.
func NewSubscriptionExpireWorker(db interfaces.SubscriptionRepository, subSvc *service.SubscriptionService) *SubscriptionExpireWorker {
	return &SubscriptionExpireWorker{db: db, subSvc: subSvc}
}

// Run starts the periodic expiration loop. It blocks until ctx is cancelled.
func (w *SubscriptionExpireWorker) Run(ctx context.Context) {
	logger.Info("Subscription expire worker started", zap.String("interval", "1h"))

	w.process(ctx)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.process(ctx)
		case <-ctx.Done():
			logger.Info("Subscription expire worker stopped")
			return
		}
	}
}

func (w *SubscriptionExpireWorker) process(ctx context.Context) {
	start := time.Now()
	defer func() {
		metrics.SubscriptionExpireDuration.Observe(time.Since(start).Seconds())
	}()

	subs, err := w.db.GetExpiredPaidSubscriptions(ctx, time.Now().UTC())
	if err != nil {
		metrics.SubscriptionExpireTotal.WithLabelValues("degraded").Inc()
		logger.Error("Failed to query expired subscriptions", zap.Error(err))

		return
	}

	if len(subs) == 0 {
		metrics.SubscriptionExpireTotal.WithLabelValues("ok").Inc()

		return
	}

	processed := 0
	failed := 0

	for _, sub := range subs {
		err := w.subSvc.ExpireSubscription(ctx, sub.ID)
		if err != nil {
			failed++
			logger.Warn("Expire subscription failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Int64("telegram_id", sub.TelegramID),
				zap.Error(err))

			continue
		}

		processed++
	}

	result := "ok"
	if failed > 0 {
		result = "degraded"
	}

	metrics.SubscriptionExpireTotal.WithLabelValues(result).Inc()
	metrics.SubscriptionsExpiredTotal.Add(float64(processed))

	logger.Info("Subscription expiration processed",
		zap.Int("found", len(subs)),
		zap.Int("expired", processed))
}
