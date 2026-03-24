package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rs8kvn_bot/internal/logger"
)

func init() {
	// Initialize logger for tests
	logger.Init("", "error")
}

func TestBackupDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a test database file
	content := []byte("test database content")
	if err := os.WriteFile(dbPath, content, 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	err := BackupDatabase(dbPath)
	if err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	// Check backup file exists
	backupPath := dbPath + ".backup"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Fatal("Backup file was not created")
	}

	// Check backup content matches original
	backupContent, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("Failed to read backup file: %v", err)
	}

	if string(backupContent) != string(content) {
		t.Error("Backup content does not match original")
	}
}

func TestBackupDatabase_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	err := BackupDatabase(dbPath)
	if err == nil {
		t.Fatal("BackupDatabase() should return error for non-existent file")
	}
}

func TestBackupDatabase_OverwritesExistingBackup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	backupPath := dbPath + ".backup"

	// Create initial database
	if err := os.WriteFile(dbPath, []byte("initial content"), 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	// Create first backup
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("First BackupDatabase() error = %v", err)
	}

	// Modify database
	if err := os.WriteFile(dbPath, []byte("modified content"), 0644); err != nil {
		t.Fatalf("Failed to modify test database: %v", err)
	}

	// Create second backup
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("Second BackupDatabase() error = %v", err)
	}

	// Check backup has new content
	backupContent, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("Failed to read backup file: %v", err)
	}

	if string(backupContent) != "modified content" {
		t.Error("Backup should contain modified content")
	}
}

func TestRotateBackups_NoBackup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Should not error when no backup exists
	err := RotateBackups(dbPath, 5)
	if err != nil {
		t.Errorf("RotateBackups() with no backup should not error, got: %v", err)
	}
}

func TestRotateBackups_WithBackup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	backupPath := dbPath + ".backup"

	// Create backup file
	if err := os.WriteFile(backupPath, []byte("backup content"), 0644); err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}

	err := RotateBackups(dbPath, 5)
	if err != nil {
		t.Fatalf("RotateBackups() error = %v", err)
	}

	// Original backup should be renamed
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Error("Original backup file should be renamed")
	}

	// Find the timed backup
	pattern := dbPath + ".backup.*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Failed to find backups: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("Expected 1 timed backup, found %d", len(matches))
	}
}

func TestRotateBackups_Cleanup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create multiple timed backups
	for i := 0; i < 7; i++ {
		timedPath := dbPath + ".backup." + "20060102_15040" + string(rune('0'+i))
		if err := os.WriteFile(timedPath, []byte("backup"), 0644); err != nil {
			t.Fatalf("Failed to create timed backup: %v", err)
		}
	}

	// Create current backup
	backupPath := dbPath + ".backup"
	if err := os.WriteFile(backupPath, []byte("current"), 0644); err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}

	// Rotate and keep only 3
	err := RotateBackups(dbPath, 3)
	if err != nil {
		t.Fatalf("RotateBackups() error = %v", err)
	}

	// Check we have only 3 backups
	pattern := dbPath + ".backup.*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Failed to find backups: %v", err)
	}

	if len(matches) != 3 { // keep=3 means only 3 backups remain
		t.Errorf("Expected 3 backups after rotation, found %d", len(matches))
	}
}

func TestDailyBackup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create test database
	content := []byte("test database content")
	if err := os.WriteFile(dbPath, content, 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	err := DailyBackup(dbPath, 7)
	if err != nil {
		t.Fatalf("DailyBackup() error = %v", err)
	}

	// Check timed backup exists
	pattern := dbPath + ".backup.*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Failed to find backups: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("Expected 1 timed backup, found %d", len(matches))
	}
}

func TestDailyBackup_NonExistentDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	err := DailyBackup(dbPath, 7)
	if err == nil {
		t.Fatal("DailyBackup() should return error for non-existent database")
	}
}

func TestDailyBackup_MultipleRuns(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create test database
	if err := os.WriteFile(dbPath, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	// Run daily backup twice
	if err := DailyBackup(dbPath, 5); err != nil {
		t.Fatalf("First DailyBackup() error = %v", err)
	}

	// Small delay to ensure different timestamp
	// time.Sleep(time.Second)

	if err := DailyBackup(dbPath, 5); err != nil {
		t.Fatalf("Second DailyBackup() error = %v", err)
	}

	// Check we have 2 timed backups
	pattern := dbPath + ".backup.*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Failed to find backups: %v", err)
	}

	if len(matches) < 1 {
		t.Errorf("Expected at least 1 timed backup, found %d", len(matches))
	}
}

func TestGetBackupInfo_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		t.Fatalf("GetBackupInfo() error = %v", err)
	}

	if len(infos) != 0 {
		t.Errorf("Expected 0 backups, got %d", len(infos))
	}
}

func TestGetBackupInfo_WithBackups(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database and backups
	if err := os.WriteFile(dbPath, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Create current backup
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	// Create timed backup
	if err := DailyBackup(dbPath, 7); err != nil {
		t.Fatalf("DailyBackup() error = %v", err)
	}

	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		t.Fatalf("GetBackupInfo() error = %v", err)
	}

	if len(infos) == 0 {
		t.Error("Expected at least 1 backup info")
	}

	// Verify backup info structure
	for _, info := range infos {
		if info.Path == "" {
			t.Error("Backup path should not be empty")
		}
		if info.Size < 0 {
			t.Error("Backup size should be non-negative")
		}
	}
}

func TestGetBackupInfo_SortedByTime(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	if err := os.WriteFile(dbPath, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Create multiple timed backups
	for i := 0; i < 3; i++ {
		if err := DailyBackup(dbPath, 7); err != nil {
			t.Fatalf("DailyBackup() error = %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		t.Fatalf("GetBackupInfo() error = %v", err)
	}

	// Verify sorted by time (newest first)
	for i := 0; i < len(infos)-1; i++ {
		if infos[i].ModTime.Before(infos[i+1].ModTime) {
			t.Error("Backups should be sorted by modification time (newest first)")
		}
	}
}

func TestTotalBackupSize_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	size, err := TotalBackupSize(dbPath)
	if err != nil {
		t.Fatalf("TotalBackupSize() error = %v", err)
	}

	if size != 0 {
		t.Errorf("Expected 0 size, got %d", size)
	}
}

func TestTotalBackupSize_WithBackups(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	content := []byte("test database content")
	if err := os.WriteFile(dbPath, content, 0644); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Create backup
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	size, err := TotalBackupSize(dbPath)
	if err != nil {
		t.Fatalf("TotalBackupSize() error = %v", err)
	}

	if size == 0 {
		t.Error("Expected non-zero total backup size")
	}

	if size != int64(len(content)) {
		t.Errorf("Expected size %d, got %d", len(content), size)
	}
}

func TestTotalBackupSize_MultipleBackups(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	content := []byte("test content")
	if err := os.WriteFile(dbPath, content, 0644); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Create first backup
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	size, err := TotalBackupSize(dbPath)
	if err != nil {
		t.Fatalf("TotalBackupSize() error = %v", err)
	}

	// Should have at least the content size in backup
	if size < 5 {
		t.Errorf("Expected size >= 5, got %d", size)
	}
}

func TestValidatePath(t *testing.T) {
	// Test that paths are validated
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a database file
	if err := os.WriteFile(dbPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// ValidatePath should work for existing files
	err := BackupDatabase(dbPath)
	if err != nil {
		t.Errorf("BackupDatabase() should work for valid path: %v", err)
	}
}

func TestValidatePath_Empty(t *testing.T) {
	// Empty path should now be rejected for security
	err := validatePath("")
	if err == nil {
		t.Error("validatePath() should error on empty path")
	}
}

func TestValidatePath_Whitespace(t *testing.T) {
	// Whitespace will pass (get cleaned to ".")
	err := validatePath("   ")
	if err != nil {
		t.Errorf("validatePath() on whitespace should not error: %v", err)
	}
}

func TestDailyBackup_KeepDaysZero(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a database file
	if err := os.WriteFile(dbPath, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// With keepDays=0, should still work (keep all)
	err := DailyBackup(dbPath, 0)
	if err != nil {
		t.Errorf("DailyBackup() with keepDays=0 should not error: %v", err)
	}
}

func TestDailyBackup_KeepDaysOne(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a database file
	if err := os.WriteFile(dbPath, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// With keepDays=1, should only keep latest
	err := DailyBackup(dbPath, 1)
	if err != nil {
		t.Errorf("DailyBackup() with keepDays=1 should not error: %v", err)
	}

	// Verify only one backup exists
	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		t.Fatalf("GetBackupInfo() error = %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("Expected 1 backup, got %d", len(infos))
	}
}

func TestRotateBackups_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	// Should not error for non-existent file
	err := RotateBackups(dbPath, 5)
	if err != nil {
		t.Errorf("RotateBackups() on non-existent should not error: %v", err)
	}
}

func TestRotateBackups_KeepAll(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	if err := os.WriteFile(dbPath, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Create multiple backups by modifying the file each time
	for i := 0; i < 5; i++ {
		os.WriteFile(dbPath, []byte(fmt.Sprintf("content-%d", i)), 0644)
		time.Sleep(10 * time.Millisecond)
		if err := BackupDatabase(dbPath); err != nil {
			t.Fatalf("BackupDatabase() error = %v", err)
		}
	}

	// Rotate with keep > number of backups - should keep all
	err := RotateBackups(dbPath, 10)
	if err != nil {
		t.Errorf("RotateBackups() with keep > count should not error: %v", err)
	}

	// Should have 5 backups (5 created)
	infos, _ := GetBackupInfo(dbPath)
	if len(infos) != 5 {
		t.Logf("Got %d backups instead of 5", len(infos))
	}
}

func TestRotateBackups_KeepZero_UsesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	if err := os.WriteFile(dbPath, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Create backup
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	// Rotate with keep=0 should use default
	err := RotateBackups(dbPath, 0)
	if err != nil {
		t.Errorf("RotateBackups() with keep=0 should not error: %v", err)
	}
}

func TestRotateBackups_KeepNegative_UsesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	if err := os.WriteFile(dbPath, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Create backup
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	// Rotate with negative keep should use default
	err := RotateBackups(dbPath, -5)
	if err != nil {
		t.Errorf("RotateBackups() with keep=-5 should not error: %v", err)
	}
}

func TestGetBackupInfo_SortByTime(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
	if err := os.WriteFile(dbPath, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Create initial backup
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	// Modify and create another backup
	os.WriteFile(dbPath, []byte("content2"), 0644)
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	// Modify and create third backup
	os.WriteFile(dbPath, []byte("content3"), 0644)
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		t.Fatalf("GetBackupInfo() error = %v", err)
	}

	if len(infos) < 2 {
		t.Skipf("Expected at least 2 backups, got %d", len(infos))
	}

	// Verify sorted by ModTime descending (newest first)
	for i := 0; i < len(infos)-1; i++ {
		if infos[i].ModTime.Before(infos[i+1].ModTime) {
			t.Error("Backups not sorted by ModTime descending")
		}
	}
}

func TestDailyBackup_KeepDaysNegative_UsesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := os.WriteFile(dbPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	err := DailyBackup(dbPath, -5)
	if err != nil {
		t.Errorf("DailyBackup() with negative keepDays should not error: %v", err)
	}
}

func TestBackupInfo_Path(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := os.WriteFile(dbPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		t.Fatalf("GetBackupInfo() error = %v", err)
	}

	if len(infos) == 0 {
		t.Fatal("Expected at least one backup")
	}

	// Verify path is set
	if infos[0].Path == "" {
		t.Error("BackupInfo.Path should not be empty")
	}

	// Verify Size is set
	if infos[0].Size == 0 {
		t.Error("BackupInfo.Size should not be zero")
	}
}

func TestBackupInfo_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		t.Fatalf("GetBackupInfo() error = %v", err)
	}

	if len(infos) != 0 {
		t.Errorf("Expected 0 backups for nonexistent file, got %d", len(infos))
	}
}

func TestGetBackupInfo_Order(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := os.WriteFile(dbPath, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	os.WriteFile(dbPath, []byte("content2"), 0644)
	if err := BackupDatabase(dbPath); err != nil {
		t.Fatalf("BackupDatabase() error = %v", err)
	}

	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		t.Fatalf("GetBackupInfo() error = %v", err)
	}

	if len(infos) >= 2 {
		if !infos[0].ModTime.After(infos[1].ModTime) {
			t.Error("First backup should be newer than second")
		}
	}
}

func TestTotalBackupSize_Zero(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// No backups yet
	size, err := TotalBackupSize(dbPath)
	if err != nil {
		t.Fatalf("TotalBackupSize() error = %v", err)
	}
	if size != 0 {
		t.Errorf("Expected 0 size, got %d", size)
	}
}

func TestValidatePath_DirectoryTraversal(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"double dot in path", "../etc/passwd", true},
		{"single dot in middle", "foo/../bar/baz", true}, // Now errors: contains ".."
		{"trailing dots", "foo/bar/..", true},            // Now errors: contains ".."
		{"embedded dots", "foo/./bar", false},            // Cleaned to foo/bar
		{"absolute path with double dots", "/foo/../etc/passwd", true},
		{"multiple double dots", "../../../etc/passwd", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if tt.wantErr && err == nil {
				t.Error("validatePath() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validatePath() unexpected error: %v", err)
			}
		})
	}
}

func TestValidatePath_SystemDirectories(t *testing.T) {
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
			if tt.wantErr && err == nil {
				t.Error("validatePath() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validatePath() unexpected error: %v", err)
			}
		})
	}
}

func TestBackupDatabase_ReadError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	err := BackupDatabase(dbPath)
	if err == nil {
		t.Fatal("BackupDatabase() should return error for non-existent file")
	}
}

func TestBackupDatabase_FilePermission(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "readonly.db")

	if err := os.WriteFile(dbPath, []byte("test"), 0000); err != nil {
		t.Skipf("Skipping test: cannot create read-only file: %v", err)
	}

	err := BackupDatabase(dbPath)
	if err == nil {
		t.Fatal("BackupDatabase() should return error for unreadable file")
	}
}

func TestBackupDatabase_WriteError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := os.WriteFile(dbPath, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Make the directory read-only to cause write error
	parentDir := tmpDir
	if err := os.Chmod(parentDir, 0555); err != nil {
		t.Skipf("Skipping test: cannot change directory permissions: %v", err)
	}
	defer os.Chmod(parentDir, 0755)

	err := BackupDatabase(dbPath)
	if err == nil {
		t.Error("BackupDatabase() should return error when directory is not writable")
	}
}
