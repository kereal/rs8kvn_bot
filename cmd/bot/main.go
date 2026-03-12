package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"rs8kvn_bot/internal/backup"
	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/heartbeat"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/xui"

	"github.com/getsentry/sentry-go"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize Sentry for error tracking
	if cfg.SentryDSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			Environment:      "production",
			Release:          "rs8kvn_bot@1.5.1",
			TracesSampleRate: 0.1,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Sentry: %v\n", err)
		} else {
			defer sentry.Flush(5 * time.Second)
			logger.Info("Sentry error tracking initialized")
		}
	}

	if err := logger.Init(cfg.LogFilePath, cfg.LogLevel); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	logger.Info("Starting rs8kvn_bot...")

	if err := database.Init(cfg.DatabasePath); err != nil {
		logger.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()
	logger.Info("Database initialized successfully")

	xuiClient := xui.NewClient(cfg.XUIHost, cfg.XUIUsername, cfg.XUIPassword)

	logger.Info("Connecting to 3x-ui panel...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := xuiClient.Login(ctx); err != nil {
		cancel()
		logger.Fatalf("Failed to connect to 3x-ui panel: %v", err)
	}
	cancel()
	logger.Info("✓ 3x-ui panel connected")

	logger.Info("Validating Telegram bot token...")
	botAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		logger.Fatalf("Invalid Telegram bot token: %v", err)
	}
	logger.Infof("✓ Telegram bot authorized: @%s", botAPI.Self.UserName)

	handler := bot.NewHandler(botAPI, cfg, xuiClient)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	// Explicitly request all update types including callback queries
	u.AllowedUpdates = []string{"message", "callback_query", "edited_message", "channel_post", "edited_channel_post", "inline_query", "chosen_inline_result", "shipping_query", "pre_checkout_query", "poll", "poll_answer"}
	updates := botAPI.GetUpdatesChan(u)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	logger.Info("Bot started successfully")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				sentry.CurrentHub().Recover(r)
				sentry.Flush(2 * time.Second)
				logger.Errorf("Backup scheduler panicked: %v", r)
			}
			wg.Done()
		}()
		startBackupScheduler(ctx, cfg.DatabasePath)
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				sentry.CurrentHub().Recover(r)
				sentry.Flush(2 * time.Second)
				logger.Errorf("Heartbeat scheduler panicked: %v", r)
			}
			wg.Done()
		}()
		heartbeat.Start(ctx, cfg.HeartbeatURL, cfg.HeartbeatInterval)
	}()

	for {
		select {
		case update := <-updates:
			func() {
				defer func() {
					if r := recover(); r != nil {
						sentry.CurrentHub().Recover(r)
						sentry.Flush(2 * time.Second)
						logger.Errorf("Panic in update handler: %v", r)
					}
				}()
				handleUpdate(handler, update)
			}()

		case <-ctx.Done():
			logger.Info("Graceful shutdown initiated...")
			botAPI.StopReceivingUpdates()

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
			case "help":
				handler.HandleHelp(update)
			default:
				handler.SendMessage(context.Background(), update.Message.Chat.ID, "Неизвестная команда. Используйте /start или /help")
			}
		} else {
			handler.SendMessage(context.Background(), update.Message.Chat.ID, "Пожалуйста, используйте кнопки для взаимодействия с ботом.")
		}
	} else if update.CallbackQuery != nil {
		handler.HandleCallback(update)
	}
}

func startBackupScheduler(ctx context.Context, dbPath string) {
	logger.Info("Backup scheduler started (daily at 03:00)")

	for {
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
