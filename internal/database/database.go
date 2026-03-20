package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"rs8kvn_bot/internal/config"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Subscription represents a user's VPN subscription.
type Subscription struct {
	ID              uint           `gorm:"primaryKey"`
	TelegramID      int64          `gorm:"index;not null"`
	Username        string         `gorm:"size:255"`
	ClientID        string         `gorm:"size:255"`
	XUIHost         string         `gorm:"size:255"`
	InboundID       int            `gorm:"index"`
	TrafficLimit    int64          `gorm:"default:107374182400"`
	ExpiryTime      time.Time      `gorm:"index:idx_expiry"`
	Status          string         `gorm:"default:active;size:50;index"`
	SubscriptionURL string         `gorm:"size:512;column:subscription_url"`
	CreatedAt       time.Time      `gorm:"autoCreateTime"`
	UpdatedAt       time.Time      `gorm:"autoUpdateTime"`
	DeletedAt       gorm.DeletedAt `gorm:"index"`
}

// TableName returns the table name for Subscription.
func (Subscription) TableName() string {
	return "subscriptions"
}

// IsExpired returns true if the subscription has expired.
func (s *Subscription) IsExpired() bool {
	return time.Now().After(s.ExpiryTime)
}

// IsActive returns true if the subscription is active and not expired.
func (s *Subscription) IsActive() bool {
	return s.Status == "active" && !s.IsExpired()
}

// DB is the global database connection.
// Deprecated: Use database.Service instead for better testability.
var DB *gorm.DB

var sqlDB *sql.DB

// Init initializes the database connection and runs migrations.
// Deprecated: Use NewService for dependency injection.
func Init(dbPath string) error {
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	var err error
	// Open with PrepareStmt disabled to reduce memory overhead
	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		PrepareStmt: false, // Disable prepared statement cache to save memory
	})
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err := DB.AutoMigrate(&Subscription{}); err != nil {
		return fmt.Errorf("failed to run auto-migration: %w", err)
	}

	// Run database migrations
	if err := RunMigrations(DB); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	sqlDB, err = DB.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying SQL DB: %w", err)
	}

	// Minimal connection pool for low memory footprint
	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConnsDB)
	sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("database connection test failed: %w", err)
	}

	return nil
}

// Close closes the database connection.
func Close() error {
	if sqlDB != nil {
		err := sqlDB.Close()
		sqlDB = nil
		DB = nil
		return err
	}
	return nil
}

// GetByTelegramID retrieves an active subscription by Telegram ID.
// Returns gorm.ErrRecordNotFound if no active subscription exists.
func GetByTelegramID(telegramID int64) (*Subscription, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var sub Subscription
	result := DB.Where("telegram_id = ? AND status = ?", telegramID, "active").
		Order("created_at DESC").
		First(&sub)
	if result.Error != nil {
		return nil, result.Error
	}
	return &sub, nil
}

// CreateSubscription creates a new subscription and revokes any existing active subscriptions.
// This operation is atomic - either both operations succeed or neither does.
func CreateSubscription(sub *Subscription) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		// Revoke any existing active subscriptions for this user
		if err := tx.Model(&Subscription{}).
			Where("telegram_id = ? AND status = ?", sub.TelegramID, "active").
			Update("status", "revoked").Error; err != nil {
			return fmt.Errorf("failed to revoke old subscription: %w", err)
		}

		// Create the new subscription
		if err := tx.Create(sub).Error; err != nil {
			return fmt.Errorf("failed to create new subscription: %w", err)
		}

		return nil
	})
}

// UpdateSubscription updates an existing subscription.
func UpdateSubscription(sub *Subscription) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	result := DB.Save(sub)
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription: %w", result.Error)
	}
	return nil
}

// DeleteSubscription soft-deletes a subscription.
func DeleteSubscription(telegramID int64) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	result := DB.Where("telegram_id = ?", telegramID).Delete(&Subscription{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete subscription: %w", result.Error)
	}
	return nil
}

// GetSubscriptionByID retrieves a subscription by its database ID.
func GetSubscriptionByID(id uint) (*Subscription, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var sub Subscription
	result := DB.First(&sub, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", result.Error)
	}
	return &sub, nil
}

// DeleteSubscriptionByID hard-deletes a subscription by its database ID.
// Returns the deleted subscription so the caller can use its data for cleanup.
func DeleteSubscriptionByID(id uint) (*Subscription, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	// Get the subscription first to return it after deletion
	var sub Subscription
	result := DB.First(&sub, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to find subscription: %w", result.Error)
	}

	// Hard delete (Unscoped)
	result = DB.Unscoped().Delete(&sub)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to delete subscription: %w", result.Error)
	}

	return &sub, nil
}

// GetLatestSubscriptions retrieves the latest N subscriptions ordered by creation date.
func GetLatestSubscriptions(limit int) ([]Subscription, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var subs []Subscription
	result := DB.Where("status = ?", "active").
		Order("created_at DESC").
		Limit(limit).
		Find(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get latest subscriptions: %w", result.Error)
	}
	return subs, nil
}

// Service provides database operations with proper dependency injection.
type Service struct {
	db *gorm.DB
}

// NewService creates a new database service.
func NewService(dbPath string) (*Service, error) {
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		PrepareStmt: false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.AutoMigrate(&Subscription{}); err != nil {
		return nil, fmt.Errorf("failed to run auto-migration: %w", err)
	}

	if err := RunMigrations(db); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying SQL DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConnsDB)
	sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("database connection test failed: %w", err)
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

// GetByTelegramID retrieves an active subscription by Telegram ID.
func (s *Service) GetByTelegramID(ctx context.Context, telegramID int64) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).
		Where("telegram_id = ? AND status = ?", telegramID, "active").
		Order("created_at DESC").
		First(&sub)
	if result.Error != nil {
		return nil, result.Error
	}
	return &sub, nil
}

// CreateSubscription creates a new subscription and revokes any existing active subscriptions.
func (s *Service) CreateSubscription(ctx context.Context, sub *Subscription) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Revoke any existing active subscriptions for this user
		if err := tx.Model(&Subscription{}).
			Where("telegram_id = ? AND status = ?", sub.TelegramID, "active").
			Update("status", "revoked").Error; err != nil {
			return fmt.Errorf("failed to revoke old subscription: %w", err)
		}

		// Create the new subscription
		if err := tx.Create(sub).Error; err != nil {
			return fmt.Errorf("failed to create new subscription: %w", err)
		}

		return nil
	})
}

// UpdateSubscription updates an existing subscription.
func (s *Service) UpdateSubscription(ctx context.Context, sub *Subscription) error {
	result := s.db.WithContext(ctx).Save(sub)
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription: %w", result.Error)
	}
	return nil
}

// DeleteSubscription soft-deletes a subscription.
func (s *Service) DeleteSubscription(ctx context.Context, telegramID int64) error {
	result := s.db.WithContext(ctx).Where("telegram_id = ?", telegramID).Delete(&Subscription{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete subscription: %w", result.Error)
	}
	return nil
}

// GetLatestSubscriptions retrieves the latest N subscriptions ordered by creation date.
func (s *Service) GetLatestSubscriptions(ctx context.Context, limit int) ([]Subscription, error) {
	var subs []Subscription
	result := s.db.WithContext(ctx).
		Where("status = ?", "active").
		Order("created_at DESC").
		Limit(limit).
		Find(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get latest subscriptions: %w", result.Error)
	}
	return subs, nil
}

// GetAllSubscriptions retrieves all subscriptions (for admin stats).
func (s *Service) GetAllSubscriptions(ctx context.Context) ([]Subscription, error) {
	var subs []Subscription
	result := s.db.WithContext(ctx).Find(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get all subscriptions: %w", result.Error)
	}
	return subs, nil
}

// CountActiveSubscriptions returns the number of active, non-expired subscriptions.
func (s *Service) CountActiveSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Where("status = ? AND expiry_time > ?", "active", time.Now()).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count active subscriptions: %w", result.Error)
	}
	return count, nil
}

// CountExpiredSubscriptions returns the number of expired subscriptions.
func (s *Service) CountExpiredSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Where("status = ? AND expiry_time <= ?", "active", time.Now()).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count expired subscriptions: %w", result.Error)
	}
	return count, nil
}
