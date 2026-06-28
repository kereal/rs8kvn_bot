package utils

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"
)

// ErrNon200Response indicates the upstream returned a non-200 status code.
var ErrNon200Response = errors.New("upstream returned non-200")

// IsRetryable determines whether the given error is retryable.
// Retries are allowed only for network-level errors (DNS, timeouts, connection).
// If the server responded (even with an error), retrying is pointless.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNon200Response) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "temporary failure in name resolution") ||
		strings.Contains(msg, "name or service not known") ||
		strings.Contains(msg, "nodename nor servname provided") {
		return false
	}
	return false
}

// RetryWithBackoff executes fn with exponential backoff up to maxRetries attempts.
// It aborts on non-retryable errors or cancelled context.
func RetryWithBackoff(ctx context.Context, maxRetries int, initialDelay time.Duration, fn func() error) error {
	if maxRetries <= 0 {
		return errors.New("maxRetries must be positive")
	}
	if initialDelay <= 0 {
		return errors.New("initialDelay must be positive")
	}

	var lastErr error
	delay := initialDelay

	for attempt := range maxRetries {
		err := fn()
		if err == nil {
			return nil
		}

		if !IsRetryable(err) {
			return fmt.Errorf("non-retryable error: %w", err)
		}

		lastErr = err

		if attempt < maxRetries-1 {
			select {
			case <-time.After(delay + time.Duration(rand.Int63n(int64(delay/2)))): //nolint:gosec
				delay *= 2
			case <-ctx.Done():
				return fmt.Errorf("context cancelled: %w", ctx.Err())
			}
		}
	}

	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}
