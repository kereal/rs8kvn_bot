// Package main wires the bot runtime, services, and graceful shutdown lifecycle.
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
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
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
func initLogger(cfg *config.Config) (*logger.Service, error) {
	logService, err := logger.Init(cfg.LogFilePath, cfg.LogLevel)
	if err != nil {
		return nil, fmt.Errorf("initialize logger: %w", err)
	}

	logger.RedirectStdLog()
	logger.Info("Starting bot",
		zap.String("version", getVersion()),
		zap.String("built", buildTime))
	logger.Info("Configuration loaded", zap.String("config", cfg.String()))

	return logService, nil
}

// runtimeDeps holds the initialized runtime node clients.
type runtimeDeps struct {
	nodes      []database.Node
	xuiClients map[uint]interfaces.XUIClient
	vpnClients map[uint]vpn.Client
}

// initDatabase initializes the database service and loads runtime node clients.
// Returns the DB service and runtime dependencies.
func initDatabase(cfg *config.Config) (dbService *database.Service, deps *runtimeDeps, err error) {
	dbService, err = database.NewService(cfg.DatabasePath)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize database: %w", err)
	}

	metrics.RegisterDBMetrics(dbService.GetDB())

	nodes, err := dbService.ListNodes(context.Background())
	if err != nil {
		_ = dbService.Close()
		return nil, nil, fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes) == 0 {
		_ = dbService.Close()
		return nil, nil, fmt.Errorf("no nodes configured")
	}
	runtimeNodes, xuiClients, vpnClients, err := buildRuntimeNodeClients(nodes, defaultOptions())
	if err != nil {
		_ = dbService.Close()
		return nil, nil, fmt.Errorf("initialize node clients: %w", err)
	}

	if len(xuiClients) == 0 {
		logger.Warn("No active 3x-ui node found — /ping xui health check will report not configured")
	}

	return dbService, &runtimeDeps{
		nodes:      runtimeNodes,
		xuiClients: xuiClients,
		vpnClients: vpnClients,
	}, nil
}

type appServices struct {
	subService   *service.SubscriptionService
	subServer    *subserver.Service
	handler      *bot.Handler
	orderService *service.OrderService
	syncService  *service.SyncService
}

// initServices wires the subscription service, subserver, bot handler,
// and cache invalidation composition.
func initServices(cfg *config.Config, dbService *database.Service, deps *runtimeDeps, botAPI *tgbotapi.BotAPI, botConfig *bot.BotConfig) *appServices {
	syncSvc := service.NewSyncService(dbService, deps.vpnClients, deps.nodes)
	subService := service.NewSubscriptionService(dbService, deps.xuiClients, deps.vpnClients, deps.nodes, cfg)
	subService.SetBot(botAPI)
	subService.SetSyncService(syncSvc)
	subServer := subserver.NewService(config.SubServerCacheTTL)
	handler := bot.NewHandler(botAPI, cfg, dbService, botConfig, subService, getVersion())
	botCache := handler.Cache()
	subService.SetInvalidateBySubIDFunc(func(subID string) {
		botCache.InvalidateBySubID(subID)
		subServer.InvalidateCache(subID)
	})
	var payment service.PaymentProvider
	if cfg.PaymentEnabled {
		payment = platega.New(platega.Config{MerchantID: cfg.PlategaMerchantID, Secret: cfg.PlategaSecret})
	}
	orderService := service.NewOrderService(dbService, subService, syncSvc, payment, "", cfg)
	handler.SetOrderService(orderService)
	return &appServices{subService: subService, subServer: subServer, handler: handler, orderService: orderService, syncService: syncSvc}
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
		case update, ok := <-updates:
			if !ok {
				logger.Info("Telegram updates channel closed")
				break eventLoop
			}
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

	// Drain the updates channel to prevent goroutine leak. StopReceivingUpdates
	// closes the channel (via the polling goroutine on shutdownChannel), so a
	// plain range exits once the buffered updates are exhausted.
	go func() {
		for range updates {
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
		logger.Info("All update handlers completed successfully")
	case <-time.After(config.ShutdownTimeout):
		logger.Warn("Timeout waiting for update handlers to complete")
	}
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
