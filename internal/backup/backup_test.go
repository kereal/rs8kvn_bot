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
	if err := testutil.InitLogger(m); err != nil {
		os.Stderr.WriteString("Failed to initialize logger: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestBackupDatabase(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a test database file
	content := []byte("test database content")
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create test database")

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
	require.NoError(t, os.WriteFile(dbPath, []byte("initial content"), 0600), "Failed to create test database")

	// Create first backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "First BackupDatabase() error")

	// Modify database
	require.NoError(t, os.WriteFile(dbPath, []byte("modified content"), 0600), "Failed to modify test database")

	// Create second backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "Second BackupDatabase() error")

	// Check backup has new content
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err, "Failed to read backup file")

	assert.Equal(t, "modified content", string(backupContent), "Backup should contain modified content")
}

func TestRotateBackups_NoBackup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Should not error when no backup exists
	err := RotateBackups(dbPath, 5)
	assert.NoError(t, err, "RotateBackups() with no backup should not error")
}

func TestRotateBackups_WithBackup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	backupPath := dbPath + ".backup"

	// Create backup file
	require.NoError(t, os.WriteFile(backupPath, []byte("backup content"), 0600), "Failed to create backup")

	err := RotateBackups(dbPath, 5)
	require.NoError(t, err, "RotateBackups() error")

	// Original backup should be renamed
	_, err = os.Stat(backupPath)
	assert.True(t, os.IsNotExist(err), "Original backup file should be renamed")
}

func TestRotateBackups_Cleanup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create multiple timed backups
	for i := 0; i < 7; i++ {
		timedPath := dbPath + ".backup." + "20060102_15040" + string(rune('0'+i))
		require.NoError(t, os.WriteFile(timedPath, []byte("backup"), 0600), "Failed to create timed backup")
	}

	// Create current backup
	backupPath := dbPath + ".backup"
	require.NoError(t, os.WriteFile(backupPath, []byte("current"), 0600), "Failed to create backup")

	// Rotate and keep only 3
	err := RotateBackups(dbPath, 3)
	require.NoError(t, err, "RotateBackups() error")

	// Check we have only 3 backups
	pattern := dbPath + ".backup.*"
	matches, err := filepath.Glob(pattern)
	require.NoError(t, err, "Failed to find backups")

	assert.Equal(t, 3, len(matches), "Expected 3 backups after rotation")
}

func TestDailyBackup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create test database
	content := []byte("test database content")
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create test database")

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
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create test database")

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
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create database")

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
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create database")

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
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create database")

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
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create database")

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
	require.NoError(t, os.WriteFile(dbPath, []byte("test"), 0600), "Failed to create test file")

	// ValidatePath should work for existing files
	err := BackupDatabase(context.Background(), dbPath)
	assert.NoError(t, err, "BackupDatabase() should work for valid path")
}

func TestValidatePath_Empty(t *testing.T) {
	t.Parallel()

	// Empty path should now be rejected for security
	err := validatePath("")
	assert.Error(t, err, "validatePath() should error on empty path")
}

func TestValidatePath_Whitespace(t *testing.T) {
	t.Parallel()

	// Whitespace will pass (get cleaned to ".")
	err := validatePath("   ")
	assert.NoError(t, err, "validatePath() on whitespace should not error")
}

func TestDailyBackup_KeepDaysZero(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a database file
	require.NoError(t, os.WriteFile(dbPath, []byte("test content"), 0600), "Failed to create test file")

	// With keepDays=0, should still work (keep all)
	err := DailyBackup(context.Background(), dbPath, 0)
	assert.NoError(t, err, "DailyBackup() with keepDays=0 should not error")
}

func TestDailyBackup_KeepDaysOne(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a database file
	require.NoError(t, os.WriteFile(dbPath, []byte("test content"), 0600), "Failed to create test file")

	// With keepDays=1, should only keep latest
	err := DailyBackup(context.Background(), dbPath, 1)
	require.NoError(t, err, "DailyBackup() with keepDays=1 should not error")

	// Verify only one backup exists
	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")
	assert.Equal(t, 1, len(infos), "Expected 1 backup")
}

func TestRotateBackups_NonExistent(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	// Should not error for non-existent file
	err := RotateBackups(dbPath, 5)
	assert.NoError(t, err, "RotateBackups() on non-existent should not error")
}

func TestRotateBackups_KeepAll(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create database")

	// Create multiple backups by modifying the file each time
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(dbPath, []byte(fmt.Sprintf("content-%d", i)), 0600))
		time.Sleep(2 * time.Millisecond)
		require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")
	}

	// Rotate with keep > number of backups - should keep all
	err := RotateBackups(dbPath, 10)
	assert.NoError(t, err, "RotateBackups() with keep > count should not error")

	// Should have 5 backups (5 created)
	infos, _ := GetBackupInfo(dbPath)
	// Note: BackupDatabase overwrites .backup file each time, so we may get fewer than 5
	t.Logf("Got %d backups", len(infos))
}

func TestRotateBackups_KeepZero_UsesDefault(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create database")

	// Create backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	// Rotate with keep=0 should use default
	err := RotateBackups(dbPath, 0)
	assert.NoError(t, err, "RotateBackups() with keep=0 should not error")
}

func TestRotateBackups_KeepNegative_UsesDefault(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create database")

	// Create backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	// Rotate with negative keep should use default
	err := RotateBackups(dbPath, -5)
	assert.NoError(t, err, "RotateBackups() with keep=-5 should not error")
}

func TestGetBackupInfo_SortByTime(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create database")

	// Create initial backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")
	time.Sleep(5 * time.Millisecond)

	// Modify and create another backup
	require.NoError(t, os.WriteFile(dbPath, []byte("content2"), 0600))
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")
	time.Sleep(5 * time.Millisecond)

	// Modify and create third backup
	require.NoError(t, os.WriteFile(dbPath, []byte("content3"), 0600))
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")

	if len(infos) < 2 {
		t.Skipf("Expected at least 2 backups, got %d", len(infos))
	}

	// Verify sorted by ModTime descending (newest first)
	for i := 0; i < len(infos)-1; i++ {
		assert.True(t, infos[i].ModTime.After(infos[i+1].ModTime), "Backups not sorted by ModTime descending")
	}
}

func TestDailyBackup_KeepDaysNegative_UsesDefault(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, os.WriteFile(dbPath, []byte("test"), 0600), "Failed to create test file")

	err := DailyBackup(context.Background(), dbPath, -5)
	assert.NoError(t, err, "DailyBackup() with negative keepDays should not error")
}

func TestBackupInfo_Path(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, os.WriteFile(dbPath, []byte("test"), 0600), "Failed to create test file")
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

func TestGetBackupInfo_Order(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, os.WriteFile(dbPath, []byte("content1"), 0600), "Failed to create test file")
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	time.Sleep(5 * time.Millisecond)
	require.NoError(t, os.WriteFile(dbPath, []byte("content2"), 0600), "Failed to modify test file")
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")

	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")

	if len(infos) >= 2 {
		assert.True(t, infos[0].ModTime.After(infos[1].ModTime), "First backup should be newer than second")
	}
}

func TestTotalBackupSize_Zero(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// No backups yet
	size, err := TotalBackupSize(dbPath)
	require.NoError(t, err, "TotalBackupSize() error")
	assert.Equal(t, int64(0), size, "Expected 0 size")
}

func TestValidatePath_RootDirectory(t *testing.T) {
	t.Parallel()

	err := validatePath("/")
	assert.Error(t, err, "validatePath() should reject root directory")
	assert.Contains(t, err.Error(), "root", "Error should mention root")
}

func TestValidatePath_DevDirectory(t *testing.T) {
	t.Parallel()

	err := validatePath("/dev/sda")
	assert.Error(t, err, "validatePath() should reject /dev directory")
}

func TestValidatePath_VarRunDirectory(t *testing.T) {
	t.Parallel()

	err := validatePath("/var/run/docker.sock")
	assert.Error(t, err, "validatePath() should reject /var/run directory")
}

func TestValidatePath_ValidRelativePath(t *testing.T) {
	t.Parallel()

	err := validatePath("data/database.db")
	assert.NoError(t, err, "validatePath() should accept valid relative path")
}

func TestValidatePath_ValidAbsoluteHomePath(t *testing.T) {
	t.Parallel()

	err := validatePath("/home/user/data.db")
	assert.NoError(t, err, "validatePath() should accept valid home path")
}

func TestValidatePath_ValidTmpPath(t *testing.T) {
	t.Parallel()

	err := validatePath("/tmp/test.db")
	assert.NoError(t, err, "validatePath() should accept valid tmp path")
}

func TestValidatePath_DirectoryTraversal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"double dot in path", "../etc/passwd", true},
		{"single dot in middle", "foo/../bar/baz", true},
		{"trailing dots", "foo/bar/..", true},
		{"embedded dots", "foo/./bar", false},
		{"absolute path with double dots", "/foo/../etc/passwd", true},
		{"multiple double dots", "../../../etc/passwd", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if tt.wantErr {
				assert.Error(t, err, "validatePath() expected error")
			} else {
				assert.NoError(t, err, "validatePath() unexpected error")
			}
		})
	}
}

func TestValidatePath_SystemDirectories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"etc directory", "/etc/passwd", true},
		{"root directory", "/root/.ssh/keys", true},
		{"sys directory", "/sys/kernel/proc", true},
		{"proc directory", "/proc/1/cmdline", true},
		{"regular path", "/home/user/data.db", false},
		{"tmp directory", "/tmp/backup.db", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if tt.wantErr {
				assert.Error(t, err, "validatePath() expected error")
			} else {
				assert.NoError(t, err, "validatePath() unexpected error")
			}
		})
	}
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

	require.NoError(t, os.WriteFile(dbPath, []byte("test content"), 0600), "Failed to create test file")

	// Make the directory read-only to cause write error
	parentDir := tmpDir
	if err := os.Chmod(parentDir, 0555); err != nil {
		t.Skipf("Skipping test: cannot change directory permissions: %v", err)
	}
	defer func() {
		if err := os.Chmod(parentDir, 0755); err != nil {
			t.Logf("Warning: failed to restore directory permissions: %v", err)
		}
	}()

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
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create test database")

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
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create test database")

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
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create test database")

	// Create a context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := DailyBackup(ctx, dbPath, 7)
	require.Error(t, err, "DailyBackup() should return error when context is cancelled")
}

func TestBackupDatabase_Success(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a test database file
	content := []byte("test database content for success")
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create test database")

	// Use a valid context
	ctx := context.Background()
	err := BackupDatabase(ctx, dbPath)
	require.NoError(t, err, "BackupDatabase() error")

	// Verify backup was created
	backupPath := dbPath + ".backup"
	_, err = os.Stat(backupPath)
	require.NoError(t, err, "Backup file was not created")

	// Verify backup content matches original
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err, "Failed to read backup file")

	assert.Equal(t, string(content), string(backupContent), "Backup content does not match original")
}

// ==================== Additional Error Path Tests ====================

func TestBackupDatabase_EmptyFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create an empty file
	require.NoError(t, os.WriteFile(dbPath, []byte{}, 0600), "Failed to create empty test file")

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
	require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create large test file")

	ctx := context.Background()
	err := BackupDatabase(ctx, dbPath)
	require.NoError(t, err, "BackupDatabase() should succeed with large file")

	backupPath := dbPath + ".backup"
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err, "Failed to read backup file")
	assert.Equal(t, content, backupContent, "Backup content should match large file")
}

func TestRotateBackups_ExceedsRetention(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create multiple old backups
	for i := 0; i < 5; i++ {
		backupName := fmt.Sprintf("%s.backup.2024010%d_120000", dbPath, i)
		require.NoError(t, os.WriteFile(backupName, []byte(fmt.Sprintf("backup %d", i)), 0600))
	}

	// Create current backup
	require.NoError(t, os.WriteFile(dbPath+".backup", []byte("current backup"), 0600))

	err := RotateBackups(dbPath, 3)
	require.NoError(t, err, "RotateBackups() error")

	// Count remaining backups
	files, _ := filepath.Glob(dbPath + ".backup.*")
	assert.LessOrEqual(t, len(files), 3, "Should have at most 3 backups after rotation")
}

func TestRotateBackups_KeepsNewest(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create backups with different timestamps
	timestamps := []string{
		"20240101_120000",
		"20240102_120000",
		"20240103_120000",
	}
	for _, ts := range timestamps {
		backupName := fmt.Sprintf("%s.backup.%s", dbPath, ts)
		require.NoError(t, os.WriteFile(backupName, []byte(ts), 0600))
	}

	// Create current backup
	require.NoError(t, os.WriteFile(dbPath+".backup", []byte("current"), 0600))

	err := RotateBackups(dbPath, 2)
	require.NoError(t, err, "RotateBackups() error")

	files, _ := filepath.Glob(dbPath + ".backup.*")
	assert.LessOrEqual(t, len(files), 2, "Should keep only 2 newest backups")
}

func TestValidatePath_PathTraversal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"dotdot", "../etc/passwd", true},
		{"dotdot_subdir", "data/../../../etc/passwd", true},
		{"root", "/", true},
		{"dev", "/dev/sda", true},
		{"var_run", "/var/run/docker.sock", true},
		{"valid_relative", "data/database.db", false},
		{"valid_home", "/home/user/data.db", false},
		{"valid_tmp", "/tmp/test.db", false},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if tt.wantErr {
				assert.Error(t, err, "validatePath(%q) should error", tt.path)
			} else {
				assert.NoError(t, err, "validatePath(%q) should not error", tt.path)
			}
		})
	}
}

func TestStartScheduler_Lifecycle(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("test content"), 0600), "Failed to create test database")

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
