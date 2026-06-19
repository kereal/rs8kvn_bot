package scheduler

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

// SubscriptionExpireWorker handles expiration of paid subscriptions that have passed their expiry date.
type SubscriptionExpireWorker struct {
	repos  *database.Service
	subSvc *service.SubscriptionService
}

// NewSubscriptionExpireWorker creates a new SubscriptionExpireWorker.
func NewSubscriptionExpireWorker(repos *database.Service, subSvc *service.SubscriptionService) *SubscriptionExpireWorker {
	return &SubscriptionExpireWorker{repos: repos, subSvc: subSvc}
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
	subs, err := w.repos.GetExpiredPaidSubscriptions(ctx)
	if err != nil {
		logger.Error("Failed to query expired subscriptions", zap.Error(err))
		return
	}

	if len(subs) == 0 {
		return
	}

	processed := 0
	for _, sub := range subs {
		if err := w.subSvc.ExpireSubscription(ctx, sub.TelegramID); err != nil {
			logger.Warn("Expire subscription failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Int64("telegram_id", sub.TelegramID),
				zap.Error(err))
			continue
		}
		processed++
	}

	logger.Info("Subscription expiration processed",
		zap.Int("found", len(subs)),
		zap.Int("expired", processed))
}
