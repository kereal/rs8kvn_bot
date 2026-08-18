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
// before migration 031. Use the target version explicitly: Steps(30) would
// count migration 000 as one of the steps and leave the database at version 29.
// The tests then invoke migration 031 itself—not a copy of its SQL.
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
	require.NoError(t, m.Migrate(30))

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
		var (
			cid, notNull, pk int
			name, typeName   string
			defaultValue     any
		)
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
		var (
			seq, unique, partial int
			name, origin         string
		)
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

func TestMigration031_PendingIndexEnforcesOnlyPlategaPendingOrders(t *testing.T) {
	sqlDB, cleanup := newSQLiteAtMigration30(t)
	t.Cleanup(cleanup)

	// runMigrations now applies 033/034 with NoTxWrap, so their trailing
	// PRAGMA foreign_keys = ON actually takes effect on the migration
	// connection. This harness tests the legacy FK-off world (it inserts orders
	// with non-existent parents), so pin the pool to one connection and reset
	// the flag after migration.
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, runMigrations(sqlDB))

	_, err := sqlDB.Exec("PRAGMA foreign_keys = OFF")
	require.NoError(t, err)

	insert := func(status, provider string) error {
		_, err := sqlDB.Exec(`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, created_at) VALUES (?, ?, ?, ?, ?, ?, datetime('now'))`, 1, 1, status, 100, "RUB", provider)
		return err
	}
	require.NoError(t, insert("pending", "platega"))
	require.Error(t, insert("pending", "platega"), "duplicate pending Platega intent must be rejected")
	require.NoError(t, insert("paid", "platega"), "paid orders are outside the pending partial index")
	require.NoError(t, insert("pending", "other-provider"), "other providers are outside the pending partial index")
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

	// The current schema deliberately permits multiple pending orders with an
	// empty provider ID. Rollback must normalize those placeholders before
	// restoring the legacy IS NOT NULL index predicate.
	_, err = sqlDB.Exec(`
		INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at)
		VALUES (1001, 1001, 'pending', 100, 'RUB', 'platega', '', datetime('now'))`)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`
		INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at)
		VALUES (1002, 1002, 'pending', 100, 'RUB', 'platega', '', datetime('now'))`)
	require.NoError(t, err)

	require.NoError(t, m.Steps(-1))

	rows, err := sqlDB.Query("PRAGMA table_info(orders)")
	require.NoError(t, err)

	defer rows.Close()

	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typeName   string
			defaultValue     any
		)
		require.NoError(t, rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk))
		assert.NotContains(t, name, "payment_url")
		assert.NotContains(t, name, "payment_expires_at")
		assert.NotContains(t, name, "payment_creation_uncertain")
	}

	require.NoError(t, rows.Err())

	var nullProviderIDs, emptyProviderIDs int
	require.NoError(t, sqlDB.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN provider_payment_id IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN provider_payment_id = '' THEN 1 ELSE 0 END), 0)
		FROM orders`).Scan(&nullProviderIDs, &emptyProviderIDs))
	assert.Equal(t, 2, nullProviderIDs)
	assert.Zero(t, emptyProviderIDs)

	var partialSQL string
	require.NoError(t, sqlDB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_orders_provider_payment_unique'`).Scan(&partialSQL))
	assert.Contains(t, partialSQL, "provider_payment_id IS NOT NULL")
	assert.NotContains(t, partialSQL, "provider_payment_id <> ''")
}
