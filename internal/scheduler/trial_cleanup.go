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
	trialHours int
}

// NewTrialCleanupScheduler creates a new TrialCleanupScheduler.
func NewTrialCleanupScheduler(db *database.Service, xuiClient XUICleanupTarget, trialHours int) *TrialCleanupScheduler {
	return &TrialCleanupScheduler{
		db:         db,
		xuiClient:  xuiClient,
		trialHours: trialHours,
	}
}

// Start runs the trial cleanup scheduler loop. It blocks until ctx is cancelled.
func (s *TrialCleanupScheduler) Start(ctx context.Context) {
	logger.Info("Trial cleanup scheduler started", zap.String("schedule", "hourly"))

	// Run cleanup immediately on startup
	s.runCleanup(ctx)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.runCleanup(ctx)

		case <-ctx.Done():
			logger.Info("Trial cleanup scheduler stopped")
			return
		}
	}
}

func (s *TrialCleanupScheduler) runCleanup(ctx context.Context) {
	logger.Info("Running trial cleanup")
	deleted, err := s.db.CleanupExpiredTrials(ctx, s.trialHours, s.xuiClient)
	if err != nil {
		logger.Error("Trial cleanup failed", zap.Error(err))
	} else if deleted > 0 {
		logger.Info("Trial cleanup completed", zap.Int64("deleted", deleted))
	}
}
