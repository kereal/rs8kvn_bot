package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBotToken is a syntactically valid placeholder token used by Load() tests.
const testBotToken = "123456789:ABCdefGHIjklMNOpqrsTUVwxyz"

// TestMain isolates the Load() tests from payment environment variables that
// may be exported in the host shell or a local .env (e.g. a development
// environment with PAYMENT_ENABLED=true and a placeholder merchant ID). The
// Load() tests exercise non-payment settings only, so payment vars must not
// leak into validation. Without this, a host with payments enabled fails every
// Load() test with "PLATEGA_MERCHANT_ID must be a UUID".
func TestMain(m *testing.M) {
	for _, key := range []string{"PAYMENT_ENABLED", "PAYMENT_PROVIDER", "PLATEGA_MERCHANT_ID", "PLATEGA_SECRET"} {
		_ = os.Unsetenv(key)
	}

	os.Exit(m.Run())
}

// setLoadEnv resets the shared baseline environment for a Load() test and
// applies per-test overrides on top. All variables are registered with
// t.Setenv, so they are restored automatically when the test finishes.
// GLOBAL_SUB_URL is part of the baseline because validate() rejects configs
// without it; per-test overrides may replace any value.
func setLoadEnv(t *testing.T, overrides map[string]string) {
	t.Helper()

	t.Setenv("SUBSERVER_ACCESS_LOG", "")
	for k, v := range map[string]string{
		"TELEGRAM_BOT_TOKEN": testBotToken,
		"TELEGRAM_ADMIN_ID":  "123456",
		"LOG_LEVEL":          "info",
		"GLOBAL_SUB_URL":     "https://vpn.example.com/sub/",
	} {
		t.Setenv(k, v)
	}
	for k, v := range overrides {
		t.Setenv(k, v)
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	setLoadEnv(t, map[string]string{"NOTIFICATIONS_ENABLED": ""})

	cfg, err := Load()
	require.NoError(t, err, "Load() error")

	assert.Equal(t, testBotToken, cfg.TelegramBotToken, "TelegramBotToken")
	assert.Equal(t, int64(123456), cfg.TelegramAdminID, "TelegramAdminID")
	assert.Empty(t, cfg.SubServerAccessLogPath, "SubServerAccessLogPath")
	assert.True(t, cfg.NotificationsEnabled, "NotificationsEnabled should default to true")
}

func TestLoad_NotificationsDisabled(t *testing.T) {
	setLoadEnv(t, map[string]string{"NOTIFICATIONS_ENABLED": "false"})

	cfg, err := Load()
	require.NoError(t, err, "Load() error")
	assert.False(t, cfg.NotificationsEnabled, "NOTIFICATIONS_ENABLED=false must disable notification workers")
}

func TestLoad_CustomValues(t *testing.T) {
	setLoadEnv(t, map[string]string{
		"HEARTBEAT_INTERVAL":   "120",
		"DATABASE_PATH":        "/custom/path/db.db",
		"LOG_LEVEL":            "debug",
		"WEB_SERVER_PORT":      "9090",
		"SUBSERVER_ACCESS_LOG": "./data/subserver-test.log",
	})

	cfg, err := Load()
	require.NoError(t, err, "Load() error")

	assert.Equal(t, 120, cfg.HeartbeatInterval, "HeartbeatInterval")
	assert.Equal(t, 9090, cfg.WebServerPort, "WebServerPort")
	assert.Equal(t, "./data/subserver-test.log", cfg.SubServerAccessLogPath, "SubServerAccessLogPath")
}

func TestLoad_MissingBotToken(t *testing.T) {
	setLoadEnv(t, map[string]string{"TELEGRAM_BOT_TOKEN": ""})

	_, err := Load()
	assert.Error(t, err, "Load() should error when BOT_TOKEN is empty")
}

func TestLoad_InvalidTelegramAdminID(t *testing.T) {
	setLoadEnv(t, map[string]string{"TELEGRAM_ADMIN_ID": "invalid"})

	_, err := Load()
	require.Error(t, err, "Load() should error for invalid TELEGRAM_ADMIN_ID")
	assert.Contains(t, err.Error(), "TELEGRAM_ADMIN_ID", "Error should mention TELEGRAM_ADMIN_ID")
}

func TestLoad_InvalidHeartbeatInterval_Zero(t *testing.T) {
	setLoadEnv(t, map[string]string{
		"HEARTBEAT_INTERVAL": "0",
		"HEARTBEAT_URL":      "https://example.com/heartbeat",
	})

	_, err := Load()
	assert.Error(t, err, "Load() should error for HEARTBEAT_INTERVAL=0")
}

func TestLoad_ValidHeartbeatInterval_MinValue(t *testing.T) {
	setLoadEnv(t, map[string]string{
		"HEARTBEAT_INTERVAL": "60",
		"HEARTBEAT_URL":      "https://example.com/heartbeat",
	})

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 60, cfg.HeartbeatInterval)
}

func TestLoad_InvalidWebServerPort_Zero(t *testing.T) {
	setLoadEnv(t, map[string]string{"WEB_SERVER_PORT": "0"})

	_, err := Load()
	assert.Error(t, err, "Load() should error for WEB_SERVER_PORT=0")
}

func TestLoad_InvalidWebServerPort_Max(t *testing.T) {
	for _, port := range []string{"65536", "70000"} {
		t.Run(port, func(t *testing.T) {
			setLoadEnv(t, map[string]string{"WEB_SERVER_PORT": port})

			_, err := Load()
			assert.Error(t, err, "Load() should error for WEB_SERVER_PORT=%s (max is 65535)", port)
		})
	}
}

func TestLoad_ValidWebServerPort_Max(t *testing.T) {
	setLoadEnv(t, map[string]string{"WEB_SERVER_PORT": "65535"})

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 65535, cfg.WebServerPort)
}

func TestLoad_ValidTelegramAdminID(t *testing.T) {
	setLoadEnv(t, nil)

	cfg, err := Load()
	require.NoError(t, err, "Load() should allow valid ADMIN_ID")
	assert.Equal(t, int64(123456), cfg.TelegramAdminID)
}

func TestLoad_ValidTrialDurationHours_Min(t *testing.T) {
	setLoadEnv(t, map[string]string{"TRIAL_DURATION_HOURS": "1"})

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.TrialDurationHours)
}

func TestLoad_ValidTrialDurationHours_Max(t *testing.T) {
	setLoadEnv(t, map[string]string{"TRIAL_DURATION_HOURS": "168"})

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 168, cfg.TrialDurationHours)
}

func TestLoad_ValidTrialRateLimit_Min(t *testing.T) {
	setLoadEnv(t, map[string]string{"TRIAL_RATE_LIMIT": "1"})

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.TrialRateLimit)
}

func TestLoad_ValidTrialRateLimit_Max(t *testing.T) {
	setLoadEnv(t, map[string]string{"TRIAL_RATE_LIMIT": "100"})

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 100, cfg.TrialRateLimit)
}

func TestLoad_InvalidHeartbeatInterval(t *testing.T) {
	setLoadEnv(t, map[string]string{"HEARTBEAT_INTERVAL": "invalid"})

	_, err := Load()
	require.Error(t, err, "Load() should error for invalid HEARTBEAT_INTERVAL")
	assert.Contains(t, err.Error(), "HEARTBEAT_INTERVAL", "Error should mention HEARTBEAT_INTERVAL")
}

func TestValidateURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{}

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid https", "https://example.com", false},
		{"valid http", "https://localhost:8080", false},
		{"invalid no scheme", "example.com", true},
		{"invalid empty", "", true},
		{"no host", "http://", true},
		{"no scheme or host", "localhost:8080", true},
		{"scheme without host", "https://localhost", false},
		{"with path", "https://example.com/path/to/resource", false},
		{"with query", "https://example.com?query=value", false},
		{"with port and path", "https://localhost:8080/api/v1", false},
		{"disallowed scheme file", "file:///etc/passwd", true},
		{"disallowed scheme gopher", "gopher://localhost", true},
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

// TestConfig_String verifies the "secret not in logs" contract: the bot token
// must be masked and its real value must never appear in String() output.
func TestConfig_String(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken: testBotToken,
		TelegramAdminID:  123456,
		GlobalSubURL:     "https://vpn.example.com/sub/secretid",
	}

	str := cfg.String()

	assert.Contains(t, str, "TelegramBotToken=***", "String() should mask TelegramBotToken")
	assert.NotContains(t, str, testBotToken, "String() must never leak the real bot token")
	assert.NotContains(t, str, "secretid", "String() must mask GlobalSubURL path")
	assert.Contains(t, str, "123456", "String() should include TelegramAdminID")
}

func TestMaskURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"http", "http://example.com/path", "http://example.com/***"},
		{"https", "https://secure.example.com:8443", "https://secure.example.com:8443/***"},
		{"with port", "https://localhost:2053/xui", "https://localhost:2053/***"},
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
	t.Parallel()

	result := maskURL("not a valid url")
	assert.Equal(t, ":///***", result, "maskURL() for invalid URL")
}

func TestConfig_Validate_InvalidPaymentMerchantUUID(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken:   testBotToken,
		TelegramAdminID:    123456,
		HeartbeatInterval:  60,
		LogLevel:           "info",
		WebServerPort:      DefaultWebServerPort,
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		GlobalSubURL:       "https://vpn.example.com/sub/",
		PaymentEnabled:     true,
		PaymentProvider:    "platega",
		PlategaMerchantID:  "not-a-uuid",
		PlategaSecret:      "secret",
	}
	err := cfg.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PLATEGA_MERCHANT_ID")
}

func TestConfig_Validate_Valid(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken:   testBotToken,
		TelegramAdminID:    123456,
		HeartbeatInterval:  60,
		LogLevel:           "info",
		WebServerPort:      DefaultWebServerPort,
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		GlobalSubURL:       "https://vpn.example.com/sub/",
	}

	err := cfg.validate()
	assert.NoError(t, err, "validate() should not error for valid config")
}

func TestConfig_Validate_SentryDSN_Valid(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken:   testBotToken,
		TelegramAdminID:    123456,
		HeartbeatInterval:  60,
		LogLevel:           "info",
		SentryDSN:          "https://abc@sentry.io/123",
		WebServerPort:      DefaultWebServerPort,
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		GlobalSubURL:       "https://vpn.example.com/sub/",
	}

	err := cfg.validate()
	assert.NoError(t, err, "validate() should not error for valid SentryDSN")
}

func TestConfig_Validate_SentryDSN_Invalid(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken: testBotToken,
		TelegramAdminID:  123456,
		SentryDSN:        "invalid-dsn",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on invalid SentryDSN")
}

func TestConfig_Validate_WithHeartbeatURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken:   testBotToken,
		TelegramAdminID:    123456,
		HeartbeatInterval:  60,
		LogLevel:           "info",
		HeartbeatURL:       "https://health.example.com",
		WebServerPort:      DefaultWebServerPort,
		SiteURL:            "https://vpn.site",
		TrialDurationHours: 3,
		TrialRateLimit:     3,
		GlobalSubURL:       "https://vpn.example.com/sub/",
	}

	err := cfg.validate()
	assert.NoError(t, err, "validate() should not error with HeartbeatURL")
}

func TestConfig_Validate_InvalidTokenFormat(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken: "invalid-token-without-colon",
		TelegramAdminID:  123456,
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on invalid token format")
}

func TestConfig_Validate_NegativeAdminID(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken: testBotToken,
		TelegramAdminID:  -1,
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on negative AdminID")
}

func TestConfig_Validate_InvalidLogLevel(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken: testBotToken,
		TelegramAdminID:  123456,
		LogLevel:         "invalid",
	}

	err := cfg.validate()
	assert.Error(t, err, "validate() should error on invalid LogLevel")
}

func TestMaskURL_ValidURL(t *testing.T) {
	t.Parallel()

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
	setLoadEnv(t, map[string]string{"TRIAL_DURATION_HOURS": "0"})

	_, err := Load()
	assert.Error(t, err, "Load() should error when TRIAL_DURATION_HOURS is too low")
	assert.Contains(t, err.Error(), "TRIAL_DURATION_HOURS", "Error should mention TRIAL_DURATION_HOURS")
}

func TestLoad_TrialDurationHours_TooHigh(t *testing.T) {
	for _, hours := range []string{"169", "200"} {
		t.Run(hours, func(t *testing.T) {
			setLoadEnv(t, map[string]string{"TRIAL_DURATION_HOURS": hours})

			_, err := Load()
			assert.Error(t, err, "Load() should error when TRIAL_DURATION_HOURS=%s exceeds 168", hours)
			assert.Contains(t, err.Error(), "TRIAL_DURATION_HOURS", "Error should mention TRIAL_DURATION_HOURS")
		})
	}
}

func TestLoad_TrialRateLimit_TooLow(t *testing.T) {
	setLoadEnv(t, map[string]string{"TRIAL_RATE_LIMIT": "0"})

	_, err := Load()
	assert.Error(t, err, "Load() should error when TRIAL_RATE_LIMIT is too low")
	assert.Contains(t, err.Error(), "TRIAL_RATE_LIMIT", "Error should mention TRIAL_RATE_LIMIT")
}

func TestLoad_TrialRateLimit_TooHigh(t *testing.T) {
	setLoadEnv(t, map[string]string{"TRIAL_RATE_LIMIT": "150"})

	_, err := Load()
	assert.Error(t, err, "Load() should error when TRIAL_RATE_LIMIT is too high")
	assert.Contains(t, err.Error(), "TRIAL_RATE_LIMIT", "Error should mention TRIAL_RATE_LIMIT")
}

func TestLoad_SiteURL_Invalid(t *testing.T) {
	setLoadEnv(t, map[string]string{"SITE_URL": "not-a-valid-url"})

	_, err := Load()
	assert.Error(t, err, "Load() should error with invalid SITE_URL")
	assert.Contains(t, err.Error(), "SITE_URL", "Error should mention SITE_URL")
}

func TestLoad_SiteURL_MissingScheme(t *testing.T) {
	setLoadEnv(t, map[string]string{"SITE_URL": "example.com"})

	_, err := Load()
	assert.Error(t, err, "Load() should error when SITE_URL is missing scheme")
	assert.Contains(t, err.Error(), "SITE_URL", "Error should mention SITE_URL")
}

func TestLoad_AllTrialSettings(t *testing.T) {
	setLoadEnv(t, map[string]string{
		"TRIAL_DURATION_HOURS": "48",
		"TRIAL_RATE_LIMIT":     "10",
		"SITE_URL":             "https://trial.example.com",
	})

	cfg, err := Load()
	require.NoError(t, err, "Load() should not error with all valid settings")
	assert.Equal(t, 48, cfg.TrialDurationHours, "TrialDurationHours")
	assert.Equal(t, 10, cfg.TrialRateLimit, "TrialRateLimit")
	assert.Equal(t, "https://trial.example.com", cfg.SiteURL, "SiteURL")
}

// ==================== Fuzz Tests ====================

func FuzzLoad_InvalidEnvValues(f *testing.F) {
	baseEnvs := map[string]string{
		"TELEGRAM_BOT_TOKEN": testBotToken,
		"TELEGRAM_ADMIN_ID":  "123456",
		"LOG_LEVEL":          "info",
		"GLOBAL_SUB_URL":     "https://vpn.example.com/sub/",
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
		for _, key := range []string{"TELEGRAM_ADMIN_ID", "HEARTBEAT_INTERVAL", "TRIAL_DURATION_HOURS", "TRIAL_RATE_LIMIT", "WEB_SERVER_PORT"} {
			os.Setenv(key, invalidVal)
		}

		for k, v := range baseEnvs {
			os.Setenv(k, v)
		}
		defer func() {
			for key := range baseEnvs {
				os.Unsetenv(key)
			}

			for _, key := range []string{"HEARTBEAT_INTERVAL", "TRIAL_DURATION_HOURS", "TRIAL_RATE_LIMIT", "WEB_SERVER_PORT"} {
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

// Тест поведения: срок хранения аудита по умолчанию — полгода, а нулевое или
// отрицательное значение отвергается (иначе обслуживание снесло бы всю историю).
func TestConfig_Validate_AuditRetention(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 180, DefaultAuditLogRetentionDays)

	base := &Config{
		TelegramBotToken:      testBotToken,
		TelegramAdminID:       123456,
		HeartbeatInterval:     60,
		LogLevel:              "info",
		WebServerPort:         DefaultWebServerPort,
		SiteURL:               "https://vpn.site",
		TrialDurationHours:    3,
		TrialRateLimit:        3,
		GlobalSubURL:          "https://vpn.example.com/sub/",
		AuditLogRetentionDays: 0,
	}

	require.NoError(t, base.validate(), "0 означает значение по умолчанию")

	base.AuditLogRetentionDays = -1
	require.ErrorContains(t, base.validate(), "AUDIT_LOG_RETENTION_DAYS")

	base.AuditLogRetentionDays = 3650
	require.NoError(t, base.validate())
}
