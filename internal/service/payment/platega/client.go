// Package platega implements the Platega transaction API and callback contract.
package platega

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"go.uber.org/zap"
)

const defaultBaseURL = "https://app.platega.io"

// Sentinel errors classify provider responses so callers can distinguish
// rejected requests from authentication and transport/provider failures.
var (
	ErrAuth       = errors.New("platega: authentication failed")
	ErrBadRequest = errors.New("platega: bad request")
	ErrProvider   = errors.New("platega: provider error")
)

// Config configures the Platega API client.
type Config struct {
	MerchantID string
	Secret     string
	BaseURL    string
	HTTPClient *http.Client
}

// Client creates transactions through the Platega API using the configured
// HTTP client and merchant credentials.
type Client struct {
	cfg Config
}

// CreateTransactionRequest describes a payment link request.
type CreateTransactionRequest struct {
	AmountCents int64
	Currency    string
	Description string
	ReturnURL   string
	FailedURL   string
	Payload     string
	UserID      string
	UserName    string
}

type transactionRequest struct {
	PaymentDetails paymentDetails `json:"paymentDetails"`
	Description    string         `json:"description"`
	ReturnURL      string         `json:"return"`
	FailedURL      string         `json:"failedUrl"`
	Payload        string         `json:"payload"`
	Metadata       metadata       `json:"metadata"`
}

type paymentDetails struct {
	Amount   json.RawMessage `json:"amount"`
	Currency string          `json:"currency"`
}

type metadata struct {
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
}

// CreateTransactionResponse is returned by Platega after transaction creation.
type CreateTransactionResponse struct {
	TransactionID string `json:"transactionId"`
	Status        string `json:"status"`
	URL           string `json:"url"`
	Redirect      string `json:"redirect"`
	ExpiresIn     string `json:"expiresIn"`
}

// ParseExpiresIn parses Platega's positive HH:MM:SS payment-link lifetime
// into a Go duration used for the local payment deadline.
func ParseExpiresIn(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, errors.New("expiresIn is required")
	}
	parsed, err := time.Parse("15:04:05", value)
	if err != nil {
		return 0, fmt.Errorf("expiresIn must use HH:MM:SS: %w", err)
	}
	if parsed.Hour() == 0 && parsed.Minute() == 0 && parsed.Second() == 0 {
		return 0, errors.New("expiresIn must be positive")
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute + time.Duration(parsed.Second())*time.Second, nil
}

// New creates a Platega client with production-safe defaults.
func New(cfg Config) *Client {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{cfg: cfg}
}

// CreateTransaction creates a payment link without selecting a payment method.
// The response is validated for a UUID v4 transaction ID, a usable URL, and a
// positive expiresIn value before it is returned to the order service.
func (c *Client) CreateTransaction(ctx context.Context, req CreateTransactionRequest) (*CreateTransactionResponse, error) {
	if req.AmountCents <= 0 {
		return nil, errors.New("amount must be positive")
	}
	if strings.TrimSpace(req.Currency) == "" {
		return nil, errors.New("currency is required")
	}

	amount := []byte(fmt.Sprintf("%d.%02d", req.AmountCents/100, req.AmountCents%100))
	body, err := json.Marshal(transactionRequest{
		PaymentDetails: paymentDetails{Amount: json.RawMessage(amount), Currency: req.Currency},
		Description:    req.Description,
		ReturnURL:      req.ReturnURL,
		FailedURL:      req.FailedURL,
		Payload:        req.Payload,
		Metadata:       metadata{UserID: req.UserID, UserName: req.UserName},
	})
	if err != nil {
		return nil, fmt.Errorf("encode transaction request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.BaseURL, "/")+"/v2/transaction/process", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create transaction request: %w", err)
	}
	httpReq.Header.Set("X-Merchantid", c.cfg.MerchantID)
	httpReq.Header.Set("X-Secret", c.cfg.Secret)
	httpReq.Header.Set("Content-Type", "application/json")

	requestStarted := time.Now()
	resp, err := c.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		if logger.Log != nil {
			logger.Info("Payment provider request failed",
				zap.String("provider", "platega"),
				zap.String("operation", "create_transaction"),
				zap.Int("status_code", 0),
				zap.Duration("duration", time.Since(requestStarted)),
			)
		}
		return nil, fmt.Errorf("%w: send transaction request: %w", ErrProvider, err)
	}
	defer func() { _ = resp.Body.Close() }()
	defer func() {
		if logger.Log != nil {
			logger.Info("Payment provider response processed",
				zap.String("provider", "platega"),
				zap.String("operation", "create_transaction"),
				zap.Int("status_code", resp.StatusCode),
				zap.Duration("duration", time.Since(requestStarted)),
			)
		}
	}()

	limited := io.LimitReader(resp.Body, 1<<20)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(limited)
		switch resp.StatusCode {
		case http.StatusBadRequest:
			return nil, fmt.Errorf("%w: %s", ErrBadRequest, strings.TrimSpace(string(message)))
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("%w: %s", ErrAuth, strings.TrimSpace(string(message)))
		default:
			return nil, fmt.Errorf("%w: %s: %s", ErrProvider, resp.Status, strings.TrimSpace(string(message)))
		}
	}

	var result CreateTransactionResponse
	if err := json.NewDecoder(limited).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: decode transaction response: %w", ErrProvider, err)
	}
	transactionID := strings.TrimSpace(result.TransactionID)
	if transactionID == "" {
		return nil, fmt.Errorf("%w: response has no transactionId", ErrProvider)
	}
	if _, err := ParseTransactionID(transactionID); err != nil {
		return nil, fmt.Errorf("%w: response transactionId must be UUID v4: %w", ErrProvider, err)
	}
	if strings.TrimSpace(result.URL) == "" && strings.TrimSpace(result.Redirect) == "" {
		return nil, fmt.Errorf("%w: response has no payment URL", ErrProvider)
	}
	if _, err := ParseExpiresIn(result.ExpiresIn); err != nil {
		return nil, fmt.Errorf("%w: invalid expiresIn: %w", ErrProvider, err)
	}
	return &result, nil
}
