package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/ratelimiter"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

var _ interfaces.BotAPI = (*tgbotapi.BotAPI)(nil)

// ErrRateLimited indicates the update was rate-limited.
var ErrRateLimited = errors.New("rate limited")

// Cache constants
const (
	CacheMaxSize   = 1000
	CacheTTL       = 5 * time.Minute
	HandlerTimeout = 30 * time.Second
)

// normalizeCommand normalizes a raw command string to a fixed set of metric
// label values. Known commands are returned as-is; anything else becomes "unknown".
func normalizeCommand(cmd string) string {
	switch cmd {
	case "start", "help", "invite", "del", "broadcast", "send", "refstats", "v", "lastreg":
		return cmd
	default:
		return "unknown"
	}
}

// pendingInvite stores pending invite code with TTL
type pendingInvite struct {
	code      string
	expiresAt time.Time
}

// PendingInviteTTL -- время жизни pending invite в кэше
const PendingInviteTTL = 60 * time.Minute

type Handler struct {
	noCopy              noCopy
	bot                 interfaces.BotAPI
	cfg                 *config.Config
	db                  interfaces.DatabaseService
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
	orderService        *service.OrderService
	version             string
	referral            *ReferralHandler

	// Admin rate limiting (separate from user rate limit)
	adminRateLimiters map[int64]*ratelimiter.TokenBucket
	adminRateLimitMu  sync.RWMutex

	// Broadcast draft -> preview -> confirm session state (admin only)
	broadcastSessions map[int64]*broadcastSession
	broadcastMu       sync.Mutex

	// Decomposed handlers
	cmdHandler *CommandHandler
	cbHandler  *CallbackHandler
	subHandler *SubscriptionHandler

	// Background goroutine tracking
	bgWg sync.WaitGroup

	// Lazy init guards (for test-constructed handlers or deferred init)
	subHandlerOnce sync.Once
	referralOnce   sync.Once
	keyboardsOnce  sync.Once
}

// NewHandler creates a new Handler with all sub-handlers initialized.
func NewHandler(bot interfaces.BotAPI, cfg *config.Config, db interfaces.DatabaseService, botConfig *BotConfig, subService *service.SubscriptionService, version string) *Handler {
	rl := ratelimiter.NewPerUserRateLimiter(float64(config.RateLimiterMaxTokens), float64(config.RateLimiterRefillRate))
	kb := NewKeyboardBuilder(botConfig.Username, cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)

	h := &Handler{
		bot:                 bot,
		cfg:                 cfg,
		db:                  db,
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
	h.broadcastSessions = make(map[int64]*broadcastSession)

	// Initialize decomposed handlers
	h.cmdHandler = NewCommandHandler(h)
	h.cbHandler = NewCallbackHandler(h)
	h.subHandler = NewSubscriptionHandler(h)
	// Referral handler
	h.referral = NewReferralHandler(h.db, h.cfg, h.bot, h.botConfig, h.sender, kb)

	// Wire cache invalidation to centralized service (if service is present)
	if h.subscriptionService != nil {
		h.subscriptionService.SetInvalidateFunc(h.cache.Invalidate)
		h.subscriptionService.SetInvalidateBySubIDFunc(h.cache.InvalidateBySubID)
	}

	return h
}

// SetBot updates the bot client and propagates the change to the message sender.
func (h *Handler) SetBot(bot interfaces.BotAPI) {
	h.bot = bot
	h.sender.SetBot(bot)
}

// SetBotConfig updates runtime bot config and rebuilds keyboard templates.
func (h *Handler) SetBotConfig(bc *BotConfig) {
	h.botConfig = bc
	h.keyboards = NewKeyboardBuilder(bc.Username, h.cfg.ContactUsername, h.cfg.DonateCardNumber, h.cfg.DonateURL, h.cfg.SiteURL)
	// Propagate to decomposed handlers so generated links (invite/share) use the
	// real bot username instead of the startup placeholder.
	h.referral.SetBotConfig(bc)
}

// Cache returns the subscription cache.
// Used by external callers (e.g. main.go) to compose cache invalidation across multiple caches.
func (h *Handler) Cache() *SubscriptionCache {
	return h.cache
}

// SetOrderService wires the order service after handler construction.
func (h *Handler) SetOrderService(orderService *service.OrderService) {
	h.orderService = orderService
}

// getSubHandler returns the subscription sub-handler, initializing it once.
// The sync.Once guard supports test-constructed handlers that omit subHandler.
func (h *Handler) getSubHandler() *SubscriptionHandler {
	h.subHandlerOnce.Do(func() { h.subHandler = NewSubscriptionHandler(h) })
	return h.subHandler
}

// isAdmin returns true if chatID matches configured admin ID
func (h *Handler) isAdmin(chatID int64) bool {
	return h.cfg.TelegramAdminID > 0 && chatID == h.cfg.TelegramAdminID
}

// withTimeout returns a context with the standard handler timeout.
func (h *Handler) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, HandlerTimeout)
}

// Command delegates (implemented in command.go after handler split)
func (h *Handler) HandleStart(ctx context.Context, update tgbotapi.Update) error {
	if h.cmdHandler == nil {
		return errors.New("handler: cmdHandler is nil, use NewHandler to construct Handler")
	}
	return h.cmdHandler.HandleStart(ctx, update)
}

func (h *Handler) HandleHelp(ctx context.Context, update tgbotapi.Update) error {
	if h.cmdHandler == nil {
		return errors.New("handler: cmdHandler is nil, use NewHandler to construct Handler")
	}
	return h.cmdHandler.HandleHelp(ctx, update)
}

func (h *Handler) HandleInvite(ctx context.Context, update tgbotapi.Update) error {
	if h.cmdHandler == nil {
		return errors.New("handler: cmdHandler is nil, use NewHandler to construct Handler")
	}
	return h.cmdHandler.HandleInvite(ctx, update)
}

// Private delegations kept for test compatibility after handler split
func (h *Handler) handleBindTrial(ctx context.Context, chatID int64, username, subscriptionID string) error {
	if h.cmdHandler == nil {
		return errors.New("handler: cmdHandler is nil, use NewHandler to construct Handler")
	}
	return h.cmdHandler.handleBindTrial(ctx, chatID, username, subscriptionID)
}

func (h *Handler) handleShareStart(ctx context.Context, chatID int64, username, inviteCode string) error {
	if h.cmdHandler == nil {
		return errors.New("handler: cmdHandler is nil, use NewHandler to construct Handler")
	}
	return h.cmdHandler.handleShareStart(ctx, chatID, username, inviteCode)
}

func (h *Handler) sendInviteLink(ctx context.Context, chatID int64, messageID int) error {
	if h.referral == nil {
		return errors.New("handler: referral is nil, use NewHandler to construct Handler")
	}
	return h.referral.sendInviteLink(ctx, chatID, messageID)
}

// HandleCallback delegates callback handling to the callback handler.
func (h *Handler) HandleCallback(ctx context.Context, update tgbotapi.Update) error {
	if h.cbHandler == nil {
		return errors.New("handler: cbHandler is nil, use NewHandler to construct Handler")
	}
	return h.cbHandler.HandleCallback(ctx, update)
}

// Callback private delegates
func (h *Handler) handleShareInvite(ctx context.Context, chatID int64, username string, messageID int) error {
	if h.cbHandler == nil {
		return errors.New("handler: cbHandler is nil, use NewHandler to construct Handler")
	}
	return h.cbHandler.handleShareInvite(ctx, chatID, username, messageID)
}

// Subscription delegates

func (h *Handler) handleCreateSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	return h.getSubHandler().handleCreateSubscription(ctx, chatID, username, messageID)
}

func (h *Handler) handleMySubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	return h.getSubHandler().handleMySubscription(ctx, chatID, username, messageID)
}

func (h *Handler) handleQRCode(ctx context.Context, chatID int64, username string, messageID int) error {
	return h.getSubHandler().handleQRCode(ctx, chatID, username, messageID)
}

func (h *Handler) handleUpgradePremium(ctx context.Context, chatID int64, username string, messageID int) error {
	return h.getSubHandler().handleUpgradePremium(ctx, chatID, username, messageID)
}

func (h *Handler) handleConfirmUpgradePremium(ctx context.Context, chatID int64, username string, messageID int) error {
	return h.getSubHandler().handleConfirmUpgradePremium(ctx, chatID, username, messageID)
}

func (h *Handler) handleBackToSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	return h.getSubHandler().handleBackToSubscription(ctx, chatID, username, messageID)
}

func (h *Handler) sendQRCode(ctx context.Context, chatID int64, messageID int, url string, caption string) error {
	return h.getSubHandler().sendQRCode(ctx, chatID, messageID, url, caption)
}

func (h *Handler) handleBackToInvite(ctx context.Context, chatID int64, username string, messageID int) error {
	return h.getSubHandler().handleBackToInvite(ctx, chatID, username, messageID)
}

func (h *Handler) getSubscriptionWithCache(ctx context.Context, chatID int64) (*database.Subscription, error) {
	return h.getSubHandler().getSubscriptionWithCache(ctx, chatID)
}

// invalidateCache clears the subscription cache for the given chatID.
// It uses centralized SubscriptionService if available, otherwise falls back to direct cache access.
func (h *Handler) invalidateCache(ctx context.Context, chatID int64) {
	if h.subscriptionService != nil {
		h.subscriptionService.InvalidateSubscription(ctx, chatID)
		return
	}
	h.cache.Invalidate(chatID)
}

// Subscription direct delegates (used by tests and internal flows)

// createSubscription creates a new subscription for a user.
func (h *Handler) createSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	return h.getSubHandler().createSubscription(ctx, chatID, username, messageID)
}

// handleCreateError handles errors during subscription creation.
func (h *Handler) handleCreateError(ctx context.Context, chatID int64, messageID int, username string, err error) {
	h.getSubHandler().handleCreateError(ctx, chatID, messageID, username, err)
}

// Referral delegates

// generateInviteLink generates an invite link for a user.
func (h *Handler) generateInviteLink(ctx context.Context, chatID int64, lt linkType) (string, error) {
	h.referralOnce.Do(func() {
		h.referral = NewReferralHandler(h.db, h.cfg, h.bot, h.botConfig, h.sender, h.keyboards)
	})
	if h.referral == nil {
		return "", errors.New("handler: referral is nil, use NewHandler to construct Handler")
	}
	return h.referral.generateInviteLink(ctx, chatID, lt)
}

// sendInviteLink moved to command.go after handler split

// Utility methods
func (h *Handler) getUsername(user *tgbotapi.User) string {
	if user == nil {
		return "unknown"
	}

	return user.UserName
}

// userFields returns structured log fields identifying a Telegram user.
// Always includes chat_id (the stable user identifier). Adds first_name and
// last_name as a human-readable fallback when the @username is empty, so logs
// stay identifiable even for users without a Telegram username.
func userFields(from *tgbotapi.User, chatID int64) []zap.Field {
	fields := []zap.Field{
		zap.Int64("chat_id", chatID),
		zap.String("username", ""),
	}
	if from != nil {
		fields[1] = zap.String("username", from.UserName)
		if name := strings.TrimSpace(from.FirstName + " " + from.LastName); name != "" {
			fields = append(fields, zap.String("name", name))
		}
	}
	return fields
}

// formatUserLink returns a Markdown-formatted clickable user link for Telegram.
// For alphabetic usernames, links to https://t.me/username.
// For purely numeric usernames (e.g. "11"), uses tg://user?id=ID deep link,
// because Telegram does not resolve t.me/123 as a profile.
// For empty/unsupported usernames, falls back to tg://user?id=TelegramID deep link
// with "unknown" display text.
func formatUserLink(username string, telegramID int64) string {
	if utils.IsNumericUsername(username) && telegramID != 0 {
		return fmt.Sprintf("[%s](tg://user?id=%d)", username, telegramID)
	}
	if utils.IsRealUsername(username) {
		return fmt.Sprintf("[@%s](https://t.me/%s)", username, username)
	}
	if telegramID != 0 {
		return fmt.Sprintf("[unknown](tg://user?id=%d)", telegramID)
	}
	return "[unknown](#)"
}

// formatUserDisplay returns a display string suitable for showing a user reference.
// For real usernames returns "@username", otherwise returns the raw identifier.
func formatUserDisplay(username string) string {
	if !utils.IsRealUsername(username) {
		if username == "" {
			return "unknown"
		}
		return username
	}
	return "@" + username
}

// displayUsername formats a username for display in Telegram messages.
// Returns ", @username" if non-empty, or empty string for missing usernames.
func displayUsername(username string) string {
	if username == "" {
		return ""
	}
	return ", @" + username
}

// getMainMenuContent builds the start-screen text and keyboard for a user.
func (h *Handler) getMainMenuContent(ctx context.Context, username string, hasSubscription bool, chatID int64, sub *database.Subscription) (string, tgbotapi.InlineKeyboardMarkup) {
	// Ensure keyboards is initialized (for manually constructed handlers in tests)
	h.keyboardsOnce.Do(func() {
		if h.keyboards == nil {
			h.keyboards = NewKeyboardBuilder("", "", "", "", "")
		}
	})

	var text string
	var keyboard tgbotapi.InlineKeyboardMarkup
	freeUpgradeLabel := ""
	if hasSubscription {
		if label, ok := h.getFreeUpgradeLabel(ctx, sub); ok {
			freeUpgradeLabel = label
		}
	}

	if hasSubscription {
		text = msg(MsgStartGreeting, username)
		keyboard = h.getMainMenuKeyboard(true, freeUpgradeLabel)
	} else {
		text = msg(MsgStartGreetingNoSub, username)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "create_subscription"),
			),
		)
	}

	// Add admin buttons if the user is an admin
	h.addAdminButtons(&keyboard, chatID)

	return text, keyboard
}

// getHelpText returns the detailed setup help text.
func (h *Handler) getHelpText(trafficLimit int, subscriptionURL string) string {
	// Use the detailed help from KeyboardBuilder which includes setup instructions.
	return h.keyboards.HelpText(trafficLimit, subscriptionURL)
}

// getDonateText returns the donate info text.
func (h *Handler) getDonateText() string {
	return h.keyboards.DonateText()
}

// getBackKeyboard returns the back-navigation keyboard.
func (h *Handler) getBackKeyboard() tgbotapi.InlineKeyboardMarkup {
	return h.keyboards.Back()
}

// getQRKeyboard returns the QR-code keyboard.
func (h *Handler) getQRKeyboard() tgbotapi.InlineKeyboardMarkup {
	return h.keyboards.QR()
}

// getMainMenuKeyboard builds the main menu keyboard.
func (h *Handler) getMainMenuKeyboard(hasSubscription bool, freeUpgradeLabel ...string) tgbotapi.InlineKeyboardMarkup {
	label := ""
	if len(freeUpgradeLabel) > 0 {
		label = freeUpgradeLabel[0]
	}
	return h.keyboards.MainMenu(hasSubscription, label)
}

// getFreeUpgradeLabel returns a free-upgrade promo label when the user is on
// the free plan and a zero-cost product is configured.
func (h *Handler) getFreeUpgradeLabel(ctx context.Context, sub *database.Subscription) (string, bool) {
	if h.db == nil || sub == nil || sub.Status != "active" || h.cfg == nil || h.cfg.MainMenuBtnProductID == 0 {
		return "", false
	}
	plan, err := h.db.GetPlanByID(ctx, sub.PlanID)
	if err != nil || plan == nil || plan.Name != database.FreePlanName {
		return "", false
	}
	product, err := h.db.GetProductByID(ctx, h.cfg.MainMenuBtnProductID)
	if err != nil || product == nil || !product.IsActive || product.PriceCents != 0 {
		return "", false
	}
	return fmt.Sprintf("🎁 %s бесплатно", product.Name), true
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

func (h *Handler) handleRateLimitExceeded(ctx context.Context, chatID int64, messageID int) {
	msgText := "❌ Слишком много запросов. Пожалуйста, подождите минуту."
	if messageID > 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, msgText)
		h.safeSend(edit)
	} else {
		h.sender.SendMessage(ctx, chatID, msgText)
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
	h.bgWg.Add(1)
	go func() {
		defer h.bgWg.Done()
		h.referralCache.StartSync(ctx)
	}()
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
	h.bgWg.Add(1)
	go func() {
		defer h.bgWg.Done()
		h.cache.StartCleanup(ctx, interval)
	}()
}

func (h *Handler) StartRateLimiterCleanup(ctx context.Context, interval, maxIdle time.Duration) {
	h.bgWg.Add(1)
	go func() {
		defer h.bgWg.Done()
		h.rateLimiter.StartCleanup(ctx, interval, maxIdle)
	}()
}

func (h *Handler) WaitForBackgroundGoroutines() {
	h.bgWg.Wait()
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
	h.adminRateLimitMu.Lock()
	defer h.adminRateLimitMu.Unlock()

	if h.adminRateLimiters == nil {
		return
	}
	if bucket, ok := h.adminRateLimiters[chatID]; ok {
		bucket.Reset()
	}
}

// Main update router
func (h *Handler) HandleUpdate(ctx context.Context, update tgbotapi.Update) {
	start := time.Now()

	// Rate limiting: extract chat ID and check for non-admin users
	var chatID int64
	var command string
	if update.Message != nil {
		chatID = update.Message.Chat.ID
		if update.Message.IsCommand() {
			command = normalizeCommand(update.Message.Command())
		} else {
			command = "text"
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
		h.handleRateLimitExceeded(ctx, chatID, 0)
		metrics.BotUpdatesTotal.WithLabelValues(command, "rate_limited").Inc()
		err = ErrRateLimited
		return
	}
	defer func() {
		metrics.BotUpdateDuration.WithLabelValues(command).Observe(time.Since(start).Seconds())
		if err != nil {
			metrics.BotUpdateErrorsTotal.WithLabelValues(command).Inc()
			metrics.BotUpdatesTotal.WithLabelValues(command, "error").Inc()
		} else {
			metrics.BotUpdatesTotal.WithLabelValues(command, "success").Inc()
		}
	}()

	if update.Message != nil {
		if update.Message.IsCommand() {
			if h.isAdmin(chatID) && h.broadcastSessionActive(chatID) && update.Message.Command() != "broadcast" {
				h.clearBroadcastSession(chatID)
			}
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
			case "lastreg":
				err = h.handleAdminLastReg(ctx, update.Message.Chat.ID, update.Message.From.UserName, 0)
			default:
				h.SendMessage(ctx, update.Message.Chat.ID,
					"Неизвестная команда. Используйте /start или /help")
			}
		} else {
			// Non-command message: if admin is composing a broadcast draft,
			// treat it as the broadcast message; otherwise send help hint.
			if h.isAdmin(chatID) && h.broadcastSessionActive(chatID) {
				err = h.HandleBroadcastDraft(ctx, update)
			} else {
				err = h.HandleHelp(ctx, update)
			}
		}
	} else if update.CallbackQuery != nil {
		err = h.HandleCallback(ctx, update)
	}

}
