package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

const handlerTimeout = 30 * time.Second

func (h *Handler) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, handlerTimeout)
}

func (h *Handler) HandleStart(ctx context.Context, update tgbotapi.Update) {
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

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

	// Обработка share-ссылок: t.me/{bot}?start=share_{invite_code}
	if strings.HasPrefix(args, "share_") {
		h.handleShareStart(ctx, chatID, username, strings.TrimPrefix(args, "share_"))
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
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

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
		h.SendMessage(ctx, chatID, "❌ Не удалось активировать подписку. Возможно, ссылка уже была использована.")
		return
	}

	logger.Info("Trial subscription bound successfully",
		zap.Int64("chat_id", chatID),
		zap.String("subscription_id", subscriptionID))

	// Invalidate subscription cache — user now has an active subscription
	// but the cache may still contain a stale "no subscription" entry.
	h.invalidateCache(chatID)

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
		invite, err := h.db.GetInviteByCode(ctx, sub.InviteCode)
		if err != nil {
			logger.Warn("Failed to get invite for admin notification", zap.Error(err))
		} else if invite != nil {
			h.SendMessage(ctx, h.cfg.TelegramAdminID, fmt.Sprintf("🔔 Новый пользователь активировал подписку по реферальной ссылке!\n\n- Username: @%s\n- Telegram ID: %d\n- Пригласил: %d", username, chatID, invite.ReferrerTGID))
		}
	}

	// Notify referrer about new referral activation
	if sub.ReferredBy > 0 {
		referrerMsg := fmt.Sprintf("🎉 По вашей ссылке новый пользователь @%s активировал подписку!", username)
		msg := tgbotapi.NewMessage(sub.ReferredBy, referrerMsg)
		if err := h.sendWithError(ctx, msg); err != nil {
			logger.Warn("Failed to notify referrer", zap.Int64("referrer_id", sub.ReferredBy), zap.Error(err))
		} else {
			logger.Info("Referrer notified", zap.Int64("referrer_id", sub.ReferredBy))
		}
	}
}

func (h *Handler) HandleInvite(ctx context.Context, update tgbotapi.Update) {
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

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

	helpText := fmt.Sprintf(`📖 *Справка по командам бота*

*Доступные команды:*
/start - Начать работу с ботом
/help - Показать эту справку
/invite - Получить реферальную ссылку

*Функции бота:*
📥 *Получить подписку* - Создать новую подписку или получить существующую
📋 *Подписка* - Посмотреть информацию о текущей подписке

*Параметры подписки:*
📊 Трафик: %d ГБ в месяц

*Технические детали:*
🔐 Протокол: VLESS+Reality+Vision
📱 Совместимость: V2Ray, Xray, и другие клиенты

*Поддержка:*
При возникновении проблем обратитесь к администратору: [@%s](https://t.me/%s)

*Дополнительная информация:*
- Подписка автоматически обновляется в конце месяца
- Не передавайте ссылку на подписку третьим лицам
- При истечении трафика подписка перестанет работать до следующего месяца`,
		h.cfg.TrafficLimitGB,
		h.cfg.ContactUsername,
		h.cfg.ContactUsername,
	)

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	h.send(ctx, msg)
}

// handleShareStart обрабатывает переход по share-ссылке: t.me/{bot}?start=share_{invite_code}
// Если у пользователя уже есть активная подписка — игнорируем код.
// Если нет — сохраняем invite_code в кэш на 60 минут для последующего использования при создании подписки.
func (h *Handler) handleShareStart(ctx context.Context, chatID int64, username, inviteCode string) {
	logger.Info("User clicked share link",
		zap.Int64("chat_id", chatID),
		zap.String("username", username),
		zap.String("invite_code", inviteCode))

	// Проверяем, есть ли уже активная подписка
	sub, err := h.db.GetByTelegramID(ctx, chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	if hasSubscription {
		// У пользователя уже есть подписка — игнорируем share-код
		logger.Info("User with existing subscription clicked share link, ignoring",
			zap.Int64("chat_id", chatID),
			zap.String("invite_code", inviteCode))

		text, keyboard := h.getMainMenuContent(username, true, chatID)
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = &keyboard
		h.send(ctx, msg)
		return
	}

	// Проверяем, что invite_code существует в БД
	invite, err := h.db.GetInviteByCode(ctx, inviteCode)
	if err != nil {
		logger.Warn("Invalid invite code in share link",
			zap.String("invite_code", inviteCode),
			zap.Error(err))

		// Показываем обычное меню для нового пользователя
		text, keyboard := h.getMainMenuContent(username, false, chatID)
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = &keyboard
		h.send(ctx, msg)
		return
	}

	// Сохраняем invite_code в кэш на 60 минут
	h.pendingMu.Lock()
	h.pendingInvites[chatID] = pendingInvite{
		code:      inviteCode,
		expiresAt: time.Now().Add(PendingInviteTTL),
	}
	h.pendingMu.Unlock()

	logger.Info("Share invite code cached",
		zap.Int64("chat_id", chatID),
		zap.String("invite_code", inviteCode),
		zap.Int64("referrer_tg_id", invite.ReferrerTGID))

	// Показываем сообщение о приглашении с кнопкой получения подписки
	text := fmt.Sprintf(
		"🎉 Вас пригласили!\n\n" +
			"Нажмите кнопку ниже, чтобы получить подписку и активировать реферальное подключение.",
	)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "create_subscription"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = &keyboard
	h.send(ctx, msg)
}

func (h *Handler) sendInviteLink(ctx context.Context, chatID int64, messageID int) {
	inviteCode, err := utils.GenerateInviteCode()
	if err != nil {
		logger.Error("Failed to generate invite code",
			zap.Error(err),
			zap.Int64("chat_id", chatID))
		h.SendMessage(ctx, chatID, msg(MsgInviteCreateFailed))
		return
	}
	invite, err := h.db.GetOrCreateInvite(ctx, chatID, inviteCode)
	if err != nil {
		logger.Error("Failed to get or create invite",
			zap.Error(err),
			zap.Int64("chat_id", chatID))
		h.SendMessage(ctx, chatID, msg(MsgInviteCreateFailed))
		return
	}

	telegramLink := fmt.Sprintf("https://t.me/%s?start=share_%s", h.botConfig.Username, invite.Code)
	webLink := fmt.Sprintf("%s/i/%s", h.cfg.SiteURL, invite.Code)
	text := h.keyboards.InviteLinkText(telegramLink, webLink)
	keyboard := h.keyboards.Invite()

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
