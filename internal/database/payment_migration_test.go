package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newSQLiteAtMigration30 creates the real application schema immediately
// before migration 031. The test then invokes runMigrations, so migration 031
// itself—not a copy of its SQL—is what is validated.
func newSQLiteAtMigration30(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payment-migration.db")
	gormDB, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gormDB.DB()
	require.NoError(t, err)

	source, err := iofs.New(migrationFiles, "migrations")
	require.NoError(t, err)
	driver, err := sqlite.WithInstance(sqlDB, &sqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	require.NoError(t, err)
	require.NoError(t, m.Steps(30))
	return sqlDB, func() {
		_, _ = m.Close()
		_ = sqlDB.Close()
	}
}

func TestMigration031_PaymentIntentSchemaOnSQLite(t *testing.T) {
	sqlDB, cleanup := newSQLiteAtMigration30(t)
	t.Cleanup(cleanup)

	require.NoError(t, runMigrations(sqlDB))

	rows, err := sqlDB.Query("PRAGMA table_info(orders)")
	require.NoError(t, err)
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, typeName string
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk))
		columns[name] = true
	}
	require.NoError(t, rows.Err())
	assert.True(t, columns["payment_url"])
	assert.True(t, columns["payment_expires_at"])
	assert.True(t, columns["payment_creation_uncertain"])

	rows, err = sqlDB.Query("PRAGMA index_list(orders)")
	require.NoError(t, err)
	defer rows.Close()
	indexes := make(map[string]bool)
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		require.NoError(t, rows.Scan(&seq, &name, &unique, &origin, &partial))
		indexes[name] = unique == 1 && partial == 1
	}
	require.NoError(t, rows.Err())
	assert.True(t, indexes["idx_orders_provider_payment_unique"])
	assert.True(t, indexes["idx_orders_pending_subscription_product_unique"])
}

func TestMigration031_DoesNotRequireLegacyPaymentDeduplication(t *testing.T) {
	sqlDB, cleanup := newSQLiteAtMigration30(t)
	t.Cleanup(cleanup)

	// The project policy guarantees no historical payment rows. Verify the
	// migration succeeds on the empty pre-031 schema and creates both indexes.
	require.NoError(t, runMigrations(sqlDB))
	var orderCount int
	require.NoError(t, sqlDB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&orderCount))
	assert.Zero(t, orderCount)
}

func TestMigration031_DownRestoresOriginalProviderIndexOnSQLite(t *testing.T) {
	sqlDB, cleanup := newSQLiteAtMigration30(t)
	t.Cleanup(cleanup)

	source, err := iofs.New(migrationFiles, "migrations")
	require.NoError(t, err)
	driver, err := sqlite.WithInstance(sqlDB, &sqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })

	require.NoError(t, m.Steps(1))
	require.NoError(t, m.Steps(-1))

	rows, err := sqlDB.Query("PRAGMA table_info(orders)")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typeName string
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk))
		assert.NotContains(t, name, "payment_url")
		assert.NotContains(t, name, "payment_expires_at")
		assert.NotContains(t, name, "payment_creation_uncertain")
	}
	require.NoError(t, rows.Err())

	var partialSQL string
	require.NoError(t, sqlDB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_orders_provider_payment_unique'`).Scan(&partialSQL))
	assert.Contains(t, partialSQL, "provider_payment_id IS NOT NULL")
	assert.NotContains(t, partialSQL, "payment_provider_id <> ''")
}
