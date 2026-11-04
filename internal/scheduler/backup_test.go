package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

func TestBackupScheduler_Start_ContextCancel(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	f, err := os.Create(dbPath)
	require.NoError(t, err)

	err = f.Close()
	if err != nil {
		t.Logf("Warning: failed to close file: %v", err)
	}

	scheduler := NewBackupScheduler(dbPath, 3, 7)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		scheduler.Start(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Scheduler should stop after context cancel")
	}
}

// TestBackupScheduler_MaintenanceHook verifies the daily maintenance hook runs
// AFTER the backup completes (not before), per the SetMaintenance contract.
func TestBackupScheduler_MaintenanceHook(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	f, err := os.Create(dbPath)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	maintenanceCalled := false
	scheduler := NewBackupScheduler(dbPath, 3, 7)
	scheduler.SetMaintenance(func(context.Context) error {
		maintenanceCalled = true

		// The hook runs after the backup: the fresh backup artifact must
		// already exist on disk while the hook is executing.
		backups, globErr := filepath.Glob(dbPath + ".backup*")
		assert.NoError(t, globErr)
		assert.NotEmpty(t, backups, "backup artifact must exist while the maintenance hook runs")

		return nil
	})

	scheduler.runOnce(context.Background())

	assert.True(t, maintenanceCalled, "maintenance hook must be invoked after the backup")
}

// TestBackupScheduler_RunOnce_BackupSuccessMaintenanceError покрывает обе
// ветки runOnce, не покрытые на пустом файле в MaintenanceHook: успешный
// бэкап (валидная SQLite-база) и ошибку хука обслуживания — она логируется
// и глотается по контракту, runOnce не паникует.
func TestBackupScheduler_RunOnce_BackupSuccessMaintenanceError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = db.Exec("CREATE TABLE subscriptions (id INTEGER PRIMARY KEY, status TEXT)")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	maintenanceCalled := false
	scheduler := NewBackupScheduler(dbPath, 3, 7)
	scheduler.SetMaintenance(func(context.Context) error {
		maintenanceCalled = true

		return errors.New("audit retention vacuum failed")
	})

	require.NotPanics(t, func() { scheduler.runOnce(context.Background()) })

	assert.True(t, maintenanceCalled, "maintenance hook must run and its error be swallowed")
	// DailyBackup ротирует артефакт: .backup переименовывается в .backup.<timestamp>.
	artifacts, globErr := filepath.Glob(dbPath + ".backup*")
	assert.NoError(t, globErr)
	assert.NotEmpty(t, artifacts, "valid database must produce a backup artifact")
	assert.NoFileExists(t, dbPath+".backup.corrupt", "valid database must not be marked corrupt")
}
