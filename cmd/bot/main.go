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
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/web"
	"rs8kvn_bot/internal/xui"

	"github.com/getsentry/sentry-go"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Build information (set via ldflags)
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

// getVersion returns the current version from build info or git tag
func getVersion() string {
	// If version was set via ldflags and is not "dev", use it
	if version != "dev" {
		return "rs8kvn_bot@" + version
	}

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

	// If commit was set via ldflags, use it
	if commit != "unknown" && len(commit) >= 7 {
		return "rs8kvn_bot@" + commit[:7]
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
			TracesSampleRate: logger.SentryTracesSampleRate,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Sentry: %v\n", err)
		} else {
			defer sentry.Flush(logger.SentryFlushTimeout)
			fmt.Fprintln(os.Stderr, "Sentry error tracking initialized")
		}
	}

	// Initialize logger
	logService, err := logger.Init(cfg.LogFilePath, cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		sentry.Flush(logger.SentryFlushTimeout) // flush before exit
		os.Exit(1)                              //nolint:gocritic // flush called explicitly above
	}
	defer func() {
		if err := logService.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to close logger: %v\n", err)
		}
	}()

	// Redirect standard log output (from third-party libraries) to our logger
	logger.RedirectStdLog()

	logger.Info("Starting bot",
		zap.String("version", getVersion()),
		zap.String("built", buildTime))
	logger.Info("Configuration loaded", zap.String("config", cfg.String()))

	// Initialize database with Service pattern for dependency injection
	dbService, err := database.NewService(cfg.DatabasePath)
	if err != nil {
		logger.Fatal("Failed to initialize database", zap.Error(err))
	}
	defer func() {
		if err := dbService.Close(); err != nil {
			logger.Error("Failed to close database", zap.Error(err))
		}
	}()
	logger.Info("Database initialized successfully")

	// Initialize 3x-ui client
	xuiClient, err := xui.NewClient(cfg.XUIHost, cfg.XUIUsername, cfg.XUIPassword)
	if err != nil {
		logger.Fatal("Failed to initialize 3x-ui client", zap.Error(err))
	}
	defer func() {
		if err := xuiClient.Close(); err != nil {
			logger.Error("Failed to close 3x-ui client", zap.Error(err))
		}
	}()

	// Connect to 3x-ui panel with timeout
	logger.Info("Connecting to 3x-ui panel")
	ctx, cancel := context.WithTimeout(context.Background(), config.DefaultHTTPTimeout)
	if err := xuiClient.Login(ctx); err != nil {
		cancel()
		logger.Fatal("Failed to connect to 3x-ui panel", zap.Error(err))
	}
	cancel()
	logger.Info("3x-ui panel connected")

	// Initialize Telegram bot
	logger.Info("Validating Telegram bot token")
	botAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		logger.Fatal("Invalid Telegram bot token", zap.Error(err))
	}

	botConfig, err := bot.NewBotConfig(botAPI)
	if err != nil {
		logger.Fatal("Failed to get bot config", zap.Error(err))
	}
	logger.Info("Telegram bot authorized", zap.String("username", botConfig.Username))

	// Create subscription service (shared between bot handler and web server)
	subService := service.NewSubscriptionService(dbService, xuiClient, cfg)

	// Create bot handler
	handler := bot.NewHandler(botAPI, cfg, dbService, xuiClient, botConfig, subService)

	// Start cache cleanup goroutine to prevent memory leaks
	handler.StartCacheCleanup(ctx, bot.CacheTTL/2)

	// Start rate limiter cleanup goroutine to remove stale user buckets
	handler.StartRateLimiterCleanup(ctx, bot.CacheTTL, bot.CacheTTL*2)

	// Start referral cache sync goroutine to keep referral counts up-to-date
	handler.StartReferralCacheSync(ctx)

	// Initialize and start web server (health + trial pages)
	webServer := web.NewServer(fmt.Sprintf(":%d", cfg.HealthCheckPort), dbService, xuiClient, cfg, botConfig, subService)
	webServer.RegisterChecker("database", func(ctx context.Context) web.ComponentHealth {
		if err := dbService.Ping(ctx); err != nil {
			return web.ComponentHealth{Status: web.StatusDown, Message: err.Error()}
		}
		return web.ComponentHealth{Status: web.StatusOK}
	})
	webServer.RegisterChecker("xui", func(ctx context.Context) web.ComponentHealth {
		if err := xuiClient.Ping(ctx); err != nil {
			return web.ComponentHealth{Status: web.StatusDegraded, Message: err.Error()}
		}
		return web.ComponentHealth{Status: web.StatusOK}
	})
	if err := webServer.Start(context.Background()); err != nil {
		logger.Fatal("Failed to start web server", zap.Error(err))
	}
	webServer.SetReady(true)
	defer func() {
		webServer.SetReady(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := webServer.Stop(shutdownCtx); err != nil {
			logger.Error("Failed to stop web server", zap.Error(err))
		}
	}()

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

	// Channel to limit concurrent update handlers (worker pool)
	// This prevents unbounded goroutine spawning
	updateSem := make(chan struct{}, config.MaxConcurrentHandlers)

	// Start backup scheduler
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sentry.CurrentHub().Recover(r)
				sentry.Flush(logger.SentryPanicFlushTimeout)
				logger.Error("Backup scheduler panicked", zap.Any("panic", r))
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
				sentry.Flush(logger.SentryPanicFlushTimeout)
				logger.Error("Heartbeat scheduler panicked", zap.Any("panic", r))
			}
			wg.Done()
		}()
		heartbeat.Start(ctx, cfg.HeartbeatURL, cfg.HeartbeatInterval)
	}()

	// Start trial cleanup scheduler
	wg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sentry.CurrentHub().Recover(r)
				sentry.Flush(logger.SentryPanicFlushTimeout)
				logger.Error("Trial cleanup scheduler panicked", zap.Any("panic", r))
			}
			wg.Done()
		}()
		startTrialCleanupScheduler(ctx, dbService, xuiClient, cfg.XUIInboundID, cfg.TrialDurationHours)
	}()

	// Track in-flight update handlers
	var updatesWg sync.WaitGroup

	// Main event loop
	for {
		select {
		case update := <-updates:
			// Acquire semaphore slot (blocks if all slots are busy)
			select {
			case updateSem <- struct{}{}:
				updatesWg.Add(1)
				go func(u tgbotapi.Update) {
					defer func() {
						<-updateSem // Release semaphore slot
						updatesWg.Done()
					}()
					handleUpdateSafely(ctx, handler, u)
				}(update)
			case <-ctx.Done():
				// Shutdown initiated while waiting for semaphore
				logger.Info("Graceful shutdown initiated, draining updates...")
				goto shutdown
			}

		case <-ctx.Done():
			goto shutdown
		}
	}

shutdown:
	logger.Info("Graceful shutdown initiated")
	botAPI.StopReceivingUpdates()

	// Drain the updates channel to prevent goroutine leak
	go func() {
		for range updates {
			// Drain the channel until it's closed
		}
	}()

	// Wait for in-flight update handlers to complete
	logger.Info("Waiting for update handlers to complete...")
	done := make(chan struct{})
	go func() {
		updatesWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("All update handlers completed")
	case <-time.After(config.ShutdownTimeout):
		logger.Warn("Timeout waiting for update handlers")
	}

	// Wait for background tasks with timeout
	logger.Info("Waiting for background tasks to stop...")
	bgDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(bgDone)
	}()

	select {
	case <-bgDone:
		logger.Info("All background tasks stopped successfully")
	case <-time.After(config.ShutdownTimeout):
		logger.Warn("Timeout waiting for background tasks to stop")
	}

	logger.Info("Bot stopped successfully")
}

// handleUpdateSafely handles a Telegram update with panic recovery.
func handleUpdateSafely(ctx context.Context, handler *bot.Handler, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			sentry.CurrentHub().Recover(r)
			sentry.Flush(logger.SentryPanicFlushTimeout)
			logger.Error("Panic in update handler",
				zap.Any("panic", r),
				zap.String("stack", string(stack)),
			)
		}
	}()

	handler.HandleUpdate(ctx, update)
}

// startBackupScheduler runs the database backup scheduler.
func startBackupScheduler(ctx context.Context, dbPath string) {
	logger.Info("Backup scheduler started", zap.String("schedule", "daily at 03:00"))

	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(),
			config.DefaultBackupHour, 0, 0, 0, now.Location())
		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}

		sleepDuration := time.Until(next)
		logger.Info("Next backup scheduled", zap.Duration("duration", sleepDuration.Round(time.Minute)))

		select {
		case <-time.After(sleepDuration):
			logger.Info("Running scheduled database backup")
			if err := backup.DailyBackup(ctx, dbPath, config.DefaultBackupRetention); err != nil {
				logger.Error("Backup failed", zap.Error(err))
			} else {
				logger.Info("Database backup completed successfully")
			}

		case <-ctx.Done():
			logger.Info("Backup scheduler stopped")
			return
		}
	}
}

// startTrialCleanupScheduler runs the trial subscription cleanup scheduler.
func startTrialCleanupScheduler(ctx context.Context, db *database.Service, xuiClient *xui.Client, inboundID int, trialHours int) {
	logger.Info("Trial cleanup scheduler started", zap.String("schedule", "hourly"))

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			logger.Info("Running trial cleanup")
			deleted, err := db.CleanupExpiredTrials(ctx, trialHours, xuiClient, inboundID)
			if err != nil {
				logger.Error("Trial cleanup failed", zap.Error(err))
			} else if deleted > 0 {
				logger.Info("Trial cleanup completed", zap.Int64("deleted", deleted))
			}

		case <-ctx.Done():
			logger.Info("Trial cleanup scheduler stopped")
			return
		}
	}
}
