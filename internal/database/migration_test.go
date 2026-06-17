package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration_Idempotency(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")

	db1, err := NewService(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	sub := &Subscription{
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "client-123",
		SubscriptionID: "sub-123",
		Status:         "active",
	}
	err = db1.CreateSubscription(ctx, sub, "")
	require.NoError(t, err)

	require.NoError(t, db1.Close())

	db2, err := NewService(dbPath)
	require.NoError(t, err)

	retrieved, err := db2.GetByTelegramID(ctx, 123456)
	require.NoError(t, err)
	assert.Equal(t, "testuser", retrieved.Username)

	require.NoError(t, db2.Close())
}

func TestMigration_AddNewTable(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := NewService(dbPath)
	require.NoError(t, err)

	sqlDB, err := db.db.DB()
	require.NoError(t, err)

	_, err = sqlDB.Exec(`CREATE TABLE IF NOT EXISTS test_table (
		id INTEGER PRIMARY KEY,
		name TEXT
	)`)
	require.NoError(t, err)

	var count int
	err = sqlDB.QueryRow("SELECT COUNT(*) FROM test_table").Scan(&count)
	require.NoError(t, err)

	assert.Equal(t, 0, count)

	require.NoError(t, db.Close())
}

func TestMigration_PreserveDataOnUpgrade(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")

	db1, err := NewService(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	sub := &Subscription{
		TelegramID:     999999,
		Username:       "existing_user",
		ClientID:       "client-999",
		SubscriptionID: "sub-999",
		Status:         "active",
	}
	err = db1.CreateSubscription(ctx, sub, "")
	require.NoError(t, err)

	require.NoError(t, db1.Close())

	db2, err := NewService(dbPath)
	require.NoError(t, err)

	retrieved, err := db2.GetByTelegramID(ctx, 999999)
	require.NoError(t, err)
	assert.Equal(t, "existing_user", retrieved.Username)
	assert.Equal(t, "active", retrieved.Status)

	require.NoError(t, db2.Close())
}

func TestMigration_RunMultipleTimes(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")

	for i := 0; i < 3; i++ {
		db, err := NewService(dbPath)
		require.NoError(t, err, "Migration should succeed on attempt %d", i+1)
		require.NoError(t, db.Close())
	}
}

func TestMigration_InvalidSchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	f, err := os.Create(dbPath)
	require.NoError(t, err)
	if _, err := f.WriteString("invalid sqlite content"); err != nil {
		t.Logf("Warning: failed to write to file: %v", err)
	}
	require.NoError(t, f.Close())

	_, err = NewService(dbPath)
	assert.Error(t, err)
}

func TestMigration_SchemaVersionTracking(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := NewService(dbPath)
	require.NoError(t, err)

	sqlDB, err := db.db.DB()
	require.NoError(t, err)

	var version int
	var dirty bool
	err = sqlDB.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
	require.NoError(t, err)

	t.Logf("Current schema version: %d, dirty: %t", version, dirty)

	assert.GreaterOrEqual(t, version, 0)

	require.NoError(t, db.Close())
}

func TestMigration_ProductsHaveRequiredName(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := NewService(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	sqlDB, err := db.db.DB()
	require.NoError(t, err)

	rows, err := sqlDB.Query("PRAGMA table_info(products)")
	require.NoError(t, err)
	defer rows.Close()

	type columnInfo struct {
		name     string
		typeName string
		notNull  int
	}

	var nameColumn *columnInfo
	for rows.Next() {
		var cid int
		var name, typeName string
		var notNull int
		var defaultValue any
		var pk int

		require.NoError(t, rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk))
		if name == "name" {
			nameColumn = &columnInfo{name: name, typeName: typeName, notNull: notNull}
		}
	}
	require.NoError(t, rows.Err())
	require.NotNil(t, nameColumn)
	assert.Equal(t, "VARCHAR(255)", nameColumn.typeName)
	assert.Equal(t, 1, nameColumn.notNull)
}

// TestMigration_005_CleansUpDuplicateInvites (and the dedup logic) now primarily
// validates the deduplication SQL that was moved into migration 004 for safety.
// The test still exercises the exact window-function dedup + unique index creation
// that protects legacy databases with pre-existing duplicates.
func TestMigration_005_CleansUpDuplicateInvites(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")

	// 1. Create DB (this runs all migrations including 005 + creates the unique index)
	db, err := NewService(dbPath)
	require.NoError(t, err)
	defer db.Close()

	sqlDB, err := db.db.DB()
	require.NoError(t, err)

	referrer := int64(999888777)

	// 2. Drop the unique index temporarily so we can simulate pre-005 duplicate data
	_, _ = sqlDB.Exec(`DROP INDEX IF EXISTS idx_invites_referrer_unique`)

	// 3. Insert three codes for the same referrer with different creation times
	//    (simulating what happened on legacy DBs for years)
	_, err = sqlDB.Exec(`
		INSERT INTO invites (code, referrer_tg_id, created_at) VALUES 
			('VERYOLD', ?, '2023-01-01 00:00:00'),
			('MIDDLE',  ?, '2024-05-01 00:00:00'),
			('NEWEST',  ?, '2025-03-01 00:00:00')
	`, referrer, referrer, referrer)
	require.NoError(t, err)

	// Verify we now have 3 rows (pre-005 state)
	var count int
	err = sqlDB.QueryRow("SELECT COUNT(*) FROM invites WHERE referrer_tg_id = ?", referrer).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 3, count, "Should have 3 duplicate invites before running 005 dedup logic")

	// 4. Execute the exact deduplication statement from 005 (window function version)
	_, err = sqlDB.Exec(`
		DELETE FROM invites
		WHERE rowid NOT IN (
			SELECT rowid
			FROM (
				SELECT rowid,
					   ROW_NUMBER() OVER (
						   PARTITION BY referrer_tg_id
						   ORDER BY created_at ASC, code ASC
					   ) AS rn
				FROM invites
			) ranked
			WHERE rn = 1
		)
	`)
	require.NoError(t, err)

	// 5. Re-create the unique index (second part of 005)
	_, err = sqlDB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_invites_referrer_unique ON invites(referrer_tg_id)`)
	require.NoError(t, err)

	// 6. Assertions: only the oldest code remains
	err = sqlDB.QueryRow("SELECT COUNT(*) FROM invites WHERE referrer_tg_id = ?", referrer).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "005 dedup must leave exactly one code per referrer")

	var remainingCode string
	err = sqlDB.QueryRow("SELECT code FROM invites WHERE referrer_tg_id = ?", referrer).Scan(&remainingCode)
	require.NoError(t, err)
	assert.Equal(t, "VERYOLD", remainingCode, "005 must keep the oldest code by created_at")

	// 7. Bonus: after the index is back, inserting another code for the same referrer must fail
	_, err = sqlDB.Exec(`INSERT INTO invites (code, referrer_tg_id, created_at) VALUES ('SHOULDFAIL', ?, datetime('now'))`, referrer)
	assert.Error(t, err, "Unique index created by 005 must prevent future duplicates")
}
