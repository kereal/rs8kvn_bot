package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/heartbeat"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/scheduler"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/subproxy"
	"rs8kvn_bot/internal/web"
	"rs8kvn_bot/internal/webhook"
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

// The function performs best-effort initialization for optional components (Sentry,
// database, 3x-ui client, Telegram bot) so the service can start even if some
// dependencies are unavailable. It also starts background maintenance tasks
// (backups, heartbeat, trial cleanup, subscription proxy reload), marks the web
// server readiness, and coordinates orderly shutdown of update handlers and
// background workers when a termination signal is received.
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
	xuiClient, err := xui.NewClient(cfg.XUIHost, cfg.XUIUsername, cfg.XUIPassword, time.Duration(cfg.XUISessionMaxAgeMinutes)*time.Minute)
	if err != nil {
		logger.Fatal("Failed to initialize 3x-ui client", zap.Error(err))
	}
	defer func() {
		if err := xuiClient.Close(); err != nil {
			logger.Error("Failed to close 3x-ui client", zap.Error(err))
		}
	}()

	// Connect to 3x-ui panel in background (non-blocking startup)
	// This allows the bot to start even if the panel is temporarily unavailable
	// The circuit breaker will handle reconnection attempts
	go func() {
		defer recoverAndReport("XUI login")
		logger.Info("Connecting to 3x-ui panel (background)")
		const startupLoginMaxAttempts = 5
		startupLoginDelay := 5 * time.Second
		for i := 0; i < startupLoginMaxAttempts; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), config.XUILoginTimeout)
			err := xuiClient.Login(ctx)
			cancel()
			if err == nil {
				logger.Info("3x-ui panel connected")
				return
			}
			if i == startupLoginMaxAttempts-1 {
				logger.Warn("Failed to connect to 3x-ui panel after max attempts, will retry via circuit breaker",
					zap.Error(err),
					zap.Int("attempts", startupLoginMaxAttempts))
				return
			}
			logger.Warn("3x-ui login failed, retrying...",
				zap.Int("attempt", i+1),
				zap.Int("max_attempts", startupLoginMaxAttempts),
				zap.Error(err))
			time.Sleep(startupLoginDelay + time.Duration(rand.Int63n(int64(startupLoginDelay/2)))) //nolint:gosec // G404: math/rand is sufficient for jitter, crypto/rand overhead unnecessary
		}
	}()

	// Initialize Telegram bot with timeout to prevent blocking startup
	logger.Info("Validating Telegram bot token")

	// Use a channel to get the result asynchronously
	type botInitResult struct {
		botAPI    *tgbotapi.BotAPI
		botConfig *bot.BotConfig
		err       error
	}
	botInitChan := make(chan botInitResult, 1)

	go func() {
		defer recoverAndReport("Telegram bot init")
		api, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
		if err != nil {
			botInitChan <- botInitResult{err: err}
			return
		}

		cfg, err := bot.NewBotConfig(api)
		botInitChan <- botInitResult{botAPI: api, botConfig: cfg, err: err}
	}()

	// Declare variables for bot API and config (needed for scope)
	var botAPI *tgbotapi.BotAPI
	var botConfig *bot.BotConfig

	// Wait for bot initialization with timeout
	select {
	case result := <-botInitChan:
		if result.err != nil {
			logger.Fatal("Failed to initialize Telegram bot", zap.Error(result.err))
		}
		botAPI = result.botAPI
		botConfig = result.botConfig
		logger.Info("Telegram bot authorized", zap.String("username", botConfig.Username))
	case <-time.After(10 * time.Second):
		logger.Fatal("Timeout initializing Telegram bot (10s)")
	}

	// Create webhook sender for Proxy Manager notifications
	webhookSender := webhook.NewSender(cfg.ProxyManagerWebhookURL, cfg.ProxyManagerWebhookSecret)

	// Create subscription service (shared between bot handler and web server)
	subService := service.NewSubscriptionService(dbService, xuiClient, cfg, webhookSender)

	// Create subscription proxy service
	subProxy := subproxy.NewService(cfg)
	defer subProxy.Stop()

	// Create bot handler
	handler := bot.NewHandler(botAPI, cfg, dbService, xuiClient, botConfig, subService, getVersion())

	// Initialize and start web server (health + trial pages)
	webServer := web.NewServer(fmt.Sprintf(":%d", cfg.HealthCheckPort), dbService, xuiClient, cfg, botConfig, subService, subProxy)
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

	// Start web server in background to prevent blocking startup
	webServerStartErr := make(chan error, 1)
	go func() {
		defer recoverAndReport("Web server start")
		if err := webServer.Start(context.Background()); err != nil {
			webServerStartErr <- err
		}
	}()

	// Wait briefly for web server to start or fail
	select {
	case err := <-webServerStartErr:
		logger.Warn("Failed to start web server, continuing without web server", zap.Error(err))
	case <-time.After(2 * time.Second):
		// Web server started successfully in background
		logger.Info("Web server started", zap.Int("port", cfg.HealthCheckPort))
	}
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

	// Start background goroutines (using shutdown context for lifecycle management)
	handler.StartCacheCleanup(ctx, bot.CacheTTL/2)
	handler.StartRateLimiterCleanup(ctx, bot.CacheTTL, bot.CacheTTL*2)
	handler.StartReferralCacheSync(ctx)

	// Start subscription proxy extra servers reload loop (every 5 minutes)
	go func() {
		defer recoverAndReport("SubProxy server reload")
		subProxy.StartReloadLoop(5*time.Minute, ctx.Done())
	}()

	logger.Info("Bot started successfully")

	// Mark web server as ready after all components are initialized
	webServer.SetReady(true)

	var wg sync.WaitGroup
	wg.Add(3)

	// Channel to limit concurrent update handlers (worker pool)
	// This prevents unbounded goroutine spawning
	updateSem := make(chan struct{}, config.MaxConcurrentHandlers)

	// Start backup scheduler
	backupSched := scheduler.NewBackupScheduler(cfg.DatabasePath, config.DefaultBackupHour, config.DefaultBackupRetention)
	go func() {
		defer recoverAndReport("Backup scheduler")
		wg.Done()
		backupSched.Start(ctx)
	}()

	// Start heartbeat monitor
	go func() {
		defer recoverAndReport("Heartbeat scheduler")
		wg.Done()
		heartbeat.Start(ctx, cfg.HeartbeatURL, cfg.HeartbeatInterval)
	}()

	// Start trial cleanup scheduler
	go func() {
		defer recoverAndReport("Trial cleanup scheduler")
		wg.Done()
		trialSched := scheduler.NewTrialCleanupScheduler(dbService, xuiClient, cfg.TrialDurationHours)
		trialSched.Start(ctx)
	}()

	// Track in-flight update handlers
	var updatesWg sync.WaitGroup

	// Main event loop
eventLoop:
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
				break eventLoop
			}

		case <-ctx.Done():
			break eventLoop
		}
	}

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

// recoverAndReport recovers from panics, reports to Sentry, and logs the error.
// Usage: defer recoverAndReport("Component name")
func recoverAndReport(component string) {
	if r := recover(); r != nil {
		stack := debug.Stack()
		sentry.CurrentHub().Recover(r)
		sentry.Flush(logger.SentryPanicFlushTimeout)
		logger.Error(component+" panicked",
			zap.Any("panic", r),
			zap.String("stack", string(stack)),
		)
	}
}

// handleUpdateSafely handles a Telegram update with panic recovery.
func handleUpdateSafely(ctx context.Context, handler *bot.Handler, update tgbotapi.Update) {
	defer recoverAndReport("Update handler")
	handler.HandleUpdate(ctx, update)
}
