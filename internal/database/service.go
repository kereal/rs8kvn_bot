package database

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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

func (s *Service) GetDB() *gorm.DB {
	return s.db
}

func (s *Service) Transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(fn)
}

// NewService creates a new database service.
func NewService(dbPath string) (*Service, error) {
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

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
	if err := runMigrations(sqlDB); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConnsDB)
	sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("database connection test failed: %w", err)
	}

	// Seed default plans if none exist
	var count int64
	if err := db.WithContext(context.Background()).Model(&Plan{}).Count(&count).Error; err != nil {
		return nil, fmt.Errorf("failed to count default plans: %w", err)
	}
	if count == 0 {
		if err := db.WithContext(context.Background()).Create(&Plan{
			Name:         TrialPlanName,
			DevicesLimit: 1,
			TrafficLimit: 1073741824,
		}).Error; err != nil {
			return nil, fmt.Errorf("failed to seed default trial plan: %w", err)
		}
		if err := db.WithContext(context.Background()).Create(&Plan{
			Name:         FreePlanName,
			DevicesLimit: 1,
			TrafficLimit: 53687091200,
		}).Error; err != nil {
			return nil, fmt.Errorf("failed to seed default free plan: %w", err)
		}
		logger.Info("Inserted default trial/free plans")
	}
	return &Service{db: db}, nil
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
