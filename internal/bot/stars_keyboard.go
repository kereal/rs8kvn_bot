package bot

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// StarsTermsAccept builds the keyboard of the Terms-acceptance message: the
// only button that leads to an invoice, keyed by order/product/card IDs.
func (kb *KeyboardBuilder) StarsTermsAccept(orderID, productID uint, cardChatID int64, cardMsgID uint) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"✅ Принимаю условия и перехожу к оплате",
				fmt.Sprintf("stars_accept_%d_%d_%d_%d", orderID, productID, cardChatID, cardMsgID)),
		),
	)
}

// StarsInvoiceBack builds the back keyboard of the buyer's private «счёт
// отправлен» message (never the shared tariff card): it carries the buyer's
// Telegram id plus the invoice message id so the back callback deletes the
// invoice in the buyer's private chat only when the presser IS the buyer (§5.1),
// and the pending order id so «Назад» expires the order instead of leaving it
// alive until TTL (S4). The former product id field was unused and removed (N3).
func (kb *KeyboardBuilder) StarsInvoiceBack(buyerTelegramID int64, invoiceMessageID int, orderID uint) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад",
				fmt.Sprintf("stars_invoice_back_%d_%d_%d", buyerTelegramID, invoiceMessageID, orderID)),
		),
	)
}
