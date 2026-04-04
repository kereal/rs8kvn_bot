package scheduler

import (
	"context"
	"time"

	"rs8kvn_bot/internal/backup"
	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// BackupScheduler runs periodic database backups.
type BackupScheduler struct {
	dbPath    string
	hour      int
	retention int
}

// NewBackupScheduler creates a new BackupScheduler.
func NewBackupScheduler(dbPath string, hour, retention int) *BackupScheduler {
	return &BackupScheduler{
		dbPath:    dbPath,
		hour:      hour,
		retention: retention,
	}
}

// Start runs the backup scheduler loop. It blocks until ctx is cancelled.
func (s *BackupScheduler) Start(ctx context.Context) {
	logger.Info("Backup scheduler started", zap.Int("schedule_hour", s.hour))

	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(),
			s.hour, 0, 0, 0, now.Location())
		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}

		sleepDuration := time.Until(next)
		logger.Info("Next backup scheduled", zap.Duration("duration", sleepDuration.Round(time.Minute)))

		select {
		case <-time.After(sleepDuration):
			logger.Info("Running scheduled database backup")
			if err := backup.DailyBackup(ctx, s.dbPath, s.retention); err != nil {
				logger.Error("Backup failed", zap.Error(err))
			} else {
				logger.Info("Database backup completed successfully")
			}

		case <-ctx.Done():
			logger.Info("Backup scheduler stopped")
			return
		}
	}
}
