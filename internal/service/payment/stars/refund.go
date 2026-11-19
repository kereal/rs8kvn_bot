package stars

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ErrAlreadyRefunded — Telegram сообщил, что платёж уже возвращён.
// Идемпотентность возврата: errors.Is(err, ErrAlreadyRefunded) == true.
var ErrAlreadyRefunded = errors.New("stars payment already refunded")

// Refund выполняет полный возврат Stars-платежа (сумму refundStarPayment
// не принимает). ctx не прерывает HTTP-запрос (tgbotapi v5.5.1 не строит
// request с контекстом) — ограничение даёт таймаут payment-клиента.
func (c *Client) Refund(ctx context.Context, telegramID int64, chargeID string) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("stars refund %s: %w", chargeID, err)
	}

	resp, err := c.api.MakeRequest("refundStarPayment", tgbotapi.Params{
		"user_id":                    strconv.FormatInt(telegramID, 10),
		"telegram_payment_charge_id": chargeID,
	})
	if err != nil {
		if isAlreadyRefunded(resp, err) {
			return fmt.Errorf("%w: %s", ErrAlreadyRefunded, chargeID)
		}
		return fmt.Errorf("stars refund %s (user %d): %w", chargeID, telegramID, err)
	}
	return nil
}

// isAlreadyRefunded детектит «уже возвращён» по описанию ошибки Telegram
// (MakeRequest при ok=false возвращает и ответ, и ошибку с Description в Message).
func isAlreadyRefunded(resp *tgbotapi.APIResponse, err error) bool {
	for _, needle := range []string{"already refunded", "already_refunded"} {
		if err != nil && strings.Contains(strings.ToLower(err.Error()), needle) {
			return true
		}
		if resp != nil && strings.Contains(strings.ToLower(resp.Description), needle) {
			return true
		}
	}
	return false
}
