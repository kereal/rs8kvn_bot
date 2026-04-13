package bot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/ratelimiter"
	"rs8kvn_bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Cache constants
const (
	CacheMaxSize = 1000
	CacheTTL     = 5 * time.Minute
)

// pendingInvite хранит информацию о pending invite коде с TTL
type pendingInvite struct {
	code      string
	expiresAt time.Time
}

// PendingInviteTTL — время жизни pending invite в кэше
const PendingInviteTTL = 60 * time.Minute

type Handler struct {
	bot                 interfaces.BotAPI
	cfg                 *config.Config
	db                  interfaces.DatabaseService
	xui                 interfaces.XUIClient
	rateLimiter         *ratelimiter.PerUserRateLimiter
	cache               *SubscriptionCache
	inProgressSyncMap   sync.Map                // Atomic tracking of subscription creation in progress
	pendingInvites      map[int64]pendingInvite // chatID -> invite_code
	pendingMu           sync.RWMutex
	botConfig           *BotConfig
	subscriptionService *service.SubscriptionService
	referralCache       *ReferralCache
	sender              *MessageSender
	keyboards           *KeyboardBuilder
	version             string
}

func NewHandler(bot interfaces.BotAPI, cfg *config.Config, db interfaces.DatabaseService, xuiClient interfaces.XUIClient, botConfig *BotConfig, subService *service.SubscriptionService, version string) *Handler {
	rl := ratelimiter.NewPerUserRateLimiter(float64(config.RateLimiterMaxTokens), float64(config.RateLimiterRefillRate))
	kb := NewKeyboardBuilder(botConfig.Username, cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)

	return &Handler{
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
}

func (h *Handler) isAdmin(chatID int64) bool {
	return h.cfg.TelegramAdminID > 0 && chatID == h.cfg.TelegramAdminID
}

// HandleUpdate routes a Telegram update to the appropriate handler method.
func (h *Handler) HandleUpdate(ctx context.Context, update tgbotapi.Update) {
	if update.Message != nil {
		if update.Message.IsCommand() {
			switch update.Message.Command() {
			case "start":
				h.HandleStart(ctx, update)
			case "help":
				h.HandleHelp(ctx, update)
			case "invite":
				h.HandleInvite(ctx, update)
			case "del":
				h.HandleDel(ctx, update)
			case "broadcast":
				h.HandleBroadcast(ctx, update)
			case "send":
				h.HandleSend(ctx, update)
			case "refstats":
				h.HandleRefstats(ctx, update)
			case "plan":
				h.HandlePlan(ctx, update)
			case "v":
				h.HandleVersion(ctx, update)
			default:
				h.SendMessage(ctx, update.Message.Chat.ID,
					"Неизвестная команда. Используйте /start или /help")
			}
		} else {
			username := "unknown"
			if update.Message.From != nil {
				if update.Message.From.UserName != "" {
					username = update.Message.From.UserName
				} else if update.Message.From.FirstName != "" {
					username = update.Message.From.FirstName
				}
			}
			textPreview := update.Message.Text
			if len(textPreview) > 50 {
				textPreview = textPreview[:50] + "..."
			}
			logger.Info("Received non-command message",
				zap.Int64("chat_id", update.Message.Chat.ID),
				zap.String("username", username),
				zap.String("text_preview", textPreview))
			h.SendMessage(ctx, update.Message.Chat.ID,
				"Используйте /start для начала работы с ботом.")
		}
	} else if update.CallbackQuery != nil {
		h.HandleCallback(ctx, update)
	}
}

// StartCacheCleanup starts a background goroutine that periodically removes expired cache entries
// and cleans up stale pending invites.
func (h *Handler) StartCacheCleanup(ctx context.Context, interval time.Duration) {
	go h.cache.StartCleanup(ctx, interval)
	go h.startPendingInvitesCleanup(ctx, interval)
}

// startPendingInvitesCleanup periodically removes expired entries from pendingInvites map
// to prevent unbounded memory growth. Entries expire after PendingInviteTTL (60 minutes).
func (h *Handler) startPendingInvitesCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.cleanupPendingInvites()
		}
	}
}

// cleanupPendingInvites removes expired entries from the pendingInvites map.
func (h *Handler) cleanupPendingInvites() {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()

	now := time.Now()
	for chatID, invite := range h.pendingInvites {
		if now.After(invite.expiresAt) {
			delete(h.pendingInvites, chatID)
		}
	}
}

// StartRateLimiterCleanup starts a background goroutine that removes stale user buckets.
func (h *Handler) StartRateLimiterCleanup(ctx context.Context, interval, maxIdle time.Duration) {
	go h.rateLimiter.StartCleanup(ctx, interval, maxIdle)
}

func (h *Handler) checkAdminSendRateLimit(chatID int64) bool {
	return h.referralCache.CheckAdminSendRateLimit(chatID)
}

func (h *Handler) ClearAdminSendRateLimit(chatID int64) {
	h.referralCache.ClearAdminSendRateLimit(chatID)
}

// LoadReferralCache loads referral counts from database into memory cache.
func (h *Handler) LoadReferralCache(ctx context.Context) error {
	return h.referralCache.Load(ctx)
}

// GetReferralCount returns the cached referral count for a user.
func (h *Handler) GetReferralCount(chatID int64) int64 {
	return h.referralCache.Get(chatID)
}

// IncrementReferralCount increments the referral count for a user in cache.
func (h *Handler) IncrementReferralCount(chatID int64) {
	h.referralCache.Increment(chatID)
}

// DecrementReferralCount decrements the referral count for a user in cache.
func (h *Handler) DecrementReferralCount(chatID int64) {
	h.referralCache.Decrement(chatID)
}

// SyncReferralCache reloads the referral cache from database.
func (h *Handler) SyncReferralCache(ctx context.Context) error {
	return h.referralCache.Sync(ctx)
}

// StartReferralCacheSync starts periodic synchronization of referral cache.
func (h *Handler) StartReferralCacheSync(ctx context.Context) {
	h.referralCache.StartSync(ctx)
}

// getMainMenuKeyboard returns the inline keyboard with main menu buttons
func (h *Handler) getMainMenuKeyboard(hasSubscription bool) tgbotapi.InlineKeyboardMarkup {
	return h.keyboards.MainMenu(hasSubscription)
}

// getBackKeyboard returns the inline keyboard with back button
func (h *Handler) getBackKeyboard() tgbotapi.InlineKeyboardMarkup {
	return h.keyboards.Back()
}

// getQRKeyboard returns the inline keyboard with QR code and back buttons
func (h *Handler) getQRKeyboard() tgbotapi.InlineKeyboardMarkup {
	return h.keyboards.QR()
}

// getUsername extracts a username from a Telegram user.
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

// getMainMenuContent returns the text and keyboard for the main menu.
func (h *Handler) getMainMenuContent(username string, hasSubscription bool, chatID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	var keyboard tgbotapi.InlineKeyboardMarkup

	if hasSubscription {
		text = fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nИспользуйте кнопки ниже для взаимодействия с ботом.",
			username,
		)
		keyboard = h.getMainMenuKeyboard(true)
	} else {
		text = fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nНажмите кнопку ниже, чтобы получить подписку",
			username,
		)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "create_subscription"),
			),
		)
	}

	h.addAdminButtons(&keyboard, chatID)

	return text, keyboard
}

// showLoadingMessage shows a loading message by editing existing or sending new.
// Returns the messageID to use for subsequent edits.
func (h *Handler) showLoadingMessage(chatID int64, messageID int) int {
	if messageID == 0 {
		// No message to edit, send new one
		loadingMsg := tgbotapi.NewMessage(chatID, "⏳ Загрузка...")
		loadingMsg.DisableWebPagePreview = true
		sentMsg, err := h.bot.Send(loadingMsg)
		if err != nil {
			logger.Error("Failed to send loading message", zap.Error(err))
			return 0
		}
		return sentMsg.MessageID
	}

	// Edit existing message to show loading
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "⏳ Загрузка...")
	editMsg.DisableWebPagePreview = true
	if _, err := h.bot.Send(editMsg); err != nil {
		logger.Warn("Failed to edit message for loading, sending new one", zap.Error(err))
		// Try to send new message instead
		loadingMsg := tgbotapi.NewMessage(chatID, "⏳ Загрузка...")
		loadingMsg.DisableWebPagePreview = true
		sentMsg, err := h.bot.Send(loadingMsg)
		if err != nil {
			logger.Error("Failed to send loading message", zap.Error(err))
			return 0
		}
		return sentMsg.MessageID
	}

	return messageID
}

// getDonateText returns the donation message text.
func (h *Handler) getDonateText() string {
	return h.keyboards.DonateText()
}

// getHelpText returns the help/instruction message text with subscription URL.
func (h *Handler) getHelpText(trafficLimitGB int, subscriptionURL string) string {
	return h.keyboards.HelpText(trafficLimitGB, subscriptionURL)
}

func (h *Handler) addAdminButtons(keyboard *tgbotapi.InlineKeyboardMarkup, chatID int64) {
	h.keyboards.WithAdminButtons(keyboard, h.isAdmin(chatID))
}
