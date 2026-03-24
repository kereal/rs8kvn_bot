package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// validatePath checks if a path is safe (no directory traversal).
func validatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	// Check for directory traversal attempts in the original path
	// This must be done BEFORE cleaning because Clean() resolves ".."
	if strings.Contains(path, "..") {
		return fmt.Errorf("invalid path: directory traversal detected")
	}

	// Get absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Clean the path to resolve any . elements
	cleaned := filepath.Clean(absPath)

	// Prevent access to system directories (case-insensitive for safety)
	lowerPath := strings.ToLower(cleaned)
	if strings.HasPrefix(lowerPath, "/etc/") ||
		strings.HasPrefix(lowerPath, "/root/") ||
		strings.HasPrefix(lowerPath, "/sys/") ||
		strings.HasPrefix(lowerPath, "/proc/") ||
		strings.HasPrefix(lowerPath, "/dev/") ||
		strings.HasPrefix(lowerPath, "/var/run/") {
		return fmt.Errorf("access to system directories is forbidden")
	}

	// Ensure path is within reasonable bounds (not root)
	if cleaned == "/" {
		return fmt.Errorf("cannot use root directory")
	}

	return nil
}

// BackupDatabase creates a backup of the SQLite database file.
// It uses atomic write pattern: write to temp file, sync, then rename.
// This ensures the backup is always in a consistent state.
func BackupDatabase(dbPath string) error {
	// Validate the database path to prevent directory traversal
	if err := validatePath(dbPath); err != nil {
		return fmt.Errorf("invalid database path: %w", err)
	}

	backupPath := dbPath + ".backup"
	tempPath := backupPath + ".tmp"

	// Open source database
	// #nosec G304 -- File path is validated by validatePath() above to prevent directory traversal
	src, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			logger.Debug("Failed to close source", zap.Error(err))
		}
	}()

	// Create temp backup file
	// #nosec G304 -- tempPath is derived from validated dbPath and is safe
	dst, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create temp backup: %w", err)
	}
	defer func() {
		if err := dst.Close(); err != nil {
			logger.Debug("Failed to close destination", zap.Error(err))
		}
	}()

	// Copy database to temp file
	if _, err := io.Copy(dst, src); err != nil {
		_ = os.Remove(tempPath) // Ignore error, we're already returning an error
		return fmt.Errorf("failed to copy database: %w", err)
	}

	// Sync to ensure all data is written to disk
	if err := dst.Sync(); err != nil {
		_ = os.Remove(tempPath) // Ignore error, we're already returning an error
		return fmt.Errorf("failed to sync backup: %w", err)
	}

	// Close files before rename
	if err := dst.Close(); err != nil {
		_ = os.Remove(tempPath) // Ignore error, we're already returning an error
		return fmt.Errorf("failed to close backup file: %w", err)
	}

	if err := src.Close(); err != nil {
		_ = os.Remove(tempPath) // Ignore error, we're already returning an error
		return fmt.Errorf("failed to close database file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, backupPath); err != nil {
		_ = os.Remove(tempPath) // Ignore error, we're already returning an error
		return fmt.Errorf("failed to rename backup: %w", err)
	}

	logger.Info("Database backup created", zap.String("path", backupPath))
	return nil
}

// RotateBackups rotates the current backup file to a timestamped version
// and removes old backups beyond the retention limit.
func RotateBackups(dbPath string, keep int) error {
	if keep < 1 {
		keep = config.DefaultBackupRetention
	}

	basePath := dbPath + ".backup"

	// Check if backup exists
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return nil // No backup to rotate
	}

	// Create timestamped backup
	timestamp := time.Now().Format("20060102_150405")
	timedBackupPath := fmt.Sprintf("%s.%s", basePath, timestamp)

	if err := os.Rename(basePath, timedBackupPath); err != nil {
		return fmt.Errorf("failed to rename backup: %w", err)
	}

	logger.Info("Rotated backup", zap.String("path", timedBackupPath))

	// Find and remove old backups
	pattern := basePath + ".*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to find backups: %w", err)
	}

	// Sort backups by modification time (newest first)
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

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.After(backups[j].modTime)
	})

	// Remove old backups beyond retention limit
	removed := 0
	for i := keep; i < len(backups); i++ {
		if err := os.Remove(backups[i].path); err == nil {
			logger.Info("Removed old backup", zap.String("path", backups[i].path))
			removed++
		} else {
			logger.Warn("Failed to remove old backup",
				zap.String("path", backups[i].path),
				zap.Error(err))
		}
	}

	if removed > 0 {
		logger.Info("Cleaned up old backups", zap.Int("count", removed))
	}

	return nil
}

// DailyBackup performs a database backup and rotation.
// This is the main entry point for scheduled backups.
func DailyBackup(dbPath string, keepDays int) error {
	if keepDays < 1 {
		keepDays = config.DefaultBackupRetention
	}

	if err := BackupDatabase(dbPath); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	if err := RotateBackups(dbPath, keepDays); err != nil {
		return fmt.Errorf("rotation failed: %w", err)
	}

	return nil
}

// GetBackupInfo returns information about existing backups.
func GetBackupInfo(dbPath string) ([]BackupInfo, error) {
	basePath := dbPath + ".backup"
	pattern := basePath + ".*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to find backups: %w", err)
	}

	// Also check for the main backup file
	if _, err := os.Stat(basePath); err == nil {
		matches = append(matches, basePath)
	}

	infos := make([]BackupInfo, 0, len(matches))
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}

		infos = append(infos, BackupInfo{
			Path:    match,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}

	// Sort by modification time (newest first)
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].ModTime.After(infos[j].ModTime)
	})

	return infos, nil
}

// BackupInfo contains information about a backup file.
type BackupInfo struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// TotalBackupSize returns the total size of all backup files.
func TotalBackupSize(dbPath string) (int64, error) {
	infos, err := GetBackupInfo(dbPath)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, info := range infos {
		total += info.Size
	}

	return total, nil
}
