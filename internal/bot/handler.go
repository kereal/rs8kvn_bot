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
	bot            interfaces.BotAPI
	cfg            *config.Config
	db             interfaces.DatabaseService
	xui            interfaces.XUIClient
	rateLimiter    *ratelimiter.RateLimiter
	cache          *SubscriptionCache
	subCreationMu  sync.Mutex
	inProgress     map[int64]struct{}
	referrals      map[int64]int64
	referralsMu    sync.RWMutex
	pendingInvites map[int64]pendingInvite // chatID -> invite_code
	pendingMu      sync.RWMutex
}

func NewHandler(bot interfaces.BotAPI, cfg *config.Config, db interfaces.DatabaseService, xuiClient interfaces.XUIClient) *Handler {
	return &Handler{
		bot:            bot,
		cfg:            cfg,
		db:             db,
		xui:            xuiClient,
		rateLimiter:    ratelimiter.NewRateLimiter(config.RateLimiterMaxTokens, config.RateLimiterRefillRate),
		cache:          NewSubscriptionCache(CacheMaxSize, CacheTTL),
		subCreationMu:  sync.Mutex{},
		inProgress:     make(map[int64]struct{}),
		referrals:      make(map[int64]int64),
		referralsMu:    sync.RWMutex{},
		pendingInvites: make(map[int64]pendingInvite),
		pendingMu:      sync.RWMutex{},
	}
}

func (h *Handler) isAdmin(chatID int64) bool {
	return h.cfg.TelegramAdminID > 0 && chatID == h.cfg.TelegramAdminID
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

Есть сбор в Т-Банке
[REDACTED_DONATE_URL](REDACTED_DONATE_URL)

Если нужен другой способ — [напишите мне](https://t.me/kereal)`
}

// getHelpText returns the help/instruction message text with subscription URL.
func (h *Handler) getHelpText(trafficLimitGB int, subscriptionURL string) string {
	return fmt.Sprintf(
		"🚀 *Ваша подписка готова!*\n\nТрафик: %dГб на месяц.\n\n📲 *1. Установите приложение Happ*\n· [Скачать для iOS](https://apps.apple.com/ru/app/happ-proxy-utility-plus/id6746188973)\n· [Скачать для Android](https://play.google.com/store/apps/details?id=com.happproxy)\n\n📥 *2. Импортируйте подписку*\n\nНажмите, чтобы скопировать: `%s`\n\nВ приложении Happ нажмите *«+»* в правом верхнем углу и выберите *«Вставить из буфера»*.\n\n▶️ *3. Запустите VPN*\nДождитесь загрузки и нажмите на большую круглую кнопку в центре экрана.\n\n🛡️ *Важно знать*\nВ приложении Happ настроена автоматическая маршрутизация. Зарубежные сайты работают через VPN, а российские сервисы — напрямую. VPN можно не выключать.\n⚠️ _Если вы используете другое приложение или свою конфигурацию — не заходите через этот VPN на российские ресурсы, иначе сервер заблокируют._\n\n🤝 *Правила использования*\n· Не передавайте свою подписку другим. Делитесь ссылкой на этого бота `@rs8kvn_bot`.\n· Не публикуйте ссылку на бота в интернете, передавайте только из рук в руки (приветствуется).\n· Пользуйтесь ответственно, не занимайтесь незаконной деятельностью.\n\n☕ *Поддержка проекта*\nЭтот VPN бесплатный и существует благодаря вашим пожертвованиям и усилиям Кирилла.\n[Поддержите проект](https://t.me/rs8kvn_bot?start=donate) — важна каждая сотня.\n\nПомощь, вопросы: [@kereal](https://t.me/kereal)",
		trafficLimitGB,
		subscriptionURL,
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

	telegramLink := fmt.Sprintf("t.me/rs8vpn_bot?start=share_%s", invite.Code)
	webLink := fmt.Sprintf("%s/i/%s", h.cfg.SiteURL, invite.Code)
	text := fmt.Sprintf(`🔗 *Ваша пригласительная ссылка*

📱 *Для пользователей Telegram:*
[@rs8vpn_bot](%s)
_нажмите и держите → копировать_

🌐 *Для пользователей без Telegram:*
[%s](%s)
_нажмите и держите → копировать_

📤 *Отправьте ссылку друзьям!*

💎 За каждого приглашенного активного пользователя вы получите бонус.`, telegramLink, webLink, webLink)
	backKeyboard := h.getBackKeyboard()

	if messageID > 0 {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.ParseMode = "Markdown"
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &backKeyboard
		h.safeSend(editMsg)
	} else {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ParseMode = "Markdown"
		msg.ReplyMarkup = &backKeyboard
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

func (h *Handler) StoreConversation(ctx context.Context, chatID int64, userMessage, botResponse string) {
}

func (h *Handler) GetUserContext(ctx context.Context, chatID int64, query string) string {
	return ""
}
