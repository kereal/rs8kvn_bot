// Package main starts the Telegram bot and its HTTP/background services.
package main

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
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
	"github.com/kereal/rs8kvn_bot/internal/service/payment/platega"
	"github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
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

// paymentClientTimeout — таймаут HTTP payment-инстанса: окно pre-checkout
// Telegram — 10 секунд; бюджет — validation 4–5 c (§5.2) + HTTP 4 c,
// итого < 10 c (плана D1, §5.2).
const paymentClientTimeout = 4 * time.Second

// paymentAuthTimeout — таймаут HTTP-клиента, которым создаётся payment-инстанс.
// Аутентификация (getMe) — обычный запрос к Telegram: 4-секундного бюджета
// платёжных вызовов ей не хватает, и медленный ответ Telegram приводил к
// фатальному падению старта. После создания клиент заменяется на
// paymentClientTimeout (см. newPaymentBotAPI).
const paymentAuthTimeout = 10 * time.Second

// Общие параметры инициализации Telegram-клиентов: основной (long-poll) и
// payment-инстанс создаются одинаково — с повторами и джиттером.
const (
	botInitMaxAttempts = 5
	botInitDelay       = 3 * time.Second
)

// botInitBackoff возвращает паузу перед следующей попыткой инициализации:
// базовая задержка + случайный джиттер (чтобы ретраи не синхронизировались).
func botInitBackoff(base time.Duration) time.Duration {
	jitter := time.Duration(0)

	if maxJitter := base / 2; maxJitter > 0 {
		n, randErr := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(maxJitter)))
		if randErr == nil {
			jitter = time.Duration(n.Int64())
		}
	}

	return base + jitter
}

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

// buildRuntimeNodeClients filters active nodes and for each one builds the
// runtime access: a VPN provisioning client (every node type) and, for 3x-ui
// nodes, a panel reader. Both slots live in the single NodeClients registry
// indexed by node ID that SubscriptionService and SyncService share.
func buildRuntimeNodeClients(nodes []database.Node, opts *runOptions) ([]database.Node, service.NodeClients, error) {
	runtimeNodes := make([]database.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.IsActive {
			runtimeNodes = append(runtimeNodes, node)
		}
	}

	if len(runtimeNodes) == 0 {
		return nil, nil, fmt.Errorf("no active nodes configured")
	}

	clients := make(service.NodeClients, len(runtimeNodes))

	for _, node := range runtimeNodes {
		var xuiClient interfaces.XUIClient

		if node.Type == database.NodeType3xUI {
			client, err := opts.xuiClientFn(node.Host, node.APIToken)
			if err != nil {
				return nil, nil, fmt.Errorf("init 3x-ui client for node %d: %w", node.ID, err)
			}

			if nodeAware, ok := client.(interface{ SetNode(name string) }); ok {
				nodeAware.SetNode(node.Name)
			}

			xuiClient = client
		}

		provision, err := opts.vpnClientFn(vpn.Config{
			Host:       node.Host,
			APIToken:   node.APIToken,
			Type:       node.Type,
			InboundIDs: node.ResolveInboundIDs(),
			XUIClient:  xuiClient,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("init vpn client for node %d: %w", node.ID, err)
		}

		clients[node.ID] = service.NodeAccess{
			Provision: provision,
			Reader:    xuiClient,
			Type:      node.Type,
		}
	}

	return runtimeNodes, clients, nil
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

// getRevision returns the short VCS revision (or the ldflag commit) the binary
// was built from, for the app_info metric. Empty when the build carries no VCS
// data and no ldflag commit — tests and `go run` have none.
func getRevision() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
				return setting.Value[:7]
			}
		}
	}

	if commit != "unknown" && len(commit) >= 7 {
		return commit[:7]
	}

	return "unknown"
}

// initBot инициализирует Telegram-бота: проверяет токен, создаёт BotAPI
// и собирает bot.BotConfig с повторными попытками при сбоях подключения.
func initBot(cfg *config.Config) (*tgbotapi.BotAPI, *bot.BotConfig, error) {
	logger.Info("Validating Telegram bot token")

	var lastErr error

	for i := range botInitMaxAttempts {
		var (
			api *tgbotapi.BotAPI
			bc  *bot.BotConfig
			err error
		)

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

		time.Sleep(botInitBackoff(botInitDelay))
	}

	return nil, nil, fmt.Errorf("failed to initialize Telegram bot after %d attempts: %w", botInitMaxAttempts, lastErr)
}

// newPaymentBotAPIFactory создаёт Telegram-клиент для платёжных вызовов.
// Переменная — точка подмены в тестах.
var newPaymentBotAPIFactory = func(token string, client tgbotapi.HTTPClient) (*tgbotapi.BotAPI, error) {
	return tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, client)
}

// newPaymentBotAPI создаёт отдельный Telegram-инстанс для платёжных вызовов.
//
// Создание инстанса выполняет getMe — сетевой запрос к Telegram. Поэтому:
//   - для аутентификации используется отдельный клиент с paymentAuthTimeout:
//     4-секундного бюджета платёжных вызовов медленному ответу Telegram не
//     хватает, и его таймаут приводил к фатальному падению старта бота;
//   - запрос повторяется с тем же backoff, что и у основного long-poll клиента:
//     транзиентная ошибка Telegram не должна ронять весь бот (в т.ч. основной
//     платёж Platega);
//
// После успешной аутентификации HTTP-клиент заменяется на paymentClientTimeout:
// платёжные вызовы должны укладываться в 10-секундное окно Telegram.
func newPaymentBotAPI(token string) (*tgbotapi.BotAPI, error) {
	return newPaymentBotAPIWithDelay(token, botInitDelay)
}

// newPaymentBotAPIWithDelay — newPaymentBotAPI с явной задержкой между
// попытками (в тестах — 0).
func newPaymentBotAPIWithDelay(token string, delay time.Duration) (*tgbotapi.BotAPI, error) {
	var lastErr error

	for i := range botInitMaxAttempts {
		api, err := newPaymentBotAPIFactory(token, &http.Client{Timeout: paymentAuthTimeout})
		if err == nil && api != nil {
			api.Client = &http.Client{Timeout: paymentClientTimeout}
			logger.Info("Telegram payment client authorized")
			return api, nil
		}

		if err == nil {
			err = errors.New("telegram payment client is nil")
		}

		lastErr = err

		if i == botInitMaxAttempts-1 {
			break
		}

		logger.Warn("Telegram payment client init failed, retrying...", zap.Int("attempt", i+1), zap.Int("max_attempts", botInitMaxAttempts), zap.Error(err))

		time.Sleep(botInitBackoff(delay))
	}

	return nil, fmt.Errorf("failed to initialize Telegram payment client after %d attempts: %w", botInitMaxAttempts, lastErr)
}

// nodeHealthSource сообщает доступность нод по последнему проходу синка.
// Проверка готовности читает готовый снимок и не ходит в панель по сети.
type nodeHealthSource interface {
	NodeHealthSnapshot() (total, down int)
}

// nodeHealthComponent превращает снимок доступности нод в компонент готовности:
// часть нод недоступна — Degraded (трафик идёт через оставшиеся), все — Down.
func nodeHealthComponent(source nodeHealthSource) web.ComponentHealth {
	if source == nil {
		return web.ComponentHealth{Status: web.StatusOK}
	}

	total, down := source.NodeHealthSnapshot()

	switch {
	case total == 0 || down == 0:
		return web.ComponentHealth{Status: web.StatusOK}
	case down == total:
		return web.ComponentHealth{Status: web.StatusDown, Message: fmt.Sprintf("all %d nodes unavailable", total)}
	default:
		return web.ComponentHealth{Status: web.StatusDegraded, Message: fmt.Sprintf("%d/%d nodes unavailable", down, total)}
	}
}

// startWebServer создаёт и запускает HTTP-сервер (подписки, инвайт/trial-страницы).
// Сервер стартует асинхронно; функция ждёт до 2 секунд первой ошибки запуска,
// чтобы не блокировать старт бота, но вернуть ошибку, если сервер точно не поднялся.
func startWebServer(subService *service.SubscriptionService, cfg *config.Config, botConfig *bot.BotConfig, subServer *subserver.Service, dbService *database.Service, orderService *service.OrderService, botAPI interfaces.BotAPI, nodeHealth nodeHealthSource) (*web.Server, error) {
	webServer := web.NewServer(fmt.Sprintf(":%d", cfg.WebServerPort), dbService, cfg, botConfig.Username, subService, subServer)
	webServer.SetOrderService(orderService)
	webServer.SetBot(botAPI)

	// Web-callback Platega включается по итоговому гейту §4.1: активные продажи
	// через platega либо legacy-расчёт старых заказов (PLATEGA_CALLBACK_ENABLED).
	if cfg.ShouldEnablePlategaCallback() {
		webServer.SetPaymentConfig(&web.PaymentConfig{
			MerchantID: cfg.PlategaMerchantID,
			Secret:     cfg.PlategaSecret,
		})
	}

	webServer.RegisterChecker("nodes", func(context.Context) web.ComponentHealth {
		return nodeHealthComponent(nodeHealth)
	})

	webServer.RegisterChecker("database", func(ctx context.Context) web.ComponentHealth {
		err := dbService.Ping(ctx)
		if err != nil {
			return web.ComponentHealth{Status: web.StatusDown, Message: err.Error()}
		}

		return web.ComponentHealth{Status: web.StatusOK}
	})

	webServerStartErr := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				// Паника до готовности сервера иначе оставила бы select ниже
				// ждать вечно, а процесс — без веб-сервера.
				reportPanic("Web server start", r)
				webServerStartErr <- fmt.Errorf("web server panicked: %v", r)
			}
		}()

		err := webServer.Start(context.Background())
		if err != nil {
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
func startBackgroundWorkers(ctx context.Context, handler *bot.Handler, subService *service.SubscriptionService, syncSvc *service.SyncService, orderSvc *service.OrderService, dbService *database.Service, cfg *config.Config) *sync.WaitGroup {
	workers := 9
	if !cfg.NotificationsEnabled {
		// The reminder worker is disabled; traffic recovery still runs because
		// it restores VPN access after the panel resets the quota.
		workers--
	}

	var wg sync.WaitGroup
	wg.Add(workers)

	// The services are wired before any background goroutine starts. Reuse the
	// same SyncService instance that was injected into payment/subscription
	// services so chargeback, expiry, and the worker share one DB-sync contract.
	if syncSvc == nil {
		logger.Warn("SyncService not available; background sync workers may be degraded")
	}

	go func() {
		defer wg.Done()

		runSupervised(ctx, "DB pool metrics collector", func(ctx context.Context) {
			// Первый замер сразу: иначе до первого тика gauge показывает 0.
			metrics.CollectDatabaseFileSize(cfg.DatabasePath)

			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					metrics.CollectDBPoolMetrics(ctx, dbService.GetDB())
					metrics.CollectDatabaseFileSize(cfg.DatabasePath)
				}
			}
		})
	}()

	go func() {
		defer wg.Done()

		runSupervised(ctx, "Orphan reconciler", func(ctx context.Context) {
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

			count, err := svc.ReconcileOrphanedClients(ctx)
			if err != nil {
				logger.Warn("Initial orphan reconciliation failed", zap.Error(err))
			} else {
				logger.Info("Orphan reconciliation completed", zap.Int("revoked", count))
			}

			ticker := time.NewTicker(8 * time.Hour)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					count, err := svc.ReconcileOrphanedClients(ctx)
					if err != nil {
						logger.Warn("Orphan reconciliation failed", zap.Error(err))
					} else {
						logger.Info("Orphan reconciliation completed", zap.Int("revoked", count))
					}
				}
			}
		})
	}()

	backupSched := scheduler.NewBackupScheduler(cfg.DatabasePath, config.DefaultBackupHour, config.DefaultBackupRetention)
	backupSched.SetMaintenance(func(ctx context.Context) error {
		return dbService.DailyMaintenance(ctx, cfg.AuditLogRetentionDays)
	})

	go func() {
		defer wg.Done()

		runSupervised(ctx, "Backup scheduler", func(ctx context.Context) {
			backupSched.Start(ctx)
		})
	}()

	go func() {
		defer wg.Done()

		runSupervised(ctx, "Heartbeat scheduler", func(ctx context.Context) {
			heartbeat.Start(ctx, cfg.HeartbeatURL, cfg.HeartbeatInterval)
		})
	}()

	go func() {
		defer wg.Done()

		runSupervised(ctx, "Trial cleanup scheduler", func(ctx context.Context) {
			trialSched := scheduler.NewTrialCleanupScheduler(subService)
			trialSched.Start(ctx)
		})
	}()

	go func() {
		defer wg.Done()

		runSupervised(ctx, "Subscription sync worker", func(ctx context.Context) {
			syncWorker := scheduler.NewSubscriptionSyncWorker(syncSvc, orderSvc)
			syncWorker.SetSubscriptionService(subService)
			syncWorker.Run(ctx)
		})
	}()

	go func() {
		defer wg.Done()

		runSupervised(ctx, "Subscription expire worker", func(ctx context.Context) {
			expireWorker := scheduler.NewSubscriptionExpireWorker(dbService, subService)
			expireWorker.Run(ctx)
		})
	}()

	if cfg.NotificationsEnabled {
		go func() {
			defer wg.Done()

			runSupervised(ctx, "Subscription reminder worker", func(ctx context.Context) {
				reminderWorker := scheduler.NewSubscriptionReminderWorker(dbService, subService)
				reminderWorker.Run(ctx)
			})
		}()
	}

	go func() {
		defer wg.Done()

		runSupervised(ctx, "Subscription traffic worker", func(ctx context.Context) {
			trafficWorker := scheduler.NewSubscriptionTrafficWorker(dbService, subService)
			if cfg.NotificationsEnabled {
				trafficWorker.Run(ctx)
				return
			}
			trafficWorker.RunRecovery(ctx)
		})
	}()

	if !cfg.NotificationsEnabled {
		logger.Warn("User notifications disabled by config (NOTIFICATIONS_ENABLED=false); reminder worker disabled, traffic recovery remains active")
	}

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

	// 3. Initialize logger
	logService, err := initLogger(cfg)
	if err != nil {
		// The logger failed to initialize, so the deferred flush below is not
		// registered yet: flush Sentry explicitly before exiting.
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		sentry.Flush(logger.SentryFlushTimeout)
		os.Exit(1)
	}
	defer sentry.Flush(logger.SentryFlushTimeout)
	defer func() {
		err := logService.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to close logger: %v\n", err)
		}
	}()

	// 4. Initialize database and node clients
	dbService, deps, err := initDatabase(cfg)
	if err != nil {
		logger.Fatal("Failed to initialize database", zap.Error(err))
	}
	defer func() {
		err := dbService.Close()
		if err != nil {
			logger.Error("Failed to close database", zap.Error(err))
		}
	}()
	defer func() {
		for id, access := range deps.clients {
			if access.Reader == nil {
				continue
			}

			closer, ok := access.Reader.(interface{ Close() error })
			if !ok {
				continue
			}

			err := closer.Close()
			if err != nil {
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

	// Платёжный wiring (Platega-клиент + OrderService) — здесь же, чтобы весь
	// платёжный wiring жил в одном composition root (план §7). Stars-клиент
	// подключается ниже, после создания payment-инстанса (D1).
	svc.orderService = initPaymentService(cfg, dbService, svc.subService, svc.syncService, svc.handler)

	// 6. Start web server so subscriptions are served; bot is initialised next.
	// The web server starts with an empty bot username; initBot injects the real
	// username (from Telegram getMe) via SetBotUsername once the bot is ready, so
	// the share/invite page shows the correct @username after startup.
	webServer, err := startWebServer(svc.subService, cfg, botConfig, svc.subServer, dbService, svc.orderService, botAPI, svc.syncService)
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

		err := webServer.Stop(shutdownCtx)
		if err != nil {
			logger.Error("Failed to stop web server", zap.Error(err))
		}
	}()

	// 7. Initialize Telegram bot (initBot retries internally and calls Fatal on total failure).
	logger.Info("Initializing Telegram bot...")

	api, bc, err := initBot(cfg)
	if err != nil {
		logger.Fatal("Telegram bot initialization failed", zap.Error(err))
	}

	// D1: отдельный payment-инстанс с таймаутом HTTP — invoice,
	// answerPreCheckoutQuery и refund идут через него; polling-инстанс
	// не трогаем: его long-poll не должен упираться в общий таймаут.
	// Создание инстанса повторяется: транзиентная ошибка Telegram не должна
	// ронять старт бота вместе с основным платежом Platega.
	paymentAPI, err := newPaymentBotAPI(cfg.TelegramBotToken)
	if err != nil {
		logger.Fatal("Telegram payment client initialization failed", zap.Error(err))
	}

	svc.handler.SetBot(api)
	svc.handler.SetPaymentBot(paymentAPI)
	svc.subService.SetBot(api)
	svc.handler.SetBotConfig(bc)

	if svc.orderService != nil {
		// Stars-клиент подключён всегда независимо от активного PAYMENT_PROVIDER:
		// старые Stars-invoices и возвраты должны работать и после переключения
		// на platega. Активный провайдер влияет только на новые продажи (§4.4).
		svc.orderService.SetStarsClient(stars.New(paymentAPI))

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
	u.AllowedUpdates = []string{"message", "callback_query", "pre_checkout_query"}
	updates := botAPI.GetUpdatesChan(u)

	// 9. Setup graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	// 10. Start background goroutines
	svc.handler.StartCacheCleanup(ctx, bot.CacheTTL/2)
	svc.handler.StartRateLimiterCleanup(ctx, bot.CacheTTL, bot.CacheTTL*2)
	svc.handler.StartReferralCacheSync(ctx)
	svc.handler.StartBroadcastWorker(ctx)
	bgWg := startBackgroundWorkers(ctx, svc.handler, svc.subService, svc.syncService, svc.orderService, dbService, cfg)

	if webServer != nil {
		webServer.SetPaymentReady(true)
	}

	logger.Debug("Bot started successfully")

	if webServer != nil {
		webServer.SetReady(true)
	}

	// 11. Run the main event loop (blocks until shutdown)
	runEventLoop(ctx, botAPI, svc.handler, updates, newPreCheckoutAnswerer(paymentAPI))

	// 12. Graceful shutdown of background workers
	gracefulShutdown(bgWg, svc.handler, svc.subServer)
}

// preCheckoutAnswerMaxConcurrent — сколько ответов pre_checkout_query можно
// отправлять одновременно, когда fast-lane pre-checkout переполнен.
const preCheckoutAnswerMaxConcurrent = 4

// newPreCheckoutAnswerer возвращает обработчик отказа по pre_checkout_query.
// Ответ уходит через payment-инстанс с HTTP-таймаутом 4 c (D1), в отдельной
// горутине, чтобы не блокировать event loop, и с ограниченным параллелизмом.
// Если слот ответа занят, ответ всё равно отправляется (вне семафора): ответ на
// pre_checkout_query обязателен (§5.2) — без него запрос висит до истечения
// 10-секундного окна Telegram. Число таких ответов ограничено скоростью
// pre_checkout-апдейтов, поэтому дополнительные горутины не растут без границ.
func newPreCheckoutAnswerer(paymentAPI *tgbotapi.BotAPI) func(string) {
	sem := make(chan struct{}, preCheckoutAnswerMaxConcurrent)

	answer := func(queryID string) {
		_, err := paymentAPI.Request(tgbotapi.PreCheckoutConfig{
			PreCheckoutQueryID: queryID,
			OK:                 false,
			ErrorMessage:       "Платёж не может быть принят. Обратитесь в /paysupport",
		})
		if err != nil {
			logger.Warn("failed to answer pre_checkout_query",
				zap.String("query_id", queryID),
				zap.Error(err))
		}
	}

	return func(queryID string) {
		select {
		case sem <- struct{}{}:
			go func() {
				defer func() { <-sem }()

				answer(queryID)
			}()
		default:
			logger.Warn("pre-checkout answer queue full, answering without a slot",
				zap.String("query_id", queryID))

			go answer(queryID)
		}
	}
}

// initPaymentService создаёт Platega-клиент (по гейту §4.1: активные продажи
// только с валидным конфигом) и OrderService, подключает его к хендлеру.
// Вызывается из composition root в main(); Stars-клиент подключается отдельно
// после создания payment-инстанса (D1) — он должен работать всегда, независимо
// от активного PAYMENT_PROVIDER (§4.4 плана).
func initPaymentService(cfg *config.Config, dbService *database.Service, subService *service.SubscriptionService, syncSvc *service.SyncService, handler *bot.Handler) *service.OrderService {
	var payment service.PlategaClient
	if cfg.ShouldEnablePlategaCallback() {
		payment = platega.New(platega.Config{MerchantID: cfg.PlategaMerchantID, Secret: cfg.PlategaSecret})
	}

	orderService := service.NewOrderService(dbService, subService, syncSvc, payment, "", cfg)
	handler.SetOrderService(orderService)

	return orderService
}

// handleUpdateSafely handles a Telegram update with panic recovery.
func handleUpdateSafely(ctx context.Context, handler *bot.Handler, update tgbotapi.Update) {
	defer recoverAndReport("Update handler")

	handler.HandleUpdate(ctx, update)
}
