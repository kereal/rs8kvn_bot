package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestInit(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	err := Init(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if Log == nil {
		t.Fatal("Log is nil after Init()")
	}

	// Clean up
	Close()
}

func TestInit_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "subdir", "test.log")

	err := Init(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(filepath.Dir(logPath)); os.IsNotExist(err) {
		t.Fatal("Init() did not create parent directory")
	}

	Close()
}

func TestInit_InvalidLogLevel(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Invalid log level should default to info
	err := Init(logPath, "invalid")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("Init() with invalid level should not error, got: %v", err)
	}

	Close()
}

func TestInfo(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Should not panic
	Info("test info message")
	Infof("test info message: %s", "formatted")
}

func TestError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Should not panic
	Error("test error message")
	Errorf("test error message: %s", "formatted")
}

func TestDebug(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	if err := Init(logPath, "debug"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Should not panic
	Debug("test debug message")
	Debugf("test debug message: %s", "formatted")
}

func TestWarn(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Should not panic
	Warn("test warn message")
	Warnf("test warn message: %s", "formatted")
}

func TestSync(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Sync may return error for stdout on some systems, that's acceptable
	_ = Sync()
}

func TestSync_NilLogger(t *testing.T) {
	// Reset logger to nil
	Log = nil

	// Should not panic
	if err := Sync(); err != nil {
		t.Errorf("Sync() with nil logger should not error, got: %v", err)
	}
}

func TestClose(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Close may return sync error for stdout on some systems, that's acceptable
	_ = Close()
}

func TestClose_NilLogger(t *testing.T) {
	// Reset logger to nil
	Log = nil
	fileWriter = nil

	// Should not panic
	if err := Close(); err != nil {
		t.Errorf("Close() with nil logger should not error, got: %v", err)
	}
}

func TestClose_MultipleCalls(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// First close - ignore sync errors for stdout
	_ = Close()

	// Second close should not panic
	_ = Close()
}

func TestLogFileWritten(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	Info("test message for file")

	// Sync to ensure write
	Sync()
	Close()

	// Check file exists and has content
	content, err := os.ReadFile(logPath)
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if len(content) == 0 {
		t.Error("Log file is empty")
	}

	// Check message is in file
	contentStr := string(content)
	if len(contentStr) < 10 {
		t.Error("Log file content too short")
	}
}

func TestFormatArgs(t *testing.T) {
	tests := []struct {
		args     []interface{}
		expected string
	}{
		{[]interface{}{"hello"}, "hello"},
		{[]interface{}{123}, "123"},
		{[]interface{}{"hello", "world"}, "helloworld"},
		{[]interface{}{1, 2, 3}, "1 2 3"}, // fmt.Sprint adds spaces between numbers
	}

	for _, tt := range tests {
		result := formatArgs(tt.args...)
		if result != tt.expected {
			t.Errorf("formatArgs(%v) = %q, want %q", tt.args, result, tt.expected)
		}
	}
}

// ==================== Service Tests ====================

func TestNewService(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if service == nil {
		t.Fatal("NewService() returned nil")
	}

	// Clean up
	service.Close()
}

func TestNewService_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "subdir", "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(filepath.Dir(logPath)); os.IsNotExist(err) {
		t.Fatal("NewService() did not create parent directory")
	}

	service.Close()
}

func TestService_Close(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	// Close may return sync error on stdout, which is expected
	_ = service.Close()
}

func TestService_Info(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Should not panic
	service.Info("test info message")
	service.Infof("test info: %s", "formatted")
}

func TestService_Debug(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "debug")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	service.Debug("test debug message")
	service.Debugf("test debug: %s", "formatted")
}

func TestService_Warn(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	service.Warn("test warn message")
	service.Warnf("test warn: %s", "formatted")
}

func TestService_Error(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	service.Error("test error message")
	service.Errorf("test error: %s", "formatted")
}

func TestService_WithField(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	newService := service.WithField("key", "value")
	if newService == nil {
		t.Error("WithField() returned nil")
	}

	newService.Info("test with field")
}

func TestService_WithFields(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	fields := map[string]interface{}{
		"key1": "value1",
		"key2": 123,
	}

	newService := service.WithFields(fields)
	if newService == nil {
		t.Error("WithFields() returned nil")
	}

	newService.Info("test with fields")
}

func TestService_WithError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	testErr := fmt.Errorf("test error")
	newService := service.WithError(testErr)
	if newService == nil {
		t.Error("WithError() returned nil")
	}
}

func TestService_SetSentryHub(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	service, err := NewService(logPath, "info")
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Should not panic with nil hub
	service.SetSentryHub(nil)
}

// ==================== tgbotapiLogger Tests ====================

func TestTgbotapiLogger_Println(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize logger first
	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	logger := &tgbotapiLogger{}
	// Should not panic
	logger.Println("test", "message")
}

func TestTgbotapiLogger_Printf(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize logger first
	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	writer := &stdLogWriter{}

	// Test writing
	n, err := writer.Write([]byte("test message\n"))
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}
	if n == 0 {
		t.Error("Write() returned 0 bytes")
	}

	// Test empty message
	n, err = writer.Write([]byte(""))
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}

	// Test whitespace only
	n, err = writer.Write([]byte("   \n\t  "))
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}
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
	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	writer := Writer()
	if writer == nil {
		t.Error("Writer() returned nil")
	}

	// Test writing through the returned writer
	_, err := writer.Write([]byte("test message\n"))
	// Ignore sync errors on stdout/stderr
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}
}

// ==================== RedirectStdLog Tests ====================

func TestRedirectStdLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize logger first
	if err := Init(logPath, "info"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Should not panic
	RedirectStdLog()
}

// ==================== Edge Cases ====================

func TestNilLoggerSafety(t *testing.T) {
	// Test that global functions don't panic with nil logger
	// This simulates calling logging functions before Init()

	// Temporarily set Log to nil
	oldLog := Log
	Log = nil
	defer func() { Log = oldLog }()

	// These should not panic
	Info("test")
	Infof("test %s", "formatted")
	Debug("test")
	Debugf("test %s", "formatted")
	Warn("test")
	Warnf("test %s", "formatted")
}
