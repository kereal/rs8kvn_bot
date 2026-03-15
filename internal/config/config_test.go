package config

import (
	"os"
	"testing"
)

func TestLoad_DefaultValues(t *testing.T) {
	// Set required environment variables
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	// Unset optional variables to test defaults
	os.Unsetenv("TELEGRAM_ADMIN_ID")
	os.Unsetenv("XUI_INBOUND_ID")
	os.Unsetenv("XUI_SUB_PATH")
	os.Unsetenv("DATABASE_PATH")
	os.Unsetenv("LOG_FILE_PATH")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("TRAFFIC_LIMIT_GB")
	os.Unsetenv("HEARTBEAT_URL")
	os.Unsetenv("HEARTBEAT_INTERVAL")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Test required values
	if cfg.TelegramBotToken != "123456789:test_token" {
		t.Errorf("TelegramBotToken = %v, want 123456789:test_token", cfg.TelegramBotToken)
	}
	if cfg.XUIHost != "http://localhost:2053" {
		t.Errorf("XUIHost = %v, want http://localhost:2053", cfg.XUIHost)
	}

	// Test default values
	if cfg.TelegramAdminID != 0 {
		t.Errorf("TelegramAdminID = %v, want 0", cfg.TelegramAdminID)
	}
	if cfg.XUIInboundID != 1 {
		t.Errorf("XUIInboundID = %v, want 1", cfg.XUIInboundID)
	}
	if cfg.XUISubPath != "sub" {
		t.Errorf("XUISubPath = %v, want sub", cfg.XUISubPath)
	}
	if cfg.DatabasePath != "./data/tgvpn.db" {
		t.Errorf("DatabasePath = %v, want ./data/tgvpn.db", cfg.DatabasePath)
	}
	if cfg.LogFilePath != "./data/bot.log" {
		t.Errorf("LogFilePath = %v, want ./data/bot.log", cfg.LogFilePath)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.TrafficLimitGB != 100 {
		t.Errorf("TrafficLimitGB = %v, want 100", cfg.TrafficLimitGB)
	}
	if cfg.HeartbeatURL != "" {
		t.Errorf("HeartbeatURL = %v, want empty", cfg.HeartbeatURL)
	}
	if cfg.HeartbeatInterval != 300 {
		t.Errorf("HeartbeatInterval = %v, want 300", cfg.HeartbeatInterval)
	}
	if cfg.SentryDSN != "" {
		t.Errorf("SentryDSN = %v, want empty", cfg.SentryDSN)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	// Set all environment variables with custom values
	os.Setenv("TELEGRAM_BOT_TOKEN", "987654321:custom_token")
	os.Setenv("TELEGRAM_ADMIN_ID", "123456789")
	os.Setenv("XUI_HOST", "http://custom.host:8080")
	os.Setenv("XUI_USERNAME", "custom_user")
	os.Setenv("XUI_PASSWORD", "custom_pass")
	os.Setenv("XUI_INBOUND_ID", "5")
	os.Setenv("XUI_SUB_PATH", "custom_sub")
	os.Setenv("DATABASE_PATH", "/custom/path/db.db")
	os.Setenv("LOG_FILE_PATH", "/custom/path/log.log")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("TRAFFIC_LIMIT_GB", "200")
	os.Setenv("HEARTBEAT_URL", "https://monitor.example.com/heartbeat")
	os.Setenv("HEARTBEAT_INTERVAL", "600")
	os.Setenv("SENTRY_DSN", "https://test@sentry.example.com/123")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("XUI_SUB_PATH")
		os.Unsetenv("DATABASE_PATH")
		os.Unsetenv("LOG_FILE_PATH")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TRAFFIC_LIMIT_GB")
		os.Unsetenv("HEARTBEAT_URL")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Unsetenv("SENTRY_DSN")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.TelegramBotToken != "987654321:custom_token" {
		t.Errorf("TelegramBotToken = %v, want 987654321:custom_token", cfg.TelegramBotToken)
	}
	if cfg.TelegramAdminID != 123456789 {
		t.Errorf("TelegramAdminID = %v, want 123456789", cfg.TelegramAdminID)
	}
	if cfg.XUIHost != "http://custom.host:8080" {
		t.Errorf("XUIHost = %v, want http://custom.host:8080", cfg.XUIHost)
	}
	if cfg.XUIUsername != "custom_user" {
		t.Errorf("XUIUsername = %v, want custom_user", cfg.XUIUsername)
	}
	if cfg.XUIPassword != "custom_pass" {
		t.Errorf("XUIPassword = %v, want custom_pass", cfg.XUIPassword)
	}
	if cfg.XUIInboundID != 5 {
		t.Errorf("XUIInboundID = %v, want 5", cfg.XUIInboundID)
	}
	if cfg.XUISubPath != "custom_sub" {
		t.Errorf("XUISubPath = %v, want custom_sub", cfg.XUISubPath)
	}
	if cfg.DatabasePath != "/custom/path/db.db" {
		t.Errorf("DatabasePath = %v, want /custom/path/db.db", cfg.DatabasePath)
	}
	if cfg.LogFilePath != "/custom/path/log.log" {
		t.Errorf("LogFilePath = %v, want /custom/path/log.log", cfg.LogFilePath)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.TrafficLimitGB != 200 {
		t.Errorf("TrafficLimitGB = %v, want 200", cfg.TrafficLimitGB)
	}
	if cfg.HeartbeatURL != "https://monitor.example.com/heartbeat" {
		t.Errorf("HeartbeatURL = %v, want https://monitor.example.com/heartbeat", cfg.HeartbeatURL)
	}
	if cfg.HeartbeatInterval != 600 {
		t.Errorf("HeartbeatInterval = %v, want 600", cfg.HeartbeatInterval)
	}
	if cfg.SentryDSN != "https://test@sentry.example.com/123" {
		t.Errorf("SentryDSN = %v, want https://test@sentry.example.com/123", cfg.SentryDSN)
	}
}

func TestLoad_MissingBotToken(t *testing.T) {
	os.Unsetenv("TELEGRAM_BOT_TOKEN")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should return error when TELEGRAM_BOT_TOKEN is missing")
	}
}

func TestLoad_MissingXUIHost(t *testing.T) {
	// XUI_HOST has a default value, so unset will use default
	// This test verifies that the default is applied
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Unsetenv("XUI_HOST")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not return error when XUI_HOST is missing (default applied): %v", err)
	}
	if cfg.XUIHost != "http://localhost:2053" {
		t.Errorf("XUIHost = %v, want default http://localhost:2053", cfg.XUIHost)
	}
}

func TestLoad_MissingXUIUsername(t *testing.T) {
	// XUI_USERNAME has a default value, so unset will use default
	// This test verifies that the default is applied
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Unsetenv("XUI_USERNAME")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_PASSWORD")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not return error when XUI_USERNAME is missing (default applied): %v", err)
	}
	if cfg.XUIUsername != "admin" {
		t.Errorf("XUIUsername = %v, want default admin", cfg.XUIUsername)
	}
}

func TestLoad_MissingXUIPassword(t *testing.T) {
	// XUI_PASSWORD has a default value, so unset will use default
	// This test verifies that the default is applied
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Unsetenv("XUI_PASSWORD")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not return error when XUI_PASSWORD is missing (default applied): %v", err)
	}
	if cfg.XUIPassword != "admin" {
		t.Errorf("XUIPassword = %v, want default admin", cfg.XUIPassword)
	}
}

func TestLoad_InvalidTelegramAdminID(t *testing.T) {
	// When TELEGRAM_ADMIN_ID is invalid, it falls back to default value (0)
	// This is a design decision for more resilient configuration
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("TELEGRAM_ADMIN_ID", "invalid")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TELEGRAM_ADMIN_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not return error for invalid TELEGRAM_ADMIN_ID (falls back to default): %v", err)
	}
	if cfg.TelegramAdminID != 0 {
		t.Errorf("TelegramAdminID = %v, want 0 (default)", cfg.TelegramAdminID)
	}
}

func TestLoad_InvalidXUIInboundID(t *testing.T) {
	// When XUI_INBOUND_ID is invalid, it falls back to default value (1)
	// This is a design decision for more resilient configuration
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("XUI_INBOUND_ID", "invalid")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not return error for invalid XUI_INBOUND_ID (falls back to default): %v", err)
	}
	if cfg.XUIInboundID != 1 {
		t.Errorf("XUIInboundID = %v, want 1 (default)", cfg.XUIInboundID)
	}
}

func TestLoad_InvalidTrafficLimitGB_TooLow(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("TRAFFIC_LIMIT_GB", "0")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TRAFFIC_LIMIT_GB")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should return error for TRAFFIC_LIMIT_GB < 1")
	}
}

func TestLoad_InvalidTrafficLimitGB_TooHigh(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("TRAFFIC_LIMIT_GB", "1001")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("TRAFFIC_LIMIT_GB")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should return error for TRAFFIC_LIMIT_GB > 1000")
	}
}

func TestLoad_InvalidXUIInboundID_Negative(t *testing.T) {
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("XUI_INBOUND_ID", "-1")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("XUI_INBOUND_ID")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should return error for negative XUI_INBOUND_ID")
	}
}

func TestLoad_InvalidHeartbeatInterval(t *testing.T) {
	// When HEARTBEAT_INTERVAL is invalid, it falls back to default value (300)
	// This is a design decision for more resilient configuration
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456789:test_token")
	os.Setenv("HEARTBEAT_INTERVAL", "invalid")
	os.Setenv("XUI_HOST", "http://localhost:2053")
	os.Setenv("XUI_USERNAME", "admin")
	os.Setenv("XUI_PASSWORD", "admin")

	defer func() {
		os.Unsetenv("TELEGRAM_BOT_TOKEN")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Unsetenv("XUI_HOST")
		os.Unsetenv("XUI_USERNAME")
		os.Unsetenv("XUI_PASSWORD")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not return error for invalid HEARTBEAT_INTERVAL (falls back to default): %v", err)
	}
	if cfg.HeartbeatInterval != 300 {
		t.Errorf("HeartbeatInterval = %v, want 300 (default)", cfg.HeartbeatInterval)
	}
}

func TestGetEnv_DefaultValue(t *testing.T) {
	os.Unsetenv("NON_EXISTENT_VAR")

	result := getEnv("NON_EXISTENT_VAR", "default_value")
	if result != "default_value" {
		t.Errorf("getEnv() = %v, want default_value", result)
	}
}

func TestGetEnv_ExistingValue(t *testing.T) {
	os.Setenv("EXISTING_VAR", "existing_value")
	defer os.Unsetenv("EXISTING_VAR")

	result := getEnv("EXISTING_VAR", "default_value")
	if result != "existing_value" {
		t.Errorf("getEnv() = %v, want existing_value", result)
	}
}
