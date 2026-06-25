package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/heartbeat"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/scheduler"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/kereal/rs8kvn_bot/internal/web"
	"github.com/kereal/rs8kvn_bot/internal/webhook"
	"github.com/kereal/rs8kvn_bot/internal/xui"

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

// Option is a functional option for Run.
type Option func(*runOptions)

type runOptions struct {
	xuiClientFn func(host, apiToken string) (interfaces.XUIClient, error)
	vpnClientFn func(cfg vpn.Config) (vpn.Client, error)
}

// WithXUIClient sets a custom XUI client factory (for testing).
func WithXUIClient(fn func(host, apiToken string) (interfaces.XUIClient, error)) Option {
	return func(o *runOptions) { o.xuiClientFn = fn }
}

// WithVPNClient sets a custom VPN client factory (for testing).
func WithVPNClient(fn func(cfg vpn.Config) (vpn.Client, error)) Option {
	return func(o *runOptions) { o.vpnClientFn = fn }
}

func defaultOptions() *runOptions {
	return &runOptions{
		xuiClientFn: func(host, apiToken string) (interfaces.XUIClient, error) {
			c, err := xui.NewClient(host, apiToken)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		vpnClientFn: vpn.NewClient,
	}
}

func buildRuntimeNodeClients(nodes []database.Node, opts *runOptions) ([]database.Node, map[uint]interfaces.XUIClient, map[uint]vpn.Client, interfaces.XUIClient, error) {
	runtimeNodes := make([]database.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.IsActive {
			if node.Type != database.NodeType3xUI {
				continue
			}
			runtimeNodes = append(runtimeNodes, node)
		}
	}
	if len(runtimeNodes) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("no active nodes configured")
	}

	xuiClients := make(map[uint]interfaces.XUIClient)
	vpnClients := make(map[uint]vpn.Client)
	var legacyXUIClient interfaces.XUIClient

	for _, node := range runtimeNodes {
		var xuiClient interfaces.XUIClient
		if node.Type == database.NodeType3xUI {
			client, err := opts.xuiClientFn(node.Host, node.APIToken)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("init 3x-ui client for node %d: %w", node.ID, err)
			}
			xuiClient = client
			xuiClients[node.ID] = client
			if legacyXUIClient == nil {
				legacyXUIClient = client
			}
		}

		client, err := opts.vpnClientFn(vpn.Config{
			Host:       node.Host,
			APIToken:   node.APIToken,
			Type:       node.Type,
			InboundIDs: node.ResolveInboundIDs(),
			XUIClient:  xuiClient,
		})
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("init vpn client for node %d: %w", node.ID, err)
		}
		vpnClients[node.ID] = client
	}

	return runtimeNodes, xuiClients, vpnClients, legacyXUIClient, nil
}

// getVersion returns the service version string prefixed with "rs8kvn_bot@".
// It prefers a non-"dev" ldflag version, then a module tag from build info, then a short VCS revision from build info, then an ldflag commit, and finally falls back to the ldflag version.
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
// (backups, heartbeat, trial cleanup, Subscription server reload), marks the web
// server readiness, and coordinates orderly shutdown of update handlers and
// main is the entry point that initializes configuration and services, starts background
// workers and the web server, processes Telegram updates with bounded concurrency, and
// coordinates a graceful shutdown when termination signals are received.
//
// It performs best-effort initialization of optional components (Sentry, 3x-ui, Telegram
// bot), constructs shared services (database, subscription service, webhook sender),
// registers health checks, starts scheduled background tasks, and marks the web server
// readiness. On shutdown it stops receiving updates, drains channels, and waits for
// in-flight update handlers and background tasks to complete within configured timeouts.
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

	// Seed default node from env vars if nodes table is empty
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer seedCancel()
	isEmpty, err := dbService.IsNodesEmpty(seedCtx)
	if err != nil {
		logger.Fatal("Failed to check nodes table", zap.Error(err))
	}
	if isEmpty {
		xuiHost := os.Getenv("XUI_HOST")
		xuiAPIToken := os.Getenv("XUI_API_TOKEN")
		xuiInboundIDStr := os.Getenv("XUI_INBOUND_ID")
		var xuiInboundIDs []int
		if xuiInboundIDStr != "" {
			if parsed, convErr := strconv.Atoi(xuiInboundIDStr); convErr == nil && parsed > 0 {
				xuiInboundIDs = []int{parsed}
			} else {
				logger.Warn("Invalid XUI_INBOUND_ID",
					zap.String("raw_value", xuiInboundIDStr),
					zap.Error(convErr))
			}
		}
		if len(xuiInboundIDs) == 0 {
			xuiInboundIDs = []int{1}
		}
		defaultSubURL := cfg.GlobalSubURL
		if defaultSubURL == "" && xuiHost != "" {
			defaultSubURL = strings.TrimRight(xuiHost, "/") + "/sub/"
		}
		if err := dbService.SeedDefaultNode(seedCtx, "default", xuiHost, xuiAPIToken, xuiInboundIDs, defaultSubURL); err != nil {
			logger.Fatal("Failed to seed default node", zap.Error(err))
		}
		logger.Info("Default node seeded", zap.String("host", xuiHost))
	}

	// Load active runtime nodes and create node clients.
	nodes, err := dbService.ListNodes(context.Background())
	if err != nil {
		logger.Fatal("Failed to list nodes", zap.Error(err))
	}
	runtimeNodes, xuiClients, vpnClients, legacyXUIClient, err := buildRuntimeNodeClients(nodes, defaultOptions())
	if err != nil {
		logger.Fatal("Failed to initialize node clients", zap.Error(err))
	}

	// Close all XUI clients on shutdown
	defer func() {
		for id, client := range xuiClients {
			if err := client.Close(); err != nil {
				logger.Error("Failed to close 3x-ui client",
					zap.Uint("node_id", id),
					zap.Error(err))
			}
		}
	}()

	if legacyXUIClient == nil {
		logger.Warn("No active node found — legacy XUI client will be nil; health check on /ping will be skipped")
	}
	nodes = runtimeNodes

	// Initialize Telegram bot with retry to handle transient network issues
	logger.Info("Validating Telegram bot token")

	const botInitMaxAttempts = 5
	botInitDelay := 3 * time.Second

	var botAPI *tgbotapi.BotAPI
	var botConfig *bot.BotConfig

	for i := 0; i < botInitMaxAttempts; i++ {
		func() {
			defer recoverAndReport("Telegram bot init")

			api, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
			if err != nil {
				if i == botInitMaxAttempts-1 {
					logger.Fatal("Failed to initialize Telegram bot after max attempts",
						zap.Error(err),
						zap.Int("attempts", botInitMaxAttempts))
				}
				logger.Warn("Telegram bot init failed, retrying...",
					zap.Int("attempt", i+1),
					zap.Int("max_attempts", botInitMaxAttempts),
					zap.Error(err))
				time.Sleep(botInitDelay + time.Duration(rand.Int63n(int64(botInitDelay/2)))) //nolint:gosec // jitter
				return
			}

			bc, err := bot.NewBotConfig(api)
			if err != nil {
				if i == botInitMaxAttempts-1 {
					logger.Fatal("Failed to create bot config after max attempts",
						zap.Error(err),
						zap.Int("attempts", botInitMaxAttempts))
				}
				logger.Warn("Bot config creation failed, retrying...",
					zap.Int("attempt", i+1),
					zap.Int("max_attempts", botInitMaxAttempts),
					zap.Error(err))
				time.Sleep(botInitDelay + time.Duration(rand.Int63n(int64(botInitDelay/2)))) //nolint:gosec // jitter
				return
			}

			botAPI = api
			botConfig = bc
		}()

		if botAPI != nil {
			break
		}
	}

	if botAPI == nil {
		logger.Fatal("Failed to initialize Telegram bot after all attempts")
	}

	logger.Info("Telegram bot authorized", zap.String("username", botConfig.Username))

	// Create webhook sender for Proxy Manager notifications
	webhookSender := webhook.NewSender(cfg.ProxyManagerWebhookURL, cfg.ProxyManagerWebhookSecret)

	// Create subscription service (shared between bot handler and web server)
	subService := service.NewSubscriptionService(dbService, xuiClients, vpnClients, nodes, cfg, cfg.GlobalSubURL, webhookSender)

	// Create Subscription server service
	subServer := subserver.NewService(config.SubServerCacheTTL)
	defer subServer.Stop()

	// Create bot handler
	handler := bot.NewHandler(botAPI, cfg, dbService, legacyXUIClient, botConfig, subService, getVersion())

	// Initialize and start web server (health + trial pages)
	webServer := web.NewServer(fmt.Sprintf(":%d", cfg.HealthCheckPort), dbService, cfg, botConfig, subService, subServer)
	webServer.RegisterChecker("database", func(ctx context.Context) web.ComponentHealth {
		if err := dbService.Ping(ctx); err != nil {
			return web.ComponentHealth{Status: web.StatusDown, Message: err.Error()}
		}
		return web.ComponentHealth{Status: web.StatusOK}
	})
	webServer.RegisterChecker("xui", func(ctx context.Context) web.ComponentHealth {
		if legacyXUIClient == nil {
			return web.ComponentHealth{Status: web.StatusDegraded, Message: "no active XUI client"}
		}
		if err := legacyXUIClient.Ping(ctx); err != nil {
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
		logger.Info("Web server started", zap.String("addr", webServer.Addr()), zap.Int("port", cfg.HealthCheckPort))
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

	// wg tracks exactly these 6 long-lived background workers.
	// The first 4 are legacy workers (orphan reconciler, backup, heartbeat, trial cleanup).
	// The last 2 are subscription sync and expire workers.
	// All of them must exit (via ctx cancellation) before main returns,
	// so we wait on wg at the end of graceful shutdown.
	var wg sync.WaitGroup
	wg.Add(6)

	// Start orphaned XUI client reconciler (every 6 hours)
	go func() {
		defer recoverAndReport("Orphan reconciler")
		defer wg.Done()
		// Initial run after 30 seconds to let XUI settle
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
			return
		}
		svc := handler.GetSubscriptionService()
		if svc == nil {
			logger.Warn("SubscriptionService not available, skipping orphan reconciliation")
			return
		}
		if count, err := svc.ReconcileOrphanedClients(ctx); err != nil {
			logger.Warn("Initial orphan reconciliation failed", zap.Error(err))
		} else {
			logger.Info("Orphan reconciliation completed", zap.Int("removed", count))
		}
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if count, err := svc.ReconcileOrphanedClients(ctx); err != nil {
					logger.Warn("Orphan reconciliation failed", zap.Error(err))
				} else {
					logger.Info("Orphan reconciliation completed", zap.Int("removed", count))
				}
			}
		}
	}()

	logger.Info("Bot started successfully")

	// Mark web server as ready after all components are initialized
	webServer.SetReady(true)

	// Channel to limit concurrent update handlers (worker pool)
	// This prevents unbounded goroutine spawning
	updateSem := make(chan struct{}, config.MaxConcurrentHandlers)

	// Start backup scheduler
	backupSched := scheduler.NewBackupScheduler(cfg.DatabasePath, config.DefaultBackupHour, config.DefaultBackupRetention)
	go func() {
		defer recoverAndReport("Backup scheduler")
		defer wg.Done()
		backupSched.Start(ctx)
	}()

	// Start heartbeat monitor
	go func() {
		defer recoverAndReport("Heartbeat scheduler")
		defer wg.Done()
		heartbeat.Start(ctx, cfg.HeartbeatURL, cfg.HeartbeatInterval)
	}()

	// Start trial cleanup scheduler
	go func() {
		defer recoverAndReport("Trial cleanup scheduler")
		defer wg.Done()
		trialSched := scheduler.NewTrialCleanupScheduler(subService)
		trialSched.Start(ctx)
	}()

	// Create sync service and inject into subscription service
	syncSvc := service.NewSyncService(dbService, vpnClients, nodes)
	subService.SetSyncService(syncSvc)

	// Start subscription sync worker (every 5 minutes)
	go func() {
		defer recoverAndReport("Subscription sync worker")
		defer wg.Done()
		syncWorker := scheduler.NewSubscriptionSyncWorker(syncSvc)
		syncWorker.Run(ctx)
	}()

	// Start subscription expire worker (every 1 hour)
	go func() {
		defer recoverAndReport("Subscription expire worker")
		defer wg.Done()
		expireWorker := scheduler.NewSubscriptionExpireWorker(dbService, subService)
		expireWorker.Run(ctx)
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

	// Ожидание завершения текущих доставок webhook
	if webhookSender != nil {
		logger.Info("Waiting for webhook deliveries...")
		webhookSender.Wait()
		logger.Info("Webhook deliveries completed")
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

	// Ожидание фоновых горутин обработчика
	logger.Info("Waiting for handler background goroutines...")
	handler.WaitForBackgroundGoroutines()
	logger.Info("Handler background goroutines stopped")

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
