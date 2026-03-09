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
	TelegramID      int64          `gorm:"uniqueIndex:idx_telegram_status;not null"`
	Username        string         `gorm:"size:255"`
	ClientID        string         `gorm:"size:255"`
	XUIHost         string         `gorm:"size:255"`
	InboundID       int            `gorm:"index"`
	TrafficLimit    int64          `gorm:"default:107374182400"`
	ExpiryTime      time.Time      `gorm:"index:idx_expiry"`
	Status          string         `gorm:"default:active;size:50"`
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
	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return err
	}

	if err := DB.AutoMigrate(&Subscription{}); err != nil {
		return err
	}

	sqlDB, err = DB.DB()
	if err != nil {
		return err
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(10 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	return nil
}

func Close() error {
	if sqlDB != nil {
		return sqlDB.Close()
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

func DeleteSubscription(sub *Subscription) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	return DB.Delete(sub).Error
}

func GetExpired() ([]Subscription, error) {
	var subs []Subscription
	result := DB.Where("status = ? AND expiry_time < ?", "active", time.Now()).
		Find(&subs)
	return subs, result.Error
}
