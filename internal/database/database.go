package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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

var DB *gorm.DB
var sqlDB *sql.DB

func Init(dbPath string) error {
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return err
	}

	var err error
	// Open with PrepareStmt disabled to reduce memory overhead
	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		PrepareStmt: false, // Disable prepared statement cache to save memory
	})
	if err != nil {
		return err
	}

	if err := DB.AutoMigrate(&Subscription{}); err != nil {
		return err
	}

	// Run database migrations
	if err := RunMigrations(DB); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	sqlDB, err = DB.DB()
	if err != nil {
		return err
	}

	// Minimal connection pool for low memory footprint
	sqlDB.SetMaxOpenConns(1)                  // Single connection for SQLite
	sqlDB.SetMaxIdleConns(1)                  // Keep 1 idle connection
	sqlDB.SetConnMaxLifetime(5 * time.Minute) // Reduce from 10min to 5min
	sqlDB.SetConnMaxIdleTime(2 * time.Minute) // Close idle connections after 2min

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	return nil
}

func Close() error {
	if sqlDB != nil {
		err := sqlDB.Close()
		sqlDB = nil
		DB = nil
		return err
	}
	return nil
}

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

func CreateSubscription(sub *Subscription) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Subscription{}).
			Where("telegram_id = ? AND status = ?", sub.TelegramID, "active").
			Update("status", "revoked").Error; err != nil {
			return fmt.Errorf("failed to revoke old subscription: %w", err)
		}

		if err := tx.Create(sub).Error; err != nil {
			return fmt.Errorf("failed to create new subscription: %w", err)
		}

		return nil
	})
}

func UpdateSubscription(sub *Subscription) error {
	result := DB.Save(sub)
	return result.Error
}
