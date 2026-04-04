package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConstants_HTTPTimeouts(t *testing.T) {
	assert.Greater(t, DefaultHTTPTimeout, 0*time.Second, "DefaultHTTPTimeout should be positive")
	assert.Greater(t, DefaultIdleConnTimeout, 0*time.Second, "DefaultIdleConnTimeout should be positive")
	// Idle conn timeout can be greater than HTTP timeout (connection reuse strategy)
	assert.Equal(t, 30*time.Second, DefaultIdleConnTimeout, "DefaultIdleConnTimeout")
	assert.Equal(t, 10*time.Second, DefaultHTTPTimeout, "DefaultHTTPTimeout")
	assert.Equal(t, 2, MaxIdleConns, "MaxIdleConns should be 2 for low memory")
}

func TestConstants_XUISession(t *testing.T) {
	assert.Equal(t, 720, DefaultXUISessionMaxAgeMinutes, "DefaultXUISessionMaxAgeMinutes should be 720 (12h)")
	assert.Equal(t, 5*time.Second, XUISessionVerifyTimeout, "XUISessionVerifyTimeout")
	assert.Equal(t, 5*time.Second, XUILoginTimeout, "XUILoginTimeout")
}

func TestConstants_RateLimiter(t *testing.T) {
	assert.Equal(t, 30, RateLimiterMaxTokens, "RateLimiterMaxTokens")
	assert.Equal(t, 5, RateLimiterRefillRate, "RateLimiterRefillRate")
	assert.Equal(t, 100*time.Millisecond, RateLimiterPollInterval, "RateLimiterPollInterval")
	assert.Equal(t, 10, MaxConcurrentHandlers, "MaxConcurrentHandlers")
}

func TestConstants_TrafficLimits(t *testing.T) {
	assert.Greater(t, MinTrafficLimitGB, 0, "MinTrafficLimitGB")
	assert.Greater(t, MaxTrafficLimitGB, MinTrafficLimitGB, "MaxTrafficLimitGB > MinTrafficLimitGB")
	assert.Greater(t, DefaultTrafficLimitGB, MinTrafficLimitGB, "DefaultTrafficLimitGB > MinTrafficLimitGB")
	assert.LessOrEqual(t, DefaultTrafficLimitGB, MaxTrafficLimitGB, "DefaultTrafficLimitGB <= MaxTrafficLimitGB")
	assert.Equal(t, 30, SubscriptionResetDay, "SubscriptionResetDay")
}

func TestConstants_Database(t *testing.T) {
	assert.Equal(t, "./data/tgvpn.db", DefaultDatabasePath)
	assert.Equal(t, 5*time.Minute, ConnMaxLifetime)
	assert.Equal(t, 2*time.Minute, ConnMaxIdleTime)
	assert.Equal(t, 1, MaxOpenConns, "SQLite should use 1 connection")
	assert.Equal(t, 1, MaxIdleConnsDB)
}

func TestConstants_Heartbeat(t *testing.T) {
	assert.Equal(t, 300, DefaultHeartbeatInterval, "5 minutes")
	assert.GreaterOrEqual(t, DefaultHeartbeatInterval, MinHeartbeatInterval)
}

func TestConstants_HealthCheck(t *testing.T) {
	assert.Greater(t, DefaultHealthCheckPort, 0)
	assert.Less(t, DefaultHealthCheckPort, 65536)
}

func TestConstants_TelegramLimits(t *testing.T) {
	assert.Equal(t, 4096, MaxTelegramMessageLen)
	assert.Equal(t, 1024, MaxCaptionLen)
}

func TestConstants_Validation(t *testing.T) {
	assert.Equal(t, 1, MinInboundID)
	assert.Equal(t, 14, SubIDLengthBytes)
	assert.Equal(t, 1<<20, MaxResponseSize)
}

func TestConstants_CircuitBreaker(t *testing.T) {
	assert.Equal(t, 5, CircuitBreakerMaxFailures)
	assert.Equal(t, 30*time.Second, CircuitBreakerTimeout)
}

func TestConstants_Trial(t *testing.T) {
	assert.Equal(t, "https://vpn.site", DefaultSiteURL)
	assert.Equal(t, 3, DefaultTrialDurationHours)
	assert.Equal(t, 3, DefaultTrialRateLimit)
}

func TestConstants_AdminRateLimit(t *testing.T) {
	assert.Equal(t, 10, AdminSendRateLimit)
	assert.Equal(t, 1*time.Minute, AdminSendRateWindow)
	assert.Equal(t, 6*time.Second, AdminSendMinInterval)
}

func TestConstants_Donate(t *testing.T) {
	assert.Equal(t, "2200702156780864", DonateCardNumber)
	assert.Contains(t, DonateURL, "tbank.ru")
	assert.Equal(t, "kereal", ContactUsername)
}

func TestConstants_Backup(t *testing.T) {
	assert.Equal(t, 3, DefaultBackupHour)
	assert.Equal(t, 14, DefaultBackupRetention)
}

func TestConstants_Shutdown(t *testing.T) {
	assert.Equal(t, 30*time.Second, ShutdownTimeout)
}
