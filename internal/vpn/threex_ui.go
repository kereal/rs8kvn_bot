package vpn

import (
	"context"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/interfaces"
)

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
func (c *ThreeXUIClient) CreateSubscription(ctx context.Context, uuid, username string) error {
	_, err := c.client.AddClientWithID(ctx, c.inboundIDs, username, uuid, "", 0, time.Time{}, 0)
	if err != nil {
		return fmt.Errorf("3x-ui create subscription: %w", err)
	}
	return nil
}

// DeleteSubscription removes a client from the 3x-ui panel.
func (c *ThreeXUIClient) DeleteSubscription(ctx context.Context, uuid, username string) error {
	if err := c.client.DeleteClient(ctx, username); err != nil {
		return fmt.Errorf("3x-ui delete subscription: %w", err)
	}
	return nil
}

// Close closes the underlying XUI client.
func (c *ThreeXUIClient) Close() error {
	return c.client.Close()
}
