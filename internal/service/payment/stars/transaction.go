package stars

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// StarTransactionUser — участник транзакции (пользователь Telegram).
type StarTransactionUser struct {
	ID int64 `json:"id"`
}

// StarTransactionPartner — партнёр транзакции. Для входящих invoice_payment
// transaction_type == "invoice_payment", payload счёта и пользователь — в source.
type StarTransactionPartner struct {
	Type            string               `json:"type"`
	TransactionType string               `json:"transaction_type"`
	InvoicePayload  string               `json:"invoice_payload"`
	User            *StarTransactionUser `json:"user"`
}

// StarTransaction — одна запись из getStarTransactions.
type StarTransaction struct {
	ID       string                  `json:"id"`
	Amount   int64                   `json:"amount"`
	Source   *StarTransactionPartner `json:"source"`
	Receiver *StarTransactionPartner `json:"receiver"`
}

// IsIncomingInvoicePayment сообщает, является ли транзакция входящей оплатой
// Stars-invoice с привязанным payload и пользователем.
func (t StarTransaction) IsIncomingInvoicePayment() bool {
	return t.Source != nil && t.Source.Type == "user" &&
		t.Source.TransactionType == "invoice_payment" &&
		t.Source.InvoicePayload != "" && t.Source.User != nil
}

// StarTransactions — ответ getStarTransactions.
type StarTransactions struct {
	Transactions []StarTransaction `json:"transactions"`
}

// GetTransactions запрашивает историю Star-транзакций начиная с offset
// (0 — с начала). ctx не прерывает HTTP-запрос (tgbotapi v5.5.1 не строит
// request с контекстом) — ограничение даёт таймаут payment-клиента.
func (c *Client) GetTransactions(ctx context.Context, offset int64, limit int) (*StarTransactions, error) {
	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("stars getStarTransactions: %w", err)
	}

	params := tgbotapi.Params{}
	if offset != 0 {
		params["offset"] = strconv.FormatInt(offset, 10)
	}
	if limit > 0 {
		params["limit"] = strconv.Itoa(limit)
	}

	resp, err := c.api.MakeRequest("getStarTransactions", params)
	if err != nil {
		return nil, fmt.Errorf("stars getStarTransactions: %w", err)
	}

	var result StarTransactions
	unmarshalErr := json.Unmarshal(resp.Result, &result)
	if unmarshalErr != nil {
		return nil, fmt.Errorf("stars getStarTransactions: parse response: %w", unmarshalErr)
	}

	return &result, nil
}
