package bot

import (
	"fmt"
	"strings"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/ratelimiter"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Cache constants
const (
	CacheMaxSize = 1000
	CacheTTL     = 5 * time.Minute
)

type Handler struct {
	bot         *tgbotapi.BotAPI
	cfg         *config.Config
	db          interfaces.DatabaseService
	xui         interfaces.XUIClient
	rateLimiter *ratelimiter.RateLimiter
	cache       *SubscriptionCache
}

func NewHandler(bot *tgbotapi.BotAPI, cfg *config.Config, db interfaces.DatabaseService, xuiClient interfaces.XUIClient) *Handler {
	return &Handler{
		bot:         bot,
		cfg:         cfg,
		db:          db,
		xui:         xuiClient,
		rateLimiter: ratelimiter.NewRateLimiter(config.RateLimiterMaxTokens, config.RateLimiterRefillRate),
		cache:       NewSubscriptionCache(CacheMaxSize, CacheTTL),
	}
}

func (h *Handler) isAdmin(chatID int64) bool {
	return chatID == h.cfg.TelegramAdminID
}

// getMainMenuKeyboard returns the inline keyboard with main menu buttons
func (h *Handler) getMainMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Подписка", "menu_subscription"),
			tgbotapi.NewInlineKeyboardButtonData("☕ Донат", "menu_donate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❓ Помощь", "menu_help"),
		),
	)
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
		return sanitizeForMarkdown(user.UserName)
	}

	if user.FirstName != "" {
		return sanitizeForMarkdown(user.FirstName)
	}

	return fmt.Sprintf("user_%d", user.ID)
}

// sanitizeForMarkdown escapes special characters that have meaning in Telegram Markdown.
// This prevents users from injecting formatting or links via their username/name.
func sanitizeForMarkdown(s string) string {
	// Telegram Markdown special chars: * _ ` [ ] ( ) ~ |
	// Replace with safe alternatives or escape them
	r := strings.NewReplacer(
		"*", "∗", // asterisk
		"_", "＿", // underscore (fullwidth)
		"`", "ˋ", // backtick
		"[", "（", // bracket (using fullwidth parens as visual substitute)
		"]", "）",
		"(", "⁽", // paren
		")", "⁾",
		"~", "～", // tilde (for strikethrough)
		"|", "｜", // pipe (for tables)
		"\n", " ", // newline
		"\t", " ", // tab
	)
	return r.Replace(s)
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
		keyboard = h.getMainMenuKeyboard()
		if h.isAdmin(chatID) {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}
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
		if h.isAdmin(chatID) {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}
	}

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
