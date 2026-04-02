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
	"rs8kvn_bot/internal/utils"

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
	inProgressSyncMap   sync.Map // Atomic tracking of subscription creation in progress
	referrals           map[int64]int64
	referralsMu         sync.RWMutex
	pendingInvites      map[int64]pendingInvite // chatID -> invite_code
	pendingMu           sync.RWMutex
	botConfig           *BotConfig
	adminSendMu         sync.Map // chatID -> lastSendTime
	subscriptionService *service.SubscriptionService
}

func NewHandler(bot interfaces.BotAPI, cfg *config.Config, db interfaces.DatabaseService, xuiClient interfaces.XUIClient, botConfig *BotConfig) *Handler {
	return &Handler{
		bot:                 bot,
		cfg:                 cfg,
		db:                  db,
		xui:                 xuiClient,
		rateLimiter:         ratelimiter.NewPerUserRateLimiter(float64(config.RateLimiterMaxTokens), float64(config.RateLimiterRefillRate)),
		cache:               NewSubscriptionCache(CacheMaxSize, CacheTTL),
		inProgressSyncMap:   sync.Map{},
		referrals:           make(map[int64]int64),
		referralsMu:         sync.RWMutex{},
		pendingInvites:      make(map[int64]pendingInvite),
		pendingMu:           sync.RWMutex{},
		botConfig:           botConfig,
		adminSendMu:         sync.Map{},
		subscriptionService: service.NewSubscriptionService(db, xuiClient, cfg),
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
				h.HandleBroadcast(context.WithoutCancel(ctx), update)
			case "send":
				h.HandleSend(ctx, update)
			case "refstats":
				h.HandleRefstats(ctx, update)
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

// StartCacheCleanup starts a background goroutine that periodically removes expired cache entries.
func (h *Handler) StartCacheCleanup(ctx context.Context, interval time.Duration) {
	go h.cache.StartCleanup(ctx, interval)
}

// StartRateLimiterCleanup starts a background goroutine that removes stale user buckets.
func (h *Handler) StartRateLimiterCleanup(ctx context.Context, interval, maxIdle time.Duration) {
	go h.rateLimiter.StartCleanup(ctx, interval, maxIdle)
}

func (h *Handler) checkAdminSendRateLimit(chatID int64) bool {
	now := time.Now()

	lastSend, loaded := h.adminSendMu.Load(chatID)
	if loaded {
		lastTime := lastSend.(time.Time)
		if now.Sub(lastTime) < config.AdminSendMinInterval {
			return false
		}
	}

	h.adminSendMu.Store(chatID, now)
	return true
}

func (h *Handler) ClearAdminSendRateLimit(chatID int64) {
	h.adminSendMu.Delete(chatID)
}

// LoadReferralCache loads referral counts from database into memory cache.
func (h *Handler) LoadReferralCache(ctx context.Context) error {
	counts, err := h.db.GetAllReferralCounts(ctx)
	if err != nil {
		return err
	}

	h.referralsMu.Lock()
	defer h.referralsMu.Unlock()

	h.referrals = counts
	return nil
}

// GetReferralCount returns the cached referral count for a user.
func (h *Handler) GetReferralCount(chatID int64) int64 {
	h.referralsMu.RLock()
	defer h.referralsMu.RUnlock()

	return h.referrals[chatID]
}

// IncrementReferralCount increments the referral count for a user in cache.
func (h *Handler) IncrementReferralCount(chatID int64) {
	h.referralsMu.Lock()
	defer h.referralsMu.Unlock()

	h.referrals[chatID]++
}

// DecrementReferralCount decrements the referral count for a user in cache.
func (h *Handler) DecrementReferralCount(chatID int64) {
	h.referralsMu.Lock()
	defer h.referralsMu.Unlock()

	if h.referrals[chatID] > 0 {
		h.referrals[chatID]--
	}
}

// SyncReferralCache reloads the referral cache from database.
func (h *Handler) SyncReferralCache(ctx context.Context) error {
	return h.LoadReferralCache(ctx)
}

// StartReferralCacheSync starts periodic synchronization of referral cache.
func (h *Handler) StartReferralCacheSync(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		// Load initial cache
		if err := h.LoadReferralCache(ctx); err != nil {
			logger.Error("Failed to load referral cache", zap.Error(err))
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := h.SyncReferralCache(ctx); err != nil {
					logger.Error("Failed to sync referral cache", zap.Error(err))
				}
			}
		}
	}()
}

// getMainMenuKeyboard returns the inline keyboard with main menu buttons
func (h *Handler) getMainMenuKeyboard(hasSubscription bool) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Подписка", "menu_subscription"),
			tgbotapi.NewInlineKeyboardButtonData("☕ Донат", "menu_donate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❓ Помощь", "menu_help"),
		),
	}

	if hasSubscription {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📤 Поделиться", "share_invite"),
		))
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// getBackKeyboard returns the inline keyboard with back button
func (h *Handler) getBackKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
}

// getQRKeyboard returns the inline keyboard with QR code and back buttons
func (h *Handler) getQRKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 QR-код", "qr_code"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
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
	return `☕ *Поддержка проекта*

Любая помощь важна для стабильной работы сервиса.

😊 Сделайте свой вклад — переведите любую сумму.
Буду очень благодарен! ❤️

💳 *Карта Т-Банка:*
` + "`" + config.DonateCardNumber + "`" + `

🔗 [Сбор в Т-Банке](` + config.DonateURL + `)
💬 [Связаться](https://t.me/` + h.cfg.ContactUsername + `)`
}

// getHelpText returns the help/instruction message text with subscription URL.
func (h *Handler) getHelpText(trafficLimitGB int, subscriptionURL string) string {
	return fmt.Sprintf(
		"🚀 *Ваша подписка готова!*\n\nТрафик: %dГб на месяц.\n\n📲 *1. Установите приложение Happ*\n· [Скачать для iOS](https://apps.apple.com/ru/app/happ-proxy-utility-plus/id6746188973)\n· [Скачать для Android](https://play.google.com/store/apps/details?id=com.happproxy)\n\n📥 *2. Импортируйте подписку*\n\nНажмите, чтобы скопировать: `%s`\n\nВ приложении Happ нажмите *«+»* в правом верхнем углу и выберите *«Вставить из буфера»*.\n\n▶️ *3. Запустите VPN*\nДождитесь загрузки и нажмите на большую круглую кнопку в центре экрана.\n\n🛡️ *Важно знать*\nВ приложении Happ настроена автоматическая маршрутизация. Зарубежные сайты работают через VPN, а российские сервисы — напрямую. VPN можно не выключать.\n⚠️ _Если вы используете другое приложение или свою конфигурацию — не заходите через этот VPN на российские ресурсы, иначе сервер заблокируют._\n\n🤝 *Правила использования*\n· Не передавайте свою подписку другим. Делитесь ссылкой на этого бота `@%s`.\n· Не публикуйте ссылку на бота в интернете, передавайте только из рук в руки (приветствуется).\n· Пользуйтесь ответственно, не занимайтесь незаконной деятельностью.\n\n☕ *Поддержка проекта*\nЭтот VPN бесплатный и существует благодаря вашим пожертвованиям и усилиям Кирилла.\n[Поддержите проект](https://t.me/%s?start=donate) — важна каждая сотня.\n\nПомощь, вопросы: [@%s](https://t.me/%s)",
		trafficLimitGB,
		subscriptionURL,
		h.botConfig.Username,
		h.botConfig.Username,
		h.cfg.ContactUsername,
		h.cfg.ContactUsername,
	)
}

func (h *Handler) sendInviteLink(ctx context.Context, chatID int64, messageID int) {
	invite, err := h.db.GetOrCreateInvite(ctx, chatID, utils.GenerateInviteCode())
	if err != nil {
		logger.Error("Failed to get or create invite",
			zap.Error(err),
			zap.Int64("chat_id", chatID))
		h.SendMessage(ctx, chatID, "❌ Не удалось создать пригласительную ссылку. Попробуйте позже.")
		return
	}

	telegramLink := fmt.Sprintf("https://t.me/%s?start=share_%s", h.botConfig.Username, invite.Code)
	webLink := fmt.Sprintf("%s/i/%s", h.cfg.SiteURL, invite.Code)
	text := fmt.Sprintf(`🔗 *Ваша пригласительная ссылка*

📱 *Для пользователей Telegram:*
[@%s](%s)
_нажмите и держите → копировать_

🌐 *Тем, кто не может войти в Tg:*
[%s](%s)
_нажмите и держите → копировать_

📤 *Отправьте ссылку друзьям!*

💎 За каждого приглашенного активного пользователя вы получите бонус.`, h.botConfig.Username, telegramLink, webLink, webLink)

	// Keyboard with QR buttons
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 QR Telegram", "qr_telegram"),
			tgbotapi.NewInlineKeyboardButtonData("🌐 QR Web", "qr_web"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)

	if messageID > 0 {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.ParseMode = "Markdown"
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &keyboard
		h.safeSend(editMsg)
	} else {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ParseMode = "Markdown"
		msg.DisableWebPagePreview = true
		msg.ReplyMarkup = &keyboard
		h.send(ctx, msg)
	}
}

func (h *Handler) addAdminButtons(keyboard *tgbotapi.InlineKeyboardMarkup, chatID int64) {
	if h.isAdmin(chatID) {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
				tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
			),
		)
	}
}
