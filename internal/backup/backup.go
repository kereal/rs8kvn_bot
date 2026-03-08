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

func BackupDatabase(dbPath string) error {
	backupPath := dbPath + ".backup"
	tempPath := backupPath + ".tmp"

	src, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create temp backup: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to copy database: %w", err)
	}

	if err := dst.Sync(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to sync backup: %w", err)
	}

	if err := os.Rename(tempPath, backupPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename backup: %w", err)
	}

	logger.Infof("Database backup created: %s", backupPath)
	return nil
}

func RotateBackups(dbPath string, keep int) error {
	basePath := dbPath + ".backup"

	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return nil
	}

	timestamp := time.Now().Format("20060102_150405")
	timedBackupPath := fmt.Sprintf("%s.%s", basePath, timestamp)

	if err := os.Rename(basePath, timedBackupPath); err != nil {
		return fmt.Errorf("failed to rename backup: %w", err)
	}

	logger.Infof("Rotated backup to: %s", timedBackupPath)

	pattern := basePath + ".*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to find backups: %w", err)
	}

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

func DailyBackup(dbPath string, keepDays int) error {
	if err := BackupDatabase(dbPath); err != nil {
		return err
	}
	return RotateBackups(dbPath, keepDays)
}
