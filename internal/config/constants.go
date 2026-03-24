package config

import "time"

// HTTP Client Constants
const (
	// DefaultHTTPTimeout is the default timeout for HTTP requests
	DefaultHTTPTimeout = 10 * time.Second

	// DefaultIdleConnTimeout is the default timeout for idle connections
	DefaultIdleConnIdleTimeout = 30 * time.Second

	// MaxIdleConns is the maximum number of idle connections (optimized for low memory)
	MaxIdleConns = 2
)

// 3x-ui Panel Constants
var (
	// XUISessionValidity is how long a 3x-ui session remains valid
	XUISessionValidity = 15 * time.Minute

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
	DefaultTrafficLimitGB = 100

	// SubscriptionResetDay is the day of month for traffic reset (31 = last day)
	SubscriptionResetDay = 31
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

// Logging Constants
const (
	// DefaultLogFilePath is the default path for log files
	DefaultLogFilePath = "./data/bot.log"

	// DefaultLogLevel is the default log level
	DefaultLogLevel = "info"

	// LogMaxSizeMB is the maximum size of a log file in MB
	LogMaxSizeMB = 10

	// LogMaxBackups is the maximum number of old log files to retain
	LogMaxBackups = 2

	// LogMaxAgeDays is the maximum number of days to retain old log files
	LogMaxAgeDays = 14
)

// Sentry Constants
const (
	// SentryFlushTimeout is the timeout for flushing Sentry events
	SentryFlushTimeout = 5 * time.Second

	// SentryPanicFlushTimeout is the timeout for flushing Sentry during panic recovery
	SentryPanicFlushTimeout = 2 * time.Second

	// SentryTracesSampleRate is the sample rate for performance monitoring
	SentryTracesSampleRate = 0.1 // 10%
)

// Graceful Shutdown Constants
const (
	// ShutdownTimeout is the maximum time to wait for graceful shutdown
	ShutdownTimeout = 30 * time.Second
)

// Health Check Constants
const (
	// HealthCheckPort is the port for the health check HTTP server
	HealthCheckPort = 8080
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
