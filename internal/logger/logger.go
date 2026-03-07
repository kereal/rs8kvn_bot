package logger

import (
	"os"
	"path/filepath"
	"sync"
	"time"

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

	// Simple encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	encoder := zapcore.NewConsoleEncoder(encoderConfig)

	// Use both file (with rotation) and console
	var cores []zapcore.Core

	// Console core (always enabled for Docker)
	cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zapLevel))

	// File core with rotation - use package-level variable for proper cleanup
	fileWriter = &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    10,    // MB
		MaxBackups: 3,     // files
		MaxAge:     30,    // days
		Compress:   false, // disabled for simplicity
	}
	cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(fileWriter), zapLevel))

	// Multi-core
	core := zapcore.NewTee(cores...)

	Log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()

	return nil
}

func Info(args ...interface{}) {
	Log.Info(args...)
}

func Infof(template string, args ...interface{}) {
	Log.Infof(template, args...)
}

func Error(args ...interface{}) {
	Log.Error(args...)
}

func Errorf(template string, args ...interface{}) {
	Log.Errorf(template, args...)
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
	Log.Fatal(args...)
}

func Fatalf(template string, args ...interface{}) {
	Log.Fatalf(template, args...)
}

func Sync() error {
	if Log != nil {
		return Log.Sync()
	}
	return nil
}

// Close closes the log file and syncs buffers
func Close() error {
	logMu.Lock()
	defer logMu.Unlock()

	if Log != nil {
		if err := Log.Sync(); err != nil {
			return err
		}
	}

	// Close the lumberjack file writer to release file handles
	if fileWriter != nil {
		if err := fileWriter.Close(); err != nil {
			return err
		}
	}

	return nil
}

// GetRotationTime returns the next rotation time (daily)
func GetRotationTime() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
}
