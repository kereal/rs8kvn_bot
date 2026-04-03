package scheduler

import (
	"context"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// XUICleanupTarget defines the interface needed for trial cleanup.
type XUICleanupTarget interface {
	DeleteClient(ctx context.Context, inboundID int, clientID string) error
}

// TrialCleanupScheduler runs periodic expired trial cleanup.
type TrialCleanupScheduler struct {
	db         *database.Service
	xuiClient  XUICleanupTarget
	inboundID  int
	trialHours int
}

// NewTrialCleanupScheduler creates a new TrialCleanupScheduler.
func NewTrialCleanupScheduler(db *database.Service, xuiClient XUICleanupTarget, inboundID, trialHours int) *TrialCleanupScheduler {
	return &TrialCleanupScheduler{
		db:         db,
		xuiClient:  xuiClient,
		inboundID:  inboundID,
		trialHours: trialHours,
	}
}

// Start runs the trial cleanup scheduler loop. It blocks until ctx is cancelled.
func (s *TrialCleanupScheduler) Start(ctx context.Context) {
	logger.Info("Trial cleanup scheduler started", zap.String("schedule", "hourly"))

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			logger.Info("Running trial cleanup")
			deleted, err := s.db.CleanupExpiredTrials(ctx, s.trialHours, s.xuiClient, s.inboundID)
			if err != nil {
				logger.Error("Trial cleanup failed", zap.Error(err))
			} else if deleted > 0 {
				logger.Info("Trial cleanup completed", zap.Int64("deleted", deleted))
			}

		case <-ctx.Done():
			logger.Info("Trial cleanup scheduler stopped")
			return
		}
	}
}
