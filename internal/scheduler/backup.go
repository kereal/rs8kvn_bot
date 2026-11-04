package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/backup"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// BackupScheduler runs periodic database backups.
type BackupScheduler struct {
	dbPath    string
	hour      int
	retention int
	// maintenance — суточное обслуживание БД после бэкапа (retention аудита).
	// Отдельный хук, чтобы планировщик не знал про внутренности database.
	maintenance func(context.Context) error
}

// NewBackupScheduler creates a new BackupScheduler.
func NewBackupScheduler(dbPath string, hour, retention int) *BackupScheduler {
	return &BackupScheduler{
		dbPath:    dbPath,
		hour:      hour,
		retention: retention,
	}
}

// SetMaintenance подключает суточное обслуживание, выполняемое после бэкапа.
func (s *BackupScheduler) SetMaintenance(fn func(context.Context) error) {
	s.maintenance = fn
}

// Start runs the backup scheduler loop. It blocks until ctx is cancelled.
func (s *BackupScheduler) Start(ctx context.Context) {
	logger.Info("Backup scheduler started", zap.String("schedule_hour", fmt.Sprintf("%dh", s.hour)))

	for {
		now := time.Now()

		next := time.Date(now.Year(), now.Month(), now.Day(),
			s.hour, 0, 0, 0, now.Location())
		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}

		sleepDuration := time.Until(next)
		logger.Info("Next backup scheduled", zap.String("duration", fmt.Sprintf("%dh", int(sleepDuration.Round(time.Hour).Hours()))))

		select {
		case <-time.After(sleepDuration):
			s.runOnce(ctx)

		case <-ctx.Done():
			logger.Info("Backup scheduler stopped")
			return
		}
	}
}

// runOnce performs a single backup followed by the daily maintenance hook.
func (s *BackupScheduler) runOnce(ctx context.Context) {
	logger.Info("Running scheduled database backup")

	err := backup.DailyBackup(ctx, s.dbPath, s.retention)
	if err != nil {
		logger.Error("Backup failed", zap.Error(err))
	} else {
		logger.Info("Database backup completed successfully")
	}

	if s.maintenance != nil {
		maintErr := s.maintenance(ctx)
		if maintErr != nil {
			logger.Warn("Daily database maintenance failed", zap.Error(maintErr))
		}
	}
}
