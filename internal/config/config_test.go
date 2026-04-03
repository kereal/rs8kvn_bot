package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_DefaultValues(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	require.NoError(t, err, "Load() error")

	assert.Equal(t, "123456789:ABCdefGHIjklMNOpqrsTUVwxyz", cfg.TelegramBotToken, "TelegramBotToken")
	assert.Equal(t, int64(123456), cfg.TelegramAdminID, "TelegramAdminID")
}

func TestLoad_CustomValues(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "999999")
	os.Setenv("XUI_HOST", "http://custom:2053")
	os.Setenv("XUI_USERNAME", "customuser")
	os.Setenv("XUI_PASSWORD", "custompass")
	os.Setenv("XUI_INBOUND_ID", "5")
	os.Setenv("TRAFFIC_LIMIT_GB", "500")
	os.Setenv("HEARTBEAT_INTERVAL", "120")
	os.Setenv("DATABASE_PATH", "/custom/path/db.db")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("HEALTH_CHECK_PORT", "9090")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRAFFIC_LIMIT_GB")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Unsetenv("DATABASE_PATH")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("HEALTH_CHECK_PORT")
	}()

	cfg, err := Load()
	require.NoError(t, err, "Load() error")

	assert.Equal(t, 500, cfg.TrafficLimitGB, "TrafficLimitGB")
	assert.Equal(t, 120, cfg.HeartbeatInterval, "HeartbeatInterval")
	assert.Equal(t, 9090, cfg.HealthCheckPort, "HealthCheckPort")
}

func TestLoad_MissingBotToken(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error when BOT_TOKEN is empty")
}

func TestLoad_InvalidTelegramAdminID(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "invalid")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	require.Error(t, err, "Load() should error for invalid TELEGRAM_ADMIN_ID")
	assert.Contains(t, err.Error(), "TELEGRAM_ADMIN_ID", "Error should mention TELEGRAM_ADMIN_ID")
}

func TestLoad_InvalidXUIInboundID(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "invalid")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	require.Error(t, err, "Load() should error for invalid XUI_INBOUND_ID")
	assert.Contains(t, err.Error(), "XUI_INBOUND_ID", "Error should mention XUI_INBOUND_ID")
}

func TestLoad_InvalidTrafficLimitGB_TooLow(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRAFFIC_LIMIT_GB", "0")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRAFFIC_LIMIT_GB")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for TRAFFIC_LIMIT_GB = 0")
}

func TestLoad_InvalidTrafficLimitGB_TooHigh(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRAFFIC_LIMIT_GB", "10001")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRAFFIC_LIMIT_GB")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for TRAFFIC_LIMIT_GB > 10000")
}

func TestLoad_InvalidHeartbeatInterval_Zero(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("HEARTBEAT_INTERVAL", "0")
	os.Setenv("HEARTBEAT_URL", "http://example.com/heartbeat")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Unsetenv("HEARTBEAT_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for HEARTBEAT_INTERVAL=0")
}

func TestLoad_ValidHeartbeatInterval_MinValue(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("HEARTBEAT_INTERVAL", "60")
	os.Setenv("HEARTBEAT_URL", "http://example.com/heartbeat")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Unsetenv("HEARTBEAT_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 60, cfg.HeartbeatInterval)
}

func TestLoad_InvalidHealthCheckPort_Zero(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("HEALTH_CHECK_PORT", "0")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("HEARTBEAT_PORT")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for HEALTH_CHECK_PORT=0")
}

func TestLoad_InvalidHealthCheckPort_Max(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("HEALTH_CHECK_PORT", "65536")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("HEALTH_CHECK_PORT")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for HEALTH_CHECK_PORT=65536 (max is 65535)")
}

func TestLoad_ValidHealthCheckPort_Max(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("HEALTH_CHECK_PORT", "65535")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("HEALTH_CHECK_PORT")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 65535, cfg.HealthCheckPort)
}

func TestLoad_ValidAdminID_Zero(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "0")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	assert.NoError(t, err, "Load() should allow ADMIN_ID=0")
	assert.Equal(t, int64(0), cfg.TelegramAdminID)
}

func TestLoad_InvalidTrialDurationHours_Zero(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRIAL_DURATION_HOURS", "0")
	os.Setenv("SITE_URL", "https://example.com")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRIAL_DURATION_HOURS")
		os.Unsetenv("SITE_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for TRIAL_DURATION_HOURS=0")
}

func TestLoad_ValidTrialDurationHours_Min(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRIAL_DURATION_HOURS", "1")
	os.Setenv("SITE_URL", "https://example.com")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRIAL_DURATION_HOURS")
		os.Unsetenv("SITE_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 1, cfg.TrialDurationHours)
}

func TestLoad_ValidTrialDurationHours_Max(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRIAL_DURATION_HOURS", "168")
	os.Setenv("SITE_URL", "https://example.com")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRIAL_DURATION_HOURS")
		os.Unsetenv("SITE_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 168, cfg.TrialDurationHours)
}

func TestLoad_InvalidTrialDurationHours_OverMax(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRIAL_DURATION_HOURS", "169")
	os.Setenv("SITE_URL", "https://example.com")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRIAL_DURATION_HOURS")
		os.Unsetenv("SITE_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for TRIAL_DURATION_HOURS=169 (max is 168)")
}

func TestLoad_InvalidTrialRateLimit_Zero(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRIAL_RATE_LIMIT", "0")
	os.Setenv("SITE_URL", "https://example.com")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRIAL_RATE_LIMIT")
		os.Unsetenv("SITE_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for TRIAL_RATE_LIMIT=0")
}

func TestLoad_ValidTrialRateLimit_Min(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRIAL_RATE_LIMIT", "1")
	os.Setenv("SITE_URL", "https://example.com")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRIAL_RATE_LIMIT")
		os.Unsetenv("SITE_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 1, cfg.TrialRateLimit)
}

func TestLoad_ValidTrialRateLimit_Max(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("TRIAL_RATE_LIMIT", "100")
	os.Setenv("SITE_URL", "https://example.com")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("TRIAL_RATE_LIMIT")
		os.Unsetenv("SITE_URL")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 100, cfg.TrialRateLimit)
}

func TestLoad_InvalidXUIInboundID_Negative(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "-1")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error for negative XUI_INBOUND_ID")
}

func TestLoad_InvalidHeartbeatInterval(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("HEARTBEAT_INTERVAL", "invalid")
	os.Setenv("LOG_LEVEL", "info")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	require.Error(t, err, "Load() should error for invalid HEARTBEAT_INTERVAL")
	assert.Contains(t, err.Error(), "HEARTBEAT_INTERVAL", "Error should mention HEARTBEAT_INTERVAL")
}

func TestValidateURL(t *testing.T) {
	cfg := &Config{}

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid https", "https://example.com", false},
		{"valid http", "http://localhost:8080", false},
		{"invalid no scheme", "example.com", true},
		{"invalid empty", "", true},
		{"no host", "http://", true},
		{"no scheme or host", "localhost:8080", true},
		{"scheme without host", "http://localhost", false},
		{"with path", "https://example.com/path/to/resource", false},
		{"with query", "https://example.com?query=value", false},
		{"with port and path", "http://localhost:8080/api/v1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.validateURL("test", tt.url)
			if tt.wantErr {
				assert.Error(t, err, "validateURL() should error")
			} else {
				assert.NoError(t, err, "validateURL() should not error")
			}
		})
	}
}

func TestConfig_String(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  123456,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "admin",
		XUIPassword:      "secret",
		XUIInboundID:     1,
		TrafficLimitGB:   100,
	}

	str := cfg.String()

	assert.Contains(t, str, "TelegramBotToken", "String() should include TelegramBotToken")
	assert.Contains(t, str, "123456", "String() should include TelegramAdminID")
	assert.NotContains(t, str, "secret", "String() should mask password")
}

func TestMaskURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"http", "http://example.com/path", "http://example.com/***"},
		{"https", "https://secure.example.com:8443", "https://secure.example.com:8443/***"},
		{"with port", "http://localhost:2053/xui", "http://localhost:2053/***"},
		{"empty", "", "(not set)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskURL(tt.url)
			assert.Equal(t, tt.expected, result, "maskURL()")
		})
	}
}

func TestMaskURL_InvalidURL(t *testing.T) {
	result := maskURL("not a valid url")
	assert.Equal(t, ":///***", result, "maskURL() for invalid URL")
}

func TestConfig_Validate_EmptyXUIHost(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  123456,
		XUIHost:          "",
		XUIUsername:      "admin",
		XUIPassword:      "password",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on empty XUIHost")
}

func TestConfig_Validate_EmptyXUIUsername(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  123456,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "",
		XUIPassword:      "password",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on empty XUIUsername")
}

func TestConfig_Validate_EmptyXUIPassword(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  123456,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "admin",
		XUIPassword:      "",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on empty XUIPassword")
}

func TestConfig_Validate_InvalidAdminID_Zero(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  0,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "admin",
		XUIPassword:      "password",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on zero AdminID")
}

func TestConfig_Validate_InvalidInboundID_Zero(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  123456,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "admin",
		XUIPassword:      "password",
		XUIInboundID:     0,
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on zero InboundID")
}

func TestConfig_Validate_Valid(t *testing.T) {
	cfg := &Config{
		TelegramBotToken:   "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:    123456,
		XUIHost:            "http://localhost:2053",
		XUIUsername:        "admin",
		XUIPassword:        "password",
		XUIInboundID:       1,
		XUISubPath:         "xui",
		TrafficLimitGB:     100,
		HeartbeatInterval:  60,
		LogLevel:           "info",
		HealthCheckPort:    DefaultHealthCheckPort,
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	err := cfg.validate()
	assert.NoError(t, err, "validate() should not error for valid config")
}

func TestConfig_Validate_SentryDSN_Valid(t *testing.T) {
	cfg := &Config{
		TelegramBotToken:   "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:    123456,
		XUIHost:            "http://localhost:2053",
		XUIUsername:        "admin",
		XUIPassword:        "password",
		XUIInboundID:       1,
		XUISubPath:         "xui",
		TrafficLimitGB:     100,
		HeartbeatInterval:  60,
		LogLevel:           "info",
		SentryDSN:          "https://abc@sentry.io/123",
		HealthCheckPort:    DefaultHealthCheckPort,
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	err := cfg.validate()
	assert.NoError(t, err, "validate() should not error for valid SentryDSN")
}

func TestConfig_Validate_SentryDSN_Invalid(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  123456,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "admin",
		XUIPassword:      "password",
		XUIInboundID:     1,
		XUISubPath:       "xui",
		SentryDSN:        "invalid-dsn",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on invalid SentryDSN")
}

func TestConfig_Validate_WithSubPath(t *testing.T) {
	cfg := &Config{
		TelegramBotToken:   "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:    123456,
		XUIHost:            "http://localhost:2053",
		XUIUsername:        "admin",
		XUIPassword:        "password",
		XUIInboundID:       1,
		XUISubPath:         "custom",
		TrafficLimitGB:     100,
		HeartbeatInterval:  60,
		LogLevel:           "info",
		HealthCheckPort:    DefaultHealthCheckPort,
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	err := cfg.validate()
	assert.NoError(t, err, "validate() should not error with SubPath")
}

func TestConfig_Validate_WithHeartbeatURL(t *testing.T) {
	cfg := &Config{
		TelegramBotToken:   "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:    123456,
		XUIHost:            "http://localhost:2053",
		XUIUsername:        "admin",
		XUIPassword:        "password",
		XUIInboundID:       1,
		XUISubPath:         "xui",
		TrafficLimitGB:     100,
		HeartbeatInterval:  60,
		LogLevel:           "info",
		HeartbeatURL:       "https://health.example.com",
		HealthCheckPort:    DefaultHealthCheckPort,
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
	}

	err := cfg.validate()
	assert.NoError(t, err, "validate() should not error with HeartbeatURL")
}

func TestConfig_Validate_InvalidTokenFormat(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "invalid-token-without-colon",
		TelegramAdminID:  123456,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "admin",
		XUIPassword:      "password",
		XUIInboundID:     1,
		XUISubPath:       "/xui",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on invalid token format")
}

func TestConfig_Validate_NegativeAdminID(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  -1,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "admin",
		XUIPassword:      "password",
		XUIInboundID:     1,
		XUISubPath:       "/xui",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on negative AdminID")
}

func TestConfig_Validate_InvalidLogLevel(t *testing.T) {
	cfg := &Config{
		TelegramBotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:  123456,
		XUIHost:          "http://localhost:2053",
		XUIUsername:      "admin",
		XUIPassword:      "password",
		XUIInboundID:     1,
		XUISubPath:       "/xui",
		LogLevel:         "invalid",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on invalid LogLevel")
}

func TestConfig_Validate_InvalidHealthCheckPort_TooLow(t *testing.T) {
	cfg := &Config{
		TelegramBotToken:  "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:   123456,
		XUIHost:           "http://localhost:2053",
		XUIUsername:       "admin",
		XUIPassword:       "password",
		XUIInboundID:      1,
		XUISubPath:        "/xui",
		TrafficLimitGB:    100,
		HeartbeatInterval: 60,
		LogLevel:          "info",
		HealthCheckPort:   0,
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on HealthCheckPort = 0")
}

func TestConfig_Validate_InvalidHealthCheckPort_TooHigh(t *testing.T) {
	cfg := &Config{
		TelegramBotToken:  "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:   123456,
		XUIHost:           "http://localhost:2053",
		XUIUsername:       "admin",
		XUIPassword:       "password",
		XUIInboundID:      1,
		XUISubPath:        "/xui",
		TrafficLimitGB:    100,
		HeartbeatInterval: 60,
		LogLevel:          "info",
		HealthCheckPort:   70000,
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on HealthCheckPort = 70000")
}

func TestMaskURL_Empty(t *testing.T) {
	result := maskURL("")
	assert.Equal(t, "(not set)", result, "maskURL(\"\")")
}

func TestMaskURL_ValidURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"with path", "http://example.com/path", "http://example.com/***"},
		{"with port", "http://example.com:8080/path", "http://example.com:8080/***"},
		{"https", "https://secure.com/api", "https://secure.com/***"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskURL(tt.input)
			assert.Equal(t, tt.expected, result, "maskURL(%q)", tt.input)
		})
	}
}

func TestLoad_TrialDurationHours_TooLow(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("TRIAL_DURATION_HOURS", "0")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRIAL_DURATION_HOURS")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error when TRIAL_DURATION_HOURS is too low")
	assert.Contains(t, err.Error(), "TRIAL_DURATION_HOURS", "Error should mention TRIAL_DURATION_HOURS")
}

func TestLoad_TrialDurationHours_TooHigh(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("TRIAL_DURATION_HOURS", "200")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRIAL_DURATION_HOURS")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error when TRIAL_DURATION_HOURS is too high")
	assert.Contains(t, err.Error(), "TRIAL_DURATION_HOURS", "Error should mention TRIAL_DURATION_HOURS")
}

func TestLoad_TrialDurationHours_Valid(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("TRIAL_DURATION_HOURS", "24")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRIAL_DURATION_HOURS")
	}()

	cfg, err := Load()
	require.NoError(t, err, "Load() should not error with valid TRIAL_DURATION_HOURS")
	assert.Equal(t, 24, cfg.TrialDurationHours, "TrialDurationHours")
}

func TestLoad_TrialRateLimit_TooLow(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("TRIAL_RATE_LIMIT", "0")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRIAL_RATE_LIMIT")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error when TRIAL_RATE_LIMIT is too low")
	assert.Contains(t, err.Error(), "TRIAL_RATE_LIMIT", "Error should mention TRIAL_RATE_LIMIT")
}

func TestLoad_TrialRateLimit_TooHigh(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("TRIAL_RATE_LIMIT", "150")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRIAL_RATE_LIMIT")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error when TRIAL_RATE_LIMIT is too high")
	assert.Contains(t, err.Error(), "TRIAL_RATE_LIMIT", "Error should mention TRIAL_RATE_LIMIT")
}

func TestLoad_TrialRateLimit_Valid(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("TRIAL_RATE_LIMIT", "5")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRIAL_RATE_LIMIT")
	}()

	cfg, err := Load()
	require.NoError(t, err, "Load() should not error with valid TRIAL_RATE_LIMIT")
	assert.Equal(t, 5, cfg.TrialRateLimit, "TrialRateLimit")
}

func TestLoad_SiteURL_Invalid(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("SITE_URL", "not-a-valid-url")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("SITE_URL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error with invalid SITE_URL")
	assert.Contains(t, err.Error(), "SITE_URL", "Error should mention SITE_URL")
}

func TestLoad_SiteURL_MissingScheme(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("SITE_URL", "example.com")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("SITE_URL")
	}()

	_, err := Load()
	assert.Error(t, err, "Load() should error when SITE_URL is missing scheme")
	assert.Contains(t, err.Error(), "SITE_URL", "Error should mention SITE_URL")
}

func TestLoad_SiteURL_Valid(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("SITE_URL", "https://vpn.example.com")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("SITE_URL")
	}()

	cfg, err := Load()
	require.NoError(t, err, "Load() should not error with valid SITE_URL")
	assert.Equal(t, "https://vpn.example.com", cfg.SiteURL, "SiteURL")
}

func TestLoad_AllTrialSettings(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "password")
	os.Setenv("XUI_INBOUND_ID", "1")
	os.Setenv("LOG_LEVEL", "info")
	os.Setenv("TRIAL_DURATION_HOURS", "48")
	os.Setenv("TRIAL_RATE_LIMIT", "10")
	os.Setenv("SITE_URL", "https://trial.example.com")
	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRIAL_DURATION_HOURS")
		os.Unsetenv("TRIAL_RATE_LIMIT")
		os.Unsetenv("SITE_URL")
	}()

	cfg, err := Load()
	require.NoError(t, err, "Load() should not error with all valid settings")
	assert.Equal(t, 48, cfg.TrialDurationHours, "TrialDurationHours")
	assert.Equal(t, 10, cfg.TrialRateLimit, "TrialRateLimit")
	assert.Equal(t, "https://trial.example.com", cfg.SiteURL, "SiteURL")
}

// ==================== Fuzz Tests ====================

func FuzzLoad_InvalidEnvValues(f *testing.F) {
	baseEnvs := map[string]string{
		"TELEGRAM_BOT_TOKEN": "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		"TELEGRAM_ADMIN_ID":  "123456",
		"XUI_HOST":           "http://localhost:2053",
		"XUI_USERNAME":       "admin",
		"XUI_PASSWORD":       "password",
		"XUI_INBOUND_ID":     "1",
		"LOG_LEVEL":          "info",
	}

	invalidValues := []string{
		"invalid",
		"-1",
		"0",
		"999999999999999999",
		"abc",
		"",
		"\x00",
		"   ",
		"\n",
		"../../etc/passwd",
		"<script>alert(1)</script>",
	}

	for _, val := range invalidValues {
		f.Add(val)
	}

	f.Fuzz(func(t *testing.T, invalidVal string) {
		for _, key := range []string{"TELEGRAM_ADMIN_ID", "XUI_INBOUND_ID", "TRAFFIC_LIMIT_GB", "HEARTBEAT_INTERVAL", "TRIAL_DURATION_HOURS", "TRIAL_RATE_LIMIT", "HEALTH_CHECK_PORT"} {
			os.Setenv(key, invalidVal)
		}
		for k, v := range baseEnvs {
			os.Setenv(k, v)
		}
		defer func() {
			for _, key := range []string{"TELEGRAM_ADMIN_ID", "XUI_INBOUND_ID", "TRAFFIC_LIMIT_GB", "HEARTBEAT_INTERVAL", "TRIAL_DURATION_HOURS", "TRIAL_RATE_LIMIT", "HEALTH_CHECK_PORT", "TELEGRAM_BOT_TOKEN", "TELEGRAM_ADMIN_ID", "XUI_HOST", "XUI_USERNAME", "XUI_PASSWORD", "XUI_INBOUND_ID", "LOG_LEVEL"} {
				os.Unsetenv(key)
			}
		}()

		// Should either succeed with defaults or fail gracefully
		cfg, err := Load()
		if err == nil {
			assert.NotNil(t, cfg, "Config should not be nil on success")
		}
	})
}
