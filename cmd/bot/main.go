package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
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

// Build information (set via ldflags)
var (
	version   = "dev"
	buildTime = "unknown"
)

// getVersion returns the current version from build info or git tag
func getVersion() string {
	// Try to get version from Go build info (set by go install or git tags)
	if info, ok := debug.ReadBuildInfo(); ok {
		// Check for vcs tag (git tag)
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				// If we have a tagged version, use it
				if info.Main.Version != "" && info.Main.Version != "(devel)" {
					return "rs8kvn_bot@" + info.Main.Version
				}
				// Otherwise use short commit hash
				if len(setting.Value) >= 7 {
					return "rs8kvn_bot@" + setting.Value[:7]
				}
			}
		}
		// Fallback to module version if available
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return "rs8kvn_bot@" + info.Main.Version
		}
	}
	// Default version if no build info available
	return "rs8kvn_bot@" + version
}

func main() {
	// Load configuration first
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize Sentry for error tracking (before logger)
	if cfg.SentryDSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			Environment:      "production",
			Release:          getVersion(),
			TracesSampleRate: config.SentryTracesSampleRate,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Sentry: %v\n", err)
		} else {
			defer sentry.Flush(config.SentryFlushTimeout)
			fmt.Println("Sentry error tracking initialized")
		}
	}

	// Initialize logger
	if err := logger.Init(cfg.LogFilePath, cfg.LogLevel); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	// Redirect standard log output (from third-party libraries) to our logger
	logger.RedirectStdLog()

	logger.Infof("Starting %s (built: %s)", getVersion(), buildTime)
	logger.Infof("Configuration: %s", cfg.String())

	// Initialize database
	if err := database.Init(cfg.DatabasePath); err != nil {
		logger.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()
	logger.Info("Database initialized successfully")

	// Initialize 3x-ui client
	xuiClient := xui.NewClient(cfg.XUIHost, cfg.XUIUsername, cfg.XUIPassword)

	// Connect to 3x-ui panel with timeout
	logger.Info("Connecting to 3x-ui panel...")
	ctx, cancel := context.WithTimeout(context.Background(), config.DefaultHTTPTimeout)
	if err := xuiClient.Login(ctx); err != nil {
		cancel()
		logger.Fatalf("Failed to connect to 3x-ui panel: %v", err)
	}
	cancel()
	logger.Info("✓ 3x-ui panel connected")

	// Initialize Telegram bot
	logger.Info("Validating Telegram bot token...")
	botAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		logger.Fatalf("Invalid Telegram bot token: %v", err)
	}
	logger.Infof("✓ Telegram bot authorized: @%s", botAPI.Self.UserName)

	// Create bot handler
	handler := bot.NewHandler(botAPI, cfg, xuiClient)

	// Configure update listener
	u := tgbotapi.NewUpdate(0)
	u.Timeout = config.BotUpdateTimeout
	u.AllowedUpdates = []string{"message", "callback_query"}
	updates := botAPI.GetUpdatesChan(u)

	// Setup graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	logger.Info("Bot started successfully")

	var wg sync.WaitGroup
	wg.Add(2)

	// Start backup scheduler
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sentry.CurrentHub().Recover(r)
				sentry.Flush(config.SentryPanicFlushTimeout)
				logger.Errorf("Backup scheduler panicked: %v", r)
			}
			wg.Done()
		}()
		startBackupScheduler(ctx, cfg.DatabasePath)
	}()

	// Start heartbeat monitor
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sentry.CurrentHub().Recover(r)
				sentry.Flush(config.SentryPanicFlushTimeout)
				logger.Errorf("Heartbeat scheduler panicked: %v", r)
			}
			wg.Done()
		}()
		heartbeat.Start(ctx, cfg.HeartbeatURL, cfg.HeartbeatInterval)
	}()

	// Main event loop
	for {
		select {
		case update := <-updates:
			// Process update in a separate goroutine for better concurrency
			go handleUpdateSafely(ctx, handler, update)

		case <-ctx.Done():
			logger.Info("Graceful shutdown initiated...")
			botAPI.StopReceivingUpdates()

			// Wait for background tasks with timeout
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()

			select {
			case <-done:
				logger.Info("All background tasks stopped successfully")
			case <-time.After(config.ShutdownTimeout):
				logger.Warn("Timeout waiting for background tasks to stop")
			}

			logger.Info("Bot stopped successfully")
			return
		}
	}
}

// handleUpdateSafely handles a Telegram update with panic recovery.
func handleUpdateSafely(ctx context.Context, handler *bot.Handler, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			sentry.CurrentHub().Recover(r)
			sentry.Flush(config.SentryPanicFlushTimeout)
			logger.Errorf("Panic in update handler: %v", r)
		}
	}()

	handleUpdate(ctx, handler, update)
}

// handleUpdate routes the update to the appropriate handler.
func handleUpdate(ctx context.Context, handler *bot.Handler, update tgbotapi.Update) {
	if update.Message != nil {
		if update.Message.IsCommand() {
			switch update.Message.Command() {
			case "start":
				handler.HandleStart(ctx, update)
			case "help":
				handler.HandleHelp(ctx, update)
			default:
				handler.SendMessage(ctx, update.Message.Chat.ID,
					"Неизвестная команда. Используйте /start или /help")
			}
		} else {
			handler.SendMessage(ctx, update.Message.Chat.ID,
				"Пожалуйста, используйте кнопки для взаимодействия с ботом.")
		}
	} else if update.CallbackQuery != nil {
		handler.HandleCallback(ctx, update)
	}
}

// startBackupScheduler runs the database backup scheduler.
func startBackupScheduler(ctx context.Context, dbPath string) {
	logger.Info("Backup scheduler started (daily at 03:00)")

	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(),
			config.DefaultBackupHour, 0, 0, 0, now.Location())
		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}

		sleepDuration := time.Until(next)
		logger.Infof("Next backup in %v", sleepDuration.Round(time.Minute))

		select {
		case <-time.After(sleepDuration):
			logger.Info("Running scheduled database backup...")
			if err := backup.DailyBackup(dbPath, config.DefaultBackupRetention); err != nil {
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
