package database

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/logger"

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

	err := sqlDB.QueryRow("select sqlite_version()").Scan(&sqliteVersion)
	if err == nil {
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

			_, err := fmt.Sscanf(v, "%d.%d.%d", &a, &b, &c)
			if err != nil {
				return 0, 0, 0
			}

			return a, b, c
		}
		va, vb, vc := parse(sqliteVersion)

		ma, mb, mc := parse(minSQLiteForDropAndReturning)
		if va < ma || (va == ma && vb < mb) || (va == ma && vb == mb && vc < mc) {
			// scan embedded migrations for DROP COLUMN or RETURNING usage
			migrationNames := []string{"migrations/006_create_sources.up.sql", "migrations/031_add_payment_intent_fields.down.sql"}
			for _, migrationName := range migrationNames {
				if bytes, _ := migrationFiles.ReadFile(migrationName); bytes != nil {
					content := string(bytes)
					if strings.Contains(strings.ToUpper(content), "DROP COLUMN") || strings.Contains(strings.ToUpper(content), "RETURNING") {
						return fmt.Errorf("SQLite version %s does not support required SQL features (DROP COLUMN/RETURNING). Upgrade SQLite to >= %s or run compatible migrations manually", sqliteVersion, minSQLiteForDropAndReturning)
					}
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

	maxEmbeddedVersion, err := latestEmbeddedMigrationVersion()
	if err != nil {
		return fmt.Errorf("failed to determine latest embedded migration: %w", err)
	}

	// Get current version before migration. A database newer than the embedded
	// source is not safe to auto-repair: Force() changes bookkeeping only and
	// cannot recreate an absent migration's schema changes.
	versionBefore, dirtyBefore, versionErr := m.Version()
	if versionErr != nil && !errors.Is(versionErr, migrate.ErrNilVersion) {
		return fmt.Errorf("failed to read migration version: %w", versionErr)
	}
	// #nosec G115 -- maxEmbeddedVersion is guaranteed non-negative: the helper
	// returns an error when no embedded .up.sql migration is found.
	if versionBefore > uint(maxEmbeddedVersion) {
		return fmt.Errorf("database migration version %d is newer than the latest embedded migration %d; restore the missing migration files or perform a reviewed schema recovery before starting", versionBefore, maxEmbeddedVersion)
	}

	if dirtyBefore {
		currentVer, err := migrationVersionToInt(versionBefore)
		if err != nil {
			return fmt.Errorf("invalid dirty migration version: %w", err)
		}

		logger.Warn("Database is in dirty state, forcing migration back",
			zap.Int("current_version", currentVer))

		err = m.Force(currentVer - 1)
		if err != nil {
			return fmt.Errorf("failed to force migration version: %w", err)
		}
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		if strings.Contains(err.Error(), "file does not exist") || strings.Contains(err.Error(), "read down for version") {
			// Never repair a missing migration by changing only schema_migrations.
			// Force() cannot recreate the SQL/schema changes and would make a
			// potentially incompatible database look healthy on the next start.
			return fmt.Errorf("migration failed: %w; database references a missing migration; restore the exact migration files or perform a reviewed schema recovery", err)
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

func latestEmbeddedMigrationVersion() (int, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return 0, fmt.Errorf("read embedded migrations: %w", err)
	}

	maxVersion := -1

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		separator := strings.IndexByte(entry.Name(), '_')
		if separator <= 0 {
			continue
		}

		version, parseErr := strconv.Atoi(entry.Name()[:separator])
		if parseErr != nil || version < 0 {
			continue
		}

		if version > maxVersion {
			maxVersion = version
		}
	}

	if maxVersion < 0 {
		return 0, errors.New("no embedded up migrations found")
	}

	return maxVersion, nil
}

// migrationVersionToInt converts the migrate library's unsigned version to int
// before it is passed to Force. Dirty or failed migration recovery must never
// wrap a large version into a negative value.
func migrationVersionToInt(version uint) (int, error) {
	if version == 0 {
		return 0, errors.New("migration version must be positive")
	}

	maxInt := uint(math.MaxInt)
	if version > maxInt {
		return 0, fmt.Errorf("migration version %d overflows int", version)
	}
	// #nosec G115 -- guarded by the bounds check above
	return int(version), nil
}
