package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"github.com/getsentry/sentry-go"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// initSentry initializes Sentry error tracking if DSN is configured.
func initSentry(cfg *config.Config) {
	if cfg.SentryDSN == "" {
		return
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.SentryDSN,
		Environment:      "production",
		Release:          getVersion(),
		TracesSampleRate: logger.SentryTracesSampleRate,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize Sentry: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stderr, "Sentry error tracking initialized")
}

// initLogger initializes the logger and redirects stdlib log output.
// Returns the log service for deferred cleanup.
func initLogger(cfg *config.Config) *logger.Service {
	logService, err := logger.Init(cfg.LogFilePath, cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		sentry.Flush(logger.SentryFlushTimeout)
		os.Exit(1) //nolint:gocritic
	}

	logger.RedirectStdLog()
	logger.Info("Starting bot",
		zap.String("version", getVersion()),
		zap.String("built", buildTime))
	logger.Info("Configuration loaded", zap.String("config", cfg.String()))

	return logService
}

// runtimeDeps holds the initialized runtime node clients.
type runtimeDeps struct {
	nodes      []database.Node
	xuiClients map[uint]interfaces.XUIClient
	vpnClients map[uint]vpn.Client
}

// initDatabase initializes the database service and loads runtime node clients.
// Returns the DB service and runtime dependencies.
func initDatabase(cfg *config.Config) (*database.Service, *runtimeDeps) {
	dbService, err := database.NewService(cfg.DatabasePath)
	if err != nil {
		logger.Fatal("Failed to initialize database", zap.Error(err))
	}

	metrics.RegisterDBMetrics(dbService.GetDB())

	nodes, err := dbService.ListNodes(context.Background())
	if err != nil {
		logger.Fatal("Failed to list nodes", zap.Error(err))
	}
	if len(nodes) == 0 {
		logger.Fatal("No nodes configured — insert at least one node via migration or setup command before starting")
	}
	runtimeNodes, xuiClients, vpnClients, err := buildRuntimeNodeClients(nodes, defaultOptions())
	if err != nil {
		logger.Fatal("Failed to initialize node clients", zap.Error(err))
	}

	if len(xuiClients) == 0 {
		logger.Warn("No active 3x-ui node found — /ping xui health check will report not configured")
	}

	return dbService, &runtimeDeps{
		nodes:      runtimeNodes,
		xuiClients: xuiClients,
		vpnClients: vpnClients,
	}
}

// appServices holds the wired application services and handler.
type appServices struct {
	subService *service.SubscriptionService
	subServer  *subserver.Service
	handler    *bot.Handler
}

// initServices wires the subscription service, subserver, bot handler,
// and cache invalidation composition.
func initServices(cfg *config.Config, dbService *database.Service, deps *runtimeDeps, botAPI *tgbotapi.BotAPI, botConfig *bot.BotConfig) *appServices {
	subService := service.NewSubscriptionService(dbService, deps.xuiClients, deps.vpnClients, deps.nodes, cfg)
	subServer := subserver.NewService(config.SubServerCacheTTL)
	handler := bot.NewHandler(botAPI, cfg, dbService, botConfig, subService, getVersion())

	// Compose cache invalidation: invalidate both the bot subscription cache
	// and the subserver response cache. NewHandler wired invalidateBySubID to
	// the bot cache only; we override it here so Delete/Revoke/Expire also
	// evict the subserver entry (otherwise revoked subs serve stale config
	// for up to SubServerCacheTTL).
	botCache := handler.Cache()
	subService.SetInvalidateBySubIDFunc(func(subID string) {
		botCache.InvalidateBySubID(subID)
		subServer.InvalidateCache(subID)
	})

	return &appServices{
		subService: subService,
		subServer:  subServer,
		handler:    handler,
	}
}

// runEventLoop processes Telegram updates with bounded concurrency until
// the shutdown context is cancelled. Blocks until all in-flight handlers
// complete or the shutdown timeout elapses.
func runEventLoop(ctx context.Context, botAPI *tgbotapi.BotAPI, handler *bot.Handler, updates tgbotapi.UpdatesChannel) {
	updateSem := make(chan struct{}, config.MaxConcurrentHandlers)
	var updatesWg sync.WaitGroup

eventLoop:
	for {
		select {
		case update := <-updates:
			select {
			case updateSem <- struct{}{}:
				updatesWg.Add(1)
				go func(u tgbotapi.Update) {
					defer func() {
						<-updateSem
						updatesWg.Done()
					}()
					handleUpdateSafely(ctx, handler, u)
				}(update)
			case <-ctx.Done():
				logger.Info("Graceful shutdown initiated, draining updates...")
				break eventLoop
			}

		case <-ctx.Done():
			break eventLoop
		}
	}

	logger.Info("Graceful shutdown initiated")
	botAPI.StopReceivingUpdates()

	// Drain the updates channel to prevent goroutine leak. Bounded by ctx so it
	// can never block past shutdown even if the underlying channel is not closed.
	go func() {
		for {
			select {
			case <-updates:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for in-flight update handlers to complete
	logger.Info("Waiting for update handlers to complete...")
	done := make(chan struct{})
	go func() {
		updatesWg.Wait()
		close(done)
	}()
}
// gracefulShutdown stops background workers and handler goroutines with timeouts.
// subServer.Stop() (cache drop) runs first so revoked/updated subs stop being
// served before we wait on workers; web server drain remains via its own defer.
func gracefulShutdown(bgWg *sync.WaitGroup, handler *bot.Handler, subServer *subserver.Service) {
	subServer.Stop()

	logger.Info("Waiting for background tasks to stop...")
	bgDone := make(chan struct{})
	go func() {
		bgWg.Wait()
		close(bgDone)
	}()

	select {
	case <-bgDone:
		logger.Info("All background tasks stopped successfully")
	case <-time.After(config.ShutdownTimeout):
		logger.Warn("Timeout waiting for background tasks to stop")
	}

	logger.Info("Waiting for handler background goroutines...")
	handler.WaitForBackgroundGoroutines()
	logger.Info("Handler background goroutines stopped")
	logger.Info("Bot stopped successfully")
}
