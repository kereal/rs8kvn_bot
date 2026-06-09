package config

import (
	"fmt"
	"net/url"
	"strings"

	flag "github.com/kereal/rs8kvn_bot/internal/flag"
)

// Source represents a configured 3x-ui panel source.
type Source struct {
	ID           uint
	Name         string
	Active       bool
	Trial        bool
	XUIHost      string
	XUIAPIToken  string
	XUIInboundID int
	SubURL       string
}

// Config holds all configuration for the application.
// All fields are validated before use.
type Config struct {
	// Telegram configuration
	TelegramBotToken string
	TelegramAdminID  int64

	// Database configuration
	DatabasePath string

	// Logging configuration
	LogFilePath string
	LogLevel    string

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

	// Contact configuration
	ContactUsername string

	// Donation configuration
	DonateCardNumber string
	DonateURL        string

	// Subscription server configuration
	GlobalSubURL string

	// Sources configuration
	Sources []Source

	// API configuration
	APIToken string

	// Proxy Manager webhook configuration
	ProxyManagerWebhookSecret string
	ProxyManagerWebhookURL    string
}

// configFlags holds typed flag values for config fields.
type configFlags struct {
	telegramBotToken          *flag.StringValue
	telegramAdminID           *flag.Int64Value
	databasePath              *flag.StringValue
	logFilePath               *flag.StringValue
	logLevel                  *flag.StringValue
	heartbeatURL              *flag.StringValue
	heartbeatInterval         *flag.IntValue
	sentryDSN                 *flag.StringValue
	healthCheckPort           *flag.IntValue
	siteURL                   *flag.StringValue
	trialDurationHours        *flag.IntValue
	trialRateLimit            *flag.IntValue
	contactUsername           *flag.StringValue
	donateCardNumber          *flag.StringValue
	donateURL                 *flag.StringValue
	globalSubURL              *flag.StringValue
	apiToken                  *flag.StringValue
	proxyManagerWebhookSecret *flag.StringValue
	proxyManagerWebhookURL    *flag.StringValue
}

// registerFlags creates a new flag.Registry and initializes a configFlags instance with defaults,
// registering each configuration entry under its corresponding environment variable name.
// It returns the registry and the populated configFlags.
func registerFlags() (*flag.Registry, *configFlags) {
	r := flag.New()

	f := &configFlags{
		telegramBotToken:          flag.NewString(""),
		telegramAdminID:           flag.NewInt64(0),
		databasePath:              flag.NewString(DefaultDatabasePath),
		logFilePath:               flag.NewString(DefaultLogFilePath),
		logLevel:                  flag.NewString(DefaultLogLevel),
		heartbeatURL:              flag.NewString(""),
		heartbeatInterval:         flag.NewInt(DefaultHeartbeatInterval),
		sentryDSN:                 flag.NewString(""),
		healthCheckPort:           flag.NewInt(DefaultHealthCheckPort),
		siteURL:                   flag.NewString(DefaultSiteURL),
		trialDurationHours:        flag.NewInt(DefaultTrialDurationHours),
		trialRateLimit:            flag.NewInt(DefaultTrialRateLimit),
		contactUsername:           flag.NewString(ContactUsername),
		donateCardNumber:          flag.NewString(DonateCardNumber),
		donateURL:                 flag.NewString(DonateURL),
		globalSubURL:              flag.NewString(""),
		apiToken:                  flag.NewString(""),
		proxyManagerWebhookSecret: flag.NewString(""),
		proxyManagerWebhookURL:    flag.NewString(""),
	}

	r.Register("TELEGRAM_BOT_TOKEN", f.telegramBotToken)
	r.Register("TELEGRAM_ADMIN_ID", f.telegramAdminID)
	r.Register("GLOBAL_SUB_URL", f.globalSubURL)
	r.Register("DATABASE_PATH", f.databasePath)
	r.Register("LOG_FILE_PATH", f.logFilePath)
	r.Register("LOG_LEVEL", f.logLevel)
	r.Register("HEARTBEAT_URL", f.heartbeatURL)
	r.Register("HEARTBEAT_INTERVAL", f.heartbeatInterval)
	r.Register("SENTRY_DSN", f.sentryDSN)
	r.Register("HEALTH_CHECK_PORT", f.healthCheckPort)
	r.Register("SITE_URL", f.siteURL)
	r.Register("TRIAL_DURATION_HOURS", f.trialDurationHours)
	r.Register("TRIAL_RATE_LIMIT", f.trialRateLimit)
	r.Register("CONTACT_USERNAME", f.contactUsername)
	r.Register("DONATE_CARD_NUMBER", f.donateCardNumber)
	r.Register("DONATE_URL", f.donateURL)
	r.Register("API_TOKEN", f.apiToken)
	r.Register("PROXY_MANAGER_WEBHOOK_SECRET", f.proxyManagerWebhookSecret)
	r.Register("PROXY_MANAGER_WEBHOOK_URL", f.proxyManagerWebhookURL)

	return r, f
}

// Load reads configuration from environment variables and validates it.
// Load loads configuration from environment variables, constructs a Config from the parsed flag values, and validates it.
// It returns the validated Config on success or an error if environment loading or validation fails.
func Load() (*Config, error) {
	r, f := registerFlags()

	if err := r.LoadEnv(); err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	cfg := &Config{
		TelegramBotToken:          f.telegramBotToken.Get(),
		TelegramAdminID:           f.telegramAdminID.Get(),
		DatabasePath:              f.databasePath.Get(),
		LogFilePath:               f.logFilePath.Get(),
		LogLevel:                  f.logLevel.Get(),
		HeartbeatURL:              f.heartbeatURL.Get(),
		HeartbeatInterval:         f.heartbeatInterval.Get(),
		SentryDSN:                 f.sentryDSN.Get(),
		HealthCheckPort:           f.healthCheckPort.Get(),
		SiteURL:                   f.siteURL.Get(),
		TrialDurationHours:        f.trialDurationHours.Get(),
		TrialRateLimit:            f.trialRateLimit.Get(),
		ContactUsername:           f.contactUsername.Get(),
		DonateCardNumber:          f.donateCardNumber.Get(),
		DonateURL:                 f.donateURL.Get(),
		GlobalSubURL:              f.globalSubURL.Get(),
		APIToken:                  f.apiToken.Get(),
		ProxyManagerWebhookSecret: f.proxyManagerWebhookSecret.Get(),
		ProxyManagerWebhookURL:    f.proxyManagerWebhookURL.Get(),
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

	if c.TelegramAdminID <= 0 {
		return fmt.Errorf("TELEGRAM_ADMIN_ID must be positive")
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

	// Global subscription URL validation (required)
	if c.GlobalSubURL == "" {
		return fmt.Errorf("GLOBAL_SUB_URL is required")
	}
	if err := c.validateURL("GLOBAL_SUB_URL", c.GlobalSubURL); err != nil {
		return err
	}
	c.GlobalSubURL = strings.TrimRight(c.GlobalSubURL, "/") + "/"

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

	// Sources validation (validate individual source fields if sources are configured)
	for i, src := range c.Sources {
		if src.XUIHost == "" {
			return fmt.Errorf("source %d: XUI_HOST is required", i)
		}
		if src.XUIAPIToken == "" {
			return fmt.Errorf("source %d: XUI_API_TOKEN is required", i)
		}
		if src.XUIInboundID <= 0 {
			return fmt.Errorf("source %d: XUI_INBOUND_ID must be positive", i)
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

// String returns a safe string representation of the config (without sensitive data).
func (c *Config) String() string {
	return fmt.Sprintf("Config{"+
		"TelegramBotToken=***, "+
		"TelegramAdminID=%d, "+
		"DatabasePath=%s, "+
		"LogFilePath=%s, "+
		"LogLevel=%s, "+
		"HeartbeatURL=%s, "+
		"HeartbeatInterval=%d, "+
		"GlobalSubURL=%s, "+
		"SentryDSN=***, "+
		"}",
		c.TelegramAdminID,
		c.DatabasePath,
		c.LogFilePath,
		c.LogLevel,
		maskURL(c.HeartbeatURL),
		c.HeartbeatInterval,
		maskURL(c.GlobalSubURL),
	)
}

// SubURL builds a full subscription URL from a subscription ID.
func (c *Config) SubURL(subID string) string {
	u, err := url.JoinPath(c.GlobalSubURL, subID)
	if err != nil {
		return c.GlobalSubURL + subID
	}
	return u
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
