package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
)

func parseStarsAcceptPayload(raw string) (orderID, productID uint, cardChatID int64, cardMsgID int, ok bool) {
	parts := strings.Split(raw, "_")
	if len(parts) != 4 {
		return 0, 0, 0, 0, false
	}

	orderID64, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || orderID64 == 0 || orderID64 > uint64(^uint(0)) {
		return 0, 0, 0, 0, false
	}

	productID64, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || productID64 == 0 || productID64 > uint64(^uint(0)) {
		return 0, 0, 0, 0, false
	}

	cardChatID, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil || cardChatID == 0 {
		return 0, 0, 0, 0, false
	}

	cardMsgID, err = strconv.Atoi(parts[3])
	if err != nil || cardMsgID < 0 {
		return 0, 0, 0, 0, false
	}

	return uint(orderID64), uint(productID64), cardChatID, cardMsgID, true
}

// parseStarsInvoiceBack разбирает хвост
// stars_invoice_back_<buyer_telegram_id>_<invoice_msg_id>_<order_id>.
// product_id из формата удалён (N3): при разборе/обработке он не использовался,
// вместо него кнопка несёт order_id pending-заказа (S4 — «Назад» истекает
// pending-заказ, не дожидаясь TTL).
func parseStarsInvoiceBack(raw string) (buyerTelegramID int64, invoiceMsgID int, orderID uint, ok bool) {
	parts := strings.Split(raw, "_")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}

	buyerID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || buyerID <= 0 {
		return 0, 0, 0, false
	}

	msgID, err := strconv.Atoi(parts[1])
	if err != nil || msgID <= 0 {
		return 0, 0, 0, false
	}

	parsedOrder, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil || parsedOrder == 0 || parsedOrder > uint64(^uint(0)) {
		return 0, 0, 0, false
	}

	return buyerID, msgID, uint(parsedOrder), true
}

// handleStarsInvoiceBack истекает pending-заказ (S4: иначе order живёт до TTL,
// а dedupe-set блокирует новый invoice до 15 минут), удаляет только
// invoice-сообщение и возвращает карточку тарифа к списку тарифов
// редактированием на месте, без дубля (§5.1, шаг 11-12).
// Invoice отправлен в личный чат покупателя по telegram_id (не в callback chat):
// удаление идёт по buyerTelegramID из callback-данных и только когда нажавший
// — сам покупатель (чужой в групповом чате не удаляет сообщения в своей личке).
// Карточка НЕ пересоздаётся: её message_id — это message_id сообщения с кнопкой,
// правится она в чате самого callback-сообщения (N6: payload не содержит
// чужих чатов для правки — edit target это chatID callback'а).
func (c *CallbackHandler) handleStarsInvoiceBack(ctx context.Context, chatID, pressedBy int64, username string, cardMsgID, invoiceMsgID int, buyerTelegramID int64, orderID uint) error {
	if pressedBy != buyerTelegramID {
		logger.Warn("stars invoice back pressed by non-buyer ignored",
			zap.Int64("pressed_by", pressedBy),
			zap.Int64("buyer_telegram_id", buyerTelegramID))

		return nil
	}

	// S4: истекаем pending-заказ, чтобы повторная покупка сразу создала новый.
	// Владение подтверждаем по хранилищу, а не по buyerTelegramID из
	// callback-данных: callback_data не доказывает владение заказом (клиент может
	// прислать произвольный payload), а ExpirePendingOrder владельца не проверяет.
	// Без проверки нажатие «Назад» истекало бы чужой pending-заказ — отказ в
	// покупке другому пользователю. Нечитаемый или чужой заказ не изменяем.
	if orderID != 0 {
		expirable, loadErr := c.starsOrderExpirable(ctx, orderID, pressedBy)
		if loadErr != nil {
			// Сбой чтения заказа: статус не меняем и invoice НЕ удаляем — повторное
			// нажатие «Назад» повторит попытку (как при сбое истечения).
			logger.Warn("stars invoice back: order load failed; keeping invoice message",
				zap.Uint("order_id", orderID), zap.Error(loadErr))

			return c.h.subHandler.handleBuyPremiumList(ctx, chatID, username, cardMsgID)
		}

		if expirable {
			expired, expireErr := c.h.db.ExpirePendingOrder(ctx, orderID)
			switch {
			case expireErr != nil:
				// Сбой истечения: pending-заказ остаётся, повторная покупка покажет
				// «счёт отправлен ранее» — invoice НЕ удаляем, чтобы это оставалось
				// правдой. TTL (STARS_INVOICE_TTL) истечёт заказ фоново.
				logger.Warn("failed to expire pending order on stars invoice back; keeping invoice message",
					zap.Uint("order_id", orderID), zap.Error(expireErr))

				return c.h.subHandler.handleBuyPremiumList(ctx, chatID, username, cardMsgID)
			case !expired:
				logger.Info("stars invoice back: pending order not in expirable state",
					zap.Uint("order_id", orderID))
			}
		}
	}

	logger.Info("Stars invoice card back",
		zap.Int64("chat_id", chatID),
		zap.Int64("buyer_telegram_id", buyerTelegramID),
		zap.String("username", username),
		zap.Int("card_msg_id", cardMsgID),
		zap.Int("invoice_msg_id", invoiceMsgID),
		zap.Uint("order_id", orderID))

	deleteMsg := tgbotapi.NewDeleteMessage(buyerTelegramID, invoiceMsgID)

	_, err := c.h.requestViaPaymentAPI(deleteMsg)
	if err != nil {
		// Best-effort: не отзываемый invoice не должен ломать возврат карточки.
		logger.Warn("failed to delete stars invoice message",
			zap.Int("invoice_msg_id", invoiceMsgID),
			zap.Int64("buyer_telegram_id", buyerTelegramID),
			zap.Error(err))
	}

	return c.h.subHandler.handleBuyPremiumList(ctx, chatID, username, cardMsgID)
}

// starsOrderExpirable сообщает, можно ли истечь заказ orderID по нажатию «Назад»
// пользователем telegramID: заказ выставлен на Telegram Stars и принадлежит
// этому пользователю. Владелец вычисляется по хранилищу: заказ → его подписка →
// telegram_id подписки.
// Ошибка возвращается только когда заказ/подписку не удалось прочитать — такое
// нажатие повторяемо, поэтому вызывающий не меняет статус и не удаляет invoice.
// Ненайденный, чужой или не-Stars заказ — это false без ошибки: изменять нечего,
// обработка «Назад» продолжается.
func (c *CallbackHandler) starsOrderExpirable(ctx context.Context, orderID uint, telegramID int64) (bool, error) {
	order, err := c.h.db.GetOrderByID(ctx, orderID)
	if err != nil {
		return false, fmt.Errorf("load order %d: %w", orderID, err)
	}

	if order == nil {
		logger.Warn("stars invoice back: order not found; expire skipped",
			zap.Uint("order_id", orderID))

		return false, nil
	}

	// Истекать можно только собственный pending-заказ Stars: «Назад» относится к
	// Stars-счёту, чужой провайдер (например, Platega) — подделанный payload.
	if order.PaymentProvider != database.PaymentProviderTelegramStars {
		logger.Warn("stars invoice back: order provider mismatch; expire skipped",
			zap.Uint("order_id", orderID),
			zap.String("provider", order.PaymentProvider))

		return false, nil
	}

	sub, err := c.h.db.GetByID(ctx, order.SubscriptionID)
	if err != nil {
		return false, fmt.Errorf("load subscription %d of order %d: %w", order.SubscriptionID, orderID, err)
	}

	if sub == nil {
		logger.Warn("stars invoice back: order subscription not found; expire skipped",
			zap.Uint("order_id", orderID),
			zap.Uint("subscription_id", order.SubscriptionID))

		return false, nil
	}

	if sub.TelegramID != telegramID {
		logger.Warn("stars invoice back: order ownership mismatch; expire skipped",
			zap.Uint("order_id", orderID),
			zap.Uint("subscription_id", sub.ID),
			zap.Int64("owner_telegram_id", sub.TelegramID),
			zap.Int64("pressed_by", telegramID))

		return false, nil
	}

	return true, nil
}
