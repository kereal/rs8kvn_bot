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

// SubscriptionReminderWorker sends expiry reminders to active subscriptions.
type SubscriptionReminderWorker struct {
	db     interfaces.SubscriptionRepository
	subSvc interfaces.SubscriptionReminderService
}

// NewSubscriptionReminderWorker creates a new SubscriptionReminderWorker.
func NewSubscriptionReminderWorker(db interfaces.SubscriptionRepository, subSvc interfaces.SubscriptionReminderService) *SubscriptionReminderWorker {
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

	windows := []struct {
		name string
		bit  int
		from time.Time
		to   time.Time
	}{
		{"3d", service.ReminderBit3Days, now.Add(72*time.Hour - 30*time.Minute), now.Add(72*time.Hour + 30*time.Minute)},
		{"1d", service.ReminderBit1Day, now.Add(24*time.Hour - 30*time.Minute), now.Add(24*time.Hour + 30*time.Minute)},
		{"3h", service.ReminderBit3Hours, now.Add(3*time.Hour - 30*time.Minute), now.Add(3*time.Hour + 30*time.Minute)},
	}

	for _, win := range windows {
		subs, err := w.db.GetSubscriptionsExpiringInRange(ctx, win.from, win.to)
		if err != nil {
			logger.Error("Failed to query subscriptions for reminder window",
				zap.String("window", win.name),
				zap.Error(err))
			continue
		}
		for _, sub := range subs {
			var daysLeft, hoursLeft int
			if sub.ExpiresAt != nil {
				d := sub.ExpiresAt.Sub(now)
				if d > 0 {
					daysLeft = int(d.Hours() / 24)
					hoursLeft = int(d.Hours()) % 24
				}
			}
			if err := w.subSvc.SendExpiryReminder(ctx, &sub, win.bit, daysLeft, hoursLeft); err != nil {
				logger.Error("Failed to send expiry reminder",
					zap.Uint("subscription_id", sub.ID),
					zap.String("window", win.name),
					zap.Error(err))
				continue
			}
		}
	}
}
