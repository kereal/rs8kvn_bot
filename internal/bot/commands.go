package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

func (h *Handler) HandleStart(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleStart called with nil Message")
		return
	}

	if update.Message.From == nil {
		logger.Error("HandleStart: Message.From is nil",
			zap.Int64("chat_id", update.Message.Chat.ID))
		return
	}

	chatID := update.Message.Chat.ID
	username := h.getUsername(update.Message.From)

	args := update.Message.CommandArguments()
	if strings.HasPrefix(args, "trial_") {
		h.handleBindTrial(ctx, chatID, username, strings.TrimPrefix(args, "trial_"))
		return
	}

	logger.Info("User started bot",
		zap.Int64("chat_id", chatID),
		zap.String("username", username))

	sub, err := h.db.GetByTelegramID(ctx, chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	text, keyboard := h.getMainMenuContent(username, hasSubscription, chatID)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = &keyboard
	h.send(ctx, msg)
}

func (h *Handler) handleBindTrial(ctx context.Context, chatID int64, username, subscriptionID string) {
	logger.Info("User binding trial subscription",
		zap.Int64("chat_id", chatID),
		zap.String("username", username),
		zap.String("subscription_id", subscriptionID))

	// Check if user already has an active subscription
	if existing, err := h.db.GetByTelegramID(ctx, chatID); err == nil && existing != nil {
		logger.Warn("User already has active subscription, skipping trial bind",
			zap.Int64("chat_id", chatID),
			zap.String("existing_sub_id", existing.SubscriptionID))
		h.SendMessage(ctx, chatID, "❌ У вас уже есть активная подписка. Используйте /start для управления.")
		return
	}

	sub, err := h.db.BindTrialSubscription(ctx, subscriptionID, chatID, username)
	if err != nil {
		logger.Error("Failed to bind trial subscription",
			zap.Error(err),
			zap.Int64("chat_id", chatID))
		h.SendMessage(ctx, chatID, "❌ Не удалось активировать подписку. Возможно, ссылка уже была использована или истекла.")
		return
	}

	logger.Info("Trial subscription bound successfully",
		zap.Int64("chat_id", chatID),
		zap.String("subscription_id", subscriptionID))

	// Upgrade trial client in xui panel
	var comment string
	if invite, err := h.db.GetInviteByCode(ctx, sub.InviteCode); err == nil {
		if referrerSub, err := h.db.GetByTelegramID(ctx, invite.ReferrerTGID); err == nil {
			comment = fmt.Sprintf("from: @%s", referrerSub.Username)
		}
	}

	trafficBytes := int64(h.cfg.TrafficLimitGB) * 1024 * 1024 * 1024
	if err := h.xui.UpdateClient(ctx, h.cfg.XUIInboundID, sub.ClientID, username, sub.SubscriptionID, trafficBytes, time.UnixMilli(0), chatID, comment); err != nil {
		logger.Warn("Failed to upgrade trial client in xui", zap.Error(err))
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf("✅ Подписка активирована!\n\nДобро пожаловать!\n\nВам доступно: %dГб\n\nИспользуйте /start для работы с ботом.", h.cfg.TrafficLimitGB))

	if h.cfg.TelegramAdminID > 0 {
		invite, _ := h.db.GetInviteByCode(ctx, sub.InviteCode)
		if invite != nil {
			h.SendMessage(ctx, h.cfg.TelegramAdminID, fmt.Sprintf("🔔 Новый пользователь активировал подписку по реферальной ссылке!\n\n- Username: @%s\n- Telegram ID: %d\n- Пригласил: %d", username, chatID, invite.ReferrerTGID))
		}
	}
}

func (h *Handler) HandleInvite(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		logger.Error("HandleInvite called with nil Message")
		return
	}

	if update.Message.From == nil {
		logger.Error("HandleInvite: Message.From is nil",
			zap.Int64("chat_id", update.Message.Chat.ID))
		return
	}

	chatID := update.Message.Chat.ID
	username := h.getUsername(update.Message.From)

	logger.Info("User requested invite link",
		zap.Int64("chat_id", chatID),
		zap.String("username", username))

	h.sendInviteLink(ctx, chatID, 0)
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
/invite - Получить реферальную ссылку

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
