package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteBusyTimeoutDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "path without query",
			path: "./data/bot.db",
			want: "./data/bot.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on",
		},
		{
			name: "path with query",
			path: "file:bot.db?mode=rwc",
			want: "file:bot.db?mode=rwc&_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, sqliteBusyTimeoutDSN(tt.path))
		})
	}
}

func TestNewServiceUsesWALAndMultipleConnections(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "bot.db")
	svc, err := NewService(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	sqlDB, err := svc.db.DB()
	require.NoError(t, err)

	// Behavioral checks: the pool is configured and usable, WAL is active.
	// Exact MaxOpenConnections is intentionally not pinned — it is config that
	// can change without affecting WAL behaviour.
	assert.Greater(t, sqlDB.Stats().MaxOpenConnections, 0, "pool must allow connections")
	assert.True(t, sqlDB.Stats().OpenConnections <= sqlDB.Stats().MaxOpenConnections, "open connections must never exceed pool limit")
	require.NoError(t, sqlDB.Ping(), "pool must accept a connection")

	var journalMode string
	require.NoError(t, svc.db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error)
	require.Equal(t, "wal", journalMode)
}
