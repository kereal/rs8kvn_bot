package config

import (
	"os"
	"testing"
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
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.TelegramBotToken != "123456789:ABCdefGHIjklMNOpqrsTUVwxyz" {
		t.Errorf("TelegramBotToken = %s", cfg.TelegramBotToken)
	}
	if cfg.TelegramAdminID != 123456 {
		t.Errorf("TelegramAdminID = %d", cfg.TelegramAdminID)
	}
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
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.TrafficLimitGB != 500 {
		t.Errorf("TrafficLimitGB = %d, want 500", cfg.TrafficLimitGB)
	}
	if cfg.HeartbeatInterval != 120 {
		t.Errorf("HeartbeatInterval = %d, want 120", cfg.HeartbeatInterval)
	}
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
	if err == nil {
		t.Error("Load() should error when BOT_TOKEN is empty")
	}
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
	if err != nil {
		t.Fatalf("Load() should use default for invalid TELEGRAM_ADMIN_ID, got error: %v", err)
	}
	if cfg.TelegramAdminID != 0 {
		t.Errorf("TelegramAdminID = %d, want 0 (default)", cfg.TelegramAdminID)
	}
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
	if err != nil {
		t.Fatalf("Load() should use default for invalid XUI_INBOUND_ID, got error: %v", err)
	}
	if cfg.XUIInboundID != 1 {
		t.Errorf("XUIInboundID = %d, want 1 (default)", cfg.XUIInboundID)
	}
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
	if err == nil {
		t.Error("Load() should error for TRAFFIC_LIMIT_GB = 0")
	}
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
	if err == nil {
		t.Error("Load() should error for TRAFFIC_LIMIT_GB > 10000")
	}
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
	if err == nil {
		t.Error("Load() should error for negative XUI_INBOUND_ID")
	}
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
	if err != nil {
		t.Fatalf("Load() should use default for invalid HEARTBEAT_INTERVAL, got error: %v", err)
	}
	if cfg.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Errorf("HeartbeatInterval = %d, want %d (default)", cfg.HeartbeatInterval, DefaultHeartbeatInterval)
	}
}

func TestGetEnv_DefaultValue(t *testing.T) {
	os.Unsetenv("TEST_KEY")
	result := getEnv("TEST_KEY", "default")
	if result != "default" {
		t.Errorf("getEnv() = %s, want default", result)
	}
}

func TestGetEnv_ExistingValue(t *testing.T) {
	os.Setenv("TEST_KEY", "value")
	defer os.Unsetenv("TEST_KEY")
	result := getEnv("TEST_KEY", "default")
	if result != "value" {
		t.Errorf("getEnv() = %s, want value", result)
	}
}

func TestGetEnv_WhitespaceTrimmed(t *testing.T) {
	os.Setenv("TEST_KEY", "  value  ")
	defer os.Unsetenv("TEST_KEY")
	result := getEnv("TEST_KEY", "default")
	if result != "value" {
		t.Errorf("getEnv() = %q, want 'value'", result)
	}
}

func TestParseEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")
	result := parseEnvInt("TEST_INT", 0)
	if result != 42 {
		t.Errorf("parseEnvInt() = %d, want 42", result)
	}
}

func TestParseEnvInt_Default(t *testing.T) {
	os.Unsetenv("TEST_INT")
	result := parseEnvInt("TEST_INT", 100)
	if result != 100 {
		t.Errorf("parseEnvInt() = %d, want 100", result)
	}
}

func TestParseEnvInt_Invalid(t *testing.T) {
	os.Setenv("TEST_INT", "invalid")
	defer os.Unsetenv("TEST_INT")
	result := parseEnvInt("TEST_INT", 0)
	if result != 0 {
		t.Errorf("parseEnvInt() = %d, want 0", result)
	}
}

func TestParseEnvInt64(t *testing.T) {
	os.Setenv("TEST_INT64", "9999999999")
	defer os.Unsetenv("TEST_INT64")
	result := parseEnvInt64("TEST_INT64", 0)
	if result != 9999999999 {
		t.Errorf("parseEnvInt64() = %d, want 9999999999", result)
	}
}

func TestParseEnvInt64_Default(t *testing.T) {
	os.Unsetenv("TEST_INT64")
	result := parseEnvInt64("TEST_INT64", 500)
	if result != 500 {
		t.Errorf("parseEnvInt64() = %d, want 500", result)
	}
}

func TestParseEnvInt64_Invalid(t *testing.T) {
	os.Setenv("TEST_INT64", "invalid")
	defer os.Unsetenv("TEST_INT64")
	result := parseEnvInt64("TEST_INT64", 0)
	if result != 0 {
		t.Errorf("parseEnvInt64() = %d, want 0", result)
	}
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
			if (err != nil) != tt.wantErr {
				t.Errorf("validateURL() error = %v, wantErr %v", err, tt.wantErr)
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

	if !contains(str, "TelegramBotToken") {
		t.Error("String() should include TelegramBotToken")
	}
	if !contains(str, "123456") {
		t.Error("String() should include TelegramAdminID")
	}
	if contains(str, "secret") {
		t.Error("String() should mask password")
	}
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
			if result != tt.expected {
				t.Errorf("maskURL(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

func TestMaskURL_InvalidURL(t *testing.T) {
	result := maskURL("not a valid url")
	if result != ":///***" {
		t.Errorf("maskURL() for invalid URL = %q, want ':///***'", result)
	}
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
	if err == nil {
		t.Error("validate() should error on empty XUIHost")
	}
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
	if err == nil {
		t.Error("validate() should error on empty XUIUsername")
	}
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
	if err == nil {
		t.Error("validate() should error on empty XUIPassword")
	}
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
	if err == nil {
		t.Error("validate() should error on zero AdminID")
	}
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
	if err == nil {
		t.Error("validate() should error on zero InboundID")
	}
}

func TestConfig_Validate_Valid(t *testing.T) {
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
	}

	err := cfg.validate()
	if err != nil {
		t.Errorf("validate() error = %v", err)
	}
}

func TestConfig_Validate_SentryDSN_Valid(t *testing.T) {
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
		SentryDSN:         "https://abc@sentry.io/123",
	}

	err := cfg.validate()
	if err != nil {
		t.Errorf("validate() error = %v", err)
	}
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
	if err == nil {
		t.Error("validate() should error on invalid SentryDSN")
	}
}

func TestConfig_Validate_WithSubPath(t *testing.T) {
	cfg := &Config{
		TelegramBotToken:  "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
		TelegramAdminID:   123456,
		XUIHost:           "http://localhost:2053",
		XUIUsername:       "admin",
		XUIPassword:       "password",
		XUIInboundID:      1,
		XUISubPath:        "/custom",
		TrafficLimitGB:    100,
		HeartbeatInterval: 60,
		LogLevel:          "info",
	}

	err := cfg.validate()
	if err != nil {
		t.Errorf("validate() error = %v", err)
	}
}

func TestConfig_Validate_WithHeartbeatURL(t *testing.T) {
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
		HeartbeatURL:      "https://health.example.com",
	}

	err := cfg.validate()
	if err != nil {
		t.Errorf("validate() error = %v", err)
	}
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
	if err == nil {
		t.Error("validate() should error on invalid token format")
	}
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
	if err == nil {
		t.Error("validate() should error on negative AdminID")
	}
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
	if err == nil {
		t.Error("validate() should error on invalid LogLevel")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
