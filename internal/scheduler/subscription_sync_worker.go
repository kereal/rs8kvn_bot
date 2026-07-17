package scheduler

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

// SubscriptionSyncWorker periodically syncs pending VPN subscription nodes.
type SubscriptionSyncWorker struct {
	syncSvc *service.SyncService
}

// NewSubscriptionSyncWorker creates a new SubscriptionSyncWorker.
func NewSubscriptionSyncWorker(syncSvc *service.SyncService) *SubscriptionSyncWorker {
	return &SubscriptionSyncWorker{syncSvc: syncSvc}
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
	metrics.SubscriptionSyncTotal.Inc()
	start := time.Now()
	if err := w.syncSvc.SyncPendingNodes(ctx); err != nil {
		logger.Warn("Subscription sync failed", zap.Error(err))
	} else {
		logger.Debug("Subscription sync completed")
	}
	metrics.SubscriptionSyncDuration.Observe(time.Since(start).Seconds())
}
