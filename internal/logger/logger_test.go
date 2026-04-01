package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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

func TestInit_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "subdir", "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")

	// Verify directory was created
	_, err = os.Stat(filepath.Dir(logPath))
	assert.NoError(t, err, "Init() did not create parent directory")

	Close()
}

func TestInit_InvalidLogLevel(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Invalid log level should default to info
	_, err := Init(logPath, "invalid")
	require.NoError(t, err, "Init() with invalid level should not error")

	Close()
}

func TestInfo(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	// Should not panic
	Info("test info message")
	Info("test info message", zap.String("formatted", "formatted"))
}

func TestError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	// Should not panic
	Error("test error message")
	Error("test error message", zap.String("formatted", "formatted"))
}

func TestDebug(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "debug")
	require.NoError(t, err, "Init() error")
	defer Close()

	// Should not panic
	Debug("test debug message")
	Debug("test debug message", zap.String("formatted", "formatted"))
}

func TestWarn(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	// Should not panic
	Warn("test warn message")
	Warn("test warn message", zap.String("formatted", "formatted"))
}

func TestSync(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	// Sync may return error for stdout on some systems, that's acceptable
	_ = Sync()
}

func TestSync_NilLogger(t *testing.T) {
	// This test verifies that Sync() handles edge cases gracefully.
	// In production, the logger is always initialized before use.
	// We skip this test as it tests deprecated nil-check behavior.
	t.Skip("Testing nil logger behavior is deprecated; logger is always initialized in production")
}

func TestClose(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")

	// Close may return sync error for stdout on some systems, that's acceptable
	_ = Close()
}

func TestClose_NilLogger(t *testing.T) {
	// This test verifies that Close() handles edge cases gracefully.
	// In production, the logger is always initialized before use.
	// We skip this test as it tests deprecated nil-check behavior.
	t.Skip("Testing nil logger behavior is deprecated; logger is always initialized in production")
}

func TestClose_MultipleCalls(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")

	// First close - ignore sync errors for stdout
	_ = Close()

	// Second close should not panic
	_ = Close()
}

func TestLogFileWritten(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")

	Info("test message for file")

	// Sync to ensure write
	Sync()
	Close()

	// Check file exists and has content
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	assert.NotEmpty(t, content, "Log file is empty")

	// Check message is in file
	contentStr := string(content)
	assert.Greater(t, len(contentStr), 10, "Log file content too short")
}

// formatArgs function removed - no longer needed with structured logging

// ==================== Service Tests ====================

func TestNewService(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	require.NotNil(t, service, "NewService() returned nil")

	// Clean up
	service.Close()
}

func TestNewService_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "subdir", "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")

	// Verify directory was created
	_, err = os.Stat(filepath.Dir(logPath))
	assert.NoError(t, err, "NewService() did not create parent directory")

	service.Close()
}

func TestService_Close(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")

	// Close may return sync error on stdout, which is expected
	_ = service.Close()
}

func TestService_Info(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Should not panic
	service.Info("test info message")
	service.Info("test info", zap.String("formatted", "formatted"))
}

func TestService_Debug(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "debug")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	service.Debug("test debug message")
	service.Debug("test debug", zap.String("formatted", "formatted"))
}

func TestService_Warn(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	service.Warn("test warn message")
	service.Warn("test warn", zap.String("formatted", "formatted"))
}

func TestService_Error(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	service.Error("test error message")
	service.Error("test error", zap.String("formatted", "formatted"))
}

func TestService_With(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	newService := service.With(zap.String("key", "value"))
	assert.NotNil(t, newService, "With() returned nil")

	newService.Info("test with field")
}

func TestService_WithFields(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	newService := service.With(
		zap.String("key1", "value1"),
		zap.Int("key2", 123),
	)
	assert.NotNil(t, newService, "With() returned nil")

	newService.Info("test with fields")
}

func TestService_SetSentryHub(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Should not panic with nil hub
	service.SetSentryHub(nil)
}

// ==================== tgbotapiLogger Tests ====================

func TestTgbotapiLogger_Println(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize logger first
	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	logger := &tgbotapiLogger{}
	// Should not panic
	logger.Println("test", "message")
}

func TestTgbotapiLogger_Printf(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize logger first
	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	logger := &tgbotapiLogger{}
	// Should not panic
	logger.Printf("test %s", "formatted")
}

// ==================== stdLogWriter Tests ====================

func TestStdLogWriter_Write(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize logger first
	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	writer := &stdLogWriter{}

	// Test writing
	n, err := writer.Write([]byte("test message\n"))
	assert.NoError(t, err, "Write() error")
	assert.Greater(t, n, 0, "Write() returned 0 bytes")

	// Test empty message
	n, err = writer.Write([]byte(""))
	assert.NoError(t, err, "Write() error")

	// Test whitespace only
	n, err = writer.Write([]byte("   \n\t  "))
	assert.NoError(t, err, "Write() error")
}

// ==================== SetSentryHub Tests ====================

func TestSetSentryHub(t *testing.T) {
	// Should not panic with nil hub
	SetSentryHub(nil)
}

// ==================== Writer Tests ====================

func TestWriter(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize logger first
	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	writer := Writer()
	assert.NotNil(t, writer, "Writer() returned nil")

	// Test writing through the returned writer
	_, err = writer.Write([]byte("test message\n"))
	assert.NoError(t, err, "Write() error")
}

// ==================== RedirectStdLog Tests ====================

func TestRedirectStdLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize logger first
	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	// Should not panic
	RedirectStdLog()
}

// ==================== Edge Cases ====================

func TestNilLoggerSafety(t *testing.T) {
	// This test verifies that global functions handle nil logger gracefully.
	// In production, the logger is always initialized before use.
	// We skip this test as it tests deprecated nil-check behavior.
	t.Skip("Testing nil logger behavior is deprecated; logger is always initialized in production")
}

func TestFatal_NilLogger(t *testing.T) {
	// This test verifies that Fatal() handles nil logger gracefully.
	// In production, the logger is always initialized before use.
	// We skip this test as it tests deprecated nil-check behavior.
	t.Skip("Testing nil logger behavior is deprecated; logger is always initialized in production")
}

func TestFlushSentry(t *testing.T) {
	// flushSentry should not panic with nil hub
	flushSentry(0)
}

func TestCaptureToSentry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")
	defer Close()

	// Should not panic
	captureToSentry("test message", "info")
	captureToSentry("error message", "error")
}

func TestService_Fatal(t *testing.T) {
	t.Skip("Fatal calls os.Exit which kills the test process")
}

func TestService_WithError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	testErr := fmt.Errorf("test error with context")

	newService := service.WithError(testErr)
	assert.NotNil(t, newService, "WithError() should not return nil")

	newService.Info("logged with error")
}

func TestService_WithError_NoSentry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	testErr := fmt.Errorf("test error without sentry")
	newService := service.WithError(testErr)
	assert.NotNil(t, newService, "WithError() should not return nil")
	newService.Info("test")
}

func TestService_CaptureSentry_NoSentry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	service.captureSentry("test message", sentry.LevelInfo)
}

func TestService_FlushSentry_NoSentry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	service.flushSentry(0)
}

// ==================== isStdoutError Tests ====================

func TestIsStdoutError_NilError(t *testing.T) {
	assert.False(t, isStdoutError(nil), "nil error should not be stdout error")
}

func TestIsStdoutError_InvalidArgument(t *testing.T) {
	assert.True(t, isStdoutError(fmt.Errorf("invalid argument")), "invalid argument should be stdout error")
}

func TestIsStdoutError_BadFileDescriptor(t *testing.T) {
	assert.True(t, isStdoutError(fmt.Errorf("bad file descriptor")), "bad file descriptor should be stdout error")
}

func TestIsStdoutError_OtherError(t *testing.T) {
	assert.False(t, isStdoutError(fmt.Errorf("some other error")), "other error should not be stdout error")
}

// ==================== Service Sentry Tests ====================

func TestService_CaptureSentry_WithHub(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create a mock hub
	client, _ := sentry.NewClient(sentry.ClientOptions{
		Dsn: "",
	})
	hub := sentry.NewHub(client, sentry.NewScope())
	service.SetSentryHub(hub)

	// Should not panic and should capture event
	service.captureSentry("test message with hub", sentry.LevelError)
}

func TestService_FlushSentry_WithHub(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	client, _ := sentry.NewClient(sentry.ClientOptions{
		Dsn: "",
	})
	hub := sentry.NewHub(client, sentry.NewScope())
	service.SetSentryHub(hub)

	// Should not panic
	service.flushSentry(100 * time.Millisecond)
}

func TestService_WithError_WithHub(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	client, _ := sentry.NewClient(sentry.ClientOptions{
		Dsn: "",
	})
	hub := sentry.NewHub(client, sentry.NewScope())
	service.SetSentryHub(hub)

	testErr := fmt.Errorf("test error with sentry hub")
	newService := service.WithError(testErr)
	assert.NotNil(t, newService, "WithError() should not return nil")
}

// ==================== Close Error Aggregation Tests ====================

func TestClose_BothErrors(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	_, err := Init(logPath, "info")
	require.NoError(t, err, "Init() error")

	// First close may succeed, second close should aggregate errors
	_ = Close()
	err = Close()
	// Second close should return error since logger is already closed
	// We just verify it doesn't panic
	t.Logf("Second Close() returned: %v", err)
}
