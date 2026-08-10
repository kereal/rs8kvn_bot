package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/testutil"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	if err := testutil.InitLogger(m); err != nil {
		os.Stderr.WriteString("Failed to initialize logger: " + err.Error() + "\n")
		os.Exit(1)
	}
	goleak.VerifyTestMain(m)
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
	fi, err := os.Stat(backupPath)
	require.NoError(t, err, "Backup file was not created")
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "backup file mode should be 0600")

	// Check backup content matches original
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err, "Failed to read backup file")

	assert.Equal(t, string(content), string(backupContent), "Backup content does not match original")
}

func TestBackupDatabase_WALContainsAllCommittedRows(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "wal.db")
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE payments (id INTEGER PRIMARY KEY, status TEXT); INSERT INTO payments(status) VALUES ('pending');")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO payments(status) VALUES ('paid')")
	require.NoError(t, err)
	require.FileExists(t, dbPath+"-wal")

	require.NoError(t, BackupDatabase(context.Background(), dbPath))

	backupDB, err := sql.Open("sqlite3", dbPath+".backup")
	require.NoError(t, err)
	defer backupDB.Close()

	var count int
	require.NoError(t, backupDB.QueryRow("SELECT COUNT(*) FROM payments").Scan(&count))
	assert.Equal(t, 2, count)
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

func TestRotateBackups(t *testing.T) {
	t.Parallel()

	t.Run("simple scenarios", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			setup func(tmpDir string, dbPath string)

			check func(t *testing.T, tmpDir string, dbPath string)
		}{
			{
				name:  "no_backup_file",
				setup: func(tmpDir, dbPath string) {},
				check: func(t *testing.T, tmpDir, dbPath string) {
					assert.NoError(t, RotateBackups(dbPath, 5))
				},
			},
			{
				name: "with_current_backup",
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
				name:  "nonexistent_db",
				setup: func(tmpDir, dbPath string) {},
				check: func(t *testing.T, tmpDir, dbPath string) {
					assert.NoError(t, RotateBackups(filepath.Join(tmpDir, "nonexistent.db"), 5))
				},
			},
			{
				name: "keep_zero_uses_default",
				setup: func(tmpDir, dbPath string) {
					require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0644))
					require.NoError(t, BackupDatabase(context.Background(), dbPath))
				},
				check: func(t *testing.T, tmpDir, dbPath string) {
					assert.NoError(t, RotateBackups(dbPath, 0))
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

	tests := []struct {
		name     string
		keepDays int
		createDB bool
		check    func(t *testing.T, dbPath string)
	}{
		{
			name:     "default_keep_days",
			keepDays: 7,
			createDB: true,
			check: func(t *testing.T, dbPath string) {
				matches, err := filepath.Glob(dbPath + ".backup.*")
				require.NoError(t, err, "Failed to find backups")
				assert.Equal(t, 1, len(matches), "Expected 1 timed backup")
			},
		},
		{
			name:     "keep_days_zero",
			keepDays: 0,
			createDB: true,
			check: func(t *testing.T, dbPath string) {
				matches, err := filepath.Glob(dbPath + ".backup.*")
				require.NoError(t, err, "Failed to find backups")
				assert.Equal(t, 1, len(matches), "With keepDays=0 still triggers daily backup")
			},
		},
		{
			name:     "keep_days_one",
			keepDays: 1,
			createDB: true,
			check: func(t *testing.T, dbPath string) {
				infos, err := GetBackupInfo(dbPath)
				require.NoError(t, err, "GetBackupInfo() error")
				assert.Equal(t, 1, len(infos), "Expected 1 backup with keepDays=1")
			},
		},
		{
			name:     "non_existent_database",
			keepDays: 7,
			createDB: false,
			check: func(t *testing.T, dbPath string) {
				err := DailyBackup(context.Background(), dbPath, 7)
				require.Error(t, err, "DailyBackup() should return error for non-existent database")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpDir := t.TempDir()
			dbPath := filepath.Join(tmpDir, "test.db")

			if tt.createDB {
				require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create test database")
				require.NoError(t, DailyBackup(context.Background(), dbPath, tt.keepDays), "DailyBackup() error")
			}
			tt.check(t, dbPath)
		})
	}
}

func TestGetBackupInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, dbPath string)
		check func(t *testing.T, infos []BackupInfo)
	}{
		{
			name:  "empty",
			setup: func(t *testing.T, dbPath string) {},
			check: func(t *testing.T, infos []BackupInfo) {
				assert.Equal(t, 0, len(infos), "Expected 0 backups")
			},
		},
		{
			name: "with_backups",
			setup: func(t *testing.T, dbPath string) {
				require.NoError(t, os.WriteFile(dbPath, []byte("content"), 0600), "Failed to create database")
				require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")
				require.NoError(t, DailyBackup(context.Background(), dbPath, 7), "DailyBackup() error")
			},
			check: func(t *testing.T, infos []BackupInfo) {
				assert.Greater(t, len(infos), 0, "Expected at least 1 backup info")
				for _, info := range infos {
					assert.NotEmpty(t, info.Path, "Backup path should not be empty")
					assert.NotEmpty(t, info.Size, "BackupInfo.Size should not be zero")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpDir := t.TempDir()
			dbPath := filepath.Join(tmpDir, "test.db")
			tt.setup(t, dbPath)

			infos, err := GetBackupInfo(dbPath)
			require.NoError(t, err, "GetBackupInfo() error")
			tt.check(t, infos)
		})
	}
}

func TestGetBackupInfo_SortedByTime(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	base := time.Now().Add(-3 * time.Hour)

	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("%s.backup.2024010%d_120000", dbPath, i)
		require.NoError(t, os.WriteFile(path, []byte("backup"), 0600))
		modTime := base.Add(time.Duration(i) * time.Hour)
		require.NoError(t, os.Chtimes(path, modTime, modTime))
	}

	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err, "GetBackupInfo() error")
	require.Len(t, infos, 3)
	for i := 0; i < len(infos)-1; i++ {
		require.True(t, infos[i].ModTime.After(infos[i+1].ModTime),
			"backups should be sorted newest first")
	}
}

func TestTotalBackupSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, dbPath string)
		check func(t *testing.T, size int64)
	}{
		{
			name:  "empty",
			setup: func(t *testing.T, dbPath string) {},
			check: func(t *testing.T, size int64) {
				assert.Equal(t, int64(0), size, "Expected 0 size")
			},
		},
		{
			name: "with_backups",
			setup: func(t *testing.T, dbPath string) {
				content := []byte("test database content")
				require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create database")
				require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")
			},
			check: func(t *testing.T, size int64) {
				assert.Greater(t, size, int64(0), "Expected non-zero total backup size")
			},
		},
		{
			name: "multiple_backups",
			setup: func(t *testing.T, dbPath string) {
				content := []byte("test content")
				require.NoError(t, os.WriteFile(dbPath, content, 0600), "Failed to create database")
				require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")
			},
			check: func(t *testing.T, size int64) {
				assert.GreaterOrEqual(t, size, int64(5), "Expected size >= 5")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpDir := t.TempDir()
			dbPath := filepath.Join(tmpDir, "test.db")
			tt.setup(t, dbPath)

			size, err := TotalBackupSize(dbPath)
			require.NoError(t, err, "TotalBackupSize() error")
			tt.check(t, size)
		})
	}
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
