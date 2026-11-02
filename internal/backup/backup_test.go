package backup

import (
	"bytes"
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
	err := testutil.InitLogger(m)
	if err != nil {
		os.Stderr.WriteString("Failed to initialize logger: " + err.Error() + "\n")
		os.Exit(1)
	}

	goleak.VerifyTestMain(m)
}

// createTestSQLiteDB создаёт валидную SQLite-базу с таблицей subscriptions
// и указанным числом строк — минимальный fixture для прохождения проверки бэкапа.
func createTestSQLiteDB(t *testing.T, dbPath string, rows int) {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	defer db.Close()

	_, err = db.Exec("CREATE TABLE subscriptions (id INTEGER PRIMARY KEY, status TEXT)")
	require.NoError(t, err)

	for range rows {
		_, err = db.Exec("INSERT INTO subscriptions (status) VALUES ('active')")
		require.NoError(t, err)
	}
}

func TestBackupDatabase(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a real SQLite database with the subscriptions table
	createTestSQLiteDB(t, dbPath, 1)

	err := BackupDatabase(context.Background(), dbPath)
	require.NoError(t, err, "BackupDatabase() error")

	// Check backup file exists
	backupPath := dbPath + ".backup"
	fi, err := os.Stat(backupPath)
	require.NoError(t, err, "Backup file was not created")
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "backup file mode should be 0600")

	// Check backup passes its own integrity verification
	backupDB, err := sql.Open("sqlite3", "file:"+backupPath+"?mode=ro")
	require.NoError(t, err)

	defer backupDB.Close()

	var integrity string
	require.NoError(t, backupDB.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	assert.Equal(t, "ok", integrity, "Backup copy must pass integrity_check")

	var count int
	require.NoError(t, backupDB.QueryRow("SELECT COUNT(*) FROM subscriptions").Scan(&count))
	assert.Equal(t, 1, count, "Backup must contain the original rows")

	_, err = os.Stat(backupPath + ".corrupt")
	assert.True(t, os.IsNotExist(err), "No .corrupt marker expected for a valid database")
}

func TestBackupDatabase_WALContainsAllCommittedRows(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "wal.db")
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	defer db.Close()

	_, err = db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE payments (id INTEGER PRIMARY KEY, status TEXT); CREATE TABLE subscriptions (id INTEGER PRIMARY KEY); INSERT INTO payments(status) VALUES ('pending');")
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

	// Create real database so the backup passes verification
	createTestSQLiteDB(t, dbPath, 1)

	// Create first backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "First BackupDatabase() error")

	// Modify database
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO subscriptions (status) VALUES ('active')")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Create second backup
	require.NoError(t, BackupDatabase(context.Background(), dbPath), "Second BackupDatabase() error")

	// Check backup has new content
	backupDB, err := sql.Open("sqlite3", "file:"+backupPath+"?mode=ro")
	require.NoError(t, err)

	defer backupDB.Close()

	var count int
	require.NoError(t, backupDB.QueryRow("SELECT COUNT(*) FROM subscriptions").Scan(&count))
	assert.Equal(t, 2, count, "Backup should contain the modified data")
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
					createTestSQLiteDB(t, dbPath, 1)
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

		for i := range 7 {
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

		for i := range 5 {
			backupName := fmt.Sprintf("%s.backup.2024010%d_120000", dbPath, i)
			require.NoError(t, os.WriteFile(backupName, fmt.Appendf(nil, "backup %d", i), 0644))
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
				createTestSQLiteDB(t, dbPath, 1)
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
				createTestSQLiteDB(t, dbPath, 1)
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

	for i := range 3 {
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
				createTestSQLiteDB(t, dbPath, 1)
				require.NoError(t, BackupDatabase(context.Background(), dbPath), "BackupDatabase() error")
			},
			check: func(t *testing.T, size int64) {
				assert.Greater(t, size, int64(0), "Expected non-zero total backup size")
			},
		},
		{
			name: "multiple_backups",
			setup: func(t *testing.T, dbPath string) {
				createTestSQLiteDB(t, dbPath, 1)
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

	// Create an empty file: SQLite treats it as a valid empty database,
	// but it has no subscriptions table, so backup verification must fail.
	require.NoError(t, os.WriteFile(dbPath, []byte{}, 0600), "Failed to create empty test file")

	err := BackupDatabase(context.Background(), dbPath)
	require.Error(t, err, "BackupDatabase() should fail verification for an empty database")
	assert.ErrorContains(t, err, "backup verification failed")

	// The failed copy is preserved with the .corrupt suffix, no good backup is published
	require.FileExists(t, dbPath+".backup.corrupt")
	_, err = os.Stat(dbPath + ".backup")
	assert.True(t, os.IsNotExist(err), "No .backup should be published when verification fails")

	// The source file remains untouched
	fi, err := os.Stat(dbPath)
	require.NoError(t, err)
	assert.Equal(t, int64(0), fi.Size(), "Source file must remain untouched")
}

func TestBackupDatabase_LargeFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a large real SQLite database (~8MB blob) so the copy exercises
	// the chunked copy path and passes verification
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	_, err = db.Exec("CREATE TABLE subscriptions (id INTEGER PRIMARY KEY, status TEXT); " +
		"CREATE TABLE blobs (id INTEGER PRIMARY KEY, data BLOB); " +
		"INSERT INTO subscriptions (status) VALUES ('active'); " +
		"INSERT INTO blobs (data) VALUES (zeroblob(8 * 1024 * 1024));")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	ctx := context.Background()
	err = BackupDatabase(ctx, dbPath)
	require.NoError(t, err, "BackupDatabase() should succeed with large file")

	fi, err := os.Stat(dbPath + ".backup")
	require.NoError(t, err)
	assert.Greater(t, fi.Size(), int64(4*1024*1024), "Backup should be large")

	// The large copy must pass verification too
	backupDB, err := sql.Open("sqlite3", "file:"+dbPath+".backup?mode=ro")
	require.NoError(t, err)

	defer backupDB.Close()

	var integrity string
	require.NoError(t, backupDB.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	assert.Equal(t, "ok", integrity)
}

// ==================== Backup Verification Tests ====================

func TestBackupDatabase_CorruptCopyMarkedCorrupt(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Publish a good backup first
	createTestSQLiteDB(t, dbPath, 1)
	require.NoError(t, BackupDatabase(context.Background(), dbPath))

	goodBackup, err := os.ReadFile(dbPath + ".backup")
	require.NoError(t, err, "Failed to read the good backup")

	// Corrupt the source file directly: no valid SQLite header and no WAL
	// sidecar, so the copy is produced from garbage and must fail verification
	garbage := bytes.Repeat([]byte("not a sqlite database at all\n"), 100)
	require.NoError(t, os.WriteFile(dbPath, garbage, 0600))

	err = BackupDatabase(context.Background(), dbPath)
	require.Error(t, err, "BackupDatabase() should report verification failure")
	assert.ErrorContains(t, err, "backup verification failed")

	// The bad copy is preserved with the .corrupt suffix for inspection
	require.FileExists(t, dbPath+".backup.corrupt")

	// The previous good backup is not overwritten
	preserved, err := os.ReadFile(dbPath + ".backup")
	require.NoError(t, err, "Failed to read the preserved backup")
	assert.Equal(t, goodBackup, preserved, "Previous good backup must not be overwritten by a failed copy")

	// The source file remains untouched
	content, err := os.ReadFile(dbPath)
	require.NoError(t, err)
	assert.Equal(t, garbage, content, "Source database must remain untouched")
}

func TestBackupDatabase_MissingSubscriptionsTableMarkedCorrupt(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// A valid SQLite database without the subscriptions table:
	// integrity_check passes, but the required table check must fail
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	_, err = db.Exec("CREATE TABLE other (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = BackupDatabase(context.Background(), dbPath)
	require.Error(t, err, "BackupDatabase() should report verification failure")
	assert.ErrorContains(t, err, "subscriptions table check failed")

	require.FileExists(t, dbPath+".backup.corrupt")
	_, err = os.Stat(dbPath + ".backup")
	assert.True(t, os.IsNotExist(err), "No .backup should be published when verification fails")
}

func TestBackupIgnoresCorruptFiles(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db")
	require.NoError(t, os.WriteFile(dbPath+".backup", []byte("valid"), 0600))
	require.NoError(t, os.WriteFile(dbPath+".backup.corrupt", []byte("corrupt"), 0600))
	require.NoError(t, RotateBackups(dbPath, 1))
	infos, err := GetBackupInfo(dbPath)
	require.NoError(t, err)
	require.Len(t, infos, 1)
	assert.NotContains(t, infos[0].Path, ".corrupt")
	assert.FileExists(t, dbPath+".backup.corrupt")
}

func TestVerifyBackupCopyCleansSidecarsOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy")
	for _, suffix := range []string{"-wal", "-shm"} {
		require.NoError(t, os.WriteFile(path+suffix, []byte("sidecar"), 0600))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, verifyBackupCopy(ctx, path))
	for _, suffix := range []string{"-wal", "-shm"} {
		assert.NoFileExists(t, path+suffix)
	}
}
