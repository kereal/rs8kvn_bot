package platega

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// CallbackPayload is a status notification received from Platega.
type CallbackPayload struct {
	ID            string      `json:"id"`
	Amount        json.Number `json:"amount"`
	Currency      string      `json:"currency"`
	Status        string      `json:"status"`
	PaymentMethod *int        `json:"paymentMethod,omitempty"`
	Payload       string      `json:"payload,omitempty"`
}

// ParseCallbackAmount converts a decimal major-unit amount to integer cents.
func ParseCallbackAmount(amount json.Number) (int64, error) {
	raw := strings.TrimSpace(amount.String())
	if raw == "" {
		return 0, errors.New("amount is required")
	}
	if strings.HasPrefix(raw, "-") || strings.HasPrefix(raw, "+") {
		return 0, errors.New("amount must be non-negative")
	}
	if strings.ContainsAny(raw, "eE") {
		return 0, errors.New("amount must use fixed-point notation")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("amount is invalid")
	}
	if len(parts) == 1 {
		parts = append(parts, "")
	}
	if len(parts[1]) > 2 {
		return 0, errors.New("amount has more than two decimal places")
	}
	if parts[1] == "" {
		parts[1] = "00"
	} else if len(parts[1]) == 1 {
		parts[1] += "0"
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > (math.MaxInt64/100) {
		return 0, errors.New("amount is out of range")
	}
	fraction, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, errors.New("amount is invalid")
	}
	if whole*100 > math.MaxInt64-fraction {
		return 0, errors.New("amount is out of range")
	}
	return whole*100 + fraction, nil
}

// Validate checks fields required to process a callback.
func (p CallbackPayload) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("id is required")
	}
	if _, err := ParseCallbackAmount(p.Amount); err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	if strings.TrimSpace(p.Currency) == "" {
		return errors.New("currency is required")
	}
	if strings.TrimSpace(p.Status) == "" {
		return errors.New("status is required")
	}
	// paymentMethod is documented in examples but is not listed as a required
	// property of the callback schema. Accept callbacks that omit it; status,
	// amount, currency and ID are the fields needed by this integration.
	return nil
}

// VerifyHeaders authenticates callback headers using constant-time comparison.
// Empty credentials are rejected so an unconfigured server never verifies.
func VerifyHeaders(merchantID, secret string, headers http.Header) bool {
	if strings.TrimSpace(merchantID) == "" || strings.TrimSpace(secret) == "" {
		return false
	}
	gotMerchant := headers.Get("X-MerchantId")
	gotSecret := headers.Get("X-Secret")
	if strings.TrimSpace(gotMerchant) == "" || strings.TrimSpace(gotSecret) == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(merchantID), []byte(gotMerchant)) == 1 &&
		subtle.ConstantTimeCompare([]byte(secret), []byte(gotSecret)) == 1
}
