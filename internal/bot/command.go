package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
)

// CommandHandler handles command updates: /start, /help, /invite and share_ links.
type CommandHandler struct {
	h *Handler
}

// NewCommandHandler creates a new CommandHandler with parent reference.
func NewCommandHandler(parent *Handler) *CommandHandler {
	return &CommandHandler{h: parent}
}

// HandleStart processes /start command.
func (c *CommandHandler) HandleStart(ctx context.Context, update tgbotapi.Update) error {
	ctxWithTimeout, cancel := c.h.withTimeout(ctx)
	defer cancel()
	ctx = ctxWithTimeout

	if update.Message == nil {
		logger.Error("HandleStart called with nil Message")
		return fmt.Errorf("nil message")
	}
	if update.Message.From == nil {
		logger.Error("HandleStart: Message.From is nil",
			zap.Int64("chat_id", update.Message.Chat.ID))
		return fmt.Errorf("nil from")
	}

	chatID := update.Message.Chat.ID
	username := c.h.getUsername(update.Message.From)

	args := update.Message.CommandArguments()
	if strings.HasPrefix(args, "trial_") {
		return c.handleBindTrial(ctx, chatID, username, strings.TrimPrefix(args, "trial_"))
	}
	if strings.HasPrefix(args, "share_") {
		return c.handleShareStart(ctx, chatID, username, strings.TrimPrefix(args, "share_"))
	}

	logger.Info("User started bot",
		zap.Int64("chat_id", chatID),
		zap.String("username", username))

	sub, err := c.h.db.GetByTelegramID(ctx, chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	text, keyboard := c.h.getMainMenuContent(username, hasSubscription, chatID)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = &keyboard
	c.h.send(ctx, msg)
	return nil
}

// HandleHelp sends help message.
func (c *CommandHandler) HandleHelp(ctx context.Context, update tgbotapi.Update) error {
	ctxWithTimeout, cancel := c.h.withTimeout(ctx)
	defer cancel()
	ctx = ctxWithTimeout

	if update.Message == nil {
		logger.Error("HandleHelp called with nil Message")
		return fmt.Errorf("nil message")
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
		c.h.cfg.TrafficLimitGB,
		c.h.cfg.ContactUsername,
		c.h.cfg.ContactUsername,
	)

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	c.h.send(ctx, msg)
	return nil
}

// HandleInvite processes /invite command.
func (c *CommandHandler) HandleInvite(ctx context.Context, update tgbotapi.Update) error {
	ctxWithTimeout, cancel := c.h.withTimeout(ctx)
	defer cancel()
	ctx = ctxWithTimeout

	if update.Message == nil {
		logger.Error("HandleInvite called with nil Message")
		return fmt.Errorf("nil message")
	}
	if update.Message.From == nil {
		logger.Error("HandleInvite called with nil From")
		return fmt.Errorf("nil from")
	}

	chatID := update.Message.Chat.ID
	username := c.h.getUsername(update.Message.From)

	logger.Info("User requested invite link",
		zap.Int64("chat_id", chatID),
		zap.String("username", username))

	// Delegate to referral handler (no rate limit here)
	if err := c.h.referral.HandleInvite(ctx, chatID, username, 0); err != nil {
		return err
	}
	return nil
}

// handleShareStart processes deep links: t.me/{bot}?start=share_{invite_code}
func (c *CommandHandler) handleShareStart(ctx context.Context, chatID int64, username, inviteCode string) error {
	logger.Info("User clicked share link",
		zap.Int64("chat_id", chatID),
		zap.String("username", username),
		zap.String("invite_code", inviteCode))

	// Check existing subscription
	sub, err := c.h.db.GetByTelegramID(ctx, chatID)
	hasSubscription := err == nil && sub != nil && sub.Status == "active"

	if hasSubscription {
		logger.Info("User with existing subscription clicked share link, ignoring",
			zap.Int64("chat_id", chatID),
			zap.String("invite_code", inviteCode))

		text, keyboard := c.h.getMainMenuContent(username, true, chatID)
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = &keyboard
		c.h.send(ctx, msg)
		return nil
	}

	// Validate invite code
	invite, err := c.h.db.GetInviteByCode(ctx, inviteCode)
	if err != nil {
		if errors.Is(err, database.ErrInviteNotFound) {
			logger.Warn("Invalid invite code in share link",
				zap.String("invite_code", inviteCode),
				zap.Error(err))

			text, keyboard := c.h.getMainMenuContent(username, false, chatID)
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ReplyMarkup = &keyboard
			c.h.send(ctx, msg)
			return nil
		}
		// Infra error (DB down, etc) — log full, do not treat as "invalid code"
		logger.Error("Failed to validate invite code (infrastructure error)",
			zap.String("invite_code", inviteCode),
			zap.Error(err))
		text, keyboard := c.h.getMainMenuContent(username, false, chatID)
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = &keyboard
		c.h.send(ctx, msg)
		return fmt.Errorf("get invite by code: %w", err)
	}

	// Cache invite code for 60 minutes for later reward
	c.h.pendingMu.Lock()
	c.h.pendingInvites[chatID] = pendingInvite{
		code:      inviteCode,
		expiresAt: time.Now().Add(60 * time.Minute),
	}
	c.h.pendingMu.Unlock()

	logger.Info("Share invite code cached",
		zap.Int64("chat_id", chatID),
		zap.String("invite_code", inviteCode),
		zap.Int64("referrer_tg_id", invite.ReferrerTGID))

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
	c.h.send(ctx, msg)
	return nil
}

// handleBindTrial binds a trial subscription from a deep link.
func (c *CommandHandler) handleBindTrial(ctx context.Context, chatID int64, username, subscriptionID string) error {
	ctxWithTimeout, cancel := c.h.withTimeout(ctx)
	defer cancel()
	ctx = ctxWithTimeout

	logger.Info("User binding trial subscription",
		zap.Int64("chat_id", chatID),
		zap.String("username", username),
		zap.String("subscription_id", subscriptionID))

	// Check existing subscription — only truly active ones block trial bind (expired/cancelled allow re-trial)
	if existing, err := c.h.db.GetByTelegramID(ctx, chatID); err == nil && existing != nil && existing.IsActive() {
		logger.Warn("User already has active subscription, skipping trial bind",
			zap.Int64("chat_id", chatID),
			zap.String("existing_sub_id", existing.SubscriptionID))
		c.h.SendMessage(ctx, chatID, "❌ У вас уже есть активная подписка. Используйте /start для управления.")
		return nil
	}

	sub, err := c.h.subscriptionService.BindTrial(ctx, subscriptionID, chatID, username)
	if err != nil {
		logger.Error("Failed to bind trial subscription",
			zap.Error(err),
			zap.Int64("chat_id", chatID))
		c.h.SendMessage(ctx, chatID, "❌ Не удалось активировать подписку. Возможно, ссылка уже была использована.")
		return fmt.Errorf("bind trial: %w", err)
	}

	logger.Info("Trial subscription bound successfully",
		zap.Int64("chat_id", chatID),
		zap.String("subscription_id", subscriptionID))

	c.h.invalidateCache(chatID)
	c.h.SendMessage(ctx, chatID, fmt.Sprintf("✅ Подписка активирована!\n\nДобро пожаловать!\n\nВам доступно: %dГб\n\nИспользуйте /start для работы с ботом.", c.h.cfg.TrafficLimitGB))

	// Admin notification
	if c.h.cfg.TelegramAdminID > 0 {
		invite, err := c.h.db.GetInviteByCode(ctx, sub.InviteCode)
		if err != nil {
			logger.Warn("Failed to get invite for admin notification", zap.Error(err))
		} else if invite != nil {
			c.h.SendMessage(ctx, c.h.cfg.TelegramAdminID,
				fmt.Sprintf("🔔 Новый пользователь активировал подписку по реферационной ссылке!\n\n- Username: @%s\n- Telegram ID: %d\n- Пригласил: %d",
					username, chatID, invite.ReferrerTGID))
		}
	}

	// Notify referrer
	if sub.ReferredBy > 0 {
		referrerMsg := fmt.Sprintf("🎉 По вашей ссылке новый пользователь @%s активировал подписку!", username)
		msg := tgbotapi.NewMessage(sub.ReferredBy, referrerMsg)
		if err := c.h.sendWithError(ctx, msg); err != nil {
			logger.Warn("Failed to notify referrer", zap.Int64("referrer_id", sub.ReferredBy), zap.Error(err))
		} else {
			logger.Info("Referrer notified", zap.Int64("referrer_id", sub.ReferredBy))
		}
	}

	return nil
}
