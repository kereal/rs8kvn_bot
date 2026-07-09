package vpn

import (
	"context"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/xui"
)

var _ Client = (*ThreeXUIClient)(nil)

// ThreeXUIClient adapts an xui.Client to the Client interface.
type ThreeXUIClient struct {
	client     interfaces.XUIClient
	inboundIDs []int
}

// NewThreeXUIClient wraps the provided XUI client with inbound IDs.
func NewThreeXUIClient(client interfaces.XUIClient, inboundIDs []int) *ThreeXUIClient {
	return &ThreeXUIClient{client: client, inboundIDs: inboundIDs}
}

// CreateSubscription adds a client on the 3x-ui panel.
func (c *ThreeXUIClient) CreateSubscription(ctx context.Context, provision SubscriptionProvision) error {
	_, err := c.client.AddClientWithID(ctx, xui.ClientRequest{
		InboundIDs:   c.inboundIDs,
		Email:        provision.Username,
		ClientID:     provision.ClientID,
		SubID:        provision.SubID,
		TrafficBytes: provision.TrafficBytes,
		ExpiryTime:   provision.ExpiryTime,
		ResetDays:    provision.ResetDays,
	})
	if err != nil {
		return fmt.Errorf("3x-ui create subscription: %w", classifyCreateSubscriptionError(err))
	}
	return nil
}

// UpdateSubscription updates an existing client on the 3x-ui panel.
func (c *ThreeXUIClient) UpdateSubscription(ctx context.Context, provision SubscriptionProvision) error {
	err := c.client.UpdateClient(ctx, xui.ClientRequest{
		InboundIDs:   c.inboundIDs,
		CurrentEmail: provision.Username,
		ClientID:     provision.ClientID,
		Email:        provision.Username,
		SubID:        provision.SubID,
		TrafficBytes: provision.TrafficBytes,
		ExpiryTime:   provision.ExpiryTime,
		ResetDays:    provision.ResetDays,
	})
	if err != nil {
		return fmt.Errorf("3x-ui update subscription: %w", classifyCreateSubscriptionError(err))
	}
	return nil
}

// DeleteSubscription removes a client from the 3x-ui panel.
func (c *ThreeXUIClient) DeleteSubscription(ctx context.Context, provision SubscriptionProvision) error {
	if err := c.client.DeleteClient(ctx, provision.Username); err != nil {
		return fmt.Errorf("3x-ui delete subscription: %w", classifyDeleteSubscriptionError(err))
	}
	return nil
}

// Close closes the underlying XUI client.
func (c *ThreeXUIClient) Close() error {
	return c.client.Close()
}
