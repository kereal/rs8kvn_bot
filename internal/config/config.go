package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	TelegramBotToken string
	TelegramAdminID  int64

	XUIHost      string
	XUIUsername  string
	XUIPassword  string
	XUIInboundID int
	XUISubPath   string

	DatabasePath string

	LogFilePath string
	LogLevel    string

	TrafficLimitGB int

	HeartbeatURL      string
	HeartbeatInterval int
}

func Load() (*Config, error) {
	// Load .env file if it exists, but don't fail if it doesn't
	// This allows the application to work with just environment variables
	godotenv.Load()

	telegramAdminID, err := strconv.ParseInt(getEnv("TELEGRAM_ADMIN_ID", "0"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid TELEGRAM_ADMIN_ID: %w", err)
	}

	xuiInboundID, err := strconv.Atoi(getEnv("XUI_INBOUND_ID", "1"))
	if err != nil {
		return nil, fmt.Errorf("invalid XUI_INBOUND_ID: %w", err)
	}

	trafficLimitGB, err := strconv.Atoi(getEnv("TRAFFIC_LIMIT_GB", "100"))
	if err != nil {
		return nil, fmt.Errorf("invalid TRAFFIC_LIMIT_GB: %w", err)
	}

	heartbeatInterval, err := strconv.Atoi(getEnv("HEARTBEAT_INTERVAL", "300"))
	if err != nil {
		return nil, fmt.Errorf("invalid HEARTBEAT_INTERVAL: %w", err)
	}

	config := &Config{
		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramAdminID:  telegramAdminID,

		XUIHost:      getEnv("XUI_HOST", "http://localhost:2053"),
		XUIUsername:  getEnv("XUI_USERNAME", "admin"),
		XUIPassword:  getEnv("XUI_PASSWORD", "admin"),
		XUIInboundID: xuiInboundID,
		XUISubPath:   getEnv("XUI_SUB_PATH", "sub"),

		DatabasePath: getEnv("DATABASE_PATH", "./data/tgvpn.db"),

		LogFilePath: getEnv("LOG_FILE_PATH", "./data/bot.log"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),

		TrafficLimitGB: trafficLimitGB,

		HeartbeatURL:      getEnv("HEARTBEAT_URL", ""),
		HeartbeatInterval: heartbeatInterval,
	}

	if config.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}

	if config.XUIHost == "" {
		return nil, fmt.Errorf("XUI_HOST is required")
	}
	if config.XUIUsername == "" {
		return nil, fmt.Errorf("XUI_USERNAME is required")
	}
	if config.XUIPassword == "" {
		return nil, fmt.Errorf("XUI_PASSWORD is required")
	}

	if config.TrafficLimitGB < 1 || config.TrafficLimitGB > 1000 {
		return nil, fmt.Errorf("TRAFFIC_LIMIT_GB must be between 1 and 1000")
	}

	if config.XUIInboundID < 1 {
		return nil, fmt.Errorf("XUI_INBOUND_ID must be positive")
	}

	if config.TelegramAdminID < 0 {
		return nil, fmt.Errorf("TELEGRAM_ADMIN_ID must be non-negative")
	}

	return config, nil
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
