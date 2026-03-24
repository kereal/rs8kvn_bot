package bot

import (
	"context"
	"fmt"

	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (h *Handler) HandleStart(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleStart called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID
	username := h.getUsername(update.Message.From)
	isAdmin := chatID == h.cfg.TelegramAdminID

	sub, err := h.db.GetByTelegramID(ctx, chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	if hasSubscription {
		// User has subscription - show inline keyboard with menu buttons
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nИспользуйте кнопки ниже для взаимодействия с ботом.",
			username,
		))

		keyboard := h.getMainMenuKeyboard()
		// Add admin buttons if user is admin
		if isAdmin {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}

		msg.ReplyMarkup = keyboard
		h.send(ctx, msg)
	} else {
		// User has no subscription - show inline button to get subscription
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nНажмите кнопку ниже, чтобы получить подписку:",
			username,
		))

		inlineKeyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "get_subscription"),
			),
		)

		// Add admin buttons if user is admin
		if isAdmin {
			inlineKeyboard.InlineKeyboard = append(inlineKeyboard.InlineKeyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
					tgbotapi.NewInlineKeyboardButtonData("📋 Посл.рег", "admin_lastreg"),
				),
			)
		}

		msg.ReplyMarkup = inlineKeyboard
		h.send(ctx, msg)
	}
}

// HandleHelp handles the /help command.
func (h *Handler) HandleHelp(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleHelp called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	helpText := `📖 *Справка по командам бота*

*Доступные команды:*
/start - Начать работу с ботом
/help - Показать эту справку

*Функции бота:*
📥 *Получить подписку* - Создать новую подписку или получить существующую
📋 *Подписка* - Посмотреть информацию о текущей подписке

*Параметры подписки:*
📊 Трафик: ` + fmt.Sprintf("%d", h.cfg.TrafficLimitGB) + ` ГБ в месяц

*Технические детали:*
🔐 Протокол: VLESS+Reality+Vision
📱 Совместимость: V2Ray, Xray, и другие клиенты

*Поддержка:*
При возникновении проблем обратитесь к администратору: [@kereal](https://t.me/kereal)

*Дополнительная информация:*
- Подписка автоматически обновляется в конце месяца
- Не передавайте ссылку на подписку третьим лицам
- При истечении трафика подписка перестанет работать до следующего месяца`

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}
