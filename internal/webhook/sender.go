package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// Event types for webhook notifications.
const (
	EventSubscriptionActivated = "subscription.activated"
	EventSubscriptionExpired   = "subscription.expired"
	EventSubscriptionUpdated   = "subscription.updated"
)

// Event represents a webhook event payload.
type Event struct {
	EventID           string `json:"event_id"`
	Event             string `json:"event"`
	UserID            string `json:"user_id"`
	Email             string `json:"email"`
	SubscriptionToken string `json:"subscription_token"`
}

// PermanentError indicates a client-side HTTP error (4xx except 429) that
// should not be retried. Transient failures (transport errors, 5xx, 429)
// are returned as regular errors and will be retried.
type PermanentError struct {
	StatusCode int
	Err        error
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("permanent client error: status %d: %v", e.StatusCode, e.Err)
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

// Sender sends webhook events to Proxy Manager with retry logic.
type Sender struct {
	client *http.Client
	url    string
	secret string
}

// NewSender creates a new webhook sender.
// If url is empty, SendAsync will be a no-op and a warning is logged.
func NewSender(url, secret string) *Sender {
	s := &Sender{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		url:    url,
		secret: secret,
	}

	if url == "" {
		logger.Warn("Webhook URL not configured, webhook delivery is disabled")
	} else {
		logger.Info("Webhook sender configured", zap.String("url", url))
	}

	return s
}

// SendAsync sends a webhook event asynchronously with retry logic.
// This method does not block the caller.
// If the URL is not configured, this is a no-op.
func (s *Sender) SendAsync(event Event) {
	if s.url == "" {
		return
	}

	// Capture event by value to avoid data races
	go func(e Event) {
		delays := []time.Duration{0, 1 * time.Second, 5 * time.Second, 15 * time.Second}
		for i, delay := range delays {
			if i > 0 {
				time.Sleep(delay)
				logger.Warn("Retrying webhook delivery",
					zap.String("event_id", e.EventID),
					zap.String("event", e.Event),
					zap.Int("attempt", i+1))
			}
			err := s.send(e)
			if err == nil {
				return
			}
			// Stop retrying on permanent client errors (4xx except 429).
			// Transport errors, 5xx, and 429 are transient and will be retried.
			var permErr *PermanentError
			if errors.As(err, &permErr) {
				logger.Error("Webhook delivery failed with permanent error, not retrying",
					zap.String("event_id", e.EventID),
					zap.String("event", e.Event),
					zap.Int("status_code", permErr.StatusCode),
					zap.String("url", s.url))
				return
			}
		}
		logger.Error("Webhook delivery failed after all retries",
			zap.String("event_id", e.EventID),
			zap.String("event", e.Event),
			zap.String("url", s.url))
	}(event)
}

// send makes a single attempt to deliver the webhook event.
func (s *Sender) send(event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		logger.Warn("Webhook request failed",
			zap.String("event_id", event.EventID),
			zap.Error(err))
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logger.Info("Webhook delivered successfully",
			zap.String("event_id", event.EventID),
			zap.String("event", event.Event),
			zap.Int("status_code", resp.StatusCode))
		return nil
	}

	// Classify non-2xx responses as transient or permanent.
	// 4xx (except 429 Too Many Requests) are permanent client errors —
	// retrying won't fix a bad payload or missing auth.
	// 5xx and 429 are transient — the server may recover or rate limits reset.
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		logger.Warn("Webhook returned permanent client error",
			zap.String("event_id", event.EventID),
			zap.Int("status_code", resp.StatusCode))

		return &PermanentError{
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("webhook returned status %d", resp.StatusCode),
		}
	}

	logger.Warn("Webhook returned transient error",
		zap.String("event_id", event.EventID),
		zap.Int("status_code", resp.StatusCode))

	return fmt.Errorf("webhook returned status %d", resp.StatusCode)
}

// NoopSender is a webhook sender that does nothing (used for tests and when webhook is disabled).
type NoopSender struct{}

func (n *NoopSender) SendAsync(_ Event) {
	// No-op
}
