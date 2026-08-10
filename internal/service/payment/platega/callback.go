// Package platega implements the Platega transaction API and callback contract.
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

	"github.com/google/uuid"
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
// It accepts fixed-point notation with at most two fractional digits and rejects
// signs, exponents, malformed values, and int64 overflow.
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

// ParseTransactionID validates a provider transaction identifier as a canonical
// lowercase UUID v4, matching the identifier format used by the provider.
func ParseTransactionID(raw string) (uuid.UUID, error) {
	value := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("transaction ID must be UUID: %w", err)
	}
	if parsed.Version() != uuid.Version(4) {
		return uuid.Nil, fmt.Errorf("transaction ID must be UUID v4")
	}
	if parsed.String() != strings.ToLower(value) {
		return uuid.Nil, fmt.Errorf("transaction ID must use canonical lowercase UUID format")
	}
	return parsed, nil
}

// Validate checks the required callback fields before any order lookup or state
// transition is attempted. paymentMethod remains optional for compatibility with
// provider callbacks that omit it.
func (p CallbackPayload) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("id is required")
	}
	if _, err := ParseTransactionID(p.ID); err != nil {
		return fmt.Errorf("id: %w", err)
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
// Empty configured or received credentials are rejected before comparison.
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
