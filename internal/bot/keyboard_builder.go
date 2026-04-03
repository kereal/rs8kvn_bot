package bot

import (
	"fmt"

	"rs8kvn_bot/internal/config"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// KeyboardBuilder creates Telegram inline keyboards.
type KeyboardBuilder struct {
	botUsername string
	contactUser string
	donateCard  string
	donateURL   string
	siteURL     string
}

// NewKeyboardBuilder creates a new KeyboardBuilder.
func NewKeyboardBuilder(botUsername, contactUser, donateCard, donateURL, siteURL string) *KeyboardBuilder {
	return &KeyboardBuilder{
		botUsername: botUsername,
		contactUser: contactUser,
		donateCard:  donateCard,
		donateURL:   donateURL,
		siteURL:     siteURL,
	}
}

// MainMenu returns the inline keyboard for the main menu.
func (kb *KeyboardBuilder) MainMenu(hasSubscription bool) tgbotapi.InlineKeyboardMarkup {
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

// Back returns the inline keyboard with a back button.
func (kb *KeyboardBuilder) Back() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
}

// QR returns the inline keyboard with QR code and back buttons.
func (kb *KeyboardBuilder) QR() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 QR-код", "qr_code"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
}

// Invite returns the inline keyboard for invite sharing.
func (kb *KeyboardBuilder) Invite() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 QR Telegram", "qr_telegram"),
			tgbotapi.NewInlineKeyboardButtonData("🌐 QR Web", "qr_web"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
}

// CreateSubscription returns the keyboard for users without a subscription.
func (kb *KeyboardBuilder) CreateSubscription() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "create_subscription"),
		),
	)
}

// WithAdminButtons adds admin buttons to the keyboard if the user is an admin.
func (kb *KeyboardBuilder) WithAdminButtons(keyboard *tgbotapi.InlineKeyboardMarkup, isAdmin bool) {
	if isAdmin {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
				tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
			),
		)
	}
}

// DonateText returns the donation message text.
func (kb *KeyboardBuilder) DonateText() string {
	return `☕ *Поддержка проекта*

Любой помощи важна для стабильной работы сервиса.

😊 Сделайте свой вклад — переведите любую сумму.
Буду очень благодарен! ❤️

💳 *Карта Т-Банка:*
` + "`" + kb.donateCard + "`" + `

🔗 [Сбор в Т-Банке](` + kb.donateURL + `)
💬 [Связаться](https://t.me/` + kb.contactUser + `)`
}

// HelpText returns the help/instruction message text.
func (kb *KeyboardBuilder) HelpText(trafficLimitGB int, subscriptionURL string) string {
	return fmt.Sprintf(
		"🚀 *Ваша подписка готова!*\n\nТрафик: %dГб на месяц.\n\n📲 *1. Установите приложение Happ*\n· [Скачать для iOS](https://apps.apple.com/ru/app/happ-proxy-utility-plus/id6746188973)\n· [Скачать для Android](https://play.google.com/store/apps/details?id=com.happproxy)\n\n📥 *2. Импортируйте подписку*\n\nНажмите, чтобы скопировать: `%s`\n\nВ приложении Happ нажмите *«+»* в правом верхнем углу и выберите *«Вставить из буфера»*.\n\n▶️ *3. Запустите VPN*\nДождитесь загрузки и нажмите на большую круглую кнопку в центре экрана.\n\n🛡️ *Важно знать*\nВ приложении Happ настроена автоматическая маршрутизация. Зарубежные сайты работают через VPN, а российские сервисы — напрямую. VPN можно не выключать.\n⚠️ _Если вы используете другое приложение или свою конфигурацию — не заходите через этот VPN на российские ресурсы, иначе сервер заблокируют._\n\n🤝 *Правила использования*\n· Не передавайте свою подписку другим. Делитесь ссылкой на этого бота `@%s`.\n· Не публикуйте ссылку на бота в интернете, передавайте только из рук в руки (приветствуется).\n· Пользуйтесь ответственно, не занимайтесь незаконной деятельностью.\n\n☕ *Поддержка проекта*\nЭтот VPN бесплатный и существует благодаря вашим пожертвованиям и усилиям Кирилла.\n[Поддержите проект](https://t.me/%s?start=donate) — важна каждая сотня.\n\nПомощь, вопросы: [@%s](https://t.me/%s)",
		trafficLimitGB,
		subscriptionURL,
		kb.botUsername,
		kb.botUsername,
		kb.contactUser,
		kb.contactUser,
	)
}

// InviteLinkText returns the invite link sharing text.
func (kb *KeyboardBuilder) InviteLinkText(telegramLink, webLink string) string {
	return fmt.Sprintf(`🔗 *Ваша пригласительная ссылка*

📱 *Для пользователей Telegram:*
[@%s](%s)
_нажмите и держите → копировать_

🌐 *Тем, кто не может войти в Tg:*
[%s](%s)
_нажмите и держите → копировать_

📤 *Отправьте ссылку друзьям!*

💎 За каждого приглашенного активного пользователя вы получите бонус.`, kb.botUsername, telegramLink, webLink, webLink)
}

// Config holds configuration for creating bot components.
type Config struct {
	BotUsername     string
	ContactUsername string
	SiteURL         string
}

// FromConfig creates a Config from the application config.
func FromConfig(cfg *config.Config, botUsername string) *Config {
	return &Config{
		BotUsername:     botUsername,
		ContactUsername: cfg.ContactUsername,
		SiteURL:         cfg.SiteURL,
	}
}
