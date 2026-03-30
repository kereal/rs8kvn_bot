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

	cfg, err := Load()
	require.NoError(t, err, "Load() should use default for invalid TELEGRAM_ADMIN_ID")
	assert.Equal(t, int64(0), cfg.TelegramAdminID, "TelegramAdminID should be 0 (default)")
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

	cfg, err := Load()
	require.NoError(t, err, "Load() should use default for invalid XUI_INBOUND_ID")
	assert.Equal(t, 1, cfg.XUIInboundID, "XUIInboundID should be 1 (default)")
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

	cfg, err := Load()
	require.NoError(t, err, "Load() should use default for invalid HEARTBEAT_INTERVAL")
	assert.Equal(t, DefaultHeartbeatInterval, cfg.HeartbeatInterval, "HeartbeatInterval should be default")
}

func TestGetEnv_DefaultValue(t *testing.T) {
	os.Unsetenv("TEST_KEY")
	result := getEnv("TEST_KEY", "default")
	assert.Equal(t, "default", result, "getEnv() should return default value")
}

func TestGetEnv_ExistingValue(t *testing.T) {
	os.Setenv("TEST_KEY", "value")
	defer os.Unsetenv("TEST_KEY")
	result := getEnv("TEST_KEY", "default")
	assert.Equal(t, "value", result, "getEnv() should return existing value")
}

func TestGetEnv_WhitespaceTrimmed(t *testing.T) {
	os.Setenv("TEST_KEY", "  value  ")
	defer os.Unsetenv("TEST_KEY")
	result := getEnv("TEST_KEY", "default")
	assert.Equal(t, "value", result, "getEnv() should trim whitespace")
}

func TestParseEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")
	result := parseEnvInt("TEST_INT", 0)
	assert.Equal(t, 42, result, "parseEnvInt()")
}

func TestParseEnvInt_Default(t *testing.T) {
	os.Unsetenv("TEST_INT")
	result := parseEnvInt("TEST_INT", 100)
	assert.Equal(t, 100, result, "parseEnvInt() should return default")
}

func TestParseEnvInt_Invalid(t *testing.T) {
	os.Setenv("TEST_INT", "invalid")
	defer os.Unsetenv("TEST_INT")
	result := parseEnvInt("TEST_INT", 0)
	assert.Equal(t, 0, result, "parseEnvInt() should return default for invalid input")
}

func TestParseEnvInt64(t *testing.T) {
	os.Setenv("TEST_INT64", "9999999999")
	defer os.Unsetenv("TEST_INT64")
	result := parseEnvInt64("TEST_INT64", 0)
	assert.Equal(t, int64(9999999999), result, "parseEnvInt64()")
}

func TestParseEnvInt64_Default(t *testing.T) {
	os.Unsetenv("TEST_INT64")
	result := parseEnvInt64("TEST_INT64", 500)
	assert.Equal(t, int64(500), result, "parseEnvInt64() should return default")
}

func TestParseEnvInt64_Invalid(t *testing.T) {
	os.Setenv("TEST_INT64", "invalid")
	defer os.Unsetenv("TEST_INT64")
	result := parseEnvInt64("TEST_INT64", 0)
	assert.Equal(t, int64(0), result, "parseEnvInt64() should return default for invalid input")
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
		XUISubPath:         "/xui",
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
		XUISubPath:         "/xui",
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
		XUISubPath:       "/xui",
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
		XUISubPath:         "/custom",
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
		XUISubPath:         "/xui",
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

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
		{"http URL", "http://example.com/path", "http://example.com/***"},
		{"https URL", "https://example.com:8080/path", "https://example.com:8080/***"},
		{"URL without path", "https://example.com", "https://example.com/***"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskURL(tt.input)
			assert.Equal(t, tt.expected, result, "maskURL()")
		})
	}
}
