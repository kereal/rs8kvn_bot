package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// HandleRefund возвращает Stars-платёж: /refund <order_id>, только для админов.
// Тот же паттерн доступа, что у /del (тихий отказ не-админу).
func (h *Handler) HandleRefund(ctx context.Context, update tgbotapi.Update) error {
	if update.Message == nil {
		logger.Error("HandleRefund called with nil Message")
		return fmt.Errorf("nil message")
	}

	chatID := update.Message.Chat.ID

	if !h.isAdmin(chatID) {
		logger.Warn("Non-admin user attempted to access /refund", zap.Int64("chat_id", chatID))
		return nil
	}

	if h.orderService == nil {
		h.SendMessage(ctx, chatID, "❌ Платежи не настроены")
		return nil
	}

	// Заказ и refund пишутся в audit_log с actor_id админа.
	ctx = service.WithAuditAdmin(ctx, chatID)

	args := strings.TrimSpace(update.Message.CommandArguments())
	if args == "" {
		h.SendMessage(ctx, chatID, "❌ Использование: /refund <order_id>\n\nПример: /refund 12")
		return nil
	}

	parsedID, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		logger.Debug("Invalid /refund argument", zap.String("args", args), zap.Error(err))
		h.SendMessage(ctx, chatID, "❌ ID заказа должен быть положительным числом")
		return nil
	}

	if parsedID <= 0 {
		h.SendMessage(ctx, chatID, "❌ ID заказа должен быть положительным числом")
		return nil
	}

	orderID := uint(parsedID)

	result, err := h.orderService.RefundStarsOrder(ctx, orderID)
	if err != nil {
		logger.Warn("Stars refund failed",
			zap.Uint("order_id", orderID),
			zap.Int64("admin_chat_id", chatID),
			zap.Error(err))

		// Split-brain: Telegram уже вернул Stars, но состояние заказа/подписки
		// не применено (ошибка free-plan/CAS). Повторный /refund безопасен:
		// CHARGE_ALREADY_REFUNDED трактуется как успех, CAS доводится.
		var stateErr *service.StarsRefundStateError
		if errors.As(err, &stateErr) {
			h.SendMessage(ctx, chatID,
				fmt.Sprintf("⚠️ Stars по заказу #%d УЖЕ возвращены пользователю (charge %s), но применить состояние заказа/подписки не удалось: %v.\n\nTelegram-возврат уже выполнен — повторите `/refund <id>`, команда безопасно завершит обновление заказа.",
					orderID, stateErr.ChargeID, stateErr.Err))

			return nil
		}

		// Заказ уже отменён и возврат применён ранее: честный ответ вместо
		// ложного «✅ Возврат выполнен» — админ не запускает ручную сверку.
		if errors.Is(err, service.ErrStarsOrderAlreadyRefunded) {
			h.SendMessage(ctx, chatID,
				fmt.Sprintf("↩️ Возврат по заказу #%d уже выполнен ранее: заказ отменён, Stars возвращены пользователю. Повторный возврат не требуется.", orderID))

			return nil
		}

		h.SendMessage(ctx, chatID,
			fmt.Sprintf("⚠️ Возврат по заказу #%d не выполнен: %v\n\nПроверьте статус заказа и баланс Stars бота.", orderID, err))

		return nil
	}

	if result != nil {
		// Refund применён: уведомление покупателю best-effort — ошибка отправки
		// не превращает успешный refund в ошибку. nil result (no-op, например
		// заказ уже отменён) — без повторного уведомления.
		h.notifyRefundUser(ctx, result)
	}

	h.SendMessage(ctx, chatID, fmt.Sprintf("✅ Возврат по заказу #%d выполнен: заказ отменён, Stars возвращены пользователю.", orderID))

	logger.Info("Stars refund completed", zap.Uint("order_id", orderID), zap.Int64("admin_chat_id", chatID))

	return nil
}

// notifyRefundUser отправляет покупателю уведомление о возврате Stars: заказ,
// сумму и валюту. Best-effort: ошибка отправки фиксируется в issue-событии,
// но не отменяет уже выполненный refund.
func (h *Handler) notifyRefundUser(ctx context.Context, result *service.StarsRefundResult) {
	if result.TelegramID <= 0 {
		logger.Warn("stars refund result without buyer telegram id",
			zap.Uint("order_id", result.OrderID))

		return
	}

	text := fmt.Sprintf("💸 Возврат по заказу #%d выполнен: %s возвращены на ваш аккаунт.\nЕсли остались вопросы, напишите в /paysupport.",
		result.OrderID, utils.FormatStarsAmount(result.AmountCents))

	notice := tgbotapi.NewMessage(result.TelegramID, text)
	_, sendErr := h.sendViaPaymentAPI(notice)
	if sendErr != nil {
		logger.Warn("failed to notify user about stars refund",
			zap.Uint("order_id", result.OrderID),
			zap.Int64("telegram_id", result.TelegramID),
			zap.Error(sendErr))

		h.orderService.NotifyPaymentIssue(ctx, service.PaymentIssue{
			Event:       "refund_user_notification_failed",
			Reason:      sendErr.Error(),
			Action:      "send the refund confirmation to the buyer manually",
			OrderID:     result.OrderID,
			TelegramID:  result.TelegramID,
			AmountCents: result.AmountCents,
			Currency:    result.Currency,
		})
	}
}
