package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"tgvpn_go/internal/logger"
)

// BackupDatabase creates a backup of the database file
func BackupDatabase(dbPath string) error {
	backupPath := dbPath + ".backup"
	tempPath := backupPath + ".tmp"

	// Open source file
	src, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer src.Close()

	// Create temp file
	dst, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create temp backup: %w", err)
	}
	defer dst.Close()

	// Copy contents
	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to copy database: %w", err)
	}

	// Sync to disk
	if err := dst.Sync(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to sync backup: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, backupPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename backup: %w", err)
	}

	logger.Infof("Database backup created: %s", backupPath)
	return nil
}

// RotateBackups keeps only the specified number of backup files
func RotateBackups(dbPath string, keep int) error {
	basePath := dbPath + ".backup"

	// Check if backup exists
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return nil // No backups to rotate
	}

	// Create timestamped backup
	timestamp := time.Now().Format("20060102_150405")
	timedBackupPath := fmt.Sprintf("%s.%s", basePath, timestamp)

	if err := os.Rename(basePath, timedBackupPath); err != nil {
		return fmt.Errorf("failed to rename backup: %w", err)
	}

	logger.Infof("Rotated backup to: %s", timedBackupPath)

	// Find and remove old backups
	pattern := basePath + ".*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to find backups: %w", err)
	}

	// Sort by modification time (newest first) using efficient sort
	type backupInfo struct {
		path    string
		modTime time.Time
	}

	backups := make([]backupInfo, 0, len(matches))
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil {
			backups = append(backups, backupInfo{path: match, modTime: info.ModTime()})
		}
	}

	// Efficient O(n log n) sort
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.After(backups[j].modTime)
	})

	// Remove old backups beyond keep limit
	removed := 0
	for i := keep; i < len(backups); i++ {
		if err := os.Remove(backups[i].path); err == nil {
			logger.Infof("Removed old backup: %s", backups[i].path)
			removed++
		}
	}

	if removed > 0 {
		logger.Infof("Cleaned up %d old backup(s)", removed)
	}

	return nil
}

// DailyBackup creates a backup and rotates old ones
func DailyBackup(dbPath string, keepDays int) error {
	if err := BackupDatabase(dbPath); err != nil {
		return err
	}
	return RotateBackups(dbPath, keepDays)
}
