package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/utils"
)

// ProxmanEvent is the JSON payload sent to a proxman node webhook.
type ProxmanEvent struct {
	EventID        string `json:"event_id"`
	Event          string `json:"event"`
	ClientID       string `json:"client_id"`
	Email          string `json:"email"`
	SubscriptionID string `json:"subscription_id"`
}

// ProxmanClient implements vpn.Client for proxman nodes using HTTP webhooks.
type ProxmanClient struct {
	host       string
	apiToken   string
	httpClient *http.Client
}

// NewProxmanClient creates a client that sends HTTP requests to a proxman node.
func NewProxmanClient(host, apiToken string) *ProxmanClient {
	return &ProxmanClient{
		host:       host,
		apiToken:   apiToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreateSubscription sends a subscription.create event to proxman.
func (c *ProxmanClient) CreateSubscription(ctx context.Context, provision SubscriptionProvision) error {
	eventID, err := utils.GenerateUUID()
	if err != nil {
		return fmt.Errorf("generate event id: %w", err)
	}

	event := ProxmanEvent{
		EventID:        eventID,
		Event:          "subscription.create",
		ClientID:       provision.ClientID,
		Email:          provision.Username,
		SubscriptionID: provision.SubID,
	}

	return c.sendEvent(ctx, event)
}

// UpdateSubscription deletes then re-creates the subscription on proxman.
// Proxman does not support update, so delete + create is the idempotent path.
func (c *ProxmanClient) UpdateSubscription(ctx context.Context, provision SubscriptionProvision) error {
	if err := c.DeleteSubscription(ctx, provision); err != nil {
		return fmt.Errorf("proxman update: %w", err)
	}
	return c.CreateSubscription(ctx, provision)
}

// DeleteSubscription sends a subscription.delete event to proxman.
func (c *ProxmanClient) DeleteSubscription(ctx context.Context, provision SubscriptionProvision) error {
	eventID, err := utils.GenerateUUID()
	if err != nil {
		return fmt.Errorf("generate event id: %w", err)
	}

	event := ProxmanEvent{
		EventID:        eventID,
		Event:          "subscription.delete",
		ClientID:       provision.ClientID,
		Email:          provision.Username,
		SubscriptionID: provision.SubID,
	}

	return c.sendEvent(ctx, event)
}

// Close is a no-op for the HTTP proxman client.
func (c *ProxmanClient) Close() error {
	return nil
}

func (c *ProxmanClient) sendEvent(ctx context.Context, event ProxmanEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal proxman event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create proxman request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("proxman request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(respBody))

	switch resp.StatusCode {
	case http.StatusOK:
		if strings.Contains(bodyStr, "duplicate") {
			return nil
		}
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("proxman bad request: %s", bodyStr)
	case http.StatusUnauthorized:
		return fmt.Errorf("proxman unauthorized")
	case http.StatusNotImplemented:
		return fmt.Errorf("proxman not implemented")
	default:
		return fmt.Errorf("proxman unexpected status %d: %s", resp.StatusCode, bodyStr)
	}
}
