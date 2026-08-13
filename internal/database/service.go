package database

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Service provides database operations with proper dependency injection.
type Service struct {
	db *gorm.DB
}

// GetDB returns the underlying *gorm.DB instance.
// ⚠️ Test use only: production code should use domain methods (subscriptions, nodes, etc.)
// or Transaction(ctx, fn) instead of bypassing the service layer.
func (s *Service) GetDB() *gorm.DB {
	return s.db
}

func (s *Service) Transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(fn)
}

// NewService creates a new database service.
func NewService(dbPath string) (*Service, error) {
	dbDir := filepath.Dir(dbPath)

	err := os.MkdirAll(dbDir, 0750)
	if err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// SQLite serializes writers; keep transient contention from surfacing as
	// random "database is locked" webhook failures during concurrent payment
	// and expiry transitions. The busy timeout is part of the DSN so it applies
	// to every pooled connection, not only the one used for this setup query.
	dbPath = sqliteBusyTimeoutDSN(dbPath)

	db, err := gorm.Open(gormsqlite.Open(dbPath), &gorm.Config{
		PrepareStmt: false,
		Logger:      gormlogger.New(log.New(io.Discard, "", 0), gormlogger.Config{SlowThreshold: 200 * time.Millisecond, LogLevel: gormlogger.Silent}),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Get underlying SQL DB for migrations
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying SQL DB: %w", err)
	}

	// Run database migrations using golang-migrate
	err = runMigrations(sqlDB)
	if err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConnsDB)
	sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	err = sqlDB.Ping()
	if err != nil {
		return nil, fmt.Errorf("database connection test failed: %w", err)
	}

	// Seed default plans if none exist
	var count int64

	err = db.WithContext(context.Background()).Model(&Plan{}).Count(&count).Error
	if err != nil {
		return nil, fmt.Errorf("failed to count default plans: %w", err)
	}

	if count == 0 {
		err = db.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
			planErr := tx.Create(&Plan{
				Name:         TrialPlanName,
				DevicesLimit: 1,
				TrafficLimit: 1073741824,
			}).Error
			if planErr != nil {
				return fmt.Errorf("failed to seed default trial plan: %w", planErr)
			}

			planErr = tx.Create(&Plan{
				Name:         FreePlanName,
				DevicesLimit: 1,
				TrafficLimit: 53687091200,
			}).Error
			if planErr != nil {
				return fmt.Errorf("failed to seed default free plan: %w", planErr)
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		logger.Info("Inserted default trial/free plans")
	}

	return &Service{db: db}, nil
}

func sqliteBusyTimeoutDSN(path string) string {
	if strings.Contains(path, "?") {
		return path + "&_busy_timeout=5000"
	}

	return path + "?_busy_timeout=5000"
}

// Close closes the database connection.
func (s *Service) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}

// Ping checks the database connection health.
func (s *Service) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}

	return sqlDB.PingContext(ctx)
}

// GetPoolStats returns current database connection pool statistics.
func (s *Service) GetPoolStats() (*PoolStats, error) {
	sqlDB, err := s.db.DB()
	if err != nil {
		return nil, err
	}

	stats := sqlDB.Stats()

	return &PoolStats{
		MaxOpen:       stats.MaxOpenConnections,
		Open:          stats.OpenConnections,
		InUse:         stats.InUse,
		Idle:          stats.Idle,
		WaitCount:     stats.WaitCount,
		WaitDuration:  stats.WaitDuration,
		MaxIdleClosed: stats.MaxIdleClosed,
	}, nil
}
