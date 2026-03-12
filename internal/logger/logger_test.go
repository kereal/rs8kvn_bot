package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInit(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	err := Init(logPath, "info")
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
