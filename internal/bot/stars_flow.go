package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	stars "github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Stars-flow: выбор тарифа, согласие с условиями и выставление счёта.
// Вынесено из subscription_handler.go, чтобы файл подписок не содержал
// платёжный код конкретного провайдера.

// isStarsProvider возвращает true при активном провайдере Telegram Stars.
func (sh *SubscriptionHandler) isStarsProvider() bool {
	return sh.h.cfg != nil && sh.h.cfg.PaymentProvider == database.PaymentProviderTelegramStars
}

// filterProductsByProvider фильтрует список тарифов по активному провайдеру
// симметрично (M1): при Stars показываются только XTR-товары, при Platega —
// XTR-товары исключаются. Service-side fail-closed guard для Platega живёт
// в payment core; здесь только витрина.
func (sh *SubscriptionHandler) filterProductsByProvider(products []database.Product) []database.Product {
	starsProvider := sh.isStarsProvider()

	filtered := make([]database.Product, 0, len(products))

	for _, product := range products {
		isXTR := strings.EqualFold(product.Currency, stars.Currency)

		if starsProvider != isXTR {
			continue
		}

		filtered = append(filtered, product)
	}

	return filtered
}

// requestStarsOrder получает или переиспользует pending-заказ Stars.
// При reuse (ErrPaymentAlreadyInProgress) возвращает (order, false, nil) — вызывающий
// сам решает, как трактовать повторный клик. При создании возвращает (order, true, nil).
func (sh *SubscriptionHandler) requestStarsOrder(ctx context.Context, telegramID int64, username string, product *database.Product) (*database.Order, bool, error) {
	_, order, err := sh.h.orderService.RequestPayment(ctx, telegramID, username, product)
	if err != nil && !errors.Is(err, service.ErrPaymentAlreadyInProgress) {
		switch {
		case errors.Is(err, service.ErrPaymentDisabled):
			return nil, false, err
		case errors.Is(err, service.ErrPaymentCreationUncertain):
			return nil, false, err
		}

		logger.Warn("stars request payment failed",
			zap.Uint("product_id", product.ID),
			zap.Int64("telegram_id", telegramID),
			zap.Error(err))

		return nil, false, err
	}

	if order == nil {
		return nil, false, errors.New("stars request payment returned no order")
	}

	created := err == nil // no error = newly created; ErrAlreadyInProgress = reuse

	return order, created, nil
}

// handleStarsBuy: первый выбор Stars-тарифа создаёт pending-заказ и отправляет
// покупателю в личный чат сообщение с условиями и кнопкой согласия (§5.1).
// Invoice отправляется ТОЛЬКО из acceptance-callback (stars_accept_...).
func (sh *SubscriptionHandler) handleStarsBuy(ctx context.Context, chatID, telegramID int64, username string, messageID int, product *database.Product) error {
	order, created, err := sh.requestStarsOrder(ctx, telegramID, username, product)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPaymentAlreadyInProgress):
			return sh.showBuyError(chatID, messageID, msg(MsgPaymentInProgressStars))
		case errors.Is(err, service.ErrPaymentDisabled):
			return sh.showBuyError(chatID, messageID, "❌ Платежи временно недоступны")
		}

		return sh.showBuyError(chatID, messageID, msg(MsgSubTempError))
	}

	if !created {
		// Повторный клик по уже существующему pending — Terms-сообщение уже отправлено.
		return sh.showBuyError(chatID, messageID, msg(MsgPaymentInProgressStars))
	}

	// Карточка тарифа: подтверждение уходит в личный чат покупателя.
	cardText := fmt.Sprintf("Тариф: 💎 *%s*\n\nСтоимость: *%s*\n\nПодтвердите условия оплаты в личном чате — счёт будет отправлен после согласия.",
		utils.EscapeMarkdown(product.Name), utils.FormatStarsAmount(order.AmountCents))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, cardText)
	editMsg.ParseMode = "Markdown"
	sh.h.safeSend(editMsg)

	acceptText := fmt.Sprintf("Тариф: 💎 *%s*\n\nСтоимость: *%s*\n\nНажимая «Принимаю условия», вы принимаете Пользовательское соглашение (/terms):\n· доступ к цифровому товару предоставляется сразу после оплаты;\n· возврат возможен только если доступ не был предоставлен или услуга не оказана по вине сервиса — обращение в течение 24 часов;\n· поддержка Telegram НЕ помогает с возвратом и не может отменить платёж или вернуть Stars, потраченные в этом боте — все вопросы решает только оператор бота.",
		utils.EscapeMarkdown(product.Name), utils.FormatStarsAmount(order.AmountCents))

	acceptMsg := tgbotapi.NewMessage(telegramID, acceptText)
	acceptMsg.ParseMode = "Markdown"
	acceptMsg.DisableWebPagePreview = true
	kb := sh.h.keyboards.StarsTermsAccept(order.ID, product.ID, chatID, uint(messageID))
	acceptMsg.ReplyMarkup = &kb

	_, sendErr := sh.h.paymentAPI().Send(acceptMsg)
	if sendErr != nil {
		logger.Warn("failed to send stars terms acceptance message",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", telegramID),
			zap.Error(sendErr))

		// Заказ остался pending; освобождаем, чтобы retry создал новый.
		_, expireErr := sh.h.db.ExpirePendingOrder(ctx, order.ID)
		if expireErr != nil {
			logger.Warn("failed to expire pending order after acceptance send failure",
				zap.Uint("order_id", order.ID), zap.Error(expireErr))
			sh.h.orderService.NotifyPaymentIssue(ctx, service.PaymentIssue{
				Event:      "stars_acceptance_send_failed_expire_failed",
				Reason:     sendErr.Error(),
				Action:     "expire pending order " + fmt.Sprint(order.ID) + " manually",
				OrderID:    order.ID,
				TelegramID: telegramID,
			})
		}

		return sh.showBuyError(chatID, messageID, msg(MsgInvoiceSendFailed))
	}

	logger.Info("Stars terms acceptance requested",
		zap.Uint("order_id", order.ID),
		zap.Int64("chat_id", chatID),
		zap.Int64("telegram_id", telegramID),
		zap.String("username", username))

	return nil
}

// handleStarsAccept обрабатывает acceptance-callback stars_accept_<order>_<product>_<card>:
// загружает pending-заказ напрямую по ID (RequestPayment в callback НЕ вызывается —
// expired заказ не должен порождать replacement pending), валидирует провайдера,
// статус, продукт и покупателя, и только теперь отправляет invoice по telegram_id.
// Дубликат не создаёт: sent-set после успеха + in-flight guard на параллельные
// callback'и; кнопка согласия снимается редактированием сообщения.
func (sh *SubscriptionHandler) handleStarsAccept(ctx context.Context, chatID, cardChatID, buyerTelegramID int64, username string, acceptMsgID, cardMsgID int, expectedOrderID uint, productID uint) error {
	if sh.h.orderService == nil || !sh.h.paymentEnabled {
		return sh.showBuyError(chatID, acceptMsgID, "❌ Платежи временно недоступны")
	}

	// Активный провайдер мог переключиться (Stars→Platega) после создания
	// pending-заказа: старая кнопка согласия не должна выставлять Stars-invoice.
	// Defense-in-depth к rollout-чеклисту §8.2 (переключение только после
	// закрытия settlement-окна).
	if !sh.isStarsProvider() {
		logger.Warn("stars acceptance rejected: active provider is not telegram_stars",
			zap.Uint("order_id", expectedOrderID),
			zap.Int64("telegram_id", buyerTelegramID))

		return sh.showBuyError(chatID, acceptMsgID, "❌ Платежи временно недоступны")
	}

	product, err := sh.h.db.GetProductByID(ctx, productID)
	if err != nil || product == nil {
		logger.Warn("stars acceptance product load failed",
			zap.Uint("product_id", productID),
			zap.Error(err))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	order, err := sh.h.db.GetOrderByID(ctx, expectedOrderID)
	if err != nil || order == nil {
		logger.Warn("stars acceptance order load failed",
			zap.Uint("order_id", expectedOrderID),
			zap.Error(err))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	// Валидация заказа: Stars, pending, тот же продукт, тот же покупатель.
	if order.PaymentProvider != database.PaymentProviderTelegramStars ||
		order.ProductID != productID {
		logger.Warn("stars acceptance order mismatch",
			zap.Uint("order_id", order.ID),
			zap.Uint("expected_product_id", productID),
			zap.String("provider", order.PaymentProvider))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	if order.Status != database.OrderStatusPending {
		// Оплачен/отменён/истёк: новый invoice не отправляем, покупатель
		// выбирает тариф заново после истечения.
		logger.Info("stars acceptance on non-pending order ignored",
			zap.Uint("order_id", order.ID),
			zap.String("status", string(order.Status)))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgPaymentInProgressStars))
	}

	sub, err := sh.h.db.GetByTelegramID(ctx, buyerTelegramID)
	if err != nil || sub == nil || sub.ID != order.SubscriptionID {
		logger.Warn("stars acceptance buyer mismatch",
			zap.Uint("order_id", order.ID),
			zap.Uint("order_subscription_id", order.SubscriptionID),
			zap.Int64("telegram_id", buyerTelegramID),
			zap.Error(err))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	// Sent-set: invoice по этому заказу уже отправлен успешно.
	if sh.h.starsSentInvoices.Has(order.ID) {
		logger.Warn("duplicate stars acceptance suppressed (already invoiced)",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", buyerTelegramID))

		return nil
	}

	// In-flight guard: параллельный double-tap acceptance — второй уходит.
	if _, loaded := sh.h.starsInvoiceInFlight.LoadOrStore(order.ID, struct{}{}); loaded {
		logger.Warn("concurrent stars acceptance suppressed",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", buyerTelegramID))

		return nil
	}
	defer sh.h.starsInvoiceInFlight.Delete(order.ID)

	invoice, err := stars.BuildInvoice(stars.InvoiceParams{
		OrderID:  order.ID,
		ChatID:   buyerTelegramID,
		Title:    product.Name,
		Amount:   order.AmountCents,
		Currency: stars.Currency,
	})
	if err != nil {
		logger.Error("failed to build stars invoice",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", buyerTelegramID),
			zap.Uint("product_id", product.ID),
			zap.Error(err))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	sentInvoice, err := sh.h.paymentAPI().Send(invoice)
	if err != nil {
		logger.Warn("failed to send stars invoice",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", buyerTelegramID),
			zap.Uint("product_id", product.ID),
			zap.Error(err))

		// Заказ остался pending; освобождаем, чтобы повторная покупка создала новый.
		_, expireErr := sh.h.db.ExpirePendingOrder(ctx, order.ID)
		if expireErr != nil {
			logger.Warn("failed to expire pending order after invoice send failure",
				zap.Uint("order_id", order.ID), zap.Error(expireErr))
			sh.h.orderService.NotifyPaymentIssue(ctx, service.PaymentIssue{
				Event:      "stars_invoice_send_failed_expire_failed",
				Reason:     err.Error(),
				Action:     "expire pending order " + fmt.Sprint(order.ID) + " manually",
				OrderID:    order.ID,
				TelegramID: buyerTelegramID,
			})
		}

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgInvoiceSendFailed))
	}

	sh.h.starsSentInvoices.Add(order.ID)

	// Успех: снимаем кнопку согласия — повторный callback больше не приходит.
	acceptDone := tgbotapi.NewEditMessageText(buyerTelegramID, acceptMsgID,
		fmt.Sprintf("Тариф: 💎 *%s* — счёт отправлен ниже, нажмите «Pay».\nОплачивая счёт, вы принимаете Пользовательское соглашение (/terms).",
			utils.EscapeMarkdown(product.Name)))
	acceptDone.ParseMode = "Markdown"
	sh.h.safeSend(acceptDone)

	// Карточка тарифа: «Счёт отправлен ниже», кнопка Back несёт id invoice
	// в личном чате покупателя (stars_invoice_back удаляет его по telegram_id).
	if cardMsgID > 0 {
		invoiceText := fmt.Sprintf("Тариф: 💎 *%s*\n\nСтоимость: *%s*\n\nСчёт отправлен в личном чате — нажмите «Pay» в счёте.\nПосле оплаты тариф активируется автоматически.\nЕсли тариф уже активен, новые дни прибавятся к текущему сроку.",
			utils.EscapeMarkdown(product.Name), utils.FormatStarsAmount(order.AmountCents))

		editMsg := tgbotapi.NewEditMessageText(cardChatID, cardMsgID, invoiceText)
		editMsg.ParseMode = "Markdown"
		keyboard := sh.h.keyboards.StarsInvoiceBack(buyerTelegramID, sentInvoice.MessageID, product.ID)
		editMsg.ReplyMarkup = &keyboard
		sh.h.safeSend(editMsg)
	}

	logger.Info("Stars invoice sent after acceptance",
		zap.Uint("order_id", order.ID),
		zap.Int64("chat_id", chatID),
		zap.Int64("telegram_id", buyerTelegramID),
		zap.Int("invoice_message_id", sentInvoice.MessageID))

	return nil
}
