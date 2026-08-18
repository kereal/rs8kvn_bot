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
			migrationNames := []string{"migrations/006_create_sources.up.sql", "migrations/031_add_payment_intent_fields.down.sql", "migrations/035_add_order_payment_amounts.down.sql"}
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

	maxEmbeddedVersion, err := latestEmbeddedMigrationVersion()
	if err != nil {
		return fmt.Errorf("failed to determine latest embedded migration: %w", err)
	}

	versionBefore, dirtyBefore, err := migrationState(sqlDB)
	if err != nil {
		return fmt.Errorf("failed to read migration version: %w", err)
	}

	// #nosec G115 -- maxEmbeddedVersion is guaranteed non-negative: the helper
	// returns an error when no embedded .up.sql migration is found.
	if versionBefore > uint(maxEmbeddedVersion) {
		return fmt.Errorf("database migration version %d is newer than the latest embedded migration %d; restore the missing migration files or perform a reviewed schema recovery before starting", versionBefore, maxEmbeddedVersion)
	}

	if dirtyBefore {
		err = recoverDirtyMigration(sqlDB, versionBefore)
		if err != nil {
			return err
		}
	}

	err = applyMigrations(sqlDB, maxEmbeddedVersion)
	if err != nil {
		return err
	}

	versionAfter, _, err := migrationState(sqlDB)
	if err != nil {
		return fmt.Errorf("failed to read migration version after migration: %w", err)
	}

	if versionAfter > versionBefore {
		logger.Info("Database migrations applied",
			zap.Uint("version", versionAfter))
	} else {
		logger.Info("Database migrations up to date",
			zap.Uint("version", versionAfter))
	}

	return nil
}

// migrationState reads metadata using the transactional driver configuration.
// NoTxWrap is relevant to applying a migration, not to reading or updating the
// bookkeeping row.
func migrationState(sqlDB *sql.DB) (uint, bool, error) {
	m, err := newMigration(sqlDB, false)
	if err != nil {
		return 0, false, err
	}
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}

	return version, dirty, err
}

// applyMigrations keeps ordinary migrations transactional and runs only the
// migrations that change PRAGMA foreign_keys through the explicit no-transaction
// path. A single NoTxWrap driver cannot be used for the whole history: it would
// remove rollback protection from otherwise ordinary migrations.
func applyMigrations(sqlDB *sql.DB, maxVersion int) error {
	for {
		version, dirty, err := migrationState(sqlDB)
		if err != nil {
			return fmt.Errorf("failed to read migration state: %w", err)
		}
		if dirty {
			return fmt.Errorf("migration state became dirty at version %d; schema recovery is required before retrying", version)
		}
		if version >= uint(maxVersion) {
			return nil
		}

		currentVersion := int(version)
		nextPragmaVersion, err := nextForeignKeysMigration(currentVersion, maxVersion)
		if err != nil {
			return fmt.Errorf("failed to classify migrations: %w", err)
		}

		if nextPragmaVersion < 0 {
			err = migrateTo(sqlDB, maxVersion, false)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}
			continue
		}

		// Bring the database to the migration immediately before the special one
		// with the normal transactional driver.
		if currentVersion < nextPragmaVersion-1 {
			err = migrateTo(sqlDB, nextPragmaVersion-1, false)
			if err != nil {
				return fmt.Errorf("migration failed before foreign-key migration %d: %w", nextPragmaVersion, err)
			}
			continue
		}

		// This is deliberately the only NoTxWrap invocation. If it fails, the
		// dirty marker is left untouched and the caller must inspect/recover the
		// schema before any metadata repair is attempted.
		err = migrateTo(sqlDB, nextPragmaVersion, true)
		if err != nil {
			return fmt.Errorf("foreign-key migration %d failed; schema may be partially applied: %w", nextPragmaVersion, err)
		}
	}
}

func migrateTo(sqlDB *sql.DB, targetVersion int, noTxWrap bool) error {
	m, err := newMigration(sqlDB, noTxWrap)
	if err != nil {
		return err
	}
	err = m.Migrate(uint(targetVersion))
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}

	return err
}

func newMigration(sqlDB *sql.DB, noTxWrap bool) (*migrate.Migrate, error) {
	sourceDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to create embedded migration source: %w", err)
	}

	driver, err := sqlite.WithInstance(sqlDB, &sqlite.Config{NoTxWrap: noTxWrap})
	if err != nil {
		return nil, fmt.Errorf("failed to create migrate driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", driver)
	if err != nil {
		return nil, fmt.Errorf("failed to create migration instance: %w", err)
	}

	return m, nil
}

// recoverDirtyMigration is intentionally conservative. Transactional
// migrations can be safely rewound in metadata because their SQL was rolled
// back. A NoTxWrap migration is different: Force(previous) would hide a
// partially rebuilt table. First inspect the resulting schema; only a complete
// schema is allowed to be marked clean at the current version.
func recoverDirtyMigration(sqlDB *sql.DB, version uint) error {
	currentVersion, err := migrationVersionToInt(version)
	if err != nil {
		return fmt.Errorf("invalid dirty migration version: %w", err)
	}

	requiresForeignKeys, err := migrationRequiresForeignKeys(currentVersion)
	if err != nil {
		return fmt.Errorf("failed to classify dirty migration %d: %w", currentVersion, err)
	}

	if requiresForeignKeys {
		complete, err := foreignKeysMigrationSchemaComplete(sqlDB, currentVersion)
		if err != nil {
			return fmt.Errorf("failed to inspect schema for dirty migration %d: %w", currentVersion, err)
		}
		if !complete {
			return fmt.Errorf("migration %d is dirty and its non-transactional schema is incomplete; refusing to change migration metadata; restore the schema manually before retrying", currentVersion)
		}

		err = forceMigrationVersion(sqlDB, currentVersion)
		if err != nil {
			return fmt.Errorf("failed to mark recovered migration %d as clean: %w", currentVersion, err)
		}
		return nil
	}

	previousVersion := currentVersion - 1
	err = forceMigrationVersion(sqlDB, previousVersion)
	if err != nil {
		return fmt.Errorf("failed to rewind transactional migration version: %w", err)
	}

	return nil
}

func forceMigrationVersion(sqlDB *sql.DB, version int) error {
	m, err := newMigration(sqlDB, false)
	if err != nil {
		return err
	}
	return m.Force(version)
}

func nextForeignKeysMigration(currentVersion, maxVersion int) (int, error) {
	for version := currentVersion + 1; version <= maxVersion; version++ {
		requires, err := migrationRequiresForeignKeys(version)
		if err != nil {
			return 0, err
		}
		if requires {
			return version, nil
		}
	}

	return -1, nil
}

func migrationRequiresForeignKeys(version int) (bool, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return false, fmt.Errorf("read embedded migrations: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		separator := strings.IndexByte(entry.Name(), '_')
		if separator <= 0 {
			continue
		}
		entryVersion, parseErr := strconv.Atoi(entry.Name()[:separator])
		if parseErr != nil || entryVersion != version {
			continue
		}

		migration, readErr := migrationFiles.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			return false, fmt.Errorf("read migration %s: %w", entry.Name(), readErr)
		}

		return strings.Contains(strings.ToUpper(string(migration)), "PRAGMA FOREIGN_KEYS"), nil
	}

	return false, nil
}

func foreignKeysMigrationSchemaComplete(sqlDB *sql.DB, version int) (bool, error) {
	var complete bool
	var err error
	var fkTable string

	switch version {
	case 27:
		fkTable = "subscription_nodes"
		complete, err = tableRebuildComplete(sqlDB, fkTable, "subscription_nodes_old", "pending_update")
	case 33:
		fkTable = "subscriptions"
		complete, err = tableRebuildComplete(sqlDB, fkTable, "subscriptions_old", "status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK")
		if complete {
			var invalidRows int
			err = sqlDB.QueryRow(`SELECT COUNT(*) FROM subscriptions
				WHERE status IS NULL OR status NOT IN ('active', 'expired', 'paused', 'canceled', 'revoked')`).Scan(&invalidRows)
			if err != nil {
				complete = false
			} else {
				complete = invalidRows == 0
			}
		}
	case 34:
		fkTable = "orders"
		complete, err = tableRebuildComplete(sqlDB, fkTable, "orders_old", "ON DELETE CASCADE")
	default:
		return false, fmt.Errorf("no schema recovery verifier for migration %d", version)
	}
	if err != nil || !complete {
		return complete, err
	}

	var integrity string
	err = sqlDB.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(integrity, "ok") {
		return false, nil
	}

	// PRAGMA integrity_check does not report foreign-key violations. A failed
	// table-rebuild migration may leave the new table structurally complete
	// while retaining orphan rows, so metadata must not be repaired until the
	// separate foreign-key check is clean as well. The check is narrowed to the
	// rebuilt table: a table rebuild copies every row (INSERT ... SELECT), so
	// its own orphans are the only data damage it can introduce. Unrelated
	// foreign-key violations elsewhere (legacy data) must not block recovery.
	var foreignKeyViolations int
	err = sqlDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM pragma_foreign_key_check('%s')", fkTable)).Scan(&foreignKeyViolations)
	if err != nil {
		return false, err
	}
	if foreignKeyViolations > 0 {
		return false, fmt.Errorf("foreign key check found %d violations", foreignKeyViolations)
	}

	return true, nil
}

func tableRebuildComplete(sqlDB *sql.DB, tableName, oldTableName, requiredSQL string) (bool, error) {
	var tableSQL sql.NullString
	err := sqlDB.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, tableName).Scan(&tableSQL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !tableSQL.Valid || !strings.Contains(strings.ToUpper(tableSQL.String), strings.ToUpper(requiredSQL)) {
		return false, nil
	}

	var oldTableCount int
	err = sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, oldTableName).Scan(&oldTableCount)
	if err != nil {
		return false, err
	}

	return oldTableCount == 0, nil
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
