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

// SubscriptionReminderWorker sends expiry reminders to active paid subscriptions.
type SubscriptionReminderWorker struct {
	db     interfaces.SubscriptionReminderRepository
	subSvc interfaces.SubscriptionReminderService
}

// NewSubscriptionReminderWorker creates a new SubscriptionReminderWorker.
func NewSubscriptionReminderWorker(db interfaces.SubscriptionReminderRepository, subSvc interfaces.SubscriptionReminderService) *SubscriptionReminderWorker {
	return &SubscriptionReminderWorker{db: db, subSvc: subSvc}
}

// Run starts the periodic reminder loop. It blocks until ctx is cancelled.
func (w *SubscriptionReminderWorker) Run(ctx context.Context) {
	logger.Info("Subscription reminder worker started", zap.String("interval", "30m"))

	w.process(ctx)

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.process(ctx)
		case <-ctx.Done():
			logger.Info("Subscription reminder worker stopped")
			return
		}
	}
}

func (w *SubscriptionReminderWorker) process(ctx context.Context) {
	now := time.Now().UTC()
	metrics.SubscriptionReminderRunsTotal.Inc()

	for _, window := range service.ExpiryReminderWindows() {
		from, to := service.ReminderWindowBounds(window, now)
		subs, err := w.db.GetSubscriptionsExpiringInRange(ctx, from, to)
		if err != nil {
			logger.Error("Failed to query subscriptions for reminder window",
				zap.String("window", window.Name),
				zap.Error(err))
			continue
		}
		for _, sub := range subs {
			if err := w.subSvc.SendExpiryReminder(ctx, &sub, window); err != nil {
				logger.Error("Failed to send expiry reminder",
					zap.Uint("subscription_id", sub.ID),
					zap.String("window", window.Name),
					zap.Error(err))
			}
		}
	}
}
