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
		InboundID:      1,
		TrafficLimit:   10737418240,
		Status:         "active",
	}
	err = db1.CreateSubscription(ctx, sub)
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
		InboundID:      1,
		TrafficLimit:   10737418240,
		Status:         "active",
	}
	err = db1.CreateSubscription(ctx, sub)
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
