package bot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/metrics"
	"rs8kvn_bot/internal/ratelimiter"
	"rs8kvn_bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// ErrRateLimited indicates the update was rate-limited.
var ErrRateLimited = errors.New("rate limited")

// Cache constants
const (
	CacheMaxSize   = 1000
	CacheTTL       = 5 * time.Minute
	HandlerTimeout = 30 * time.Second
)

// pendingInvite stores pending invite code with TTL
type pendingInvite struct {
	code      string
	expiresAt time.Time
}

// Handler is the main bot handler that orchestrates sub-handlers.
type Handler struct {
	bot                 interfaces.BotAPI
	cfg                 *config.Config
	db                  interfaces.DatabaseService
	xui                 interfaces.XUIClient
	rateLimiter         *ratelimiter.PerUserRateLimiter
	cache               *SubscriptionCache
	inProgressSyncMap   sync.Map                // atomic tracking of subscription creation
	pendingInvites      map[int64]pendingInvite // chatID -> invite_code
	pendingMu           sync.RWMutex
	botConfig           *BotConfig
	subscriptionService *service.SubscriptionService
	referralCache       *ReferralCache
	sender              *MessageSender
	keyboards           *KeyboardBuilder
	version             string
	referral            *ReferralHandler

	// Admin rate limiting (separate from user rate limit)
	adminRateLimiters map[int64]*ratelimiter.TokenBucket
	adminRateLimitMu  sync.RWMutex

	// Decomposed handlers
	cmdHandler *CommandHandler
	cbHandler  *CallbackHandler
	subHandler *SubscriptionHandler

	// Lazy init guards (for test-constructed handlers or deferred init)
	subHandlerOnce sync.Once
	referralOnce   sync.Once
	keyboardsOnce  sync.Once
}

// NewHandler creates a new Handler with all sub-handlers initialized.
func NewHandler(bot interfaces.BotAPI, cfg *config.Config, db interfaces.DatabaseService, xuiClient interfaces.XUIClient, botConfig *BotConfig, subService *service.SubscriptionService, version string) *Handler {
	rl := ratelimiter.NewPerUserRateLimiter(float64(config.RateLimiterMaxTokens), float64(config.RateLimiterRefillRate))
	kb := NewKeyboardBuilder(botConfig.Username, cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)

	h := &Handler{
		bot:                 bot,
		cfg:                 cfg,
		db:                  db,
		xui:                 xuiClient,
		rateLimiter:         rl,
		cache:               NewSubscriptionCache(CacheMaxSize, CacheTTL),
		inProgressSyncMap:   sync.Map{},
		pendingInvites:      make(map[int64]pendingInvite),
		pendingMu:           sync.RWMutex{},
		botConfig:           botConfig,
		subscriptionService: subService,
		referralCache:       NewReferralCache(db),
		sender:              NewMessageSender(bot, rl),
		keyboards:           kb,
		version:             version,
	}
	// Initialize admin rate limiters map
	h.adminRateLimiters = make(map[int64]*ratelimiter.TokenBucket)

	// Initialize decomposed handlers
	h.cmdHandler = NewCommandHandler(h)
	h.cbHandler = NewCallbackHandler(h)
	h.subHandler = NewSubscriptionHandler(h)
	// Referral handler
	h.referral = NewReferralHandler(h.db, h.cfg, h.bot, h.botConfig, h.sender, kb)

	// Wire cache invalidation to centralized service (if service is present)
	if h.subscriptionService != nil {
		h.subscriptionService.SetInvalidateFunc(h.cache.Invalidate)
	}

	return h
}

// isAdmin returns true if chatID matches configured admin ID
func (h *Handler) isAdmin(chatID int64) bool {
	return h.cfg.TelegramAdminID > 0 && chatID == h.cfg.TelegramAdminID
}

// withTimeout returns a context with the standard handler timeout.
func (h *Handler) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, HandlerTimeout)
}

// Command delegates
func (h *Handler) HandleStart(ctx context.Context, update tgbotapi.Update) error {
	if h.cmdHandler != nil {
		return h.cmdHandler.HandleStart(ctx, update)
	}
	return nil
}

func (h *Handler) HandleHelp(ctx context.Context, update tgbotapi.Update) error {
	if h.cmdHandler != nil {
		return h.cmdHandler.HandleHelp(ctx, update)
	}
	return nil
}

func (h *Handler) HandleInvite(ctx context.Context, update tgbotapi.Update) error {
	if h.cmdHandler != nil {
		return h.cmdHandler.HandleInvite(ctx, update)
	}
	return nil
}

// Command private delegates
func (h *Handler) handleBindTrial(ctx context.Context, chatID int64, username, subscriptionID string) error {
	if h.cmdHandler != nil {
		return h.cmdHandler.handleBindTrial(ctx, chatID, username, subscriptionID)
	}
	return nil
}

func (h *Handler) handleShareStart(ctx context.Context, chatID int64, username, inviteCode string) error {
	if h.cmdHandler != nil {
		return h.cmdHandler.handleShareStart(ctx, chatID, username, inviteCode)
	}
	return nil
}

// Callback delegate
func (h *Handler) HandleCallback(ctx context.Context, update tgbotapi.Update) error {
	if h.cbHandler != nil {
		return h.cbHandler.HandleCallback(ctx, update)
	}
	return nil
}

// Callback private delegates
func (h *Handler) handleShareInvite(ctx context.Context, chatID int64, username string, messageID int) error {
	if h.cbHandler != nil {
		return h.cbHandler.handleShareInvite(ctx, chatID, username, messageID)
	}
	return nil
}

func (h *Handler) handleQRTelegram(ctx context.Context, chatID int64, username string, messageID int) error {
	if h.cbHandler != nil {
		return h.cbHandler.handleQRTelegram(ctx, chatID, username, messageID)
	}
	return nil
}

func (h *Handler) handleQRWeb(ctx context.Context, chatID int64, username string, messageID int) error {
	if h.cbHandler != nil {
		return h.cbHandler.handleQRWeb(ctx, chatID, username, messageID)
	}
	return nil
}

// Subscription delegates
func (h *Handler) handleCreateSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.handleCreateSubscription(ctx, chatID, username, messageID)
}

func (h *Handler) handleMySubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.handleMySubscription(ctx, chatID, username, messageID)
}

func (h *Handler) handleQRCode(ctx context.Context, chatID int64, username string, messageID int) error {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.handleQRCode(ctx, chatID, username, messageID)
}

func (h *Handler) handleBackToSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.handleBackToSubscription(ctx, chatID, username, messageID)
}

func (h *Handler) sendQRCode(ctx context.Context, chatID int64, messageID int, url string, caption string) error {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.sendQRCode(ctx, chatID, messageID, url, caption)
}

func (h *Handler) handleBackToInvite(ctx context.Context, chatID int64, username string, messageID int) error {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.handleBackToInvite(ctx, chatID, username, messageID)
}

func (h *Handler) getSubscriptionWithCache(ctx context.Context, chatID int64) (*database.Subscription, error) {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.getSubscriptionWithCache(ctx, chatID)
}

// invalidateCache clears the subscription cache for the given chatID.
// It uses centralized SubscriptionService if available, otherwise falls back to direct cache access.
func (h *Handler) invalidateCache(chatID int64) {
	if h.subscriptionService != nil {
		_ = h.subscriptionService.InvalidateSubscription(context.Background(), chatID)
		return
	}
	h.cache.Invalidate(chatID)
}

// Subscription direct delegates (used by tests and internal flows)
func (h *Handler) createSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.createSubscription(ctx, chatID, username, messageID)
}

func (h *Handler) handleCreateError(ctx context.Context, chatID int64, messageID int, username string, err error) error {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler.handleCreateError(ctx, chatID, messageID, username, err)
}

// Referral delegates
func (h *Handler) generateInviteLink(ctx context.Context, chatID int64, lt linkType) (string, error) {
	h.referralOnce.Do(func() {
		h.referral = NewReferralHandler(h.db, h.cfg, h.bot, h.botConfig, h.sender, h.keyboards)
	})
	return h.referral.generateInviteLink(ctx, chatID, lt)
}

func (h *Handler) sendInviteLink(ctx context.Context, chatID int64, messageID int) error {
	h.referralOnce.Do(func() {
		h.referral = NewReferralHandler(h.db, h.cfg, h.bot, h.botConfig, h.sender, h.keyboards)
	})
	return h.referral.sendInviteLink(ctx, chatID, messageID)
}

// Utility methods
func (h *Handler) getUsername(user *tgbotapi.User) string {
	if user == nil {
		return "unknown"
	}
	if user.UserName != "" {
		return user.UserName
	}
	if user.FirstName != "" {
		return user.FirstName
	}
	return fmt.Sprintf("user_%d", user.ID)
}

func (h *Handler) getMainMenuContent(username string, hasSubscription bool, chatID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	// Ensure keyboards is initialized (for manually constructed handlers in tests)
	h.keyboardsOnce.Do(func() {
		if h.keyboards == nil {
			h.keyboards = NewKeyboardBuilder("", "", "", "", "")
		}
	})
	var keyboard tgbotapi.InlineKeyboardMarkup
	if hasSubscription {
		keyboard = h.keyboards.MainMenu(true)
	} else {
		keyboard = h.keyboards.CreateSubscription()
	}
	// Add admin buttons if the user is an admin
	h.addAdminButtons(&keyboard, chatID)
	var text string
	if hasSubscription {
		text = msg(MsgStartGreeting, username)
	} else {
		text = msg(MsgStartGreetingNoSub, username)
	}
	return text, keyboard
}

func (h *Handler) getHelpText(trafficLimitGB int, subscriptionURL string) string {
	// Use the detailed help from KeyboardBuilder which includes setup instructions.
	return h.keyboards.HelpText(trafficLimitGB, subscriptionURL)
}

func (h *Handler) getDonateText() string {
	return h.keyboards.DonateText()
}

func (h *Handler) getBackKeyboard() tgbotapi.InlineKeyboardMarkup {
	return h.keyboards.Back()
}

func (h *Handler) getQRKeyboard() tgbotapi.InlineKeyboardMarkup {
	return h.keyboards.QR()
}

func (h *Handler) getMainMenuKeyboard(hasSubscription bool) tgbotapi.InlineKeyboardMarkup {
	return h.keyboards.MainMenu(hasSubscription)
}

// addAdminButtons appends admin control buttons to a keyboard if the user is an admin.
func (h *Handler) addAdminButtons(keyboard *tgbotapi.InlineKeyboardMarkup, chatID int64) {
	if h.isAdmin(chatID) {
		adminRow := tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
			tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
		)
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, adminRow)
	}
}

// Messaging helpers
func (h *Handler) send(ctx context.Context, msg tgbotapi.MessageConfig) {
	h.sender.Send(ctx, msg)
}

func (h *Handler) sendWithError(ctx context.Context, msg tgbotapi.MessageConfig) error {
	return h.sender.SendWithError(ctx, msg)
}

// safeSend sends a message without rate limiting and logs errors; returns true on success.
func (h *Handler) safeSend(chattable tgbotapi.Chattable) bool {
	_, err := h.bot.Send(chattable)
	if err != nil {
		logger.Error("Failed to send message", zap.Error(err))
		return false
	}
	return true
}

func (h *Handler) SendMessage(ctx context.Context, chatID int64, text string) {
	h.sender.SendMessage(ctx, chatID, text)
}

func (h *Handler) showLoadingMessage(chatID int64, messageID int) int {
	if messageID == 0 {
		msg := tgbotapi.NewMessage(chatID, "⏳ Загрузка...")
		sentMsg, err := h.bot.Send(msg)
		if err != nil {
			logger.Error("Failed to send loading message", zap.Error(err))
			return 0
		}
		return sentMsg.MessageID
	}
	edit := tgbotapi.NewEditMessageText(chatID, messageID, "⏳ Загрузка...")
	if !h.safeSend(edit) {
		return 0
	}
	return messageID
}

func (h *Handler) checkRateLimit(chatID int64) bool {
	return h.rateLimiter.Allow(chatID)
}

func (h *Handler) handleRateLimitExceeded(chatID int64, messageID int) {
	msgText := "❌ Слишком много запросов. Пожалуйста, подождите минуту."
	if messageID > 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, msgText)
		h.safeSend(edit)
	} else {
		h.sender.SendMessage(context.Background(), chatID, msgText)
	}
}

// Referral cache management (used by CommandHandler)
func (h *Handler) GetReferralCount(chatID int64) int64 {
	return h.referralCache.Get(chatID)
}

func (h *Handler) IncrementReferralCount(chatID int64) {
	h.referralCache.Increment(chatID)
}

func (h *Handler) DecrementReferralCount(chatID int64) {
	h.referralCache.Decrement(chatID)
}

func (h *Handler) SyncReferralCache(ctx context.Context) error {
	return h.referralCache.Sync(ctx)
}

func (h *Handler) StartReferralCacheSync(ctx context.Context) {
	h.referralCache.StartSync(ctx)
}

func (h *Handler) LoadReferralCache(ctx context.Context) error {
	return h.referralCache.Sync(ctx)
}

// GetSubscriptionService returns the subscription service.
// It is primarily for starting background tasks that need access to the service.
func (h *Handler) GetSubscriptionService() *service.SubscriptionService {
	return h.subscriptionService
}

// Lifecycle
func (h *Handler) StartCacheCleanup(ctx context.Context, interval time.Duration) {
	go h.cache.StartCleanup(ctx, interval)
}

func (h *Handler) StartRateLimiterCleanup(ctx context.Context, interval, maxIdle time.Duration) {
	// Start cleanup in a separate goroutine to avoid blocking.
	go h.rateLimiter.StartCleanup(ctx, interval, maxIdle)
}

// checkAdminSendRateLimit checks if an admin can send a message (rate limit: 1 per minute).
func (h *Handler) checkAdminSendRateLimit(chatID int64) bool {
	if h.adminRateLimiters == nil {
		// Handler created without NewHandler (e.g., in tests); initialize lazily
		h.adminRateLimitMu.Lock()
		if h.adminRateLimiters == nil {
			h.adminRateLimiters = make(map[int64]*ratelimiter.TokenBucket)
		}
		h.adminRateLimitMu.Unlock()
	}
	h.adminRateLimitMu.Lock()
	bucket, ok := h.adminRateLimiters[chatID]
	if !ok {
		// Create a new token bucket: 1 token, refills every minute (1/60 per second)
		bucket = ratelimiter.NewTokenBucket(1, 1.0/60.0)
		h.adminRateLimiters[chatID] = bucket
	}
	h.adminRateLimitMu.Unlock()
	return bucket.Allow()
}

// ClearAdminSendRateLimit resets the rate limit for an admin, allowing immediate send.
func (h *Handler) ClearAdminSendRateLimit(chatID int64) {
	if h.adminRateLimiters == nil {
		return
	}
	h.adminRateLimitMu.Lock()
	defer h.adminRateLimitMu.Unlock()
	if bucket, ok := h.adminRateLimiters[chatID]; ok {
		bucket.Reset()
	}
}

// Main update router
func (h *Handler) HandleUpdate(ctx context.Context, update tgbotapi.Update) {
	start := time.Now()
	defer func() {
		metrics.BotUpdateDuration.Observe(time.Since(start).Seconds())
	}()

	// Rate limiting: extract chat ID and check for non-admin users
	var chatID int64
	var command string
	if update.Message != nil {
		chatID = update.Message.Chat.ID
		if update.Message.IsCommand() {
			command = update.Message.Command()
		}
	} else if update.CallbackQuery != nil {
		if update.CallbackQuery.Message != nil {
			chatID = update.CallbackQuery.Message.Chat.ID
		} else if update.CallbackQuery.From != nil {
			chatID = update.CallbackQuery.From.ID
		}
		command = "callback"
	}

	var err error
	if chatID != 0 && !h.isAdmin(chatID) && !h.checkRateLimit(chatID) {
		h.handleRateLimitExceeded(chatID, 0)
		metrics.BotUpdatesTotal.WithLabelValues(command, "rate_limited").Inc()
		err = ErrRateLimited
		return
	}
	defer func() {
		if err != nil {
			metrics.BotUpdateErrorsTotal.WithLabelValues(command).Inc()
			metrics.BotUpdatesTotal.WithLabelValues(command, "error").Inc()
		} else {
			metrics.BotUpdatesTotal.WithLabelValues(command, "success").Inc()
		}
	}()

	if update.Message != nil {
		if update.Message.IsCommand() {
			switch update.Message.Command() {
			case "start":
				err = h.HandleStart(ctx, update)
			case "help":
				err = h.HandleHelp(ctx, update)
			case "invite":
				err = h.HandleInvite(ctx, update)
			case "del":
				err = h.HandleDel(ctx, update)
			case "broadcast":
				err = h.HandleBroadcast(ctx, update)
			case "send":
				err = h.HandleSend(ctx, update)
			case "refstats":
				err = h.HandleRefstats(ctx, update)
			case "v":
				err = h.HandleVersion(ctx, update)
			default:
				h.SendMessage(ctx, update.Message.Chat.ID,
					"Неизвестная команда. Используйте /start или /help")
			}
		} else {
			// Non-command message: send help hint
			err = h.HandleHelp(ctx, update)
		}
	} else if update.CallbackQuery != nil {
		err = h.HandleCallback(ctx, update)
	}
}
