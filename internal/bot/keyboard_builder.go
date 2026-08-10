package bot

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/database"
)

// KeyboardBuilder creates Telegram inline keyboards.
type KeyboardBuilder struct {
	botUsername   string
	contactUser   string
	donateCard    string
	donateURL     string
	siteURL       string
	donateEnabled bool
}

// NewKeyboardBuilder creates a new KeyboardBuilder.
// donateEnabled toggles the visibility of the "☕ Донат" button in MainMenu.
func NewKeyboardBuilder(botUsername, contactUser, donateCard, donateURL, siteURL string, donateEnabled bool) *KeyboardBuilder {
	return &KeyboardBuilder{botUsername: botUsername, contactUser: contactUser, donateCard: donateCard, donateURL: donateURL, siteURL: siteURL, donateEnabled: donateEnabled}
}

// MainMenu returns the inline keyboard for the main menu.
// paymentEnabled controls whether the "💎 Купить Premium" button is shown;
// it is passed per-call because the flag can be toggled at runtime via config.
func (kb *KeyboardBuilder) MainMenu(hasSubscription bool, paymentEnabled bool) tgbotapi.InlineKeyboardMarkup {
	var firstRow []tgbotapi.InlineKeyboardButton
	firstRow = append(firstRow, tgbotapi.NewInlineKeyboardButtonData("📋 Подписка", "menu_subscription"))
	if kb.donateEnabled {
		firstRow = append(firstRow, tgbotapi.NewInlineKeyboardButtonData("☕ Донат", "menu_donate"))
	}
	rows := [][]tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardRow(firstRow...)}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❓ Помощь", "menu_help"), tgbotapi.NewInlineKeyboardButtonData("📑 Документы", "menu_documents")))
	if paymentEnabled && hasSubscription {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("💎 Купить Premium", "buy_premium_list")))
	}
	if hasSubscription {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("📤 Поделиться", "share_invite")))
	}
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// BuyProductList builds the paid product selection keyboard.
func (kb *KeyboardBuilder) BuyProductList(products []database.Product) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(products)+1)
	for _, product := range products {
		if !product.IsActive || product.PriceCents <= 0 {
			continue
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s — %.2f ₽", product.Name, float64(product.PriceCents)/100), fmt.Sprintf("buy_product_%d", product.ID))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_start")))
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// BuyProductConfirm builds the payment-link keyboard.
func (kb *KeyboardBuilder) BuyProductConfirm(product *database.Product, paymentURL string) tgbotapi.InlineKeyboardMarkup {
	price := ""
	if product != nil {
		price = fmt.Sprintf("%.2f ₽", float64(product.PriceCents)/100)
	}
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonURL("🔗 Оплатить "+price, paymentURL)),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "buy_premium_list")),
	}}
}

// Back returns the inline keyboard with a back button.
func (kb *KeyboardBuilder) Back() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
}

// BackToDocuments returns the inline keyboard with a back button to documents.
func (kb *KeyboardBuilder) BackToDocuments() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_documents"),
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

// Documents returns the inline keyboard for the documents menu.
func (kb *KeyboardBuilder) Documents() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔒 Политика конфиденциальности", "menu_privacy"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📄 Пользовательское соглашение", "menu_terms"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💬 Поддержка", "menu_support"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)
}

// PrivacyText returns the privacy policy text.
func (kb *KeyboardBuilder) PrivacyText() string {
	return PrivacyText()
}

// TermsText returns the terms of service text.
func (kb *KeyboardBuilder) TermsText() string {
	return TermsText()
}

// SupportText returns the support contact text.
func (kb *KeyboardBuilder) SupportText() string {
	return SupportText()
}

// DonateText returns the donation message text.
func (kb *KeyboardBuilder) DonateText() string {
	return `☕ *Поддержка проекта*

Любая помощь важна для стабильной работы сервиса.

😊 Сделайте свой вклад — переведите любую сумму.
Буду очень благодарен! ❤️

💳 *Карта Т-Банка:*
` + "`" + kb.donateCard + "`" + `

🔗 [Сбор в Т-Банке](` + kb.donateURL + `)
💬 [Связаться](https://t.me/` + kb.contactUser + `)`
}

// HelpText returns the help/instruction message text.
func (kb *KeyboardBuilder) HelpText(trafficLimit int, subscriptionURL string) string {
	return fmt.Sprintf(
		"🚀 *Ваша подписка готова!*\n\nТрафик: %dГб на месяц.\n\n📲 *1. Установите приложение Happ*\n· [Скачать для iOS](https://apps.apple.com/ru/app/happ-proxy-utility-plus/id6746188973)\n· [Скачать для Android](https://play.google.com/store/apps/details?id=com.happproxy)\n\n📥 *2. Импортируйте подписку*\n\nНажмите, чтобы скопировать: `%s`\n\nВ приложении Happ нажмите *«+»* в правом верхнем углу и выберите *«Вставить из буфера»*.\n\n▶️ *3. Запустите VPN*\nДождитесь загрузки и нажмите на большую круглую кнопку в центре экрана.\n\n🛡️ *Важно знать*\nВ приложении Happ настроена автоматическая маршрутизация. Зарубежные сайты работают через VPN, а российские сервисы — напрямую. VPN можно не выключать.\n\n🤝 *Правила использования*\n· Не передавайте свою подписку другим. Делитесь ссылкой на этого бота `@%s`.\n· Не публикуйте ссылку на бота в интернете, передавайте только из рук в руки (приветствуется).\n· Пользуйтесь ответственно, не занимайтесь незаконной деятельностью.\n\n☕ *Поддержка проекта*\nЭтот VPN доступен бесплатно и существует благодаря вашим покупкам и усилиям Кирилла.\nПоддержите проект — купите Premium-аккаунт.\n\nПомощь, вопросы: [@%s](https://t.me/%s)",
		trafficLimit,
		subscriptionURL,
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
