package database

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"

	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ErrInviteNotFound is the sentinel returned (via errors.Is) by GetInviteByCode
// when the invite code does not exist. Allows callers (e.g. handlers) to
// distinguish "invalid code" (not found) from infrastructure/DB errors.
var ErrInviteNotFound = errors.New("invite not found")

//go:embed migrations/*.sql
var migrationFiles embed.FS

const (
	TrialPlanName = "trial"
	FreePlanName  = "free"
)

// Subscription represents a user's VPN subscription.
type Subscription struct {
	ID             uint      `gorm:"primaryKey"`
	TelegramID     int64     `gorm:"index"`
	Username       string    `gorm:"size:255;index"`
	ClientID       string    `gorm:"size:255"`
	SubscriptionID string    `gorm:"size:255;index"`
	ExpiresAt      time.Time `gorm:"index:idx_expiry"`
	Status         string    `gorm:"default:active;size:50;index"`
	InviteCode     string    `gorm:"size:16;index"`
	PlanID         uint      `gorm:"index"`
	ReferredBy     int64     `gorm:"index"`
	Devices        string    `gorm:"type:text;default:'[]'"` // JSON array of {header_key: value} device entries
	Ips            string    `gorm:"type:text;default:'[]'"` // JSON array of {ip: timestamp} entries
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

// Node represents a configured 3x-ui panel source.
type Node struct {
	ID              uint      `gorm:"primaryKey;column:id"`
	Name            string    `gorm:"size:255;column:name"`
	IsActive        bool      `gorm:"default:true;column:is_active"`
	Host            string    `gorm:"size:255;column:host"`
	APIToken        string    `gorm:"size:255;column:api_token"`
	InboundID       int       `gorm:"not null;column:inbound_id"`
	SubscriptionURL string    `gorm:"size:512;column:subscription_url"`
	Type            string    `gorm:"type:varchar(10);not null;default: x-ui;column:type" json:"type"`
	CreatedAt       time.Time `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime;column:updated_at"`
}

// Plan represents a subscription plan.
type Plan struct {
	ID           uint      `gorm:"primaryKey;column:id"`
	Name         string    `gorm:"size:50;uniqueIndex;column:name"`
	Price        float64   `gorm:"default:0;column:price"`
	DevicesLimit int       `gorm:"default:1;column:devices_limit"`
	TrafficLimit int64     `gorm:"default:0;column:traffic_limit"`
	Duration     int       `gorm:"default:0;column:duration"` // hours, 0=unlimited
	CreatedAt    time.Time `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime;column:updated_at"`
}

// PlanNode is the join model for M2M between Plan and Node.
type PlanNode struct {
	PlanID uint `gorm:"primaryKey;column:plan_id"`
	NodeID uint `gorm:"primaryKey;column:node_id"`
}

// Invite represents a referral invite code.
type Invite struct {
	Code         string    `gorm:"primaryKey;size:16"`
	ReferrerTGID int64     `gorm:"index;not null"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
}

// TrialRequest tracks trial requests for rate limiting.
type TrialRequest struct {
	ID        uint      `gorm:"primaryKey"`
	IP        string    `gorm:"size:45;index"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

// TableName returns the table name for Node.
func (Node) TableName() string {
	return "nodes"
}

// TableName returns the table name for Plan.
func (Plan) TableName() string {
	return "plans"
}

// TableName returns the table name for PlanNode.
func (PlanNode) TableName() string {
	return "plan_nodes"
}

// TableName returns the table name for Subscription.
func (Subscription) TableName() string {
	return "subscriptions"
}

// TableName returns the table name for Invite.
func (Invite) TableName() string {
	return "invites"
}

// TableName returns the table name for TrialRequest.
func (TrialRequest) TableName() string {
	return "trial_requests"
}

// IsExpired returns true if the subscription has expired.
// A zero ExpiresAt means no expiry is set, so it is not considered expired.
func (s *Subscription) IsExpired() bool {
	if s.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(s.ExpiresAt)
}

// IsActive returns true if the subscription is active and not expired.
func (s *Subscription) IsActive() bool {
	return s.Status == "active" && !s.IsExpired()
}

// GetDevices parses the Devices JSON string into a slice of header maps.
func (s *Subscription) GetDevices() ([]map[string]string, error) {
	if s.Devices == "" {
		return []map[string]string{}, nil
	}
	var devices []map[string]string
	if err := json.Unmarshal([]byte(s.Devices), &devices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal devices: %w", err)
	}
	return devices, nil
}

// SetDevices serializes a slice of header maps into the Devices JSON string.
func (s *Subscription) SetDevices(devices []map[string]string) error {
	data, err := json.Marshal(devices)
	if err != nil {
		return fmt.Errorf("failed to marshal devices: %w", err)
	}
	s.Devices = string(data)
	return nil
}

// GetIPs parses the Ips JSON string into a slice of ip->timestamp maps.
func (s *Subscription) GetIPs() ([]map[string]string, error) {
	if s.Ips == "" {
		return []map[string]string{}, nil
	}
	var ips []map[string]string
	if err := json.Unmarshal([]byte(s.Ips), &ips); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ips: %w", err)
	}
	return ips, nil
}

// SetIPs serializes a slice of ip->timestamp maps into the Ips JSON string.
func (s *Subscription) SetIPs(ips []map[string]string) error {
	data, err := json.Marshal(ips)
	if err != nil {
		return fmt.Errorf("failed to marshal ips: %w", err)
	}
	s.Ips = string(data)
	return nil
}

// SubscriptionFull holds a subscription together with its plan and active nodes.
type SubscriptionFull struct {
	Subscription
	Plan  Plan
	Nodes []Node
}

// runMigrations applies the embedded SQL schema migrations to the provided database,
// handling legacy subscriptions-table adjustments and one-time referral bootstrap.
//
// When an older subscriptions table is detected, it performs manual legacy adjustments
// (e.g. adding subscription_id). If referral columns (`invite_code`, `is_trial`, `referred_by`)
// were added outside of migrations (before 003 existed), it performs a one-time m.Force(3)
// bootstrap. Unlike the previous hack, it does NOT early-return — this ensures that
// all subsequent embedded migrations (004, 005, ...) are still applied on legacy DBs.
//
// The function returns an error if creating migration drivers or applying migrations fails.
func runMigrations(sqlDB *sql.DB) error {
	// Determine SQLite version to verify features (DROP COLUMN, RETURNING) availability
	var sqliteVersion string
	if err := sqlDB.QueryRow("select sqlite_version()").Scan(&sqliteVersion); err == nil {
		logger.Info("SQLite version detected", zap.String("version", sqliteVersion))
	} else {
		logger.Warn("Failed to detect SQLite version", zap.Error(err))
	}

	const minSQLiteForDropAndReturning = "3.35.0"
	// If embedded migrations contain potentially incompatible SQL, fail early on older SQLite
	if sqliteVersion != "" {
		// simple semver compare: major.minor.patch
		parse := func(v string) (int, int, int) {
			var a, b, c int
			fmt.Sscanf(v, "%d.%d.%d", &a, &b, &c)
			return a, b, c
		}
		va, vb, vc := parse(sqliteVersion)
		ma, mb, mc := parse(minSQLiteForDropAndReturning)
		if va < ma || (va == ma && vb < mb) || (va == ma && vb == mb && vc < mc) {
			// scan embedded migrations for DROP COLUMN or RETURNING usage
			if bytes, _ := migrationFiles.ReadFile("migrations/006_create_sources.up.sql"); bytes != nil {
				content := string(bytes)
				if strings.Contains(strings.ToUpper(content), "DROP COLUMN") || strings.Contains(strings.ToUpper(content), "RETURNING") {
					return fmt.Errorf("SQLite version %s does not support required SQL features (DROP COLUMN/RETURNING). Upgrade SQLite to >= %s or run compatible migrations manually", sqliteVersion, minSQLiteForDropAndReturning)
				}
			}
		}
	}

	// Create embedded source driver from migrationFiles
	sourceDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create embedded migration source: %w", err)
	}

	// Create SQLite driver
	driver, err := sqlite.WithInstance(sqlDB, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migrate driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	// Get current version before migration
	versionBefore, dirtyBefore, _ := m.Version()

	if dirtyBefore {
		currentVer := int(versionBefore)
		logger.Warn("Database is in dirty state, forcing migration back",
			zap.Int("current_version", currentVer))
		if err := m.Force(currentVer - 1); err != nil {
			return fmt.Errorf("failed to force migration version: %w", err)
		}
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		if strings.Contains(err.Error(), "file does not exist") || strings.Contains(err.Error(), "read down for version") {
			forceVer := int(versionBefore) - 1
			logger.Warn("Missing migration file detected, forcing version to last known good state",
				zap.Int("forced_version", forceVer))
			if forceErr := m.Force(forceVer); forceErr != nil {
				return fmt.Errorf("migration failed: %w; additionally failed to force version: %w", err, forceErr)
			}
			logger.Info("Database version forced due to missing migration files",
				zap.Int("forced_version", forceVer))
			return nil
		}
		return fmt.Errorf("migration failed: %w", err)
	}

	// Get version after migration
	versionAfter, _, _ := m.Version()

	if versionAfter > versionBefore {
		logger.Info("Database migrations applied",
			zap.Uint("version", versionAfter))
	} else {
		logger.Info("Database migrations up to date",
			zap.Uint("version", versionAfter))
	}

	return nil
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
			Price:        0,
			DevicesLimit: 1,
			TrafficLimit: 1073741824,
			Duration:     3,
		}).Error; err != nil {
			return nil, fmt.Errorf("failed to seed default trial plan: %w", err)
		}
		if err := db.WithContext(context.Background()).Create(&Plan{
			Name:         FreePlanName,
			Price:        0,
			DevicesLimit: 1,
			TrafficLimit: 53687091200,
			Duration:     0,
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
		return nil, fmt.Errorf("failed to get subscription by telegram ID: %w", result.Error)
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
// If inviteCode is non-empty and resolves to a valid Invite, sub.InviteCode and sub.ReferredBy
// are populated atomically inside the same transaction.
func (s *Service) CreateSubscription(ctx context.Context, sub *Subscription, inviteCode string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Revoke any existing active subscriptions for this user
		if err := tx.Model(&Subscription{}).
			Where("telegram_id = ? AND status = ?", sub.TelegramID, "active").
			Update("status", "revoked").Error; err != nil {
			return fmt.Errorf("failed to revoke old subscription: %w", err)
		}

		// Resolve referral invite atomically. A missing invite is non-fatal:
		// the subscription is still created without referral attribution.
		if inviteCode != "" {
			var inv Invite
			if err := tx.Where("code = ?", inviteCode).First(&inv).Error; err == nil {
				sub.InviteCode = inviteCode
				sub.ReferredBy = inv.ReferrerTGID
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("failed to resolve invite: %w", err)
			}
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
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("id = ?", sub.ID).
		Select("telegram_id", "username", "client_id", "subscription_id", "expires_at", "status", "invite_code", "plan_id", "referred_by", "devices", "ips").
		Updates(sub)
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription: %w", result.Error)
	}
	return nil
}

// DeleteSubscription deletes a subscription.
func (s *Service) DeleteSubscription(ctx context.Context, telegramID int64) error {
	result := s.db.WithContext(ctx).Where("telegram_id = ?", telegramID).Delete(&Subscription{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete subscription: %w", result.Error)
	}
	return nil
}

// ListNodes returns all configured nodes.
func (s *Service) ListNodes(ctx context.Context) ([]Node, error) {
	var nodes []Node
	result := s.db.WithContext(ctx).Find(&nodes)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", result.Error)
	}
	return nodes, nil
}

// GetNodesByPlanName returns nodes for the plan with the given name.
func (s *Service) GetNodesByPlanName(ctx context.Context, planName string) ([]Node, error) {
	var nodes []Node
	result := s.db.WithContext(ctx).
		Table("nodes").
		Select("nodes.*").
		Joins("JOIN plan_nodes ON plan_nodes.node_id = nodes.id").
		Joins("JOIN plans ON plans.id = plan_nodes.plan_id").
		Where("plans.name = ?", planName).
		Find(&nodes)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get nodes by plan name: %w", result.Error)
	}
	return nodes, nil
}

// GetPlanByName returns a plan by its name.
func (s *Service) GetPlanByName(ctx context.Context, name string) (*Plan, error) {
	var plan Plan
	result := s.db.WithContext(ctx).Where("name = ?", name).First(&plan)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get plan by name: %w", result.Error)
	}
	return &plan, nil
}

// GetPlanByID returns a plan by its ID.
func (s *Service) GetPlanByID(ctx context.Context, id uint) (*Plan, error) {
	var plan Plan
	result := s.db.WithContext(ctx).First(&plan, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get plan by id: %w", result.Error)
	}
	return &plan, nil
}

// IsNodesEmpty returns true if no nodes exist in the database.
func (s *Service) IsNodesEmpty(ctx context.Context) (bool, error) {
	var count int64
	result := s.db.WithContext(ctx).Model(&Node{}).Count(&count)
	if result.Error != nil {
		return false, fmt.Errorf("failed to count nodes: %w", result.Error)
	}
	return count == 0, nil
}

// SeedDefaultNode inserts the default node from environment variables if the nodes table is empty.
// It also links all existing plans to the new node and assigns the free plan to legacy subscriptions.
func (s *Service) SeedDefaultNode(ctx context.Context, name, host, apiToken string, inboundID int, subscriptionURL string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node := Node{
			Name:            name,
			IsActive:        true,
			Host:            host,
			APIToken:        apiToken,
			InboundID:       inboundID,
			SubscriptionURL: subscriptionURL,
			Type:            "x-ui",
		}
		if err := tx.Create(&node).Error; err != nil {
			return err
		}
		var plans []Plan
		if err := tx.Find(&plans).Error; err != nil {
			return err
		}
		for _, p := range plans {
			pn := PlanNode{PlanID: p.ID, NodeID: node.ID}
			if err := tx.Create(&pn).Error; err != nil {
				return fmt.Errorf("failed to link plan %d to node %d: %w", p.ID, node.ID, err)
			}
		}
		return tx.Exec(
			`UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE name = ?) WHERE plan_id IS NULL`,
			FreePlanName,
		).Error
	})
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

// DeleteSubscriptionByID soft-deletes a subscription by its database ID.
func (s *Service) DeleteSubscriptionByID(ctx context.Context, id uint) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).First(&sub, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to find subscription: %w", result.Error)
	}

	result = s.db.WithContext(ctx).Delete(&sub)
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

// CountActiveSubscriptions returns the number of active subscriptions.
func (s *Service) CountActiveSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&Subscription{}).
		Where("status = ?", "active").
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
		Where("expires_at <= ?", time.Now()).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count expired subscriptions: %w", result.Error)
	}
	return count, nil
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

// GetInviteByReferrer returns the canonical (oldest) invite code for the given referrer.
// If the user has multiple historical codes (pre-005 duplicates), returns the one with the smallest created_at.
// Returns ErrInviteNotFound when no invite exists for this referrer.
func (s *Service) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*Invite, error) {
	var invite Invite
	result := s.db.WithContext(ctx).
		Where("referrer_tg_id = ?", referrerTGID).
		Order("created_at ASC, code ASC").
		First(&invite)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("failed to get invite by referrer: %w", result.Error)
	}
	return &invite, nil
}

// GetOrCreateInvite returns an existing invite for the referrer or creates a new one.
// It always returns the oldest (canonical) code for the user.
// After migration 005 the unique constraint guarantees at most one row per referrer_tg_id.
func (s *Service) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*Invite, error) {
	// First try to return existing canonical code (oldest)
	if existing, err := s.GetInviteByReferrer(ctx, referrerTGID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrInviteNotFound) {
		return nil, err
	}

	// No invite yet — create one with the proposed code
	now := time.Now()
	if err := s.db.WithContext(ctx).Exec(
		"INSERT INTO invites (code, referrer_tg_id, created_at) VALUES (?, ?, ?)",
		code, referrerTGID, now,
	).Error; err != nil {
		// Race: someone else just created it — read the canonical one
		if existing, err2 := s.GetInviteByReferrer(ctx, referrerTGID); err2 == nil {
			return existing, nil
		}
		return nil, fmt.Errorf("failed to create invite after race: %w", err)
	}

	return &Invite{
		Code:         code,
		ReferrerTGID: referrerTGID,
		CreatedAt:    now,
	}, nil
}

// GetInviteByCode returns an invite by its code.
// Returns ErrInviteNotFound (such that errors.Is(err, ErrInviteNotFound) is true)
// when the code does not exist. Other errors are infrastructure failures (DB, etc).
func (s *Service) GetInviteByCode(ctx context.Context, code string) (*Invite, error) {
	var invite Invite
	result := s.db.WithContext(ctx).Where("code = ?", code).First(&invite)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("failed to get invite by code: %w", result.Error)
	}
	return &invite, nil
}

// GetReferralCount returns the number of referrals for a user.
func (s *Service) GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("referred_by = ?", referrerTGID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count referrals: %w", err)
	}
	return count, nil
}

// GetAllReferralCounts returns a map of referrer TGID to referral count.
func (s *Service) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	type ReferralCount struct {
		ReferredBy int64
		Count      int64
	}
	var results []ReferralCount

	if err := s.db.WithContext(ctx).Model(&Subscription{}).
		Select("referred_by, COUNT(*) as count").
		Where("referred_by > 0").
		Group("referred_by").
		Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("failed to get referral counts: %w", err)
	}

	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.ReferredBy] = r.Count
	}
	return counts, nil
}

// CreateTrialSubscription creates a new trial subscription.
func (s *Service) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*Subscription, error) {
	planID, err := s.resolveTrialPlanID(ctx)
	if err != nil {
		return nil, err
	}

	sub := &Subscription{
		TelegramID:     0,
		SubscriptionID: subscriptionID,
		ClientID:       clientID,
		InviteCode:     inviteCode,
		ExpiresAt:      expiryTime,
		PlanID:         planID,
		Status:         "active",
	}
	if err := s.db.WithContext(ctx).Create(sub).Error; err != nil {
		return nil, fmt.Errorf("failed to create trial subscription: %w", err)
	}
	return sub, nil
}

func (s *Service) resolveTrialPlanID(ctx context.Context) (uint, error) {
	var plan Plan
	if err := s.db.WithContext(ctx).Where("name = ?", TrialPlanName).First(&plan).Error; err != nil {
		return 0, fmt.Errorf("trial plan not found: %w", err)
	}
	return plan.ID, nil
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

// GetSubscriptionStatus returns only the status and expiry time for a subscription
// by its subscription_id. It is intended for cheap cache-hit checks in the
// subscription server (since v2.3.0) — it avoids the full JOIN with plans and
// sources required by GetSubscriptionWithPlanAndSources. Returns
// gorm.ErrRecordNotFound if no row matches.
func (s *Service) GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, time.Time, error) {
	var row struct {
		Status    string
		ExpiresAt time.Time
	}
	result := s.db.WithContext(ctx).
		Table("subscriptions").
		Select("status, expires_at").
		Where("subscription_id = ?", subscriptionID).
		Scan(&row)
	if result.Error != nil {
		return "", time.Time{}, result.Error
	}
	if result.RowsAffected == 0 {
		return "", time.Time{}, gorm.ErrRecordNotFound
	}
	return row.Status, row.ExpiresAt, nil
}

// GetSubscriptionWithPlanAndNodes returns a subscription (status=active) by subscription ID
// together with its plan and active nodes, via JOINs through plan_nodes.
func (s *Service) GetSubscriptionWithPlanAndNodes(ctx context.Context, subscriptionID string) (*SubscriptionFull, error) {
	var result SubscriptionFull

	subQuery := s.db.WithContext(ctx).Where("subscription_id = ? AND status = ?", subscriptionID, "active")

	if err := subQuery.First(&result.Subscription).Error; err != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	if err := s.db.WithContext(ctx).First(&result.Plan, result.Subscription.PlanID).Error; err != nil {
		return nil, fmt.Errorf("failed to get plan: %w", err)
	}

	if err := s.db.WithContext(ctx).
		Table("nodes").
		Select("nodes.*").
		Joins("JOIN plan_nodes ON plan_nodes.node_id = nodes.id").
		Where("plan_nodes.plan_id = ? AND nodes.is_active = ?", result.Plan.ID, true).
		Find(&result.Nodes).Error; err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	return &result, nil
}

// UpdateSubscriptionDevices updates only the devices JSON column for a subscription.
func (s *Service) UpdateSubscriptionDevices(ctx context.Context, id uint, devicesJSON string) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).Where("id = ?", id).Update("devices", devicesJSON)
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription devices: %w", result.Error)
	}
	return nil
}

// UpdateSubscriptionIPs updates only the ips JSON column for a subscription.
func (s *Service) UpdateSubscriptionIPs(ctx context.Context, id uint, ipsJSON string) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).Where("id = ?", id).Update("ips", ipsJSON)
	if result.Error != nil {
		return fmt.Errorf("failed to update subscription ips: %w", result.Error)
	}
	return nil
}

// GetTrialSubscriptionBySubID returns a trial subscription by its subscription ID.
// A subscription is considered trial if its plan has name 'trial'.
func (s *Service) GetTrialSubscriptionBySubID(ctx context.Context, subscriptionID string) (*Subscription, error) {
	var sub Subscription
	result := s.db.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		First(&sub)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get trial subscription by subscription_id: %w", result.Error)
	}

	var plan Plan
	if err := s.db.WithContext(ctx).Where("id = ?", sub.PlanID).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("subscription is not a trial")
		}
		return nil, fmt.Errorf("failed to get plan for trial check: %w", err)
	}
	if plan.Name != TrialPlanName {
		return nil, fmt.Errorf("subscription is not a trial")
	}
	return &sub, nil
}

// BindTrialSubscription binds a trial subscription to a Telegram user.
// Uses UPDATE with WHERE to prevent race conditions — if telegram_id was already set
// by a concurrent bind, RowsAffected will be 0.
func (s *Service) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*Subscription, error) {
	var sub Subscription
	var referredBy int64
	var freePlanID uint

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var trialPlan Plan
		if err := tx.Where("name = ?", TrialPlanName).First(&trialPlan).Error; err != nil {
			return fmt.Errorf("failed to resolve trial plan: %w", err)
		}
		planID := trialPlan.ID

		if err := tx.Where("subscription_id = ? AND plan_id = ? AND telegram_id = ?", subscriptionID, planID, 0).First(&sub).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("trial subscription not found or already activated")
			}
			return fmt.Errorf("failed to get trial subscription: %w", err)
		}

		if sub.InviteCode != "" {
			var invite Invite
			if err := tx.Where("code = ?", sub.InviteCode).First(&invite).Error; err == nil {
				referredBy = invite.ReferrerTGID
			}
		}

		var freePlan Plan
		if err := tx.Where("name = ?", FreePlanName).First(&freePlan).Error; err != nil {
			return fmt.Errorf("failed to resolve free plan: %w", err)
		}
		freePlanID = freePlan.ID
		result := tx.Model(&Subscription{}).
			Where("id = ? AND telegram_id = ? AND plan_id = ?", sub.ID, 0, planID).
			Updates(map[string]any{
				"telegram_id": telegramID,
				"username":    username,
				"plan_id":     freePlanID,
				"referred_by": referredBy,
			})
		if result.Error != nil {
			return fmt.Errorf("failed to bind trial subscription: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("trial subscription not found or already activated")
		}

		// Defensive: revoke any other active subscription the user may already have
		// (e.g. a free-plan sub created concurrently via /start). Without this, the
		// user could end up with two active subscriptions.
		if err := tx.Model(&Subscription{}).
			Where("telegram_id = ? AND status = ? AND id <> ?", telegramID, "active", sub.ID).
			Update("status", "revoked").Error; err != nil {
			return fmt.Errorf("failed to revoke pre-existing active subscriptions: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sub.TelegramID = telegramID
	sub.Username = username
	sub.PlanID = freePlanID
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
// Uses atomic DELETE ... RETURNING to prevent race conditions with concurrent trial activation.
func (s *Service) CleanupExpiredTrials(ctx context.Context, hours int) ([]Subscription, error) {
	var trialPlan Plan
	if err := s.db.WithContext(ctx).Where("name = ?", TrialPlanName).First(&trialPlan).Error; err != nil {
		return nil, fmt.Errorf("failed to resolve trial plan: %w", err)
	}

	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	var subs []Subscription
	result := s.db.WithContext(ctx).Raw(
		`DELETE FROM subscriptions
		 WHERE plan_id = ? AND telegram_id = ? AND created_at < ?
		 RETURNING id, client_id, subscription_id`,
		trialPlan.ID, 0, cutoff,
	).Scan(&subs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to cleanup expired trials: %w", result.Error)
	}

	rateLimitCutoff := time.Now().Add(-1*time.Hour + 1*time.Second)
	s.db.WithContext(ctx).
		Where("created_at < ?", rateLimitCutoff).
		Delete(&TrialRequest{})

	return subs, nil
}
