// Package main starts the Telegram bot and its HTTP/background services.
package main

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/heartbeat"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/scheduler"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/kereal/rs8kvn_bot/internal/web"
	"github.com/kereal/rs8kvn_bot/internal/xui"
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

// runOptions содержит инъектируемые фабричные функции для XUI- и VPN-клиентов,
// позволяющие подменять реальные реализации моками в тестах.
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

// defaultOptions возвращает настройки запуска с фабриками клиентов по умолчанию.
// XUI- и VPN-клиенты создаются реальными реализациями; для тестов их можно
// переопределить через WithXUIClient / WithVPNClient.
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

// buildRuntimeNodeClients фильтрует активные ноды и для каждой создаёт
// runtime-клиенты: XUI-клиент (только для 3x-ui нод) и VPN-клиент. Возвращает
// отфильтрованные ноды и карты клиентов, индексированные по ID ноды.
func buildRuntimeNodeClients(nodes []database.Node, opts *runOptions) ([]database.Node, map[uint]interfaces.XUIClient, map[uint]vpn.Client, error) {
	runtimeNodes := make([]database.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.IsActive {
			runtimeNodes = append(runtimeNodes, node)
		}
	}
	if len(runtimeNodes) == 0 {
		return nil, nil, nil, fmt.Errorf("no active nodes configured")
	}

	xuiClients := make(map[uint]interfaces.XUIClient)
	vpnClients := make(map[uint]vpn.Client)

	for _, node := range runtimeNodes {
		var xuiClient interfaces.XUIClient
		if node.Type == database.NodeType3xUI {
			client, err := opts.xuiClientFn(node.Host, node.APIToken)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("init 3x-ui client for node %d: %w", node.ID, err)
			}
			xuiClient = client
			xuiClients[node.ID] = client
		}

		client, err := opts.vpnClientFn(vpn.Config{
			Host:       node.Host,
			APIToken:   node.APIToken,
			Type:       node.Type,
			InboundIDs: node.ResolveInboundIDs(),
			XUIClient:  xuiClient,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("init vpn client for node %d: %w", node.ID, err)
		}
		vpnClients[node.ID] = client
	}

	return runtimeNodes, xuiClients, vpnClients, nil
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

// initBot инициализирует Telegram-бота: проверяет токен, создаёт BotAPI
// и собирает bot.BotConfig с повторными попытками при сбоях подключения.
func initBot(cfg *config.Config) (*tgbotapi.BotAPI, *bot.BotConfig, error) {
	logger.Info("Validating Telegram bot token")

	const botInitMaxAttempts = 5
	botInitDelay := 3 * time.Second

	var lastErr error
	for i := 0; i < botInitMaxAttempts; i++ {
		var api *tgbotapi.BotAPI
		var bc *bot.BotConfig
		var err error

		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					lastErr = fmt.Errorf("telegram bot init panic: %v", recovered)
				}
			}()
			api, err = tgbotapi.NewBotAPI(cfg.TelegramBotToken)
			if err == nil {
				bc, err = bot.NewBotConfig(api)
			}
		}()
		if err == nil && api != nil && bc != nil {
			logger.Info("Telegram bot authorized", zap.String("username", bc.Username))
			return api, bc, nil
		}
		if err == nil {
			err = lastErr
		}
		lastErr = err
		if i == botInitMaxAttempts-1 {
			break
		}
		logger.Warn("Telegram bot init failed, retrying...", zap.Int("attempt", i+1), zap.Int("max_attempts", botInitMaxAttempts), zap.Error(err))
		jitter := time.Duration(0)
		if maxJitter := botInitDelay / 2; maxJitter > 0 {
			if n, randErr := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(maxJitter))); randErr == nil {
				jitter = time.Duration(n.Int64())
			}
		}
		time.Sleep(botInitDelay + jitter)
	}
	return nil, nil, fmt.Errorf("failed to initialize Telegram bot after %d attempts: %w", botInitMaxAttempts, lastErr)
}

// startWebServer создаёт и запускает HTTP-сервер (подписки, инвайт/trial-страницы).
// Сервер стартует асинхронно; функция ждёт до 2 секунд первой ошибки запуска,
// чтобы не блокировать старт бота, но вернуть ошибку, если сервер точно не поднялся.
func startWebServer(subService *service.SubscriptionService, cfg *config.Config, botConfig *bot.BotConfig, subServer *subserver.Service, dbService *database.Service, orderService *service.OrderService, botAPI interfaces.BotAPI) (*web.Server, error) {
	webServer := web.NewServer(fmt.Sprintf(":%d", cfg.WebServerPort), dbService, cfg, botConfig.Username, subService, subServer)
	webServer.SetOrderService(orderService)
	webServer.SetBot(botAPI)
	if cfg.PaymentEnabled {
		webServer.SetPaymentConfig(&web.PaymentConfig{
			Enabled:    true,
			MerchantID: cfg.PlategaMerchantID,
			Secret:     cfg.PlategaSecret,
		})
	}
	webServer.RegisterChecker("database", func(ctx context.Context) web.ComponentHealth {
		if err := dbService.Ping(ctx); err != nil {
			return web.ComponentHealth{Status: web.StatusDown, Message: err.Error()}
		}
		return web.ComponentHealth{Status: web.StatusOK}
	})

	webServerStartErr := make(chan error, 1)
	go func() {
		defer recoverAndReport("Web server start")
		if err := webServer.Start(context.Background()); err != nil {
			webServerStartErr <- err
		}
	}()

	select {
	case err := <-webServerStartErr:
		return nil, err
	case <-time.After(2 * time.Second):
		return webServer, nil
	}
}

// startBackgroundWorkers запускает фоновые goroutine-воркеры: сбор метрик пула БД,
// периодическую сверку осиротевших клиентов, бэкапы, heartbeat, очистку trial,
// а также синхронизацию и истечение подписок. Возвращает WaitGroup для ожидания
// завершения при штатном выключении.
func startBackgroundWorkers(ctx context.Context, handler *bot.Handler, subService *service.SubscriptionService, dbService *database.Service, cfg *config.Config, vpnClients map[uint]vpn.Client, nodes []database.Node) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(8)

	// Create sync service and wire it into subscription service BEFORE starting
	// any background goroutines. This prevents a race where the trial cleanup
	// scheduler calls CleanupExpiredTrials before SetSyncService has run, which
	// would bypass the sync-based deprovision path and fall into the legacy
	// deleteClientFromAllNodes code that sends delete requests to ALL nodes.
	syncSvc := service.NewSyncService(dbService, vpnClients, nodes)
	if orderService := handler.OrderService(); orderService != nil {
		orderService.SetSyncService(syncSvc)
	}

	go func() {
		defer recoverAndReport("DB pool metrics collector")
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				metrics.CollectDBPoolMetrics(ctx, dbService.GetDB())
			}
		}
	}()

	go func() {
		defer recoverAndReport("Orphan reconciler")
		defer wg.Done()
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

	backupSched := scheduler.NewBackupScheduler(cfg.DatabasePath, config.DefaultBackupHour, config.DefaultBackupRetention)
	go func() {
		defer recoverAndReport("Backup scheduler")
		defer wg.Done()
		backupSched.Start(ctx)
	}()

	go func() {
		defer recoverAndReport("Heartbeat scheduler")
		defer wg.Done()
		heartbeat.Start(ctx, cfg.HeartbeatURL, cfg.HeartbeatInterval)
	}()

	go func() {
		defer recoverAndReport("Trial cleanup scheduler")
		defer wg.Done()
		trialSched := scheduler.NewTrialCleanupScheduler(subService)
		trialSched.Start(ctx)
	}()

	go func() {
		defer recoverAndReport("Subscription sync worker")
		defer wg.Done()
		syncWorker := scheduler.NewSubscriptionSyncWorker(syncSvc)
		syncWorker.Run(ctx)
	}()

	go func() {
		defer recoverAndReport("Subscription expire worker")
		defer wg.Done()
		expireWorker := scheduler.NewSubscriptionExpireWorker(dbService, subService)
		expireWorker.Run(ctx)
	}()

	go func() {
		defer recoverAndReport("Subscription reminder worker")
		defer wg.Done()
		reminderWorker := scheduler.NewSubscriptionReminderWorker(dbService, subService)
		reminderWorker.Run(ctx)
	}()

	return &wg
}

// main — точка входа: инициализирует конфигурацию и сервисы, запускает фоновые
// воркеры и веб-сервер, обрабатывает обновления Telegram с ограниченным
// параллелизмом и выполняет корректное завершение при получении сигнала.
// Инициализация опциональных компонентов (Sentry, БД, 3x-ui клиент, бот) делается
// по принципу best-effort, чтобы сервис стартовал даже при недоступности части
// зависимостей.
func main() {
	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// 2. Initialize Sentry (before logger)
	initSentry(cfg)
	defer sentry.Flush(logger.SentryFlushTimeout)

	// 3. Initialize logger
	logService, err := initLogger(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := logService.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to close logger: %v\n", err)
		}
	}()

	// 4. Initialize database and node clients
	dbService, deps, err := initDatabase(cfg)
	if err != nil {
		logger.Fatal("Failed to initialize database", zap.Error(err))
	}
	defer func() {
		if err := dbService.Close(); err != nil {
			logger.Error("Failed to close database", zap.Error(err))
		}
	}()
	defer func() {
		for id, client := range deps.xuiClients {
			if err := client.Close(); err != nil {
				logger.Error("Failed to close 3x-ui client",
					zap.Uint("node_id", id),
					zap.Error(err))
			}
		}
	}()
	logger.Info("Database initialized successfully")
	// 5. Wire application services with a placeholder bot; initBot runs below
	// (step 7) and replaces it with the real bot before any update is processed.
	// The real username comes from Telegram getMe inside initBot — it is NOT
	// configured manually. The placeholder carries no username; the real one is
	// injected via SetBotConfig once initBot returns.
	botConfig := &bot.BotConfig{}
	botAPI := &tgbotapi.BotAPI{Self: tgbotapi.User{UserName: ""}}
	svc := initServices(cfg, dbService, deps, botAPI, botConfig)

	// 6. Start web server so subscriptions are served; bot is initialised next.
	// The web server starts with an empty bot username; initBot injects the real
	// username (from Telegram getMe) via SetBotUsername once the bot is ready, so
	// the share/invite page shows the correct @username after startup.
	webServer, err := startWebServer(svc.subService, cfg, botConfig, svc.subServer, dbService, svc.orderService, botAPI)
	if err != nil {
		logger.Fatal("Failed to start web server", zap.Error(err))
	}
	defer func() {
		if webServer == nil {
			return
		}
		webServer.SetReady(false)
		webServer.SetPaymentReady(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := webServer.Stop(shutdownCtx); err != nil {
			logger.Error("Failed to stop web server", zap.Error(err))
		}
	}()

	// 7. Initialize Telegram bot (initBot retries internally and calls Fatal on total failure).
	logger.Info("Initializing Telegram bot...")
	api, bc, err := initBot(cfg)
	if err != nil {
		logger.Fatal("Telegram bot initialization failed", zap.Error(err))
	}
	svc.handler.SetBot(api)
	svc.subService.SetBot(api)
	svc.handler.SetBotConfig(bc)
	if svc.orderService != nil {
		svc.orderService.SetBotUsername(bc.Username)
		svc.orderService.SetAdminBot(api)
	}
	botAPI = api
	if webServer != nil {
		webServer.SetBot(api)
		webServer.SetBotUsername(bc.Username)
	} else {
		logger.Warn("web server not running; share/invite page username not updated")
	}
	u := tgbotapi.NewUpdate(0)
	u.Timeout = config.BotUpdateTimeout
	u.AllowedUpdates = []string{"message", "callback_query"}
	updates := botAPI.GetUpdatesChan(u)

	// 9. Setup graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	// 10. Start background goroutines
	svc.handler.StartCacheCleanup(ctx, bot.CacheTTL/2)
	svc.handler.StartRateLimiterCleanup(ctx, bot.CacheTTL, bot.CacheTTL*2)
	svc.handler.StartReferralCacheSync(ctx)
	bgWg := startBackgroundWorkers(ctx, svc.handler, svc.subService, dbService, cfg, deps.vpnClients, deps.nodes)
	if webServer != nil {
		webServer.SetPaymentReady(true)
	}

	logger.Debug("Bot started successfully")
	if webServer != nil {
		webServer.SetReady(true)
	}

	// 11. Run the main event loop (blocks until shutdown)
	runEventLoop(ctx, botAPI, svc.handler, updates)

	// 12. Graceful shutdown of background workers
	gracefulShutdown(bgWg, svc.handler, svc.subServer)
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
