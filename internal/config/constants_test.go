package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Поведенческое покрытие констант живёт в потребителях: ratelimiter
// (internal/ratelimiter, internal/bot/handlers_test.go), splitMessage
// (internal/bot/split_message_test.go, MaxTelegramMessageLen),
// SubscriptionResetDay (internal/service/sync_test.go, internal/xui),
// MaxResponseSize (internal/subserver, internal/vpn), MaxOpenConns
// (internal/database). Оставшиеся значения нигде поведенчески не проверяются —
// держим один тест с их смысловыми инвариантами вместо тавтологичных пинов.
func TestConstants_UncoveredInvariants(t *testing.T) {
	t.Parallel()

	// HTTP transport
	assert.Greater(t, DefaultHTTPTimeout, time.Duration(0), "DefaultHTTPTimeout must be positive")
	assert.Greater(t, DefaultIdleConnTimeout, time.Duration(0), "DefaultIdleConnTimeout must be positive")
	assert.Greater(t, MaxIdleConns, 0, "MaxIdleConns must be positive")

	// Rate limiting
	assert.Greater(t, RateLimiterMaxTokens, 0, "RateLimiterMaxTokens must be positive")
	assert.Greater(t, RateLimiterRefillRate, 0, "RateLimiterRefillRate must be positive")
	assert.Greater(t, MaxConcurrentHandlers, 0, "MaxConcurrentHandlers must be positive")

	// Database
	assert.NotEmpty(t, DefaultDatabasePath, "DefaultDatabasePath must be set")
	assert.Greater(t, ConnMaxLifetime, time.Duration(0))
	assert.Greater(t, ConnMaxIdleTime, time.Duration(0))
	assert.Greater(t, MaxIdleConnsDB, 0)

	// Heartbeat
	assert.GreaterOrEqual(t, DefaultHeartbeatInterval, MinHeartbeatInterval,
		"default heartbeat interval must satisfy its own minimum")

	// Health check port
	assert.Greater(t, DefaultWebServerPort, 0)
	assert.Less(t, DefaultWebServerPort, 65536)

	// Telegram limits
	assert.Greater(t, MaxCaptionLen, 0, "MaxCaptionLen must be positive")

	// Shutdown
	assert.Greater(t, ShutdownTimeout, time.Duration(0), "ShutdownTimeout must be positive")

	// Trial defaults must satisfy validate() bounds
	assert.GreaterOrEqual(t, DefaultTrialDurationHours, 1)
	assert.LessOrEqual(t, DefaultTrialDurationHours, 168)
	assert.GreaterOrEqual(t, DefaultTrialRateLimit, 1)
	assert.LessOrEqual(t, DefaultTrialRateLimit, 100)
	assert.NotEmpty(t, DefaultSiteURL, "DefaultSiteURL must be set")

	// Admin send rate limiting
	assert.Greater(t, AdminSendRateLimit, 0)
	assert.Greater(t, AdminSendRateWindow, time.Duration(0))
	assert.Greater(t, AdminSendMinInterval, time.Duration(0))

	// Feature flags preserving historical behaviour
	assert.True(t, DefaultDonateEnabled, "DefaultDonateEnabled should stay true")
	assert.True(t, DefaultNotificationsEnabled, "DefaultNotificationsEnabled should stay true")

	// Backup
	assert.GreaterOrEqual(t, DefaultBackupHour, 0, "backup hour must be a valid hour")
	assert.LessOrEqual(t, DefaultBackupHour, 23)
	assert.Greater(t, DefaultBackupRetention, 0, "backup retention must keep at least one backup")
}
