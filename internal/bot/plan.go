package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// HandlePlan handles the /plan command for admins.
// Changes the subscription plan for a user by Telegram ID.
// Usage: /plan <telegram_id> <plan>
// Allowed plans: free, basic, premium, vip
func (h *Handler) HandlePlan(ctx context.Context, update tgbotapi.Update) {
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

	if update.Message == nil {
		logger.Error("HandlePlan called with nil Message")
		return
	}

	chatID := update.Message.Chat.ID

	// Verify admin access
	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /plan", zap.Int64("chat_id", chatID))
		return
	}

	// Parse the command arguments: /plan <telegram_id> <plan>
	args := strings.TrimSpace(update.Message.CommandArguments())
	parts := strings.Fields(args)
	if len(parts) != 2 {
		h.SendMessage(ctx, chatID, "❌ Использование: /plan <telegram_id> <plan>\n\nДоступные планы: free, basic, premium, vip\n\nПример: /plan 123456 premium")
		return
	}

	// Parse Telegram ID
	var telegramID int64
	var err error
	if telegramID, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
		h.SendMessage(ctx, chatID, "❌ Неверный формат Telegram ID. Использование: /plan <telegram_id> <plan>")
		return
	}

	// Validate plan
	plan := strings.ToLower(parts[1])
	if !database.ValidPlans[plan] {
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Неизвестный план: %s\n\nДоступные планы: free, basic, premium, vip", parts[1]))
		return
	}

	// Update plan in database
	if err := h.db.UpdatePlan(ctx, telegramID, plan); err != nil {
		logger.Error("Failed to update plan",
			zap.Int64("telegram_id", telegramID),
			zap.String("plan", plan),
			zap.Error(err))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Ошибка обновления плана: %v", err))
		return
	}

	// Invalidate cache so next request sees the new plan
	h.invalidateCache(telegramID)

	logger.Info("Plan updated",
		zap.Int64("telegram_id", telegramID),
		zap.String("plan", plan),
		zap.Int64("admin_id", chatID))

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ План обновлён!\n\n🆔 Telegram ID: %d\n📦 План: %s", telegramID, plan))
}
