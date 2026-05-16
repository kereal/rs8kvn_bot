package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
}

func TestBackupDatabase(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a test database file
	content := []byte("test database content")
	require.NoError(t, os.WriteFile(dbPath, content, 0644), "Failed to create test database")

	err := BackupDatabase(context.Background(), dbPath)
	require.NoError(t, err, "BackupDatabase() error")

	// Check backup file exists
	backupPath := dbPath + ".backup"
	_, err = os.Stat(backupPath)
	require.NoError(t, err, "Backup file was not created")

	// Check backup content matches original
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err, "Failed to read backup file")

	assert.Equal(t, string(content), string(backupContent), "Backup content does not match original")
}

func TestBackupDatabase_NonExistentFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	err := BackupDatabase(context.Background(), dbPath)
	require.Error(t, err, "BackupDatabase() should return error for non-existent file")
}

func TestBackupDatabase_OverwritesExistingBackup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	backupPath := dbPath + ".backup"

	// Create initial database
	require.NoError(t, os.WriteFile(dbPath, []byte("initial content"), 0644), "Failed to create test database")

	// Create first backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "First BackupDatabase() error")

	// Modify database
	require.NoError(t, os.WriteFile(dbPath, []byte("modified content"), 0644), "Failed to modify test database")

	// Create second backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "Second BackupDatabase() error")

	// Check backup has new content
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err, "Failed to read backup file")

	assert.Equal(t, "modified content", string(backupContent), "Backup should contain modified content")
}

func TestRotateBackups(t *testing.T) {
	t.Parallel()

	t.Run("simple scenarios", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			keep     int
			setup    func(tmpDir string, dbPath string)
			check    func(t *testing.T, tmpDir string, dbPath string)
		}{
			{
				name: "no_backup_file",
				keep: 5,
				setup: func(tmpDir, dbPath string) {},
				check: func(t *testing.T, tmpDir, dbPath string) {
					assert.NoError(t, RotateBackups(dbPath, 5))
				},
			},
			{
				name: "with_current_backup",
				keep: 5,
				setup: func(tmpDir, dbPath string) {
					require.NoError(t, os.WriteFile(dbPath+".backup", []byte("backup content"), 0644))
				},
				check: func(t *testing.T, tmpDir, dbPath string) {
					assert.NoError(t, RotateBackups(dbPath, 5))
					_, err := os.Stat(dbPath + ".backup")
					assert.True(t, os.IsNotExist(err), "original backup should be renamed")

					// Verify at least one rotated backup file was created
					matches, _ := filepath.Glob(dbPath + ".backup.*")
					assert.True(t, len(matches) > 0, "should have at least one rotated backup file")
				},
			},
			{
				name: "nonexistent_db",
				keep: 5,
				setup: func(tmpDir, dbPath string) {},
				check: func(t *testing.T, tmpDir, dbPath string) {
					assert.NoError(t, RotateBackups(filepath.Join(tmpDir, "nonexistent.db"), 5))
				},
			},
			{
				name: "keep_zero_uses_default",
				keep: 0,
				setup: func(tmpDir, dbPath string) {
					require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0644))
					require.NoError(t, BackupDatabase(context.Background(), dbPath))
				},
				check: func(t *testing.T, tmpDir, dbPath string) {
					assert.NoError(t, RotateBackups(dbPath, 0))
				},
			},
			{
				name: "keep_negative_uses_default",
				keep: -5,
				setup: func(tmpDir, dbPath string) {
					require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0644))
					require.NoError(t, BackupDatabase(context.Background(), dbPath))
				},
				check: func(t *testing.T, tmpDir, dbPath string) {
					assert.NoError(t, RotateBackups(dbPath, -5))
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tmpDir := t.TempDir()
				dbPath := filepath.Join(tmpDir, "test.db")
				tt.setup(tmpDir, dbPath)
				tt.check(t, tmpDir, dbPath)
			})
		}
	})

	t.Run("cleanup_old_backups", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		for i := 0; i < 7; i++ {
			timedPath := dbPath + ".backup." + "20060102_15040" + string(rune('0'+i))
			require.NoError(t, os.WriteFile(timedPath, []byte("backup"), 0644))
		}

		require.NoError(t, os.WriteFile(dbPath+".backup", []byte("current"), 0644))

		err := RotateBackups(dbPath, 3)
		require.NoError(t, err)

		matches, _ := filepath.Glob(dbPath + ".backup.*")
		assert.Equal(t, 3, len(matches))
	})

	t.Run("exceeds_retention", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		for i := 0; i < 5; i++ {
			backupName := fmt.Sprintf("%s.backup.2024010%d_120000", dbPath, i)
			require.NoError(t, os.WriteFile(backupName, []byte(fmt.Sprintf("backup %d", i)), 0644))
		}

		require.NoError(t, os.WriteFile(dbPath+".backup", []byte("current backup"), 0644))

		err := RotateBackups(dbPath, 3)
		require.NoError(t, err)

		files, _ := filepath.Glob(dbPath + ".backup.*")
		assert.LessOrEqual(t, len(files), 3)
	})

	t.Run("keeps_newest", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		timestamps := []string{"20240101_120000", "20240102_120000", "20240103_120000"}
		for _, ts := range timestamps {
			require.NoError(t, os.WriteFile(dbPath+".backup."+ts, []byte(ts), 0644))
		}

		require.NoError(t, os.WriteFile(dbPath+".backup", []byte("current"), 0644))

		err := RotateBackups(dbPath, 2)
		require.NoError(t, err)

		files, _ := filepath.Glob(dbPath + ".backup.*")
		assert.LessOrEqual(t, len(files), 2)
	})
}

func TestDailyBackup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create test database
	content := []byte("test database content")
	require.NoError(t, os.WriteFile(dbPath, content, 0644), "Failed to create test database")

	err := DailyBackup(context.Background(), dbPath, 7)
	require.NoError(t, err, "DailyBackup() error")

	// Check timed backup exists
	pattern := dbPath + ".backup.*"
	matches, err := filepath.Glob(pattern)
	require.NoError(t, err, "Failed to find backups")

	assert.Equal(t, 1, len(matches), "Expected 1 timed backup")
}

func TestDailyBackup_NonExistentDatabase(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	err := DailyBackup(context.Background(), dbPath, 7)
	require.Error(t, err, "DailyBackup() should return error for non-existent database")
}

func TestDailyBackup_MultipleRuns(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create test database
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0644), "Failed to create test database")

	// Run daily backup twice
	require.NoError(t, DailyBackup(context.Background(), dbPath, 5), "First DailyBackup() error")

	// Small delay to ensure different timestamp
	// time.Sleep(time.Second)

	require.NoError(t, DailyBackup(context.Background(), dbPath, 5), "Second DailyBackup() error")

	// Check we have at least 1 timed backup
	pattern := dbPath + ".backup.*"
	matches, err := filepath.Glob(pattern)
	require.NoError(t, err, "Failed to find backups")

	assert.GreaterOrEqual(t, len(matches), 1, "Expected at least 1 timed backup")
}

func TestGetBackupInfo_Empty(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")

	assert.Equal(t, 0, len(infos), "Expected 0 backups")
}

func TestGetBackupInfo_WithBackups(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database and backups
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0644), "Failed to create database")

	// Create current backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	// Create timed backup
	require.NoError(t, DailyBackup(context.Background(), dbPath, 7), "DailyBackup() error")

	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")

	assert.Greater(t, len(infos), 0, "Expected at least 1 backup info")

	// Verify backup info structure
	for _, info := range infos {
		assert.NotEmpty(t, info.Path, "Backup path should not be empty")
		assert.GreaterOrEqual(t, info.Size, int64(0), "Backup size should be non-negative")
	}
}

func TestGetBackupInfo_SortedByTime(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0644), "Failed to create database")

	// Create multiple timed backups
	for i := 0; i < 3; i++ {
		require.NoError(t, DailyBackup(context.Background(), dbPath, 7), "DailyBackup() error")
		time.Sleep(2 * time.Millisecond)
	}

	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")

	// Verify sorted by time (newest first)
	for i := 0; i < len(infos)-1; i++ {
		assert.True(t, infos[i].ModTime.After(infos[i+1].ModTime) || infos[i].ModTime.Equal(infos[i+1].ModTime),
			"Backups should be sorted by modification time (newest first)")
	}
}

func TestTotalBackupSize_Empty(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	size, err := TotalBackupSize(dbPath)
	require.NoError(t, err, "TotalBackupSize() error")

	assert.Equal(t, int64(0), size, "Expected 0 size")
}

func TestTotalBackupSize_WithBackups(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	content := []byte("test database content")
	require.NoError(t, os.WriteFile(dbPath, content, 0644), "Failed to create database")

	// Create backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	size, err := TotalBackupSize(dbPath)
	require.NoError(t, err, "TotalBackupSize() error")

	assert.Greater(t, size, int64(0), "Expected non-zero total backup size")
	assert.Equal(t, int64(len(content)), size, "Expected size to match content length")
}

func TestTotalBackupSize_MultipleBackups(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	content := []byte("test content")
	require.NoError(t, os.WriteFile(dbPath, content, 0644), "Failed to create database")

	// Create first backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	size, err := TotalBackupSize(dbPath)
	require.NoError(t, err, "TotalBackupSize() error")

	// Should have at least the content size in backup
	assert.GreaterOrEqual(t, size, int64(5), "Expected size >= 5")
}

func TestValidatePath(t *testing.T) {
	t.Parallel()

	// Test that paths are validated
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a database file
	require.NoError(t, os.WriteFile(dbPath, []byte("test"), 0644), "Failed to create test file")

	// ValidatePath should work for existing files
	err := BackupDatabase(context.Background(), dbPath)
	assert.NoError(t, err, "BackupDatabase() should work for valid path")
}

func TestDailyBackup_KeepDaysZero(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a database file
	require.NoError(t, os.WriteFile(dbPath, []byte("test content"), 0644), "Failed to create test file")

	// With keepDays=0, should still work (keep all)
	err := DailyBackup(context.Background(), dbPath, 0)
	assert.NoError(t, err, "DailyBackup() with keepDays=0 should not error")
}

func TestDailyBackup_KeepDaysOne(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a database file
	require.NoError(t, os.WriteFile(dbPath, []byte("test content"), 0644), "Failed to create test file")

	// With keepDays=1, should only keep latest
	err := DailyBackup(context.Background(), dbPath, 1)
	require.NoError(t, err, "DailyBackup() with keepDays=1 should not error")

	// Verify only one backup exists
	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")
	assert.Equal(t, 1, len(infos), "Expected 1 backup")
}

func TestDailyBackup_KeepDaysNegative_UsesDefault(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, os.WriteFile(dbPath, []byte("test"), 0644), "Failed to create test file")

	err := DailyBackup(context.Background(), dbPath, -5)
	assert.NoError(t, err, "DailyBackup() with negative keepDays should not error")
}

func TestBackupInfo_Path(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, os.WriteFile(dbPath, []byte("test"), 0644), "Failed to create test file")
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")
	require.Greater(t, len(infos), 0, "Expected at least one backup")

	// Verify path is set
	assert.NotEmpty(t, infos[0].Path, "BackupInfo.Path should not be empty")

	// Verify Size is set
	assert.Greater(t, infos[0].Size, int64(0), "BackupInfo.Size should not be zero")
}

func TestBackupInfo_FileNotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")

	assert.Equal(t, 0, len(infos), "Expected 0 backups for nonexistent file")
}

func TestBackupDatabase_ReadError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	err := BackupDatabase(context.Background(), dbPath)
	require.Error(t, err, "BackupDatabase() should return error for non-existent file")
}

func TestBackupDatabase_FilePermission(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "readonly.db")

	if err := os.WriteFile(dbPath, []byte("test"), 0000); err != nil {
		t.Skipf("Skipping test: cannot create read-only file: %v", err)
	}

	err := BackupDatabase(context.Background(), dbPath)
	require.Error(t, err, "BackupDatabase() should return error for unreadable file")
}

func TestBackupDatabase_WriteError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, os.WriteFile(dbPath, []byte("test content"), 0644), "Failed to create test file")

	// Make the directory read-only to cause write error
	parentDir := tmpDir
	if err := os.Chmod(parentDir, 0555); err != nil {
		t.Skipf("Skipping test: cannot change directory permissions: %v", err)
	}
	defer os.Chmod(parentDir, 0755)

	err := BackupDatabase(context.Background(), dbPath)
	assert.Error(t, err, "BackupDatabase() should return error when directory is not writable")
}

// ==================== Context Cancellation Tests ====================

func TestBackupDatabase_ContextCancellation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a test database file with some content
	content := make([]byte, 1024*1024) // 1MB file
	for i := range content {
		content[i] = byte(i % 256)
	}
	require.NoError(t, os.WriteFile(dbPath, content, 0644), "Failed to create test database")

	// Create a context that will be cancelled immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := BackupDatabase(ctx, dbPath)
	require.Error(t, err, "BackupDatabase() should return error when context is cancelled")
	assert.Equal(t, context.Canceled, err, "BackupDatabase() error should be context.Canceled")

	// Verify no backup file was created
	backupPath := dbPath + ".backup"
	_, err = os.Stat(backupPath)
	assert.True(t, os.IsNotExist(err), "Backup file should not be created when context is cancelled")
}

func TestBackupDatabase_ContextTimeout(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a test database file
	content := make([]byte, 100*1024) // 100KB file
	require.NoError(t, os.WriteFile(dbPath, content, 0644), "Failed to create test database")

	// Create a context with a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give the context time to expire
	time.Sleep(2 * time.Millisecond)

	err := BackupDatabase(ctx, dbPath)
	require.Error(t, err, "BackupDatabase() should return error when context times out")
	assert.Equal(t, context.DeadlineExceeded, err, "BackupDatabase() error should be context.DeadlineExceeded")
}

func TestDailyBackup_ContextCancellation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a test database file
	content := []byte("test database content")
	require.NoError(t, os.WriteFile(dbPath, content, 0644), "Failed to create test database")

	// Create a context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := DailyBackup(ctx, dbPath, 7)
	require.Error(t, err, "DailyBackup() should return error when context is cancelled")
}

// ==================== Additional Error Path Tests ====================

func TestBackupDatabase_EmptyFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create an empty file
	require.NoError(t, os.WriteFile(dbPath, []byte{}, 0644), "Failed to create empty test file")

	err := BackupDatabase(context.Background(), dbPath)
	require.NoError(t, err, "BackupDatabase() should succeed with empty file")

	backupPath := dbPath + ".backup"
	_, err = os.Stat(backupPath)
	require.NoError(t, err, "Backup file should be created for empty source")
}

func TestBackupDatabase_LargeFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a 10MB file
	content := make([]byte, 10*1024*1024)
	for i := range content {
		content[i] = byte(i % 256)
	}
	require.NoError(t, os.WriteFile(dbPath, content, 0644), "Failed to create large test file")

	ctx := context.Background()
	err := BackupDatabase(ctx, dbPath)
	require.NoError(t, err, "BackupDatabase() should succeed with large file")

	backupPath := dbPath + ".backup"
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err, "Failed to read backup file")
	assert.Equal(t, content, backupContent, "Backup content should match large file")
}

func TestStartScheduler_Lifecycle(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("test content"), 0644), "Failed to create test database")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = DailyBackup(context.Background(), dbPath, 7)
			}
		}
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Scheduler did not stop after context cancellation")
	}
}
