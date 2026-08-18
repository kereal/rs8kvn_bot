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
// сохранены как в API провайдера, включая их опечатки (mechantId, comission,
// comissionUsdt). Amount/Commission парсятся как json.Number, чтобы сохранить
// точное десятичное представление провайдера.
type TransactionStatusResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	PaymentDetails struct {
		Amount   json.Number `json:"amount"`
		Currency string      `json:"currency"`
	} `json:"paymentDetails"`
	MerchantName      string      `json:"merchantName"`
	MerchantID        string      `json:"mechantId"`
	Commission        json.Number `json:"comission"`
	PaymentMethod     string      `json:"paymentMethod"`
	ExpiresIn         string      `json:"expiresIn"`
	Return            string      `json:"return"`
	CommissionUsdt    json.Number `json:"comissionUsdt"`
	AmountUsdt        json.Number `json:"amountUsdt"`
	QR                string      `json:"qr"`
	PayformSuccessURL string      `json:"payformSuccessUrl"`
	Payload           string      `json:"payload"`
	CommissionType    *int        `json:"comissionType"`
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

	resp, err := c.cfg.HTTPClient.Do(httpReq)
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
			return nil, fmt.Errorf("%w: transaction %s not found", ErrProvider, transactionID.String())
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
// поэтому значение округляется до копеек.
func (r *TransactionStatusResponse) CommissionCents() (int64, error) {
	raw := strings.TrimSpace(r.Commission.String())
	if raw == "" {
		return 0, errors.New("comission is empty")
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse comission: %w", err)
	}

	return int64(math.Round(value * 100)), nil
}
