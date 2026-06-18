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

//nolint:gosec // query uses subqueries and string concatenation intentionally for raw SQL readability
func (w *SubscriptionExpireWorker) process(ctx context.Context) {
	const query = `SELECT s.id, s.telegram_id, s.username, s.client_id, s.subscription_id
		FROM subscriptions s
		WHERE s.expires_at <= ?
		  AND s.status = 'active'
		  AND s.plan_id != (SELECT id FROM plans WHERE name = ?)`

	rows, err := w.repos.GetDB().Raw(query, time.Now().UTC().Truncate(time.Minute), database.FreePlanName).Rows()
	if err != nil {
		logger.Error("Failed to query expired subscriptions", zap.Error(err))
		return
	}
	defer rows.Close()

	type expiryTarget struct {
		id            uint
		telegramID    int64
		username      string
		clientID      string
		subscriptionID string
	}

	var targets []expiryTarget
	for rows.Next() {
		var t expiryTarget
		if err := rows.Scan(&t.id, &t.telegramID, &t.username, &t.clientID, &t.subscriptionID); err != nil {
			logger.Error("Scan expired subscription failed", zap.Error(err))
			continue
		}
		targets = append(targets, t)
	}

	if len(targets) == 0 {
		return
	}

	processed := 0
	for _, t := range targets {
		if err := w.subSvc.ExpireSubscription(ctx, t.telegramID); err != nil {
			logger.Warn("Expire subscription failed",
				zap.Uint("subscription_id", t.id),
				zap.Int64("telegram_id", t.telegramID),
				zap.Error(err))
			continue
		}
		processed++
	}

	logger.Info("Subscription expiration processed",
		zap.Int("found", len(targets)),
		zap.Int("expired", processed))
}
