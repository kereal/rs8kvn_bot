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

	// Health check configuration
	HealthCheckPort int

	// Trial & Referral configuration
	SiteURL            string
	TrialDurationHours int
	TrialRateLimit     int
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
		XUIHost:          getEnv("XUI_HOST", "http://localhost:2053"),
		XUIUsername:      getEnv("XUI_USERNAME", ""),
		XUIPassword:      getEnv("XUI_PASSWORD", ""),
		XUISubPath:       getEnv("XUI_SUB_PATH", DefaultXUISubPath),
		DatabasePath:     getEnv("DATABASE_PATH", DefaultDatabasePath),
		LogFilePath:      getEnv("LOG_FILE_PATH", DefaultLogFilePath),
		LogLevel:         getEnv("LOG_LEVEL", DefaultLogLevel),
		HeartbeatURL:     getEnv("HEARTBEAT_URL", ""),
		SentryDSN:        getEnv("SENTRY_DSN", ""),
		SiteURL:          getEnv("SITE_URL", DefaultSiteURL),
	}

	var err error
	if cfg.TelegramAdminID, err = parseEnvInt64("TELEGRAM_ADMIN_ID", 0); err != nil {
		return nil, fmt.Errorf("invalid TELEGRAM_ADMIN_ID: %w", err)
	}
	if cfg.XUIInboundID, err = parseEnvInt("XUI_INBOUND_ID", 1); err != nil {
		return nil, fmt.Errorf("invalid XUI_INBOUND_ID: %w", err)
	}
	if cfg.TrafficLimitGB, err = parseEnvInt("TRAFFIC_LIMIT_GB", DefaultTrafficLimitGB); err != nil {
		return nil, fmt.Errorf("invalid TRAFFIC_LIMIT_GB: %w", err)
	}
	if cfg.HeartbeatInterval, err = parseEnvInt("HEARTBEAT_INTERVAL", DefaultHeartbeatInterval); err != nil {
		return nil, fmt.Errorf("invalid HEARTBEAT_INTERVAL: %w", err)
	}
	if cfg.HealthCheckPort, err = parseEnvInt("HEALTH_CHECK_PORT", DefaultHealthCheckPort); err != nil {
		return nil, fmt.Errorf("invalid HEALTH_CHECK_PORT: %w", err)
	}
	if cfg.TrialDurationHours, err = parseEnvInt("TRIAL_DURATION_HOURS", DefaultTrialDurationHours); err != nil {
		return nil, fmt.Errorf("invalid TRIAL_DURATION_HOURS: %w", err)
	}
	if cfg.TrialRateLimit, err = parseEnvInt("TRIAL_RATE_LIMIT", DefaultTrialRateLimit); err != nil {
		return nil, fmt.Errorf("invalid TRIAL_RATE_LIMIT: %w", err)
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

	// Health check port validation
	if c.HealthCheckPort < 1 || c.HealthCheckPort > 65535 {
		return fmt.Errorf("HEALTH_CHECK_PORT must be between 1 and 65535")
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

	// Site URL validation
	if err := c.validateURL("SITE_URL", c.SiteURL); err != nil {
		return err
	}

	// Trial duration validation
	if c.TrialDurationHours < 1 || c.TrialDurationHours > 168 {
		return fmt.Errorf("TRIAL_DURATION_HOURS must be between 1 and 168 (max 7 days)")
	}

	// Trial rate limit validation
	if c.TrialRateLimit < 1 || c.TrialRateLimit > 100 {
		return fmt.Errorf("TRIAL_RATE_LIMIT must be between 1 and 100")
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
// Returns an error if the variable is set but cannot be parsed as an integer.
func parseEnvInt(key string, defaultValue int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}

	intValue, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got: %q", key, value)
	}

	return intValue, nil
}

// parseEnvInt64 parses an environment variable as an int64.
// Returns the default value if the variable is not set or empty.
// Returns an error if the variable is set but cannot be parsed as an integer.
func parseEnvInt64(key string, defaultValue int64) (int64, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}

	intValue, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got: %q", key, value)
	}

	return intValue, nil
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
		"SentryDSN=***, "+
		"SupermemoryAPIKey=%s}",
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

func maskAPIKey(key string) string {
	if key == "" {
		return "(not set)"
	}
	return "***"
}
