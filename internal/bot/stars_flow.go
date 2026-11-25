package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/service"
	stars "github.com/kereal/rs8kvn_bot/internal/service/payment/stars"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Stars-flow: выбор тарифа, согласие с условиями и выставление счёта.
// Вынесено из subscription_handler.go, чтобы файл подписок не содержал
// платёжный код конкретного провайдера.

// starsSentInvoiceSet — bounded set успешно отправленных Stars-invoice order IDs:
// «протухший» replay acceptance-кнопки после успеха не создаёт второй invoice.
// Кольцевой буфер на max элементов: переполнение вытесняет самый старый id,
// память ограничена. In-flight параллельные отправки guard-ит starsInvoiceInFlight.
type starsSentInvoiceSet struct {
	mu   sync.Mutex
	ring []uint
	pos  int
	set  map[uint]struct{}
}

const starsSentInvoicesMax = 1024

func newStarsSentInvoiceSet(max int) *starsSentInvoiceSet {
	return &starsSentInvoiceSet{
		ring: make([]uint, max),
		set:  make(map[uint]struct{}, max),
	}
}

// Has reports whether the order invoice was already sent successfully.
func (s *starsSentInvoiceSet) Has(orderID uint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.set[orderID]

	return ok
}

// Add records the order invoice as sent, evicting the oldest entry when full.
func (s *starsSentInvoiceSet) Add(orderID uint) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if old := s.ring[s.pos]; old != 0 {
		delete(s.set, old)
	}

	s.ring[s.pos] = orderID
	s.pos = (s.pos + 1) % len(s.ring)
	s.set[orderID] = struct{}{}
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

// starsPendingMessage подбирает текст под фактическое состояние pending-заказа:
// invoice уже отправлен (acceptance прошёл) — «нажмите на счёт», иначе
// покупатель ещё не подтвердил условия — «подтвердите условия в личном чате».
func (sh *SubscriptionHandler) starsPendingMessage(order *database.Order) string {
	if order != nil && sh.h.starsSentInvoices.Has(order.ID) {
		return msg(MsgPaymentInProgressStars)
	}

	return msg(MsgPaymentAwaitingAcceptance)
}

// handleStarsBuy: первый выбор Stars-тарифа создаёт pending-заказ и отправляет
// покупателю в личный чат сообщение с условиями и кнопкой согласия (§5.1).
// Invoice отправляется ТОЛЬКО из acceptance-callback (stars_accept_...).
func (sh *SubscriptionHandler) handleStarsBuy(ctx context.Context, chatID, telegramID int64, username string, messageID int, product *database.Product) error {
	order, created, err := sh.requestStarsOrder(ctx, telegramID, username, product)
	if err != nil {
		if errors.Is(err, service.ErrPaymentDisabled) {
			return sh.showBuyError(chatID, messageID, "❌ Платежи временно недоступны")
		}

		return sh.showBuyError(chatID, messageID, msg(MsgSubTempError))
	}

	if !created {
		// Повторный клик по уже существующему pending: текст зависит от того,
		// отправлен ли уже invoice (acceptance прошёл) или покупатель ещё не
		// подтвердил условия.
		return sh.showBuyError(chatID, messageID, sh.starsPendingMessage(order))
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

	_, sendErr := sh.h.sendViaPaymentAPI(acceptMsg)
	if sendErr != nil {
		logger.Warn("failed to send stars terms acceptance message",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", telegramID),
			zap.Error(sendErr))

		// Заказ остался pending; освобождаем, чтобы retry создал новый.
		sh.expireStarsPending(ctx, order.ID, telegramID)

		return sh.showBuyError(chatID, messageID, msg(MsgInvoiceSendFailed))
	}

	logger.Info("Stars terms acceptance requested",
		zap.Uint("order_id", order.ID),
		zap.Int64("chat_id", chatID),
		zap.Int64("telegram_id", telegramID),
		zap.String("username", username))

	return nil
}

// expireStarsPending истекает pending-заказ Stars, чтобы покупатель мог
// повторить покупку (in-memory dedupe-set не должен блокировать новый invoice).
// Best-effort: сбой истечения не роняет бизнес-операцию, но репортится
// админу через issue-событие.
func (sh *SubscriptionHandler) expireStarsPending(ctx context.Context, orderID uint, telegramID int64) {
	_, expireErr := sh.h.db.ExpirePendingOrder(ctx, orderID)
	if expireErr == nil {
		return
	}

	logger.Warn("failed to expire pending stars order",
		zap.Uint("order_id", orderID), zap.Error(expireErr))

	if sh.h.orderService == nil {
		return
	}

	sh.h.orderService.NotifyPaymentIssue(ctx, service.PaymentIssue{
		Event:      "stars_pending_order_expire_failed",
		Reason:     expireErr.Error(),
		Action:     "expire pending order " + fmt.Sprint(orderID) + " manually",
		OrderID:    orderID,
		TelegramID: telegramID,
	})
}

// handleStarsAccept обрабатывает acceptance-callback stars_accept_<order>_<product>_<card>:
// загружает pending-заказ напрямую по ID (RequestPayment в callback НЕ вызывается —
// expired заказ не должен порождать replacement pending), валидирует провайдера,
// принадлежность заказа (товар, владелец) и статус, затем товар/план, и только
// теперь отправляет invoice по telegram_id. Побочные эффекты (expireStarsPending)
// возможны лишь после подтверждения принадлежности заказа.
// Дубликат не создаёт: sent-set после успеха + in-flight guard на параллельные
// callback'и; кнопка согласия снимается редактированием сообщения.
func (sh *SubscriptionHandler) handleStarsAccept(ctx context.Context, chatID, cardChatID, buyerTelegramID int64, username string, acceptMsgID, cardMsgID int, expectedOrderID uint, productID uint) error {
	if sh.h.orderService == nil || !sh.h.paymentEnabled {
		return sh.showBuyError(chatID, acceptMsgID, "❌ Платежи временно недоступны")
	}

	// cardChatID (карточка тарифа) и chatID (чат, где нажата кнопка согласия)
	// легально различаются: карточка публикуется в группе/канале, а кнопка
	// согласия приходит в личку покупателя. Строгое равенство сломало бы
	// покупку из группового чата. Защита от подделки callback-data здесь
	// избыточна: Telegram разрешает боту редактировать только собственные
	// сообщения, а покупатель и заказ проверяются ниже по order.ID и
	// subscription владельца.
	//
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

	// Order-first: загружаем заказ и подтверждаем принадлежность (провайдер,
	// товар, владелец) ДО любых побочных эффектов. Иначе подделанный callback
	// с чужим order_id истёк бы чужой pending-заказ через expireStarsPending
	// в ветках проверки товара/плана ниже.
	order, err := sh.h.db.GetOrderByID(ctx, expectedOrderID)
	if err != nil || order == nil {
		logger.Warn("stars acceptance order load failed",
			zap.Uint("order_id", expectedOrderID),
			zap.Error(err))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	// Валидация заказа: Stars, тот же продукт.
	if order.PaymentProvider != database.PaymentProviderTelegramStars ||
		order.ProductID != productID {
		logger.Warn("stars acceptance order mismatch",
			zap.Uint("order_id", order.ID),
			zap.Uint("expected_product_id", productID),
			zap.String("provider", order.PaymentProvider))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	// Владелец заказа — покупатель, нажавший кнопку согласия. Проверяется до
	// любых изменений заказа и до раскрытия его статуса.
	sub, err := sh.h.db.GetByTelegramID(ctx, buyerTelegramID)
	if err != nil || sub == nil || sub.ID != order.SubscriptionID {
		logger.Warn("stars acceptance buyer mismatch",
			zap.Uint("order_id", order.ID),
			zap.Uint("order_subscription_id", order.SubscriptionID),
			zap.Int64("telegram_id", buyerTelegramID),
			zap.Error(err))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	if order.Status != database.OrderStatusPending {
		// Оплачен/отменён/истёк: новый invoice не отправляем. Текст зависит от
		// фактического статуса: оплата получена — сообщаем об успехе, иначе
		// предлагаем выбрать тариф заново.
		logger.Info("stars acceptance on non-pending order ignored",
			zap.Uint("order_id", order.ID),
			zap.String("status", string(order.Status)))

		if order.Status == database.OrderStatusPaid {
			return sh.showBuyError(chatID, acceptMsgID, msg(MsgStarsAlreadyPaid))
		}

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgStarsOrderNotPending))
	}

	product, err := sh.h.db.GetProductByID(ctx, productID)
	if err != nil || product == nil {
		logger.Warn("stars acceptance product load failed",
			zap.Uint("order_id", order.ID),
			zap.Uint("product_id", productID),
			zap.Error(err))

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	// S5: товар и план могли быть деактивированы после создания pending-заказа
	// (проверки выполняются при создании, но acceptance может прийти к устаревшему
	// заказу). Перечитываем дерево перед выставлением счёта и отклоняем покупку,
	// освобождая pending-заказ для повторной попытки.
	if !product.IsActive {
		logger.Warn("stars acceptance product inactive",
			zap.Uint("order_id", order.ID),
			zap.Uint("product_id", productID))

		sh.expireStarsPending(ctx, order.ID, buyerTelegramID)

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	plan, err := sh.h.db.GetPlanByID(ctx, product.PlanID)
	if err != nil || plan == nil {
		logger.Warn("stars acceptance plan load failed",
			zap.Uint("order_id", order.ID),
			zap.Uint("product_id", productID),
			zap.Uint("plan_id", product.PlanID),
			zap.Error(err))

		sh.expireStarsPending(ctx, order.ID, buyerTelegramID)

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	if !plan.IsActive {
		logger.Warn("stars acceptance plan inactive",
			zap.Uint("order_id", order.ID),
			zap.Uint("product_id", productID),
			zap.Uint("plan_id", plan.ID))

		sh.expireStarsPending(ctx, order.ID, buyerTelegramID)

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgSubTempError))
	}

	// In-flight guard: параллельный double-tap acceptance — второй уходит.
	if _, loaded := sh.h.starsInvoiceInFlight.LoadOrStore(order.ID, struct{}{}); loaded {
		logger.Warn("concurrent stars acceptance suppressed",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", buyerTelegramID))

		return nil
	}
	defer sh.h.starsInvoiceInFlight.Delete(order.ID)

	// Sent-set проверяется ВНУТРИ слота: между проверкой и захватом параллельный
	// callback мог успеть отправить invoice. Метка ставится только после успешной
	// отправки, поэтому неудачная отправка слот освобождает и не блокирует retry.
	if sh.h.starsSentInvoices.Has(order.ID) {
		logger.Warn("duplicate stars acceptance suppressed (already invoiced)",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", buyerTelegramID))

		return nil
	}

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

	sentInvoice, err := sh.h.sendViaPaymentAPI(invoice)
	if err != nil {
		logger.Warn("failed to send stars invoice",
			zap.Uint("order_id", order.ID),
			zap.Int64("telegram_id", buyerTelegramID),
			zap.Uint("product_id", product.ID),
			zap.Error(err))

		// Заказ остался pending; освобождаем, чтобы повторная покупка создала новый.
		sh.expireStarsPending(ctx, order.ID, buyerTelegramID)

		return sh.showBuyError(chatID, acceptMsgID, msg(MsgInvoiceSendFailed))
	}

	sh.h.starsSentInvoices.Add(order.ID)

	// Успех: снимаем кнопку согласия — повторный callback больше не приходит.
	// Кнопка Back привязана к личному сообщению покупателя: cardChatID/cardMsgID
	// могут быть общей групповой карточкой, на которой персональная клавиатура
	// ссылалась бы на чужой invoice и чужой pending-заказ. Личное сообщение
	// гарантирует, что кнопка видна только владельцу счёта и ведёт на его заказ.
	acceptDone := tgbotapi.NewEditMessageText(buyerTelegramID, acceptMsgID,
		fmt.Sprintf("Тариф: 💎 *%s* — счёт отправлен ниже, нажмите «Pay».\nОплачивая счёт, вы принимаете Пользовательское соглашение (/terms).",
			utils.EscapeMarkdown(product.Name)))
	acceptDone.ParseMode = "Markdown"
	backKeyboard := sh.h.keyboards.StarsInvoiceBack(buyerTelegramID, sentInvoice.MessageID, order.ID)
	acceptDone.ReplyMarkup = &backKeyboard
	sh.h.safeSend(acceptDone)

	// Карточка тарифа остаётся общей: только текст «Счёт отправлен ниже»,
	// без персональной клавиатуры (кнопка живёт в личном чате покупателя).
	if cardMsgID > 0 {
		invoiceText := fmt.Sprintf("Тариф: 💎 *%s*\n\nСтоимость: *%s*\n\nСчёт отправлен в личном чате — нажмите «Pay» в счёте.\nПосле оплаты тариф активируется автоматически.\nЕсли тариф уже активен, новые дни прибавятся к текущему сроку.",
			utils.EscapeMarkdown(product.Name), utils.FormatStarsAmount(order.AmountCents))

		editMsg := tgbotapi.NewEditMessageText(cardChatID, cardMsgID, invoiceText)
		editMsg.ParseMode = "Markdown"
		sh.h.safeSend(editMsg)
	}

	logger.Info("Stars invoice sent after acceptance",
		zap.Uint("order_id", order.ID),
		zap.Int64("chat_id", chatID),
		zap.Int64("telegram_id", buyerTelegramID),
		zap.Int("invoice_message_id", sentInvoice.MessageID))

	return nil
}

// PreCheckoutTimeout ограничивает бюджет ответа на pre_checkout_query.
//
// Суммарный бюджет укладывается в окно Telegram 10 секунд и строго
// последовательный: 4s — валидация заказа в БД (ValidateStarsPreCheckout, одна
// операция, никаких параллельных вызовов) + до 4s — HTTP-ответ answerPreCheckout
// через payment-клиент с таймаутом 4s. Плюс ожидание слота fast-lane
// (preCheckoutSlotWaitTimeout, 1s) в cmd/bot. Итого worst case ≤9s < 10s.
//
// Экспортирован, чтобы cmd/bot мог зафиксировать инвариант «ответ укладывается в
// окно Telegram» тестом.
const PreCheckoutTimeout = 4 * time.Second

// IsFinancialUpdate сообщает, что апдейт должен пройти вне общего rate limiter:
// pre_checkout_query обрабатывается отдельной веткой выше, здесь —
// successful_payment и команды /paysupport, /terms (compliance: платёжные
// апдейты не отбрасываются лимитом). Экспортирован, чтобы event loop
// (cmd/bot) не сбрасывал финансовые апдейты при переполнении обычной очереди.
func (h *Handler) IsFinancialUpdate(update tgbotapi.Update) bool {
	if update.Message == nil {
		return false
	}

	if update.Message.SuccessfulPayment != nil {
		return true
	}

	if !update.Message.IsCommand() {
		return false
	}

	switch normalizeCommand(update.Message.Command()) {
	case "paysupport", "terms":
		return true
	default:
		return false
	}
}

// runFinancialUpdate диспетчеризует финансовый апдейт вне общего rate limiter,
// сохраняя стандартный HandlerTimeout и метрики.
func (h *Handler) runFinancialUpdate(ctx context.Context, update tgbotapi.Update) {
	ctx, cancel := h.withTimeout(ctx)
	defer cancel()

	start := time.Now()

	var (
		command string
		err     error
	)

	switch {
	case update.Message.SuccessfulPayment != nil:
		command = "successful_payment"
		err = h.handleSuccessfulPayment(ctx, update)
	case normalizeCommand(update.Message.Command()) == "paysupport":
		command = "paysupport"
		err = h.HandlePaySupport(ctx, update)
	default:
		command = "terms"
		err = h.HandleTerms(ctx, update)
	}

	metrics.BotUpdateDuration.WithLabelValues(command).Observe(time.Since(start).Seconds())

	if err != nil {
		metrics.BotUpdatesTotal.WithLabelValues(command, "error").Inc()

		return
	}

	metrics.BotUpdatesTotal.WithLabelValues(command, "success").Inc()
}

// handlePreCheckout отвечает на pre_checkout_query: валидация без побочных
// эффектов, ответ уходит ВСЕГДА (OK=false при любой ошибке), бюджет §5.2.
func (h *Handler) handlePreCheckout(ctx context.Context, update tgbotapi.Update) {
	q := update.PreCheckoutQuery

	if q.From == nil {
		// Патологический кейс (Telegram всегда присылает From), но ответ — всегда.
		logger.Warn("pre_checkout_query without From", zap.String("query_id", q.ID))
		h.answerPreCheckout(q.ID, false)

		return
	}

	if h.orderService == nil {
		logger.Warn("pre_checkout_query without order service", zap.String("query_id", q.ID))
		h.answerPreCheckout(q.ID, false)

		return
	}

	ctx, cancel := context.WithTimeout(ctx, PreCheckoutTimeout)
	defer cancel()

	err := h.orderService.ValidateStarsPreCheckout(ctx, q.InvoicePayload, q.From.ID, int64(q.TotalAmount), q.Currency)

	h.answerPreCheckout(q.ID, err == nil)

	logger.Info("Stars pre-checkout validated",
		zap.String("provider", database.PaymentProviderTelegramStars),
		zap.String("query_id", q.ID),
		zap.Int64("telegram_id", q.From.ID),
		zap.String("payload", q.InvoicePayload),
		zap.Int64("amount", int64(q.TotalAmount)),
		zap.String("currency", q.Currency),
		zap.Bool("ok", err == nil),
		zap.Error(err))
}

// answerPreCheckout отправляет ответ на pre_checkout_query через payment-инстанс.
func (h *Handler) answerPreCheckout(queryID string, ok bool) {
	answer := tgbotapi.PreCheckoutConfig{PreCheckoutQueryID: queryID, OK: ok}
	if !ok {
		answer.ErrorMessage = "Платёж не может быть принят. Обратитесь в /paysupport"
	}

	_, err := h.requestViaPaymentAPI(answer)
	if err != nil {
		logger.Warn("failed to answer pre_checkout_query",
			zap.String("query_id", queryID),
			zap.Bool("ok", ok),
			zap.Error(err))
	}
}

// handleSuccessfulPayment обрабатывает Stars successful_payment: подтверждение
// оплаты (источник истины — successful_payment), уведомление пользователю,
// разбор ошибок после списания (§5.3).
func (h *Handler) handleSuccessfulPayment(ctx context.Context, update tgbotapi.Update) error {
	payment := update.Message.SuccessfulPayment

	if update.Message.From == nil {
		logger.Warn("successful_payment without From",
			zap.String("payload", payment.InvoicePayload),
			zap.String("charge_id", payment.TelegramPaymentChargeID))

		return errors.New("successful_payment without From")
	}

	if h.orderService == nil {
		return errors.New("order service is not configured")
	}

	chatID := update.Message.Chat.ID

	confirmation, err := h.orderService.ConfirmStarsPayment(ctx,
		payment.TelegramPaymentChargeID,
		payment.InvoicePayload,
		int64(payment.TotalAmount),
		payment.Currency,
		update.Message.From.ID)
	if err != nil {
		// Деньги уже списаны: отказ пользователю + правка вручную через /refund.
		logger.Error("Stars successful_payment confirm failed",
			zap.String("provider", database.PaymentProviderTelegramStars),
			zap.String("charge_id", payment.TelegramPaymentChargeID),
			zap.String("payload", payment.InvoicePayload),
			zap.Int64("telegram_id", update.Message.From.ID),
			zap.Error(err))

		h.orderService.NotifyPaymentIssue(ctx, service.PaymentIssue{
			Event:          "stars_payment_confirm_failed",
			Reason:         err.Error(),
			Action:         "reconcile the Stars payment manually; refund via /refund when disputed",
			TelegramID:     update.Message.From.ID,
			Currency:       payment.Currency,
			AmountCents:    int64(payment.TotalAmount),
			CallbackStatus: "successful_payment",
			Payload:        payment.InvoicePayload,
			ProviderID:     payment.TelegramPaymentChargeID,
		})

		h.sendPaymentSupportNotice(ctx, chatID)

		return fmt.Errorf("confirm stars payment: %w", err)
	}

	if !confirmation.Activated {
		if confirmation.AlreadyRefunded {
			// Заказ отменён после уже выполненного refund: деньги у покупателя,
			// повторный возврат не требуется. Сообщаем именно об этом, а не о
			// «списании по отменённому заказу».
			refundNotice := tgbotapi.NewMessage(chatID,
				"↩️ Платёж по этому заказу уже возвращён: Stars зачислены обратно на ваш аккаунт. Если остались вопросы, напишите в /paysupport")
			_, sendErr := h.sendViaPaymentAPI(refundNotice)
			if sendErr != nil {
				logger.Warn("failed to notify user about already refunded payment",
					zap.Int64("chat_id", chatID), zap.Error(sendErr))
			}

			return nil
		}

		if confirmation.AutoRefunded {
			// Сервис уже вернул Stars покупателю: понятное уведомление вместо
			// canceled-order warning; service issue alert админу уходит отдельно,
			// если автовозврат не удался (тогда AutoRefunded == false).
			refundNotice := tgbotapi.NewMessage(chatID,
				"↩️ Платёж возвращён: Stars зачислены обратно на ваш аккаунт. Если остались вопросы, напишите в /paysupport")
			_, sendErr := h.sendViaPaymentAPI(refundNotice)
			if sendErr != nil {
				logger.Warn("failed to notify user about auto refund",
					zap.Int64("chat_id", chatID), zap.Error(sendErr))
			}

			return nil
		}

		if confirmation.DuplicateCharge && !confirmation.AutoRefunded {
			// Дубль оплаты, автовозврат не удался (контракт PaymentConfirmation):
			// деньги списаны дважды, вернуть автоматически не удалось — сообщаем
			// покупателю и направляем в /paysupport для ручного возврата.
			dupNotice := tgbotapi.NewMessage(chatID,
				"⚠️ Оплата задвоилась: автоматический возврат не удался. Обратитесь в /paysupport — вернём Stars вручную")
			_, sendErr := h.sendViaPaymentAPI(dupNotice)
			if sendErr != nil {
				logger.Warn("failed to notify user about duplicate charge refund failure",
					zap.Int64("chat_id", chatID), zap.Error(sendErr))
			}

			return nil
		}

		if confirmation.Order == nil {
			// Заказ ссылается на удалённый product/subscription: активировать
			// нечего — ручной разбор. Сообщение в /paysupport, не «отменённый
			// заказ» (сервис уже сообщил об этом в issue-событии).
			h.sendPaymentSupportNotice(ctx, chatID)

			return nil
		}

		if confirmation.Order.Status == database.OrderStatusPaid {
			// Повторная доставка successful_payment: идемпотентный no-op без
			// второго продления и без предупреждения об отмене.
			return nil
		}

		// Заказ canceled: деньги списаны, доступ отозван — ручная сверка.
		warning := tgbotapi.NewMessage(chatID,
			"⚠️ Оплата по отменённому заказу. Деньги списаны, доступ отозван. Обратитесь в /paysupport")
		_, sendErr := h.sendViaPaymentAPI(warning)
		if sendErr != nil {
			logger.Warn("failed to notify user about canceled-order payment",
				zap.Int64("chat_id", chatID), zap.Error(sendErr))
		}

		return nil
	}

	targetID, text, err := h.orderService.BuildPaidUserNotification(ctx, confirmation.Order)
	if err != nil {
		logger.Warn("failed to build paid notification",
			zap.String("provider", database.PaymentProviderTelegramStars),
			zap.Uint("order_id", confirmation.Order.ID),
			zap.Error(err))

		h.sendPaidNotificationFallback(ctx, "paid_notification_build_failed", err.Error(),
			confirmation.Order, chatID, payment.TelegramPaymentChargeID, payment.InvoicePayload)

		return nil
	}

	if targetID <= 0 {
		return nil
	}

	userMessage := tgbotapi.NewMessage(targetID, text)
	userMessage.ParseMode = tgbotapi.ModeMarkdown

	_, err = h.sendViaPaymentAPI(userMessage)
	if err != nil {
		logger.Warn("failed to send paid notification",
			zap.String("provider", database.PaymentProviderTelegramStars),
			zap.Uint("order_id", confirmation.Order.ID),
			zap.Int64("telegram_id", targetID),
			zap.Error(err))

		h.sendPaidNotificationFallback(ctx, "paid_notification_send_failed", err.Error(),
			confirmation.Order, targetID, payment.TelegramPaymentChargeID, payment.InvoicePayload)
	}

	return nil
}

// sendPaymentSupportNotice сообщает покупателю об оплате, которую не удалось
// подтвердить: указывает обратиться в /paysupport. Best-effort.
func (h *Handler) sendPaymentSupportNotice(ctx context.Context, chatID int64) {
	notice := tgbotapi.NewMessage(chatID,
		"✅ Оплата получена, но подтвердить заказ не удалось. Напишите в /paysupport")
	_, sendErr := h.sendViaPaymentAPI(notice)
	if sendErr != nil {
		logger.Warn("failed to notify user about confirm failure",
			zap.Int64("chat_id", chatID), zap.Error(sendErr))
	}
}

// sendPaidNotificationFallback сообщает в ops о непрошедшем уведомлении об
// оплате и отправляет покупателю статичный fallback с подсказкой /paysupport.
func (h *Handler) sendPaidNotificationFallback(ctx context.Context, event, reason string,
	order *database.Order, telegramID int64, chargeID, payload string) {
	h.orderService.NotifyPaymentIssue(ctx, service.PaymentIssue{
		Event:          event,
		Reason:         reason,
		Action:         "send the confirmed payment details to the user manually",
		OrderID:        order.ID,
		TelegramID:     telegramID,
		SubscriptionID: order.SubscriptionID,
		ProductID:      order.ProductID,
		AmountCents:    order.AmountCents,
		Currency:       order.Currency,
		ProviderID:     chargeID,
		CallbackStatus: "successful_payment",
		Payload:        payload,
	})

	fallback := tgbotapi.NewMessage(telegramID,
		"Оплата подтверждена. Напишите в /paysupport, если доступ не появился")
	_, sendErr := h.sendViaPaymentAPI(fallback)
	if sendErr != nil {
		logger.Warn("failed to send paid notification fallback",
			zap.Int64("telegram_id", telegramID), zap.Error(sendErr))
	}
}
