package config

import "time"

// HTTP Client Constants
const (
	// DefaultHTTPTimeout is the default timeout for HTTP requests
	DefaultHTTPTimeout = 10 * time.Second

	// DefaultIdleConnTimeout is the default timeout for idle connections
	DefaultIdleConnTimeout = 30 * time.Second

	// MaxIdleConns is the maximum number of idle connections (optimized for low memory)
	MaxIdleConns = 2
)

// 3x-ui Panel Constants
const (
	// DefaultXUISessionMaxAgeMinutes is the default session lifetime in minutes (12 hours).
	// Must match the panel's sessionMaxAge setting.
	DefaultXUISessionMaxAgeMinutes = 720

	// XUISessionVerifyTimeout is the timeout for verifying session validity.
	XUISessionVerifyTimeout = 5 * time.Second

	// XUILoginTimeout is the timeout for login requests.
	XUILoginTimeout = 5 * time.Second
)

// XUI Retry Settings (var for test override)
var (
	// XUIMaxRetries is the maximum number of retries for 3x-ui API calls
	XUIMaxRetries = 3

	// XUIInitialRetryDelay is the initial delay between retries
	XUIInitialRetryDelay = 2 * time.Second
)

// Telegram Bot Constants
const (
	// BotUpdateTimeout is the timeout for long polling updates from Telegram
	BotUpdateTimeout = 60

	// RateLimiterMaxTokens is the maximum number of tokens in the rate limiter
	RateLimiterMaxTokens = 30

	// RateLimiterRefillRate is the rate at which tokens are refilled (tokens per second)
	RateLimiterRefillRate = 5

	// RateLimiterPollInterval is the interval for checking token availability
	RateLimiterPollInterval = 100 * time.Millisecond

	// MaxConcurrentHandlers is the maximum number of concurrent update handlers
	// This prevents unbounded goroutine spawning under load
	MaxConcurrentHandlers = 10
)

// Database Constants
const (
	// DefaultDatabasePath is the default path to the SQLite database
	DefaultDatabasePath = "./data/tgvpn.db"

	// ConnMaxLifetime is the maximum time a connection can be reused
	ConnMaxLifetime = 5 * time.Minute

	// ConnMaxIdleTime is the maximum time a connection can be idle
	ConnMaxIdleTime = 2 * time.Minute

	// MaxOpenConns is the maximum number of open connections (1 for SQLite)
	MaxOpenConns = 1

	// MaxIdleConnsDB is the maximum number of idle connections
	MaxIdleConnsDB = 1
)

// Subscription Constants
const (
	// MinTrafficLimitGB is the minimum traffic limit in GB
	MinTrafficLimitGB = 1

	// MaxTrafficLimitGB is the maximum traffic limit in GB
	MaxTrafficLimitGB = 1000

	// DefaultTrafficLimitGB is the default traffic limit in GB
	DefaultTrafficLimitGB = 30

	// SubscriptionResetDay is the interval in days for automatic traffic reset.
	// When combined with ExpiryTime > 0, traffic resets every N days and expiry extends.
	// Example: reset=30 means traffic resets every 30 days from creation date.
	// IMPORTANT: Auto-reset only works when ExpiryTime is set (not zero).
	// Source: https://github.com/mhsanaei/3x-ui/blob/main/web/service/inbound.go#L888-L912
	SubscriptionResetDay = 30
)

// Backup Constants
const (
	// DefaultBackupHour is the hour when daily backup runs (3 AM)
	DefaultBackupHour = 3

	// DefaultBackupRetention is the number of days to keep backups
	DefaultBackupRetention = 14
)

// Heartbeat Constants
const (
	// DefaultHeartbeatInterval is the default interval between heartbeat requests in seconds
	DefaultHeartbeatInterval = 300 // 5 minutes

	// MinHeartbeatInterval is the minimum allowed heartbeat interval in seconds
	MinHeartbeatInterval = 10
)

// Database Pool Statistics Constants
const (
	// PoolStatsLogInterval is the interval for logging database pool statistics
	PoolStatsLogInterval = 5 * time.Minute
)

// Logging Constants
// NOTE: LogMaxSizeMB, LogMaxBackups, LogMaxAgeDays have been moved to the
// logger package. Sentry constants have also been moved to logger.
const (
	// DefaultLogFilePath is the default path for log files
	DefaultLogFilePath = "./data/bot.log"

	// DefaultLogLevel is the default log level
	DefaultLogLevel = "info"
)

// Graceful Shutdown Constants
const (
	// ShutdownTimeout is the maximum time to wait for graceful shutdown
	ShutdownTimeout = 30 * time.Second
)

// Health Check Constants
const (
	// DefaultHealthCheckPort is the default port for the health check HTTP server
	DefaultHealthCheckPort = 8880
)

// 3x-ui Subscription Path Constants
const (
	// DefaultXUISubPath is the default subscription path segment
	DefaultXUISubPath = "sub"
)

// Validation Constants
const (
	// MinInboundID is the minimum valid inbound ID
	MinInboundID = 1

	// SubIDLengthBytes is the number of random bytes for subscription ID (28 hex chars)
	SubIDLengthBytes = 14
)

// MaxResponseSize is the maximum response size to read (1MB)
const MaxResponseSize = 1 << 20

const (
	CircuitBreakerMaxFailures = 5
	CircuitBreakerTimeout     = 30 * time.Second
)

// Trial & Referral Constants
const (
	DefaultSiteURL            = "https://vpn.site"
	DefaultTrialDurationHours = 3
	DefaultTrialRateLimit     = 3
)

// Telegram Limits
const (
	MaxTelegramMessageLen = 4096
	MaxCaptionLen         = 1024
)

// Admin Rate Limiting
const (
	AdminSendRateLimit   = 10              // Max messages per window
	AdminSendRateWindow  = 1 * time.Minute // Time window
	AdminSendMinInterval = 6 * time.Second // Min interval between messages
)

// Donate Constants
const (
	// DonateCardNumber is the card number for donations (T-Bank)
	DonateCardNumber = "REDACTED_CARD_NUMBER"

	// DonateURL is the T-Bank collection link for donations
	DonateURL = "REDACTED_DONATE_URL"

	// ContactUsername is the Telegram username for contact/support
	ContactUsername = "kereal"
)
