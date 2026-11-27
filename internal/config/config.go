// Package config loads, validates, and presents application configuration.
package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	flag "github.com/kereal/rs8kvn_bot/internal/flag"
)

// Config holds all configuration for the application.
// All fields are validated before use.
type Config struct {
	// Telegram configuration
	TelegramBotToken string
	TelegramAdminID  int64

	// Database configuration
	DatabasePath string

	// Logging configuration
	LogFilePath string
	LogLevel    string

	// Monitoring configuration
	HeartbeatURL      string
	HeartbeatInterval int

	// Error tracking
	SentryDSN string

	// Health check configuration
	WebServerPort int

	// AuditLogRetentionDays — сколько дней хранится журнал аудита. По умолчанию
	// полгода (180); дневное обслуживание удаляет более старые записи.
	AuditLogRetentionDays int

	// AdminListen — адрес локальной админки (host:port). По умолчанию
	// 127.0.0.1:8881: панель не должна попадать в публичный интерфейс.
	AdminListen string

	// AdminToken — bearer-токен админки. Пустое значение полностью отключает
	// панель: сервер не биндится, эндпоинтов нет.
	AdminToken string

	// MetricsToken — shared secret для /metrics. Пустое значение оставляет
	// эндпоинт открытым (обратная совместимость и scrape с localhost), но
	// при публичном порту токен обязателен: метрики раскрывают топологию.
	MetricsToken string

	// Trial & Referral configuration
	SiteURL            string
	TrialDurationHours int
	TrialRateLimit     int

	// Contact configuration
	ContactUsername string

	// Donation configuration
	DonateCardNumber string
	DonateURL        string
	// DonateEnabled controls whether the "☕ Донат" button is shown in the main menu.
	// Defaults to true (preserves historical behaviour).
	DonateEnabled bool
	// Subscription server configuration
	GlobalSubURL           string
	SubServerAccessLogPath string

	// NotificationsEnabled controls the background workers that message users
	// (expiry reminders and traffic notifications). Defaults to true; set to
	// false for local runs against a production-DB copy.
	NotificationsEnabled bool

	// PlategaCallbackEnabled включает legacy HTTP-callback Platega для расчёта
	// старых заказов, даже когда новые продажи идут через Telegram Stars.
	// Итоговый гейт — ShouldEnablePlategaCallback.
	PlategaCallbackEnabled bool
	// StarsInvoiceTTL — локальный срок жизни Stars-invoice (payment_expires_at).
	StarsInvoiceTTL time.Duration

	// Payment configuration
	PaymentEnabled    bool
	PaymentProvider   string
	PlategaMerchantID string
	PlategaSecret     string
}

// starsInvoiceTTLDefaultRaw — строковый дефолт STARS_INVOICE_TTL, единственный
// источник: из него выводятся и регистрация флага, и DefaultStarsInvoiceTTL.
const starsInvoiceTTLDefaultRaw = "15m"

// DefaultStarsInvoiceTTL — срок жизни Stars-invoice, когда STARS_INVOICE_TTL не задан.
var DefaultStarsInvoiceTTL = mustDuration(starsInvoiceTTLDefaultRaw)

// mustDuration парсит встроенный дефолт длительности. Panic допустим только
// здесь: ошибка — программный инвариант встроенной константы, не данные.
func mustDuration(raw string) time.Duration {
	d, err := time.ParseDuration(raw)
	if err != nil {
		panic(fmt.Sprintf("config: built-in default duration %q is invalid: %v", raw, err))
	}

	return d
}

// paymentProviders — допустимые значения PAYMENT_PROVIDER при PAYMENT_ENABLED=true.
var paymentProviders = map[string]bool{"platega": true, "telegram_stars": true}

// configFlags holds typed flag values for config fields.
type configFlags struct {
	telegramBotToken       *flag.StringValue
	telegramAdminID        *flag.Int64Value
	databasePath           *flag.StringValue
	logFilePath            *flag.StringValue
	logLevel               *flag.StringValue
	heartbeatURL           *flag.StringValue
	heartbeatInterval      *flag.IntValue
	sentryDSN              *flag.StringValue
	webServerPort          *flag.IntValue
	siteURL                *flag.StringValue
	trialDurationHours     *flag.IntValue
	trialRateLimit         *flag.IntValue
	contactUsername        *flag.StringValue
	donateCardNumber       *flag.StringValue
	donateURL              *flag.StringValue
	donateEnabled          *flag.BoolValue
	globalSubURL           *flag.StringValue
	subServerAccessLogPath *flag.StringValue
	paymentEnabled         *flag.BoolValue
	notificationsEnabled   *flag.BoolValue
	metricsToken           *flag.StringValue
	adminListen            *flag.StringValue
	adminToken             *flag.StringValue
	auditRetentionDays     *flag.IntValue
	paymentProvider        *flag.StringValue
	plategaMerchantID      *flag.StringValue
	plategaSecret          *flag.StringValue
	plategaCallbackEnabled *flag.BoolValue
	starsInvoiceTTL        *flag.StringValue
}

// registerFlags creates a new flag.Registry and initializes a configFlags instance with defaults,
// registering each configuration entry under its corresponding environment variable name.
// It returns the registry and the populated configFlags.
func registerFlags() (*flag.Registry, *configFlags) {
	r := flag.New()
	f := &configFlags{
		telegramBotToken: flag.NewString(""), telegramAdminID: flag.NewInt64(0),
		databasePath: flag.NewString(DefaultDatabasePath), logFilePath: flag.NewString(DefaultLogFilePath),
		logLevel: flag.NewString(DefaultLogLevel), heartbeatURL: flag.NewString(""),
		heartbeatInterval: flag.NewInt(DefaultHeartbeatInterval), sentryDSN: flag.NewString(""),
		webServerPort: flag.NewInt(DefaultWebServerPort), siteURL: flag.NewString(DefaultSiteURL),
		trialDurationHours: flag.NewInt(DefaultTrialDurationHours), trialRateLimit: flag.NewInt(DefaultTrialRateLimit),
		contactUsername: flag.NewString(ContactUsername), donateCardNumber: flag.NewString(DonateCardNumber),
		donateURL: flag.NewString(DonateURL), donateEnabled: flag.NewBool(DefaultDonateEnabled),
		globalSubURL: flag.NewString(""), subServerAccessLogPath: flag.NewString(""),
		paymentEnabled: flag.NewBool(false), paymentProvider: flag.NewString("platega"), plategaMerchantID: flag.NewString(""), plategaSecret: flag.NewString(""),
		plategaCallbackEnabled: flag.NewBool(false), starsInvoiceTTL: flag.NewString(starsInvoiceTTLDefaultRaw),
		notificationsEnabled: flag.NewBool(DefaultNotificationsEnabled), metricsToken: flag.NewString(""),
		adminListen: flag.NewString(DefaultAdminListen), adminToken: flag.NewString(""),
		auditRetentionDays: flag.NewInt(DefaultAuditLogRetentionDays),
	}
	r.Register("TELEGRAM_BOT_TOKEN", f.telegramBotToken)
	r.Register("TELEGRAM_ADMIN_ID", f.telegramAdminID)
	r.Register("GLOBAL_SUB_URL", f.globalSubURL)
	r.Register("DATABASE_PATH", f.databasePath)
	r.Register("LOG_FILE_PATH", f.logFilePath)
	r.Register("LOG_LEVEL", f.logLevel)
	r.Register("HEARTBEAT_URL", f.heartbeatURL)
	r.Register("HEARTBEAT_INTERVAL", f.heartbeatInterval)
	r.Register("SENTRY_DSN", f.sentryDSN)
	r.Register("WEB_SERVER_PORT", f.webServerPort)
	r.Register("SITE_URL", f.siteURL)
	r.Register("TRIAL_DURATION_HOURS", f.trialDurationHours)
	r.Register("TRIAL_RATE_LIMIT", f.trialRateLimit)
	r.Register("CONTACT_USERNAME", f.contactUsername)
	r.Register("DONATE_CARD_NUMBER", f.donateCardNumber)
	r.Register("DONATE_URL", f.donateURL)
	r.Register("DONATE_ENABLED", f.donateEnabled)
	r.Register("SUBSERVER_ACCESS_LOG", f.subServerAccessLogPath)
	r.Register("PAYMENT_ENABLED", f.paymentEnabled)
	r.Register("NOTIFICATIONS_ENABLED", f.notificationsEnabled)
	r.Register("PAYMENT_PROVIDER", f.paymentProvider)
	r.Register("PLATEGA_MERCHANT_ID", f.plategaMerchantID)
	r.Register("PLATEGA_SECRET", f.plategaSecret)
	r.Register("PLATEGA_CALLBACK_ENABLED", f.plategaCallbackEnabled)
	r.Register("STARS_INVOICE_TTL", f.starsInvoiceTTL)
	r.Register("METRICS_TOKEN", f.metricsToken)
	r.Register("ADMIN_LISTEN", f.adminListen)
	r.Register("ADMIN_TOKEN", f.adminToken)
	r.Register("AUDIT_LOG_RETENTION_DAYS", f.auditRetentionDays)

	return r, f
}

// Load reads configuration from environment variables and validates it.
// Load loads configuration from environment variables, constructs a Config from the parsed flag values, and validates it.
// It returns the validated Config on success or an error if environment loading or validation fails.
func Load() (*Config, error) {
	r, f := registerFlags()

	err := r.LoadEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	cfg := &Config{
		TelegramBotToken: f.telegramBotToken.Get(), TelegramAdminID: f.telegramAdminID.Get(),
		DatabasePath: f.databasePath.Get(), LogFilePath: f.logFilePath.Get(), LogLevel: f.logLevel.Get(),
		HeartbeatURL: f.heartbeatURL.Get(), HeartbeatInterval: f.heartbeatInterval.Get(), SentryDSN: f.sentryDSN.Get(),
		WebServerPort: f.webServerPort.Get(), MetricsToken: f.metricsToken.Get(),
		AdminListen: f.adminListen.Get(), AdminToken: f.adminToken.Get(),
		AuditLogRetentionDays: f.auditRetentionDays.Get(), SiteURL: f.siteURL.Get(),
		TrialDurationHours: f.trialDurationHours.Get(), TrialRateLimit: f.trialRateLimit.Get(),
		ContactUsername: f.contactUsername.Get(), DonateCardNumber: f.donateCardNumber.Get(), DonateURL: f.donateURL.Get(),
		DonateEnabled: f.donateEnabled.Get(), GlobalSubURL: f.globalSubURL.Get(),
		SubServerAccessLogPath: f.subServerAccessLogPath.Get(),
		PaymentEnabled:         f.paymentEnabled.Get(), PaymentProvider: f.paymentProvider.Get(),
		NotificationsEnabled: f.notificationsEnabled.Get(),
		PlategaMerchantID:    f.plategaMerchantID.Get(), PlategaSecret: f.plategaSecret.Get(),
		PlategaCallbackEnabled: f.plategaCallbackEnabled.Get(),
	}

	ttl, err := time.ParseDuration(f.starsInvoiceTTL.Get())
	if err != nil {
		return nil, fmt.Errorf("configuration validation failed: STARS_INVOICE_TTL is not a valid duration: %w", err)
	}
	cfg.StarsInvoiceTTL = ttl

	// Validate all required fields
	err = cfg.validate()
	if err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// validate checks that all configuration values are valid.
func (c *Config) validate() error {
	// Telegram validation
	if c.TelegramBotToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}

	if !strings.Contains(c.TelegramBotToken, ":") {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN appears to be invalid (expected format: 'number:token')")
	}

	if c.TelegramAdminID <= 0 {
		return fmt.Errorf("TELEGRAM_ADMIN_ID must be positive")
	}

	// Heartbeat validation
	if c.HeartbeatInterval < MinHeartbeatInterval {
		return fmt.Errorf("HEARTBEAT_INTERVAL must be at least %d seconds", MinHeartbeatInterval)
	}

	// Health check port validation
	if c.WebServerPort < 1 || c.WebServerPort > 65535 {
		return fmt.Errorf("WEB_SERVER_PORT must be between 1 and 65535")
	}

	// Log level validation
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		return fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error")
	}

	// Sentry DSN validation (optional but if provided, should be valid URL)
	if c.SentryDSN != "" {
		err := c.validateURL("SENTRY_DSN", c.SentryDSN)
		if err != nil {
			return err
		}
	}

	// Heartbeat URL validation (optional but if provided, should be valid URL)
	if c.HeartbeatURL != "" {
		err := c.validateURL("HEARTBEAT_URL", c.HeartbeatURL)
		if err != nil {
			return err
		}
	}

	// PAYMENT_PROVIDER: allowlist при включённых оплатах.
	if c.PaymentEnabled && !paymentProviders[c.PaymentProvider] {
		return fmt.Errorf("PAYMENT_PROVIDER must be one of: platega, telegram_stars (got %q)", c.PaymentProvider)
	}

	// Platega-креды обязательны, только если Platega реально используется:
	// активный провайдер platega при включённых оплатах либо включённый
	// legacy-callback (в т.ч. для расчёта старых заказов при провайдере Stars).
	// Выключенные оплаты с дефолтным провайдером и пустыми кредами — валидный
	// конфиг (.env.example без оплат).
	if c.plategaInUse() {
		if strings.TrimSpace(c.PlategaMerchantID) == "" || strings.TrimSpace(c.PlategaSecret) == "" {
			return fmt.Errorf("PLATEGA_MERCHANT_ID and PLATEGA_SECRET are required when Platega is in use (PAYMENT_ENABLED=true with PAYMENT_PROVIDER=platega, or PLATEGA_CALLBACK_ENABLED=true)")
		}

		_, err := uuid.Parse(strings.TrimSpace(c.PlategaMerchantID))
		if err != nil {
			return fmt.Errorf("PLATEGA_MERCHANT_ID must be a UUID: %w", err)
		}
	}

	// Локальный TTL Stars-invoice должен быть положительным.
	if c.StarsInvoiceTTL <= 0 {
		return fmt.Errorf("STARS_INVOICE_TTL must be positive (default %s)", DefaultStarsInvoiceTTL)
	}
	// Global subscription URL validation (required)
	if c.GlobalSubURL == "" {
		return fmt.Errorf("GLOBAL_SUB_URL is required")
	}

	err := c.validateURL("GLOBAL_SUB_URL", c.GlobalSubURL)
	if err != nil {
		return err
	}

	c.GlobalSubURL = strings.TrimRight(c.GlobalSubURL, "/") + "/"

	// Site URL validation
	err = c.validateURL("SITE_URL", c.SiteURL)
	if err != nil {
		return err
	}

	// Trial duration validation
	if c.TrialDurationHours < 1 || c.TrialDurationHours > 168 {
		return fmt.Errorf("TRIAL_DURATION_HOURS must be between 1 and 168 (max 7 days)")
	}

	// Trial rate limit validation
	if c.TrialRateLimit < 1 || c.TrialRateLimit > 100 {
		return fmt.Errorf("TRIAL_RATE_LIMIT must be between 1 and 100")
	}

	// Admin panel validation. The token is optional (empty disables the panel),
	// but a configured token must come with a bindable host:port address.
	// Whitespace-only tokens are treated the same as empty ones by the runtime
	// (TrimSpace in startAdminServer), so validation must agree.
	if strings.TrimSpace(c.AdminToken) != "" {
		host, port, splitErr := net.SplitHostPort(c.AdminListen)
		if splitErr != nil {
			return fmt.Errorf("ADMIN_LISTEN must be host:port: %w", splitErr)
		}

		if host == "" {
			return fmt.Errorf("ADMIN_LISTEN must include a host (use 127.0.0.1 for the default)")
		}

		portNum, convErr := strconv.Atoi(port)
		if convErr != nil || portNum < 1 || portNum > 65535 {
			return fmt.Errorf("ADMIN_LISTEN port must be between 1 and 65535")
		}

		if portNum == c.WebServerPort {
			return fmt.Errorf("ADMIN_LISTEN must differ from WEB_SERVER_PORT")
		}
	}

	// Audit retention validation. Ноль означает «взять значение по умолчанию»
	// (полгода) — так пустая настройка не удаляет всю историю. Отрицательные
	// значения запрещены, верхняя граница — 10 лет.
	if c.AuditLogRetentionDays < 0 || c.AuditLogRetentionDays > 3650 {
		return fmt.Errorf("AUDIT_LOG_RETENTION_DAYS must be between 0 (default %d) and 3650 (10 years)", DefaultAuditLogRetentionDays)
	}

	return nil
}

// validateURL checks if a URL string is valid.
func (c *Config) validateURL(name, value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", name, err)
	}

	// Check that scheme is present and allowed
	switch u.Scheme {
	case "http", "https":
		// allowed
	default:
		return fmt.Errorf("%s must use http or https scheme", name)
	}

	// Check that host is present
	if u.Host == "" {
		return fmt.Errorf("%s must include a host", name)
	}

	return nil
}

// String returns a safe string representation of the config (without sensitive data).
func (c *Config) String() string {
	adminURL := c.AdminURL()
	if adminURL == "" {
		adminURL = "(disabled)"
	}

	return fmt.Sprintf("Config{"+
		"TelegramBotToken=***, "+
		"TelegramAdminID=%d, "+
		"DatabasePath=%s, "+
		"LogFilePath=%s, "+
		"LogLevel=%s, "+
		"HeartbeatURL=%s, "+
		"HeartbeatInterval=%d, "+
		"GlobalSubURL=%s, "+
		"AdminURL=%s, "+
		"AdminToken=%s, "+
		"SentryDSN=***, "+
		"PaymentEnabled=%v, "+
		"PaymentProvider=%s, "+
		"PlategaCallbackEnabled=%v, "+
		"StarsInvoiceTTL=%s, "+
		"}",
		c.TelegramAdminID,
		c.DatabasePath,
		c.LogFilePath,
		c.LogLevel,
		maskURL(c.HeartbeatURL),
		c.HeartbeatInterval,
		maskURL(c.GlobalSubURL),
		adminURL,
		maskedPresence(c.AdminToken),
		c.PaymentEnabled,
		c.PaymentProvider,
		c.PlategaCallbackEnabled,
		c.StarsInvoiceTTL,
	)
}

// AdminURL returns the address of the admin panel as a clickable URL, or an
// empty string when the panel is disabled (empty ADMIN_TOKEN means no listener
// at all). A wildcard bind address (0.0.0.0, :: or a bare port) becomes
// 127.0.0.1: the browser needs an address it can actually reach, while the
// panel's own bind address stays visible in the startup log.
func (c *Config) AdminURL() string {
	if strings.TrimSpace(c.AdminToken) == "" {
		return ""
	}

	host, port, err := net.SplitHostPort(c.AdminListen)
	if err != nil {
		return ""
	}

	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, port) + "/admin/"
}

// plategaInUse — Platega реально используется: активный провайдер platega
// при включённых оплатах, либо явно включённый legacy-callback (расчёт
// старых заказов). Единственный источник формулы для валидации кредов и гейта.
func (c *Config) plategaInUse() bool {
	return (c.PaymentEnabled && c.PaymentProvider == "platega") || c.PlategaCallbackEnabled
}

// ShouldEnablePlategaCallback — итоговый гейт legacy HTTP-callback Platega.
func (c *Config) ShouldEnablePlategaCallback() bool {
	return c.plategaInUse()
}

// SubURL builds a full subscription URL from a subscription ID.
func (c *Config) SubURL(subID string) string {
	u, err := url.JoinPath(c.GlobalSubURL, subID)
	if err != nil {
		return c.GlobalSubURL + subID
	}

	return u
}

// maskedPresence reports whether a secret is configured without printing it.
func maskedPresence(secret string) string {
	if strings.TrimSpace(secret) == "" {
		return "(disabled)"
	}

	return "***"
}

// maskURL returns a masked version of a URL for logging purposes.
func maskURL(urlStr string) string {
	if urlStr == "" {
		return "(not set)"
	}

	u, err := url.Parse(urlStr)
	if err != nil {
		return "***"
	}

	// Show scheme and host only
	return fmt.Sprintf("%s://%s/***", u.Scheme, u.Host)
}
