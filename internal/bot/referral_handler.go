package bot

import (
	"context"
	"fmt"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// ReferralHandler manages all referral-related operations: invite generation,
// sharing links, and QR code presentation for referral links.
type ReferralHandler struct {
	db        interfaces.DatabaseService
	cfg       *config.Config
	bot       interfaces.BotAPI
	botConfig *BotConfig
	sender    *MessageSender
	keyboards *KeyboardBuilder
}

// NewReferralHandler creates a new ReferralHandler.
func NewReferralHandler(
	db interfaces.DatabaseService,
	cfg *config.Config,
	bot interfaces.BotAPI,
	botConfig *BotConfig,
	sender *MessageSender,
	keyboards *KeyboardBuilder,
) *ReferralHandler {
	return &ReferralHandler{
		db:        db,
		cfg:       cfg,
		bot:       bot,
		botConfig: botConfig,
		sender:    sender,
		keyboards: keyboards,
	}
}

// HandleInvite handles the /invite command.
func (rh *ReferralHandler) HandleInvite(ctx context.Context, chatID int64, username string, messageID int) {
	logger.Info("User requesting invite link",
		zap.Int64("chat_id", chatID),
		zap.String("username", username))
	rh.sendInviteLink(ctx, chatID, messageID)
}

// sendInviteLink generates a new invite code and sends the invite links to the user.
func (rh *ReferralHandler) sendInviteLink(ctx context.Context, chatID int64, messageID int) {
	inviteCode, err := utils.GenerateInviteCode()
	if err != nil {
		logger.Error("Failed to generate invite code",
			zap.Error(err),
			zap.Int64("chat_id", chatID))
		rh.sender.SendMessage(ctx, chatID, msg(MsgInviteCreateFailed))
		return
	}
	invite, err := rh.db.GetOrCreateInvite(ctx, chatID, inviteCode)
	if err != nil {
		logger.Error("Failed to get or create invite",
			zap.Error(err),
			zap.Int64("chat_id", chatID))
		rh.sender.SendMessage(ctx, chatID, msg(MsgInviteCreateFailed))
		return
	}

	telegramLink := fmt.Sprintf("https://t.me/%s?start=share_%s", rh.botConfig.Username, invite.Code)
	webLink := fmt.Sprintf("%s/i/%s", rh.cfg.SiteURL, invite.Code)
	text := rh.keyboards.InviteLinkText(telegramLink, webLink)
	keyboard := rh.keyboards.Invite()

	if messageID > 0 {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.ParseMode = "Markdown"
		editMsg.DisableWebPagePreview = true
		editMsg.ReplyMarkup = &keyboard
		rh.sender.SafeSend(editMsg)
	} else {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ParseMode = "Markdown"
		msg.DisableWebPagePreview = true
		msg.ReplyMarkup = &keyboard
		rh.sender.Send(ctx, msg)
	}
}

// generateInviteLink returns a Telegram or web invite link for the given user.
func (rh *ReferralHandler) generateInviteLink(ctx context.Context, chatID int64, lt linkType) (string, error) {
	inviteCode, err := utils.GenerateInviteCode()
	if err != nil {
		return "", fmt.Errorf("generate invite code: %w", err)
	}
	invite, err := rh.db.GetOrCreateInvite(ctx, chatID, inviteCode)
	if err != nil {
		return "", fmt.Errorf("get or create invite: %w", err)
	}

	switch lt {
	case linkTypeTelegram:
		return fmt.Sprintf("https://t.me/%s?start=share_%s", rh.botConfig.Username, invite.Code), nil
	case linkTypeWeb:
		return fmt.Sprintf("%s/i/%s", rh.cfg.SiteURL, invite.Code), nil
	default:
		return "", fmt.Errorf("unknown link type: %s", lt)
	}
}
