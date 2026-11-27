package config

import (
	"os"
	"testing"
	"time"

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
	for _, key := range []string{"PAYMENT_ENABLED", "PAYMENT_PROVIDER", "PLATEGA_MERCHANT_ID", "PLATEGA_SECRET", "PLATEGA_CALLBACK_ENABLED", "STARS_INVOICE_TTL"} {
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

func TestLoad_AdminDefaultsDisabled(t *testing.T) {
	setLoadEnv(t, map[string]string{"ADMIN_TOKEN": ""})

	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.AdminToken, "empty ADMIN_TOKEN must disable the panel")
	assert.Equal(t, DefaultAdminListen, cfg.AdminListen)
}

// TestLoad_AdminWhitespaceTokenActsLikeEmpty: пробельный ADMIN_TOKEN трактуется
// рантаймом (TrimSpace в startAdminServer) как выключенная панель — значит
// validate() не может требовать корректный ADMIN_LISTEN в этом случае.
func TestLoad_AdminWhitespaceTokenActsLikeEmpty(t *testing.T) {
	setLoadEnv(t, map[string]string{"ADMIN_TOKEN": "   ", "ADMIN_LISTEN": "not-a-valid-listen"})

	_, err := Load()
	require.NoError(t, err, "a whitespace token disables the panel, so ADMIN_LISTEN is not validated")
}

func TestLoad_AdminTokenRequiresValidListen(t *testing.T) {
	cases := map[string]string{
		"no port":        "127.0.0.1",
		"no host":        ":8881",
		"bad port":       "127.0.0.1:port",
		"port too large": "127.0.0.1:70000",
	}

	for name, listen := range cases {
		t.Run(name, func(t *testing.T) {
			setLoadEnv(t, map[string]string{"ADMIN_TOKEN": "secret", "ADMIN_LISTEN": listen})

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "ADMIN_LISTEN")
		})
	}
}

func TestLoad_AdminListenMustDifferFromWebPort(t *testing.T) {
	setLoadEnv(t, map[string]string{
		"ADMIN_TOKEN":     "secret",
		"ADMIN_LISTEN":    "127.0.0.1:8880",
		"WEB_SERVER_PORT": "8880",
	})

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must differ from WEB_SERVER_PORT")
}

func TestConfig_StringMasksAdminToken(t *testing.T) {
	cfg := &Config{AdminToken: "super-secret-token", AdminListen: "127.0.0.1:8881"}

	assert.NotContains(t, cfg.String(), "super-secret-token")
	assert.Contains(t, cfg.String(), "AdminToken=***")
	assert.Contains(t, cfg.String(), "AdminURL=http://127.0.0.1:8881/admin/")

	// Пустой ADMIN_TOKEN выключает панель целиком: слушателя нет, поэтому в
	// сводке не должно быть «рабочей» ссылки.
	disabled := &Config{AdminListen: "127.0.0.1:8881"}
	assert.Contains(t, disabled.String(), "AdminURL=(disabled)")
}

func TestConfig_AdminURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		adminToken string
		listen     string
		want       string
	}{
		{name: "loopback", adminToken: "secret", listen: "127.0.0.1:8881", want: "http://127.0.0.1:8881/admin/"},
		{name: "wildcard host", adminToken: "secret", listen: "0.0.0.0:8881", want: "http://127.0.0.1:8881/admin/"},
		{name: "wildcard ipv6", adminToken: "secret", listen: "[::]:8881", want: "http://127.0.0.1:8881/admin/"},
		{name: "ipv6 loopback", adminToken: "secret", listen: "[::1]:8881", want: "http://[::1]:8881/admin/"},
		{name: "panel disabled without token", listen: "127.0.0.1:8881", want: ""},
		{name: "unparsable listen", adminToken: "secret", listen: "no-port", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{AdminToken: tt.adminToken, AdminListen: tt.listen}

			assert.Equal(t, tt.want, cfg.AdminURL())
		})
	}
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
		StarsInvoiceTTL:    DefaultStarsInvoiceTTL,
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
		StarsInvoiceTTL:    DefaultStarsInvoiceTTL,
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
		StarsInvoiceTTL:    DefaultStarsInvoiceTTL,
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
		StarsInvoiceTTL:    DefaultStarsInvoiceTTL,
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
		StarsInvoiceTTL:       DefaultStarsInvoiceTTL,
		AuditLogRetentionDays: 0,
	}

	require.NoError(t, base.validate(), "0 означает значение по умолчанию")

	base.AuditLogRetentionDays = -1
	require.ErrorContains(t, base.validate(), "AUDIT_LOG_RETENTION_DAYS")

	base.AuditLogRetentionDays = 3650
	require.NoError(t, base.validate())
}

func TestLoad_PaymentProvider_Allowlist(t *testing.T) {
	for _, provider := range []string{"platega", "telegram_stars"} {
		env := map[string]string{
			"PAYMENT_ENABLED":  "true",
			"PAYMENT_PROVIDER": provider,
		}
		if provider == "platega" {
			env["PLATEGA_MERCHANT_ID"] = "6f9619ff-8b86-d011-b42d-00c04fc964ff"
			env["PLATEGA_SECRET"] = "secret"
		}
		setLoadEnv(t, env)

		cfg, err := Load()
		require.NoError(t, err, "provider %q должен быть валиден при PAYMENT_ENABLED=true", provider)
		assert.Equal(t, provider, cfg.PaymentProvider, "PaymentProvider")
	}

	setLoadEnv(t, map[string]string{
		"PAYMENT_ENABLED":  "true",
		"PAYMENT_PROVIDER": "bitcoin",
	})

	_, err := Load()
	require.Error(t, err, "неизвестный провайдер должен отклоняться")
	assert.Contains(t, err.Error(), "PAYMENT_PROVIDER")
}

func TestLoad_PaymentDisabled_NoCredentialsRequired(t *testing.T) {
	// B1-регрессия: .env.example-подобный дефолт — оплаты выключены,
	// провайдер по умолчанию platega, креды пустые — конфиг валиден.
	setLoadEnv(t, nil)

	cfg, err := Load()
	require.NoError(t, err, "Load() error")
	assert.False(t, cfg.PaymentEnabled, "PaymentEnabled")
	assert.Equal(t, "platega", cfg.PaymentProvider, "PaymentProvider default")
	assert.False(t, cfg.PlategaCallbackEnabled, "PlategaCallbackEnabled default")
	assert.False(t, cfg.ShouldEnablePlategaCallback(), "callback должен быть выключен")
}

func TestLoad_PlategaCallbackEnabled_RequiresCredentials(t *testing.T) {
	setLoadEnv(t, map[string]string{"PLATEGA_CALLBACK_ENABLED": "true"})
	_, err := Load()
	require.Error(t, err, "callback без Platega-кредов невалиден")
	assert.Contains(t, err.Error(), "PLATEGA_MERCHANT_ID")

	setLoadEnv(t, map[string]string{
		"PAYMENT_PROVIDER":         "telegram_stars",
		"PLATEGA_CALLBACK_ENABLED": "true",
	})
	_, err = Load()
	require.Error(t, err, "Stars + callback без Platega-кредов невалиден")
	assert.Contains(t, err.Error(), "PLATEGA_MERCHANT_ID")

	setLoadEnv(t, map[string]string{
		"PLATEGA_CALLBACK_ENABLED": "true",
		"PLATEGA_MERCHANT_ID":      "6f9619ff-8b86-d011-b42d-00c04fc964ff",
		"PLATEGA_SECRET":           "secret",
	})
	cfg, err := Load()
	require.NoError(t, err, "callback с валидными кредами должен загружаться")
	assert.True(t, cfg.ShouldEnablePlategaCallback(), "callback должен включаться по флагу")
}

func TestLoad_TelegramStars_NoPlategaCredentials(t *testing.T) {
	setLoadEnv(t, map[string]string{
		"PAYMENT_ENABLED":  "true",
		"PAYMENT_PROVIDER": "telegram_stars",
	})

	cfg, err := Load()
	require.NoError(t, err, "Stars без Platega-кредов валиден")
	assert.Equal(t, "telegram_stars", cfg.PaymentProvider, "PaymentProvider")
	assert.False(t, cfg.ShouldEnablePlategaCallback(), "callback выключен при Stars")
}

func TestLoad_StarsInvoiceTTL(t *testing.T) {
	setLoadEnv(t, nil)
	cfg, err := Load()
	require.NoError(t, err, "Load() error")
	assert.Equal(t, 15*time.Minute, cfg.StarsInvoiceTTL, "STARS_INVOICE_TTL default")

	setLoadEnv(t, map[string]string{"STARS_INVOICE_TTL": "30m"})
	cfg, err = Load()
	require.NoError(t, err, "Load() error")
	assert.Equal(t, 30*time.Minute, cfg.StarsInvoiceTTL, "STARS_INVOICE_TTL custom")

	setLoadEnv(t, map[string]string{"STARS_INVOICE_TTL": "0"})
	_, err = Load()
	require.Error(t, err, "нулевой TTL должен отклоняться")
	assert.Contains(t, err.Error(), "STARS_INVOICE_TTL")

	setLoadEnv(t, map[string]string{"STARS_INVOICE_TTL": "-5m"})
	_, err = Load()
	require.Error(t, err, "отрицательный TTL должен отклоняться")
	assert.Contains(t, err.Error(), "STARS_INVOICE_TTL")

	setLoadEnv(t, map[string]string{"STARS_INVOICE_TTL": "bogus"})
	_, err = Load()
	require.Error(t, err, "нечисловое значение должно отклоняться")
	assert.Contains(t, err.Error(), "STARS_INVOICE_TTL")
}

func TestConfig_Validate_PlategaCredentials(t *testing.T) {
	t.Parallel()

	newCfg := func() *Config {
		return &Config{
			TelegramBotToken:   testBotToken,
			TelegramAdminID:    123456,
			HeartbeatInterval:  60,
			LogLevel:           "info",
			WebServerPort:      DefaultWebServerPort,
			SiteURL:            "https://vpn.site",
			TrialDurationHours: 3,
			TrialRateLimit:     3,
			GlobalSubURL:       "https://vpn.example.com/sub/",
			StarsInvoiceTTL:    DefaultStarsInvoiceTTL,
			PaymentProvider:    "platega",
		}
	}

	// Выключенные оплаты без кредов — валидно.
	require.NoError(t, newCfg().validate(), "payments disabled без кредов валиден")

	// Включённые оплаты platega без кредов — ошибка.
	cfg := newCfg()
	cfg.PaymentEnabled = true
	require.ErrorContains(t, cfg.validate(), "PLATEGA_MERCHANT_ID")

	// С валидными кредами — ок.
	cfg = newCfg()
	cfg.PaymentEnabled = true
	cfg.PlategaMerchantID = "6f9619ff-8b86-d011-b42d-00c04fc964ff"
	cfg.PlategaSecret = "secret"
	require.NoError(t, cfg.validate())

	// Некорректный UUID — ошибка.
	cfg.PlategaMerchantID = "not-a-uuid"
	require.ErrorContains(t, cfg.validate(), "PLATEGA_MERCHANT_ID")
}

func TestConfig_ShouldEnablePlategaCallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		paymentEnabled         bool
		paymentProvider        string
		plategaCallbackEnabled bool
		want                   bool
	}{
		{"disabled payments, platega default, flag off", false, "platega", false, false},
		{"disabled payments, flag on", false, "platega", true, true},
		{"enabled payments, platega", true, "platega", false, true},
		{"enabled payments, stars, flag off", true, "telegram_stars", false, false},
		{"enabled payments, stars, flag on", true, "telegram_stars", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{
				PaymentEnabled:         tt.paymentEnabled,
				PaymentProvider:        tt.paymentProvider,
				PlategaCallbackEnabled: tt.plategaCallbackEnabled,
			}
			assert.Equal(t, tt.want, cfg.ShouldEnablePlategaCallback())
		})
	}
}

func TestConfig_String_NoSecrets(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		TelegramBotToken:       testBotToken,
		SentryDSN:              "https://key@sentry.example.com/1",
		PaymentEnabled:         true,
		PaymentProvider:        "telegram_stars",
		PlategaCallbackEnabled: true,
		StarsInvoiceTTL:        DefaultStarsInvoiceTTL,
		PlategaMerchantID:      "6f9619ff-8b86-d011-b42d-00c04fc964ff",
		PlategaSecret:          "supersecret",
	}

	s := cfg.String()
	assert.Contains(t, s, "PaymentEnabled=true", "PaymentEnabled")
	assert.Contains(t, s, "PaymentProvider=telegram_stars", "PaymentProvider")
	assert.Contains(t, s, "PlategaCallbackEnabled=true", "PlategaCallbackEnabled")
	assert.Contains(t, s, "StarsInvoiceTTL=15m0s", "StarsInvoiceTTL")
	assert.NotContains(t, s, "supersecret", "PlategaSecret не должен попадать в String()")
	assert.NotContains(t, s, "6f9619ff", "PlategaMerchantID не должен попадать в String()")
}
