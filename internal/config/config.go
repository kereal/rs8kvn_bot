package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the application.
// All fields are validated before use.
type Config struct {
	// Telegram configuration
	TelegramBotToken string
	TelegramAdminID  int64

	// 3x-ui panel configuration
	XUIHost      string
	XUIUsername  string
	XUIPassword  string
	XUIInboundID int
	XUISubPath   string

	// Database configuration
	DatabasePath string

	// Logging configuration
	LogFilePath string
	LogLevel    string

	// Subscription configuration
	TrafficLimitGB int

	// Monitoring configuration
	HeartbeatURL      string
	HeartbeatInterval int

	// Error tracking
	SentryDSN string
}

// Load reads configuration from environment variables and validates it.
// Returns an error if any required field is missing or invalid.
func Load() (*Config, error) {
	// Load .env file if it exists, but don't fail if it doesn't
	// This allows the application to work with just environment variables
	if err := godotenv.Load(); err != nil {
		// .env file is optional, ignore "not found" errors
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load .env file: %w", err)
		}
	}

	cfg := &Config{
		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramAdminID:  parseEnvInt64("TELEGRAM_ADMIN_ID", 0),

		XUIHost:      getEnv("XUI_HOST", "http://localhost:2053"),
		XUIUsername:  getEnv("XUI_USERNAME", "admin"),
		XUIPassword:  getEnv("XUI_PASSWORD", "admin"),
		XUIInboundID: parseEnvInt("XUI_INBOUND_ID", 1),
		XUISubPath:   getEnv("XUI_SUB_PATH", DefaultXUISubPath),

		DatabasePath: getEnv("DATABASE_PATH", DefaultDatabasePath),

		LogFilePath: getEnv("LOG_FILE_PATH", DefaultLogFilePath),
		LogLevel:    getEnv("LOG_LEVEL", DefaultLogLevel),

		TrafficLimitGB: parseEnvInt("TRAFFIC_LIMIT_GB", DefaultTrafficLimitGB),

		HeartbeatURL:      getEnv("HEARTBEAT_URL", ""),
		HeartbeatInterval: parseEnvInt("HEARTBEAT_INTERVAL", DefaultHeartbeatInterval),

		SentryDSN: getEnv("SENTRY_DSN", ""),
	}

	// Validate all required fields
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// validate checks that all configuration values are valid.
func (c *Config) validate() error {
	// Telegram validation
	if c.TelegramBotToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if !strings.Contains(c.TelegramBotToken, ":") {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN appears to be invalid (expected format: 'number:token')")
	}

	if c.TelegramAdminID < 0 {
		return fmt.Errorf("TELEGRAM_ADMIN_ID must be non-negative")
	}

	// 3x-ui validation
	if err := c.validateURL("XUI_HOST", c.XUIHost); err != nil {
		return err
	}

	if c.XUIUsername == "" {
		return fmt.Errorf("XUI_USERNAME is required")
	}

	if c.XUIPassword == "" {
		return fmt.Errorf("XUI_PASSWORD is required")
	}

	if c.XUIInboundID < MinInboundID {
		return fmt.Errorf("XUI_INBOUND_ID must be at least %d", MinInboundID)
	}

	if c.XUISubPath == "" {
		return fmt.Errorf("XUI_SUB_PATH cannot be empty")
	}

	// Traffic limit validation
	if c.TrafficLimitGB < MinTrafficLimitGB || c.TrafficLimitGB > MaxTrafficLimitGB {
		return fmt.Errorf("TRAFFIC_LIMIT_GB must be between %d and %d", MinTrafficLimitGB, MaxTrafficLimitGB)
	}

	// Heartbeat validation
	if c.HeartbeatInterval < MinHeartbeatInterval {
		return fmt.Errorf("HEARTBEAT_INTERVAL must be at least %d seconds", MinHeartbeatInterval)
	}

	// Log level validation
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		return fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error")
	}

	// Sentry DSN validation (optional but if provided, should be valid URL)
	if c.SentryDSN != "" {
		if err := c.validateURL("SENTRY_DSN", c.SentryDSN); err != nil {
			return err
		}
	}

	// Heartbeat URL validation (optional but if provided, should be valid URL)
	if c.HeartbeatURL != "" {
		if err := c.validateURL("HEARTBEAT_URL", c.HeartbeatURL); err != nil {
			return err
		}
	}

	return nil
}

// validateURL checks if a URL string is valid.
func (c *Config) validateURL(name, value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", name, err)
	}

	// Check that scheme is present
	if u.Scheme == "" {
		return fmt.Errorf("%s must include a scheme (http:// or https://)", name)
	}

	// Check that host is present
	if u.Host == "" {
		return fmt.Errorf("%s must include a host", name)
	}

	return nil
}

// getEnv returns the value of an environment variable or a default value.
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return strings.TrimSpace(value)
}

// parseEnvInt parses an environment variable as an integer.
// Returns the default value if the variable is not set or empty.
func parseEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	intValue, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return defaultValue
	}

	return intValue
}

// parseEnvInt64 parses an environment variable as an int64.
// Returns the default value if the variable is not set or empty.
func parseEnvInt64(key string, defaultValue int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	intValue, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return defaultValue
	}

	return intValue
}

// String returns a safe string representation of the config (without sensitive data).
func (c *Config) String() string {
	return fmt.Sprintf("Config{"+
		"TelegramBotToken=***, "+
		"TelegramAdminID=%d, "+
		"XUIHost=%s, "+
		"XUIUsername=%s, "+
		"XUIPassword=***, "+
		"XUIInboundID=%d, "+
		"XUISubPath=%s, "+
		"DatabasePath=%s, "+
		"LogFilePath=%s, "+
		"LogLevel=%s, "+
		"TrafficLimitGB=%d, "+
		"HeartbeatURL=%s, "+
		"HeartbeatInterval=%d, "+
		"SentryDSN=***}",
		c.TelegramAdminID,
		c.XUIHost,
		c.XUIUsername,
		c.XUIInboundID,
		c.XUISubPath,
		c.DatabasePath,
		c.LogFilePath,
		c.LogLevel,
		c.TrafficLimitGB,
		maskURL(c.HeartbeatURL),
		c.HeartbeatInterval,
	)
}

// maskURL returns a masked version of a URL for logging purposes.
func maskURL(urlStr string) string {
	if urlStr == "" {
		return "(not set)"
	}

	u, err := url.Parse(urlStr)
	if err != nil {
		return "***"
	}

	// Show scheme and host only
	return fmt.Sprintf("%s://%s/***", u.Scheme, u.Host)
}
