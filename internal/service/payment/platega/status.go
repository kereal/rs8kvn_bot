package platega

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// TransactionStatusResponse содержит детали транзакции, возвращаемые
// GET /transaction/{id} («Проверка статуса оплаты платежа»). Имена полей
// сохранены как в API провайдера, включая их опечатки (например, mechantId).
// Amount/Commission парсятся как json.Number, чтобы сохранить точное
// десятичное представление провайдера.
type TransactionStatusResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	PaymentDetails struct {
		Amount   json.Number `json:"amount"`
		Currency string      `json:"currency"`
	} `json:"paymentDetails"`
	MerchantName      string      `json:"merchantName"`
	MerchantID        string      `json:"mechantId"`
	Commission        json.Number `json:"comission"` //nolint:misspell // имя поля в API провайдера
	PaymentMethod     string      `json:"paymentMethod"`
	ExpiresIn         string      `json:"expiresIn"`
	Return            string      `json:"return"`
	CommissionUsdt    json.Number `json:"comissionUsdt"`
	AmountUsdt        json.Number `json:"amountUsdt"`
	QR                string      `json:"qr"`
	PayformSuccessURL string      `json:"payformSuccessUrl"`
	Payload           string      `json:"payload"`
	ExternalID        string      `json:"externalId"`
	Description       string      `json:"description"`
}

// GetTransactionStatus запрашивает статус и детали транзакции у провайдера.
// Используется как best-effort источник комиссии: колбэк её не содержит.
func (c *Client) GetTransactionStatus(ctx context.Context, transactionID uuid.UUID) (*TransactionStatusResponse, error) {
	if transactionID == uuid.Nil {
		return nil, errors.New("transaction ID is required")
	}

	statusURL := fmt.Sprintf("%s/transaction/%s", strings.TrimRight(c.cfg.BaseURL, "/"), transactionID.String())

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create status request: %w", ErrProvider, err)
	}

	httpReq.Header.Set("X-Merchantid", c.cfg.MerchantID)
	httpReq.Header.Set("X-Secret", c.cfg.Secret)

	resp, err := c.rejectingClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: send status request: %w", ErrProvider, err)
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, 1<<20)

	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(limited)

		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("%w: %s", ErrAuth, strings.TrimSpace(string(message)))
		case http.StatusNotFound:
			return nil, fmt.Errorf("%w: %s", ErrTransactionNotFound, transactionID.String())
		default:
			return nil, fmt.Errorf("%w: %s: %s", ErrProvider, resp.Status, strings.TrimSpace(string(message)))
		}
	}

	var result TransactionStatusResponse

	err = json.NewDecoder(limited).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("%w: decode transaction status: %w", ErrProvider, err)
	}

	return &result, nil
}

// CommissionCents возвращает комиссию провайдера в копейках валюты транзакции.
// Провайдер может отдавать комиссию с произвольной десятичной точностью,
// поэтому значение округляется к ближайшей копейке (half-up) точной
// десятичной арифметикой — без промежуточного float, который искажает такие
// значения, как 1.005. Результат явно проверяется на переполнение int64.
func (r *TransactionStatusResponse) CommissionCents() (int64, error) {
	raw := strings.TrimSpace(r.Commission.String())
	if raw == "" {
		return 0, errors.New("provider commission is empty")
	}
	if strings.HasPrefix(raw, "-") || strings.HasPrefix(raw, "+") {
		return 0, errors.New("provider commission must be non-negative")
	}
	if strings.ContainsAny(raw, "eE") {
		return 0, errors.New("provider commission must use fixed-point notation")
	}

	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("provider commission is invalid")
	}

	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}

	for _, ch := range parts[0] + fraction {
		if ch < '0' || ch > '9' {
			return 0, errors.New("provider commission is invalid")
		}
	}

	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, errors.New("provider commission is out of range")
	}

	cents := int64(0)
	if len(fraction) >= 1 {
		cents += int64(fraction[0]-'0') * 10
	}
	if len(fraction) >= 2 {
		cents += int64(fraction[1]-'0')
	}

	// Half-up округление: решает старший отбрасываемый знак (третий после
	// запятой); более дальние знаки не могут изменить результат.
	roundUp := len(fraction) >= 3 && fraction[2] >= '5'

	if whole > (math.MaxInt64-cents)/100 {
		return 0, errors.New("provider commission is out of range")
	}

	total := whole*100 + cents
	if roundUp {
		if total == math.MaxInt64 {
			return 0, errors.New("provider commission is out of range")
		}
		total++
	}

	return total, nil
}
