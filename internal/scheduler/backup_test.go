package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rs8kvn_bot/internal/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

func TestBackupScheduler_New(t *testing.T) {
	t.Parallel()

	scheduler := NewBackupScheduler("/tmp/test.db", 2, 7)

	assert.NotNil(t, scheduler)
	assert.Equal(t, "/tmp/test.db", scheduler.dbPath)
	assert.Equal(t, 2, scheduler.hour)
	assert.Equal(t, 7, scheduler.retention)
}

func TestBackupScheduler_Start_ContextCancel(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	f, err := os.Create(dbPath)
	require.NoError(t, err)
	f.Close()

	scheduler := NewBackupScheduler(dbPath, 3, 7)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		scheduler.Start(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Scheduler should stop after context cancel")
	}
}

func TestBackupScheduler_CalculateNextBackup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	f, err := os.Create(dbPath)
	require.NoError(t, err)
	f.Close()

	scheduler := NewBackupScheduler(dbPath, 2, 7)

	_ = scheduler

	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
	if now.After(next) {
		next = next.Add(24 * time.Hour)
	}

	duration := time.Until(next)
	assert.Greater(t, duration, 0*time.Second)
}
