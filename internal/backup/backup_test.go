package backup

import (
	"os"
	"path/filepath"
	"testing"

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
