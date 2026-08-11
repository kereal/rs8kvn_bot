// Package bot contains Telegram handlers, callbacks, keyboards, and user flows.
package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
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
		if sub.Status == "active" {
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
	if sub != nil && sub.Status == "active" {
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

	if sub.Status != "active" {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, msg(MsgSubNoActive))
		sh.h.safeSend(editMsg)
		return nil
	}

	statusText := sub.Status
	if statusText == "active" {
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

	if _, err := sh.h.bot.Send(photo); err != nil {
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
	if _, err := sh.h.bot.Request(deleteMsg); err != nil {
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

	if _, err := sh.h.bot.Send(photo); err != nil {
		logger.Error("Failed to send QR photo", zap.Error(err))
		return fmt.Errorf("send QR photo: %w", err)
	}
	return nil
}

// handleBackToInvite deletes the QR photo and returns to invite.
func (sh *SubscriptionHandler) handleBackToInvite(_ context.Context, chatID int64, username string, messageID int) error {
	logger.Info("User closing QR code", zap.String("username", username), zap.Int64("chat_id", chatID))
	deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
	if _, err := sh.h.bot.Request(deleteMsg); err != nil {
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
	if err := sh.h.notifyAdmin(ctx, username, chatID, result.SubscriptionURL); err != nil {
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
	if len(products) == 0 {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Нет доступных тарифов")
		kb := sh.h.keyboards.Back()
		editMsg.ReplyMarkup = &kb
		sh.h.safeSend(editMsg)
		return nil
	}
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, "Выберите тариф для оплаты:")
	keyboard := sh.h.keyboards.BuyProductList(products)
	editMsg.ReplyMarkup = &keyboard
	sh.h.safeSend(editMsg)
	return nil
}

// handleBuyProduct creates a Platega payment link for a specific product.
func (sh *SubscriptionHandler) handleBuyProduct(ctx context.Context, chatID int64, username string, messageID int, productID uint) error {
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
	info, _, err := sh.h.orderService.RequestPayment(ctx, chatID, username, product)
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
	text := fmt.Sprintf("Тариф: 💎 *%s*\n\nСтоимость: *%d₽*\n\nПосле оплаты тариф активируется автоматически.\nЕсли тариф уже активен, новые дни прибавятся к текущему сроку.\n\n_Платёжная система может дополнительно взимать комиссию._",
		utils.EscapeMarkdown(product.Name), product.PriceCents/100)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	keyboard := sh.h.keyboards.BuyProductConfirm(product, info.URL)
	editMsg.ReplyMarkup = &keyboard
	sh.h.safeSend(editMsg)
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
