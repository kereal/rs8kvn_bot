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
	// Log is the global sugared logger instance.
	// Deprecated: Use logger.Service for dependency injection.
	Log        *zap.SugaredLogger
	fileWriter *lumberjack.Logger
	logMu      sync.Mutex
	sentryHub  *sentry.Hub
)

// Init initializes the global logger with file and console output.
// Deprecated: Use NewService for dependency injection.
func Init(logFilePath, level string) error {
	logDir := filepath.Dir(logFilePath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
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
	fileWriter = &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    config.LogMaxSizeMB,
		MaxBackups: config.LogMaxBackups,
		MaxAge:     config.LogMaxAgeDays,
		Compress:   false, // Disable compression to save memory
	}
	cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(fileWriter), zapLevel))

	core := zapcore.NewTee(cores...)
	Log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()

	return nil
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
		Log.Warn(v...)
	}
}

func (l *tgbotapiLogger) Printf(format string, v ...interface{}) {
	if Log != nil {
		Log.Warnf(format, v...)
	}
}

// RedirectStdLog redirects standard Go log output to our zap logger.
// This ensures all log messages (including from third-party libraries)
// have consistent formatting.
func RedirectStdLog() {
	log.SetOutput(&stdLogWriter{})
	log.SetFlags(0)
	tgbotapi.SetLogger(&tgbotapiLogger{})
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

func Info(args ...interface{}) {
	if Log != nil {
		Log.Info(args...)
	}
}

func Infof(template string, args ...interface{}) {
	if Log != nil {
		Log.Infof(template, args...)
	}
}

func Error(args ...interface{}) {
	if Log != nil {
		Log.Error(args...)
	}
	captureToSentry(formatArgs(args...), "error")
}

func Errorf(template string, args ...interface{}) {
	if Log != nil {
		Log.Errorf(template, args...)
	}
	captureToSentry(fmt.Sprintf(template, args...), "error")
}

func Debug(args ...interface{}) {
	if Log != nil {
		Log.Debug(args...)
	}
}

func Debugf(template string, args ...interface{}) {
	if Log != nil {
		Log.Debugf(template, args...)
	}
}

func Warn(args ...interface{}) {
	if Log != nil {
		Log.Warn(args...)
	}
}

func Warnf(template string, args ...interface{}) {
	if Log != nil {
		Log.Warnf(template, args...)
	}
}

func Fatal(args ...interface{}) {
	captureToSentry("[FATAL] "+formatArgs(args...), "fatal")
	flushSentry(config.SentryFlushTimeout)
	if Log != nil {
		Log.Fatal(args...)
	}
}

func Fatalf(template string, args ...interface{}) {
	captureToSentry("[FATAL] "+fmt.Sprintf(template, args...), "fatal")
	flushSentry(config.SentryFlushTimeout)
	if Log != nil {
		Log.Fatalf(template, args...)
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

func formatArgs(args ...interface{}) string {
	return fmt.Sprint(args...)
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
	log       *zap.SugaredLogger
	file      *lumberjack.Logger
	sentryHub *sentry.Hub
}

// NewService creates a new logger service with file and console output.
func NewService(logFilePath, level string) (*Service, error) {
	logDir := filepath.Dir(logFilePath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
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
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()

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
func (s *Service) Info(args ...interface{}) {
	s.log.Info(args...)
}

// Infof logs at INFO level with format.
func (s *Service) Infof(template string, args ...interface{}) {
	s.log.Infof(template, args...)
}

// Debug logs at DEBUG level.
func (s *Service) Debug(args ...interface{}) {
	s.log.Debug(args...)
}

// Debugf logs at DEBUG level with format.
func (s *Service) Debugf(template string, args ...interface{}) {
	s.log.Debugf(template, args...)
}

// Warn logs at WARN level.
func (s *Service) Warn(args ...interface{}) {
	s.log.Warn(args...)
}

// Warnf logs at WARN level with format.
func (s *Service) Warnf(template string, args ...interface{}) {
	s.log.Warnf(template, args...)
}

// Error logs at ERROR level and sends to Sentry.
func (s *Service) Error(args ...interface{}) {
	s.log.Error(args...)
	s.captureSentry(formatArgs(args...), sentry.LevelError)
}

// Errorf logs at ERROR level with format and sends to Sentry.
func (s *Service) Errorf(template string, args ...interface{}) {
	s.log.Errorf(template, args...)
	s.captureSentry(fmt.Sprintf(template, args...), sentry.LevelError)
}

// Fatal logs at FATAL level, sends to Sentry, and exits.
func (s *Service) Fatal(args ...interface{}) {
	s.captureSentry("[FATAL] "+formatArgs(args...), sentry.LevelFatal)
	s.flushSentry(config.SentryFlushTimeout)
	s.log.Fatal(args...)
}

// Fatalf logs at FATAL level with format, sends to Sentry, and exits.
func (s *Service) Fatalf(template string, args ...interface{}) {
	s.captureSentry("[FATAL] "+fmt.Sprintf(template, args...), sentry.LevelFatal)
	s.flushSentry(config.SentryFlushTimeout)
	s.log.Fatalf(template, args...)
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

// WithField returns a logger with an additional field.
func (s *Service) WithField(key string, value interface{}) *Service {
	return &Service{
		log:       s.log.With(key, value),
		file:      s.file,
		sentryHub: s.sentryHub,
	}
}

// WithFields returns a logger with additional fields.
func (s *Service) WithFields(fields map[string]interface{}) *Service {
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return &Service{
		log:       s.log.With(args...),
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
