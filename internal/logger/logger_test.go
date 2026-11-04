package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

// newTempLogger creates a Service backed by a temp-dir log file and registers
// Close via t.Cleanup, replacing the repeated Init/NewService setup boilerplate.
func newTempLogger(t *testing.T) *Service {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "test.log")
	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	require.NotNil(t, service, "NewService() returned nil")

	t.Cleanup(func() { _ = service.Close() })

	return service
}

// newTempGlobalLogger is newTempLogger for tests that exercise the package
// globals (Log, stdLogWriter, RedirectStdLog) via Init/Close.
func newTempGlobalLogger(t *testing.T) string {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "test.log")
	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")

	t.Cleanup(func() { _ = Close() })

	return logPath
}

func TestSentryLevelFromString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected sentry.Level
	}{
		{"debug", "debug", sentry.LevelDebug},
		{"info", "info", sentry.LevelInfo},
		{"warn", "warn", sentry.LevelWarning},
		{"error", "error", sentry.LevelError},
		{"fatal", "fatal", sentry.LevelFatal},
		{"unknown", "unknown", sentry.LevelError},
		{"empty", "", sentry.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sentryLevelFromString(tt.input)
			assert.Equal(t, tt.expected, result, "sentryLevelFromString(%q)", tt.input)
		})
	}
}

func TestInit_InvalidLogLevel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "test.log")

	// Invalid log level should default to info, not error
	_, err := Init(logPath, "invalid")
	require.NoError(t, err, "Init() with invalid level should not error")

	_ = Close()
}

func TestLogFileWritten(t *testing.T) {
	logPath := newTempGlobalLogger(t)

	Info("test message for file")

	// Sync to ensure write
	_ = Sync()

	// Check file exists and has content
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	assert.NotEmpty(t, content, "Log file is empty")
	assert.Contains(t, string(content), "test message for file", "Logged message should be written to the file")
}

// ==================== Service Tests ====================

func TestNewService(t *testing.T) {
	_ = newTempLogger(t)
}

func TestNewService_CreatesDirectory(t *testing.T) {
	// Init delegates to NewService, so this also covers Init creating dirs.
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "subdir", "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")

	// Verify directory was created
	_, err = os.Stat(filepath.Dir(logPath))
	assert.NoError(t, err, "NewService() did not create parent directory")

	_ = service.Close()
}

func TestService_Close(t *testing.T) {
	service := newTempLogger(t)

	// Close may return sync error on stdout, which is expected
	_ = service.Close()
}

func TestService_With(t *testing.T) {
	service := newTempLogger(t)

	newService := service.With(zap.String("key", "value"))
	assert.NotNil(t, newService, "With() returned nil")
	newService.Info("test with field")

	multiService := service.With(
		zap.String("key1", "value1"),
		zap.Int("key2", 123),
	)
	assert.NotNil(t, multiService, "With() returned nil")
	multiService.Info("test with fields")
}

func TestService_WithError(t *testing.T) {
	t.Run("no_sentry", func(t *testing.T) {
		service := newTempLogger(t)

		newService := service.WithError(fmt.Errorf("test error with context"))
		assert.NotNil(t, newService, "WithError() should not return nil")
		newService.Info("logged with error")
	})

	t.Run("with_hub", func(t *testing.T) {
		service := newTempLogger(t)

		client, _ := sentry.NewClient(sentry.ClientOptions{Dsn: ""})
		service.SetSentryHub(sentry.NewHub(client, sentry.NewScope()))

		newService := service.WithError(fmt.Errorf("test error with sentry hub"))
		assert.NotNil(t, newService, "WithError() should not return nil")
		newService.Info("logged with error")
	})
}

// ==================== stdLogWriter Tests ====================

func TestStdLogWriter_Write(t *testing.T) {
	newTempGlobalLogger(t)

	writer := &stdLogWriter{}

	// Test writing
	n, err := writer.Write([]byte("test message\n"))
	assert.NoError(t, err, "Write() error")
	assert.Greater(t, n, 0, "Write() returned 0 bytes")

	// Test empty message
	_, err = writer.Write([]byte(""))
	assert.NoError(t, err, "Write() error")

	// Test whitespace only
	_, err = writer.Write([]byte("   \n\t  "))
	assert.NoError(t, err, "Write() error")
}

// ==================== Writer Tests ====================

func TestWriter(t *testing.T) {
	newTempGlobalLogger(t)

	writer := Writer()
	assert.NotNil(t, writer, "Writer() returned nil")

	// Test writing through the returned writer
	_, err := writer.Write([]byte("test message\n"))
	assert.NoError(t, err, "Write() error")
}

// ==================== RedirectStdLog Tests ====================

func TestRedirectStdLog_ActualRedirection(t *testing.T) {
	logPath := newTempGlobalLogger(t)

	// Capture original std log output
	oldFlags := log.Flags()
	defer func() {
		log.SetFlags(oldFlags)
	}()

	RedirectStdLog()

	// Write via standard log package
	log.Println("redirected stdlog test message")

	// Give logger time to flush
	time.Sleep(10 * time.Millisecond)
	_ = Sync()

	// Verify the message appears in our log file
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	assert.Contains(t, string(content), "redirected stdlog test message", "Std log output should be redirected to zap logger")
}

// ==================== isStdoutError Tests ====================

func TestIsStdoutError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"invalid argument", fmt.Errorf("invalid argument"), true},
		{"bad file descriptor", fmt.Errorf("bad file descriptor"), true},
		{"other error", fmt.Errorf("some other error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isStdoutError(tt.err))
		})
	}
}
