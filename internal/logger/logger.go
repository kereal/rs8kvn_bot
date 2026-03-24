package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"rs8kvn_bot/internal/config"
)

var (
	// Log is the global logger instance.
	// Deprecated: Use logger.Service for dependency injection.
	Log        *zap.Logger
	fileWriter *lumberjack.Logger
	logMu      sync.Mutex
	sentryHub  *sentry.Hub
)

// Init initializes the global logger with file and console output.
// Returns a Service for dependency injection.
// The global Log variable is also set for backward compatibility.
func Init(logFilePath, level string) (*Service, error) {
	svc, err := NewService(logFilePath, level)
	if err != nil {
		return nil, err
	}
	Log = svc.log
	return svc, nil
}

// stdLogWriter implements io.Writer for redirecting standard log output.
type stdLogWriter struct{}

func (w *stdLogWriter) Write(p []byte) (n int, err error) {
	if Log == nil {
		return len(p), nil
	}

	msg := strings.TrimSpace(string(p))
	if len(msg) == 0 {
		return len(p), nil
	}

	Log.Info(msg)
	return len(p), nil
}

// tgbotapiLogger implements tgbotapi.BotLogger interface.
type tgbotapiLogger struct{}

func (l *tgbotapiLogger) Println(v ...interface{}) {
	if Log != nil {
		Log.Warn(fmt.Sprint(v...))
	}
}

func (l *tgbotapiLogger) Printf(format string, v ...interface{}) {
	if Log != nil {
		Log.Warn(fmt.Sprintf(format, v...))
	}
}

// RedirectStdLog redirects standard Go log output to our zap logger.
// This ensures all log messages (including from third-party libraries)
// have consistent formatting.
func RedirectStdLog() {
	log.SetOutput(&stdLogWriter{})
	log.SetFlags(0)
	// Ignore error from SetLogger - this is not critical for application startup
	_ = tgbotapi.SetLogger(&tgbotapiLogger{})
}

// Writer returns an io.Writer that logs to our zap logger at INFO level.
func Writer() io.Writer {
	return &stdLogWriter{}
}

// SetSentryHub sets the Sentry hub for error reporting.
// This should be called after Sentry is initialized.
func SetSentryHub(hub *sentry.Hub) {
	logMu.Lock()
	defer logMu.Unlock()
	sentryHub = hub
}

// --- Global logger functions (deprecated, kept for backward compatibility) ---

func Info(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Info(msg, fields...)
	}
}

func Error(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Error(msg, fields...)
	}
	captureToSentry(msg, "error")
}

func Debug(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Debug(msg, fields...)
	}
}

func Warn(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Warn(msg, fields...)
	}
}

func Fatal(msg string, fields ...zap.Field) {
	captureToSentry("[FATAL] "+msg, "fatal")
	flushSentry(config.SentryFlushTimeout)
	if Log != nil {
		Log.Fatal(msg, fields...)
	}
}

func Sync() error {
	if Log != nil {
		return Log.Sync()
	}
	return nil
}

// Close closes the logger and flushes any buffered data.
func Close() error {
	logMu.Lock()
	defer logMu.Unlock()

	var errs []error

	if Log != nil {
		if err := Log.Sync(); err != nil {
			errs = append(errs, err)
		}
	}

	if fileWriter != nil {
		if err := fileWriter.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing logger: %v", errs)
	}
	return nil
}

func captureToSentry(message string, level string) {
	logMu.Lock()
	hub := sentryHub
	logMu.Unlock()

	if hub == nil {
		hub = sentry.CurrentHub()
	}

	if hub != nil {
		event := &sentry.Event{
			Message: message,
			Level:   sentryLevelFromString(level),
		}
		hub.CaptureEvent(event)
	}
}

func sentryLevelFromString(level string) sentry.Level {
	switch level {
	case "debug":
		return sentry.LevelDebug
	case "info":
		return sentry.LevelInfo
	case "warn":
		return sentry.LevelWarning
	case "error":
		return sentry.LevelError
	case "fatal":
		return sentry.LevelFatal
	default:
		return sentry.LevelError
	}
}

func flushSentry(timeout time.Duration) {
	logMu.Lock()
	hub := sentryHub
	logMu.Unlock()

	if hub == nil {
		hub = sentry.CurrentHub()
	}

	if hub != nil {
		hub.Flush(timeout)
	}
}

// --- Service provides dependency injection for logger ---

// Service wraps zap.Logger with Sentry integration.
type Service struct {
	log       *zap.Logger
	file      *lumberjack.Logger
	sentryHub *sentry.Hub
}

// NewService creates a new logger service with file and console output.
func NewService(logFilePath, level string) (*Service, error) {
	logDir := filepath.Dir(logFilePath)
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
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

	// Console output
	cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zapLevel))

	// File output with rotation
	fileWriter := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    config.LogMaxSizeMB,
		MaxBackups: config.LogMaxBackups,
		MaxAge:     config.LogMaxAgeDays,
		Compress:   false,
	}
	cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(fileWriter), zapLevel))

	core := zapcore.NewTee(cores...)
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	return &Service{
		log:  logger,
		file: fileWriter,
	}, nil
}

// SetSentryHub sets the Sentry hub for this logger service.
func (s *Service) SetSentryHub(hub *sentry.Hub) {
	s.sentryHub = hub
}

// Close closes the logger service.
func (s *Service) Close() error {
	var errs []error

	if s.log != nil {
		if err := s.log.Sync(); err != nil {
			errs = append(errs, err)
		}
	}

	if s.file != nil {
		if err := s.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing logger: %v", errs)
	}
	return nil
}

// Info logs at INFO level.
func (s *Service) Info(msg string, fields ...zap.Field) {
	s.log.Info(msg, fields...)
}

// Debug logs at DEBUG level.
func (s *Service) Debug(msg string, fields ...zap.Field) {
	s.log.Debug(msg, fields...)
}

// Warn logs at WARN level.
func (s *Service) Warn(msg string, fields ...zap.Field) {
	s.log.Warn(msg, fields...)
}

// Error logs at ERROR level and sends to Sentry.
func (s *Service) Error(msg string, fields ...zap.Field) {
	s.log.Error(msg, fields...)
	s.captureSentry(msg, sentry.LevelError)
}

// Fatal logs at FATAL level, sends to Sentry, and exits.
func (s *Service) Fatal(msg string, fields ...zap.Field) {
	s.captureSentry("[FATAL] "+msg, sentry.LevelFatal)
	s.flushSentry(config.SentryFlushTimeout)
	s.log.Fatal(msg, fields...)
}

// WithError returns a logger with error context for Sentry.
func (s *Service) WithError(err error) *Service {
	if s.sentryHub != nil {
		s.sentryHub.ConfigureScope(func(scope *sentry.Scope) {
			scope.SetTag("error", err.Error())
		})
	}
	return s
}

// With returns a logger with additional fields.
func (s *Service) With(fields ...zap.Field) *Service {
	return &Service{
		log:       s.log.With(fields...),
		file:      s.file,
		sentryHub: s.sentryHub,
	}
}

func (s *Service) captureSentry(message string, level sentry.Level) {
	if s.sentryHub != nil {
		event := &sentry.Event{
			Message: message,
			Level:   level,
		}
		s.sentryHub.CaptureEvent(event)
	}
}

func (s *Service) flushSentry(timeout time.Duration) {
	if s.sentryHub != nil {
		s.sentryHub.Flush(timeout)
	}
}
