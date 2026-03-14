package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	Log        *zap.SugaredLogger
	fileWriter *lumberjack.Logger
	logMu      sync.Mutex
)

func Init(logFilePath, level string) error {
	logDir := filepath.Dir(logFilePath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	encoder := zapcore.NewConsoleEncoder(encoderConfig)

	var cores []zapcore.Core

	cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zapLevel))

	fileWriter = &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    5,     // 5MB max file size (reduced from 10MB)
		MaxBackups: 2,     // Keep only 2 backup files (reduced from 3)
		MaxAge:     7,     // Keep logs for 7 days (reduced from 30)
		Compress:   false, // Disable compression to save memory
	}
	cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(fileWriter), zapLevel))

	core := zapcore.NewTee(cores...)

	Log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()

	return nil
}

// stdLogWriter is an io.Writer that redirects standard log output to our zap logger
type stdLogWriter struct{}

func (w *stdLogWriter) Write(p []byte) (n int, err error) {
	if Log == nil {
		return len(p), nil
	}

	msg := strings.TrimSpace(string(p))
	if len(msg) == 0 {
		return len(p), nil
	}

	// Detect error/warning messages and handle accordingly
	if strings.Contains(msg, "Conflict:") ||
		strings.Contains(msg, "Failed to") ||
		strings.Contains(msg, "Error:") ||
		strings.Contains(msg, "error:") {
		// Send to Sentry
		sentry.CaptureMessage("[TGAPI] " + msg)
		Log.Warn(msg)
	} else if strings.Contains(msg, "WARN") || strings.Contains(msg, "warn") {
		Log.Warn(msg)
	} else {
		// Default to info level
		Log.Info(msg)
	}

	return len(p), nil
}

// RedirectStdLog redirects standard Go log output to our zap logger.
// This ensures all log messages (including from third-party libraries)
// have consistent formatting.
func RedirectStdLog() {
	log.SetOutput(&stdLogWriter{})
	log.SetFlags(0) // Disable standard log flags since we handle formatting ourselves
}

func Info(args ...interface{}) {
	Log.Info(args...)
}

func Infof(template string, args ...interface{}) {
	Log.Infof(template, args...)
}

func Error(args ...interface{}) {
	Log.Error(args...)
	// Send error to Sentry
	sentry.CaptureMessage(formatArgs(args...))
}

func Errorf(template string, args ...interface{}) {
	Log.Errorf(template, args...)
	// Send error to Sentry
	sentry.CaptureMessage(fmt.Sprintf(template, args...))
}

func Debug(args ...interface{}) {
	Log.Debug(args...)
}

func Debugf(template string, args ...interface{}) {
	Log.Debugf(template, args...)
}

func Warn(args ...interface{}) {
	Log.Warn(args...)
}

func Warnf(template string, args ...interface{}) {
	Log.Warnf(template, args...)
}

func Fatal(args ...interface{}) {
	// Send fatal error to Sentry before exit
	sentry.CaptureMessage("[FATAL] " + formatArgs(args...))
	sentry.Flush(2 * time.Second)
	Log.Fatal(args...)
}

func Fatalf(template string, args ...interface{}) {
	// Send fatal error to Sentry before exit
	sentry.CaptureMessage("[FATAL] " + fmt.Sprintf(template, args...))
	sentry.Flush(2 * time.Second)
	Log.Fatalf(template, args...)
}

func Sync() error {
	if Log != nil {
		return Log.Sync()
	}
	return nil
}

func Close() error {
	logMu.Lock()
	defer logMu.Unlock()

	if Log != nil {
		if err := Log.Sync(); err != nil {
			return err
		}
	}

	if fileWriter != nil {
		if err := fileWriter.Close(); err != nil {
			return err
		}
	}

	return nil
}

// formatArgs formats variadic args to a string
func formatArgs(args ...interface{}) string {
	return fmt.Sprint(args...)
}
