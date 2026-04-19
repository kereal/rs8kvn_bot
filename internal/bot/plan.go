package bot

import (
	"context"
	"fmt"
	"strings"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// HandlePlan handles the /plan command for admins.
// Changes the subscription plan for a user by username.
// Usage: /plan @username <plan>
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

	// Parse the command arguments: /plan @username <plan>
	args := strings.TrimSpace(update.Message.CommandArguments())
	parts := strings.Fields(args)
	if len(parts) != 2 {
		h.SendMessage(ctx, chatID, "❌ Использование: /plan @username <plan>\n\nДоступные планы: free, basic, premium, vip\n\nПример: /plan @user premium")
		return
	}

	// Parse username (strip @ prefix)
	username := strings.TrimPrefix(parts[0], "@")

	// Validate plan
	plan := strings.ToLower(parts[1])
	if !database.ValidPlans[plan] {
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Неизвестный план: %s\n\nДоступные планы: free, basic, premium, vip", parts[1]))
		return
	}

	// Look up telegram ID by username
	telegramID, err := h.db.GetTelegramIDByUsername(ctx, username)
	if err != nil {
		logger.Error("Failed to find user by username",
			zap.String("username", username),
			zap.Error(err))
		h.SendMessage(ctx, chatID, fmt.Sprintf("❌ Пользователь @%s не найден", username))
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
		zap.String("username", username),
		zap.Int64("telegram_id", telegramID),
		zap.String("plan", plan),
		zap.Int64("admin_id", chatID))

	h.SendMessage(ctx, chatID, fmt.Sprintf(
		"✅ План обновлён!\n\n👤 @%s\n📦 План: %s", username, plan))
}
