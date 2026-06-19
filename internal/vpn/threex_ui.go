package vpn

import (
	"context"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/interfaces"
)

var _ Client = (*ThreeXUIClient)(nil)

// ThreeXUIClient adapts an xui.Client to the VPNClient interface.
type ThreeXUIClient struct {
	client      interfaces.XUIClient
	inboundIDs  []int
}

// NewThreeXUIClient wraps the provided XUI client with inbound IDs.
func NewThreeXUIClient(client interfaces.XUIClient, inboundIDs []int) *ThreeXUIClient {
	return &ThreeXUIClient{client: client, inboundIDs: inboundIDs}
}

// CreateSubscription adds a client on the 3x-ui panel.
func (c *ThreeXUIClient) CreateSubscription(ctx context.Context, provision SubscriptionProvision) error {
	_, err := c.client.AddClientWithID(
		ctx,
		c.inboundIDs,
		provision.Username,
		provision.ClientID,
		provision.SubID,
		provision.TrafficBytes,
		provision.ExpiryTime,
		provision.ResetDays,
	)
	if err != nil {
		return fmt.Errorf("3x-ui create subscription: %w", err)
	}
	return nil
}

// DeleteSubscription removes a client from the 3x-ui panel.
func (c *ThreeXUIClient) DeleteSubscription(ctx context.Context, provision SubscriptionProvision) error {
	if err := c.client.DeleteClient(ctx, provision.Username); err != nil {
		return fmt.Errorf("3x-ui delete subscription: %w", err)
	}
	return nil
}

// Close closes the underlying XUI client.
func (c *ThreeXUIClient) Close() error {
	return c.client.Close()
}
