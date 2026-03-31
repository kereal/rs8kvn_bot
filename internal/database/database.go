package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"

	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/zap"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var _ = logger.Info // suppress unused import warning

// Subscription represents a user's VPN subscription.
type Subscription struct {
	ID              uint           `gorm:"primaryKey"`
	TelegramID      int64          `gorm:"index"`
	Username        string         `gorm:"size:255;index"`
	ClientID        string         `gorm:"size:255"`
	SubscriptionID  string         `gorm:"size:255;index"`
	InboundID       int            `gorm:"index"`
	TrafficLimit    int64          `gorm:"default:107374182400"`
	ExpiryTime      time.Time      `gorm:"index:idx_expiry"`
	Status          string         `gorm:"default:active;size:50;index"`
	SubscriptionURL string         `gorm:"size:512;column:subscription_url"`
	InviteCode      string         `gorm:"size:16;index"`
	IsTrial         bool           `gorm:"default:false;index"`
	ReferredBy      int64          `gorm:"index"`
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

// Invite represents a referral invite code.
type Invite struct {
	Code         string    `gorm:"primaryKey;size:16"`
	ReferrerTGID int64     `gorm:"index;not null"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
}

// TableName returns the table name for Invite.
func (Invite) TableName() string {
	return "invites"
}

// TrialRequest tracks trial requests for rate limiting.
type TrialRequest struct {
	ID        uint      `gorm:"primaryKey"`
	IP        string    `gorm:"size:45;index"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

// TableName returns the table name for TrialRequest.
func (TrialRequest) TableName() string {
	return "trial_requests"
}

// DB is the global database connection.
// Deprecated: Use database.Service instead for better testability.
var DB *gorm.DB

var sqlDB *sql.DB

// Init initializes the database connection and runs migrations.
// Deprecated: Use NewService for dependency injection.
func Init(dbPath string) error {
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0750); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	var err error
	// Open with PrepareStmt disabled to reduce memory overhead
	// Use silent logger to suppress SQL query output in console
	DB, err = gorm.Open(gormsqlite.Open(dbPath), &gorm.Config{
		PrepareStmt: false,
		Logger:      gormlogger.New(log.New(io.Discard, "", 0), gormlogger.Config{SlowThreshold: 200 * time.Millisecond, LogLevel: gormlogger.Silent}),
	})
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Run database migrations using golang-migrate
	if err := runMigrations(dbPath); err != nil {
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

// findMigrationsDir finds the migrations directory by checking multiple possible paths
func findMigrationsDir() string {
	wd, _ := os.Getwd()
	possiblePaths := []string{
		filepath.Join(wd, "migrations"),
		filepath.Join(wd, "internal/database/migrations"),
		filepath.Join(wd, "../database/migrations"),
		filepath.Join(wd, "../../database/migrations"),
		filepath.Join(wd, "../../../database/migrations"),
	}
	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// runMigrations applies database migrations using golang-migrate
func runMigrations(dbPath string) error {
	migrationsDir := findMigrationsDir()
	if migrationsDir == "" {
		return nil
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database for migrations: %w", err)
	}
	defer sqlDB.Close()

	return runMigrationsWithDBAndDir(sqlDB, migrationsDir)
}

// runMigrationsWithDB applies database migrations using an existing database connection
func runMigrationsWithDB(sqlDB *sql.DB) error {
	migrationsDir := findMigrationsDir()
	if migrationsDir == "" {
		return nil
	}

	return runMigrationsWithDBAndDir(sqlDB, migrationsDir)
}

// runMigrationsWithDBAndDir applies database migrations using an existing database connection and migrations directory
func runMigrationsWithDBAndDir(sqlDB *sql.DB, migrationsDir string) error {
	var err error

	// Check if subscriptions table exists and its structure
	var tableExists int
	sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='subscriptions'").Scan(&tableExists)

	var xuiHostExists, subIDExists int
	if tableExists > 0 {
		sqlDB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'x_ui_host'").Scan(&xuiHostExists)
		sqlDB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'subscription_id'").Scan(&subIDExists)
	}

	// Legacy database: has subscriptions table but missing subscription_id column
	if tableExists > 0 && subIDExists == 0 {
		logger.Info("Running legacy migration 001 (old subscriptions table found)")

		// Add subscription_id column if not exists
		if subIDExists == 0 {
			_, err = sqlDB.Exec("ALTER TABLE subscriptions ADD COLUMN subscription_id VARCHAR(255)")
			if err != nil {
				logger.Warn("Migration 001 ADD COLUMN failed", zap.String("error", err.Error()))
			}
		}

		// Update subscription_id from subscription_url (extract UUID after /s/)
		_, err = sqlDB.Exec(`
			UPDATE subscriptions 
			SET subscription_id = SUBSTR(subscription_url, INSTR(subscription_url, '/s/') + 3)
			WHERE subscription_url LIKE '%/s/%';
		`)
		if err != nil {
			logger.Warn("Migration 001 UPDATE subscription_id failed", zap.String("error", err.Error()))
		}

		// Drop x_ui_host column if exists
		if xuiHostExists > 0 {
			_, err = sqlDB.Exec("ALTER TABLE subscriptions DROP COLUMN x_ui_host")
			if err != nil {
				logger.Warn("Migration 001 DROP COLUMN x_ui_host failed", zap.String("error", err.Error()))
			}
		}

		logger.Info("Legacy migration 001 applied")
	} else if tableExists == 0 {
		// Fresh database - will be created by migration 000
		logger.Info("No legacy migration needed - fresh database")
	}

	// Check if referral columns already exist
	var hasInviteCode, hasIsTrial, hasReferredBy int
	if tableExists > 0 {
		sqlDB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'invite_code'").Scan(&hasInviteCode)
		sqlDB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'is_trial'").Scan(&hasIsTrial)
		sqlDB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'referred_by'").Scan(&hasReferredBy)
	}

	// If referral columns exist, we need to skip migration 003
	if hasInviteCode > 0 || hasIsTrial > 0 || hasReferredBy > 0 {
		// Drop old migrations table and create new one with correct version
		_, _ = sqlDB.Exec(`DROP TABLE IF EXISTS schema_migrations`)
		_, _ = sqlDB.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, dirty INTEGER)`)
		_, _ = sqlDB.Exec(`INSERT INTO schema_migrations (version, dirty) VALUES (3, 0)`)
		//logger.Info("Referral columns already exist, skipping migration 003")
		return nil
	}

	// Drop old migrations table if exists (to ensure correct schema)
	_, _ = sqlDB.Exec(`DROP TABLE IF EXISTS schema_migrations`)

	// Create migrations table for golang-migrate (will be created automatically by ensureVersionTable)

	// Run all other migrations
	driver, err := sqlite.WithInstance(sqlDB, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migrate driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://"+migrationsDir,
		"sqlite",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	// Get current version before migration
	versionBefore, _, _ := m.Version()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migration failed: %w", err)
	}

	// Get version after migration
	versionAfter, _, _ := m.Version()

	if versionAfter > versionBefore {
		// Read migration files that were applied
		migrationFiles, _ := filepath.Glob(filepath.Join(migrationsDir, "*.up.sql"))
		var appliedMigrations []string
		for _, f := range migrationFiles {
			name := filepath.Base(f)
			var num int
			fmt.Sscanf(name, "%d_", &num)
			if uint(num) > versionBefore && uint(num) <= versionAfter {
				appliedMigrations = append(appliedMigrations, name)
			}
		}
		logger.Info("Database migrations applied",
			zap.Strings("migrations", appliedMigrations),
			zap.Uint("version", versionAfter))
	} else {
		logger.Info("Database migrations up to date",
			zap.Uint("version", versionAfter))
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
	if err := runMigrationsWithDB(sqlDB); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
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

// Ping checks the database connection health.
func (s *Service) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

// PoolStats contains database connection pool statistics.
type PoolStats struct {
	MaxOpen       int
	Open          int
	InUse         int
	Idle          int
	WaitCount     int64
	WaitDuration  time.Duration
	MaxIdleClosed int64
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

// GetByID retrieves a subscription by its database ID.
func (s *Service) GetByID(ctx context.Context, id uint) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).First(&sub, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", result.Error)
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

// GetAllTelegramIDs returns all unique Telegram IDs from subscriptions.
func (s *Service) GetAllTelegramIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Distinct("telegram_id").
		Pluck("telegram_id", &ids)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get telegram IDs: %w", result.Error)
	}
	return ids, nil
}

// GetTelegramIDByUsername returns the Telegram ID for a given username.
func (s *Service) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).Where("username = ?", username).First(&sub)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to find user by username: %w", result.Error)
	}
	return sub.TelegramID, nil
}

// DeleteSubscriptionByID hard-deletes a subscription by its database ID.
func (s *Service) DeleteSubscriptionByID(ctx context.Context, id uint) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).First(&sub, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to find subscription: %w", result.Error)
	}

	result = s.db.WithContext(ctx).Unscoped().Delete(&sub)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to delete subscription: %w", result.Error)
	}

	return &sub, nil
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

// CountAllSubscriptions returns the total number of subscriptions.
func (s *Service) CountAllSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count all subscriptions: %w", result.Error)
	}
	return count, nil
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
		Where("expiry_time <= ?", time.Now()).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count expired subscriptions: %w", result.Error)
	}
	return count, nil
}

// GetAllTelegramIDs returns all unique Telegram IDs from subscriptions.
func GetAllTelegramIDs() ([]int64, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var ids []int64
	result := DB.Model(&Subscription{}).
		Distinct("telegram_id").
		Pluck("telegram_id", &ids)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get telegram IDs: %w", result.Error)
	}
	return ids, nil
}

// GetTelegramIDByUsername returns the Telegram ID for a given username.
func GetTelegramIDByUsername(username string) (int64, error) {
	if DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	var sub Subscription
	result := DB.Where("username = ?", username).First(&sub)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to find user by username: %w", result.Error)
	}
	return sub.TelegramID, nil
}

// GetTelegramIDsBatch returns a batch of unique Telegram IDs for broadcast.
// offset is the starting position, limit is the maximum number of IDs to return.
func (s *Service) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	var ids []int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Distinct("telegram_id").
		Order("telegram_id ASC").
		Limit(limit).
		Offset(offset).
		Pluck("telegram_id", &ids)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get telegram IDs batch: %w", result.Error)
	}
	return ids, nil
}

// GetTotalTelegramIDCount returns the total count of unique Telegram IDs.
func (s *Service) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Distinct("telegram_id").
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count telegram IDs: %w", result.Error)
	}
	return count, nil
}

// GetOrCreateInvite returns an existing invite for the referrer or creates a new one.
func (s *Service) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*Invite, error) {
	var invite Invite
	result := s.db.WithContext(ctx).Where("referrer_tg_id = ?", referrerTGID).First(&invite)
	if result.Error == nil {
		return &invite, nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to get invite: %w", result.Error)
	}

	invite = Invite{
		Code:         code,
		ReferrerTGID: referrerTGID,
	}
	if err := s.db.WithContext(ctx).Create(&invite).Error; err != nil {
		return nil, fmt.Errorf("failed to create invite: %w", err)
	}
	return &invite, nil
}

// GetInviteByCode returns an invite by its code.
func (s *Service) GetInviteByCode(ctx context.Context, code string) (*Invite, error) {
	var invite Invite
	result := s.db.WithContext(ctx).Where("code = ?", code).First(&invite)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get invite by code: %w", result.Error)
	}
	return &invite, nil
}

// CreateTrialSubscription creates a new trial subscription.
func (s *Service) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*Subscription, error) {
	sub := &Subscription{
		TelegramID:      0,
		SubscriptionID:  subscriptionID,
		ClientID:        clientID,
		InviteCode:      inviteCode,
		InboundID:       inboundID,
		TrafficLimit:    trafficBytes,
		ExpiryTime:      expiryTime,
		SubscriptionURL: subURL,
		IsTrial:         true,
		Status:          "active",
	}
	if err := s.db.WithContext(ctx).Create(sub).Error; err != nil {
		return nil, fmt.Errorf("failed to create trial subscription: %w", err)
	}
	return sub, nil
}

// GetSubscriptionBySubscriptionID returns a subscription by its subscription ID.
func (s *Service) GetSubscriptionBySubscriptionID(ctx context.Context, subscriptionID string) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).Where("subscription_id = ?", subscriptionID).First(&sub)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get subscription by subscription_id: %w", result.Error)
	}
	return &sub, nil
}

// GetTrialSubscriptionBySubID returns a trial subscription by its subscription ID.
func (s *Service) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).Where("subscription_id = ? AND is_trial = ?", subscriptionID, true).First(&sub)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get trial subscription by subscription_id: %w", result.Error)
	}
	return &sub, nil
}

// BindTrialSubscription binds a trial subscription to a Telegram user.
// Uses UPDATE with WHERE to prevent race conditions — if telegram_id was already set
// by a concurrent bind, RowsAffected will be 0.
func (s *Service) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*Subscription, error) {
	var sub Subscription
	if err := s.db.WithContext(ctx).Where("subscription_id = ? AND is_trial = ? AND telegram_id = ?", subscriptionID, true, 0).First(&sub).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("trial subscription not found or already activated")
		}
		return nil, fmt.Errorf("failed to get trial subscription: %w", err)
	}

	var referredBy int64
	if sub.InviteCode != "" {
		var invite Invite
		if err := s.db.WithContext(ctx).Where("code = ?", sub.InviteCode).First(&invite).Error; err == nil {
			referredBy = invite.ReferrerTGID
		}
	}

	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("id = ? AND telegram_id = ? AND is_trial = ?", sub.ID, 0, true).
		Updates(map[string]interface{}{
			"telegram_id": telegramID,
			"username":    username,
			"is_trial":    false,
			"referred_by": referredBy,
		})
	if result.Error != nil {
		return nil, fmt.Errorf("failed to bind trial subscription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, fmt.Errorf("trial subscription not found or already activated")
	}

	sub.TelegramID = telegramID
	sub.Username = username
	sub.IsTrial = false
	sub.ReferredBy = referredBy
	return &sub, nil
}

// CountTrialRequestsByIPLastHour returns the number of trial requests from an IP in the last hour.
func (s *Service) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	var count int64
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	result := s.db.WithContext(ctx).
		Model(&TrialRequest{}).
		Where("ip = ? AND created_at > ?", ip, oneHourAgo).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count trial requests: %w", result.Error)
	}
	return int(count), nil
}

// CreateTrialRequest records a new trial request.
func (s *Service) CreateTrialRequest(ctx context.Context, ip string) error {
	req := &TrialRequest{
		IP: ip,
	}
	if err := s.db.WithContext(ctx).Create(req).Error; err != nil {
		return fmt.Errorf("failed to create trial request: %w", err)
	}
	return nil
}

// CleanupExpiredTrials deletes trial subscriptions that have expired without being activated.
func (s *Service) CleanupExpiredTrials(ctx context.Context, hours int, xuiClient interface {
	DeleteClient(ctx context.Context, inboundID int, clientID string) error
}, inboundID int) (int64, error) {
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	var subs []Subscription
	if err := s.db.WithContext(ctx).
		Where("is_trial = ? AND telegram_id = ? AND created_at < ?", true, 0, cutoff).
		Find(&subs).Error; err != nil {
		return 0, fmt.Errorf("failed to find expired trials: %w", err)
	}

	deletedCount := int64(len(subs))

	for _, sub := range subs {
		if sub.ClientID != "" && xuiClient != nil {
			if err := xuiClient.DeleteClient(ctx, inboundID, sub.ClientID); err != nil {
				logger.Warn("Failed to delete trial client from xui",
					zap.String("client_id", sub.ClientID),
					zap.Error(err))
			}
		}
	}

	if deletedCount > 0 {
		result := s.db.WithContext(ctx).
			Where("is_trial = ? AND telegram_id = ? AND created_at < ?", true, 0, cutoff).
			Delete(&Subscription{})
		if result.Error != nil {
			return 0, fmt.Errorf("failed to cleanup expired trials: %w", result.Error)
		}
	}

	// Cleanup old trial_requests (rate limit records)
	s.db.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Delete(&TrialRequest{})

	return deletedCount, nil
}
