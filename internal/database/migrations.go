package database

import (
	"database/sql"
	"fmt"
	"time"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SchemaMigration tracks applied database migrations
type SchemaMigration struct {
	ID        uint      `gorm:"primaryKey"`
	Name      string    `gorm:"uniqueIndex;size:255"`
	AppliedAt time.Time `gorm:"autoCreateTime"`
}

// Migration represents a database migration
type Migration struct {
	Name string
	SQL  string
}

// migrations is the list of database migrations to apply
// Add new migrations to this list when schema changes are needed
var migrations = []Migration{
	// Example migrations (uncomment and modify when needed):
	// {
	// 	Name: "001_add_traffic_used_column",
	// 	SQL: `ALTER TABLE subscriptions ADD COLUMN traffic_used INTEGER DEFAULT 0;`,
	// },
	// {
	// 	Name: "002_add_notes_column",
	// 	SQL: `ALTER TABLE subscriptions ADD COLUMN notes TEXT DEFAULT '';`,
	// },
}

// RunMigrations applies all pending migrations
func RunMigrations(db *gorm.DB) error {
	// Ensure schema_migrations table exists
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	for _, migration := range migrations {
		applied, err := isMigrationApplied(db, migration.Name)
		if err != nil {
			return err
		}

		if applied {
			continue
		}

		logger.Info("Applying migration", zap.String("name", migration.Name))

		if err := applyMigration(db, migration); err != nil {
			return fmt.Errorf("migration %s failed: %w", migration.Name, err)
		}

		logger.Info("Migration applied", zap.String("name", migration.Name))
	}

	return nil
}

// isMigrationApplied checks if a migration has already been applied
func isMigrationApplied(db *gorm.DB, name string) (bool, error) {
	var count int64
	if err := db.Model(&SchemaMigration{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration executes a migration and records it
func applyMigration(db *gorm.DB, migration Migration) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// Execute the migration SQL
		if migration.SQL != "" {
			if err := tx.Exec(migration.SQL).Error; err != nil {
				return err
			}
		}

		// Record the migration
		return tx.Create(&SchemaMigration{Name: migration.Name}).Error
	})
}

// GetSchemaVersion returns the name of the last applied migration
func GetSchemaVersion() (string, error) {
	if DB == nil {
		return "", fmt.Errorf("database not initialized")
	}

	var migration SchemaMigration
	result := DB.Order("applied_at DESC").First(&migration)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return "initial", nil
		}
		return "", result.Error
	}
	return migration.Name, nil
}

// AddMigration adds a new migration to the migrations list
// This can be used to programmatically add migrations
func AddMigration(name, sqlStr string) {
	migrations = append(migrations, Migration{Name: name, SQL: sqlStr})
}

// GetPendingMigrations returns list of migrations not yet applied
func GetPendingMigrations() ([]Migration, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var pending []Migration
	for _, m := range migrations {
		applied, err := isMigrationApplied(DB, m.Name)
		if err != nil {
			return nil, err
		}
		if !applied {
			pending = append(pending, m)
		}
	}
	return pending, nil
}

// GetAppliedMigrations returns list of applied migrations
func GetAppliedMigrations() ([]SchemaMigration, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var applied []SchemaMigration
	result := DB.Order("applied_at ASC").Find(&applied)
	if result.Error != nil {
		return nil, result.Error
	}
	return applied, nil
}

// ExecSQL executes raw SQL directly (for manual migrations)
func ExecSQL(sqlStr string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	return DB.Exec(sqlStr).Error
}

// GetSQLDB returns the underlying sql.DB for advanced operations
func GetSQLDB() (*sql.DB, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return DB.DB()
}
