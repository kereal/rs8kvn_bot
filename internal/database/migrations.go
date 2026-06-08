package database

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"

	"rs8kvn_bot/internal/logger"

	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

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
