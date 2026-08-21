package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"github.com/kereal/rs8kvn_bot/internal/logger"
)

// CallbackHandler processes callback queries from inline keyboards.
type CallbackHandler struct {
	h *Handler
}

// NewCallbackHandler constructs CallbackHandler with parent reference.
func NewCallbackHandler(parent *Handler) *CallbackHandler {
	return &CallbackHandler{h: parent}
}

// HandleCallback routes callback data to appropriate handlers.
func (c *CallbackHandler) HandleCallback(ctx context.Context, update tgbotapi.Update) error {
	if update.CallbackQuery == nil {
		logger.Error("HandleCallback called with nil CallbackQuery")
		return fmt.Errorf("nil callback query")
	}

	if update.CallbackQuery.From == nil {
		logger.Error("HandleCallback: CallbackQuery.From is nil",
			zap.String("data", update.CallbackQuery.Data))

		return fmt.Errorf("nil from")
	}

	if update.CallbackQuery.Message == nil {
		logger.Warn("CallbackQuery has nil Message, skipping",
			zap.String("data", update.CallbackQuery.Data),
			zap.Int64("from_id", update.CallbackQuery.From.ID))

		callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "Сообщение не найдено")

		_, err := c.h.bot.Request(callback)
		if err != nil {
			logger.Error("Failed to answer callback", zap.Error(err))
		}

		return nil
	}

	data := update.CallbackQuery.Data
	chatID := update.CallbackQuery.Message.Chat.ID
	username := c.h.getUsername(update.CallbackQuery.From)

	logger.Debug("Callback received",
		append(userFields(update.CallbackQuery.From, chatID), zap.String("data", data))...)

	switch {
	case data == "create_subscription":
		err := c.h.handleCreateSubscription(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle create_subscription: %w", err)
		}
	case data == "qr_code":
		err := c.h.handleQRCode(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle qr_code: %w", err)
		}
	case data == "admin_stats":
		err := c.h.handleAdminStats(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle admin_stats: %w", err)
		}
	case data == "admin_lastreg":
		err := c.h.handleAdminLastReg(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle admin_lastreg: %w", err)
		}
	case data == "menu_subscription":
		err := c.h.handleMySubscription(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle menu_subscription: %w", err)
		}
	case data == "back_to_start":
		err := c.h.handleBackToStart(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle back_to_start: %w", err)
		}
	case data == "buy_premium_list":
		err := c.h.handleBuyPremiumList(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle buy_premium_list: %w", err)
		}
	case strings.HasPrefix(data, "buy_product_"):
		raw := strings.TrimPrefix(data, "buy_product_")

		id64, _ := strconv.ParseUint(raw, 10, 64)
		if id64 == 0 || id64 > uint64(^uint(0)) {
			logger.Warn("invalid buy_product callback payload",
				zap.String("data", data),
				zap.String("raw", raw))

			return nil
		}

		err := c.h.handleBuyProduct(ctx, chatID, username, update.CallbackQuery.Message.MessageID, uint(id64))
		if err != nil {
			return fmt.Errorf("handle buy_product: %w", err)
		}
	case data == "menu_donate":
		err := c.h.handleMenuDonate(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle menu_donate: %w", err)
		}
	case data == "back_to_subscription":
		err := c.h.handleBackToSubscription(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle back_to_subscription: %w", err)
		}
	case data == "menu_help":
		err := c.h.handleMenuHelp(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle menu_help: %w", err)
		}
	case data == "back_to_documents":
		err := c.h.handleBackToDocuments(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle back_to_documents: %w", err)
		}
	case data == "menu_documents":
		err := c.h.handleMenuDocuments(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle menu_documents: %w", err)
		}
	case data == "share_invite":
		err := c.handleShareInvite(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle share_invite: %w", err)
		}
	case data == "menu_privacy":
		err := c.h.handleMenuPrivacy(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle menu_privacy: %w", err)
		}
	case data == "menu_terms":
		err := c.h.handleMenuTerms(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle menu_terms: %w", err)
		}
	case data == "menu_support":
		err := c.h.handleMenuSupport(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle menu_support: %w", err)
		}
	case data == "qr_telegram":
		err := c.handleQRTelegram(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle qr_telegram: %w", err)
		}
	case data == "qr_web":
		err := c.handleQRWeb(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle qr_web: %w", err)
		}
	case data == "broadcast_confirm":
		err := c.h.handleBroadcastConfirm(ctx, chatID)
		if err != nil {
			return fmt.Errorf("handle broadcast_confirm: %w", err)
		}
	case data == "broadcast_cancel":
		err := c.h.handleBroadcastCancel(ctx, chatID)
		if err != nil {
			return fmt.Errorf("handle broadcast_cancel: %w", err)
		}
	case data == "broadcast_final_confirm":
		err := c.h.handleBroadcastFinalConfirm(ctx, chatID)
		if err != nil {
			return fmt.Errorf("handle broadcast_final_confirm: %w", err)
		}
	case data == "broadcast_back_to_filters":
		err := c.h.handleBroadcastBackToFilters(ctx, chatID, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle broadcast_back_to_filters: %w", err)
		}
	case strings.HasPrefix(data, "bfilter_"):
		if !c.h.isAdmin(chatID) {
			return nil
		}
		err := c.h.handleBroadcastFilter(ctx, chatID, update.CallbackQuery.Message.MessageID, data)
		if err != nil {
			return fmt.Errorf("handle broadcast filter: %w", err)
		}
	case data == "back_to_invite":
		err := c.h.handleBackToInvite(ctx, chatID, username, update.CallbackQuery.Message.MessageID)
		if err != nil {
			return fmt.Errorf("handle back_to_invite: %w", err)
		}
	case strings.HasPrefix(data, "broadcast_details_"):
		if !c.h.isAdmin(chatID) {
			logger.Warn("Non-admin user attempted to access broadcast details",
				zap.Int64("chat_id", chatID))
			return nil
		}

		raw := strings.TrimPrefix(data, "broadcast_details_")

		id64, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id64 == 0 || id64 > uint64(^uint(0)) {
			logger.Warn("invalid broadcast_details callback payload",
				zap.String("data", data), zap.Error(err))

			return nil
		}

		c.h.sendBroadcastDetails(ctx, chatID, uint(id64))
	default:
		logger.Warn("Unknown callback data", zap.String("data", data))
	}

	callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "")

	_, err := c.h.bot.Request(callback)
	if err != nil {
		logger.Error("Failed to answer callback", zap.Error(err))
	}

	return nil
}

// handleShareInvite generates and sends an invite link.
func (c *CallbackHandler) handleShareInvite(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting share invite", zap.String("username", username), zap.Int64("chat_id", chatID))
	return c.h.referral.sendInviteLink(ctx, chatID, messageID)
}

// handleQRTelegram generates QR for Telegram invite link.
func (c *CallbackHandler) handleQRTelegram(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting QR for Telegram invite", zap.String("username", username), zap.Int64("chat_id", chatID))

	link, err := c.h.referral.generateInviteLink(ctx, chatID, linkTypeTelegram)
	if err != nil {
		return fmt.Errorf("generate invite link (telegram): %w", err)
	}

	err = c.h.sendQRCode(ctx, chatID, messageID, link, "📱 QR-код для Telegram-инвайта")
	if err != nil {
		return fmt.Errorf("send qr code: %w", err)
	}

	return nil
}

// handleQRWeb generates QR for web invite page.
func (c *CallbackHandler) handleQRWeb(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting QR for web invite", zap.String("username", username), zap.Int64("chat_id", chatID))

	link, err := c.h.referral.generateInviteLink(ctx, chatID, linkTypeWeb)
	if err != nil {
		logger.Error("Failed to get invite for QR", zap.Error(err))

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка генерации QR-кода. Попробуйте позже.")
		c.h.safeSend(editMsg)

		return fmt.Errorf("generate invite link (web): %w", err)
	}

	err = c.h.sendQRCode(ctx, chatID, messageID, link, "🌐 QR-код для веб-страницы\n\nПокажите этот QR-код для открытия страницы с подпиской")
	if err != nil {
		return fmt.Errorf("send qr code: %w", err)
	}

	return nil
}
