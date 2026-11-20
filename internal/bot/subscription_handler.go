package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	stars "github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SubscriptionHandler groups subscription-related business logic.
type SubscriptionHandler struct {
	h *Handler // parent
}

// NewSubscriptionHandler creates a new SubscriptionHandler.
func NewSubscriptionHandler(parent *Handler) *SubscriptionHandler {
	return &SubscriptionHandler{h: parent}
}

// getSubscriptionWithCache retrieves a subscription using cache first, then DB.
func (sh *SubscriptionHandler) getSubscriptionWithCache(ctx context.Context, chatID int64) (*database.Subscription, error) {
	// Try cache first
	if sub := sh.h.cache.Get(chatID); sub != nil {
		if sub.Status == string(database.SubscriptionStatusActive) {
			return sub, nil
		}
		// Stale cache entry (non-active) — invalidate and fall through
		sh.h.invalidateCache(ctx, chatID)
	}

	// Cache miss, query database
	sub, err := sh.h.db.GetByTelegramID(ctx, chatID)
	if err != nil {
		return nil, err
	}

	// Store in cache only if active
	if sub != nil && sub.Status == string(database.SubscriptionStatusActive) {
		sh.h.cache.Set(chatID, sub)
	}

	return sub, nil
}

// handleCreateSubscription handles the "create_subscription" callback or deep link flow.
func (sh *SubscriptionHandler) handleCreateSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting subscription", zap.String("username", username), zap.Int64("chat_id", chatID))

	// Prevent duplicate creation
	if _, loaded := sh.h.inProgressSyncMap.LoadOrStore(chatID, true); loaded {
		logger.Info("Subscription creation already in progress", zap.Int64("chat_id", chatID))
		return nil
	}
	defer sh.h.inProgressSyncMap.Delete(chatID)

	sub, err := sh.getSubscriptionWithCache(ctx, chatID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Info("No existing subscription, creating new one", zap.String("username", username), zap.Int64("chat_id", chatID))
		} else {
			logger.Error("Failed to check subscription", zap.Error(err))

			errMsg := msg(MsgSubTempError)
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
			sh.h.safeSend(editMsg)

			return fmt.Errorf("check subscription: %w", err)
		}
	} else if sub != nil {
		trafficLimit := 0
		if sh.h.subscriptionService != nil {
			trafficLimit = sh.h.subscriptionService.PlanTrafficLimitGB(ctx, chatID)
		}

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubCreatedSuccess, trafficLimit, sh.h.cfg.SubURL(sub.SubscriptionID)))
		editMsg.ParseMode = "Markdown"
		editMsg.DisableWebPagePreview = true
		kb := sh.h.getQRKeyboard()
		editMsg.ReplyMarkup = &kb
		sh.h.safeSend(editMsg)

		return nil
	}

	// No existing, create new
	return sh.createSubscription(ctx, chatID, username, messageID)
}

// handleMySubscription displays user's subscription details.
func (sh *SubscriptionHandler) handleMySubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User checking subscription status", zap.String("username", username), zap.Int64("chat_id", chatID))

	messageID = sh.h.showLoadingMessage(chatID, messageID)
	if messageID == 0 {
		return nil
	}

	sub, traffic, err := sh.h.subscriptionService.GetWithTraffic(ctx, chatID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
			sh.h.safeSend(editMsg)

			return nil
		}

		logger.Error("Failed to get subscription", zap.Error(err))

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubTempError))
		sh.h.safeSend(editMsg)

		return fmt.Errorf("get subscription: %w", err)
	}

	if sub.Status != string(database.SubscriptionStatusActive) {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
		sh.h.safeSend(editMsg)

		return nil
	}

	statusText := sub.Status
	if statusText == string(database.SubscriptionStatusActive) {
		statusText = "активна"
	}

	messageText := service.FormatSubscriptionMessage(
		"📋 *Ваша подписка*",
		statusText,
		traffic,
		service.SubscriptionURL(sh.h.cfg, sub.SubscriptionID),
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, messageText)
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	kb := sh.h.getQRKeyboard()
	editMsg.ReplyMarkup = &kb
	sh.h.safeSend(editMsg)

	return nil
}

// handleQRCode generates and sends QR code for subscription.
func (sh *SubscriptionHandler) handleQRCode(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User requesting QR code", zap.String("username", username), zap.Int64("chat_id", chatID))

	sub, err := sh.h.db.GetByTelegramID(ctx, chatID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			// No active subscription — user-friendly message
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
			sh.h.safeSend(editMsg)

			return nil
		}

		logger.Error("Failed to get subscription for QR", zap.Error(err), zap.Int64("chat_id", chatID))

		return fmt.Errorf("get subscription: %w", err)
	}

	if sub == nil {
		// Safety net: sub nil with no error
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
		sh.h.safeSend(editMsg)

		return nil
	}

	pngBytes, err := utils.GenerateQRCodePNG(sh.h.cfg.SubURL(sub.SubscriptionID))
	if err != nil {
		logger.Error("Failed to generate QR code", zap.Error(err))

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgQRCodeFailed))
		sh.h.safeSend(editMsg)

		return fmt.Errorf("generate QR code: %w", err)
	}

	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  "qr.png",
		Bytes: pngBytes,
	})
	photo.Caption = msg(MsgQRCodeCaption)
	photo.ParseMode = "Markdown"

	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ "+msg(MsgQRCodeBack), "back_to_subscription"),
		),
	)
	photo.ReplyMarkup = &backKeyboard

	_, err = sh.h.bot.Send(photo)
	if err != nil {
		logger.Error("Failed to send QR photo", zap.Error(err))
		return fmt.Errorf("send QR photo: %w", err)
	}

	// NAVIGATION CONTRACT (shared by QR / invite / any "own message" screen):
	//   - send the screen as a SEPARATE message (photo here); do NOT delete the
	//     underlying card/menu (messageID) — it must stay open underneath.
	//   - the Back button is attached to THIS message, so the back callback's
	//     messageID IS this message's id. Never "fix" a missing card by
	//     re-sending it here: that spawns a stray message. See AGENTS.md
	//     "Back-button navigation" and TestNavigation_OpenAndBack.
	return nil
}

// handleBackToSubscription deletes the screen message and nothing else.
//
// NAVIGATION CONTRACT: the Back button is attached to the screen message
// itself, so the callback's messageID IS that message's id — just delete it.
// Do NOT re-show the underlying card/menu here: it is already open underneath,
// and re-sending it spawns a duplicate/stray message (this broke once — see
// AGENTS.md "Back-button navigation" and TestNavigation_OpenAndBack).
func (sh *SubscriptionHandler) handleBackToSubscription(_ context.Context, chatID int64, _ string, messageID int) error {
	logger.Info("User closing QR code", zap.Int64("chat_id", chatID))

	deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)

	_, err := sh.h.bot.Request(deleteMsg)
	if err != nil {
		logger.Warn("Failed to delete QR message", zap.Error(err), zap.Int("target_msg_id", messageID))
	}

	return nil
}

// sendQRCode generates and sends a QR code for the given URL.
func (sh *SubscriptionHandler) sendQRCode(ctx context.Context, chatID int64, messageID int, url string, caption string) error {
	pngBytes, err := utils.GenerateQRCodePNG(url)
	if err != nil {
		logger.Error("Failed to generate QR code", zap.Error(err))

		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgQRCodeFailed))
		sh.h.safeSend(editMsg)

		return fmt.Errorf("generate QR code: %w", err)
	}

	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  "qr.png",
		Bytes: pngBytes,
	})
	photo.Caption = caption
	photo.ParseMode = "Markdown"

	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ "+msg(MsgQRCodeBack), "back_to_invite"),
		),
	)
	photo.ReplyMarkup = &backKeyboard

	_, err = sh.h.bot.Send(photo)
	if err != nil {
		logger.Error("Failed to send QR photo", zap.Error(err))
		return fmt.Errorf("send QR photo: %w", err)
	}

	return nil
}

// handleBackToInvite deletes the QR photo and returns to invite.
func (sh *SubscriptionHandler) handleBackToInvite(_ context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User closing QR code", zap.String("username", username), zap.Int64("chat_id", chatID))

	deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)

	_, err := sh.h.bot.Request(deleteMsg)
	if err != nil {
		logger.Warn("Failed to delete QR message", zap.Error(err))
	}

	return nil
}

// createSubscription creates a new subscription (atomic with rollback).
func (sh *SubscriptionHandler) createSubscription(ctx context.Context, chatID int64, username string, messageID int) error {
	messageID = sh.h.showLoadingMessage(chatID, messageID)
	if messageID == 0 {
		// Could not show loading; treat as error?
		return fmt.Errorf("failed to show loading message")
	}

	// Capture the pending invite code under a brief lock, then release it
	// before the (potentially slow) HTTP call into the XUI panel.
	sh.h.pendingMu.Lock()

	var inviteCode string
	if p, ok := sh.h.pendingInvites[chatID]; ok && time.Now().Before(p.expiresAt) {
		inviteCode = p.code
	}
	sh.h.pendingMu.Unlock()

	result, err := sh.h.subscriptionService.Create(ctx, chatID, username, inviteCode)
	if err != nil {
		sh.handleCreateError(ctx, chatID, messageID, username, err)
		return fmt.Errorf("create subscription: %w", err)
	}

	// Consume the pending invite (best-effort). Always clear, regardless of
	// whether the invite resolved to a referrer.
	sh.h.pendingMu.Lock()
	delete(sh.h.pendingInvites, chatID)
	sh.h.pendingMu.Unlock()

	if result.ReferrerTGID > 0 {
		sh.h.IncrementReferralCount(result.ReferrerTGID)
	}

	sh.h.cache.Set(chatID, result.Subscription)

	err = sh.h.notifyAdmin(ctx, username, chatID, result.SubscriptionURL)
	if err != nil {
		logger.Warn("Failed to notify admin of new subscription", zap.Error(err))
	}

	backKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)

	trafficLimit := 0
	if sh.h.subscriptionService != nil {
		trafficLimit = sh.h.subscriptionService.PlanTrafficLimitGB(ctx, result.Subscription.TelegramID)
	}

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, sh.h.getHelpText(trafficLimit, result.SubscriptionURL))
	editMsg.ParseMode = "Markdown"
	editMsg.DisableWebPagePreview = true
	editMsg.ReplyMarkup = &backKeyboard
	sh.h.safeSend(editMsg)

	logger.Info("Subscription created successfully",
		zap.String("username", username),
		zap.Int64("chat_id", chatID))

	return nil
}

// handleCreateError handles errors from createSubscription.
func (sh *SubscriptionHandler) handleCreateError(ctx context.Context, chatID int64, messageID int, username string, err error) {
	logger.Error("Failed to create subscription", zap.Error(err))

	classified := classifyXUIError(err)

	errMsg := msg(MsgErrGeneric)

	switch {
	case errors.Is(classified, ErrXUIConnection):
		errMsg = msg(MsgErrConnection)
	case errors.Is(classified, ErrXUIAuth):
		errMsg = msg(MsgErrAuth)
	case errors.Is(classified, ErrXUIContextCanceled):
		errMsg = msg(MsgErrRequestCanceled)
	case errors.Is(classified, ErrXUIDNS):
		errMsg = msg(MsgErrDialTCP)
	case errors.Is(classified, ErrXUITLS):
		errMsg = msg(MsgErrTLS)
	case errors.Is(classified, ErrXUIServer):
		errMsg = msg(MsgErrInboundNotFound)
	case errors.Is(classified, ErrXUIRollbackFailed):
		errMsg = msg(MsgErrPartialSave)

		sh.h.notifyAdminError(ctx, fmt.Sprintf("⚠️ ORPHAN CLIENT WARNING: %v", err))
	}

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, errMsg)
	editMsg.DisableWebPagePreview = true
	sh.h.safeSend(editMsg)
}

// handleBuyPremiumList renders the product list for the payment flow.
func (sh *SubscriptionHandler) handleBuyPremiumList(ctx context.Context, chatID int64, username string, messageID int) error {
	logger.Info("Payment menu opened",
		zap.Int64("chat_id", chatID),
		zap.String("username", username))

	if sh.h.orderService == nil || !sh.h.paymentEnabled {
		return sh.showBuyError(chatID, messageID, "❌ Платежи временно недоступны")
	}

	products, err := sh.h.db.ListActiveProducts(ctx)
	if err != nil {
		logger.Warn("list active products failed", zap.Error(err))
		return sh.showBuyError(chatID, messageID, msg(MsgSubTempError))
	}

	// M1: при активном провайдере Stars в списке только XTR-товары — RUB-тариф
	// виден иначе, но покупка упирается в fail-closed ошибку сервиса.
	products = sh.filterProductsByProvider(products)

	if len(products) == 0 {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Нет доступных тарифов")
		kb := sh.h.keyboards.Back()
		editMsg.ReplyMarkup = &kb
		sh.h.safeSend(editMsg)

		return nil
	}

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgPremiumBenefits))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	keyboard := sh.h.keyboards.BuyProductList(products)
	editMsg.ReplyMarkup = &keyboard
	sh.h.safeSend(editMsg)

	return nil
}

// handleBuyProduct creates a Platega payment link for a specific product.
// telegramID — Telegram user ID покупателя: заказ и invoice привязываются к нему.
// chatID — чат, в котором редактируется карточка тарифа.
func (sh *SubscriptionHandler) handleBuyProduct(ctx context.Context, chatID, telegramID int64, username string, messageID int, productID uint) error {
	if sh.h.orderService == nil || !sh.h.paymentEnabled {
		return sh.showBuyError(chatID, messageID, "❌ Платежи временно недоступны")
	}

	product, err := sh.h.db.GetProductByID(ctx, productID)
	if err != nil {
		logger.Warn("load product failed", zap.Uint("product_id", productID), zap.Error(err))
		return sh.showBuyError(chatID, messageID, msg(MsgSubTempError))
	}

	if product == nil || !product.IsActive || product.PriceCents <= 0 {
		return sh.showBuyError(chatID, messageID, "❌ Тариф недоступен")
	}

	logger.Info("Payment purchase requested",
		zap.Int64("chat_id", chatID),
		zap.String("username", username),
		zap.Uint("product_id", productID),
		zap.String("product_name", product.Name),
		zap.Int64("amount_cents", product.PriceCents),
		zap.String("currency", product.Currency))

	if sh.isStarsProvider() {
		return sh.handleStarsBuy(ctx, chatID, telegramID, username, messageID, product)
	}

	info, _, err := sh.h.orderService.RequestPayment(ctx, telegramID, username, product)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPaymentDisabled):
			return sh.showBuyError(chatID, messageID, "❌ Платежи временно недоступны")
		case errors.Is(err, service.ErrPaymentAlreadyInProgress):
			return sh.showBuyError(chatID, messageID, msg(MsgPaymentInProgress))
		case errors.Is(err, service.ErrPaymentCreationUncertain):
			return sh.showBuyError(chatID, messageID, msg(MsgPaymentNeedsReview))
		}

		logger.Warn("request payment failed",
			zap.Uint("product_id", productID),
			zap.Int64("chat_id", chatID),
			zap.Error(err))

		return sh.showBuyError(chatID, messageID, msg(MsgSubTempError))
	}

	text := fmt.Sprintf("Тариф: 💎 *%s*\n\nСтоимость: *%s*\n\nПосле оплаты тариф активируется автоматически.\nЕсли тариф уже активен, новые дни прибавятся к текущему сроку.\n\n_Платёжная система может дополнительно взимать комиссию._",
		utils.EscapeMarkdown(product.Name), utils.FormatPriceCents(product.PriceCents, product.Currency))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	keyboard := sh.h.keyboards.BuyProductConfirm(product, info.URL)
	editMsg.ReplyMarkup = &keyboard
	sh.h.safeSend(editMsg)

	return nil
}

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

// showBuyError renders an error message and a back button.
func (sh *SubscriptionHandler) showBuyError(chatID int64, messageID int, text string) error {
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	kb := sh.h.keyboards.Back()
	editMsg.ReplyMarkup = &kb
	sh.h.safeSend(editMsg)

	return nil
}
