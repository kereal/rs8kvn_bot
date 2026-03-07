package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"tgvpn_go/internal/backup"
	"tgvpn_go/internal/bot"
	"tgvpn_go/internal/config"
	"tgvpn_go/internal/database"
	"tgvpn_go/internal/logger"
	"tgvpn_go/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	if err := logger.Init(cfg.LogFilePath, cfg.LogLevel); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	logger.Info("Starting TGVPN Bot...")

	// Initialize database
	if err := database.Init(cfg.DatabasePath); err != nil {
		logger.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()
	logger.Info("Database initialized successfully")

	// Initialize 3x-ui client
	xuiClient := xui.NewClient(cfg.XUIHost, cfg.XUIUsername, cfg.XUIPassword)

	// Validate 3x-ui connection
	logger.Info("Connecting to 3x-ui panel...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := xuiClient.Login(ctx); err != nil {
		cancel()
		logger.Fatalf("Failed to connect to 3x-ui panel: %v", err)
	}
	cancel()
	logger.Info("✓ 3x-ui panel connected")

	// Validate Telegram bot token
	logger.Info("Validating Telegram bot token...")
	botAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		logger.Fatalf("Invalid Telegram bot token: %v", err)
	}
	logger.Infof("✓ Telegram bot authorized: @%s", botAPI.Self.UserName)

	// Create bot handler
	handler := bot.NewHandler(botAPI, cfg, xuiClient)

	// Get updates channel
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := botAPI.GetUpdatesChan(u)

	// Create context for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	logger.Info("Bot started successfully")

	// WaitGroup to track backup scheduler goroutine
	var wg sync.WaitGroup
	wg.Add(1)

	// Start daily backup scheduler (at 3 AM) with panic recovery
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("Backup scheduler panicked: %v", r)
			}
			wg.Done()
		}()
		startBackupScheduler(ctx, cfg.DatabasePath)
	}()

	// Handle updates with panic recovery
	for {
		select {
		case update := <-updates:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Errorf("Panic in update handler: %v", r)
					}
				}()
				handleUpdate(handler, update)
			}()

		case <-ctx.Done():
			logger.Info("Graceful shutdown initiated...")
			botAPI.StopReceivingUpdates()

			// Wait for backup scheduler to finish (with timeout)
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()

			select {
			case <-done:
				logger.Info("Backup scheduler stopped successfully")
			case <-time.After(30 * time.Second):
				logger.Warn("Timeout waiting for backup scheduler to stop")
			}

			logger.Info("Bot stopped successfully")
			return
		}
	}
}

func handleUpdate(handler *bot.Handler, update tgbotapi.Update) {
	if update.Message != nil {
		if update.Message.IsCommand() {
			switch update.Message.Command() {
			case "start":
				handler.HandleStart(update)
			default:
				handler.SendMessage(context.Background(), update.Message.Chat.ID, "Неизвестная команда. Используйте /start")
			}
		} else {
			handler.SendMessage(context.Background(), update.Message.Chat.ID, "Пожалуйста, используйте кнопки для взаимодействия с ботом.")
		}
	} else if update.CallbackQuery != nil {
		handler.HandleCallback(update)
	}
}

// startBackupScheduler runs daily backups at 3 AM
func startBackupScheduler(ctx context.Context, dbPath string) {
	logger.Info("Backup scheduler started (daily at 03:00)")

	for {
		// Calculate time until next 3 AM
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}

		sleepDuration := time.Until(next)
		logger.Infof("Next backup in %v", sleepDuration.Round(time.Minute))

		select {
		case <-time.After(sleepDuration):
			logger.Info("Running scheduled database backup...")
			if err := backup.DailyBackup(dbPath, 7); err != nil {
				logger.Errorf("Backup failed: %v", err)
			} else {
				logger.Info("Database backup completed successfully")
			}

		case <-ctx.Done():
			logger.Info("Backup scheduler stopped")
			return
		}
	}
}
